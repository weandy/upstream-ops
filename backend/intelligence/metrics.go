package intelligence

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	// TrendBucketCount 趋势条时间桶个数（近 90 分钟，每分钟 1 桶）。
	TrendBucketCount = 90
)

// PerformanceSummary 24 小时主动测活性能汇总。
type PerformanceSummary struct {
	Window          string  `json:"window"`
	SampleCount     int     `json:"sample_count"`
	SuccessCount    int     `json:"success_count"`
	SuccessRate     float64 `json:"success_rate"`
	AvgFirstTokenMS int64   `json:"avg_first_token_ms"`
	AvgLatencyMS    int64   `json:"avg_latency_ms"`
	TPS             float64 `json:"tps"`
}

// AvailabilityBucket 单分钟可用率趋势桶。
type AvailabilityBucket struct {
	Timestamp    time.Time `json:"timestamp"`
	SampleCount  int       `json:"sample_count"`
	SuccessCount int       `json:"success_count"`
	SuccessRate  float64   `json:"success_rate"`
	Tone         string    `json:"tone"` // ok | warn | down | none
}

// AggregateLiveness 根据给定时间窗口内的主动测活样本计算 24h 性能汇总与近 90 分钟趋势条。
// 纯内存计算，跨 SQLite / MySQL / PostgreSQL 零方言差异。
func AggregateLiveness(samples []storage.ProbeLivenessSample, now time.Time) (PerformanceSummary, []AvailabilityBucket) {
	nowUTC := now.UTC()
	cutoff24h := nowUTC.Add(-24 * time.Hour)
	currentMinute := nowUTC.Truncate(time.Minute)
	startMinute := currentMinute.Add(-time.Duration(TrendBucketCount-1) * time.Minute)

	buckets := make([]AvailabilityBucket, TrendBucketCount)
	for i := 0; i < TrendBucketCount; i++ {
		buckets[i] = AvailabilityBucket{
			Timestamp: startMinute.Add(time.Duration(i) * time.Minute),
			Tone:      "none",
		}
	}

	var (
		total24h       int
		success24h     int
		ttftSum        int64
		ttftCount      int64
		latencySum     int64
		latencyCount   int64
		weightedTokens float64
		weightedGenSec float64
	)

	for _, s := range samples {
		ts := s.ObservedAt.UTC()
		if !ts.Before(cutoff24h) && !ts.After(nowUTC.Add(time.Minute)) {
			total24h++
			if s.Success {
				success24h++
				if s.FirstTokenMS > 0 {
					ttftSum += s.FirstTokenMS
					ttftCount++
				}
				if s.LatencyMS > 0 {
					latencySum += s.LatencyMS
					latencyCount++
				}
				if s.OutputTokens > 0 && s.LatencyMS > 0 {
					genMS := s.LatencyMS - s.FirstTokenMS
					if genMS <= 0 {
						genMS = s.LatencyMS
					}
					weightedTokens += float64(s.OutputTokens)
					weightedGenSec += float64(genMS) / 1000.0
				}
			}
		}

		minute := ts.Truncate(time.Minute)
		idx := int(minute.Sub(startMinute) / time.Minute)
		if idx >= 0 && idx < TrendBucketCount {
			buckets[idx].SampleCount++
			if s.Success {
				buckets[idx].SuccessCount++
			}
		}
	}

	perf := PerformanceSummary{
		Window:       "24h",
		SampleCount:  total24h,
		SuccessCount: success24h,
	}
	if total24h > 0 {
		perf.SuccessRate = round2(float64(success24h) * 100.0 / float64(total24h))
	}
	if ttftCount > 0 {
		perf.AvgFirstTokenMS = ttftSum / ttftCount
	}
	if latencyCount > 0 {
		perf.AvgLatencyMS = latencySum / latencyCount
	}
	if weightedGenSec > 0 && weightedTokens > 0 {
		perf.TPS = round2(weightedTokens / weightedGenSec)
	}

	for i := range buckets {
		if buckets[i].SampleCount == 0 {
			buckets[i].Tone = "none"
			continue
		}
		rate := float64(buckets[i].SuccessCount) * 100.0 / float64(buckets[i].SampleCount)
		buckets[i].SuccessRate = round2(rate)
		switch {
		case rate >= 90.0:
			buckets[i].Tone = "ok"
		case rate >= 50.0:
			buckets[i].Tone = "warn"
		default:
			buckets[i].Tone = "down"
		}
	}

	return perf, buckets
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// PerformanceTrendPoint 分桶性能趋势单点（用于 6h / 24h / 7d 折线图与多目标对比）。
type PerformanceTrendPoint struct {
	Timestamp       time.Time `json:"timestamp"`
	SampleCount     int       `json:"sample_count"`
	SuccessCount    int       `json:"success_count"`
	SuccessRate     float64   `json:"success_rate"`
	AvgFirstTokenMS int64     `json:"avg_first_token_ms"`
	P95FirstTokenMS int64     `json:"p95_first_token_ms"`
	AvgLatencyMS    int64     `json:"avg_latency_ms"`
	TPS             float64   `json:"tps"`
}

// PerformanceTrendSeries 单目标分桶性能趋势响应。
type PerformanceTrendSeries struct {
	TargetID      uint                    `json:"target_id"`
	Window        string                  `json:"window"`
	BucketMinutes int                     `json:"bucket_minutes"`
	Points        []PerformanceTrendPoint `json:"points"`
}

// NormalizeTrendWindow 解析趋势窗口并返回规范窗口名、总时长、单桶时长和桶数。
func NormalizeTrendWindow(raw string) (window string, span time.Duration, bucket time.Duration, count int) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "6h":
		return "6h", 6 * time.Hour, 10 * time.Minute, 36
	case "7d":
		return "7d", 7 * 24 * time.Hour, 3 * time.Hour, 56
	default:
		return "24h", 24 * time.Hour, 30 * time.Minute, 48
	}
}

// AggregatePerformanceTrend 按指定时间窗口将主动测活样本聚合成对齐的分桶时间序列（纯 Go 内存计算，跨库零方言差异）。
func AggregatePerformanceTrend(samples []storage.ProbeLivenessSample, windowRaw string, now time.Time) (string, int, []PerformanceTrendPoint) {
	window, _, bucketDur, count := NormalizeTrendWindow(windowRaw)
	nowUTC := now.UTC()
	endBucket := nowUTC.Truncate(bucketDur)
	startBucket := endBucket.Add(-time.Duration(count-1) * bucketDur)

	type accum struct {
		samples        int
		success        int
		ttftValues     []int64
		ttftSum        int64
		latencySum     int64
		latencyCount   int64
		weightedTokens float64
		weightedGenSec float64
	}
	buckets := make([]accum, count)

	for _, s := range samples {
		ts := s.ObservedAt.UTC().Truncate(bucketDur)
		idx := int(ts.Sub(startBucket) / bucketDur)
		if idx < 0 || idx >= count {
			continue
		}
		b := &buckets[idx]
		b.samples++
		if !s.Success {
			continue
		}
		b.success++
		if s.FirstTokenMS > 0 {
			b.ttftSum += s.FirstTokenMS
			b.ttftValues = append(b.ttftValues, s.FirstTokenMS)
		}
		if s.LatencyMS > 0 {
			b.latencySum += s.LatencyMS
			b.latencyCount++
		}
		if s.OutputTokens > 0 && s.LatencyMS > 0 {
			genMS := s.LatencyMS - s.FirstTokenMS
			if genMS <= 0 {
				genMS = s.LatencyMS
			}
			b.weightedTokens += float64(s.OutputTokens)
			b.weightedGenSec += float64(genMS) / 1000.0
		}
	}

	points := make([]PerformanceTrendPoint, count)
	for i := 0; i < count; i++ {
		b := &buckets[i]
		pt := PerformanceTrendPoint{
			Timestamp:    startBucket.Add(time.Duration(i) * bucketDur),
			SampleCount:  b.samples,
			SuccessCount: b.success,
		}
		if b.samples > 0 {
			pt.SuccessRate = round2(float64(b.success) * 100.0 / float64(b.samples))
		}
		if len(b.ttftValues) > 0 {
			pt.AvgFirstTokenMS = b.ttftSum / int64(len(b.ttftValues))
			pt.P95FirstTokenMS = percentileInt64(b.ttftValues, 0.95)
		}
		if b.latencyCount > 0 {
			pt.AvgLatencyMS = b.latencySum / b.latencyCount
		}
		if b.weightedGenSec > 0 && b.weightedTokens > 0 {
			pt.TPS = round2(b.weightedTokens / b.weightedGenSec)
		}
		points[i] = pt
	}
	return window, int(bucketDur / time.Minute), points
}

func percentileInt64(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	cp := make([]int64, len(values))
	copy(cp, values)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	if p <= 0 {
		return cp[0]
	}
	if p >= 1 {
		return cp[len(cp)-1]
	}
	idx := int(math.Ceil(float64(len(cp))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

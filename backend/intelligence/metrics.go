package intelligence

import (
	"math"
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

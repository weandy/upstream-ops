package siteops

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

var (
	locUTC8             = time.FixedZone("UTC+8", 8*3600)
	reportEmailPattern  = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
)

func sanitizeReportText(raw string, maxRunes int) string {
	cleaned := reportEmailPattern.ReplaceAllString(raw, "[redacted]")
	return SanitizeErrorText(cleaned, maxRunes)
}

const (
	dailySLANoteWeighted = "按昨日 24h 小时桶请求量加权聚合（非 1h SLA 算术平均）"
	dailySLANoteMissing  = "缺失（昨日无有效可信请求样本；采用昨日 24h 小时桶请求量加权聚合，非 1h SLA 算术平均）"
	dailyLatencyMissing  = "缺失（未采集上游 24h 延迟分位统计，不以 1h P95 平均冒充）"
	dailyLatencyPartial  = "部分缺失（未采集到的上游 24h 延迟分位统计不以 1h P95 平均冒充）"
	dailyLatencyPresent  = "来自上游 24h 延迟分位统计"
)

// DailyReportInput 24h 运维日报聚合输入（P1-A 仅消费运维可观测数据，默认零 PII）。
type DailyReportInput struct {
	Instance                 storage.SiteOpsInstance
	Now                      time.Time
	Snapshots                []storage.SiteOpsSnapshot
	ActiveEvents             []storage.SiteOpsEvent
	Upstream24hTTFTP95MS     *int
	Upstream24hDurationP95MS *int
	IncludeMaskedSummary     bool
	PoolForecast             *PoolForecastResponse
}

// YesterdayWindowUTC8 精确计算昨日 UTC+8 的自然日窗口 [00:00:00, 24:00:00) 及其 UTC 边界。
func YesterdayWindowUTC8(now time.Time) (startUTC, endUTC time.Time, dateLabel string) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowUTC8 := now.In(locUTC8)
	todayStart := time.Date(nowUTC8.Year(), nowUTC8.Month(), nowUTC8.Day(), 0, 0, 0, 0, locUTC8)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	return yesterdayStart.UTC(), todayStart.UTC(), yesterdayStart.Format("2006-01-02")
}

// BuildDailyReport 生成昨日 24h 同窗聚合的站点运维日报预览。
// 严格遵守：
//   - 24h SLA 按昨日自然小时桶请求量加权聚合，绝不对 1h SLA 求算术平均；
//   - 24h P95 TTFT / 总耗时未提供时显式为 nil 并标注「缺失」；
//   - 最高并发水位仅统计 trusted 样本，绝不计入 unverified_zero / stale；
//   - 默认纯聚合零 PII（不含邮箱、用户名或密钥）。
func BuildDailyReport(in DailyReportInput) DailyReportPreview {
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	startUTC, endUTC, dateLabel := YesterdayWindowUTC8(now)

	sorted := append([]storage.SiteOpsSnapshot(nil), in.Snapshots...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ObservedAt.Before(sorted[j].ObservedAt)
	})

	metrics := DailyReportMetrics{
		WindowStartUTC8: startUTC.In(locUTC8),
		WindowEndUTC8:   endUTC.In(locUTC8),
		SLANote:         dailySLANoteMissing,
		Latency24hNote:  dailyLatencyMissing,
	}

	type hourBucket struct {
		observedAt time.Time
		reqTotal   int64
		errTotal   int64
		valid      bool
	}
	var buckets [24]hourBucket

	var latestAvailSnap *storage.SiteOpsSnapshot
	for i := range sorted {
		snap := &sorted[i]
		fq := parseSnapshotFieldQuality(snap.FieldQualityJSON)

		// 当前账号总数与异常账号数取最新一份可信样本。
		if fq[DomainAccountAvailability] == QualityTrusted &&
			(snap.AccountsTotal != nil || snap.AccountsError != nil) {
			latestAvailSnap = snap
		}

		// 仅昨日 UTC+8 窗口内快照参与 24h 日报聚合。
		obsUTC := snap.ObservedAt.UTC()
		if obsUTC.Before(startUTC) || !obsUTC.Before(endUTC) {
			continue
		}

		metrics.SampleCount++
		if snap.Reachable {
			metrics.ReachableSampleCount++
		}

		// 1. 昨日 24h 小时桶请求量加权 SLA（每自然小时取最新一份 trusted 快照作为该小时代表）
		if fq[DomainOpsOverview] == QualityTrusted && snap.ReqTotal1h != nil && snap.ErrTotal1h != nil {
			hour := obsUTC.In(locUTC8).Hour()
			if hour >= 0 && hour < 24 && (!buckets[hour].valid || !obsUTC.Before(buckets[hour].observedAt)) {
				req := *snap.ReqTotal1h
				errCnt := *snap.ErrTotal1h
				if req < 0 {
					req = 0
				}
				if errCnt < 0 {
					errCnt = 0
				}
				if errCnt > req {
					errCnt = req
				}
				buckets[hour] = hourBucket{
					observedAt: obsUTC,
					reqTotal:   req,
					errTotal:   errCnt,
					valid:      true,
				}
			}
		}

		// 2. 昨日最高并发水位（严禁计入 unverified_zero / stale）
		concTrusted := snap.ConcTrustState == string(QualityTrusted)
		if concTrusted {
			if st, exists := fq[DomainConcurrency]; exists && st != "" && st != QualityTrusted {
				concTrusted = false
			}
		}
		if concTrusted && snap.ConcUsed != nil {
			used := *snap.ConcUsed
			if metrics.PeakConcUsed == nil || used > *metrics.PeakConcUsed {
				metrics.PeakConcUsed = ptr(used)
				if snap.ConcMax != nil {
					metrics.PeakConcMax = ptr(*snap.ConcMax)
				} else {
					metrics.PeakConcMax = nil
				}
			}
		}

		// 3. 昨日累计切号与 1m 峰值（仅统计 trusted infra_metrics）
		if fq[DomainInfraMetrics] == QualityTrusted && snap.AccountSwitchCount1m != nil && *snap.AccountSwitchCount1m >= 0 {
			sw := *snap.AccountSwitchCount1m
			metrics.TotalSwitches1m += sw
			if sw > metrics.PeakSwitches1m {
				metrics.PeakSwitches1m = sw
			}
		}
	}

	// 汇总 24h 小时桶请求量与错误量，计算加权 SLA。
	var sumReq, sumErr int64
	validBuckets := 0
	for _, b := range buckets {
		if !b.valid {
			continue
		}
		validBuckets++
		sumReq += b.reqTotal
		sumErr += b.errTotal
	}
	if validBuckets > 0 {
		metrics.ReqTotal24h = ptr(sumReq)
		metrics.ErrTotal24h = ptr(sumErr)
		if sumReq > 0 {
			sla := math.Round(float64(sumReq-sumErr)/float64(sumReq)*10000) / 100
			metrics.SLAPercent24h = &sla
			metrics.SLANote = dailySLANoteWeighted
		}
	}

	// 24h P95 TTFT / 总耗时（严格不以 1h P95 平均冒充）
	if in.Upstream24hTTFTP95MS != nil {
		v := *in.Upstream24hTTFTP95MS
		metrics.TTFTP95MS24h = &v
	}
	if in.Upstream24hDurationP95MS != nil {
		v := *in.Upstream24hDurationP95MS
		metrics.DurationP95MS24h = &v
	}
	switch {
	case metrics.TTFTP95MS24h != nil && metrics.DurationP95MS24h != nil:
		metrics.Latency24hNote = dailyLatencyPresent
	case metrics.TTFTP95MS24h != nil || metrics.DurationP95MS24h != nil:
		metrics.Latency24hNote = dailyLatencyPartial
	default:
		metrics.Latency24hNote = dailyLatencyMissing
	}

	// 当前账号状态摘要
	if latestAvailSnap != nil {
		if latestAvailSnap.AccountsTotal != nil {
			metrics.CurrentAccountsTotal = ptr(*latestAvailSnap.AccountsTotal)
		}
		if latestAvailSnap.AccountsError != nil {
			metrics.CurrentAccountsError = ptr(*latestAvailSnap.AccountsError)
		}
	}

	// 筛选未恢复告警
	firing := make([]storage.SiteOpsEvent, 0, len(in.ActiveEvents))
	for _, evt := range in.ActiveEvents {
		if evt.State == EventStateFiring {
			firing = append(firing, evt)
		}
	}
	metrics.ActiveFiringAlerts = len(firing)

	instName := sanitizeReportText(in.Instance.Name, 80)
	if instName == "" {
		instName = fmt.Sprintf("instance#%d", in.Instance.ID)
	}
	subject := fmt.Sprintf("[站点运维日报] %s · %s", instName, dateLabel)
	body := formatDailyReportBody(instName, dateLabel, metrics, firing, in.PoolForecast)

	return DailyReportPreview{
		InstanceID:   in.Instance.ID,
		InstanceName: instName,
		DateUTC8:     dateLabel,
		GeneratedAt:  now,
		Subject:      subject,
		Body:         body,
		Metrics:      metrics,
	}
}

func parseSnapshotFieldQuality(raw string) FieldQualityMap {
	out := make(FieldQualityMap)
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return out
	}
	_ = json.Unmarshal([]byte(trimmed), &out)
	return out
}

func formatDailyReportBody(instName, dateLabel string, m DailyReportMetrics, firing []storage.SiteOpsEvent, pf *PoolForecastResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "站点运维日报：%s（%s，UTC+8 00:00–24:00）\n", instName, dateLabel)
	fmt.Fprintf(&b, "- 采样可达：%d / %d\n", m.ReachableSampleCount, m.SampleCount)

	if m.SLAPercent24h != nil && m.ReqTotal24h != nil && m.ErrTotal24h != nil {
		fmt.Fprintf(&b, "- 昨日 24h SLA：%.2f%%（总请求 %d，错误 %d；%s）\n",
			*m.SLAPercent24h, *m.ReqTotal24h, *m.ErrTotal24h, m.SLANote)
	} else {
		fmt.Fprintf(&b, "- 昨日 24h SLA：%s\n", m.SLANote)
	}

	ttftStr := "缺失"
	if m.TTFTP95MS24h != nil {
		ttftStr = fmt.Sprintf("%dms", *m.TTFTP95MS24h)
	}
	durStr := "缺失"
	if m.DurationP95MS24h != nil {
		durStr = fmt.Sprintf("%dms", *m.DurationP95MS24h)
	}
	latencyReason := m.Latency24hNote
	switch latencyReason {
	case dailyLatencyMissing:
		latencyReason = "未采集上游 24h 延迟分位统计，不以 1h P95 平均冒充"
	case dailyLatencyPartial:
		latencyReason = "未采集到的上游 24h 延迟分位统计不以 1h P95 平均冒充"
	}
	fmt.Fprintf(&b, "- 昨日 24h 延迟 P95：TTFT %s / 总耗时 %s（%s）\n", ttftStr, durStr, latencyReason)

	if m.PeakConcUsed != nil {
		if m.PeakConcMax != nil && *m.PeakConcMax > 0 {
			pct := float64(*m.PeakConcUsed) / float64(*m.PeakConcMax) * 100
			fmt.Fprintf(&b, "- 昨日最高并发水位：%d / %d（%.1f%%，仅统计可信样本）\n", *m.PeakConcUsed, *m.PeakConcMax, pct)
		} else {
			fmt.Fprintf(&b, "- 昨日最高并发水位：%d（仅统计可信样本）\n", *m.PeakConcUsed)
		}
	} else {
		b.WriteString("- 昨日最高并发水位：无可用可信样本\n")
	}

	fmt.Fprintf(&b, "- 昨日切号统计：累计 %d 次，单分钟峰值 %d 次\n", m.TotalSwitches1m, m.PeakSwitches1m)

	if m.CurrentAccountsTotal != nil && m.CurrentAccountsError != nil {
		fmt.Fprintf(&b, "- 当前账号异常数：%d / %d\n", *m.CurrentAccountsError, *m.CurrentAccountsTotal)
	} else if m.CurrentAccountsError != nil {
		fmt.Fprintf(&b, "- 当前账号异常数：%d\n", *m.CurrentAccountsError)
	} else {
		b.WriteString("- 当前账号异常数：无可用可信样本\n")
	}

	fmt.Fprintf(&b, "- 当前未恢复告警：%d 条", m.ActiveFiringAlerts)
	if len(firing) > 0 {
		b.WriteByte('\n')
		limit := len(firing)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			evt := firing[i]
			title := sanitizeReportText(evt.Title, 120)
			if title == "" {
				title = evt.RuleKey
			}
			res := sanitizeReportText(evt.ResourceKey, 64)
			if res == "" {
				res = "site"
			}
			fmt.Fprintf(&b, "  • [%s] %s（%s）\n", evt.Severity, title, res)
		}
		if len(firing) > limit {
			fmt.Fprintf(&b, "  • 其余 %d 条未恢复告警已省略\n", len(firing)-limit)
		}
	}

	if pf != nil && pf.ProvenCount > 0 {
		if len(firing) == 0 {
			b.WriteByte('\n')
		}
		var riskGroups []string
		for _, g := range pf.Groups {
			if !g.CannotSustainRisk {
				continue
			}
			label := sanitizeReportText(g.GroupName, 64)
			if label == "" {
				label = fmt.Sprintf("group:%d", g.GroupID)
			}
			riskGroups = append(riskGroups, label)
		}
		fmt.Fprintf(&b, "- 号池周期预测风险（被动证实 %d 号）：已耗尽 %d、高危提前耗尽(will_exhaust_high) %d、临界提前耗尽(will_exhaust) %d",
			pf.ProvenCount, pf.ExhaustedCount, pf.WillExhaustHighCount, pf.WillExhaustCount)
		if len(riskGroups) > 0 {
			display := riskGroups
			suffix := ""
			if len(display) > 8 {
				display = display[:8]
				suffix = fmt.Sprintf(" 等 %d 个高危分组", len(riskGroups))
			}
			fmt.Fprintf(&b, "；高危分组：%s%s", strings.Join(display, "、"), suffix)
		} else {
			b.WriteString("；高危分组：无")
		}
	}

	return strings.TrimSpace(b.String())
}

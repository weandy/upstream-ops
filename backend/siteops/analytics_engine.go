package siteops

import (
	"math"
	"time"
)

func safeFinitePtr(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
func rounded(v, scale float64) *float64 { return safeFinitePtr(math.Round(v*scale) / scale) }
func costSpread(a, b *float64) bool {
	return a != nil && b != nil && math.Abs(*a-*b) > max(.01, 1e-6*max(*a, *b))
}
func cacheMetrics(i, o, w, r, t *int64) CacheMetricsView {
	v := CacheMetricsView{InputTokens: i, OutputTokens: o, CacheCreationTokens: w, CacheReadTokens: r, TotalTokens: t, InputMode: "unverified", TokenQuality: QualityUnavailable}
	if t == nil || *t < 0 {
		return v
	}
	var sum int64
	for _, n := range []*int64{i, o, w, r} {
		if n == nil || *n < 0 || *n > math.MaxInt64-sum {
			return v
		}
		sum += *n
	}
	tol := max(int64(10), *t/100)
	near := func(n int64) bool { return *t-n <= tol && n-*t <= tol }
	var effective int64
	switch {
	case sum == 0 && *t == 0 || *t > 0 && near(sum):
		effective = sum - *o
		v.InputMode = "exclusive"
	case *t > 0 && *w+*r > 0 && *i >= *w+*r && near(*i+*o):
		effective = *i
		v.InputMode = "inclusive_detected"
	default:
		return v
	}
	v.TokenQuality = QualityTrusted
	v.EffectiveInput = &effective
	if effective > 0 {
		v.CacheReadHitPct = rounded(100*float64(*r)/float64(effective), 100)
		v.CacheWritePct = rounded(100*float64(*w)/float64(effective), 100)
	}
	if *w > 0 {
		v.CacheRWRatio = rounded(float64(*r)/float64(*w), 100)
	}
	return v
}
func priceCompare(p *ReferencePriceView, cfg AnalyticsConfig, cycle, term time.Duration, unique bool, actual, forecast *float64) SubscriptionPricingCompare {
	v := SubscriptionPricingCompare{PriceSource: "none", MarginRisk: "unconfigured"}
	if p == nil {
		return v
	}
	v.ReferencePrice = p
	v.PriceSource = p.Scope
	v.EffectiveFX = 1
	if p.Currency == "CNY" {
		v.EffectiveFX = cfg.CNYPerUSD
		if p.CNYPerUSDMicros != nil {
			v.EffectiveFX = float64(*p.CNYPerUSDMicros) / 1e6
		}
	}
	v.MarginRisk = "unknown"
	if cycle > 0 && v.EffectiveFX > 0 {
		v.Proration = ptr(float64(cycle) / float64(cycleDuration))
		v.CycleRefNative = rounded(float64(p.MonthlyMinor)/100*(*v.Proration), 100)
		v.CycleRefUSD = rounded(*v.CycleRefNative/v.EffectiveFX, 10000)
		native := rounded(float64(p.MonthlyMinor)/100*float64(term)/float64(cycleDuration), 100)
		v.TermRefUSD = rounded(*native/v.EffectiveFX, 10000)
	}
	if p.MonthlyMinor == 0 {
		v.MarginRisk = "free"
		return v
	}
	if !unique || actual == nil || v.CycleRefUSD == nil || *v.CycleRefUSD <= 0 {
		return v
	}
	v.ActualLeverageX = rounded(*actual / *v.CycleRefUSD, 100)
	if forecast != nil {
		v.ProjectedLeverageX = rounded(*forecast / *v.CycleRefUSD, 100)
	}
	if cfg.LeverageWarnThreshold == 0 && cfg.LeverageDangerThreshold == 0 {
		v.MarginRisk = "threshold_disabled"
		return v
	}
	peak := max(val(v.ActualLeverageX), val(v.ProjectedLeverageX))
	switch {
	case cfg.LeverageDangerThreshold > 0 && peak >= cfg.LeverageDangerThreshold:
		v.MarginRisk = "danger"
	case cfg.LeverageWarnThreshold > 0 && peak >= cfg.LeverageWarnThreshold:
		v.MarginRisk = "warn"
	default:
		v.MarginRisk = "normal"
	}
	return v
}
func val[T ~int | ~int64 | ~float64](p *T) T {
	if p == nil {
		return -1
	}
	return *p
}
func daySH(t time.Time) time.Time { return nextMidnight(t).AddDate(0, 0, -1) }
func dateRange(a, b time.Time) AnalyticsDateRange {
	return AnalyticsDateRange{a.In(locUTC8).Format(time.DateOnly), b.In(locUTC8).Format(time.DateOnly), "Asia/Shanghai"}
}
func activeSub(s SanitizedSubscription, n time.Time) bool {
	return s.Status == "active" && s.StartsAt != nil && !s.StartsAt.After(n) && s.ExpiresAt != nil && s.ExpiresAt.After(n)
}
func userTags(u AnalyticsUserRow, subs []SanitizedSubscription, complete bool, now time.Time) []OperationalTagView {
	tags := []OperationalTagView{}
	add := func(key, kind, label, severity, reason string) {
		tags = append(tags, OperationalTagView{key, kind, label, severity, reason})
	}
	all, week := u.AllTime, u.Last7d
	history := val(u.RecentAggregateActualUSD)
	if all != nil && all.ActualUSD != nil {
		history = *all.ActualUSD
	}
	var age time.Duration
	if u.CreatedAt != nil {
		age = now.Sub(*u.CreatedAt)
	}
	expiring, riskExpiry, expired, active := false, false, false, false
	for _, s := range subs {
		if s.UserID != u.UserID {
			continue
		}
		if activeSub(s, now) {
			active = true
			left := s.ExpiresAt.Sub(now)
			expiring = expiring || left <= 7*24*time.Hour
			riskExpiry = riskExpiry || left <= 5*24*time.Hour
			continue
		}
		end := s.ExpiresAt
		if s.Status == "revoked" || s.Status == "cancelled" {
			end = s.RevokedAt
		}
		if end == nil || end.After(now) || now.Sub(*end) > 30*24*time.Hour || s.Status == "" {
			continue
		}
		renewed := false
		for _, other := range subs {
			if other.ID != s.ID && other.UserID == s.UserID && other.GroupID == s.GroupID && other.Status == "active" && other.ExpiresAt != nil && other.ExpiresAt.After(now) {
				renewed = true
				break
			}
		}
		expired = expired || !renewed
	}
	risk := riskExpiry
	if all != nil && week != nil {
		if age >= 14*24*time.Hour && all.ActualUSD != nil && week.ActualUSD != nil && *week.ActualUSD >= 0 {
			days := max(1, daySH(now).AddDate(0, 0, -6).Sub(daySH(*u.CreatedAt)).Hours()/24)
			prior := (*all.ActualUSD - *week.ActualUSD) / days
			risk = risk || (prior >= 2 && *week.ActualUSD/7 <= .35*prior)
		}
		risk = risk || val(all.Requests) >= 20 && val(week.Requests) >= 20 && val(all.AverageDurationMS) >= 0 && val(week.AverageDurationMS) >= 8000 && val(week.AverageDurationMS) >= 1.8*val(all.AverageDurationMS)
	}
	if history >= 50 && risk {
		add("high_value_at_risk", "", "高危回访", "critical", "高消费用户近期活跃度、延迟或续费时间触及阈值")
	}
	if expiring {
		add("expiring_soon", "", "临期续费", "warning", "订阅将在七天内到期")
	}
	if complete && expired && history > 0 {
		add("expired_unrenewed", "", "已到期未续费", "warning", "最近三十天同组无接续订阅")
	}
	if week != nil && week.Cache.TokenQuality == QualityTrusted && val(week.Requests) >= 20 && val(week.Cache.EffectiveInput) >= 100000 && week.Cache.CacheReadHitPct != nil {
		hit := *week.Cache.CacheReadHitPct
		drop := all != nil && all.StartDate < week.StartDate && all.Cache.TokenQuality == QualityTrusted && val(all.Requests) >= 20 && val(all.Cache.EffectiveInput) >= 100000 && val(all.Cache.CacheReadHitPct) >= 40 && val(all.Cache.CacheReadHitPct)-hit >= 20
		if drop {
			reason := "命中率下降至少二十个百分点"
			if hit < 30 {
				reason += "，且低于 30%"
			}
			add("cache_anomaly", "cache_drop", "缓存骤降", "warning", reason)
		} else if hit < 30 {
			add("cache_anomaly", "low_cache_hit", "缓存偏低", "warning", "命中率低于 30%")
		}
	}
	if c := u.LiveConcurrency; c != nil && (val(c.WaitingInQueue) > 0 || val(c.MaxCapacity) > 0 && 100*float64(val(c.CurrentInUse))/float64(*c.MaxCapacity) >= 85) {
		add("concurrency_queued", "", "并发排队", "warning", "存在排队或负载达到 85%")
	}
	if u.Status == "active" && val(u.BalanceUSD) >= 20 && age >= 7*24*time.Hour && val(u.TodayActualUSD) == 0 && (week != nil && week.Requests != nil && *week.Requests <= 2 && week.ActualUSD != nil && *week.ActualUSD < .1 || week == nil && !active && complete && val(u.RecentAggregateActualUSD) == 0) {
		add("dormant_high_balance", "", "休眠高余额", "info", "余额较高且近期使用很少")
	}
	return tags
}

// userActivityState 返回 4 个互斥、直白的客户活跃度状态：drop / idle_balance / active / low。
func userActivityState(u AnalyticsUserRow, tags []OperationalTagView) string {
	for _, t := range tags {
		if t.Key == "high_value_at_risk" {
			return "drop"
		}
	}
	for _, t := range tags {
		if t.Key == "dormant_high_balance" {
			return "idle_balance"
		}
	}
	today := val(u.TodayActualUSD)
	week := -1.0
	if u.Last7d != nil && u.Last7d.ActualUSD != nil {
		week = *u.Last7d.ActualUSD
	}
	recent := val(u.RecentAggregateActualUSD)
	if val(u.BalanceUSD) >= 20 && today <= 0 && (week == 0 || (week < 0 && recent <= 0)) && u.ActiveSubCount == 0 {
		return "idle_balance"
	}
	if today > 0 || week > 0 || (week < 0 && recent > 0) {
		return "active"
	}
	return "low"
}
func riskLamp(w *SanitizedRiskControlStatus, m DomainMeta) RiskControlLampView {
	v := RiskControlLampView{Level: SafetyLightUnknown, Quality: m.Quality, ObservedAt: m.ObservedAt, Reasons: []string{}}
	if w == nil || m.Quality != QualityTrusted {
		return v
	}
	v.Engine = w.Engine
	v.Enabled = w.Enabled
	v.RiskControlEnabled = w.RiskControlEnabled
	v.Mode = w.Mode
	v.QueueSize = w.QueueSize
	v.QueueLength = w.QueueLength
	v.QueueUsagePercent = w.QueueUsagePercent
	v.Enqueued = w.Enqueued
	v.Dropped = w.Dropped
	v.Processed = w.Processed
	v.Errors = w.Errors
	v.PreBlockChecked = w.PreBlockChecked
	v.PreBlockAllowed = w.PreBlockAllowed
	v.PreBlockBlocked = w.PreBlockBlocked
	v.PreBlockErrors = w.PreBlockErrors
	v.PreBlockAvgLatencyMS = w.PreBlockAvgLatencyMS
	v.PreBlockAPIKeyAvailCount = w.PreBlockAPIKeyAvailCount
	v.FlaggedHashCount = w.FlaggedHashCount
	if w.Enabled != nil && !*w.Enabled || w.RiskControlEnabled != nil && !*w.RiskControlEnabled {
		v.Quality = QualityDisabled
		v.Reasons = []string{"风控已关闭"}
		return v
	}
	if w.Enabled == nil || w.RiskControlEnabled == nil {
		v.Quality = QualityUnavailable
		return v
	}
	if v.QueueUsagePercent == nil && val(w.QueueSize) > 0 && val(w.QueueLength) >= 0 {
		v.QueueUsagePercent = safeFinitePtr(100 * float64(*w.QueueLength) / float64(*w.QueueSize))
	}
	queue, latency := val(v.QueueUsagePercent), val(w.PreBlockAvgLatencyMS)
	rate := func(n, d float64) bool { return d > 0 && n/d >= .05 }
	switch {
	case val(w.Dropped) > 0 || latency > 800 || queue >= 90 || val(w.PreBlockChecked) > 0 && val(w.PreBlockAPIKeyAvailCount) == 0 || w.Processed != nil && rate(float64(val(w.Errors)), float64(val(w.Processed))+float64(val(w.Errors))) || rate(float64(val(w.PreBlockErrors)), float64(val(w.PreBlockChecked))):
		v.Level = SafetyLightRed
		v.Reasons = []string{"风控丢弃、队列、时延或累计错误达到告警阈值"}
	case latency > 300 || queue >= 60 || val(w.Errors) > 0 || val(w.PreBlockErrors) > 0 || val(w.PreBlockChecked) > 0 && val(w.PreBlockAPIKeyAvailCount) == 1:
		v.Level = SafetyLightAmber
		v.Reasons = []string{"风控指标预警，错误数自引擎启动累计"}
	case val(w.Dropped) >= 0 && val(w.Errors) >= 0 && val(w.Processed) >= 0 && queue >= 0 && latency >= 0 && val(w.PreBlockChecked) >= 0 && val(w.PreBlockErrors) >= 0 && (val(w.PreBlockChecked) == 0 || val(w.PreBlockAPIKeyAvailCount) >= 0):
		v.Level = SafetyLightGreen
	default:
		v.Quality = QualityUnavailable
	}
	return v
}
func backupLamp(w SanitizedBackupsResult, m DomainMeta, now time.Time) BackupLampView {
	v := BackupLampView{Level: SafetyLightUnknown, Quality: m.Quality, ObservedAt: m.ObservedAt, TotalCount: len(w.Rows), Reasons: []string{}}
	if m.Quality != QualityTrusted {
		return v
	}
	if w.Truncated {
		v.Quality = QualityUnavailable
		v.Reasons = []string{"备份目录被截断"}
		return v
	}
	if len(w.Rows) == 0 {
		v.Level = SafetyLightRed
		v.Reasons = []string{"尚无备份记录"}
		return v
	}
	latest := w.Rows[0]
	v.LatestStatus, v.BackupType, v.SizeBytes, v.StartedAt, v.FinishedAt, v.ExpiresAt = latest.Status, latest.BackupType, latest.SizeBytes, latest.StartedAt, latest.FinishedAt, latest.ExpiresAt
	valid := func(r SanitizedBackup) bool {
		return r.StartedAt != nil && !r.StartedAt.After(now.Add(5*time.Minute)) && (r.FinishedAt == nil || !r.FinishedAt.Before(*r.StartedAt) && !r.FinishedAt.After(now.Add(5*time.Minute)))
	}
	if latest.Status == "" || !valid(latest) || latest.Status == "completed" && latest.FinishedAt == nil {
		v.Quality = QualityUnavailable
		return v
	}
	var last *SanitizedBackup
	for i := range w.Rows {
		r := &w.Rows[i]
		if r.Status == "completed" && valid(*r) && r.FinishedAt != nil && (r.SizeBytes == nil || *r.SizeBytes > 0) && (last == nil || r.FinishedAt.After(*last.FinishedAt)) {
			last = r
		}
	}
	if last != nil {
		v.LastCompletedAt = last.FinishedAt
		v.AgeHours = rounded(now.Sub(*last.FinishedAt).Hours(), 100)
	}
	switch {
	case latest.Status != "completed" && latest.Status != "running" && latest.Status != "in_progress" || latest.Status == "completed" && latest.SizeBytes != nil && *latest.SizeBytes <= 0 || last == nil || now.Sub(*last.FinishedAt) > 48*time.Hour || last.ExpiresAt != nil && !now.Before(*last.ExpiresAt):
		v.Level = SafetyLightRed
		v.Reasons = []string{"最新尝试失败或没有可用的近期备份"}
	case now.Sub(*last.FinishedAt) > 24*time.Hour || latest.Status != "completed" && now.Sub(*latest.StartedAt) > 2*time.Hour:
		v.Level = SafetyLightAmber
		v.Reasons = []string{"备份时间或运行时长超过预警阈值"}
	default:
		v.Level = SafetyLightGreen
	}
	return v
}

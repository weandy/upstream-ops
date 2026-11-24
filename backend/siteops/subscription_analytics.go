package siteops

import (
	"math"
	"time"
)

const cycleDuration = 30 * 24 * time.Hour

func subscriptionCalendar(s SanitizedSubscription, others []SanitizedSubscription, complete bool, now time.Time) AnalyticsSubscriptionRow {
	r := AnalyticsSubscriptionRow{SubscriptionID: s.ID, UserID: s.UserID, UserEmail: s.Email, UserEmailMasked: s.EmailMasked, GroupID: s.GroupID, GroupName: s.Group.Name, Platform: s.Group.Platform, Status: s.Status, StartsAt: s.StartsAt, ExpiresAt: s.ExpiresAt, CycleState: "invalid_dates", Attribution: "group_only", DailyWindow: s.Daily, WeeklyWindow: s.Weekly, MonthlyWindow: s.Monthly}
	if s.StartsAt == nil || s.ExpiresAt == nil || !s.ExpiresAt.After(*s.StartsAt) {
		return r
	}
	start, end := s.StartsAt.UTC(), s.ExpiresAt.UTC()
	r.TotalCycles = int((end.Sub(start)-1)/cycleDuration) + 1
	r.ExpiresInDays = safeFinitePtr(end.Sub(now).Hours() / 24)
	var k int
	observed := now
	switch {
	case !now.After(start):
		r.CycleState = "not_started"
		observed = start
	case s.Status == "revoked" || s.Status == "cancelled":
		r.CycleState = "revoked"
		k = r.TotalCycles - 1
		observed = end
	case s.Status == "":
		return r
	case s.Status != "active" || !now.Before(end):
		r.CycleState = "expired"
		k = r.TotalCycles - 1
		observed = end
	default:
		r.CycleState = "active"
		k = int(now.Sub(start) / cycleDuration)
	}
	b := start.Add(time.Duration(k) * cycleDuration)
	f := b.Add(cycleDuration)
	if f.After(end) {
		f = end
	}
	r.SubCycleStart, r.SubCycleEnd, r.CycleIndex, r.SameAsTerm = &b, &f, k+1, r.TotalCycles == 1
	if !complete {
		r.AttributionReason = "订阅目录分页未全量"
		return r
	}
	if observed.Equal(end) {
		observed = observed.Add(-time.Nanosecond)
	}
	for _, o := range others {
		if o.ID == s.ID || o.UserID != s.UserID || o.GroupID != s.GroupID {
			continue
		}
		finish := o.ExpiresAt
		if o.RevokedAt != nil {
			finish = o.RevokedAt
		}
		if o.StartsAt == nil || finish == nil || !finish.After(*o.StartsAt) {
			r.AttributionReason = "同组订阅时间未知"
			return r
		}
		if !daySH(*o.StartsAt).After(daySH(observed)) && !daySH(finish.Add(-time.Nanosecond)).Before(daySH(start)) {
			r.AttributionReason = "同组订阅自然日重叠"
			return r
		}
	}
	r.Attribution = "unique_group_term"
	return r
}
func ceilDivDur(a, b time.Duration) int64 {
	if a <= 0 {
		return 0
	}
	return int64(1 + (a-1)/b)
}
func windowReset(w *RollingWindowView, observed time.Time) time.Time {
	if w.ResetsAt != nil {
		return *w.ResetsAt
	}
	if w.ResetsInSeconds != nil && *w.ResetsInSeconds >= 0 && *w.ResetsInSeconds <= math.MaxInt64/int64(time.Second) && !observed.IsZero() {
		return observed.Add(time.Duration(*w.ResetsInSeconds) * time.Second)
	}
	return time.Time{}
}
func windowBound(w RollingWindowView, p, h time.Duration, t, progAt time.Time) (float64, bool) {
	l := *w.LimitUSD
	if h <= 0 {
		return 0, true
	}
	remaining := l
	exact := true
	if val(w.UsedUSD) >= 0 {
		remaining = max(0, l-*w.UsedUSD)
		if w.RemainingUSD != nil && math.Abs(*w.RemainingUSD-remaining) > max(.0001, l*1e-6) {
			remaining = l
			exact = false
		}
	} else {
		exact = false
	}
	reset := windowReset(&w, progAt)
	switch {
	case reset.After(t):
		return remaining + l*float64(ceilDivDur(h-reset.Sub(t), p)), exact
	case w.WindowStart == nil && val(w.UsedUSD) == 0:
		return l * float64(ceilDivDur(h, p)), exact
	case !reset.IsZero() && t.Sub(reset) <= p:
		return l * float64(ceilDivDur(h, p)), exact
	case !reset.IsZero():
		remaining = l
	}
	return remaining + l*float64(ceilDivDur(h, p)), false
}
func subscriptionLayer(row AnalyticsSubscriptionRow, a, z, now, progAt time.Time, usage *UsageWindowView, renewal, knownLimits bool) SubscriptionTermLayerView {
	v := SubscriptionTermLayerView{StartAt: a, EndAt: z, CycleIndex: row.CycleIndex, TotalCycles: row.TotalCycles, DurationDays: z.Sub(a).Hours() / 24, Confidence: "insufficient", CapState: "unknown", ForecastStatus: row.CycleState}
	v.BoundaryApprox = !a.Equal(daySH(a)) || row.CycleState == "expired" && !z.Equal(daySH(z))
	if v.BoundaryApprox {
		v.DayBoundaryNote = "按上海自然日查询，边界日包含整天消耗"
		if renewal && !a.Equal(daySH(a)) {
			v.DayBoundaryNote += "，首日可能混入上一子周期消耗"
		}
	}
	if row.CycleState == "revoked" {
		v.Reason = "已撤销，精确终点未知"
		return v
	}
	if row.Attribution != "unique_group_term" {
		v.GroupUsage = usage
		return v
	}
	v.Usage = usage
	if usage == nil || val(usage.ActualUSD) < 0 {
		return v
	}
	u := *usage.ActualUSD
	t := now
	if usage.ObservedAt != nil && t.After(*usage.ObservedAt) {
		t = *usage.ObservedAt
	}
	if t.After(z) {
		t = z
	}
	e := max(time.Duration(0), t.Sub(a))
	h := max(time.Duration(0), z.Sub(t))
	v.ObservedUntil = &t
	v.ElapsedHours = e.Hours()
	v.RemainingHours = h.Hours()
	if row.CycleState == "expired" {
		v.ForecastStatus = "completed"
		v.ProjectedEndUSD = rounded(u, 10000)
		v.Confidence = "high"
		if v.BoundaryApprox {
			v.Confidence = "medium"
		}
		return v
	}
	if row.CycleState != "active" {
		return v
	}
	guard := renewal && v.BoundaryApprox && e < 24*time.Hour
	if e >= 6*time.Hour {
		den := e
		if guard {
			den = 24 * time.Hour
		}
		v.BurnSlowUSDDay = safeFinitePtr(u / (den.Hours() / 24))
	}
	var fastAge time.Duration
	for _, j := range []struct {
		name     string
		w        RollingWindowView
		min, max time.Duration
	}{{"weekly", row.WeeklyWindow, 12 * time.Hour, 7 * 24 * time.Hour}, {"daily", row.DailyWindow, 6 * time.Hour, 24 * time.Hour}} {
		if progAt.IsZero() || t.Sub(progAt).Abs() > 5*time.Minute || j.w.Source != "progress" || j.w.WindowStart == nil || val(j.w.UsedUSD) < 0 {
			continue
		}
		elapsed := progAt.Sub(*j.w.WindowStart)
		if elapsed >= j.min && elapsed <= j.max+2*time.Minute {
			fastAge = elapsed
			v.BurnFastSource = j.name
			v.BurnFastUSDDay = safeFinitePtr(*j.w.UsedUSD / (elapsed.Hours() / 24))
			break
		}
	}
	slow, fast := v.BurnSlowUSDDay, v.BurnFastUSDDay
	switch {
	case fast != nil && slow != nil:
		weight := .6
		if v.BurnFastSource == "daily" {
			weight = .35
		}
		v.BurnEffectiveUSDDay = safeFinitePtr(weight*(*fast) + (1-weight)*(*slow))
	case slow != nil:
		v.BurnEffectiveUSDDay = slow
	default:
		v.BurnEffectiveUSDDay = fast
	}
	if rate := v.BurnEffectiveUSDDay; rate != nil {
		if *rate <= 1e-6 {
			v.BurnEffectiveUSDDay = ptr(0.0)
		}
		v.UncappedEstimateUSD = rounded(u+*v.BurnEffectiveUSDDay*h.Hours()/24, 10000)
	}
	best := math.Inf(1)
	precise := true
	for _, j := range []struct {
		name string
		w    RollingWindowView
		p    time.Duration
	}{{"daily", row.DailyWindow, 24 * time.Hour}, {"weekly", row.WeeklyWindow, 7 * 24 * time.Hour}, {"monthly", row.MonthlyWindow, cycleDuration}} {
		if j.w.LimitUSD == nil || val(j.w.LimitUSD) <= 0 || safeFinitePtr(*j.w.LimitUSD) == nil {
			continue
		}
		bound, exact := windowBound(j.w, j.p, h, t, progAt)
		precise = precise && exact
		if bound < best {
			best = bound
			v.CapBindingWindow = j.name
		}
	}
	if !math.IsInf(best, 1) {
		v.CapState = "verified"
		if !precise {
			v.CapState = "partial"
		}
		v.TheoreticalMaxUSD = rounded(u+best, 10000)
		v.ProjectedEndUSD = v.UncappedEstimateUSD
		if v.UncappedEstimateUSD != nil && v.TheoreticalMaxUSD != nil && *v.UncappedEstimateUSD > *v.TheoreticalMaxUSD {
			v.ProjectedEndUSD = v.TheoreticalMaxUSD
			v.CappedByLimit = true
		}
	} else if knownLimits {
		v.CapState = "unlimited"
		v.ProjectedEndUSD = v.UncappedEstimateUSD
	} else {
		v.Reason = "未校验限额"
	}
	if v.BurnEffectiveUSDDay == nil {
		return v
	}
	v.Confidence = "low"
	if guard || v.CapState == "partial" || v.CapState == "unknown" || (e < math.MaxInt64/3 && h > 3*e) {
		return v
	}
	if e >= 24*time.Hour && slow != nil && (fast != nil || e >= 72*time.Hour) {
		v.Confidence = "medium"
	}
	if e >= 72*time.Hour && v.BurnFastSource == "weekly" && fastAge >= 24*time.Hour && slow != nil && fast != nil && val(usage.Requests) >= 10 && math.Abs(*slow-*fast)/max(*slow, *fast, .01) <= .40 && (v.CapState == "verified" || v.CapState == "unlimited") {
		v.Confidence = "high"
	}
	return v
}

// subscriptionForecastSummary 基于订阅周期、已用量、可用总量和当前日均速率计算周期期望与预测结论。
func subscriptionForecastSummary(row AnalyticsSubscriptionRow, cfg AnalyticsConfig, now time.Time) *SubscriptionForecastSummary {
	cfg = cfg.withForecastDefaults()
	start, end := row.SubCycleStart, row.SubCycleEnd
	if start == nil || end == nil {
		start, end = row.StartsAt, row.ExpiresAt
	}
	if start == nil || end == nil || !end.After(*start) {
		return nil
	}
	cycleDays := end.Sub(*start).Hours() / 24
	if cycleDays <= 0 {
		return nil
	}
	elapsed := max(0.0, now.Sub(*start).Hours()/24)
	if elapsed > cycleDays {
		elapsed = cycleDays
	}
	remaining := max(0.0, cycleDays-elapsed)
	timePct := math.Min(100, math.Round(elapsed/cycleDays*1000)/10)

	// 1. 确定周期可用总量（优先月限额，其次周/日限额周期封顶，均未设则按不限额期望上限）
	var capUSD float64
	isUnlimited := false
	switch {
	case row.MonthlyWindow.LimitUSD != nil && *row.MonthlyWindow.LimitUSD > 0:
		capUSD = *row.MonthlyWindow.LimitUSD
	default:
		best := math.Inf(1)
		if row.WeeklyWindow.LimitUSD != nil && *row.WeeklyWindow.LimitUSD > 0 {
			weeks := math.Ceil(cycleDays / 7)
			if v := *row.WeeklyWindow.LimitUSD * weeks; v < best {
				best = v
			}
		}
		if row.DailyWindow.LimitUSD != nil && *row.DailyWindow.LimitUSD > 0 {
			if v := *row.DailyWindow.LimitUSD * math.Ceil(cycleDays); v < best {
				best = v
			}
		}
		if !math.IsInf(best, 1) && best > 0 {
			capUSD = best
		} else {
			isUnlimited = true
			capUSD = cfg.UnlimitedTargetUSD
		}
	}

	// 2. 当前本期已用量（优先月窗快照，退回周/日窗）
	var usedPtr *float64
	switch {
	case row.MonthlyWindow.UsedUSD != nil && *row.MonthlyWindow.UsedUSD >= 0:
		usedPtr = row.MonthlyWindow.UsedUSD
	case row.WeeklyWindow.UsedUSD != nil && *row.WeeklyWindow.UsedUSD >= 0:
		usedPtr = row.WeeklyWindow.UsedUSD
	case row.DailyWindow.UsedUSD != nil && *row.DailyWindow.UsedUSD >= 0:
		usedPtr = row.DailyWindow.UsedUSD
	}

	out := &SubscriptionForecastSummary{
		CycleDays:       math.Round(cycleDays*10) / 10,
		ElapsedDays:     math.Round(elapsed*10) / 10,
		RemainingDays:   math.Round(remaining*10) / 10,
		TimeProgressPct: timePct,
		UsedUSD:         usedPtr,
		QuotaCapUSD:     rounded(capUSD, 100),
		IsUnlimited:     isUnlimited,
		Verdict:         "observing",
	}

	if usedPtr != nil && capUSD > 0 {
		out.UsedPct = rounded(math.Min(100, *usedPtr/capUSD*100), 10)
	}

	if row.CycleState == "expired" || row.CycleState == "revoked" {
		out.Verdict = "expired"
		out.ProjectedEndUSD = usedPtr
		out.ProjectedPct = out.UsedPct
		return out
	}
	if row.CycleState != "active" || usedPtr == nil {
		return out
	}

	used := *usedPtr
	// 3. 估算有效日均消耗（融合周速率与周期平均速率）
	var slowRate, fastRate *float64
	if elapsed >= 0.25 {
		slowRate = safeFinitePtr(used / elapsed)
	}
	if row.WeeklyWindow.UsedUSD != nil && *row.WeeklyWindow.UsedUSD >= 0 && row.WeeklyWindow.WindowStart != nil {
		wElapsed := now.Sub(*row.WeeklyWindow.WindowStart).Hours() / 24
		if wElapsed >= 0.5 && wElapsed <= 7.05 {
			fastRate = safeFinitePtr(*row.WeeklyWindow.UsedUSD / wElapsed)
		}
	}
	var burn float64
	switch {
	case fastRate != nil && slowRate != nil:
		burn = 0.6*(*fastRate) + 0.4*(*slowRate)
	case slowRate != nil:
		burn = *slowRate
	case fastRate != nil:
		burn = *fastRate
	}
	if burn < 0 || math.IsNaN(burn) || math.IsInf(burn, 0) {
		burn = 0
	}
	out.BurnUSDDay = rounded(burn, 100)

	// 4. 刚开通不足 2 天：先标待观察，避免样本过少误判
	if elapsed < 2 {
		out.ProjectedEndUSD = rounded(used, 100)
		out.ProjectedPct = out.UsedPct
		out.Verdict = "observing"
		return out
	}

	rawProj := used + burn*remaining
	projUSD := rawProj
	if !isUnlimited && capUSD > 0 && projUSD > capUSD {
		projUSD = capUSD
	}
	out.ProjectedEndUSD = rounded(projUSD, 100)
	var projPct float64
	if capUSD > 0 {
		projPct = rawProj / capUSD * 100
		out.ProjectedPct = rounded(projPct, 10)
	}

	hitDaily := row.DailyWindow.LimitUSD != nil && *row.DailyWindow.LimitUSD > 0 && row.DailyWindow.UsedUSD != nil && *row.DailyWindow.UsedUSD >= *row.DailyWindow.LimitUSD
	hitWeekly := row.WeeklyWindow.LimitUSD != nil && *row.WeeklyWindow.LimitUSD > 0 && row.WeeklyWindow.UsedUSD != nil && *row.WeeklyWindow.UsedUSD >= *row.WeeklyWindow.LimitUSD
	hitCycle := !isUnlimited && capUSD > 0 && used >= capUSD

	switch {
	case hitCycle || hitDaily || hitWeekly:
		out.Verdict = "exhausted"
		out.DaysToExhaust = ptr(0.0)
	case capUSD > 0 && rawProj >= capUSD:
		out.Verdict = "will_hit"
		if burn > 0 && capUSD > used {
			out.DaysToExhaust = rounded((capUSD-used)/burn, 10)
		}
	case projPct >= cfg.NearFullPct:
		out.Verdict = "near_full"
	case timePct >= 30 && projPct < cfg.UnderUsePct:
		out.Verdict = "under_use"
	default:
		out.Verdict = "on_track"
	}
	return out
}

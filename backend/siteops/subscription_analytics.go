package siteops

import (
	"math"
	"time"
)

const cycleDuration = 30 * 24 * time.Hour

func subscriptionCalendar(s SanitizedSubscription, others []SanitizedSubscription, complete bool, now time.Time) AnalyticsSubscriptionRow {
	r := AnalyticsSubscriptionRow{SubscriptionID: s.ID, UserID: s.UserID, UserEmailMasked: s.EmailMasked, GroupID: s.GroupID, GroupName: s.Group.Name, Platform: s.Group.Platform, Status: s.Status, StartsAt: s.StartsAt, ExpiresAt: s.ExpiresAt, CycleState: "invalid_dates", Attribution: "group_only", DailyWindow: s.Daily, WeeklyWindow: s.Weekly, MonthlyWindow: s.Monthly}
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

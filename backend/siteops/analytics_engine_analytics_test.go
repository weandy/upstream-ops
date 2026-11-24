package siteops

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestAnalyticsCacheRatesAndTagsAndLights(t *testing.T) {
	for _, tc := range []struct {
		i, o, w, r, total int64
		mode              string
		hit, write, ratio float64
	}{{60, 20, 10, 30, 120, "exclusive", 30, 10, 3}, {60, 20, 0, 20, 100, "exclusive", 25, 0, -1}, {80, 20, 0, 20, 100, "inclusive_detected", 25, 0, -1}, {0, 0, 0, 0, 0, "exclusive", -1, -1, -1}, {60, 20, 10, 30, 1000, "unverified", -1, -1, -1}, {-1, 0, 0, 0, 0, "unverified", -1, -1, -1}, {math.MaxInt64, 1, 0, 0, 0, "unverified", -1, -1, -1}} {
		m := cacheMetrics(&tc.i, &tc.o, &tc.w, &tc.r, &tc.total)
		if m.InputMode != tc.mode || val(m.CacheReadHitPct) != tc.hit || val(m.CacheWritePct) != tc.write || val(m.CacheRWRatio) != tc.ratio {
			t.Fatalf("token result %+v", m)
		}
	}
	cfg := DefaultAnalyticsConfig()
	p := ReferencePriceView{Scope: "group", MonthlyMinor: 7200, Currency: "CNY"}
	for _, tc := range []struct {
		days        int
		actual, ref float64
		risk        string
	}{{30, 30, 10, "warn"}, {30, 50, 10, "danger"}, {15, 0, 5, "normal"}} {
		v := priceCompare(&p, cfg, time.Duration(tc.days)*24*time.Hour, cycleDuration, true, &tc.actual, nil)
		if val(v.CycleRefUSD) != tc.ref || v.MarginRisk != tc.risk {
			t.Fatalf("price %+v", v)
		}
	}
	cfg.LeverageWarnThreshold, cfg.LeverageDangerThreshold = 0, 0
	if priceCompare(&p, cfg, cycleDuration, cycleDuration, true, ptr(30.), nil).MarginRisk != "threshold_disabled" {
		t.Fatal("threshold")
	}
	p.MonthlyMinor = 0
	if priceCompare(&p, cfg, cycleDuration, cycleDuration, true, ptr(30.), nil).MarginRisk != "free" {
		t.Fatal("gift")
	}
	n := testSiteOpsStartTime
	m := DomainMeta{Quality: QualityTrusted}
	risk := SanitizedRiskControlStatus{Enabled: ptr(true), RiskControlEnabled: ptr(true), Dropped: ptr(int64(0)), Errors: ptr(int64(0)), Processed: ptr(int64(100)), QueueUsagePercent: ptr(0.), PreBlockErrors: ptr(int64(0)), PreBlockChecked: ptr(int64(0))}
	for _, tc := range []struct {
		ms    float64
		level SafetyLightLevel
	}{{300, SafetyLightGreen}, {301, SafetyLightAmber}, {800, SafetyLightAmber}, {801, SafetyLightRed}} {
		risk.PreBlockAvgLatencyMS = &tc.ms
		if l := riskLamp(&risk, m); l.Level != tc.level {
			t.Fatalf("latency %v: %s", tc.ms, l.Level)
		}
	}
	risk.PreBlockAvgLatencyMS = ptr(0.)
	risk.Errors = ptr(int64(49))
	risk.Processed = ptr(int64(951))
	if riskLamp(&risk, m).Level != SafetyLightAmber {
		t.Fatal("4.9%")
	}
	risk.Errors = ptr(int64(50))
	risk.Processed = ptr(int64(950))
	if riskLamp(&risk, m).Level != SafetyLightRed {
		t.Fatal("5%")
	}
	risk.Enabled = ptr(false)
	if riskLamp(&risk, m).Level != SafetyLightUnknown {
		t.Fatal("disabled")
	}
	risk.Enabled = ptr(true)
	risk.Errors = nil
	if riskLamp(&risk, m).Level != SafetyLightUnknown {
		t.Fatal("missing health")
	}
	risk.Dropped = ptr(int64(1))
	if riskLamp(&risk, m).Level != SafetyLightRed {
		t.Fatal("dropped")
	}
	for _, tc := range []struct {
		age   time.Duration
		level SafetyLightLevel
	}{{24 * time.Hour, SafetyLightGreen}, {24*time.Hour + time.Second, SafetyLightAmber}, {48 * time.Hour, SafetyLightAmber}, {48*time.Hour + time.Second, SafetyLightRed}} {
		w := SanitizedBackupsResult{Rows: []SanitizedBackup{{Status: "completed", StartedAt: ptr(n.Add(-tc.age - time.Hour)), FinishedAt: ptr(n.Add(-tc.age))}}}
		if l := backupLamp(w, m, n); l.Level != tc.level {
			t.Fatal(l)
		}
		w.Rows[0].ExpiresAt = &n
		if backupLamp(w, m, n).Level != SafetyLightRed {
			t.Fatal("expired backup")
		}
		w.Truncated = true
		if backupLamp(w, m, n).Level != SafetyLightUnknown {
			t.Fatal("truncated backup")
		}
	}
	backups := SanitizedBackupsResult{Rows: []SanitizedBackup{{Status: "failed", StartedAt: &n}, {Status: "completed", StartedAt: ptr(n.Add(-2 * time.Hour)), FinishedAt: ptr(n.Add(-time.Hour))}}}
	if b := backupLamp(backups, m, n); b.Level != SafetyLightRed || b.LastCompletedAt == nil || !b.LastCompletedAt.Equal(n.Add(-time.Hour)) {
		t.Fatal(b)
	}
	if backupLamp(SanitizedBackupsResult{}, m, n).Level != SafetyLightRed {
		t.Fatal("empty backups")
	}
	u := AnalyticsUserRow{UserID: 1, Status: "active", CreatedAt: ptr(n.AddDate(0, -1, 0)), BalanceUSD: ptr(30.), TodayActualUSD: ptr(0.), RecentAggregateActualUSD: ptr(60.), LiveConcurrency: &UserConcurrencyView{WaitingInQueue: ptr(int64(1))}, Last7d: &UsageWindowView{ActualUSD: ptr(.01), Requests: ptr(int64(2))}}
	sub := SanitizedSubscription{ID: 1, UserID: 1, GroupID: 2, Status: "active", StartsAt: u.CreatedAt, ExpiresAt: ptr(n.AddDate(0, 0, 3))}
	expired := sub
	expired.ID = 2
	expired.GroupID = 3
	expired.ExpiresAt = ptr(n.AddDate(0, 0, -3))
	expired.Status = "expired"
	subs := []SanitizedSubscription{sub, expired}
	tags := userTags(u, subs, true, n)
	if len(tags) != 5 || tags[0].Key != "high_value_at_risk" || tags[4].Key != "dormant_high_balance" {
		t.Fatalf("tags %+v", tags)
	}
	if len(userTags(u, subs, false, n)) != 4 {
		t.Fatal("incomplete directory")
	}
	u.Last7d.Requests = ptr(int64(20))
	u.Last7d.Cache = CacheMetricsView{TokenQuality: QualityTrusted, EffectiveInput: ptr(int64(100000)), CacheReadHitPct: ptr(20.)}
	found := false
	for _, tag := range userTags(u, nil, true, n) {
		found = found || tag.Key == "cache_anomaly"
	}
	u.Last7d.StartDate = "2024-01-10"
	all := *u.Last7d
	all.StartDate = "2023-12-10"
	all.Cache.CacheReadHitPct = ptr(60.)
	u.AllTime = &all
	anomalies := 0
	for _, tag := range userTags(u, nil, true, n) {
		if tag.Key == "cache_anomaly" {
			anomalies++
			if tag.SubKind != "cache_drop" || !strings.Contains(tag.Reason, "低于 30%") {
				t.Fatal(tag)
			}
		}
	}
	if anomalies != 1 {
		t.Fatal("同一用户应只产生一条缓存异常")
	}
	if !found || len(userTags(AnalyticsUserRow{}, nil, false, n)) != 0 {
		t.Fatal("cache tags")
	}
}
func TestSubscriptionThreeLayersAndCapBounds(t *testing.T) {
	start := time.Date(2024, 1, 31, 12, 0, 0, 0, locUTC8)
	s := SanitizedSubscription{ID: 1, UserID: 1, GroupID: 2, Status: "active", StartsAt: &start}
	for _, days := range []int{29, 30, 31, 60, 90, 120, 150} {
		end := start.AddDate(0, 0, days)
		s.ExpiresAt = &end
		for _, n := range []time.Time{start.Add(-1), start, start.Add(cycleDuration), end, end.Add(time.Hour)} {
			r := subscriptionCalendar(s, nil, true, n)
			if r.TotalCycles != (days+29)/30 || r.CycleIndex > r.TotalCycles || r.SameAsTerm != (days <= 30) {
				t.Fatalf("calendar %d %+v", days, r)
			}
			if !n.After(start) && r.CycleState != "not_started" || !n.Before(end) && r.CycleState != "expired" {
				t.Fatal("boundary state")
			}
		}
	}
	s.ExpiresAt = ptr(start.AddDate(0, 0, 90))
	n := start.Add(cycleDuration)
	r := subscriptionCalendar(s, nil, true, n)
	if r.CycleIndex != 2 || !r.SubCycleStart.Equal(time.Date(2024, 3, 1, 12, 0, 0, 0, locUTC8)) || !r.SubCycleEnd.Equal(n.Add(cycleDuration)) {
		t.Fatal("leap date")
	}
	adjacent := s
	adjacent.ID = 2
	adjacent.StartsAt = ptr(start.AddDate(0, -1, 0))
	adjacent.ExpiresAt = &start
	if subscriptionCalendar(s, []SanitizedSubscription{adjacent}, true, n).Attribution != "group_only" || subscriptionCalendar(s, nil, false, n).Attribution != "group_only" {
		t.Fatal("double attribution")
	}
	r.DailyWindow = RollingWindowView{LimitUSD: ptr(10.), UsedUSD: ptr(8.), ResetsAt: ptr(n.Add(12 * time.Hour))}
	r.WeeklyWindow = RollingWindowView{Source: "progress", LimitUSD: ptr(50.), UsedUSD: ptr(43.), WindowStart: ptr(n.Add(-129 * time.Hour)), ResetsAt: ptr(n.Add(60 * time.Hour))}
	usage := &UsageWindowView{ActualUSD: ptr(40.), Requests: ptr(int64(20)), ObservedAt: &n}
	for _, tc := range []struct {
		h       time.Duration
		cap     float64
		binding string
	}{{36 * time.Hour, 47, "weekly"}, {12 * time.Hour, 42, "daily"}} {
		v := subscriptionLayer(r, n.Add(-240*time.Hour), n.Add(tc.h), n, n, usage, false, true)
		if val(v.TheoreticalMaxUSD) != tc.cap || v.CapBindingWindow != tc.binding || val(v.BurnEffectiveUSDDay) != 6.4 {
			t.Fatalf("hand vector %+v", v)
		}
		if tc.h == 36*time.Hour && (val(v.UncappedEstimateUSD) != 49.6 || val(v.ProjectedEndUSD) != 47) {
			t.Fatal("forecast")
		}
		if _, err := json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		h time.Duration
		c float64
	}{{0, 0}, {12 * time.Hour, 2}, {12*time.Hour + time.Second, 12}, {36 * time.Hour, 12}} {
		b, _ := windowBound(r.DailyWindow, 24*time.Hour, tc.h, n, n)
		if b != tc.c {
			t.Fatalf("bound %v %v", tc.h, b)
		}
	}
	w := RollingWindowView{LimitUSD: ptr(10.), UsedUSD: ptr(0.)}
	if b, exact := windowBound(w, 24*time.Hour, 24*time.Hour, n, n); b != 10 || !exact {
		t.Fatal("empty window")
	}
	w.UsedUSD = ptr(3.)
	if b, exact := windowBound(w, 24*time.Hour, 24*time.Hour, n, n); b != 17 || exact {
		t.Fatal("unknown phase")
	}
	w.RemainingUSD = ptr(9.)
	if b, exact := windowBound(w, 24*time.Hour, 24*time.Hour, n, n); b != 20 || exact {
		t.Fatal("inconsistent window")
	}
	r.DailyWindow = RollingWindowView{}
	r.WeeklyWindow = RollingWindowView{}
	v := subscriptionLayer(r, n.Add(-10*time.Hour), n.Add(time.Hour), n, n, usage, true, true)
	if val(v.BurnSlowUSDDay) != 40 || v.Confidence != "low" || v.CapState != "unlimited" {
		t.Fatal(v)
	}
	v = subscriptionLayer(r, n.Add(-time.Hour), n.Add(time.Hour), n, n, usage, false, false)
	if v.Confidence != "insufficient" || v.ProjectedEndUSD != nil {
		t.Fatal(v)
	}
}

func TestAnalyticsSubscriptionBoundariesAndConfidence(t *testing.T) {
	a := daySH(testSiteOpsStartTime)
	z := a.Add(cycleDuration)
	s := SanitizedSubscription{ID: 1, UserID: 1, GroupID: 1, StartsAt: &a, ExpiresAt: &z, Status: "revoked"}
	row := subscriptionCalendar(s, nil, true, z)
	v := subscriptionLayer(row, a, z, z, time.Time{}, nil, false, false)
	if row.CycleState != "revoked" || v.ForecastStatus != "revoked" || v.Reason != "已撤销，精确终点未知" || v.ProjectedEndUSD != nil {
		t.Fatal(v)
	}
	s.Status = "expired"
	adjacent := s
	adjacent.ID = 2
	adjacent.StartsAt = &z
	adjacent.ExpiresAt = ptr(z.Add(cycleDuration))
	if row := subscriptionCalendar(s, []SanitizedSubscription{adjacent}, true, z); row.Attribution != "unique_group_term" {
		t.Fatal("午夜结束的订阅与次日订阅重叠", row)
	}
	row.CycleState = "expired"
	v = subscriptionLayer(row, a, z.Add(time.Hour), z, time.Time{}, nil, true, false)
	if strings.Contains(v.DayBoundaryNote, "上一子周期") {
		t.Fatal(v.DayBoundaryNote)
	}
	row.CycleState = "active"
	now := a.Add(72 * time.Hour)
	u := &UsageWindowView{ActualUSD: ptr(30.), ObservedAt: &now}
	v = subscriptionLayer(row, a, now.Add(time.Hour), now, time.Time{}, u, false, false)
	if v.CapState != "unknown" || v.Confidence != "low" || v.Reason != "未校验限额" {
		t.Fatal(v)
	}
	v = subscriptionLayer(row, a, now.Add(3*72*time.Hour+time.Nanosecond), now, time.Time{}, u, false, true)
	if v.Confidence != "low" {
		t.Fatal("3E+1ns 置信度未封顶", v)
	}
}

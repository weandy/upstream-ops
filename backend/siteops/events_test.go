package siteops

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

type memEventStore struct {
	mu           sync.Mutex
	nextEventID  uint
	nextDelivID  uint
	events       map[uint]storage.SiteOpsEvent
	deliveries   map[uint]storage.SiteOpsAlertDelivery
}

func newMemEventStore() *memEventStore {
	return &memEventStore{
		nextEventID: 1,
		nextDelivID: 1,
		events:      make(map[uint]storage.SiteOpsEvent),
		deliveries:  make(map[uint]storage.SiteOpsAlertDelivery),
	}
}

func (m *memEventStore) ListActiveEvents(_ context.Context, instanceID uint) ([]storage.SiteOpsEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.SiteOpsEvent
	for _, evt := range m.events {
		if evt.InstanceID == instanceID && evt.State == EventStateFiring {
			out = append(out, evt)
		}
	}
	return out, nil
}

func (m *memEventStore) UpsertEvent(_ context.Context, evt *storage.SiteOpsEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if evt.ID == 0 {
		evt.ID = m.nextEventID
		m.nextEventID++
	}
	m.events[evt.ID] = *evt
	return nil
}

func (m *memEventStore) ListEvents(_ context.Context, instanceID uint, state, severity string, limit int) ([]storage.SiteOpsEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.SiteOpsEvent
	for _, evt := range m.events {
		if evt.InstanceID != instanceID {
			continue
		}
		if state != "" && evt.State != state {
			continue
		}
		if severity != "" && evt.Severity != severity {
			continue
		}
		out = append(out, evt)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memEventStore) CreateDelivery(_ context.Context, d *storage.SiteOpsAlertDelivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID == 0 {
		d.ID = m.nextDelivID
		m.nextDelivID++
	}
	m.deliveries[d.ID] = *d
	return nil
}

func (m *memEventStore) HasActiveCooldown(_ context.Context, instanceID, notifyChannelID uint, cooldownKey string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.deliveries {
		if d.InstanceID == instanceID &&
			d.NotifyChannelID == notifyChannelID &&
			d.CooldownKey == cooldownKey &&
			d.CooldownUntil != nil &&
			d.CooldownUntil.After(now) {
			return true, nil
		}
	}
	return false, nil
}

func (m *memEventStore) ListDeliveriesByEventIDs(_ context.Context, eventIDs []uint) (map[uint][]storage.SiteOpsAlertDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := make(map[uint]bool, len(eventIDs))
	for _, id := range eventIDs {
		set[id] = true
	}
	out := make(map[uint][]storage.SiteOpsAlertDelivery)
	for _, d := range m.deliveries {
		if set[d.EventID] {
			out[d.EventID] = append(out[d.EventID], d)
		}
	}
	return out, nil
}

func (m *memEventStore) ListDueDeliveries(_ context.Context, now time.Time, limit int) ([]storage.SiteOpsAlertDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.SiteOpsAlertDelivery
	for _, d := range m.deliveries {
		if (d.Status == DeliveryStatusPending || d.Status == DeliveryStatusFailed) &&
			d.NextRetryAt != nil && !d.NextRetryAt.After(now) {
			out = append(out, d)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *memEventStore) UpdateDelivery(_ context.Context, d *storage.SiteOpsAlertDelivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deliveries[d.ID] = *d
	return nil
}

func (m *memEventStore) DeleteEventsBeforeChunked(context.Context, time.Time, int, int, time.Duration) (int64, error) {
	return 0, nil
}

func (m *memEventStore) DeleteDeliveriesBeforeChunked(context.Context, time.Time, int, int, time.Duration) (int64, error) {
	return 0, nil
}

func TestParseAlertConfig(t *testing.T) {
	def := DefaultAlertConfig()

	if got := ParseAlertConfig(""); got != def {
		t.Fatalf("empty raw should fallback to default, got %+v", got)
	}
	if got := ParseAlertConfig("{bad json"); got != def {
		t.Fatalf("invalid json should fallback to default, got %+v", got)
	}

	got := ParseAlertConfig(`{"enabled":false,"sla_threshold_pct":92.5,"cooldown_minutes":15,"daily_report_hour_utc8":8}`)
	if got.Enabled {
		t.Fatalf("expected Enabled=false")
	}
	if got.SLAThresholdPct != 92.5 || got.CooldownMinutes != 15 || got.DailyReportHourUTC8 != 8 {
		t.Fatalf("unexpected parsed fields: %+v", got)
	}
	if got.MinSamplesForSLA != def.MinSamplesForSLA || got.ConcurrencyThresholdPct != def.ConcurrencyThresholdPct {
		t.Fatalf("unspecified fields should inherit defaults: %+v", got)
	}

	// 越界值应回退默认
	clamped := ParseAlertConfig(`{"sla_threshold_pct":-5,"daily_report_hour_utc8":99,"cooldown_minutes":-1}`)
	if clamped.SLAThresholdPct != def.SLAThresholdPct || clamped.DailyReportHourUTC8 != def.DailyReportHourUTC8 || clamped.CooldownMinutes != def.CooldownMinutes {
		t.Fatalf("out-of-range values should fallback to defaults: %+v", clamped)
	}
}

func testInstance() storage.SiteOpsInstance {
	return storage.SiteOpsInstance{
		ID:      1,
		Name:    "portal-prod",
		CredGen: 1,
		Enabled: true,
	}
}

func findTransition(list []EventTransition, ruleKey, resourceKey, kind string) *EventTransition {
	for i := range list {
		if list[i].Event.RuleKey == ruleKey && list[i].Event.ResourceKey == resourceKey && list[i].Kind == kind {
			return &list[i]
		}
	}
	return nil
}

func TestEventSiteOfflineAndSameDomainRecovery(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	// 第 1 次不可达：防抖未满 2 次，不触发
	trs, err := eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now,
		Snapshot:     &storage.SiteOpsSnapshot{Reachable: false, ProbeLatencyMS: 500},
		FieldQuality: FieldQualityMap{DomainReachability: QualityTrusted},
	})
	if err != nil {
		t.Fatalf("eval 1: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("expected 0 transitions on first hit, got %d", len(trs))
	}

	// 第 2 次不可达：触发 critical site_offline
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now.Add(time.Minute),
		Snapshot:     &storage.SiteOpsSnapshot{Reachable: false, ProbeLatencyMS: 600},
		FieldQuality: FieldQualityMap{DomainReachability: QualityTrusted},
	})
	if err != nil {
		t.Fatalf("eval 2: %v", err)
	}
	tr := findTransition(trs, RuleSiteOffline, "site", DeliveryKindAlert)
	if tr == nil || !tr.NewlyTransitioned || tr.Event.Severity != SeverityCritical {
		t.Fatalf("expected critical site_offline firing transition, got %+v", trs)
	}

	// reachability 非 trusted（stale/unavailable）时，即使 Reachable=true 也绝不自动恢复！
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now.Add(2 * time.Minute),
		Snapshot:     &storage.SiteOpsSnapshot{Reachable: true},
		FieldQuality: FieldQualityMap{DomainReachability: QualityStale},
	})
	if err != nil {
		t.Fatalf("eval 3: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("stale reachability must not resolve site_offline, got %+v", trs)
	}

	// 无关域 unauthorized/unavailable，只要 reachability 为 trusted 且 Reachable=true，必须正常自动恢复！
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance: inst,
		Now:      now.Add(3 * time.Minute),
		Snapshot: &storage.SiteOpsSnapshot{Reachable: true, ProbeLatencyMS: 20},
		FieldQuality: FieldQualityMap{
			DomainReachability:    QualityTrusted,
			DomainSystemVersion:   QualityUnauthorized,
			DomainAuthCacheHealth: QualityUnavailable,
		},
	})
	if err != nil {
		t.Fatalf("eval 4: %v", err)
	}
	rec := findTransition(trs, RuleSiteOffline, "site", DeliveryKindRecovery)
	if rec == nil || rec.Event.State != EventStateResolved || rec.Event.ResolvedAt == nil {
		t.Fatalf("expected site_offline recovery despite unrelated domain unauthorized, got %+v", trs)
	}
}

func TestEventAuthInvalidCredGenIsolation(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	inst.CredGen = 2
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	// 旧 cred_gen=1 迟到 401（即使 consecutive>=2）绝不触发！
	trs, err := eval.Evaluate(ctx, store, EvalInput{
		Instance:           inst,
		Now:                now,
		Auth401Consecutive: 3,
		Auth401CredGen:     1,
	})
	if err != nil {
		t.Fatalf("eval stale cred_gen: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("stale cred_gen 401 must not fire, got %+v", trs)
	}

	// 同 cred_gen=2 仅 1 次 401，不触发
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:           inst,
		Now:                now.Add(time.Minute),
		Auth401Consecutive: 1,
		Auth401CredGen:     2,
	})
	if err != nil {
		t.Fatalf("eval 1st 401: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("single 401 must not fire, got %+v", trs)
	}

	// 同 cred_gen=2 连续 2 次 401，触发 auth_invalid
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:           inst,
		Now:                now.Add(2 * time.Minute),
		Auth401Consecutive: 2,
		Auth401CredGen:     2,
	})
	if err != nil {
		t.Fatalf("eval 2nd 401: %v", err)
	}
	if tr := findTransition(trs, RuleAuthInvalid, "site", DeliveryKindAlert); tr == nil || tr.Event.Severity != SeverityCritical {
		t.Fatalf("expected auth_invalid critical alert, got %+v", trs)
	}

	// 换新 Key (cred_gen=3) 但尚未 AuthVerified，且无新鲜通过信号，不应误恢复
	inst.CredGen = 3
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:           inst,
		Now:                now.Add(3 * time.Minute),
		Auth401Consecutive: 2,
		Auth401CredGen:     2, // 旧代际
		AuthVerified:       false,
	})
	if err != nil {
		t.Fatalf("eval after key rotate: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("expected no transition before AuthVerified, got %+v", trs)
	}

	// 当前 cred_gen=3 经 Admin 请求证实 AuthVerified=true → 自动恢复
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now.Add(4 * time.Minute),
		AuthVerified: true,
	})
	if err != nil {
		t.Fatalf("eval verified: %v", err)
	}
	if rec := findTransition(trs, RuleAuthInvalid, "site", DeliveryKindRecovery); rec == nil || rec.Event.State != EventStateResolved {
		t.Fatalf("expected auth_invalid recovery on AuthVerified=true, got %+v", trs)
	}
}

func TestEventInfraDownAndResetInstance(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	snapDown := &storage.SiteOpsSnapshot{DBOK: ptr(true), RedisOK: ptr(false)}
	fq := FieldQualityMap{DomainInfraMetrics: QualityTrusted}

	// 第 1 次失败
	trs, _ := eval.Evaluate(ctx, store, EvalInput{Instance: inst, Now: now, Snapshot: snapDown, FieldQuality: fq})
	if len(trs) != 0 {
		t.Fatalf("expected debounce on hit 1, got %+v", trs)
	}
	// ResetInstance 清空防抖状态，再命中 1 次仍不应触发
	eval.ResetInstance(inst.ID)
	trs, _ = eval.Evaluate(ctx, store, EvalInput{Instance: inst, Now: now.Add(time.Minute), Snapshot: snapDown, FieldQuality: fq})
	if len(trs) != 0 {
		t.Fatalf("expected debounce reset after ResetInstance, got %+v", trs)
	}
	// 连续第 2 次命中 → 触发
	trs, _ = eval.Evaluate(ctx, store, EvalInput{Instance: inst, Now: now.Add(2 * time.Minute), Snapshot: snapDown, FieldQuality: fq})
	if tr := findTransition(trs, RuleInfraDown, "site", DeliveryKindAlert); tr == nil {
		t.Fatalf("expected infra_down alert, got %+v", trs)
	}
	// 恢复（即使无关域 DomainSystemVersion=unauthorized）
	snapOK := &storage.SiteOpsSnapshot{DBOK: ptr(true), RedisOK: ptr(true)}
	trs, _ = eval.Evaluate(ctx, store, EvalInput{
		Instance: inst,
		Now:      now.Add(3 * time.Minute),
		Snapshot: snapOK,
		FieldQuality: FieldQualityMap{
			DomainInfraMetrics:  QualityTrusted,
			DomainSystemVersion: QualityUnauthorized,
		},
	})
	if rec := findTransition(trs, RuleInfraDown, "site", DeliveryKindRecovery); rec == nil {
		t.Fatalf("expected infra_down recovery, got %+v", trs)
	}
}

func TestEventGroupPoolExhaustedBlocksUnverifiedZeroAndStale(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	unverifiedGroup := []GroupCapacityItem{{
		GroupID:              5,
		GroupName:            "claude-vip",
		TrustState:           QualityUnverifiedZero,
		AccountsTotal:        10,
		AccountsOpsAvailable: 0,
	}}
	// unverified_zero 连续 3 次也绝不能触发 group_pool_exhausted
	for i := 0; i < 3; i++ {
		trs, err := eval.Evaluate(ctx, store, EvalInput{
			Instance: inst,
			Now:      now.Add(time.Duration(i) * time.Minute),
			Snapshot: &storage.SiteOpsSnapshot{RedisOK: ptr(true)},
			FieldQuality: FieldQualityMap{
				DomainAccountAvailability: QualityTrusted,
				DomainInfraMetrics:        QualityTrusted,
			},
			Groups: unverifiedGroup,
		})
		if err != nil {
			t.Fatalf("eval unverified %d: %v", i, err)
		}
		if len(trs) != 0 {
			t.Fatalf("unverified_zero must never fire group_pool_exhausted, got %+v", trs)
		}
	}

	// redis_ok=false 时也绝不能触发 group_pool_exhausted
	trustedExhausted := []GroupCapacityItem{{
		GroupID:              5,
		GroupName:            "claude-vip",
		TrustState:           QualityTrusted,
		AccountsTotal:        10,
		AccountsOpsAvailable: 0,
	}}
	for i := 0; i < 2; i++ {
		trs, _ := eval.Evaluate(ctx, store, EvalInput{
			Instance: inst,
			Now:      now.Add(time.Duration(10+i) * time.Minute),
			Snapshot: &storage.SiteOpsSnapshot{RedisOK: ptr(false), DBOK: ptr(true)},
			FieldQuality: FieldQualityMap{
				DomainAccountAvailability: QualityTrusted,
				DomainInfraMetrics:        QualityTrusted,
			},
			Groups: trustedExhausted,
		})
		if tr := findTransition(trs, RuleGroupPoolExhausted, "group:5", DeliveryKindAlert); tr != nil {
			t.Fatalf("redis_ok=false must not fire group_pool_exhausted")
		}
	}

	// 新实例测试正常 trusted + redis_ok=true 连续 2 次触发
	store2 := newMemEventStore()
	eval2 := NewEventEvaluator()
	fqTrusted := FieldQualityMap{
		DomainAccountAvailability: QualityTrusted,
		DomainInfraMetrics:        QualityTrusted,
	}
	eval2.Evaluate(ctx, store2, EvalInput{
		Instance: inst, Now: now, Snapshot: &storage.SiteOpsSnapshot{RedisOK: ptr(true), DBOK: ptr(true)},
		FieldQuality: fqTrusted, Groups: trustedExhausted,
	})
	trs, _ := eval2.Evaluate(ctx, store2, EvalInput{
		Instance: inst, Now: now.Add(time.Minute), Snapshot: &storage.SiteOpsSnapshot{RedisOK: ptr(true), DBOK: ptr(true)},
		FieldQuality: fqTrusted, Groups: trustedExhausted,
	})
	if tr := findTransition(trs, RuleGroupPoolExhausted, "group:5", DeliveryKindAlert); tr == nil {
		t.Fatalf("expected group_pool_exhausted alert for group:5, got %+v", trs)
	}

	// 处于 firing 状态时，unverified_zero 或 stale 样本即使显示 available=5 也绝不自动恢复！
	for _, badState := range []FieldQualityState{QualityUnverifiedZero, QualityStale} {
		trs, _ = eval2.Evaluate(ctx, store2, EvalInput{
			Instance: inst, Now: now.Add(2 * time.Minute), Snapshot: &storage.SiteOpsSnapshot{RedisOK: ptr(true), DBOK: ptr(true)},
			FieldQuality: fqTrusted,
			Groups: []GroupCapacityItem{{
				GroupID: 5, GroupName: "claude-vip", TrustState: badState, AccountsTotal: 10, AccountsOpsAvailable: 5,
			}},
		})
		if len(trs) != 0 {
			t.Fatalf("%s must not resolve group_pool_exhausted, got %+v", badState, trs)
		}
	}

	// 仅当新鲜 trusted + redis_ok=true + available > 0 时才自动恢复
	trs, _ = eval2.Evaluate(ctx, store2, EvalInput{
		Instance: inst, Now: now.Add(3 * time.Minute), Snapshot: &storage.SiteOpsSnapshot{RedisOK: ptr(true), DBOK: ptr(true)},
		FieldQuality: fqTrusted,
		Groups: []GroupCapacityItem{{
			GroupID: 5, GroupName: "claude-vip", TrustState: QualityTrusted, AccountsTotal: 10, AccountsOpsAvailable: 4,
		}},
	})
	if rec := findTransition(trs, RuleGroupPoolExhausted, "group:5", DeliveryKindRecovery); rec == nil {
		t.Fatalf("expected group_pool_exhausted recovery, got %+v", trs)
	}
}

func TestEventSLADegradedConcurrencySwitchResource(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	fqAll := FieldQualityMap{
		DomainOpsOverview:  QualityTrusted,
		DomainInfraMetrics: QualityTrusted,
		DomainConcurrency:  QualityTrusted,
	}

	// 1) sla_degraded: 样本不足 (<20) 不触发
	for i := 0; i < 3; i++ {
		trs, _ := eval.Evaluate(ctx, store, EvalInput{
			Instance: inst, Now: now.Add(time.Duration(i) * time.Minute),
			Snapshot: &storage.SiteOpsSnapshot{
				ReqTotal1h: ptr(int64(10)), SLAPercent1h: ptr(50.0), UpstreamErrorRatePct1h: ptr(40.0),
			},
			FieldQuality: fqAll,
		})
		if tr := findTransition(trs, RuleSLADegraded, "site", DeliveryKindAlert); tr != nil {
			t.Fatalf("low sample count must not fire sla_degraded")
		}
	}

	// 2) 连续 2 次触发 sla_degraded & high_switch_spike；连续 3 次触发 concurrency_saturated(site + group:7) & resource_pressure
	snapBad := &storage.SiteOpsSnapshot{
		ReqTotal1h:             ptr(int64(100)),
		SLAPercent1h:           ptr(91.0),
		UpstreamErrorRatePct1h: ptr(12.0),
		ConcUsed:               ptr(int64(90)),
		ConcMax:                ptr(int64(100)),
		QueueDepth:             ptr(2),
		AccountSwitchCount1m:   ptr(int64(6)),
		QPSCurrent:             ptr(0.5), // 0.5*60=30 >= 20, 6/30=20% > 15%
		CPUPercent:             ptr(88.0),
		MemPercent:             ptr(60.0),
		DBConnActive:           ptr(10),
		DBMaxOpenConns:         ptr(50),
		DBConnWaiting:          ptr(0),
	}
	badGroups := []GroupCapacityItem{{
		GroupID: 7, GroupName: "openai-std", TrustState: QualityTrusted,
		ConcUsed: ptr(int64(18)), ConcMax: ptr(int64(20)), LoadPercent: ptr(90.0),
	}}

	// 第 1 次
	trs1, _ := eval.Evaluate(ctx, store, EvalInput{Instance: inst, Now: now.Add(10 * time.Minute), Snapshot: snapBad, FieldQuality: fqAll, Groups: badGroups})
	if len(trs1) != 0 {
		t.Fatalf("hit 1 should not trigger 2x/3x rules, got %+v", trs1)
	}
	// 第 2 次：sla_degraded 和 high_switch_spike 应触发，3x 规则未触发
	trs2, _ := eval.Evaluate(ctx, store, EvalInput{Instance: inst, Now: now.Add(11 * time.Minute), Snapshot: snapBad, FieldQuality: fqAll, Groups: badGroups})
	if findTransition(trs2, RuleSLADegraded, "site", DeliveryKindAlert) == nil {
		t.Fatalf("expected sla_degraded on hit 2, got %+v", trs2)
	}
	if findTransition(trs2, RuleHighSwitchSpike, "site", DeliveryKindAlert) == nil {
		t.Fatalf("expected high_switch_spike on hit 2, got %+v", trs2)
	}
	if findTransition(trs2, RuleConcurrencySaturated, "site", DeliveryKindAlert) != nil ||
		findTransition(trs2, RuleResourcePressure, "site", DeliveryKindAlert) != nil {
		t.Fatalf("3x rules must not trigger on hit 2, got %+v", trs2)
	}

	// 第 3 次：concurrency_saturated(site + group:7) 和 resource_pressure 应触发，已 firing 的 sla/switch 不重复发
	trs3, _ := eval.Evaluate(ctx, store, EvalInput{Instance: inst, Now: now.Add(12 * time.Minute), Snapshot: snapBad, FieldQuality: fqAll, Groups: badGroups})
	if findTransition(trs3, RuleConcurrencySaturated, "site", DeliveryKindAlert) == nil {
		t.Fatalf("expected site concurrency_saturated on hit 3, got %+v", trs3)
	}
	if findTransition(trs3, RuleConcurrencySaturated, "group:7", DeliveryKindAlert) == nil {
		t.Fatalf("expected group:7 concurrency_saturated on hit 3, got %+v", trs3)
	}
	if findTransition(trs3, RuleResourcePressure, "site", DeliveryKindAlert) == nil {
		t.Fatalf("expected resource_pressure on hit 3, got %+v", trs3)
	}
	if findTransition(trs3, RuleSLADegraded, "site", DeliveryKindAlert) != nil {
		t.Fatalf("already firing sla_degraded must not emit duplicate alert")
	}

	// 恢复全部（同时夹杂无关域 unauthorized 证明不阻断）
	snapGood := &storage.SiteOpsSnapshot{
		ReqTotal1h:             ptr(int64(100)),
		SLAPercent1h:           ptr(99.5),
		UpstreamErrorRatePct1h: ptr(0.5),
		ConcUsed:               ptr(int64(20)),
		ConcMax:                ptr(int64(100)),
		QueueDepth:             ptr(0),
		AccountSwitchCount1m:   ptr(int64(1)),
		QPSCurrent:             ptr(1.0),
		CPUPercent:             ptr(30.0),
		MemPercent:             ptr(40.0),
		DBConnActive:           ptr(5),
		DBMaxOpenConns:         ptr(50),
		DBConnWaiting:          ptr(0),
	}
	goodGroups := []GroupCapacityItem{{
		GroupID: 7, GroupName: "openai-std", TrustState: QualityTrusted,
		ConcUsed: ptr(int64(4)), ConcMax: ptr(int64(20)), LoadPercent: ptr(20.0),
	}}
	fqRecover := FieldQualityMap{
		DomainOpsOverview:     QualityTrusted,
		DomainInfraMetrics:    QualityTrusted,
		DomainConcurrency:     QualityTrusted,
		DomainSystemVersion:   QualityUnauthorized,
		DomainAuthCacheHealth: QualityUnavailable,
	}
	trsRec, _ := eval.Evaluate(ctx, store, EvalInput{Instance: inst, Now: now.Add(13 * time.Minute), Snapshot: snapGood, FieldQuality: fqRecover, Groups: goodGroups})
	for _, want := range []struct{ rule, res string }{
		{RuleSLADegraded, "site"},
		{RuleHighSwitchSpike, "site"},
		{RuleConcurrencySaturated, "site"},
		{RuleConcurrencySaturated, "group:7"},
		{RuleResourcePressure, "site"},
	} {
		if findTransition(trsRec, want.rule, want.res, DeliveryKindRecovery) == nil {
			t.Fatalf("expected recovery for %s/%s, got %+v", want.rule, want.res, trsRec)
		}
	}
}

func TestEventJobAndAuthCacheDomainSeparation(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	// 同时触发 job:agg_metrics 与 auth_cache（1 次即触发）
	trs, err := eval.Evaluate(ctx, store, EvalInput{
		Instance: inst,
		Now:      now,
		Detail: SnapshotDetailPayload{
			JobHeartbeats: []SanitizedJobHeartbeat{
				{JobName: "agg_metrics", Status: "failed", HasError: true, ErrorSummary: "timeout"},
			},
		},
		AuthCacheHealth: &SanitizedAuthCacheHealth{Healthy: false, SubscriberOK: false, SubscriberError: "redis pubsub lost"},
		FieldQuality: FieldQualityMap{
			DomainOpsOverview:     QualityTrusted,
			DomainAuthCacheHealth: QualityTrusted,
		},
	})
	if err != nil {
		t.Fatalf("eval 1: %v", err)
	}
	trJob := findTransition(trs, RuleJobOrCacheUnhealthy, "job:agg_metrics", DeliveryKindAlert)
	trCache := findTransition(trs, RuleJobOrCacheUnhealthy, "auth_cache", DeliveryKindAlert)
	if trJob == nil || trCache == nil {
		t.Fatalf("expected both job:agg_metrics and auth_cache alerts, got %+v", trs)
	}
	var domains []string
	_ = json.Unmarshal([]byte(trCache.Event.RequiredDomainsJSON), &domains)
	if len(domains) != 1 || domains[0] != DomainAuthCacheHealth {
		t.Fatalf("auth_cache required domains mismatch: %v", domains)
	}

	// L2 本轮未采集 auth_cache（nil + unavailable），但 ops_overview 正常且 job 恢复 →
	// job:agg_metrics 必须恢复，auth_cache 必须保持 firing！
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance: inst,
		Now:      now.Add(time.Minute),
		Detail: SnapshotDetailPayload{
			JobHeartbeats: []SanitizedJobHeartbeat{
				{JobName: "agg_metrics", Status: "ok", HasError: false},
			},
		},
		AuthCacheHealth: nil,
		FieldQuality: FieldQualityMap{
			DomainOpsOverview:     QualityTrusted,
			DomainAuthCacheHealth: QualityUnavailable,
		},
	})
	if err != nil {
		t.Fatalf("eval 2: %v", err)
	}
	if findTransition(trs, RuleJobOrCacheUnhealthy, "job:agg_metrics", DeliveryKindRecovery) == nil {
		t.Fatalf("expected job:agg_metrics recovery, got %+v", trs)
	}
	if findTransition(trs, RuleJobOrCacheUnhealthy, "auth_cache", DeliveryKindRecovery) != nil {
		t.Fatalf("auth_cache must remain firing when L2 not collected")
	}
	active, _ := store.ListActiveEvents(ctx, inst.ID)
	if len(active) != 1 || active[0].ResourceKey != "auth_cache" {
		t.Fatalf("expected only auth_cache active, got %+v", active)
	}

	// L2 采集到 auth_cache Healthy=true 且 domain trusted → 自动恢复
	trs, _ = eval.Evaluate(ctx, store, EvalInput{
		Instance:        inst,
		Now:             now.Add(5 * time.Minute),
		AuthCacheHealth: &SanitizedAuthCacheHealth{Healthy: true, OutboxRunning: true, SubscriberOK: true},
		FieldQuality:    FieldQualityMap{DomainAuthCacheHealth: QualityTrusted},
	})
	if findTransition(trs, RuleJobOrCacheUnhealthy, "auth_cache", DeliveryKindRecovery) == nil {
		t.Fatalf("expected auth_cache recovery, got %+v", trs)
	}
}

func TestEventAccountErrorExcludesPure429AndRateLimitWaveDuration(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	fq := FieldQualityMap{DomainAccountAvailability: QualityTrusted}

	// 纯 429 限流（无论 HasError=false 还是 HasError=true 且摘要仅提示 429/rate_limit）绝不触发 account_error_new！
	accountsWave := []SanitizedAccountAvailability{
		{AccountID: 101, Platform: "anthropic", Available: false, RateLimited: true, HasError: false},
		{AccountID: 102, Platform: "anthropic", Available: false, RateLimited: true, HasError: true, ErrorSummary: "HTTP 429 rate_limit exceeded"},
		{AccountID: 103, Platform: "openai", Available: true, RateLimited: false, HasError: false},
	}
	// 2/3 = 66.7% >= 50%，但在 t=0 刚超过阈值，持续 < 5 分钟不应触发 account_rate_limit_wave
	trs, err := eval.Evaluate(ctx, store, EvalInput{
		Instance: inst, Now: now, FieldQuality: fq, Accounts: accountsWave,
	})
	if err != nil {
		t.Fatalf("eval t=0: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("pure 429 must not fire account_error_new and wave <5m must not fire, got %+v", trs)
	}

	// t=4m 仍不足 5 分钟；同时新增一个真正硬错误账号 #104（含 401）
	accountsT4 := append(append([]SanitizedAccountAvailability(nil), accountsWave...), SanitizedAccountAvailability{
		AccountID: 104, Platform: "anthropic", Available: false, RateLimited: true, HasError: true, ErrorSummary: "HTTP 401 unauthorized invalid_api_key",
	})
	// 3/4 = 75% >= 50%
	trs, _ = eval.Evaluate(ctx, store, EvalInput{
		Instance: inst, Now: now.Add(4 * time.Minute), FieldQuality: fq, Accounts: accountsT4,
	})
	if findTransition(trs, RuleAccountErrorNew, "account:104", DeliveryKindAlert) == nil {
		t.Fatalf("expected account_error_new for real 401 error account:104, got %+v", trs)
	}
	if findTransition(trs, RuleAccountRateLimitWave, "site", DeliveryKindAlert) != nil {
		t.Fatalf("rate limit wave at 4m must not trigger before 5m")
	}

	// t=5m 满 5 分钟 → 触发 account_rate_limit_wave；#104 已在 firing 不重复触发
	trs, _ = eval.Evaluate(ctx, store, EvalInput{
		Instance: inst, Now: now.Add(5 * time.Minute), FieldQuality: fq, Accounts: accountsT4,
	})
	if findTransition(trs, RuleAccountRateLimitWave, "site", DeliveryKindAlert) == nil {
		t.Fatalf("expected account_rate_limit_wave at 5m, got %+v", trs)
	}
	if findTransition(trs, RuleAccountErrorNew, "account:104", DeliveryKindAlert) != nil {
		t.Fatalf("already firing account:104 must not emit duplicate alert")
	}

	// 非 trusted（stale）时两者均保持 firing，不自动恢复
	trs, _ = eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now.Add(6 * time.Minute),
		FieldQuality: FieldQualityMap{DomainAccountAvailability: QualityStale},
		Accounts: []SanitizedAccountAvailability{
			{AccountID: 101, Available: true},
			{AccountID: 104, Available: true},
		},
	})
	if len(trs) != 0 {
		t.Fatalf("stale availability must not resolve account alerts, got %+v", trs)
	}

	// 新鲜 trusted 样本：#104 错误清除，限流比例回落至 0% → 两者自动恢复
	trs, _ = eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now.Add(7 * time.Minute),
		FieldQuality: fq,
		Accounts: []SanitizedAccountAvailability{
			{AccountID: 101, Available: true},
			{AccountID: 102, Available: true},
			{AccountID: 103, Available: true},
			{AccountID: 104, Available: true, HasError: false},
		},
	})
	if findTransition(trs, RuleAccountErrorNew, "account:104", DeliveryKindRecovery) == nil {
		t.Fatalf("expected account:104 recovery, got %+v", trs)
	}
	if findTransition(trs, RuleAccountRateLimitWave, "site", DeliveryKindRecovery) == nil {
		t.Fatalf("expected account_rate_limit_wave recovery, got %+v", trs)
	}
}

func TestEventPoolCannotSustainResetAndSameDomainRecovery(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	eval := NewEventEvaluator()
	inst := testInstance()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	riskyForecast := &PoolForecastResponse{
		InstanceID: inst.ID,
		TrustState: QualityTrusted,
		Groups: []GroupForecastRiskView{
			{
				GroupID:              9,
				GroupName:            "claude-max-pool",
				Platform:             "anthropic",
				ProvenAccounts:       5,
				ExhaustedCount:       1,
				WillExhaustHighCount: 2,
				SafeOrIdleCount:      2,
				HighRiskRatioPct:     ptr(60.0),
				CannotSustainRisk:    true,
				RiskNote:             "高危占比 60.0%",
			},
		},
	}

	// 1. DomainPoolForecast 非 trusted（stale）时，即使 CannotSustainRisk=true 也绝不触发
	trs, err := eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now,
		FieldQuality: FieldQualityMap{DomainPoolForecast: QualityStale},
		PoolForecast: riskyForecast,
	})
	if err != nil {
		t.Fatalf("eval stale: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("stale pool_forecast must not trigger alert, got %+v", trs)
	}

	// 2. DomainPoolForecast == QualityTrusted 时，连续 1 次即触发 warning 告警
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now.Add(time.Minute),
		FieldQuality: FieldQualityMap{DomainPoolForecast: QualityTrusted},
		PoolForecast: riskyForecast,
	})
	if err != nil {
		t.Fatalf("eval fire: %v", err)
	}
	tr := findTransition(trs, RulePoolCannotSustainReset, "group:9", DeliveryKindAlert)
	if tr == nil || tr.Event.Severity != SeverityWarning || !tr.NewlyTransitioned {
		t.Fatalf("expected warning pool_cannot_sustain_reset alert for group:9, got %+v", trs)
	}
	var reqDomains []string
	_ = json.Unmarshal([]byte(tr.Event.RequiredDomainsJSON), &reqDomains)
	if len(reqDomains) != 1 || reqDomains[0] != DomainPoolForecast {
		t.Fatalf("expected required domains [%s], got %v", DomainPoolForecast, reqDomains)
	}

	// 3. 非 trusted（stale）时即使 CannotSustainRisk=false 也绝不自动恢复
	healthyForecast := &PoolForecastResponse{
		InstanceID: inst.ID,
		TrustState: QualityTrusted,
		Groups: []GroupForecastRiskView{
			{
				GroupID:           9,
				GroupName:         "claude-max-pool",
				Platform:          "anthropic",
				ProvenAccounts:    5,
				SafeOrIdleCount:   5,
				HighRiskRatioPct:  ptr(0.0),
				CannotSustainRisk: false,
			},
		},
	}
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance:     inst,
		Now:          now.Add(2 * time.Minute),
		FieldQuality: FieldQualityMap{DomainPoolForecast: QualityStale},
		PoolForecast: healthyForecast,
	})
	if err != nil {
		t.Fatalf("eval stale recovery: %v", err)
	}
	if len(trs) != 0 {
		t.Fatalf("stale pool_forecast must not resolve alert, got %+v", trs)
	}

	// 4. 同域 DomainPoolForecast == QualityTrusted 且 CannotSustainRisk=false（即使无关域 unauthorized）→ 自动恢复
	trs, err = eval.Evaluate(ctx, store, EvalInput{
		Instance: inst,
		Now:      now.Add(3 * time.Minute),
		FieldQuality: FieldQualityMap{
			DomainPoolForecast:  QualityTrusted,
			DomainSystemVersion: QualityUnauthorized,
		},
		PoolForecast: healthyForecast,
	})
	if err != nil {
		t.Fatalf("eval recovery: %v", err)
	}
	rec := findTransition(trs, RulePoolCannotSustainReset, "group:9", DeliveryKindRecovery)
	if rec == nil || rec.Event.State != EventStateResolved || rec.Event.ResolvedAt == nil {
		t.Fatalf("expected pool_cannot_sustain_reset recovery for group:9, got %+v", trs)
	}
}

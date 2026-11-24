package siteops

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

type sentRecord struct {
	channelID uint
	subject   string
	body      string
}

type mockDirectNotifier struct {
	mu       sync.Mutex
	channels []storage.NotificationChannel
	failIDs  map[uint]error
	sent     []sentRecord
	counts   map[uint]int
}

func newMockDirectNotifier(channels []storage.NotificationChannel) *mockDirectNotifier {
	return &mockDirectNotifier{
		channels: channels,
		failIDs:  make(map[uint]error),
		counts:   make(map[uint]int),
	}
}

func (m *mockDirectNotifier) ListEnabledChannels() ([]storage.NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.NotificationChannel
	for _, ch := range m.channels {
		if ch.Enabled {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (m *mockDirectNotifier) SendDirect(_ context.Context, channelID uint, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[channelID]++
	if err, shouldFail := m.failIDs[channelID]; shouldFail && err != nil {
		return err
	}
	m.sent = append(m.sent, sentRecord{channelID: channelID, subject: subject, body: body})
	return nil
}

func (m *mockDirectNotifier) attemptCount(channelID uint) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[channelID]
}

func TestAlertDispatcherZeroBroadcastDefaultDeny(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	channels := []storage.NotificationChannel{
		{ID: 1, Name: "legacy-empty", Enabled: true, Subscriptions: "[]", SiteOpsSubscriptions: ""},
		{ID: 2, Name: "bad-json", Enabled: true, Subscriptions: "[]", SiteOpsSubscriptions: "{broken"},
		{ID: 3, Name: "disabled-sub", Enabled: true, SiteOpsSubscriptions: `{"enabled":false,"instance_ids":["*"],"events":["*"]}`},
		{ID: 4, Name: "empty-events", Enabled: true, SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["1"],"events":[]}`},
		{ID: 5, Name: "other-instance", Enabled: true, SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["2"],"events":["site_offline"]}`},
		{ID: 6, Name: "other-event", Enabled: true, SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["1"],"events":["sla_degraded"]}`},
		{ID: 7, Name: "channel-disabled", Enabled: false, SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["*"],"events":["*"]}`},
	}
	notifier := newMockDirectNotifier(channels)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	dispatcher := NewAlertDispatcher(store, notifier)
	dispatcher.Now = func() time.Time { return now }

	inst := storage.SiteOpsInstance{ID: 1, Name: "portal-main"}
	transitions := []EventTransition{{
		Event: storage.SiteOpsEvent{
			ID:              10,
			InstanceID:      1,
			Severity:        SeverityCritical,
			RuleKey:         RuleSiteOffline,
			ResourceKey:     "site",
			State:           EventStateFiring,
			ConsecutiveHits: 2,
			Title:           "站点 HTTP 探活不可达",
			FiredAt:         now,
		},
		Kind:              DeliveryKindAlert,
		NewlyTransitioned: true,
	}}

	deliveries, err := dispatcher.DispatchTransitions(ctx, inst, DefaultAlertConfig(), transitions)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("expected 0 deliveries for default-deny channels, got %d", len(deliveries))
	}
	if len(notifier.sent) != 0 {
		t.Fatalf("expected zero broadcast, got %+v", notifier.sent)
	}
}

func TestAlertDispatcherPartialFailureAndRestartResume(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	subAll := `{"enabled":true,"instance_ids":["1"],"events":["site_offline"]}`
	channels := []storage.NotificationChannel{
		{ID: 11, Name: "tg-1", Enabled: true, SiteOpsSubscriptions: subAll},
		{ID: 12, Name: "webhook-2", Enabled: true, SiteOpsSubscriptions: subAll},
		{ID: 13, Name: "feishu-3", Enabled: true, SiteOpsSubscriptions: subAll},
	}
	notifier := newMockDirectNotifier(channels)
	// 第 2 个渠道首轮发送失败，返回含敏感 token 的错误信息以验证脱敏。
	notifier.failIDs[12] = errors.New("post failed with Bearer secret-token-xyz")

	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	dispatcher1 := NewAlertDispatcher(store, notifier)
	dispatcher1.Now = func() time.Time { return clock }

	inst := storage.SiteOpsInstance{ID: 1, Name: "portal-main"}
	tr := EventTransition{
		Event: storage.SiteOpsEvent{
			ID:              20,
			InstanceID:      1,
			Severity:        SeverityCritical,
			RuleKey:         RuleSiteOffline,
			ResourceKey:     "site",
			State:           EventStateFiring,
			ConsecutiveHits: 2,
			Title:           "站点 HTTP 探活不可达",
			Summary:         "连续 2 次不可达",
			FiredAt:         clock,
		},
		Kind:              DeliveryKindAlert,
		NewlyTransitioned: true,
	}

	deliveries, err := dispatcher1.DispatchTransitions(ctx, inst, DefaultAlertConfig(), []EventTransition{tr})
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if len(deliveries) != 3 {
		t.Fatalf("expected 3 delivery records, got %d", len(deliveries))
	}

	byChannel := make(map[uint]storage.SiteOpsAlertDelivery)
	for _, d := range deliveries {
		byChannel[d.NotifyChannelID] = d
	}
	if byChannel[11].Status != DeliveryStatusSent || byChannel[13].Status != DeliveryStatusSent {
		t.Fatalf("channels 11 & 13 must be sent, got 11=%s 13=%s", byChannel[11].Status, byChannel[13].Status)
	}
	d12 := byChannel[12]
	if d12.Status != DeliveryStatusFailed || d12.Attempts != 1 || d12.NextRetryAt == nil {
		t.Fatalf("channel 12 must be failed with next_retry_at, got %+v", d12)
	}
	if strings.Contains(d12.LastError, "secret-token-xyz") {
		t.Fatalf("last_error must be sanitized, got %q", d12.LastError)
	}
	if d12.CooldownUntil == nil || !d12.CooldownUntil.Equal(clock.Add(30*time.Minute)) {
		t.Fatalf("cooldown_until must be persisted on delivery creation, got %v", d12.CooldownUntil)
	}

	// 模拟进程重启：新建一个全新的 AlertDispatcher，时间推进到退避窗口后且渠道 12 恢复正常。
	delete(notifier.failIDs, 12)
	clock = clock.Add(45 * time.Second)
	dispatcherAfterRestart := NewAlertDispatcher(store, notifier)
	dispatcherAfterRestart.Now = func() time.Time { return clock }

	processed, err := dispatcherAfterRestart.ProcessDueDeliveries(ctx, 20)
	if err != nil {
		t.Fatalf("process due after restart: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected only 1 failed delivery retried after restart, got %d", processed)
	}

	// 严格验证：渠道 11、13 发送次数仍为 1（绝不重复轰炸）；渠道 12 变为 sent 且总尝试 2 次！
	if notifier.attemptCount(11) != 1 || notifier.attemptCount(13) != 1 {
		t.Fatalf("sent channels must never be resent: ch11=%d ch13=%d", notifier.attemptCount(11), notifier.attemptCount(13))
	}
	if notifier.attemptCount(12) != 2 {
		t.Fatalf("channel 12 should be attempted exactly twice, got %d", notifier.attemptCount(12))
	}
	updated12 := store.deliveries[d12.ID]
	if updated12.Status != DeliveryStatusSent || updated12.SentAt == nil || updated12.NextRetryAt != nil {
		t.Fatalf("channel 12 should transition to sent after retry, got %+v", updated12)
	}
}

func TestAlertDispatcherCooldownAndRecoveryBypass(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	channels := []storage.NotificationChannel{
		{ID: 21, Name: "ops-tg", Enabled: true, SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["1"],"events":["sla_degraded"]}`},
	}
	notifier := newMockDirectNotifier(channels)
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	dispatcher := NewAlertDispatcher(store, notifier)
	dispatcher.Now = func() time.Time { return clock }
	inst := storage.SiteOpsInstance{ID: 1, Name: "portal-main"}
	cfg := DefaultAlertConfig()
	cfg.CooldownMinutes = 30

	alertTr := EventTransition{
		Event: storage.SiteOpsEvent{
			ID: 30, InstanceID: 1, Severity: SeverityWarning, RuleKey: RuleSLADegraded,
			ResourceKey: "site", State: EventStateFiring, ConsecutiveHits: 2, Title: "SLA 降级", FiredAt: clock,
		},
		Kind:              DeliveryKindAlert,
		NewlyTransitioned: true,
	}

	// 第 1 次告警：正常发送
	d1, err := dispatcher.DispatchTransitions(ctx, inst, cfg, []EventTransition{alertTr})
	if err != nil || len(d1) != 1 || d1[0].Status != DeliveryStatusSent {
		t.Fatalf("first alert should be sent, got %+v err=%v", d1, err)
	}

	// 10 分钟后（仍在 30m 冷却期内）再次触发同一 rule:resource → 生成 skipped_cooldown，不重复发送！
	clock = clock.Add(10 * time.Minute)
	alertTr2 := alertTr
	alertTr2.Event.ID = 31
	d2, err := dispatcher.DispatchTransitions(ctx, inst, cfg, []EventTransition{alertTr2})
	if err != nil || len(d2) != 1 || d2[0].Status != DeliveryStatusSkippedCooldown {
		t.Fatalf("second alert in cooldown should be skipped_cooldown, got %+v err=%v", d2, err)
	}
	if notifier.attemptCount(21) != 1 {
		t.Fatalf("skipped_cooldown must not call SendDirect, attempts=%d", notifier.attemptCount(21))
	}

	// 恢复通知在冷却期内不受阻塞，必须正常发出！
	resolvedAt := clock
	recTr := EventTransition{
		Event: storage.SiteOpsEvent{
			ID: 31, InstanceID: 1, Severity: SeverityWarning, RuleKey: RuleSLADegraded,
			ResourceKey: "site", State: EventStateResolved, Title: "SLA 降级", ResolvedAt: &resolvedAt,
		},
		Kind:              DeliveryKindRecovery,
		NewlyTransitioned: true,
	}
	dRec, err := dispatcher.DispatchTransitions(ctx, inst, cfg, []EventTransition{recTr})
	if err != nil || len(dRec) != 1 || dRec[0].Status != DeliveryStatusSent || dRec[0].DeliveryKind != DeliveryKindRecovery {
		t.Fatalf("recovery must bypass alert cooldown and send, got %+v err=%v", dRec, err)
	}
	if notifier.attemptCount(21) != 2 {
		t.Fatalf("expected 2 sends (1 alert + 1 recovery), got %d", notifier.attemptCount(21))
	}
}

func TestAlertDispatcherDailyReportAndMaxAttempts(t *testing.T) {
	ctx := context.Background()
	store := newMemEventStore()
	channels := []storage.NotificationChannel{
		{ID: 31, Name: "daily-ch", Enabled: true, SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["1"],"events":["daily_report"]}`},
		{ID: 32, Name: "alert-only-ch", Enabled: true, SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["1"],"events":["site_offline"]}`},
	}
	notifier := newMockDirectNotifier(channels)
	notifier.failIDs[31] = errors.New("network unreachable")

	clock := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	dispatcher := NewAlertDispatcher(store, notifier)
	dispatcher.Now = func() time.Time { return clock }
	dispatcher.MaxAttempts = 3

	inst := storage.SiteOpsInstance{ID: 1, Name: "portal-main"}
	preview := DailyReportPreview{
		InstanceID: 1, InstanceName: "portal-main", DateUTC8: "2026-11-23",
		Subject: "[站点运维日报] portal-main · 2026-11-23", Body: "汇总指标",
	}

	matched, delivs, err := dispatcher.DispatchDailyReport(ctx, inst, preview)
	if err != nil {
		t.Fatalf("dispatch daily report: %v", err)
	}
	if matched != 1 || len(delivs) != 1 {
		t.Fatalf("expected 1 matched daily_report channel, got matched=%d len=%d", matched, len(delivs))
	}
	if delivs[0].EventID != 0 || delivs[0].DeliveryKind != DeliveryKindDailyReport || delivs[0].CooldownKey != "daily_report:2026-11-23" {
		t.Fatalf("unexpected daily report delivery metadata: %+v", delivs[0])
	}
	if notifier.attemptCount(32) != 0 {
		t.Fatalf("alert-only channel must not receive daily_report")
	}

	// 连续重试直到达到 MaxAttempts=3 后停止重试（NextRetryAt 置 nil）
	for step := 0; step < 3; step++ {
		clock = clock.Add(10 * time.Minute)
		_, _ = dispatcher.ProcessDueDeliveries(ctx, 10)
	}
	finalRec := store.deliveries[delivs[0].ID]
	if finalRec.Attempts != 3 || finalRec.NextRetryAt != nil || finalRec.Status != DeliveryStatusFailed {
		t.Fatalf("delivery should stop retrying after MaxAttempts=3, got %+v", finalRec)
	}
}

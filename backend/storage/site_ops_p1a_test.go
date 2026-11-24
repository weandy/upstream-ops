package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSiteOpsEventStoreLifecycleAndChunkedRetention(t *testing.T) {
	s := newTestSiteOpsStore(t)
	ctx := context.Background()
	inst := createTestSiteOpsInstance(t, s, "portal-events", true, 0)
	other := createTestSiteOpsInstance(t, s, "other-events", true, 1)

	base := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)

	evt1 := &SiteOpsEvent{
		InstanceID:          inst.ID,
		Severity:            "critical",
		RuleKey:             "site_offline",
		ResourceKey:         "site",
		RequiredDomainsJSON: `["reachability"]`,
		State:               "firing",
		ConsecutiveHits:     2,
		Title:               "站点不可达",
		Summary:             "连续 2 次探测失败",
		DetailJSON:          `{"code":"SITE_OPS_UPSTREAM_ERROR"}`,
		FiredAt:             base,
		CreatedAt:           base,
	}
	if err := s.UpsertEvent(ctx, evt1); err != nil || evt1.ID == 0 {
		t.Fatalf("UpsertEvent create evt1: %v, id=%d", err, evt1.ID)
	}

	evt2 := &SiteOpsEvent{
		InstanceID:          inst.ID,
		Severity:            "warning",
		RuleKey:             "sla_degraded",
		ResourceKey:         "site",
		RequiredDomainsJSON: `["ops_overview"]`,
		State:               "firing",
		ConsecutiveHits:     1,
		Title:               "SLA 下降",
		Summary:             "1h SLA 低于 95%",
		FiredAt:             base.Add(time.Minute),
		CreatedAt:           base.Add(time.Minute),
	}
	if err := s.UpsertEvent(ctx, evt2); err != nil || evt2.ID == 0 {
		t.Fatalf("UpsertEvent create evt2: %v, id=%d", err, evt2.ID)
	}

	evtOther := &SiteOpsEvent{
		InstanceID: inst.ID,
		Severity:   "info",
		RuleKey:    "account_error_new",
		State:      "resolved",
		Title:      "旧恢复事件",
		FiredAt:    base.Add(-2 * time.Hour),
		CreatedAt:  base.Add(-2 * time.Hour),
	}
	if err := s.UpsertEvent(ctx, evtOther); err != nil {
		t.Fatalf("UpsertEvent create evtOther: %v", err)
	}
	if err := s.UpsertEvent(ctx, &SiteOpsEvent{
		InstanceID: other.ID,
		Severity:   "critical",
		RuleKey:    "site_offline",
		State:      "firing",
		FiredAt:    base,
		CreatedAt:  base,
	}); err != nil {
		t.Fatalf("UpsertEvent create other instance evt: %v", err)
	}

	// ListActiveEvents returns only firing events of inst ordered by fired_at DESC, id DESC.
	active, err := s.ListActiveEvents(ctx, inst.ID)
	if err != nil {
		t.Fatalf("ListActiveEvents: %v", err)
	}
	if len(active) != 2 || active[0].ID != evt2.ID || active[1].ID != evt1.ID {
		t.Fatalf("unexpected active events: %+v", active)
	}

	// UpsertEvent update preserves CreatedAt and updates ResolvedAt/State/ConsecutiveHits.
	resolvedAt := base.Add(5 * time.Minute)
	origCreatedAt := evt1.CreatedAt
	evt1.State = "resolved"
	evt1.ConsecutiveHits = 3
	evt1.ResolvedAt = &resolvedAt
	if err := s.UpsertEvent(ctx, evt1); err != nil {
		t.Fatalf("UpsertEvent update: %v", err)
	}
	filtered, err := s.ListEvents(ctx, inst.ID, "resolved", "critical", 10)
	if err != nil || len(filtered) != 1 || filtered[0].ID != evt1.ID || filtered[0].ConsecutiveHits != 3 ||
		filtered[0].ResolvedAt == nil || !filtered[0].ResolvedAt.Equal(resolvedAt) || !filtered[0].CreatedAt.Equal(origCreatedAt) {
		t.Fatalf("ListEvents after update: %+v, %v", filtered, err)
	}

	// Deliveries, cooldown semantics, due retries, and UpdateDelivery.
	cdFuture := base.Add(30 * time.Minute)
	cdPast := base.Add(-1 * time.Minute)
	dueTime := base.Add(2 * time.Minute)
	futureRetry := base.Add(20 * time.Minute)

	dSent := &SiteOpsAlertDelivery{
		EventID:         evt1.ID,
		InstanceID:      inst.ID,
		NotifyChannelID: 10,
		DeliveryKind:    "alert",
		CooldownKey:     "site_offline:site",
		CooldownUntil:   &cdFuture,
		Status:          "sent",
		Attempts:        1,
		Subject:         "sub1",
		Body:            "body1",
		CreatedAt:       base,
	}
	dSkipped := &SiteOpsAlertDelivery{
		EventID:         evt1.ID,
		InstanceID:      inst.ID,
		NotifyChannelID: 11,
		DeliveryKind:    "alert",
		CooldownKey:     "skipped:key",
		CooldownUntil:   &cdFuture,
		Status:          "skipped_cooldown",
		CreatedAt:       base,
	}
	dExpired := &SiteOpsAlertDelivery{
		EventID:         evt2.ID,
		InstanceID:      inst.ID,
		NotifyChannelID: 12,
		DeliveryKind:    "alert",
		CooldownKey:     "expired:key",
		CooldownUntil:   &cdPast,
		Status:          "sent",
		CreatedAt:       base.Add(-2 * time.Hour),
	}
	dDueFailed := &SiteOpsAlertDelivery{
		EventID:         evt2.ID,
		InstanceID:      inst.ID,
		NotifyChannelID: 13,
		DeliveryKind:    "alert",
		CooldownKey:     "sla:site",
		CooldownUntil:   &cdFuture,
		Status:          "failed",
		Attempts:        1,
		NextRetryAt:     &dueTime,
		LastError:       "timeout",
		CreatedAt:       base,
	}
	dNotDue := &SiteOpsAlertDelivery{
		EventID:         evt2.ID,
		InstanceID:      inst.ID,
		NotifyChannelID: 14,
		DeliveryKind:    "alert",
		Status:          "pending",
		NextRetryAt:     &futureRetry,
		CreatedAt:       base,
	}
	for _, d := range []*SiteOpsAlertDelivery{dSent, dSkipped, dExpired, dDueFailed, dNotDue} {
		if err := s.CreateDelivery(ctx, d); err != nil {
			t.Fatalf("CreateDelivery: %v", err)
		}
	}

	if ok, err := s.HasActiveCooldown(ctx, inst.ID, 10, "site_offline:site", base.Add(10*time.Minute)); err != nil || !ok {
		t.Fatalf("expected active cooldown for sent delivery: ok=%v err=%v", ok, err)
	}
	if ok, err := s.HasActiveCooldown(ctx, inst.ID, 13, "sla:site", base.Add(10*time.Minute)); err != nil || !ok {
		t.Fatalf("expected active cooldown for failed delivery: ok=%v err=%v", ok, err)
	}
	if ok, err := s.HasActiveCooldown(ctx, inst.ID, 11, "skipped:key", base.Add(10*time.Minute)); err != nil || ok {
		t.Fatalf("skipped_cooldown must not count as active cooldown: ok=%v err=%v", ok, err)
	}
	if ok, err := s.HasActiveCooldown(ctx, inst.ID, 12, "expired:key", base); err != nil || ok {
		t.Fatalf("expired cooldown must return false: ok=%v err=%v", ok, err)
	}

	grouped, err := s.ListDeliveriesByEventIDs(ctx, []uint{evt1.ID, evt2.ID})
	if err != nil {
		t.Fatalf("ListDeliveriesByEventIDs: %v", err)
	}
	if len(grouped[evt1.ID]) != 2 || len(grouped[evt2.ID]) != 3 {
		t.Fatalf("unexpected delivery grouping: evt1=%d evt2=%d", len(grouped[evt1.ID]), len(grouped[evt2.ID]))
	}

	due, err := s.ListDueDeliveries(ctx, base.Add(5*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDueDeliveries: %v", err)
	}
	if len(due) != 1 || due[0].ID != dDueFailed.ID {
		t.Fatalf("unexpected due deliveries: %+v", due)
	}

	sentAt := base.Add(6 * time.Minute)
	dDueFailed.Status = "sent"
	dDueFailed.Attempts = 2
	dDueFailed.NextRetryAt = nil
	dDueFailed.LastError = ""
	dDueFailed.SentAt = &sentAt
	if err := s.UpdateDelivery(ctx, dDueFailed); err != nil {
		t.Fatalf("UpdateDelivery: %v", err)
	}
	dueAfter, err := s.ListDueDeliveries(ctx, base.Add(10*time.Minute), 10)
	if err != nil || len(dueAfter) != 0 {
		t.Fatalf("expected no due deliveries after update: %+v, %v", dueAfter, err)
	}

	// Chunked retention deletes only rows older than cutoff and respects maxRows.
	cutoff := base.Add(-1 * time.Hour)
	deletedEvents, err := s.DeleteEventsBeforeChunked(ctx, cutoff, 1, 10, 2*time.Second)
	if err != nil || deletedEvents != 1 {
		t.Fatalf("DeleteEventsBeforeChunked = (%d, %v), want (1, nil)", deletedEvents, err)
	}
	deletedDeliveries, err := s.DeleteDeliveriesBeforeChunked(ctx, cutoff, 1, 10, 2*time.Second)
	if err != nil || deletedDeliveries != 1 {
		t.Fatalf("DeleteDeliveriesBeforeChunked = (%d, %v), want (1, nil)", deletedDeliveries, err)
	}

	// DeleteInstance cascades to SiteOpsEvent and SiteOpsAlertDelivery without affecting other instances.
	if err := s.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	leftInstEvents, err := s.ListEvents(ctx, inst.ID, "", "", 100)
	if err != nil || len(leftInstEvents) != 0 {
		t.Fatalf("events survived DeleteInstance: %+v, %v", leftInstEvents, err)
	}
	leftDeliveries, err := s.ListDeliveriesByEventIDs(ctx, []uint{evt1.ID, evt2.ID})
	if err != nil || len(leftDeliveries) != 0 {
		t.Fatalf("deliveries survived DeleteInstance: %+v, %v", leftDeliveries, err)
	}
	otherEvents, err := s.ListEvents(ctx, other.ID, "", "", 100)
	if err != nil || len(otherEvents) != 1 {
		t.Fatalf("other instance events affected: %+v, %v", otherEvents, err)
	}
}

func TestDeleteUpstreamChannelDoesNotTouchSiteOpsEventsOrCooldowns(t *testing.T) {
	db, err := Open(DBConfig{Path: filepath.Join(t.TempDir(), "site-ops-isolation.db")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	channelsRepo := NewChannels(db)
	notifiesRepo := NewNotifications(db)
	siteOpsStore := NewSiteOpsStore(db)
	ctx := context.Background()

	// Create an upstream channel and a siteops instance with the exact same numeric ID.
	upCh := &Channel{
		Name:    "upstream-same-id",
		Type:    ChannelTypeNewAPI,
		SiteURL: "https://upstream.example.com",
	}
	if err := channelsRepo.Create(upCh); err != nil {
		t.Fatalf("create upstream channel: %v", err)
	}
	siteInst := &SiteOpsInstance{
		ID:      upCh.ID,
		Name:    "siteops-same-id",
		Slug:    "siteops-same-id",
		BaseURL: "https://portal.example.com",
		Enabled: true,
	}
	if err := siteOpsStore.CreateInstance(ctx, siteInst); err != nil {
		t.Fatalf("create siteops instance: %v", err)
	}
	if siteInst.ID != upCh.ID {
		t.Fatalf("expected matching numeric IDs: siteInst=%d upCh=%d", siteInst.ID, upCh.ID)
	}

	// Claim a legacy notification cooldown for the upstream channel.
	if claimed, err := notifiesRepo.TryClaimCooldown(upCh.ID, EventBalanceLow, time.Hour); err != nil || !claimed {
		t.Fatalf("claim legacy cooldown: claimed=%v err=%v", claimed, err)
	}

	// Create a SiteOpsEvent and a SiteOpsAlertDelivery cooldown reservation for the same numeric ID.
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(30 * time.Minute)
	evt := &SiteOpsEvent{
		InstanceID: siteInst.ID,
		Severity:   "critical",
		RuleKey:    "site_offline",
		State:      "firing",
		Title:      "站点离线",
		FiredAt:    now,
		CreatedAt:  now,
	}
	if err := siteOpsStore.UpsertEvent(ctx, evt); err != nil {
		t.Fatalf("upsert siteops event: %v", err)
	}
	delivery := &SiteOpsAlertDelivery{
		EventID:         evt.ID,
		InstanceID:      siteInst.ID,
		NotifyChannelID: 7,
		DeliveryKind:    "alert",
		CooldownKey:     "site_offline:site",
		CooldownUntil:   &until,
		Status:          "sent",
		Subject:         "upstream-same-id",
		Body:            "siteops body",
		CreatedAt:       now,
	}
	if err := siteOpsStore.CreateDelivery(ctx, delivery); err != nil {
		t.Fatalf("create siteops delivery: %v", err)
	}

	// Delete the upstream channel with the same numeric ID.
	if err := channelsRepo.Delete(upCh.ID); err != nil {
		t.Fatalf("delete upstream channel: %v", err)
	}

	// Legacy cooldown should be gone, while SiteOps events and cooldown deliveries remain completely intact.
	var legacyCooldowns int64
	if err := db.Model(&NotificationCooldown{}).Where("channel_id = ?", upCh.ID).Count(&legacyCooldowns).Error; err != nil {
		t.Fatalf("count legacy cooldowns: %v", err)
	}
	if legacyCooldowns != 0 {
		t.Fatalf("expected legacy cooldown to be deleted, got %d", legacyCooldowns)
	}

	events, err := siteOpsStore.ListActiveEvents(ctx, siteInst.ID)
	if err != nil || len(events) != 1 || events[0].ID != evt.ID {
		t.Fatalf("site_ops_events were damaged by Channels.Delete: %+v, %v", events, err)
	}
	activeCooldown, err := siteOpsStore.HasActiveCooldown(ctx, siteInst.ID, 7, "site_offline:site", now.Add(5*time.Minute))
	if err != nil || !activeCooldown {
		t.Fatalf("site_ops_alert_deliveries cooldown was damaged by Channels.Delete: active=%v, err=%v", activeCooldown, err)
	}
	deliveriesByEvt, err := siteOpsStore.ListDeliveriesByEventIDs(ctx, []uint{evt.ID})
	if err != nil || len(deliveriesByEvt[evt.ID]) != 1 || deliveriesByEvt[evt.ID][0].ID != delivery.ID {
		t.Fatalf("site_ops_alert_deliveries row was damaged: %+v, %v", deliveriesByEvt, err)
	}
}

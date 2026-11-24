package siteops

import (
	"context"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

var (
	_ EventStore = (*storage.SiteOpsStore)(nil)
	_ EventStore = storageStore{}
)

func (s storageStore) ListActiveEvents(ctx context.Context, instanceID uint) ([]storage.SiteOpsEvent, error) {
	events, err := s.SiteOpsStore.ListActiveEvents(ctx, instanceID)
	return events, siteOpsStoreError(err)
}

func (s storageStore) UpsertEvent(ctx context.Context, evt *storage.SiteOpsEvent) error {
	return siteOpsStoreError(s.SiteOpsStore.UpsertEvent(ctx, evt))
}

func (s storageStore) ListEvents(ctx context.Context, instanceID uint, state, severity string, limit int) ([]storage.SiteOpsEvent, error) {
	events, err := s.SiteOpsStore.ListEvents(ctx, instanceID, state, severity, limit)
	return events, siteOpsStoreError(err)
}

func (s storageStore) CreateDelivery(ctx context.Context, d *storage.SiteOpsAlertDelivery) error {
	return siteOpsStoreError(s.SiteOpsStore.CreateDelivery(ctx, d))
}

func (s storageStore) HasActiveCooldown(ctx context.Context, instanceID, notifyChannelID uint, cooldownKey string, now time.Time) (bool, error) {
	active, err := s.SiteOpsStore.HasActiveCooldown(ctx, instanceID, notifyChannelID, cooldownKey, now)
	return active, siteOpsStoreError(err)
}

func (s storageStore) ListDeliveriesByEventIDs(ctx context.Context, eventIDs []uint) (map[uint][]storage.SiteOpsAlertDelivery, error) {
	deliveries, err := s.SiteOpsStore.ListDeliveriesByEventIDs(ctx, eventIDs)
	return deliveries, siteOpsStoreError(err)
}

func (s storageStore) ListDueDeliveries(ctx context.Context, now time.Time, limit int) ([]storage.SiteOpsAlertDelivery, error) {
	deliveries, err := s.SiteOpsStore.ListDueDeliveries(ctx, now, limit)
	return deliveries, siteOpsStoreError(err)
}

func (s storageStore) UpdateDelivery(ctx context.Context, d *storage.SiteOpsAlertDelivery) error {
	return siteOpsStoreError(s.SiteOpsStore.UpdateDelivery(ctx, d))
}

func (s storageStore) DeleteEventsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	deleted, err := s.SiteOpsStore.DeleteEventsBeforeChunked(ctx, cutoff, batchSize, maxRows, maxDuration)
	return deleted, siteOpsStoreError(err)
}

func (s storageStore) DeleteDeliveriesBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	deleted, err := s.SiteOpsStore.DeleteDeliveriesBeforeChunked(ctx, cutoff, batchSize, maxRows, maxDuration)
	return deleted, siteOpsStoreError(err)
}

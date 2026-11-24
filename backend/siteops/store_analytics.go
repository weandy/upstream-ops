package siteops

import (
	"context"
	"github.com/bejix/upstream-ops/backend/storage"
	"time"
)

var _ AnalyticsStore = (*storage.SiteOpsStore)(nil)
var _ AnalyticsStore = storageStore{}
var _ UserConcurrencyPeakStore = (*storage.SiteOpsStore)(nil)
var _ UserConcurrencyPeakStore = storageStore{}

func (s storageStore) ListReferencePrices(ctx context.Context, instanceID uint, targetBinding string) ([]storage.SiteOpsReferencePrice, error) {
	rows, err := s.SiteOpsStore.ListReferencePrices(ctx, instanceID, targetBinding)
	return rows, siteOpsStoreError(err)
}
func (s storageStore) ListReferencePricesByIDs(ctx context.Context, instanceID uint, targetBinding, scope string, scopeIDs []int64) ([]storage.SiteOpsReferencePrice, error) {
	rows, err := s.SiteOpsStore.ListReferencePricesByIDs(ctx, instanceID, targetBinding, scope, scopeIDs)
	return rows, siteOpsStoreError(err)
}
func (s storageStore) PutReferencePrice(ctx context.Context, expectedGen int64, row *storage.SiteOpsReferencePrice) error {
	return siteOpsStoreError(s.SiteOpsStore.PutReferencePrice(ctx, expectedGen, row))
}
func (s storageStore) ClearReferencePrice(ctx context.Context, instanceID uint, expectedGen int64, targetBinding, scope string, scopeID int64) error {
	return siteOpsStoreError(s.SiteOpsStore.ClearReferencePrice(ctx, instanceID, expectedGen, targetBinding, scope, scopeID))
}
func (s storageStore) UpdateAnalyticsConfig(ctx context.Context, instanceID uint, configJSON string) error {
	return siteOpsStoreError(s.SiteOpsStore.UpdateAnalyticsConfig(ctx, instanceID, configJSON))
}
func (s storageStore) ExtendAdminBackoff(ctx context.Context, instanceID uint, expectedGen int64, until time.Time) error {
	return siteOpsStoreError(s.SiteOpsStore.ExtendAdminBackoff(ctx, instanceID, expectedGen, until))
}
func (s storageStore) RecordUserConcurrencyPeaks(ctx context.Context, instanceID uint, windowDate string, now time.Time, samples []storage.SiteOpsUserConcurrencyPeak) error {
	return siteOpsStoreError(s.SiteOpsStore.RecordUserConcurrencyPeaks(ctx, instanceID, windowDate, now, samples))
}
func (s storageStore) QueryUserConcurrencyPeaks(ctx context.Context, instanceID uint, sinceDate string, userIDs []int64) (map[int64]storage.SiteOpsUserConcurrencyPeak, error) {
	rows, err := s.SiteOpsStore.QueryUserConcurrencyPeaks(ctx, instanceID, sinceDate, userIDs)
	return rows, siteOpsStoreError(err)
}
func (s storageStore) PruneUserConcurrencyPeaks(ctx context.Context, beforeDate string) (int64, error) {
	n, err := s.SiteOpsStore.PruneUserConcurrencyPeaks(ctx, beforeDate)
	return n, siteOpsStoreError(err)
}

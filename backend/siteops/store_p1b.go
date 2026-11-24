package siteops

import (
	"context"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

var (
	_ P1BStore = (*storage.SiteOpsStore)(nil)
	_ P1BStore = storageStore{}
)

func (s storageStore) UpsertWindowSamples(ctx context.Context, samples []storage.SiteOpsAccountWindowSample) error {
	return siteOpsStoreError(s.SiteOpsStore.UpsertWindowSamples(ctx, samples))
}

func (s storageStore) ListWindowSamples(ctx context.Context, instanceID uint, accountID int64, windowKey, source string, since time.Time, limit int) ([]storage.SiteOpsAccountWindowSample, error) {
	rows, err := s.SiteOpsStore.ListWindowSamples(ctx, instanceID, accountID, windowKey, source, since, limit)
	return rows, siteOpsStoreError(err)
}

func (s storageStore) ListLatestWindowSamplesByInstance(ctx context.Context, instanceID uint, since time.Time) ([]storage.SiteOpsAccountWindowSample, error) {
	rows, err := s.SiteOpsStore.ListLatestWindowSamplesByInstance(ctx, instanceID, since)
	return rows, siteOpsStoreError(err)
}

func (s storageStore) DeleteWindowSamplesBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	deleted, err := s.SiteOpsStore.DeleteWindowSamplesBeforeChunked(ctx, cutoff, batchSize, maxRows, maxDuration)
	return deleted, siteOpsStoreError(err)
}

func (s storageStore) UpsertAccountCost(ctx context.Context, cost *storage.SiteOpsAccountCost) error {
	return siteOpsStoreError(s.SiteOpsStore.UpsertAccountCost(ctx, cost))
}

func (s storageStore) ListAccountCosts(ctx context.Context, instanceID uint) ([]storage.SiteOpsAccountCost, error) {
	rows, err := s.SiteOpsStore.ListAccountCosts(ctx, instanceID)
	return rows, siteOpsStoreError(err)
}

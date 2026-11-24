package storage

import (
	"context"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }
func int64Ptr(v int64) *int64     { return &v }

func TestSiteOpsP1BWindowSamplesAndAccountCosts(t *testing.T) {
	s := newTestSiteOpsStore(t)
	ctx := context.Background()
	inst := createTestSiteOpsInstance(t, s, "portal-p1b", true, 0)
	other := createTestSiteOpsInstance(t, s, "other-p1b", true, 1)

	base := time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)
	resetAt := base.Add(3 * time.Hour)

	// Empty slice is a no-op.
	if err := s.UpsertWindowSamples(ctx, nil); err != nil {
		t.Fatalf("UpsertWindowSamples(nil): %v", err)
	}

	// Insert multiple window samples across instances, accounts, and windows (including a duplicate in-slice key).
	initial := []SiteOpsAccountWindowSample{
		{
			InstanceID:         inst.ID,
			AccountID:          101,
			AccountName:        "claude-01",
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			WindowKey:          "five_hour",
			WindowDurationSec:  18000,
			Utilization:        35.0,
			ResetsAt:           &resetAt,
			RemainingSeconds:   10800,
			UpstreamObservedAt: base.Add(-20 * time.Minute),
			WindowStandardCost: floatPtr(12.5),
			WindowUserCost:     floatPtr(15.0),
			WindowRequests:     int64Ptr(60),
			BurnRatePerHour:    floatPtr(10.0),
			ETASeconds:         int64Ptr(23400),
			PredictedEndUtil:   floatPtr(65.0),
			MarginHours:        floatPtr(3.5),
			ForecastStatus:     "safe",
			Confidence:         "medium",
			ObservedAt:         base.Add(-15 * time.Minute),
		},
		{
			// Same unique key in the same batch: should collapse cleanly without error.
			InstanceID:         inst.ID,
			AccountID:          101,
			AccountName:        "claude-01",
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			WindowKey:          "five_hour",
			WindowDurationSec:  18000,
			Utilization:        35.0,
			ResetsAt:           &resetAt,
			RemainingSeconds:   10800,
			UpstreamObservedAt: base.Add(-20 * time.Minute),
			BurnRatePerHour:    floatPtr(11.5),
			ETASeconds:         int64Ptr(20300),
			PredictedEndUtil:   floatPtr(69.5),
			MarginHours:        floatPtr(2.6),
			ForecastStatus:     "tight",
			Confidence:         "medium",
			ObservedAt:         base.Add(-14 * time.Minute),
		},
		{
			InstanceID:         inst.ID,
			AccountID:          101,
			AccountName:        "claude-01",
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			WindowKey:          "five_hour",
			WindowDurationSec:  18000,
			Utilization:        55.0,
			ResetsAt:           &resetAt,
			RemainingSeconds:   9000,
			UpstreamObservedAt: base.Add(-5 * time.Minute),
			BurnRatePerHour:    floatPtr(22.0),
			ETASeconds:         int64Ptr(7363),
			PredictedEndUtil:   floatPtr(110.0),
			MarginHours:        floatPtr(-0.45),
			ForecastStatus:     "will_exhaust_high",
			Confidence:         "high",
			ObservedAt:         base,
		},
		{
			// Old sample for chunked retention test.
			InstanceID:         inst.ID,
			AccountID:          101,
			AccountName:        "claude-01",
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			WindowKey:          "seven_day",
			WindowDurationSec:  7 * 24 * 3600,
			Utilization:        10.0,
			UpstreamObservedAt: base.Add(-10 * 24 * time.Hour),
			ForecastStatus:     "safe",
			Confidence:         "low",
			ObservedAt:         base.Add(-10 * 24 * time.Hour),
		},
		{
			// Different instance sample must stay isolated.
			InstanceID:         other.ID,
			AccountID:          201,
			AccountName:        "other-01",
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			WindowKey:          "five_hour",
			WindowDurationSec:  18000,
			Utilization:        20.0,
			UpstreamObservedAt: base,
			ForecastStatus:     "safe",
			Confidence:         "low",
			ObservedAt:         base,
		},
	}
	if err := s.UpsertWindowSamples(ctx, initial); err != nil {
		t.Fatalf("UpsertWindowSamples initial: %v", err)
	}

	// Re-upserting the same upstream_observed_at must update forecast columns idempotently without inserting duplicate rows.
	updatedObservedAt := base.Add(2 * time.Minute)
	if err := s.UpsertWindowSamples(ctx, []SiteOpsAccountWindowSample{
		{
			InstanceID:         inst.ID,
			AccountID:          101,
			AccountName:        "claude-01",
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			WindowKey:          "five_hour",
			WindowDurationSec:  18000,
			Utilization:        99.9, // Utilization should remain original on conflict
			UpstreamObservedAt: base.Add(-5 * time.Minute),
			BurnRatePerHour:    floatPtr(25.0),
			ETASeconds:         int64Ptr(6480),
			PredictedEndUtil:   floatPtr(117.5),
			MarginHours:        floatPtr(-0.7),
			ForecastStatus:     "will_exhaust_high",
			Confidence:         "high",
			ObservedAt:         updatedObservedAt,
		},
	}); err != nil {
		t.Fatalf("UpsertWindowSamples idempotent update: %v", err)
	}

	rows, err := s.ListWindowSamples(ctx, inst.ID, 101, "five_hour", "passive_cache", base.Add(-time.Hour), 0)
	if err != nil {
		t.Fatalf("ListWindowSamples: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 deduplicated rows, got %d: %+v", len(rows), rows)
	}
	if !rows[0].ObservedAt.Before(rows[1].ObservedAt) {
		t.Fatalf("expected ASC observed_at ordering: %+v", rows)
	}
	if rows[0].ForecastStatus != "tight" || rows[0].BurnRatePerHour == nil || *rows[0].BurnRatePerHour != 11.5 {
		t.Fatalf("in-slice dedup row mismatch: %+v", rows[0])
	}
	if rows[1].Utilization != 55.0 || !rows[1].UpstreamObservedAt.Equal(base.Add(-5*time.Minute)) ||
		!rows[1].ObservedAt.Equal(updatedObservedAt) || rows[1].BurnRatePerHour == nil || *rows[1].BurnRatePerHour != 25.0 ||
		rows[1].ETASeconds == nil || *rows[1].ETASeconds != 6480 || rows[1].MarginHours == nil || *rows[1].MarginHours != -0.7 {
		t.Fatalf("conflict update mismatch: %+v", rows[1])
	}

	// ListLatestWindowSamplesByInstance returns all samples for the instance since cutoff ordered ASC.
	byInst, err := s.ListLatestWindowSamplesByInstance(ctx, inst.ID, base.Add(-11*24*time.Hour))
	if err != nil {
		t.Fatalf("ListLatestWindowSamplesByInstance: %v", err)
	}
	if len(byInst) != 3 {
		t.Fatalf("expected 3 samples for inst, got %d", len(byInst))
	}
	if byInst[0].WindowKey != "seven_day" || byInst[1].WindowKey != "five_hour" || byInst[2].WindowKey != "five_hour" {
		t.Fatalf("unexpected instance sample order: %+v", byInst)
	}

	// Chunked retention deletes only rows older than cutoff.
	deleted, err := s.DeleteWindowSamplesBeforeChunked(ctx, base.Add(-7*24*time.Hour), 1, 10, 2*time.Second)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteWindowSamplesBeforeChunked = (%d, %v), want (1, nil)", deleted, err)
	}
	byInstAfter, err := s.ListLatestWindowSamplesByInstance(ctx, inst.ID, base.Add(-11*24*time.Hour))
	if err != nil || len(byInstAfter) != 2 {
		t.Fatalf("samples after chunked delete: len=%d err=%v", len(byInstAfter), err)
	}

	// UpsertAccountCost & ListAccountCosts idempotent create/update and account_id ASC ordering.
	t1 := base.Add(time.Minute)
	c2 := &SiteOpsAccountCost{InstanceID: inst.ID, AccountID: 200, MonthlyCostUSD: 100.0, Note: "team seat", UpdatedAt: t1}
	c1 := &SiteOpsAccountCost{InstanceID: inst.ID, AccountID: 101, MonthlyCostUSD: 200.0, Note: "max plan", UpdatedAt: t1}
	cOther := &SiteOpsAccountCost{InstanceID: other.ID, AccountID: 201, MonthlyCostUSD: 50.0, Note: "other inst", UpdatedAt: t1}
	for _, c := range []*SiteOpsAccountCost{c2, c1, cOther} {
		if err := s.UpsertAccountCost(ctx, c); err != nil || c.ID == 0 {
			t.Fatalf("UpsertAccountCost(%d): err=%v id=%d", c.AccountID, err, c.ID)
		}
	}

	t2 := base.Add(10 * time.Minute)
	c1Updated := &SiteOpsAccountCost{InstanceID: inst.ID, AccountID: 101, MonthlyCostUSD: 240.5, Note: "upgraded plan", UpdatedAt: t2}
	if err := s.UpsertAccountCost(ctx, c1Updated); err != nil {
		t.Fatalf("UpsertAccountCost update: %v", err)
	}
	costs, err := s.ListAccountCosts(ctx, inst.ID)
	if err != nil {
		t.Fatalf("ListAccountCosts: %v", err)
	}
	if len(costs) != 2 || costs[0].AccountID != 101 || costs[1].AccountID != 200 {
		t.Fatalf("unexpected account costs: %+v", costs)
	}
	if costs[0].MonthlyCostUSD != 240.5 || costs[0].Note != "upgraded plan" || !costs[0].UpdatedAt.Equal(t2) {
		t.Fatalf("updated account cost mismatch: %+v", costs[0])
	}

	// DeleteInstance cascades to SiteOpsAccountWindowSample and SiteOpsAccountCost without touching other instances.
	if err := s.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	leftSamples, err := s.ListLatestWindowSamplesByInstance(ctx, inst.ID, time.Time{})
	if err != nil || len(leftSamples) != 0 {
		t.Fatalf("window samples survived DeleteInstance: %+v, %v", leftSamples, err)
	}
	leftCosts, err := s.ListAccountCosts(ctx, inst.ID)
	if err != nil || len(leftCosts) != 0 {
		t.Fatalf("account costs survived DeleteInstance: %+v, %v", leftCosts, err)
	}
	otherSamples, err := s.ListLatestWindowSamplesByInstance(ctx, other.ID, time.Time{})
	if err != nil || len(otherSamples) != 1 {
		t.Fatalf("other instance samples affected: %+v, %v", otherSamples, err)
	}
	otherCosts, err := s.ListAccountCosts(ctx, other.ID)
	if err != nil || len(otherCosts) != 1 {
		t.Fatalf("other instance costs affected: %+v, %v", otherCosts, err)
	}
}

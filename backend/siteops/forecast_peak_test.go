package siteops

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestUserConcurrencyPeakPersistenceAndPruning(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "peaks.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	store := storage.NewSiteOpsStore(db)
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, locUTC8)

	// 写入两天高水位，验证高水位只升不降且保留高点时刻
	if err := store.RecordUserConcurrencyPeaks(ctx, 1, "2026-11-23", t0.Add(-24*time.Hour), []storage.SiteOpsUserConcurrencyPeak{
		{UserID: 101, PeakInUse: 9, MaxCapacity: 10, PeakWaiting: 2, FullLoadHits: 1},
		{UserID: 102, PeakInUse: 4, MaxCapacity: 10, PeakWaiting: 0, FullLoadHits: 0},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUserConcurrencyPeaks(ctx, 1, "2026-11-24", t0, []storage.SiteOpsUserConcurrencyPeak{
		{UserID: 101, PeakInUse: 3, MaxCapacity: 10, PeakWaiting: 0, FullLoadHits: 0},
		{UserID: 102, PeakInUse: 8, MaxCapacity: 10, PeakWaiting: 1, FullLoadHits: 1},
	}); err != nil {
		t.Fatal(err)
	}
	// 更早的 10 天前记录应被 7 天查询排除并被清理
	if err := store.RecordUserConcurrencyPeaks(ctx, 1, "2026-11-10", t0.AddDate(0, 0, -14), []storage.SiteOpsUserConcurrencyPeak{
		{UserID: 101, PeakInUse: 20, MaxCapacity: 20, PeakWaiting: 5, FullLoadHits: 3},
	}); err != nil {
		t.Fatal(err)
	}

	peaks, err := store.QueryUserConcurrencyPeaks(ctx, 1, "2026-11-18", []int64{101, 102})
	if err != nil {
		t.Fatal(err)
	}
	if peaks[101].PeakInUse != 9 || peaks[101].PeakWaiting != 2 || peaks[101].FullLoadHits != 1 {
		t.Fatalf("user 101 7d peak mismatch: %+v", peaks[101])
	}
	if peaks[102].PeakInUse != 8 || peaks[102].PeakWaiting != 1 || peaks[102].FullLoadHits != 1 {
		t.Fatalf("user 102 7d peak mismatch: %+v", peaks[102])
	}
	deleted, err := store.PruneUserConcurrencyPeaks(ctx, "2026-11-17")
	if err != nil || deleted != 1 {
		t.Fatalf("expected 1 pruned row, got %d err=%v", deleted, err)
	}
}

func TestSubscriptionForecastSummaryVerdicts(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, locUTC8)
	cfg := DefaultAnalyticsConfig()

	mkRow := func(elapsedDays, used, limit float64, state string) AnalyticsSubscriptionRow {
		start := now.Add(-time.Duration(elapsedDays * float64(24*time.Hour)))
		end := start.Add(30 * 24 * time.Hour)
		var limPtr *float64
		if limit > 0 {
			limPtr = ptr(limit)
		}
		return AnalyticsSubscriptionRow{
			SubscriptionID: 1,
			UserID:         1,
			CycleState:     state,
			StartsAt:       &start,
			ExpiresAt:      &end,
			SubCycleStart:  &start,
			SubCycleEnd:    &end,
			MonthlyWindow:  RollingWindowView{UsedUSD: ptr(used), LimitUSD: limPtr, Quality: QualityTrusted},
		}
	}

	cases := []struct {
		name string
		row  AnalyticsSubscriptionRow
		want string
	}{
		{"observing_new", mkRow(1.0, 20, 600, "active"), "observing"},
		{"exhausted_hit", mkRow(10.0, 600, 600, "active"), "exhausted"},
		{"will_hit_fast", mkRow(19.0, 460, 600, "active"), "will_hit"},
		{"near_full", mkRow(25.0, 455, 600, "active"), "near_full"},
		{"on_track", mkRow(21.0, 295, 600, "active"), "on_track"},
		{"under_use", mkRow(16.0, 64, 400, "active"), "under_use"},
		{"expired_done", mkRow(30.0, 410, 600, "expired"), "expired"},
		{"unlimited_over", mkRow(15.0, 260, 0, "active"), "will_hit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := subscriptionForecastSummary(tc.row, cfg, now)
			if f == nil || f.Verdict != tc.want {
				t.Fatalf("%s: got %+v, want verdict %s", tc.name, f, tc.want)
			}
		})
	}
}

func TestUserActivityStates(t *testing.T) {
	drop := userActivityState(AnalyticsUserRow{}, []OperationalTagView{{Key: "high_value_at_risk"}})
	if drop != "drop" {
		t.Fatalf("expected drop, got %s", drop)
	}
	idle := userActivityState(AnalyticsUserRow{BalanceUSD: ptr(50.0), TodayActualUSD: ptr(0.0), RecentAggregateActualUSD: ptr(0.0)}, nil)
	if idle != "idle_balance" {
		t.Fatalf("expected idle_balance, got %s", idle)
	}
	act := userActivityState(AnalyticsUserRow{TodayActualUSD: ptr(12.5)}, nil)
	if act != "active" {
		t.Fatalf("expected active, got %s", act)
	}
	low := userActivityState(AnalyticsUserRow{BalanceUSD: ptr(2.0), TodayActualUSD: ptr(0.0), RecentAggregateActualUSD: ptr(0.0)}, nil)
	if low != "low" {
		t.Fatalf("expected low, got %s", low)
	}
}

package siteops

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestP1BSanitizerFixturesAndPrivacyWhitelist(t *testing.T) {
	// 1. groups_capacity_summary
	caps, err := ParseGroupCapacitySummary(fixture(t, "groups_capacity_summary"))
	if err != nil {
		t.Fatalf("ParseGroupCapacitySummary: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("caps len = %d, want 2", len(caps))
	}
	if caps[0].GroupID != 10 || caps[0].ConcurrencyUsed != 14 || caps[0].ConcurrencyMax != 32 ||
		caps[0].SessionsUsed != 9 || caps[0].SessionsMax != 40 || caps[0].RPMUsed != 128 || caps[0].RPMMax != 600 {
		t.Fatalf("unexpected first group capacity: %+v", caps[0])
	}
	if caps[1].GroupID != 20 || caps[1].RPMMax != 300 {
		t.Fatalf("unexpected second group capacity: %+v", caps[1])
	}

	// 2. accounts_usage_batch
	batch, err := ParseBatchUsageResult(fixture(t, "accounts_usage_batch"))
	if err != nil {
		t.Fatalf("ParseBatchUsageResult: %v", err)
	}
	if len(batch.Usage) != 2 {
		t.Fatalf("batch usage len = %d, want 2", len(batch.Usage))
	}
	u101, ok := batch.Usage[101]
	if !ok || u101.AccountID != 101 || u101.Source != "passive_cache" || u101.UpdatedAt == nil || len(u101.Windows) != 4 {
		t.Fatalf("unexpected account 101 usage: %+v", u101)
	}
	w0 := u101.Windows[0]
	if w0.WindowKey != "five_hour" || w0.WindowDurationSec != 5*3600 || w0.Utilization != 62.5 ||
		w0.RemainingSeconds != 10800 || w0.ResetsAt == nil || w0.WindowStats == nil ||
		w0.WindowStats.Requests != 145 || w0.WindowStats.Tokens != 1240000 ||
		w0.WindowStats.Cost != 18.4 || w0.WindowStats.StandardCost != 24.0 || w0.WindowStats.UserCost != 31.2 {
		t.Fatalf("unexpected 101 five_hour window: %+v", w0)
	}
	if u101.Windows[1].WindowKey != "seven_day" || u101.Windows[1].WindowDurationSec != 7*24*3600 ||
		u101.Windows[2].WindowKey != "seven_day_sonnet" || u101.Windows[3].WindowKey != "seven_day_fable" {
		t.Fatalf("unexpected 101 windows order/keys: %+v", u101.Windows)
	}

	u102, ok := batch.Usage[102]
	if !ok || u102.Source != "passive" || len(u102.Windows) != 4 {
		t.Fatalf("unexpected account 102 usage: %+v", u102)
	}
	if u102.Windows[0].WindowKey != "thirty_day" || !u102.Windows[0].UtilizationUnknown || u102.Windows[0].WindowDurationSec != 30*24*3600 ||
		u102.Windows[1].WindowKey != "gemini_shared_daily" || u102.Windows[1].WindowDurationSec != 24*3600 ||
		u102.Windows[2].WindowKey != "gemini_pro_daily" || u102.Windows[3].WindowKey != "gemini_flash_daily" {
		t.Fatalf("unexpected 102 windows: %+v", u102.Windows)
	}

	err103, ok := batch.Errors[103]
	if !ok || !strings.Contains(err103, "[redacted]") {
		t.Fatalf("expected sanitized error for account 103, got %q (ok=%v)", err103, ok)
	}

	// 3. account_stats (verify v0.2.24 three-metric semantics & history standard_cost backfill)
	stats, err := ParseAccountStats(101, fixture(t, "account_stats"))
	if err != nil {
		t.Fatalf("ParseAccountStats: %v", err)
	}
	if stats.AccountID != 101 || stats.Days != 30 || stats.ActualDaysUsed != 18 ||
		stats.TotalRequests != 4210 || stats.TotalTokens != 58200000 ||
		stats.TotalCost != 186.5 || stats.TotalStandardCost != 265.0 || stats.TotalUserCost != 318.0 {
		t.Fatalf("unexpected account stats summary: %+v", stats)
	}
	if stats.TodayDate != "2026-11-24" || stats.TodayRequests != 215 || stats.TodayTokens != 2900000 ||
		stats.TodayCost != 9.25 || stats.TodayStandardCost != 13.75 || stats.TodayUserCost != 16.4 {
		t.Fatalf("unexpected account stats today metrics: %+v", stats)
	}

	// Verify zero secret leakage across all sanitized outputs.
	forbidden := []string{
		"sk-group-cap-secret-10",
		"adm_secret_cap_key",
		"secret-batch-access-101",
		"secret-batch-refresh-101",
		"sk-batch-header-secret",
		"sk-live-batch-err-103",
		"superpass",
		"sk-stats-should-not-leak",
		"stats-refresh-secret-token",
		"credentials",
		"raw_headers",
	}
	for _, item := range []any{caps, batch, stats} {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal sanitized output: %v", err)
		}
		text := string(encoded)
		for _, secret := range forbidden {
			if strings.Contains(text, secret) {
				t.Fatalf("sanitized payload leaked %q: %s", secret, text)
			}
		}
	}
}

func TestP1BSanitizerVariantsAndEdgeCases(t *testing.T) {
	// ParseGroupCapacitySummary supports direct array and {items:[...]}.
	directCaps, err := ParseGroupCapacitySummary([]byte(`[{"group_id":7,"concurrency_used":1,"concurrency_max":4}]`))
	if err != nil || len(directCaps) != 1 || directCaps[0].GroupID != 7 || directCaps[0].ConcurrencyMax != 4 {
		t.Fatalf("direct array caps = (%+v, %v)", directCaps, err)
	}
	itemsCaps, err := ParseGroupCapacitySummary([]byte(`{"code":0,"data":{"items":[{"id":8,"rpm_used":5,"rpm_max":100}]}}`))
	if err != nil || len(itemsCaps) != 1 || itemsCaps[0].GroupID != 8 || itemsCaps[0].RPMMax != 100 {
		t.Fatalf("items wrapper caps = (%+v, %v)", itemsCaps, err)
	}

	// ParseAccountUsage standalone with envelope and missing source defaults to "passive".
	singleUsage, err := ParseAccountUsage(55, []byte(`{"code":0,"data":{"updated_at":"2026-11-24T06:00:00Z","five_hour":{"utilization":42.5,"remaining_seconds":3600},"credentials":{"api_key":"sk-leak"}}}`))
	if err != nil || singleUsage.AccountID != 55 || singleUsage.Source != "passive" || len(singleUsage.Windows) != 1 ||
		singleUsage.Windows[0].WindowDurationSec != 18000 || singleUsage.Windows[0].Utilization != 42.5 {
		t.Fatalf("ParseAccountUsage = (%+v, %v)", singleUsage, err)
	}

	// ParseAccountStats leaves TodayStandardCost at 0 when today.date is absent from history (never fabricates from today.cost).
	statsNoMatch, err := ParseAccountStats(88, []byte(`{
		"summary": {
			"days": 30,
			"total_cost": 50.0,
			"total_standard_cost": 80.0,
			"total_user_cost": 95.0,
			"today": {"date": "2026-11-24", "requests": 10, "tokens": 1000, "cost": 4.2, "user_cost": 6.5}
		},
		"history": [
			{"date": "2026-11-23", "cost": 12.0, "actual_cost": 8.0, "user_cost": 14.0}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseAccountStats no matching history day: %v", err)
	}
	if statsNoMatch.TodayCost != 4.2 || statsNoMatch.TodayStandardCost != 0 || statsNoMatch.TodayUserCost != 6.5 {
		t.Fatalf("expected TodayStandardCost=0 without fabricating, got %+v", statsNoMatch)
	}

	// Invalid payloads rejected.
	for _, bad := range [][]byte{
		[]byte(`{"code":500,"message":"fail"}`),
		[]byte(`null`),
		[]byte(`not-json`),
	} {
		if _, err := ParseGroupCapacitySummary(bad); err == nil {
			t.Fatalf("ParseGroupCapacitySummary accepted invalid payload: %s", string(bad))
		}
		if _, err := ParseAccountUsage(1, bad); err == nil {
			t.Fatalf("ParseAccountUsage accepted invalid payload: %s", string(bad))
		}
		if _, err := ParseBatchUsageResult(bad); err == nil {
			t.Fatalf("ParseBatchUsageResult accepted invalid payload: %s", string(bad))
		}
		if _, err := ParseAccountStats(1, bad); err == nil {
			t.Fatalf("ParseAccountStats accepted invalid payload: %s", string(bad))
		}
	}
	if _, err := ParseAccountStats(1, []byte(`{}`)); err == nil {
		t.Fatal("ParseAccountStats accepted empty object without summary/history")
	}
}

func TestNewStorageStoreSatisfiesP1BStore(t *testing.T) {
	db, err := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "store-p1b.db")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	wrapped := NewStorageStore(storage.NewSiteOpsStore(db))
	if _, ok := wrapped.(P1BStore); !ok {
		t.Fatal("NewStorageStore return value does not satisfy P1BStore")
	}
}

package siteops

import (
	"math"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestAnalyzeCollectionDeduplicatesAcrossOverlappingGroups(t *testing.T) {
	now := time.Date(2026, 11, 24, 1, 30, 0, 0, time.UTC)
	in := AnalyzeInput{InstanceID: 42, Now: now,
		Overview: &SanitizedOpsOverview{AccountSwitchCount1m: ptr(int64(7)), ReqTotal1h: ptr(int64(12450)), QPSCurrent: ptr(2.0)},
		Concurrency: &SanitizedConcurrencyPayload{
			Accounts: map[int64]SanitizedAccountConcurrency{
				101: {AccountID: 101, Platform: "anthropic", CurrentInUse: 3, MaxCapacity: 5, WaitingInQueue: 1},
				102: {AccountID: 102, Platform: "openai", CurrentInUse: 2, MaxCapacity: 4},
				999: {AccountID: 101, Platform: "anthropic", CurrentInUse: 3, MaxCapacity: 5, WaitingInQueue: 1},
			},
			Groups: map[int64]SanitizedGroupConcurrency{
				10: {GroupID: 10, CurrentInUse: 3, MaxCapacity: 5, WaitingInQueue: 1},
				11: {GroupID: 11, CurrentInUse: 3, MaxCapacity: 5, WaitingInQueue: 1},
				20: {GroupID: 20, CurrentInUse: 2, MaxCapacity: 4},
			},
		},
		Availability: &SanitizedAvailabilityPayload{
			Accounts: map[int64]SanitizedAccountAvailability{
				101: {AccountID: 101, Available: true},
				102: {AccountID: 102, HasError: true},
				999: {AccountID: 101, Available: true},
			},
			Groups: map[int64]SanitizedGroupAvailability{
				10: {GroupID: 10, TotalCount: 1, AvailableCount: 1},
				11: {GroupID: 11, TotalCount: 1, AvailableCount: 1},
				20: {GroupID: 20, TotalCount: 3, AvailableCount: 2},
			},
		},
		Groups: []SanitizedGroupInfo{{ID: 10, Name: "shared", Platform: "anthropic"}, {ID: 30, Name: "metadata-only"}},
	}
	out := AnalyzeCollection(in)
	snap := out.Snapshot
	assertMetric(t, snap.ConcUsed, 5)
	assertMetric(t, snap.ConcMax, 9)
	assertMetric(t, snap.ConcWaiting, 1)
	assertMetric(t, snap.AccountsTotal, 2)
	assertMetric(t, snap.AccountsOpsAvailable, 1)
	assertMetric(t, snap.AccountsError, 1)
	assertMetric(t, snap.AccountSwitchCount1m, 7)
	if pct := out.Detail.EstimatedSwitchPct1m; pct == nil || math.Abs(*pct-7.0/120*100) > 1e-10 {
		t.Fatalf("one-minute switch rate: %v", pct)
	}
	if snap.InstanceID != 42 || len(out.FieldQuality) != 7 || out.FieldQuality[DomainGroupCapacity] != QualityTrusted {
		t.Fatalf("missing site scope or domain qualities: %+v", out)
	}
	if len(out.Groups) != 3 || out.Groups[0].GroupName != "shared" || out.Groups[0].Platform != "anthropic" ||
		!out.Groups[0].SinglePointRisk || !out.Groups[1].SinglePointRisk || out.Groups[2].SinglePointRisk {
		t.Fatalf("server group aggregates and risk flags: %+v", out.Groups)
	}
	platforms := out.Detail.PlatformBreakdown
	if len(platforms) != 2 || platforms[0].Platform != "anthropic" || platforms[0].AccountsOpsAvailable != 1 || *platforms[0].LoadPercent != 60 {
		t.Fatalf("platform aggregates: %+v", platforms)
	}
	assertMetric(t, out.Groups[1].ConcMax, 5)
	in.Overview.QPSCurrent = ptr(0.2)
	if lowTraffic := AnalyzeCollection(in); lowTraffic.Detail.EstimatedSwitchPct1m != nil || *lowTraffic.Snapshot.AccountSwitchCount1m != 7 {
		t.Fatalf("low traffic should still retain the raw switch count: %+v", lowTraffic)
	}
}

func TestAnalyzeCollectionRedisFalseZeros(t *testing.T) {
	now := time.Date(2026, 11, 24, 1, 31, 0, 0, time.UTC)
	newInput := func() AnalyzeInput {
		return AnalyzeInput{Now: now,
			Overview: &SanitizedOpsOverview{QPSCurrent: ptr(2.0), RedisOK: ptr(true), SystemMetricsAt: &now},
			Concurrency: &SanitizedConcurrencyPayload{Enabled: ptr(true),
				Accounts: map[int64]SanitizedAccountConcurrency{101: {MaxCapacity: 5}},
				Groups:   map[int64]SanitizedGroupConcurrency{10: {MaxCapacity: 5}}},
			Availability: &SanitizedAvailabilityPayload{Enabled: ptr(true),
				Accounts: map[int64]SanitizedAccountAvailability{101: {Available: true}},
				Groups:   map[int64]SanitizedGroupAvailability{10: {TotalCount: 1, AvailableCount: 1}}},
		}
	}
	previous := &storage.SiteOpsSnapshot{ConcUsed: ptr(int64(3)), AccountsTotal: ptr(int64(1)), ConcTrustState: string(QualityTrusted)}
	tests := []struct {
		name  string
		alter func(*AnalyzeInput)
	}{
		{"redis unhealthy", func(in *AnalyzeInput) { in.Overview.RedisOK = ptr(false) }},
		{"infra stale", func(in *AnalyzeInput) { in.Overview.SystemMetricsAt = ptr(now.Add(-181 * time.Second)) }},
		{"active traffic and sudden zero", func(in *AnalyzeInput) { in.PreviousTrusted = previous }},
		{"empty payload after active groups", func(in *AnalyzeInput) {
			in.Concurrency = &SanitizedConcurrencyPayload{RawEmpty: true}
			in.PreviousLiveGroups = true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInput()
			tt.alter(&in)
			out := AnalyzeCollection(in)
			if out.Snapshot.ConcUsed != nil || out.FieldQuality[DomainConcurrency] != QualityUnverifiedZero ||
				out.FieldQuality[DomainGroupCapacity] != QualityUnverifiedZero || out.Detail.ConcTrustReason == "" ||
				len(out.Groups) != 1 || out.Groups[0].TrustState != QualityUnverifiedZero || out.Groups[0].ConcUsed != nil {
				t.Fatalf("suspect zero was treated as evidence: %+v", out)
			}
		})
	}
	t.Run("explicitly enabled idle", func(t *testing.T) {
		in := newInput()
		in.Concurrency.Accounts = map[int64]SanitizedAccountConcurrency{}
		in.Concurrency.RawEmpty = true
		in.PreviousLiveGroups = true
		out := AnalyzeCollection(in)
		assertMetric(t, out.Snapshot.ConcUsed, 0)
		if out.FieldQuality[DomainConcurrency] != QualityTrusted || loadPercent(out.Snapshot.ConcUsed, out.Snapshot.ConcMax) != nil {
			t.Fatalf("explicitly enabled idle state should be trusted: %+v", out)
		}
	})

	t.Run("disabled and missing domains", func(t *testing.T) {
		out := AnalyzeCollection(AnalyzeInput{Now: now,
			Overview:    &SanitizedOpsOverview{OpsEnabled: ptr(false), HealthScore: ptr(0), AccountSwitchCount1m: ptr(int64(0))},
			Concurrency: &SanitizedConcurrencyPayload{Enabled: ptr(false), Accounts: map[int64]SanitizedAccountConcurrency{}},
		})
		if out.Snapshot.HealthScore != nil || out.Snapshot.AccountSwitchCount1m != nil || out.Snapshot.ConcUsed != nil ||
			out.Snapshot.AccountsTotal != nil || len(out.Groups) != 0 || out.FieldQuality[DomainConcurrency] != QualityDisabled ||
			out.FieldQuality[DomainOpsOverview] != QualityDisabled || out.FieldQuality[DomainAccountAvailability] != QualityUnavailable {
			t.Fatalf("disabled or missing domain wrote false zero: %+v", out)
		}
	})
}

func assertMetric(t *testing.T, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("missing metric, want %d", want)
	}
	if *got != want {
		t.Fatalf("metric = %d, want %d", *got, want)
	}
}

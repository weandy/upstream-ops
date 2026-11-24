package siteops

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestP1ASanitizerFixturesAndPrivacyWhitelist(t *testing.T) {
	// 1. ops_alert_events
	events, err := ParseOpsAlertEvents(fixture(t, "ops_alert_events"))
	if err != nil {
		t.Fatalf("ParseOpsAlertEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].ID != 901 || events[0].RuleID != 12 || events[0].Severity != "critical" || events[0].Status != "firing" {
		t.Fatalf("unexpected first alert event: %+v", events[0])
	}
	if events[0].MetricValue == nil || *events[0].MetricValue != 18.5 || events[0].ThresholdValue == nil || *events[0].ThresholdValue != 10.0 {
		t.Fatalf("unexpected alert metrics: %+v", events[0])
	}
	if events[1].ResolvedAt == nil || events[1].Status != "resolved" {
		t.Fatalf("unexpected second alert event: %+v", events[1])
	}

	// 2. ops_ingress_health
	ingress, err := ParseIngressRejectHealth(fixture(t, "ops_ingress_health"))
	if err != nil {
		t.Fatalf("ParseIngressRejectHealth: %v", err)
	}
	if ingress.Cardinality != 142 || ingress.Capacity != 4096 || ingress.PendingBatches != 2 ||
		ingress.PendingRows != 35 || ingress.Dropped != 1 || ingress.Flushed != 9821 ||
		ingress.FlushFailures != 3 || !ingress.Accepting {
		t.Fatalf("unexpected ingress health: %+v", ingress)
	}

	// 3. ops_auth_cache_health
	authCache, err := ParseAuthCacheHealth(fixture(t, "ops_auth_cache_health"))
	if err != nil {
		t.Fatalf("ParseAuthCacheHealth: %v", err)
	}
	if !authCache.OutboxRunning || authCache.OutboxPending != 4 || authCache.OutboxOldestMS != 250 ||
		authCache.OutboxHealthySLA != 5000 || !authCache.SubscriberOK || !authCache.Healthy {
		t.Fatalf("unexpected auth cache health: %+v", authCache)
	}

	// 4. accounts_page
	accountsPage, err := ParseAccountsPage(fixture(t, "accounts_page"))
	if err != nil {
		t.Fatalf("ParseAccountsPage: %v", err)
	}
	if accountsPage.Total != 2 || accountsPage.Page != 1 || accountsPage.PageSize != 100 || len(accountsPage.Items) != 2 {
		t.Fatalf("unexpected accounts page: %+v", accountsPage)
	}
	a1, a2 := accountsPage.Items[0], accountsPage.Items[1]
	if a1.AccountID != 101 || a1.Name != "claude-main-01" || a1.Platform != "anthropic" || a1.Status != "active" ||
		!a1.Schedulable || a1.Concurrency != 8 || a1.Priority != 10 || len(a1.GroupIDs) != 2 || a1.ExpiresAt == nil {
		t.Fatalf("unexpected account 101: %+v", a1)
	}
	if a2.AccountID != 102 || a2.Status != "error" || a2.Schedulable ||
		a2.RateLimitResetAt == nil || a2.OverloadUntil == nil || a2.TempUnschedulableUntil == nil ||
		!strings.Contains(a2.ErrorSummary, "[redacted]") {
		t.Fatalf("unexpected account 102: %+v", a2)
	}

	// Ensure zero secret leakage across all 4 sanitized payloads.
	forbidden := []string{
		"secret-token-abc",
		"admin:pass",
		"sk-live-secret",
		"sk-should-be-dropped",
		"secret-ingress-token",
		"should-not-appear",
		"secret-access-token-101",
		"secret-refresh-token-101",
		"sk-ant-secret-101",
		"proxypass",
		"secretpass",
		"credentials",
		"error_message",
	}
	for _, item := range []any{events, ingress, authCache, accountsPage} {
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

func TestP1ASanitizerVariantsAndHealthDerivation(t *testing.T) {
	// Direct array for alert events and accounts page.
	evts, err := ParseOpsAlertEvents([]byte(`[{"id":1,"rule_id":2,"severity":"warning","status":"firing","title":"t","message":"m"}]`))
	if err != nil || len(evts) != 1 || evts[0].Description != "m" {
		t.Fatalf("direct array alert events = (%+v, %v)", evts, err)
	}
	evtsFromEventsKey, err := ParseOpsAlertEvents([]byte(`{"events":[{"id":2,"title":"from-events"}]}`))
	if err != nil || len(evtsFromEventsKey) != 1 || evtsFromEventsKey[0].ID != 2 {
		t.Fatalf("events wrapper alert events = (%+v, %v)", evtsFromEventsKey, err)
	}
	directAccounts, err := ParseAccountsPage([]byte(`[{"id":5,"name":"acc-5","platform":"openai","status":"active","schedulable":true}]`))
	if err != nil || directAccounts.Total != 1 || len(directAccounts.Items) != 1 || directAccounts.Items[0].AccountID != 5 {
		t.Fatalf("direct accounts array = (%+v, %v)", directAccounts, err)
	}

	// Auth cache health derivation cases.
	for _, tc := range []struct {
		name        string
		raw         string
		wantHealthy bool
		wantSubOK   bool
	}{
		{
			name:        "subscriber_running_and_connected",
			raw:         `{"outbox":{"running":true,"oldest_lag_ms":100,"healthy_sla_ms":5000},"subscriber":{"running":true,"connected":true}}`,
			wantHealthy: true,
			wantSubOK:   true,
		},
		{
			name:        "lag_exceeds_sla",
			raw:         `{"outbox":{"running":true,"oldest_lag":"8s","healthy_sla":"5s"},"subscriber":{"healthy":true}}`,
			wantHealthy: false,
			wantSubOK:   true,
		},
		{
			name:        "outbox_stats_error",
			raw:         `{"outbox":{"running":true,"stats_error":"redis auth password=topsecret"},"subscriber":{"healthy":true}}`,
			wantHealthy: false,
			wantSubOK:   true,
		},
		{
			name:        "subscriber_disconnected",
			raw:         `{"outbox":{"running":true,"oldest_lag":10,"healthy_sla":1000},"subscriber":{"running":true,"connected":false,"last_error":"EOF"}}`,
			wantHealthy: false,
			wantSubOK:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAuthCacheHealth([]byte(tc.raw))
			if err != nil {
				t.Fatalf("ParseAuthCacheHealth(%s): %v", tc.name, err)
			}
			if got.Healthy != tc.wantHealthy || got.SubscriberOK != tc.wantSubOK {
				t.Fatalf("got healthy=%v subOK=%v, want healthy=%v subOK=%v (%+v)",
					got.Healthy, got.SubscriberOK, tc.wantHealthy, tc.wantSubOK, got)
			}
			if strings.Contains(got.OutboxLastError, "topsecret") {
				t.Fatalf("stats_error leaked secret: %q", got.OutboxLastError)
			}
		})
	}

	// Invalid payloads return errInvalidPayload.
	for _, bad := range [][]byte{
		[]byte(`{"code":500,"message":"fail"}`),
		[]byte(`null`),
		[]byte(`not-json`),
	} {
		if _, err := ParseOpsAlertEvents(bad); err == nil {
			t.Fatalf("ParseOpsAlertEvents accepted invalid payload: %s", string(bad))
		}
		if _, err := ParseIngressRejectHealth(bad); err == nil {
			t.Fatalf("ParseIngressRejectHealth accepted invalid payload: %s", string(bad))
		}
		if _, err := ParseAuthCacheHealth(bad); err == nil {
			t.Fatalf("ParseAuthCacheHealth accepted invalid payload: %s", string(bad))
		}
		if _, err := ParseAccountsPage(bad); err == nil {
			t.Fatalf("ParseAccountsPage accepted invalid payload: %s", string(bad))
		}
	}
}

func TestNewStorageStoreSatisfiesEventStore(t *testing.T) {
	db, err := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "store-p1a.db")})
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
	if _, ok := wrapped.(EventStore); !ok {
		t.Fatal("NewStorageStore return value does not satisfy EventStore")
	}
}

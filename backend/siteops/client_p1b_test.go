package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
)

func TestClientP1BEndpointsAndParams(t *testing.T) {
	capJSON := fixture(t, "groups_capacity_summary")
	statsJSON := fixture(t, "account_stats")

	var sawPassiveQuery atomic.Bool
	var sawClampedDays atomic.Bool
	var sawDefaultDays atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != testAdminKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.RequestURI() == "/api/v1/admin/groups/capacity-summary":
			_, _ = w.Write(capJSON)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/accounts/101/usage":
			if r.URL.Query().Get("source") != "passive" {
				t.Errorf("expected ?source=passive, got %q", r.URL.RawQuery)
			} else {
				sawPassiveQuery.Store(true)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"source":"passive_cache","updated_at":"2026-11-24T06:00:00Z","five_hour":{"utilization":48.5,"remaining_seconds":7200}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/accounts/101/stats":
			switch r.URL.Query().Get("days") {
			case "30":
				sawDefaultDays.Store(true)
			case "90":
				sawClampedDays.Store(true)
			default:
				t.Errorf("unexpected days query: %q", r.URL.RawQuery)
			}
			_, _ = w.Write(statsJSON)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	if client.batchMinGap != PassiveUsageBatchMinGap {
		t.Fatalf("default batchMinGap = %v, want %v", client.batchMinGap, PassiveUsageBatchMinGap)
	}
	ctx := context.Background()

	caps, meta, err := client.FetchGroupCapacitySummary(ctx, inst)
	if err != nil || len(caps) != 2 || meta.Endpoint != EndpointGroupsCapacitySummary || meta.HTTPStatus != http.StatusOK {
		t.Fatalf("FetchGroupCapacitySummary = (%d, %+v, %v)", len(caps), meta, err)
	}

	usage, meta, err := client.FetchAccountPassiveUsage(ctx, inst, 101)
	if err != nil || usage == nil || !sawPassiveQuery.Load() || len(usage.Windows) != 1 || meta.Endpoint != EndpointAccountsUsagePassive {
		t.Fatalf("FetchAccountPassiveUsage = (%+v, %+v, %v)", usage, meta, err)
	}

	statsDefault, meta, err := client.FetchAccountStats(ctx, inst, 101, 0)
	if err != nil || statsDefault == nil || !sawDefaultDays.Load() || statsDefault.TodayStandardCost != 13.75 || meta.Endpoint != EndpointAccountsStats {
		t.Fatalf("FetchAccountStats(days=0) = (%+v, %+v, %v)", statsDefault, meta, err)
	}

	statsClamped, _, err := client.FetchAccountStats(ctx, inst, 101, 365)
	if err != nil || statsClamped == nil || !sawClampedDays.Load() {
		t.Fatalf("FetchAccountStats(days=365) = (%+v, %v)", statsClamped, err)
	}

	// Invalid accountID rejected without hitting upstream or spending budget.
	if _, _, err := client.FetchAccountPassiveUsage(ctx, inst, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("FetchAccountPassiveUsage(0) err = %v, want ErrInvalidInput", err)
	}
	if _, _, err := client.FetchAccountStats(ctx, inst, -5, 30); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("FetchAccountStats(-5) err = %v, want ErrInvalidInput", err)
	}

	if usage := client.BudgetUsage(inst.InstanceID, 0); usage.UsedToday != 4 {
		t.Fatalf("UsedToday = %d, want 4", usage.UsedToday)
	}
}

func TestClientFetchAccountsUsageBatchChunkingRateAndForceFalse(t *testing.T) {
	var (
		mu         sync.Mutex
		chunkSizes []int
		callTimes  []time.Time
		allForceOK = true
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/accounts/usage/batch" {
			t.Errorf("unexpected batch request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != testAdminKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var rawBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
			t.Errorf("decode batch request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		forceVal, hasForce := rawBody["force"].(bool)
		rawIDs, _ := rawBody["account_ids"].([]any)

		mu.Lock()
		callTimes = append(callTimes, time.Now())
		chunkSizes = append(chunkSizes, len(rawIDs))
		if !hasForce || forceVal {
			allForceOK = false
		}
		mu.Unlock()

		usageMap := make(map[string]any, len(rawIDs))
		errMap := make(map[string]string)
		for _, v := range rawIDs {
			id := int64(v.(float64))
			if id == 170 {
				errMap[fmt.Sprintf("%d", id)] = "failed Bearer sk-batch-leak-170"
				continue
			}
			usageMap[fmt.Sprintf("%d", id)] = map[string]any{
				"source":     "passive_cache",
				"updated_at": "2026-11-24T06:00:00Z",
				"five_hour": map[string]any{
					"utilization":       float64(id % 100),
					"remaining_seconds": 3600,
				},
			}
		}
		respBytes, _ := json.Marshal(map[string]any{
			"code": 0,
			"data": map[string]any{
				"usage":  usageMap,
				"errors": errMap,
			},
		})
		_, _ = w.Write(respBytes)
	}))
	defer server.Close()

	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	const testGap = 40 * time.Millisecond
	client.batchMinGap = testGap
	ctx := context.Background()

	// Empty / invalid IDs produce zero HTTP calls and zero budget spend.
	emptyRes, emptyMeta, err := client.FetchAccountsUsageBatch(ctx, inst, []int64{0, -1})
	if err != nil || emptyRes == nil || len(emptyRes.Usage) != 0 || emptyMeta.Endpoint != EndpointAccountsUsageBatch {
		t.Fatalf("empty batch = (%+v, %+v, %v)", emptyRes, emptyMeta, err)
	}
	if usage := client.BudgetUsage(inst.InstanceID, 0); usage.UsedToday != 0 {
		t.Fatalf("empty batch spent budget: %+v", usage)
	}

	// Build 170 unique positive IDs plus duplicates and non-positive values -> must split into 80 + 80 + 10.
	ids := make([]int64, 0, 175)
	ids = append(ids, 0, -3)
	for i := int64(1); i <= 170; i++ {
		ids = append(ids, i)
	}
	ids = append(ids, 1, 80, 170)

	res, meta, err := client.FetchAccountsUsageBatch(ctx, inst, ids)
	if err != nil {
		t.Fatalf("FetchAccountsUsageBatch: %v", err)
	}
	if meta.Endpoint != EndpointAccountsUsageBatch || meta.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected batch meta: %+v", meta)
	}

	mu.Lock()
	defer mu.Unlock()
	if !allForceOK {
		t.Fatal("expected every batch request to explicitly carry force:false")
	}
	if len(chunkSizes) != 3 || chunkSizes[0] != 80 || chunkSizes[1] != 80 || chunkSizes[2] != 10 {
		t.Fatalf("chunkSizes = %v, want [80 80 10]", chunkSizes)
	}
	for i := 1; i < len(callTimes); i++ {
		gap := callTimes[i].Sub(callTimes[i-1])
		if gap < testGap-10*time.Millisecond {
			t.Fatalf("gap between batch %d and %d was %v, want >= %v", i, i+1, gap, testGap)
		}
	}
	if len(res.Usage) != 169 {
		t.Fatalf("combined usage len = %d, want 169", len(res.Usage))
	}
	errMsg, hasErr := res.Errors[170]
	if !hasErr || !strings.Contains(errMsg, "[redacted]") || strings.Contains(errMsg, "sk-batch-leak-170") {
		t.Fatalf("expected sanitized error for account 170, got %q (exists=%v)", errMsg, hasErr)
	}
	if usage := client.BudgetUsage(inst.InstanceID, 0); usage.UsedToday != 3 {
		t.Fatalf("UsedToday = %d, want 3", usage.UsedToday)
	}
}

func TestClientP1BErrorHandling401_403_429AndBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/groups/capacity-summary":
			w.WriteHeader(http.StatusUnauthorized)
		case "/api/v1/admin/accounts/101/usage":
			w.WriteHeader(http.StatusForbidden)
		case "/api/v1/admin/accounts/101/stats":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/api/v1/admin/accounts/usage/batch":
			_, _ = w.Write([]byte(`{"code":0,"data":{"usage":{"1":{"source":"passive_cache","five_hour":{"utilization":10}}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	client.batchMinGap = 5 * time.Millisecond
	ctx := context.Background()

	if _, meta, err := client.FetchGroupCapacitySummary(ctx, inst); !errors.Is(err, ErrUnauthorized) || meta.ErrorCode != ErrCodeUnauthorized {
		t.Fatalf("capacity-summary 401 = (%+v, %v)", meta, err)
	}
	if _, meta, err := client.FetchAccountPassiveUsage(ctx, inst, 101); !errors.Is(err, ErrForbidden) || meta.ErrorCode != ErrCodeForbidden {
		t.Fatalf("passive usage 403 = (%+v, %v)", meta, err)
	}
	if _, meta, err := client.FetchAccountStats(ctx, inst, 101, 30); !errors.Is(err, ErrRateLimited) ||
		meta.ErrorCode != ErrCodeRateLimited || meta.RetryAfter == nil || *meta.RetryAfter != 30*time.Second {
		t.Fatalf("account stats 429 = (%+v, %v)", meta, err)
	}

	// Budget exceeded stops multi-batch POST cleanly on the second batch.
	tightClient, tightInst := testClient(server.URL, config.SiteOpsConfig{DefaultAuditBudgetPerDay: 1})
	tightClient.batchMinGap = 5 * time.Millisecond
	ids := make([]int64, 90)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	res, meta, err := tightClient.FetchAccountsUsageBatch(ctx, tightInst, ids)
	if !errors.Is(err, ErrBudgetExceeded) || res != nil || meta.ErrorCode != ErrCodeBudgetExceeded {
		t.Fatalf("batch budget exceeded = (%+v, %+v, %v)", res, meta, err)
	}
}

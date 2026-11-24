package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
)

func TestClientP1AEndpoints(t *testing.T) {
	alertJSON := fixture(t, "ops_alert_events")
	ingressJSON := fixture(t, "ops_ingress_health")
	authCacheJSON := fixture(t, "ops_auth_cache_health")
	accountsJSON := fixture(t, "accounts_page")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != testAdminKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.RequestURI() {
		case "/api/v1/admin/ops/alert-events?limit=50":
			_, _ = w.Write(alertJSON)
		case "/api/v1/admin/ops/ingress-rejections/health":
			_, _ = w.Write(ingressJSON)
		case "/api/v1/admin/ops/auth-cache-invalidation/health":
			_, _ = w.Write(authCacheJSON)
		case "/api/v1/admin/accounts?page=1&page_size=100":
			_, _ = w.Write(accountsJSON)
		default:
			t.Errorf("unexpected URI: %s", r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	ctx := context.Background()

	events, meta, err := client.FetchOpsAlertEvents(ctx, inst)
	if err != nil || len(events) != 2 || meta.Endpoint != EndpointOpsAlertEvents || meta.HTTPStatus != http.StatusOK {
		t.Fatalf("FetchOpsAlertEvents = (%d, %+v, %v)", len(events), meta, err)
	}

	ingress, meta, err := client.FetchIngressRejectHealth(ctx, inst)
	if err != nil || ingress == nil || ingress.Cardinality != 142 || meta.Endpoint != EndpointOpsIngressHealth {
		t.Fatalf("FetchIngressRejectHealth = (%+v, %+v, %v)", ingress, meta, err)
	}

	authCache, meta, err := client.FetchAuthCacheHealth(ctx, inst)
	if err != nil || authCache == nil || !authCache.Healthy || meta.Endpoint != EndpointOpsAuthCacheHealth {
		t.Fatalf("FetchAuthCacheHealth = (%+v, %+v, %v)", authCache, meta, err)
	}

	accounts, meta, err := client.FetchAccountsDirectory(ctx, inst)
	if err != nil || len(accounts) != 2 || meta.Endpoint != EndpointAccounts || meta.HTTPStatus != http.StatusOK {
		t.Fatalf("FetchAccountsDirectory = (%d, %+v, %v)", len(accounts), meta, err)
	}

	// All 4 endpoints must be accounted in audit budget.
	if usage := client.BudgetUsage(inst.InstanceID, 0); usage.UsedToday != 4 {
		t.Fatalf("UsedToday = %d, want 4", usage.UsedToday)
	}
}

func TestClientFetchAccountsDirectoryPaginationCapAndEarlyStop(t *testing.T) {
	makePage := func(page, count int, total int64) []byte {
		items := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			id := int64((page-1)*100 + i + 1)
			items = append(items, map[string]any{
				"id":          id,
				"name":        fmt.Sprintf("acc-%d", id),
				"platform":    "openai",
				"status":      "active",
				"schedulable": true,
				"credentials": map[string]any{"api_key": fmt.Sprintf("sk-secret-%d", id)},
			})
		}
		raw, _ := json.Marshal(map[string]any{
			"code": 0,
			"data": map[string]any{
				"items":     items,
				"total":     total,
				"page":      page,
				"page_size": 100,
			},
		})
		return raw
	}

	t.Run("caps_at_10_pages", func(t *testing.T) {
		var pagesRequested atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			pagesRequested.Add(1)
			_, _ = w.Write(makePage(page, 100, 1500))
		}))
		defer server.Close()

		client, inst := testClient(server.URL, config.SiteOpsConfig{DefaultAuditBudgetPerDay: 5000})
		items, meta, err := client.FetchAccountsDirectory(context.Background(), inst)
		if err != nil {
			t.Fatalf("FetchAccountsDirectory: %v", err)
		}
		if pagesRequested.Load() != 10 {
			t.Fatalf("pagesRequested = %d, want 10", pagesRequested.Load())
		}
		if len(items) != 1000 || meta.Endpoint != EndpointAccounts {
			t.Fatalf("len(items)=%d meta=%+v", len(items), meta)
		}
		if usage := client.BudgetUsage(inst.InstanceID, 0); usage.UsedToday != 10 {
			t.Fatalf("UsedToday = %d, want 10", usage.UsedToday)
		}
	})

	t.Run("stops_early_when_total_reached", func(t *testing.T) {
		var pagesRequested atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			pagesRequested.Add(1)
			if page == 1 {
				_, _ = w.Write(makePage(1, 100, 150))
				return
			}
			_, _ = w.Write(makePage(2, 50, 150))
		}))
		defer server.Close()

		client, inst := testClient(server.URL, config.SiteOpsConfig{})
		items, _, err := client.FetchAccountsDirectory(context.Background(), inst)
		if err != nil {
			t.Fatalf("FetchAccountsDirectory: %v", err)
		}
		if pagesRequested.Load() != 2 || len(items) != 150 {
			t.Fatalf("pages=%d items=%d, want 2 and 150", pagesRequested.Load(), len(items))
		}
	})

	t.Run("returns_budget_error_on_non_first_page", func(t *testing.T) {
		var pagesRequested atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			pagesRequested.Add(1)
			_, _ = w.Write(makePage(page, 100, 500))
		}))
		defer server.Close()

		client, inst := testClient(server.URL, config.SiteOpsConfig{DefaultAuditBudgetPerDay: 2})
		items, meta, err := client.FetchAccountsDirectory(context.Background(), inst)
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("err = %v, want ErrBudgetExceeded", err)
		}
		if items != nil || meta.ErrorCode != ErrCodeBudgetExceeded || pagesRequested.Load() != 2 {
			t.Fatalf("unexpected non-first-page budget result: items=%v meta=%+v pages=%d", items, meta, pagesRequested.Load())
		}
	})
}

func TestClientP1AErrorHandling401_403_429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/ops/alert-events":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("raw secret error"))
		case "/api/v1/admin/ops/ingress-rejections/health":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("forbidden secret detail"))
		case "/api/v1/admin/ops/auth-cache-invalidation/health":
			w.Header().Set("Retry-After", "45")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/api/v1/admin/accounts":
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	ctx := context.Background()

	if _, meta, err := client.FetchOpsAlertEvents(ctx, inst); !errors.Is(err, ErrUnauthorized) ||
		meta.ErrorCode != ErrCodeUnauthorized || strings.Contains(meta.SanitizedError, "raw secret") {
		t.Fatalf("401 handling = (%+v, %v)", meta, err)
	}
	if _, meta, err := client.FetchIngressRejectHealth(ctx, inst); !errors.Is(err, ErrForbidden) ||
		meta.ErrorCode != ErrCodeForbidden || strings.Contains(meta.SanitizedError, "forbidden secret") {
		t.Fatalf("403 handling = (%+v, %v)", meta, err)
	}
	if _, meta, err := client.FetchAuthCacheHealth(ctx, inst); !errors.Is(err, ErrRateLimited) ||
		meta.ErrorCode != ErrCodeRateLimited || meta.RetryAfter == nil || *meta.RetryAfter != 45*time.Second {
		t.Fatalf("429 handling = (%+v, %v)", meta, err)
	}
	if _, meta, err := client.FetchAccountsDirectory(ctx, inst); !errors.Is(err, ErrForbidden) ||
		meta.ErrorCode != ErrCodeForbidden {
		t.Fatalf("accounts 403 handling = (%+v, %v)", meta, err)
	}
}

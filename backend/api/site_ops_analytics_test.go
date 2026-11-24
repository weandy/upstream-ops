package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/siteops"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSiteOpsAnalyticsHTTPRoutes(t *testing.T) {
	var outbound atomic.Int32
	var deny atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/risk-control/status":
			outbound.Add(1)
			if deny.Load() {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"enabled":false,"key_hash":"deadbeef1234"}`)
		case "/api/v1/admin/backups":
			outbound.Add(1)
			w.WriteHeader(403)
		case "/api/v1/admin/channel-monitor-v2/snapshot":
			outbound.Add(1)
			fmt.Fprint(w, `{"channels":[]}`)
		case "/api/v1/admin/users":
			outbound.Add(1)
			fmt.Fprint(w, `{"items":[{"id":1,"email":"alice.chen@portal.example.com","username":"AliceRealName"}],"total":1}`)
		case "/api/v1/admin/users/1/api-keys":
			outbound.Add(1)
			fmt.Fprint(w, `[{"id":2,"key":"sk-canary-secret-9999","name":"ProdBillingKey"}]`)
		case "/api/v1/admin/dashboard/api-keys-usage", "/api/v1/admin/dashboard/users-usage":
			outbound.Add(1)
			fmt.Fprint(w, `{"stats":{}}`)
		case "/api/v1/admin/usage/stats":
			outbound.Add(1)
			fmt.Fprint(w, `{"total_requests":5}`)
		case "/api/v1/admin/ops/user-concurrency":
			outbound.Add(1)
			fmt.Fprint(w, `{"enabled":true,"user":{}}`)
		case "/api/v1/admin/subscriptions":
			outbound.Add(1)
			fmt.Fprint(w, `{"items":[{"id":2,"user_id":1,"group_id":3,"starts_at":"2024-01-01T00:00:00Z","expires_at":"2024-01-31T00:00:00Z","status":"expired"}],"total":1}`)
		case "/api/v1/admin/subscriptions/2/progress":
			outbound.Add(1)
			fmt.Fprint(w, `{"id":2,"expires_at":"2024-01-31T00:00:00Z"}`)
		case "/api/v1/admin/dashboard/models":
			outbound.Add(1)
			fmt.Fprint(w, `{"models":[]}`)
		case "/api/v1/admin/dashboard/groups":
			outbound.Add(1)
			fmt.Fprint(w, `{"groups":[]}`)
		case "/api/v1/admin/groups/all":
			outbound.Add(1)
			fmt.Fprint(w, "[]")
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	router, svc := setupSiteOpsTestRouter(t, false, config.SiteOpsConfig{AllowLoopback: true})
	inst, err := svc.CreateInstance(context.Background(), siteops.CreateInstanceInput{Name: "Portal", BaseURL: upstream.URL, APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/site-ops/instances/%d/analytics", inst.ID)
	do := func(method, path, body string, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		for _, secret := range []string{"alice.chen@portal.example.com", "AliceRealName", "sk-canary-secret-9999", "ProdBillingKey", "deadbeef1234", "target_binding"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("HTTP 泄漏")
			}
		}
		return w.Body.String()
	}
	before := outbound.Load()
	for _, q := range []string{"/users?force=1&force=0", "/users?top_n=0", "/users?timezone=x", "/users/-1", "/users/9007199254740992", "/subscriptions?group_id=%2B2", "/market?range=2d", "/lights?force=true", "/subscriptions/2?subscription_page=10001"} {
		do("GET", q, "", 400)
	}
	do("PUT", "/pricing/config", `{"unknown":1}`, 400)
	do("PUT", "/reference-prices/group/2", `{"generation":1,"currency":"CNY"}`, 400)
	do("DELETE", "/reference-prices/group/2", "", 400)
	if outbound.Load() != before {
		t.Fatal("invalid request made network call")
	}
	lights := do("GET", "/lights", "", 200)
	if !strings.Contains(lights, `"quality":"unauthorized"`) || !strings.Contains(lights, `"quality":"disabled"`) {
		t.Fatal("分域降级", lights)
	}
	for _, p := range []string{"/channel-status", "/users", "/users/1", "/subscriptions?status=all", "/market", "/pricing"} {
		do("GET", p, "", 200)
	}
	var detail struct {
		Data siteops.AnalyticsSubscriptionDetailResponse
	}
	for i := 0; i < 2; i++ {
		before = outbound.Load()
		body := do("GET", "/subscriptions/2", "", 200)
		if err := json.Unmarshal([]byte(body), &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Data.Subscription.SubscriptionID != 2 || detail.Data.Domains["subscription_progress"].Quality != siteops.QualityTrusted {
			t.Fatal(body)
		}
		if i == 1 && outbound.Load() != before {
			t.Fatal("已到期订阅重复请求上游")
		}
	}
	do("GET", "/users/8", "", 404)
	do("GET", "/subscriptions/9", "", 404)
	before = outbound.Load()
	do("PUT", "/pricing/config", `{"cny_per_usd":7.2,"leverage_warn_threshold":3,"leverage_danger_threshold":5}`, 200)
	do("PUT", "/reference-prices/group/2", `{"generation":1,"currency":"CNY","monthly_minor":0}`, 200)
	do("DELETE", "/reference-prices/group/2?generation=2", "", 409)
	do("DELETE", "/reference-prices/group/2?generation=1", "", 200)
	do("GET", "/market?range=7d&cache_only=1", "", 200)
	if outbound.Load() != before {
		t.Fatal("local edits made network call")
	}
	deny.Store(true)
	name := "Portal 2"
	if _, err := svc.UpdateInstance(context.Background(), inst.ID, siteops.UpdateInstanceInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if b := do("GET", "/lights", "", 502); !strings.Contains(b, "SITE_OPS_UNAUTHORIZED") {
		t.Fatal(b)
	}
}

func TestSiteOpsAnalyticsEndpointForbidden(t *testing.T) {
	for _, tc := range []struct{ route, endpoint, denied, trusted string }{
		{"/lights", "/risk-control/status", "risk_control", "backups"},
		{"/users", "/ops/user-concurrency", "user_concurrency", "users"},
		{"/users/7", "/users/7/api-keys", "user_keys", "users"},
	} {
		t.Run(tc.denied, func(t *testing.T) {
			var deniedCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin")
				if path == tc.endpoint {
					deniedCalls.Add(1)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				switch path {
				case "/users":
					fmt.Fprint(w, `{"items":[{"id":7}],"page_size":50,"total":1}`)
				case "/dashboard/users-usage":
					fmt.Fprint(w, `{"stats":{}}`)
				case "/ops/user-concurrency":
					fmt.Fprint(w, `{"user":{}}`)
				case "/usage/stats":
					fmt.Fprint(w, `{"total_requests":2}`)
				default:
					fmt.Fprint(w, "[]")
				}
			}))
			defer upstream.Close()
			router, svc := setupSiteOpsTestRouter(t, false, config.SiteOpsConfig{AllowLoopback: true})
			inst, err := svc.CreateInstance(context.Background(), siteops.CreateInstanceInput{Name: "Portal", BaseURL: upstream.URL, APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/site-ops/instances/%d/analytics%s", inst.ID, tc.route), nil))
				var result struct {
					Data struct {
						siteops.AnalyticsMeta
						Users siteops.AnalyticsPage[siteops.AnalyticsUserRow]
						User  siteops.AnalyticsUserRow
					}
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
					t.Fatal(w.Code, w.Body.String())
				}
				v := result.Data
				if v.Domains[tc.denied].Quality != siteops.QualityUnauthorized || v.Domains[tc.trusted].Quality != siteops.QualityTrusted {
					t.Fatal(v.Domains)
				}
				if tc.route == "/users" && (v.Users.PageSize != 50 || len(v.Users.Items) != 1 || v.Users.Items[0].UserID != 7) || tc.route == "/users/7" && v.User.UserID != 7 {
					t.Fatal(w.Body.String())
				}
				if deniedCalls.Load() != 1 {
					t.Fatal("403 负缓存未生效", deniedCalls.Load())
				}
			}
		})
	}
}

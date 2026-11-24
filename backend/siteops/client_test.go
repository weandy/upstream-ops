package siteops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
)

const testAdminKey = "adm_sc_example_for_tests"

func testClient(origin string, cfg config.SiteOpsConfig) (*Client, TargetEndpoint) {
	cfg.AllowedOrigins = []string{origin}
	return NewClient(cfg, config.ProxyConfig{}, config.UpstreamConfig{}, nil, nil),
		TargetEndpoint{InstanceID: 1, BaseURL: origin, APIKey: testAdminKey}
}

func TestHealthAndRedirectPolicy(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case healthPath:
			if _, ok := r.Header[http.CanonicalHeaderKey("x-api-key")]; ok {
				t.Error("/health carried the Admin header")
			}
			w.WriteHeader(http.StatusOK)
		case versionPath:
			if r.Header.Get("x-api-key") != testAdminKey {
				t.Error("the approved Admin endpoint did not receive its key")
			}
			http.Redirect(w, r, destination.URL, http.StatusFound)
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	inst.BaseURL += "/sub2api"
	health, err := client.ProbeHealth(ctx, inst)
	if err != nil || !health.Reachable || health.HTTPStatus != 200 || health.CheckedAt.IsZero() {
		t.Fatalf("health = %+v, %v", health, err)
	}
	if usage := client.BudgetUsage(inst.InstanceID, 0); usage.UsedToday != 0 {
		t.Fatalf("health was charged: %+v", usage)
	}
	inst.BaseURL = destination.URL
	if _, _, err := client.FetchSystemVersion(ctx, inst); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("unapproved origin = %v", err)
	}
	inst.BaseURL = server.URL
	if _, meta, err := client.FetchSystemVersion(ctx, inst); !errors.Is(err, ErrRedirectBlocked) || meta.HTTPStatus != http.StatusFound {
		t.Fatalf("redirect = %+v, %v", meta, err)
	}
	if redirected.Load() != 0 {
		t.Fatal("unexpected request to the unapproved origin")
	}
}

func TestConditionalObservationAndAuditBudget(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1, 4:
			if r.Header.Get("If-None-Match") != "" {
				t.Error("uncached request unexpectedly sent a validator")
			}
			w.Header().Set("ETag", `"v1"`)
			io.WriteString(w, `{"version":"v0.2.24","password":"example"}`)
		case 2:
			if r.Header.Get("If-None-Match") != `"v1"` {
				t.Error("missing cached ETag")
			}
			w.WriteHeader(http.StatusNotModified)
		case 3:
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "raw upstream detail")
		default:
			t.Error("budget allowed an unexpected call")
		}
	}))
	defer server.Close()
	client, inst := testClient(server.URL, config.SiteOpsConfig{DefaultAuditBudgetPerDay: 3})
	ctx := context.Background()
	first, baseline, err := client.FetchSystemVersion(ctx, inst)
	if err != nil || first.Version != "v0.2.24" || baseline.ObservedAt.IsZero() {
		t.Fatalf("first response = %+v, %+v, %v", first, baseline, err)
	}
	first.Version = "modified by caller"
	waitUntil(t, func() bool { return time.Now().After(baseline.VerifiedAt) })
	second, cached, err := client.FetchSystemVersion(ctx, inst)
	if err != nil || second.Version != "v0.2.24" || !cached.NotModified || cached.HTTPStatus != 304 ||
		cached.ETag != `"v1"` || !cached.ObservedAt.Equal(baseline.ObservedAt) || !cached.VerifiedAt.After(baseline.VerifiedAt) {
		t.Fatalf("304 response = %+v, %+v, %v", second, cached, err)
	}
	if _, denied, err := client.FetchSystemVersion(ctx, inst); !errors.Is(err, ErrForbidden) ||
		denied.ErrorCode != ErrCodeForbidden || strings.Contains(denied.SanitizedError, "raw upstream detail") {
		t.Fatalf("403 response = %+v, %v", denied, err)
	}
	if _, _, err := client.FetchSystemVersion(ctx, inst); !errors.Is(err, ErrBudgetExceeded) || hits.Load() != 3 {
		t.Fatalf("budget exceeded = %v; hits = %d", err, hits.Load())
	}
	usage := client.BudgetUsage(inst.InstanceID, 0)
	if usage.UsedToday != 3 || usage.UsedLastMin != 3 || usage.DailyLimit != 3 || usage.PerMinLimit != 12 ||
		usage.ResetAt == nil || usage.ResetAt.Hour() != 0 || usage.ResetAt.Location() != time.UTC {
		t.Fatalf("usage = %+v", usage)
	}
	client.ResetInstanceState(inst.InstanceID)
	// ResetInstanceState must clear ETag cache while preserving daily audit budget accounting.
	if _, _, err := client.FetchSystemVersion(ctx, inst); !errors.Is(err, ErrBudgetExceeded) || hits.Load() != 3 {
		t.Fatalf("reset should preserve daily budget: err = %v; hits = %d", err, hits.Load())
	}
	inst.AuditBudgetPerDay = 4
	if _, _, err := client.FetchSystemVersion(ctx, inst); err != nil || hits.Load() != 4 {
		t.Fatalf("fetch after reset with raised budget = %v; hits = %d", err, hits.Load())
	}
}

func TestAuditMinuteLimitAndUTCDateRollover(t *testing.T) {
	client, inst := testClient("http://portal.example.com", config.SiteOpsConfig{})
	inst.AuditBudgetPerDay = 2400
	for i := 0; i < 12; i++ {
		charge, release, err := client.reserveAudit(inst, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		charge()
		release()
	}
	if _, _, err := client.reserveAudit(inst, context.Background()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("minute limit = %v", err)
	}
	now := time.Date(2026, 11, 24, 0, 0, 30, 0, time.UTC)
	window := auditWindow{
		date: "2026-11-23", used: 10,
		recent: []time.Time{now.Add(-time.Minute), now.Add(-45 * time.Second)},
	}
	window.advance(now)
	if window.date != "2026-11-24" || window.used != 0 || len(window.recent) != 1 {
		t.Fatalf("rolled window = %+v", window)
	}
}

func TestETagIsolationAcrossCredentialChangeAndReset(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := hits.Add(1)
		validator := r.Header.Get("If-None-Match")
		if call == 3 {
			if validator != `"observation-2"` {
				t.Error("missing validator for the new credentials")
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if validator != "" {
			t.Error("sent a validator after credentials changed or state was reset")
		}
		w.Header().Set("ETag", fmt.Sprintf("\"observation-%d\"", call))
		io.WriteString(w, `{"version":"v0.2.24"}`)
	}))
	defer server.Close()
	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	ctx := context.Background()
	_, first, err := client.FetchSystemVersion(ctx, inst)
	if err != nil {
		t.Fatal(err)
	}
	inst.CredGen++
	inst.APIKey += "_rotated"
	waitUntil(t, func() bool { return time.Now().After(first.ObservedAt) })
	_, second, err := client.FetchSystemVersion(ctx, inst)
	if err != nil || !second.ObservedAt.After(first.ObservedAt) {
		t.Fatalf("rotated response = %+v, %v", second, err)
	}
	_, verified, err := client.FetchSystemVersion(ctx, inst)
	if err != nil || !verified.NotModified || !verified.ObservedAt.Equal(second.ObservedAt) {
		t.Fatalf("rotated 304 = %+v, %v", verified, err)
	}
	client.ResetInstanceState(inst.InstanceID)
	_, reset, err := client.FetchSystemVersion(ctx, inst)
	if err != nil || reset.NotModified || hits.Load() != 4 {
		t.Fatalf("reset response = %+v, %v, hits = %d", reset, err, hits.Load())
	}
}

func TestRequestAndBatchDeadlines(t *testing.T) {
	for _, cfg := range []config.SiteOpsConfig{
		{RequestTimeoutSeconds: 1, BatchTimeoutSeconds: 10},
		{RequestTimeoutSeconds: 10, BatchTimeoutSeconds: 1},
	} {
		t.Run(fmt.Sprintf("request=%ds/batch=%ds", cfg.RequestTimeoutSeconds, cfg.BatchTimeoutSeconds), func(t *testing.T) {
			canceled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
				close(canceled)
			}))
			defer server.Close()
			client, inst := testClient(server.URL, cfg)
			if _, _, err := client.FetchSystemVersion(context.Background(), inst); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline response = %v", err)
			}
			receive(t, canceled)
		})
	}
}

func TestRetryAfterFormatsAndBodyLimit(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "17")
		case 2:
			w.Header().Set("Retry-After", time.Now().UTC().Add(30*time.Second).Format(http.TimeFormat))
		default:
			io.WriteString(w, strings.Repeat("a", 33))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, inst := testClient(server.URL, config.SiteOpsConfig{MaxResponseBytes: 32})
	ctx := context.Background()
	for i, bounds := range [][2]time.Duration{{17 * time.Second, 17 * time.Second}, {28 * time.Second, 30 * time.Second}} {
		_, meta, err := client.FetchSystemVersion(ctx, inst)
		if !errors.Is(err, ErrRateLimited) || meta.RetryAfter == nil || *meta.RetryAfter < bounds[0] || *meta.RetryAfter > bounds[1] {
			t.Fatalf("retry format %d: %+v, %v", i, meta, err)
		}
		// 下一个响应样本使用独立实例，避免继承本次 429 的退避。
		inst.InstanceID++
	}
	if _, _, err := client.FetchSystemVersion(ctx, inst); !errors.Is(err, errResponseLimit) {
		t.Fatalf("oversized body = %v", err)
	}
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	for _, layout := range []string{time.RFC850, time.ANSIC} {
		date := now.Add(time.Minute).Format(layout)
		if layout == time.RFC850 {
			date = strings.ReplaceAll(date, "UTC", "GMT")
		}
		if got := parseRetryAfter(date, now); got == nil || *got != time.Minute {
			t.Errorf("Retry-After %s = %v", layout, got)
		}
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 2s")
		}
		time.Sleep(time.Millisecond)
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("channel did not receive within 3s")
		var zero T
		return zero
	}
}

func TestSharedFlightCancellation(t *testing.T) {
	for _, cancelBoth := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelBoth=%t", cancelBoth), func(t *testing.T) {
			started, canceled, allowResponse := make(chan struct{}, 2), make(chan struct{}, 2), make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				started <- struct{}{}
				select {
				case <-r.Context().Done():
					canceled <- struct{}{}
				case <-allowResponse:
					io.WriteString(w, `{"version":"v0.2.24"}`)
				}
			}))
			defer server.Close()
			client, inst := testClient(server.URL, config.SiteOpsConfig{})
			first, cancelFirst := context.WithCancel(context.Background())
			defer cancelFirst()
			second, cancelSecond := context.WithCancel(context.Background())
			defer cancelSecond()
			call := func(ctx context.Context) <-chan error {
				done := make(chan error, 1)
				go func() {
					_, _, err := client.FetchSystemVersion(ctx, inst)
					done <- err
				}()
				return done
			}
			r1 := call(first)
			receive(t, started)
			r2 := call(second)
			waitUntil(t, func() bool {
				client.mu.Lock()
				defer client.mu.Unlock()
				for _, flight := range client.flights {
					if flight.waiters == 2 {
						return true
					}
				}
				return false
			})
			cancelFirst()
			if err := receive(t, r1); !errors.Is(err, context.Canceled) {
				t.Fatalf("first caller = %v", err)
			}
			select {
			case <-canceled:
				t.Fatal("upstream canceled while it still had a waiter")
			default:
			}
			if cancelBoth {
				cancelSecond()
				if err := receive(t, r2); !errors.Is(err, context.Canceled) {
					t.Fatalf("second caller = %v", err)
				}
				receive(t, canceled)
			} else {
				close(allowResponse)
				if err := receive(t, r2); err != nil {
					t.Fatalf("second caller = %v", err)
				}
			}
			if requests.Load() != 1 || client.BudgetUsage(inst.InstanceID, 0).UsedToday != 1 {
				t.Fatalf("shared request = %d HTTP calls, %+v", requests.Load(), client.BudgetUsage(inst.InstanceID, 0))
			}
		})
	}
}

func TestSiteOpsQueueLimits(t *testing.T) {
	client, _ := testClient("http://portal.example.com", config.SiteOpsConfig{
		GlobalMaxConcurrency: 2, PerInstanceMaxConcurrency: 1, PerInstanceQueueLimit: 1,
	})
	ctx := context.Background()
	if err := client.acquire(ctx, 1, time.Second); err != nil {
		t.Fatal(err)
	}
	defer client.release(1)
	if err := client.acquire(ctx, 2, time.Second); err != nil {
		t.Fatal(err)
	}
	defer client.release(2)

	queued := make(chan error, 1)
	go func() { queued <- client.acquire(ctx, 1, time.Second) }()
	waitUntil(t, func() bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.slots[1].queued == 1
	})
	if err := client.acquire(ctx, 1, time.Second); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("full instance queue = %v", err)
	}
	if err := client.acquire(ctx, 3, 50*time.Millisecond); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("saturated global slots = %v", err)
	}
	if err := receive(t, queued); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("queue deadline = %v", err)
	}
}

func TestProxyPinsResolvedTarget(t *testing.T) {
	var hits atomic.Int32
	expectedConnect := "192.0.2.30:80"
	expectedHost := "portal.example.com"
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodConnect || r.Host != expectedConnect || r.Header.Get("x-api-key") != "" {
			t.Errorf("unexpected CONNECT: %s %s (want %s), Admin header present = %v", r.Method, r.Host, expectedConnect, r.Header.Get("x-api-key") != "")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, stream, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		stream.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		stream.Flush()
		req, err := http.ReadRequest(stream.Reader)
		if err != nil {
			t.Error(err)
			return
		}
		if req.Host != expectedHost || req.URL.Path != versionPath || req.Header.Get("x-api-key") != testAdminKey {
			t.Error("tunneled request lost its upstream Host, path or credentials")
		}
		body := `{"version":"v0.2.24"}`
		fmt.Fprintf(stream, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		stream.Flush()
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	hostname, portText, _ := net.SplitHostPort(proxyURL.Host)
	port, _ := strconv.Atoi(portText)
	// Default empty SiteOpsConfig allows public resolved IPs and blocks cloud metadata / private IPs.
	siteCfg := config.SiteOpsConfig{}
	guard := NewOutboundGuard(false, nil, nil)
	guard.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	}
	proxyCfg := config.ProxyConfig{Enabled: true, Protocol: "http", Host: hostname, Port: port}
	client := NewClient(siteCfg, proxyCfg, config.UpstreamConfig{}, guard, nil)
	inst := TargetEndpoint{InstanceID: 1, APIKey: testAdminKey, BaseURL: "http://portal.example.com", ProxyEnabled: true}
	if _, _, err := client.FetchSystemVersion(context.Background(), inst); !errors.Is(err, ErrOriginNotAllowed) || hits.Load() != 0 {
		t.Fatalf("metadata proxy target = %v, %d proxy hits", err, hits.Load())
	}
	guard.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
	}
	if _, _, err := client.FetchSystemVersion(context.Background(), inst); !errors.Is(err, ErrOriginNotAllowed) || hits.Load() != 0 {
		t.Fatalf("private proxy target = %v, %d proxy hits", err, hits.Load())
	}
	guard.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.30")}, nil
	}
	if result, _, err := client.FetchSystemVersion(context.Background(), inst); err != nil || result.Version != "v0.2.24" || hits.Load() != 1 {
		t.Fatalf("public proxy target = %+v, %v, %d proxy hits", result, err, hits.Load())
	}
	// Explicit internal Docker exemption works and revokes immediately upon runtime config update.
	expectedConnect, expectedHost = "172.18.0.3:8080", "sub2api:8080"
	client.UpdateRuntimeConfig(config.SiteOpsConfig{AllowedOrigins: []string{"http://sub2api:8080"}}, proxyCfg, config.UpstreamConfig{})
	guard.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("172.18.0.3")}, nil
	}
	dockerInst := TargetEndpoint{InstanceID: 2, APIKey: testAdminKey, BaseURL: "http://sub2api:8080", ProxyEnabled: true}
	if result, _, err := client.FetchSystemVersion(context.Background(), dockerInst); err != nil || result.Version != "v0.2.24" || hits.Load() != 2 {
		t.Fatalf("exempted internal proxy target = %+v, %v, %d proxy hits", result, err, hits.Load())
	}
	client.UpdateRuntimeConfig(config.SiteOpsConfig{}, proxyCfg, config.UpstreamConfig{})
	if _, _, err := client.FetchSystemVersion(context.Background(), dockerInst); !errors.Is(err, ErrOriginNotAllowed) || hits.Load() != 2 {
		t.Fatalf("revoked internal target = %v, %d proxy hits", err, hits.Load())
	}
}

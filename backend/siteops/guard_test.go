package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/gin-gonic/gin"
)

func TestSiteOpsAccessGate(t *testing.T) {
	auth := false
	cfg := config.SiteOpsConfig{}
	router := gin.New()
	router.Use(RequireAccessMiddleware(func() bool { return auth }, func() config.SiteOpsConfig { return cfg }))
	router.Any("/site-ops", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, tc := range []struct {
		name, method, remote string
		auth, local          bool
		want                 int
	}{
		{"default closed", http.MethodGet, "127.0.0.1:1200", false, false, 403},
		{"spoofed proxy headers", http.MethodGet, "192.0.2.1:1200", false, true, 403},
		{"other v4 loopback", http.MethodGet, "127.0.0.2:1200", false, true, 403},
		{"mapped loopback", http.MethodGet, "[::ffff:127.0.0.1]:1200", false, true, 403},
		{"v4 loopback", http.MethodGet, "127.0.0.1:1200", false, true, 204},
		{"v6 loopback", http.MethodHead, "[::1]:1200", false, true, 204},
		{"local write blocked", http.MethodPost, "127.0.0.1:1200", false, true, 403},
		{"authentication active", http.MethodPost, "192.0.2.1:1200", true, false, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, cfg.AllowUnauthenticatedLocalOnly = tc.auth, tc.local
			req := httptest.NewRequest(tc.method, "/site-ops", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			req.Header.Set("X-Real-IP", "::1")
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("status = %d, want %d", resp.Code, tc.want)
			}
			if tc.want == http.StatusForbidden {
				var body map[string]string
				if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil ||
					body["code"] != ErrCodeAuthRequired || body["error"] != ErrAuthRequired.Error() {
					t.Fatalf("unexpected forbidden response: %v", body)
				}
			}
			if got := IsPollingAllowed(auth, cfg); got != (tc.auth || tc.local) {
				t.Fatalf("IsPollingAllowed = %v", got)
			}
		})
	}
}

func TestOutboundOriginPolicy(t *testing.T) {
	g := NewOutboundGuard([]string{"https://portal.example.com"}, []string{"192.0.2.0/24"})
	for _, tc := range []struct{ url, want string }{
		{"https://PORTAL.example.com:0443/sub2api///", "https://portal.example.com:443/sub2api"},
		{"https://portal.example.com", "https://portal.example.com"},
		{"http://192.0.2.15:8080/", "http://192.0.2.15:8080"},
		{"http://portal.example.com", ""},
		{"https://portal.example.com:8443", ""},
		{"https://portal.example.com.invalid", ""},
		{"https://example:example@portal.example.com", ""},
		{"https://portal.example.com?query=1", ""},
		{"https://portal.example.com#fragment", ""},
		{"ftp://192.0.2.15", ""},
		{"http://192.0.3.15", ""},
	} {
		got, err := g.ValidateBaseURL(tc.url)
		if got != tc.want || (tc.want == "" && !errors.Is(err, ErrOriginNotAllowed)) || (tc.want != "" && err != nil) {
			t.Errorf("ValidateBaseURL(%q) = %q, %v; want %q", tc.url, got, err, tc.want)
		}
	}
	g.UpdatePolicy(nil, nil)
	if _, err := g.ValidateBaseURL("https://portal.example.com"); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("cleared policy allowed origin: %v", err)
	}
}

func TestOutboundDialChecksResolvedAddresses(t *testing.T) {
	g := NewOutboundGuard([]string{"http://portal.example.com:80"}, nil)
	ip := netip.MustParseAddr
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.2", "169.254.169.254", "100.100.100.200", "fd00::1", "fe80::1"} {
		g.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{ip(address)}, nil
		}
		if conn, err := g.DialContext(context.Background(), "tcp", "portal.example.com:80"); !errors.Is(err, ErrOriginNotAllowed) {
			if conn != nil {
				conn.Close()
			}
			t.Errorf("resolved %s: got %v, want ErrOriginNotAllowed", address, err)
		}
	}
	g.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{ip("127.0.0.1"), ip("192.0.2.10")}, nil
	}
	targets, err := g.resolveTarget(context.Background(), "tcp", "portal.example.com:80")
	if err != nil || len(targets) != 1 || targets[0] != "192.0.2.10:80" {
		t.Fatalf("checked targets = %v, %v", targets, err)
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	g.UpdatePolicy([]string{"http://" + listener.Addr().String()}, nil)
	dial := func(addr string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn, err := g.DialContext(ctx, "tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	dial(listener.Addr().String())
	g.UpdatePolicy([]string{"http://portal.example.com:" + port}, []string{"127.0.0.1/32"})
	dial("portal.example.com:" + port)
}

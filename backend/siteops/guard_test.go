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
			if got := IsPollingAllowed(auth, cfg); !got {
				t.Fatalf("IsPollingAllowed = %v, want true for P0-P2", got)
			}
		})
	}
}

func TestOutboundDefaultPublicAndInternalBlocklist(t *testing.T) {
	// Default policy: allow public domains & public IPs, block loopback / private / reserved / cloud metadata / single-label internal names.
	g := NewOutboundGuard(false, nil, nil)
	for _, tc := range []struct{ url, want string }{
		{"https://PORTAL.example.com:0443/sub2api///", "https://portal.example.com:443/sub2api"},
		{"https://portal.example.com", "https://portal.example.com"},
		{"http://portal.example.com:8443", "http://portal.example.com:8443"},
		{"http://192.0.2.15:8080/", "http://192.0.2.15:8080"},
		{"https://example:example@portal.example.com", ""},
		{"https://portal.example.com?query=1", ""},
		{"https://portal.example.com#fragment", ""},
		{"ftp://192.0.2.15", ""},
		{"http://localhost:8080", ""},
		{"http://api.localhost:8080", ""},
		{"http://localhost.:8080", ""},
		{"http://sub2api:8080", ""},
		{"http://127.0.0.1:8080", ""},
		{"http://[::1]:8080", ""},
		{"http://[::ffff:127.0.0.1]:8080", ""},
		{"http://10.0.0.1:8080", ""},
		{"http://172.16.0.1:8080", ""},
		{"http://192.168.1.1:8080", ""},
		{"http://100.64.0.1:8080", ""},
		{"http://169.254.169.254/latest/meta-data", ""},
		{"http://100.100.100.200/latest/meta-data", ""},
		{"http://[fe80::1]:8080", ""},
		{"http://[fd00::1]:8080", ""},
		{"http://[fd00:ec2::254]/latest", ""},
		{"http://0.0.0.0:8080", ""},
	} {
		got, err := g.ValidateBaseURL(tc.url)
		if got != tc.want || (tc.want == "" && !errors.Is(err, ErrOriginNotAllowed)) || (tc.want != "" && err != nil) {
			t.Errorf("default ValidateBaseURL(%q) = %q, %v; want %q", tc.url, got, err, tc.want)
		}
	}

	// allowLoopback=true permits localhost/127.0.0.1/::1 only; RFC1918 private & cloud metadata remain blocked.
	g.UpdatePolicy(true, nil, nil)
	for _, allowed := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, err := g.ValidateBaseURL(allowed); err != nil {
			t.Errorf("allowLoopback ValidateBaseURL(%q) unexpected err: %v", allowed, err)
		}
	}
	for _, blocked := range []string{"http://10.0.0.1:8080", "http://192.168.1.1:8080", "http://169.254.169.254", "http://[fe80::1]"} {
		if _, err := g.ValidateBaseURL(blocked); !errors.Is(err, ErrOriginNotAllowed) {
			t.Errorf("allowLoopback should still block %q, got %v", blocked, err)
		}
	}

	// Explicit allowedOrigins / allowedCIDRs permit private Docker/LAN targets, while NEVER exempting cloud metadata / link-local / unspecified.
	g.UpdatePolicy(false, []string{"http://sub2api:8080", "http://169.254.169.254"}, []string{"172.18.0.0/16", "169.254.0.0/16", "100.64.0.0/10"})
	for _, allowed := range []string{"http://sub2api:8080", "http://172.18.0.5:8080"} {
		if _, err := g.ValidateBaseURL(allowed); err != nil {
			t.Errorf("exempted internal target %q unexpected err: %v", allowed, err)
		}
	}
	for _, hardBlocked := range []string{"http://169.254.169.254", "http://100.100.100.200", "http://[fe80::1]", "http://0.0.0.0:80", "http://[fd00:ec2::254]"} {
		if _, err := g.ValidateBaseURL(hardBlocked); !errors.Is(err, ErrOriginNotAllowed) {
			t.Errorf("hard-blocked target %q must never be exempted, got %v", hardBlocked, err)
		}
	}
}

func TestOutboundDialChecksResolvedAddresses(t *testing.T) {
	// Default policy: public domain works without allowlist, while DNS rebinding to internal/metadata IPs is blocked.
	g := NewOutboundGuard(false, nil, nil)
	ip := netip.MustParseAddr
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.2", "172.16.0.5", "192.168.1.10", "169.254.169.254", "100.100.100.200", "fd00::1", "fe80::1", "0.0.0.0", "fd00:ec2::254"} {
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

	// allowLoopback=true MUST NOT allow an arbitrary public domain DNS-rebinding to 127.0.0.1.
	g.UpdatePolicy(true, nil, nil)
	g.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{ip("127.0.0.1")}, nil
	}
	if _, err := g.resolveTarget(context.Background(), "tcp", "evil.example.com:80"); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("allowLoopback allowed non-localhost domain rebinding to 127.0.0.1: %v", err)
	}

	// Same-host Docker origin exemption allows private IP while still hard-blocking cloud metadata.
	g.UpdatePolicy(false, []string{"http://sub2api:8080"}, nil)
	g.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{ip("169.254.169.254")}, nil
	}
	if _, err := g.resolveTarget(context.Background(), "tcp", "sub2api:8080"); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("exempted origin allowed cloud metadata IP: %v", err)
	}
	g.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{ip("172.18.0.3")}, nil
	}
	targets, err = g.resolveTarget(context.Background(), "tcp", "sub2api:8080")
	if err != nil || len(targets) != 1 || targets[0] != "172.18.0.3:8080" {
		t.Fatalf("exempted Docker origin targets = %v, %v", targets, err)
	}

	// Actual loopback dial works when explicitly permitted by allowLoopback, allowedOrigins, or allowedCIDRs.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
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
	g.UpdatePolicy(true, nil, nil)
	dial(listener.Addr().String())
	g.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{ip("127.0.0.1")}, nil
	}
	dial("localhost:" + port)
	g.UpdatePolicy(false, []string{"http://" + listener.Addr().String()}, nil)
	dial(listener.Addr().String())
	g.UpdatePolicy(false, nil, []string{"127.0.0.1/32"})
	dial("portal.example.com:" + port)
}

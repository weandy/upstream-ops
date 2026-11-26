package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/gin-gonic/gin"
)

func TestNewRouterTrustedProxiesBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("empty trusted proxies ignores forwarded headers", func(t *testing.T) {
		r, err := newRouter(config.ServerConfig{Mode: gin.TestMode})
		if err != nil {
			t.Fatalf("newRouter: %v", err)
		}
		var gotIP string
		r.GET("/ip", func(c *gin.Context) {
			gotIP = c.ClientIP()
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/ip", nil)
		req.RemoteAddr = "198.51.100.10:12345"
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		req.Header.Set("X-Real-IP", "203.0.113.8")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if gotIP != "198.51.100.10" {
			t.Fatalf("ClientIP = %q, want 198.51.100.10 (spoofed headers must be ignored)", gotIP)
		}
	})

	t.Run("configured trusted proxy reads X-Forwarded-For only from trusted peer", func(t *testing.T) {
		r, err := newRouter(config.ServerConfig{
			Mode:           gin.TestMode,
			TrustedProxies: []string{"172.18.0.1"},
		})
		if err != nil {
			t.Fatalf("newRouter: %v", err)
		}
		var gotIP string
		r.GET("/ip", func(c *gin.Context) {
			gotIP = c.ClientIP()
			c.Status(http.StatusOK)
		})

		// 1. 来自受信任代理的 X-Forwarded-For 生效。
		reqTrusted := httptest.NewRequest(http.MethodGet, "/ip", nil)
		reqTrusted.RemoteAddr = "172.18.0.1:43210"
		reqTrusted.Header.Set("X-Forwarded-For", "203.0.113.9")
		r.ServeHTTP(httptest.NewRecorder(), reqTrusted)
		if gotIP != "203.0.113.9" {
			t.Fatalf("trusted peer ClientIP = %q, want 203.0.113.9", gotIP)
		}

		// 2. 来自受信任代理但仅携带 X-Real-IP 时忽略 X-Real-IP，回落到对端地址。
		reqRealIP := httptest.NewRequest(http.MethodGet, "/ip", nil)
		reqRealIP.RemoteAddr = "172.18.0.1:43211"
		reqRealIP.Header.Set("X-Real-IP", "203.0.113.9")
		r.ServeHTTP(httptest.NewRecorder(), reqRealIP)
		if gotIP != "172.18.0.1" {
			t.Fatalf("X-Real-IP fallback should be disabled, got %q", gotIP)
		}

		// 3. 来自未授权对端的伪造 X-Forwarded-For 被拒绝。
		reqUntrusted := httptest.NewRequest(http.MethodGet, "/ip", nil)
		reqUntrusted.RemoteAddr = "198.51.100.20:54321"
		reqUntrusted.Header.Set("X-Forwarded-For", "203.0.113.9")
		r.ServeHTTP(httptest.NewRecorder(), reqUntrusted)
		if gotIP != "198.51.100.20" {
			t.Fatalf("untrusted peer ClientIP = %q, want 198.51.100.20", gotIP)
		}
	})

	t.Run("invalid or wildcard trusted proxies rejected", func(t *testing.T) {
		for _, bad := range [][]string{{"not-an-ip"}, {"0.0.0.0/0"}, {"::/0"}} {
			if _, err := newRouter(config.ServerConfig{Mode: gin.TestMode, TrustedProxies: bad}); err == nil {
				t.Fatalf("expected error for TrustedProxies=%v", bad)
			}
		}
	})
}

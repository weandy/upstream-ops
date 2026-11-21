package embed

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/gin-gonic/gin"
)

func TestIPRateLimiterTokenBucketRefillAndCap(t *testing.T) {
	now := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	lim := newIPRateLimiter(true, 2.0, 3, func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	lim.maxTracked = 4

	// 1. Burst = 3，前 3 次放行，第 4 次限流并给出 Retry-After >= 1。
	for i := 0; i < 3; i++ {
		if ok, _ := lim.Allow("1.2.3.4"); !ok {
			t.Fatalf("expected burst request %d allowed", i+1)
		}
	}
	if ok, retry := lim.Allow("1.2.3.4"); ok || retry < 1 {
		t.Fatalf("expected 4th request rate limited with retry>=1, got (%v, %d)", ok, retry)
	}

	// 2. 推进 500ms（2 req/s -> 补回 1 个令牌）应恢复放行。
	mu.Lock()
	now = now.Add(500 * time.Millisecond)
	mu.Unlock()
	if ok, _ := lim.Allow("1.2.3.4"); !ok {
		t.Fatalf("expected token refill after 500ms")
	}

	// 3. 验证 maxTracked 硬上限：写入 20 个不同 IP，内存桶数绝不超过 maxTracked=4。
	for i := 0; i < 20; i++ {
		mu.Lock()
		now = now.Add(time.Millisecond)
		mu.Unlock()
		lim.Allow(fmt.Sprintf("10.0.0.%d", i))
	}
	if got := lim.TrackedCount(); got > 4 {
		t.Fatalf("tracked IP buckets exceeded cap 4: got %d", got)
	}
}

func TestEmbedPublicRoutesRateLimitResponses(t *testing.T) {
	svc, _ := openTestEmbedEnv(t)
	now := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	svc.NowFunc = func() time.Time { return now }

	on := true
	ttl := 5
	svc.UpdateRuntimeConfig(config.EmbedConfig{
		CacheTTLSeconds:  &ttl,
		RateLimitEnabled: &on,
		RateLimitRPS:     1.0,
		RateLimitBurst:   2,
	})

	router := gin.New()
	svc.RegisterPublic(router)

	// 前 2 次放行。
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/embed", nil)
		req.RemoteAddr = "198.51.100.10:12345"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, rec.Code)
		}
	}

	// 第 3 次 HTML 请求应返回 429 + Retry-After + 零脚本且不含 <meta http-equiv="refresh">。
	reqHTML429 := httptest.NewRequest(http.MethodGet, "/embed", nil)
	reqHTML429.RemoteAddr = "198.51.100.10:12345"
	recHTML429 := httptest.NewRecorder()
	router.ServeHTTP(recHTML429, reqHTML429)
	if recHTML429.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on HTML, got %d", recHTML429.Code)
	}
	if recHTML429.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429")
	}
	bodyLower := strings.ToLower(recHTML429.Body.String())
	if strings.Contains(bodyLower, "<script") || strings.Contains(bodyLower, "http-equiv=\"refresh\"") {
		t.Fatalf("429 HTML must have no script or meta refresh: %s", recHTML429.Body.String())
	}

	// 同 IP 访问 /api/public/status 返回 429 JSON。
	reqJSON429 := httptest.NewRequest(http.MethodGet, "/api/public/status", nil)
	reqJSON429.RemoteAddr = "198.51.100.10:54321"
	recJSON429 := httptest.NewRecorder()
	router.ServeHTTP(recJSON429, reqJSON429)
	if recJSON429.Code != http.StatusTooManyRequests || !strings.Contains(recJSON429.Body.String(), "rate limit exceeded") {
		t.Fatalf("expected 429 JSON, got code=%d body=%s", recJSON429.Code, recJSON429.Body.String())
	}

	// 另一 IP 不受影响（独立桶）。
	reqOther := httptest.NewRequest(http.MethodGet, "/embed", nil)
	reqOther.RemoteAddr = "198.51.100.20:11111"
	recOther := httptest.NewRecorder()
	router.ServeHTTP(recOther, reqOther)
	if recOther.Code != http.StatusOK {
		t.Fatalf("other IP should still get 200, got %d", recOther.Code)
	}

	// 热更新关闭限流后立即放行原被限 IP。
	off := false
	svc.UpdateRuntimeConfig(config.EmbedConfig{
		CacheTTLSeconds:  &ttl,
		RateLimitEnabled: &off,
	})
	reqUnblocked := httptest.NewRequest(http.MethodGet, "/embed", nil)
	reqUnblocked.RemoteAddr = "198.51.100.10:12345"
	recUnblocked := httptest.NewRecorder()
	router.ServeHTTP(recUnblocked, reqUnblocked)
	if recUnblocked.Code != http.StatusOK {
		t.Fatalf("expected 200 after disabling rate limit, got %d", recUnblocked.Code)
	}
}

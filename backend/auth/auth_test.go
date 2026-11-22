package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestMiddlewareRejectsQueryTokenAcceptsBearerAndCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, err := New("admin", "strong-pass", "secret-key-32-bytes-long!!!!!!!!", time.Hour)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	tok, _, err := svc.Login("admin", "strong-pass")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	r := gin.New()
	r.Use(svc.Middleware())
	r.GET("/api/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 1. 绝不允许通过 URL query ?token= 鉴权（防止泄露进日志和浏览器历史）。
	reqQuery := httptest.NewRequest(http.MethodGet, "/api/protected?token="+tok, nil)
	recQuery := httptest.NewRecorder()
	r.ServeHTTP(recQuery, reqQuery)
	if recQuery.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for query token, got %d", recQuery.Code)
	}

	// 2. Authorization: Bearer 正常通过。
	reqBearer := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	reqBearer.Header.Set("Authorization", "Bearer "+tok)
	recBearer := httptest.NewRecorder()
	r.ServeHTTP(recBearer, reqBearer)
	if recBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 for bearer token, got %d", recBearer.Code)
	}

	// 3. uh_token Cookie 正常通过。
	reqCookie := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	reqCookie.AddCookie(&http.Cookie{Name: "uh_token", Value: tok})
	recCookie := httptest.NewRecorder()
	r.ServeHTTP(recCookie, reqCookie)
	if recCookie.Code != http.StatusOK {
		t.Fatalf("expected 200 for cookie token, got %d", recCookie.Code)
	}
}

func TestLoginRateLimiterBlocksBruteForceAndResetsOnSuccess(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	lim := NewLoginRateLimiter(3, time.Minute, func() time.Time { return now })

	// 前 3 次失败后，第 4 次尝试应被拦截。
	for i := 0; i < 3; i++ {
		if ok, _ := lim.Allow("198.51.100.1"); !ok {
			t.Fatalf("attempt %d should be allowed before reaching threshold", i+1)
		}
		lim.RecordFailure("198.51.100.1")
	}
	if ok, retry := lim.Allow("198.51.100.1"); ok || retry < 1 {
		t.Fatalf("expected IP locked after 3 failures, got ok=%v retry=%d", ok, retry)
	}

	// 其他 IP 不受影响。
	if ok, _ := lim.Allow("198.51.100.2"); !ok {
		t.Fatalf("expected independent IP to remain allowed")
	}

	// 冷却期结束后恢复允许，成功登录后清空失败计数。
	now = now.Add(65 * time.Second)
	if ok, _ := lim.Allow("198.51.100.1"); !ok {
		t.Fatalf("expected IP unlocked after cooldown")
	}
	lim.RecordSuccess("198.51.100.1")
	lim.RecordFailure("198.51.100.1")
	if ok, _ := lim.Allow("198.51.100.1"); !ok {
		t.Fatalf("expected reset failure count after success")
	}
}

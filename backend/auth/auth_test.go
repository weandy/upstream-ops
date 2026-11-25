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

func TestMiddlewareEnforcesMustChangeCredentialsAndTokenCredVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "secret-key-32-bytes-long!!!!!!!!"
	svc, err := NewWithOptions("admin", "admin_ops", secret, time.Hour, true)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	tok, _, err := svc.Login("admin", "admin_ops")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	r := gin.New()
	r.Use(svc.Middleware())
	r.GET("/api/auth/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.POST("/api/auth/setup", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	r.GET("/api/channels", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// 1. 携带默认凭据 token 访问 /api/auth/me 和 /api/auth/setup 允许通过。
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/setup"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s expected 200 during setup, got %d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// 2. 业务接口必须返回 403，禁止绕过初始化。
	reqBiz := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	reqBiz.Header.Set("Authorization", "Bearer "+tok)
	recBiz := httptest.NewRecorder()
	r.ServeHTTP(recBiz, reqBiz)
	if recBiz.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for /api/channels before setup, got %d body=%s", recBiz.Code, recBiz.Body.String())
	}

	// 3. 即使保留同一用户名 admin 仅修改密码，旧 token 也必须因凭据版本变化而失效。
	svcUpdated, err := NewWithOptions("admin", "new-strong-pass", secret, time.Hour, false)
	if err != nil {
		t.Fatalf("new updated service: %v", err)
	}
	if _, err := svcUpdated.Verify(tok); err == nil {
		t.Fatalf("expected old token to be rejected after password change")
	}
}

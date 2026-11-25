package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/runtimeconfig"
	"github.com/gin-gonic/gin"
)

func TestAuthDefaultCredentialsForcedSetupFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg, _, err := config.LoadWithPath(cfgPath)
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("save defaults: %v", err)
	}

	secret := "test-auth-setup-secret-0123456789abcdef"
	authSvc, err := runtimeconfig.BuildAuth(cfg.Auth, secret)
	if err != nil {
		t.Fatalf("BuildAuth: %v", err)
	}
	if authSvc == nil || !authSvc.MustChangeCredentials() {
		t.Fatalf("expected default auth enabled with MustChangeCredentials=true")
	}

	rt := runtimeconfig.New(
		cfgPath,
		secret,
		nil,
		nil, nil, nil,
		authSvc,
		nil,
		config.ProxyConfig{},
		config.UpstreamConfig{},
		config.GatewayConfig{},
		nil,
	)

	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(rt.AuthMiddleware())
	registerAuth(apiGroup, &Deps{Runtime: rt})
	registerSettings(apiGroup, &Deps{Runtime: rt})

	// 1. 未登录访问受保护接口返回 401。
	recAnon := httptest.NewRecorder()
	r.ServeHTTP(recAnon, httptest.NewRequest(http.MethodGet, "/api/settings/config", nil))
	if recAnon.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 before login, got %d", recAnon.Code)
	}

	// 2. 使用默认账号密码 admin / admin_ops 登录成功，且返回 must_change_credentials=true。
	recLogin := httptest.NewRecorder()
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"admin_ops"}`))
	reqLogin.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(recLogin, reqLogin)
	if recLogin.Code != http.StatusOK {
		t.Fatalf("default login failed: code=%d body=%s", recLogin.Code, recLogin.Body.String())
	}
	var loginResp struct {
		Data struct {
			Token                 string `json:"token"`
			Username              string `json:"username"`
			MustChangeCredentials bool   `json:"must_change_credentials"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recLogin.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Data.Token == "" || !loginResp.Data.MustChangeCredentials {
		t.Fatalf("unexpected login response: %+v", loginResp.Data)
	}
	initToken := loginResp.Data.Token

	// 3. 修改前访问 /api/auth/me 允许（返回 must_change_credentials=true），但业务接口返回 403。
	reqMe := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	reqMe.Header.Set("Authorization", "Bearer "+initToken)
	recMe := httptest.NewRecorder()
	r.ServeHTTP(recMe, reqMe)
	if recMe.Code != http.StatusOK || !strings.Contains(recMe.Body.String(), `"must_change_credentials":true`) {
		t.Fatalf("unexpected /auth/me response before setup: code=%d body=%s", recMe.Code, recMe.Body.String())
	}

	reqBlocked := httptest.NewRequest(http.MethodGet, "/api/settings/config", nil)
	reqBlocked.Header.Set("Authorization", "Bearer "+initToken)
	recBlocked := httptest.NewRecorder()
	r.ServeHTTP(recBlocked, reqBlocked)
	if recBlocked.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on protected route before setup, got %d body=%s", recBlocked.Code, recBlocked.Body.String())
	}

	// 4. 拒绝把默认密码继续提交为新密码。
	reqBadSetup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"username":"owner","password":"admin_ops"}`))
	reqBadSetup.Header.Set("Content-Type", "application/json")
	reqBadSetup.Header.Set("Authorization", "Bearer "+initToken)
	recBadSetup := httptest.NewRecorder()
	r.ServeHTTP(recBadSetup, reqBadSetup)
	if recBadSetup.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when reusing default password, got %d body=%s", recBadSetup.Code, recBadSetup.Body.String())
	}

	// 5. 重设新账号密码后，返回新 token、持久化 config.yaml、旧 token 失效、新 token 放行。
	setupPayload, _ := json.Marshal(map[string]string{
		"username": "weandy_admin",
		"password": "StrongNewPassword!2026",
	})
	reqSetup := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(setupPayload))
	reqSetup.Header.Set("Content-Type", "application/json")
	reqSetup.Header.Set("Authorization", "Bearer "+initToken)
	recSetup := httptest.NewRecorder()
	r.ServeHTTP(recSetup, reqSetup)
	if recSetup.Code != http.StatusOK {
		t.Fatalf("setup failed: code=%d body=%s", recSetup.Code, recSetup.Body.String())
	}
	var setupResp struct {
		Data struct {
			Token                 string `json:"token"`
			Username              string `json:"username"`
			MustChangeCredentials bool   `json:"must_change_credentials"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recSetup.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	if setupResp.Data.Token == "" || setupResp.Data.Username != "weandy_admin" || setupResp.Data.MustChangeCredentials {
		t.Fatalf("unexpected setup response: %+v", setupResp.Data)
	}

	reqOldTok := httptest.NewRequest(http.MethodGet, "/api/settings/config", nil)
	reqOldTok.Header.Set("Authorization", "Bearer "+initToken)
	recOldTok := httptest.NewRecorder()
	r.ServeHTTP(recOldTok, reqOldTok)
	if recOldTok.Code != http.StatusUnauthorized {
		t.Fatalf("expected old token invalidated (401), got %d", recOldTok.Code)
	}

	reqNewTok := httptest.NewRequest(http.MethodGet, "/api/settings/config", nil)
	reqNewTok.Header.Set("Authorization", "Bearer "+setupResp.Data.Token)
	recNewTok := httptest.NewRecorder()
	r.ServeHTTP(recNewTok, reqNewTok)
	if recNewTok.Code != http.StatusOK {
		t.Fatalf("expected new token to access protected route (200), got %d body=%s", recNewTok.Code, recNewTok.Body.String())
	}

	saved, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("reload saved config: %v", err)
	}
	if !saved.Auth.Enabled || saved.Auth.Username != "weandy_admin" || saved.Auth.Password != "StrongNewPassword!2026" {
		t.Fatalf("saved auth mismatch: %+v", saved.Auth)
	}

	// 6. 已初始化后再次调用 /api/auth/setup 应返回 409。
	reqAgain := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(setupPayload))
	reqAgain.Header.Set("Content-Type", "application/json")
	reqAgain.Header.Set("Authorization", "Bearer "+setupResp.Data.Token)
	recAgain := httptest.NewRecorder()
	r.ServeHTTP(recAgain, reqAgain)
	if recAgain.Code != http.StatusConflict {
		t.Fatalf("expected 409 on repeat /auth/setup, got %d body=%s", recAgain.Code, recAgain.Body.String())
	}
}

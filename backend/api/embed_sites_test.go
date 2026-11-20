package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bejix/upstream-ops/backend/auth"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	embedsvc "github.com/bejix/upstream-ops/backend/embed"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/runtimeconfig"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

func TestEmbedSitesAPIAuthAndSPAIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(dir, "api-embed.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	secret := "test-api-embed-secret-0123456789"
	cipher, err := crypto.NewCipher(secret)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	authSvc, err := auth.New("admin", "secret-pass", secret, time.Hour)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	rt := runtimeconfig.New(
		filepath.Join(dir, "config.yaml"),
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

	intel := intelligence.NewService(db, cipher, nil, nil, nil)
	emb := embedsvc.NewService(db, intel)

	fakeSPA := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html><body>SPA_INDEX</body></html>")},
	}

	r := gin.New()
	Register(r, &Deps{
		DB:           db,
		Cipher:       cipher,
		Runtime:      rt,
		Intelligence: intel,
		Embed:        emb,
		Frontend:     fakeSPA,
	})

	// 1. 未鉴权访问管理接口 /api/embed-sites 必须返回 401。
	recUnauth := httptest.NewRecorder()
	r.ServeHTTP(recUnauth, httptest.NewRequest(http.MethodGet, "/api/embed-sites", nil))
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/embed-sites, got %d", recUnauth.Code)
	}

	// 2. 登录后获取管理 token。
	loginBody := `{"username":"admin","password":"secret-pass"}`
	recLogin := httptest.NewRecorder()
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(recLogin, reqLogin)
	if recLogin.Code != http.StatusOK {
		t.Fatalf("login failed: code=%d body=%s", recLogin.Code, recLogin.Body.String())
	}
	var loginResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recLogin.Body.Bytes(), &loginResp); err != nil || loginResp.Data.Token == "" {
		t.Fatalf("decode login token failed: %v body=%s", err, recLogin.Body.String())
	}
	token := loginResp.Data.Token

	// 3. 携带管理 token 完成 CRUD。
	createPayload := `{"slug":"demo-site","title":"渠道监控","eyebrow":"DEMO","enabled":true,"refresh_seconds":15,"allowed_origins":["https://demo.example"],"cors_origins":["https://demo.example"],"theme":"paper","target_ids":[],"show_identity_column":true}`
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/embed-sites", bytes.NewBufferString(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Authorization", "Bearer "+token)
	recCreate := httptest.NewRecorder()
	r.ServeHTTP(recCreate, reqCreate)
	if recCreate.Code != http.StatusCreated {
		t.Fatalf("create site code=%d body=%s", recCreate.Code, recCreate.Body.String())
	}
	var created struct {
		Data embedsvc.SiteDTO `json:"data"`
	}
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil || created.Data.ID == 0 {
		t.Fatalf("decode created site failed: %v", err)
	}

	// 4. 免登录访问公开 /embed 与 /api/public/status 必须正常返回 200（且不被 SPA 劫持）。
	recEmbed := httptest.NewRecorder()
	r.ServeHTTP(recEmbed, httptest.NewRequest(http.MethodGet, "/embed?site=demo-site", nil))
	if recEmbed.Code != http.StatusOK || !strings.Contains(recEmbed.Body.String(), "渠道监控") || strings.Contains(recEmbed.Body.String(), "SPA_INDEX") {
		t.Fatalf("unexpected /embed response: code=%d body=%s", recEmbed.Code, recEmbed.Body.String())
	}

	recPub := httptest.NewRecorder()
	reqPub := httptest.NewRequest(http.MethodGet, "/api/public/status?site=demo-site", nil)
	reqPub.Header.Set("Origin", "https://demo.example")
	r.ServeHTTP(recPub, reqPub)
	if recPub.Code != http.StatusOK || recPub.Header().Get("Access-Control-Allow-Origin") != "https://demo.example" {
		t.Fatalf("unexpected /api/public/status: code=%d cors=%q body=%s", recPub.Code, recPub.Header().Get("Access-Control-Allow-Origin"), recPub.Body.String())
	}

	// 5. 未知 /embed/unknown 与 /api/public/unknown 必须返回 404（绝不回退 SPA）。
	for _, missing := range []string{"/embed/unknown", "/api/public/unknown"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, missing, nil))
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "SPA_INDEX") {
			t.Fatalf("GET %s expected 404 without SPA fallback, got code=%d body=%s", missing, rec.Code, rec.Body.String())
		}
	}

	// 6. 管理端前端 SPA 路由 /embed-admin 与 /intelligence 必须回退到 SPA index.html。
	for _, spaPath := range []string{"/embed-admin", "/intelligence", "/gateway"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, spaPath, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SPA_INDEX") {
			t.Fatalf("GET %s expected SPA index.html, got code=%d body=%s", spaPath, rec.Code, rec.Body.String())
		}
	}

	// 7. 删除站点后对外 /embed?site=demo-site 返回 404。
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/embed-sites/1", nil)
	reqDel.Header.Set("Authorization", "Bearer "+token)
	recDel := httptest.NewRecorder()
	r.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusOK {
		t.Fatalf("delete site failed: code=%d body=%s", recDel.Code, recDel.Body.String())
	}
}

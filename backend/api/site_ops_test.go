package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/siteops"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

func loadSiteOpsFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "siteops", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func setupSiteOpsTestRouter(t *testing.T, authActive bool, siteCfg config.SiteOpsConfig) (*gin.Engine, *siteops.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "siteops-api.db")
	db, err := storage.Open(storage.DBConfig{Driver: storage.DBDriverSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	cipher, err := crypto.NewCipher("test-app-secret-for-siteops-api-tests")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc, _ := siteops.NewDefaultService(db, cipher, siteCfg, config.ProxyConfig{}, config.UpstreamConfig{}, authActive, nil)
	if err := svc.Start(); err != nil {
		t.Fatalf("start siteops: %v", err)
	}
	t.Cleanup(func() {
		svc.Stop()
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	r := gin.New()
	Register(r, &Deps{DB: db, Cipher: cipher, SiteOps: svc})
	return r, svc
}

func TestSiteOpsAuthGateAndLocalEscapeHatch(t *testing.T) {
	t.Run("fail closed when auth disabled", func(t *testing.T) {
		r, _ := setupSiteOpsTestRouter(t, false, config.SiteOpsConfig{})
		req := httptest.NewRequest(http.MethodGet, "/api/site-ops/summary", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body=%s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != siteops.ErrCodeAuthRequired {
			t.Fatalf("code = %v, want %s", body["code"], siteops.ErrCodeAuthRequired)
		}
	})

	t.Run("local only allows loopback GET but blocks spoofed XFF and POST", func(t *testing.T) {
		r, _ := setupSiteOpsTestRouter(t, false, config.SiteOpsConfig{AllowUnauthenticatedLocalOnly: true})

		// Legit loopback GET passes.
		req := httptest.NewRequest(http.MethodGet, "/api/site-ops/summary", nil)
		req.RemoteAddr = "127.0.0.1:23456"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("loopback GET status = %d, want 200, body=%s", w.Code, w.Body.String())
		}

		// Non-loopback with spoofed X-Forwarded-For is rejected.
		spoof := httptest.NewRequest(http.MethodGet, "/api/site-ops/summary", nil)
		spoof.RemoteAddr = "192.168.1.50:23456"
		spoof.Header.Set("X-Forwarded-For", "127.0.0.1")
		spoof.Header.Set("X-Real-IP", "127.0.0.1")
		wSpoof := httptest.NewRecorder()
		r.ServeHTTP(wSpoof, spoof)
		if wSpoof.Code != http.StatusForbidden {
			t.Fatalf("spoofed XFF status = %d, want 403", wSpoof.Code)
		}

		// POST from loopback is rejected in local-only read mode.
		post := httptest.NewRequest(http.MethodPost, "/api/site-ops/instances", bytes.NewBufferString(`{"name":"x"}`))
		post.RemoteAddr = "127.0.0.1:23456"
		post.Header.Set("Content-Type", "application/json")
		wPost := httptest.NewRecorder()
		r.ServeHTTP(wPost, post)
		if wPost.Code != http.StatusForbidden {
			t.Fatalf("loopback POST in local-only mode status = %d, want 403", wPost.Code)
		}
	})
}

func TestSiteOpsEndToEndCRUDProbeRefreshOverviewGroups(t *testing.T) {
	healthJSON := loadSiteOpsFixture(t, "health_ok.json")
	snapJSON := loadSiteOpsFixture(t, "ops_snapshot_v2.json")
	concJSON := loadSiteOpsFixture(t, "ops_concurrency.json")
	availJSON := loadSiteOpsFixture(t, "ops_account_availability.json")
	groupsJSON := loadSiteOpsFixture(t, "groups_all.json")
	verJSON := loadSiteOpsFixture(t, "system_version.json")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if r.Header.Get("x-api-key") != "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(healthJSON)
		case "/api/v1/admin/ops/dashboard/snapshot-v2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(snapJSON)
		case "/api/v1/admin/ops/concurrency":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(concJSON)
		case "/api/v1/admin/ops/account-availability":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(availJSON)
		case "/api/v1/admin/groups/all":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(groupsJSON)
		case "/api/v1/admin/system/version":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(verJSON)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	r, _ := setupSiteOpsTestRouter(t, true, config.SiteOpsConfig{
		AllowedOrigins: []string{upstream.URL},
	})

	// 1. Unapproved origin is rejected.
	badBody := `{"name":"bad","base_url":"https://portal.example.com","api_key":"adm_sc_secret_1234567890"}`
	reqBad := httptest.NewRequest(http.MethodPost, "/api/site-ops/instances", bytes.NewBufferString(badBody))
	reqBad.Header.Set("Content-Type", "application/json")
	wBad := httptest.NewRecorder()
	r.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("unapproved origin status = %d, want 400, body=%s", wBad.Code, wBad.Body.String())
	}

	// 2. Create approved instance.
	createPayload, _ := json.Marshal(siteops.CreateInstanceInput{
		Name:    "主站-测试",
		BaseURL: upstream.URL,
		APIKey:  "adm_sc_secret_1234567890",
		KeyKind: "scoped",
	})
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/site-ops/instances", bytes.NewReader(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	wCreate := httptest.NewRecorder()
	r.ServeHTTP(wCreate, reqCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201, body=%s", wCreate.Code, wCreate.Body.String())
	}
	var createResp struct {
		Data siteops.InstanceView `json:"data"`
	}
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if !createResp.Data.APIKeySet || createResp.Data.APIKeyHint == "" {
		t.Fatalf("expected sanitized hint, got %+v", createResp.Data)
	}

	// 3. Probe + Refresh + Overview + Trends + Groups.
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/site-ops/instances/1/probe"},
		{http.MethodPost, "/api/site-ops/instances/1/refresh"},
		{http.MethodGet, "/api/site-ops/instances/1/overview"},
		{http.MethodGet, "/api/site-ops/instances/1/trends?range=6h"},
		{http.MethodGet, "/api/site-ops/instances/1/groups"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// 4. Delete instance.
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/site-ops/instances/1", nil)
	wDel := httptest.NewRecorder()
	r.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", wDel.Code, wDel.Body.String())
	}
}

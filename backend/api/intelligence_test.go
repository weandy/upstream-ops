package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

type apiStubLLM struct{}

func (apiStubLLM) Complete(_ context.Context, req llmclient.Request) (*llmclient.Response, error) {
	content := "A gentle sunrise warms the calm blue ocean horizon."
	switch {
	case strings.Contains(req.Prompt, "糖果"):
		content = "21"
	case strings.Contains(req.Prompt, "鹈鹕"):
		content = "```svg\n<svg viewBox=\"0 0 100 100\" xmlns=\"http://www.w3.org/2000/svg\"><circle cx=\"50\" cy=\"50\" r=\"20\"/></svg>\n```"
	}
	return &llmclient.Response{
		Content:      content,
		Complete:     true,
		HTTPStatus:   200,
		FirstTokenMS: 420,
		LatencyMS:    1220,
		OutputTokens: 16,
		TPS:          20,
		ErrorClass:   llmclient.ErrorClassOK,
	}, nil
}

func (apiStubLLM) ListModels(_ context.Context, _ llmclient.Endpoint) ([]string, error) {
	return []string{"gpt-6.2-sol", "gpt-6.2-astra"}, nil
}

func TestIntelligenceTargetsHTTPAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "api_intel.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	cipher, _ := crypto.NewCipher("api-test-secret-32-bytes-minimum!")
	intelSvc := intelligence.NewService(db, cipher, nil, apiStubLLM{}, nil)

	r := gin.New()
	Register(r, &Deps{
		DB:           db,
		Cipher:       cipher,
		Intelligence: intelSvc,
	})

	// 1. 创建目标
	body := `{"name":"codex-plus","family":"codex","source_kind":"direct","base_url":"https://api.example.com","api_key":"sk-secret123456","protocol":"openai_chat","enabled":true,"embed_visible":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/intelligence/targets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST target status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data storage.ProbeTarget `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.ID == 0 || created.Data.Slug != "codex-plus" {
		t.Fatalf("unexpected created target: %+v", created.Data)
	}

	// 2. 拉取模型列表
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence/targets/1/models", nil))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("gpt-6.2-sol")) {
		t.Fatalf("GET models status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. 手动执行主动测活
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/intelligence/targets/1/liveness", bytes.NewBufferString(`{"model_name":"gpt-6.2-sol"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST liveness status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 4. 读取单目标总览
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence/targets/1/overview", nil))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"sample_count":1`)) {
		t.Fatalf("GET overview status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. 删除目标
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/intelligence/targets/1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE target status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntelligencePluginWorkflowAndArtifactSandboxAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "api_plugins.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	cipher, _ := crypto.NewCipher("api-plugin-secret-32-bytes!!!!!!!")
	intelSvc := intelligence.NewService(db, cipher, nil, apiStubLLM{}, nil)

	r := gin.New()
	Register(r, &Deps{DB: db, Cipher: cipher, Intelligence: intelSvc})

	// 1. 拉取已注册插件清单
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence/detectors", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"candy"`) || !strings.Contains(rec.Body.String(), `"pelican"`) {
		t.Fatalf("GET detectors status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. 创建目标
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/intelligence/targets", bytes.NewBufferString(`{"name":"demo","source_kind":"direct","base_url":"https://api.example.com","api_key":"sk-12345678","protocol":"openai_chat","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create target status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. 保存插件开关矩阵（勾选 candy 和 pelican）
	cfgBody := `{"items":[{"detector_key":"candy","enabled":true,"model_name":"gpt-6.2-sol","interval_seconds":3600,"embed_visible":true},{"detector_key":"pelican","enabled":true,"model_name":"gpt-6.2-sol","interval_seconds":3600,"params_json":"{\"variant\":\"original\"}","embed_visible":true}]}`
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/intelligence/targets/1/detectors", bytes.NewBufferString(cfgBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT detectors status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 4. 手动执行全部已启用插件
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/intelligence/targets/1/run", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"candy"`) || !strings.Contains(rec.Body.String(), `"pelican"`) {
		t.Fatalf("POST run all status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. 查询 pelican 历史列表（应有 run_uuid，且不含大字段 artifact_svg）
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence/targets/1/runs?detector=pelican&limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET runs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var runsResp struct {
		Data []storage.ProbeIntelligenceRun `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runsResp); err != nil || len(runsResp.Data) != 1 {
		t.Fatalf("decode runs failed: len=%d err=%v body=%s", len(runsResp.Data), err, rec.Body.String())
	}
	if runsResp.Data[0].ArtifactSVG != "" {
		t.Fatal("runs list should omit artifact_svg")
	}

	// 6. 访问单张 Pelican 隔离沙箱预览接口，必须带严格 sandbox CSP！
	runUUID := runsResp.Data[0].RunUUID
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence/runs/"+runUUID+"/artifact", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET artifact status=%d body=%s", rec.Code, rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "sandbox") {
		t.Fatalf("expected strict sandbox CSP, got %q", csp)
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Fatalf("expected svg html body, got %s", rec.Body.String())
	}

	// 7. 读取 Overview 与批量 Overviews，均应包含 latest_runs（candy 与 pelican）与 candy_24h 计数。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence/targets/1/overview", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"latest_runs"`) || !strings.Contains(rec.Body.String(), `"candy_passed_24h":1`) {
		t.Fatalf("GET overview missing latest_runs/candy_24h: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/intelligence/overviews", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"candy_passed_24h":1`) {
		t.Fatalf("GET overviews failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

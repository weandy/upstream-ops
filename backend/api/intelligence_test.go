package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

type apiStubLLM struct{}

func (apiStubLLM) Complete(_ context.Context, _ llmclient.Request) (*llmclient.Response, error) {
	return &llmclient.Response{
		Content:      "A gentle sunrise warms the calm blue ocean horizon.",
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

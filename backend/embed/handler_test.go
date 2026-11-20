package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestEmbedSSROverviewSubpagesXSSAndZeroScript(t *testing.T) {
	svc, intel := openTestEmbedEnv(t)
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	svc.NowFunc = func() time.Time { return now }

	xssPayload := `<script>alert("xss")</script><img src=x onerror=alert(1)>`
	target, err := intel.CreateTarget(intelligence.TargetInput{
		Name:         "codex-" + xssPayload,
		Slug:         "codex-xss",
		Family:       storage.ProbeFamilyCodex,
		SourceKind:   storage.ProbeSourceDirect,
		BaseURL:      "https://secret-upstream-host.local",
		APIKey:       "sk-never-leak-this-key-123456",
		Protocol:     storage.ProbeProtocolOpenAIChat,
		Enabled:      true,
		EmbedVisible: true,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := intel.SaveTargetDetectors(target.ID, []intelligence.DetectorConfigInput{
		{DetectorKey: intelligence.DetectorKeyLiveness, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 180, EmbedVisible: true},
		{DetectorKey: intelligence.DetectorKeyCandy, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 3600, EmbedVisible: true},
		{DetectorKey: intelligence.DetectorKeyLMFPD, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 10800, EmbedVisible: true},
		{DetectorKey: intelligence.DetectorKeyPelican, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 3600, EmbedVisible: true},
	}); err != nil {
		t.Fatalf("save detectors: %v", err)
	}

	_ = intel.Liveness.Append(&storage.ProbeLivenessSample{
		TargetID:     target.ID,
		ModelName:    "gpt-6.2-sol",
		Success:      true,
		HTTPStatus:   200,
		FirstTokenMS: 510,
		LatencyMS:    1510,
		OutputTokens: 48,
		TPS:          48.0,
		ObservedAt:   now.Add(-time.Minute),
	})
	_ = intel.Runs.Append(&storage.ProbeIntelligenceRun{
		TargetID:    target.ID,
		DetectorKey: intelligence.DetectorKeyCandy,
		ModelName:   "gpt-6.2-sol",
		Status:      storage.IntelligenceStatusSuspected,
		Summary:     "未通过：回答包含注入 " + xssPayload,
		DetailJSON:  `{"answer":"` + strings.ReplaceAll(xssPayload, `"`, `\"`) + `","passed":false}`,
		ObservedAt:  now.Add(-2 * time.Minute),
	})
	_ = intel.Runs.Append(&storage.ProbeIntelligenceRun{
		TargetID:     target.ID,
		DetectorKey:  intelligence.DetectorKeyLMFPD,
		ModelName:    "gpt-6.2-sol",
		Status:       storage.IntelligenceStatusNormal,
		Summary:      "最像 gpt-6.2-sol（排第 1）",
		ClaimedRank:  1,
		TopCandidate: "gpt-6.2-sol",
		DetailJSON:   `{"claimed_model":"gpt-6.2-sol","claimed_in_bank":true,"decision":"model","probability":0.89,"top_candidates":[{"id":"gpt-6.2-sol","display_name":"gpt-6.2-sol","probability":0.89}]}`,
		ObservedAt:   now.Add(-3 * time.Minute),
	})
	pelicanRun := &storage.ProbeIntelligenceRun{
		TargetID:    target.ID,
		DetectorKey: intelligence.DetectorKeyPelican,
		ModelName:   "gpt-6.2-sol",
		Status:      storage.IntelligenceStatusNormal,
		Summary:     "已生成画面",
		ArtifactSVG: `<!doctype html><html><body><svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="20"/></svg></body></html>`,
		ObservedAt:  now.Add(-4 * time.Minute),
	}
	_ = intel.Runs.Append(pelicanRun)

	_, err = svc.CreateSite(SiteInput{
		Slug:               "ooioo",
		Title:              "渠道性能 " + xssPayload,
		Eyebrow:            "CHANNEL PERFORMANCE",
		Enabled:            true,
		RefreshSeconds:     15,
		AllowedOrigins:     []string{"https://ooioo.work"},
		CORSOrigins:        []string{"https://ooioo.work"},
		Theme:              "paper",
		ShowIdentityColumn: true,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}

	router := gin.New()
	svc.RegisterPublic(router)

	pages := []string{
		"/embed?site=ooioo",
		"/embed/candy?site=ooioo&target=codex-xss",
		"/embed/fingerprint?site=ooioo&target=codex-xss",
		"/embed/pelican?site=ooioo&target=codex-xss",
	}
	for _, u := range pages {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", u, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		lower := strings.ToLower(body)
		if strings.Contains(lower, "<script") {
			t.Fatalf("GET %s must contain zero <script> tags, body snippet found script", u)
		}
		if strings.Contains(body, xssPayload) {
			t.Fatalf("GET %s failed to escape XSS payload", u)
		}
		if strings.Contains(body, "sk-never-leak") || strings.Contains(body, "secret-upstream-host.local") {
			t.Fatalf("GET %s leaked internal secret or upstream URL", u)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'self' https://ooioo.work") {
			t.Fatalf("GET %s unexpected CSP: %s", u, csp)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s expected Cache-Control: no-store, got %q", u, rec.Header().Get("Cache-Control"))
		}
	}

	// 验证主表自动刷新 meta 与双沙箱缩略图 iframe。
	recOverview := httptest.NewRecorder()
	router.ServeHTTP(recOverview, httptest.NewRequest(http.MethodGet, "/embed?site=ooioo", nil))
	ovBody := recOverview.Body.String()
	if !strings.Contains(ovBody, `<meta http-equiv="refresh" content="15">`) {
		t.Fatalf("overview missing meta refresh")
	}
	if !strings.Contains(ovBody, `<iframe sandbox=""`) || !strings.Contains(ovBody, pelicanRun.RunUUID) {
		t.Fatalf("overview missing sandboxed pelican thumbnail iframe")
	}

	// 验证单张 Pelican 隔离端点强沙箱 CSP。
	recPelican := httptest.NewRecorder()
	router.ServeHTTP(recPelican, httptest.NewRequest(http.MethodGet, "/embed/pelican/"+pelicanRun.RunUUID+"?site=ooioo", nil))
	if recPelican.Code != http.StatusOK {
		t.Fatalf("pelican artifact status=%d", recPelican.Code)
	}
	if recPelican.Header().Get("Content-Security-Policy") != PelicanSandboxCSP {
		t.Fatalf("unexpected pelican CSP: %q", recPelican.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(recPelican.Body.String(), "<svg") {
		t.Fatalf("pelican body missing svg")
	}

	// 验证公开 JSON API + CORS 白名单 + 零敏感信息泄漏。
	reqAllowed := httptest.NewRequest(http.MethodGet, "/api/public/status?site=ooioo", nil)
	reqAllowed.Header.Set("Origin", "https://ooioo.work")
	recJSON := httptest.NewRecorder()
	router.ServeHTTP(recJSON, reqAllowed)
	if recJSON.Code != http.StatusOK {
		t.Fatalf("public status code=%d", recJSON.Code)
	}
	if recJSON.Header().Get("Access-Control-Allow-Origin") != "https://ooioo.work" {
		t.Fatalf("expected CORS allow origin https://ooioo.work, got %q", recJSON.Header().Get("Access-Control-Allow-Origin"))
	}
	rawJSON := recJSON.Body.String()
	if strings.Contains(rawJSON, "sk-never-leak") || strings.Contains(rawJSON, "secret-upstream-host.local") {
		t.Fatalf("public status JSON leaked internal fields: %s", rawJSON)
	}
	var parsed struct {
		Data SiteSnapshot `json:"data"`
	}
	if err := json.Unmarshal(recJSON.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode public status: %v", err)
	}
	if len(parsed.Data.Targets) != 1 || parsed.Data.Targets[0].Slug != "codex-xss" {
		t.Fatalf("unexpected public targets: %+v", parsed.Data.Targets)
	}

	// 非白名单 Origin 不应收到 Access-Control-Allow-Origin。
	reqDenied := httptest.NewRequest(http.MethodGet, "/api/public/status?site=ooioo", nil)
	reqDenied.Header.Set("Origin", "https://evil.example")
	recDenied := httptest.NewRecorder()
	router.ServeHTTP(recDenied, reqDenied)
	if recDenied.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected CORS header for disallowed origin: %q", recDenied.Header().Get("Access-Control-Allow-Origin"))
	}

	// OPTIONS 预检返回 204 并携带允许头。
	reqOpt := httptest.NewRequest(http.MethodOptions, "/api/public/status?site=ooioo", nil)
	reqOpt.Header.Set("Origin", "https://ooioo.work")
	recOpt := httptest.NewRecorder()
	router.ServeHTTP(recOpt, reqOpt)
	if recOpt.Code != http.StatusNoContent || recOpt.Header().Get("Access-Control-Allow-Origin") != "https://ooioo.work" {
		t.Fatalf("unexpected OPTIONS response: code=%d cors=%q", recOpt.Code, recOpt.Header().Get("Access-Control-Allow-Origin"))
	}
}

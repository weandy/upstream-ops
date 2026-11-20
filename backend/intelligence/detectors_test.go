package intelligence

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

func TestCandyAnswerGrading(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   bool
	}{
		{"exact_21", "21", true},
		{"markdown_bold_21", "**21**", true},
		{"sentence_end_21", "最少需要取出 21 个糖果。", true},
		{"multiline_conclusion_21", "分析最坏情况...\n最终答案：21", true},
		{"reject_121", "121", false},
		{"reject_210", "210", false},
		{"reject_decimal_21_0", "21.0", false},
		{"reject_decimal_21_5", "答案是 21.5", false},
		{"reject_ambiguous_or", "21 或 22", false},
		{"reject_ambiguous_slash", "21/22", false},
		{"reject_wrong_29", "29", false},
		{"reject_empty", "   ", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GradeCandyAnswer(tc.answer)
			if got != tc.want {
				t.Fatalf("GradeCandyAnswer(%q) = %v, want %v", tc.answer, got, tc.want)
			}
		})
	}
}

func TestCandyDetectorRun(t *testing.T) {
	d := NewCandyDetector()
	if d.Key() != DetectorKeyCandy {
		t.Fatalf("unexpected key: %s", d.Key())
	}

	// 1. 正常回答 21 -> normal
	stubOK := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "21",
			Reasoning:    "worst case 15 + 6 = 21",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 1200,
			LatencyMS:    2500,
			OutputTokens: 10,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	res, err := d.Run(context.Background(), stubOK, DetectorRunRequest{
		TargetID: 1,
		Request:  llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: "gpt-6.2-sol"},
	})
	if err != nil {
		t.Fatalf("candy run ok failed: %v", err)
	}
	if res.Status != storage.IntelligenceStatusNormal {
		t.Fatalf("expected normal, got %+v", res)
	}

	// 2. 思考链里出现 21，但最终回答是 29 -> suspected
	stubWrong := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "29",
			Reasoning:    "maybe 21? no 29",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 800,
			LatencyMS:    1800,
			OutputTokens: 8,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	resWrong, err := d.Run(context.Background(), stubWrong, DetectorRunRequest{
		TargetID: 1,
		Request:  llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: "gpt-6.2-sol"},
	})
	if err != nil {
		t.Fatalf("candy run wrong failed: %v", err)
	}
	if resWrong.Status != storage.IntelligenceStatusSuspected {
		t.Fatalf("expected suspected for wrong answer, got %+v", resWrong)
	}

	// 3. 流式截断 -> error（不判 normal）
	stubTrunc := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "21",
			Complete:     false,
			HTTPStatus:   200,
			ErrorClass:   llmclient.ErrorClassTruncated,
			ErrorMessage: "stream ended early",
		},
	}
	resTrunc, err := d.Run(context.Background(), stubTrunc, DetectorRunRequest{
		TargetID: 1,
		Request:  llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: "gpt-6.2-sol"},
	})
	if err != nil {
		t.Fatalf("candy run trunc err: %v", err)
	}
	if resTrunc.Status != storage.IntelligenceStatusError {
		t.Fatalf("expected error for truncated stream, got %+v", resTrunc)
	}
}

func TestPelicanDetectorExtractionAndValidation(t *testing.T) {
	d := NewPelicanDetector()
	if err := d.ValidateParams(`{"variant":"original"}`); err != nil {
		t.Fatalf("valid variant failed: %v", err)
	}
	if err := d.ValidateParams(`{"variant":"invalid-x"}`); err == nil {
		t.Fatal("expected invalid variant error")
	}

	// 1. 提取 fenced svg 并自动包裹安全自适应 HTML 文档
	stubSVG := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "Here is your drawing:\n```svg\n<svg viewBox=\"0 0 800 480\" xmlns=\"http://www.w3.org/2000/svg\"><circle cx=\"50\" cy=\"50\" r=\"40\"/></svg>\n```",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 1000,
			LatencyMS:    4000,
			OutputTokens: 200,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	res, err := d.Run(context.Background(), stubSVG, DetectorRunRequest{
		TargetID:   1,
		ParamsJSON: `{"variant":"original"}`,
		Request:    llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: "gpt-6.2-sol"},
	})
	if err != nil {
		t.Fatalf("pelican run failed: %v", err)
	}
	if res.Status != storage.IntelligenceStatusNormal {
		t.Fatalf("expected normal, got %+v", res)
	}
	if len(stubSVG.reqs) != 1 || stubSVG.reqs[0].MaxTokens != 4096 {
		t.Fatalf("expected default pelican max_tokens=4096 for upstream compatibility, got %+v", stubSVG.reqs)
	}
	if !strings.Contains(res.ArtifactSVG, "<svg") || !strings.Contains(strings.ToLower(res.ArtifactSVG), "<!doctype html>") {
		t.Fatalf("expected wrapped HTML containing svg, got %q", res.ArtifactSVG)
	}

	// 2. 无 SVG 内容 -> suspected
	stubBad := &stubLLMClient{
		resp: &llmclient.Response{
			Content:    "Sorry, I cannot draw right now.",
			Complete:   true,
			HTTPStatus: 200,
			ErrorClass: llmclient.ErrorClassOK,
		},
	}
	resBad, err := d.Run(context.Background(), stubBad, DetectorRunRequest{
		TargetID: 1,
		Request:  llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: "gpt-6.2-sol"},
	})
	if err != nil {
		t.Fatalf("pelican bad run err: %v", err)
	}
	if resBad.Status != storage.IntelligenceStatusSuspected {
		t.Fatalf("expected suspected when svg missing, got %+v", resBad)
	}
}

func TestCustomPromptDetectorModes(t *testing.T) {
	d := NewCustomPromptDetector()
	if err := d.ValidateParams(`{"prompt":"1+1=?","match_mode":"exact_number","expected":"2"}`); err != nil {
		t.Fatalf("valid custom params failed: %v", err)
	}
	if err := d.ValidateParams(`{"prompt":"","match_mode":"contains","expected":"ok"}`); err == nil {
		t.Fatal("expected empty prompt validation error")
	}
	if err := d.ValidateParams(`{"prompt":"hi","match_mode":"regex","expected":"[invalid"}`); err == nil {
		t.Fatal("expected invalid regex validation error")
	}

	run := func(params, answer string) string {
		res, err := d.Run(context.Background(), &stubLLMClient{
			resp: &llmclient.Response{
				Content:    answer,
				Complete:   true,
				HTTPStatus: 200,
				ErrorClass: llmclient.ErrorClassOK,
			},
		}, DetectorRunRequest{
			TargetID:   1,
			ParamsJSON: params,
			Request:    llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: "gpt-6.2-sol"},
		})
		if err != nil {
			t.Fatalf("custom prompt run err: %v", err)
		}
		return res.Status
	}

	if got := run(`{"prompt":"2+2","match_mode":"exact_number","expected":"4"}`, "答案是 4"); got != storage.IntelligenceStatusNormal {
		t.Fatalf("exact_number expected normal, got %s", got)
	}
	if got := run(`{"prompt":"2+2","match_mode":"exact_number","expected":"4"}`, "14"); got != storage.IntelligenceStatusSuspected {
		t.Fatalf("exact_number should reject 14, got %s", got)
	}
	if got := run(`{"prompt":"say hello","match_mode":"contains","expected":"hello","forbid":"error"}`, "hello world"); got != storage.IntelligenceStatusNormal {
		t.Fatalf("contains expected normal, got %s", got)
	}
	if got := run(`{"prompt":"say hello","match_mode":"contains","expected":"hello","forbid":"error"}`, "hello error"); got != storage.IntelligenceStatusSuspected {
		t.Fatalf("forbid should reject, got %s", got)
	}
	if got := run(`{"prompt":"code","match_mode":"regex","expected":"^OK-[0-9]{3}$"}`, "OK-402"); got != storage.IntelligenceStatusNormal {
		t.Fatalf("regex expected normal, got %s", got)
	}
}

func TestLiveIntelligenceDetectors(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("LIVE_TEST_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("LIVE_TEST_API_KEY"))
	model := strings.TrimSpace(os.Getenv("LIVE_TEST_MODEL"))
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("live test env vars not set")
	}

	client := llmclient.New()
	baseReq := llmclient.Request{
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Protocol: storage.ProbeProtocolOpenAIChat,
		Model:    model,
		Timeout:  90 * time.Second,
	}

	// 1. Live Candy
	candyRes, err := NewCandyDetector().Run(context.Background(), client, DetectorRunRequest{
		TargetID: 1,
		Request:  baseReq,
	})
	if err != nil {
		t.Fatalf("live candy err: %v", err)
	}
	t.Logf("live candy: status=%s summary=%q latency=%dms tokens=%d", candyRes.Status, candyRes.Summary, candyRes.LatencyMS, candyRes.OutputTokens)
	if candyRes.Status == storage.IntelligenceStatusError {
		t.Fatalf("live candy errored: %s", candyRes.ErrorMessage)
	}

	// 2. Live CustomPrompt
	customRes, err := NewCustomPromptDetector().Run(context.Background(), client, DetectorRunRequest{
		TargetID:   1,
		ParamsJSON: `{"prompt":"Compute 13 * 7. Reply with only the integer.","match_mode":"exact_number","expected":"91"}`,
		Request:    baseReq,
	})
	if err != nil {
		t.Fatalf("live custom prompt err: %v", err)
	}
	t.Logf("live custom: status=%s summary=%q latency=%dms", customRes.Status, customRes.Summary, customRes.LatencyMS)
	if customRes.Status != storage.IntelligenceStatusNormal {
		t.Fatalf("expected live custom prompt normal, got %+v", customRes)
	}

	// 3. Live Pelican
	pelicanRes, err := NewPelicanDetector().Run(context.Background(), client, DetectorRunRequest{
		TargetID:   1,
		ParamsJSON: `{"variant":"original"}`,
		Request:    baseReq,
	})
	if err != nil {
		t.Fatalf("live pelican err: %v", err)
	}
	t.Logf("live pelican: status=%s summary=%q svg_bytes=%d latency=%dms tokens=%d",
		pelicanRes.Status, pelicanRes.Summary, len(pelicanRes.ArtifactSVG), pelicanRes.LatencyMS, pelicanRes.OutputTokens)
	if pelicanRes.Status != storage.IntelligenceStatusNormal || !strings.Contains(pelicanRes.ArtifactSVG, "<svg") {
		t.Fatalf("expected live pelican normal with svg, got status=%s err=%s", pelicanRes.Status, pelicanRes.ErrorMessage)
	}
}

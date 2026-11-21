package llmclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestOpenAIChatStreamSuccessAndReasoningStripping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("unexpected auth header: %s", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["stream"] != true {
			t.Errorf("expected stream=true, got %v", body["stream"])
		}
		if body["reasoning_effort"] != "high" {
			t.Errorf("expected reasoning_effort=high, got %v", body["reasoning_effort"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		chunks := []string{
			`data: {"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"let me think: 20+1"},"finish_reason":null}]}`,
			`data: {"choices":[{"index":0,"delta":{"content":"21"},"finish_reason":null}]}`,
			`data: {"choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":20}}`,
			`data: [DONE]`,
		}
		for i, c := range chunks {
			if i == 1 {
				time.Sleep(15 * time.Millisecond)
			}
			if i == 2 {
				time.Sleep(20 * time.Millisecond)
			}
			_, _ = fmt.Fprintf(w, "%s\n\n", c)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := New()
	resp, err := c.Complete(context.Background(), Request{
		BaseURL:         srv.URL,
		APIKey:          "sk-test",
		Protocol:        storage.ProbeProtocolOpenAIChat,
		Model:           "gpt-6.2-sol",
		Prompt:          "answer 21",
		ReasoningEffort: "high",
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if !resp.Complete {
		t.Fatalf("expected Complete=true, got false (errClass=%s)", resp.ErrorClass)
	}
	if strings.TrimSpace(resp.Content) != "21" {
		t.Fatalf("expected content '21', got %q", resp.Content)
	}
	if !strings.Contains(resp.Reasoning, "20+1") {
		t.Fatalf("expected reasoning preserved separately, got %q", resp.Reasoning)
	}
	if resp.InputTokens != 12 || resp.OutputTokens != 20 {
		t.Fatalf("expected tokens 12/20, got %d/%d", resp.InputTokens, resp.OutputTokens)
	}
	if resp.FirstTokenMS <= 0 || resp.LatencyMS < resp.FirstTokenMS {
		t.Fatalf("invalid timings: ttft=%d latency=%d", resp.FirstTokenMS, resp.LatencyMS)
	}
	if resp.TPS <= 0 {
		t.Fatalf("expected TPS > 0, got %v", resp.TPS)
	}
}

func TestOpenAIChatStripInlineThinkTagAndNonStreamFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"index":0,"message":{"role":"assistant","content":"<think>intermediate 99</think>\n21"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":15}
		}`))
	}))
	defer srv.Close()

	resp, err := New().Complete(context.Background(), Request{
		BaseURL:  srv.URL,
		APIKey:   "sk-test",
		Protocol: storage.ProbeProtocolOpenAIChat,
		Model:    "deepseek-r1",
		Prompt:   "test",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Complete non-stream fallback failed: %v", err)
	}
	if !resp.Complete || strings.TrimSpace(resp.Content) != "21" {
		t.Fatalf("expected stripped content '21' and Complete=true, got content=%q complete=%v", resp.Content, resp.Complete)
	}
	if !strings.Contains(resp.Reasoning, "intermediate 99") {
		t.Fatalf("expected think block captured in reasoning, got %q", resp.Reasoning)
	}
}

func TestOpenAIResponsesStreamNoDuplicateOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["input"] == nil {
			t.Errorf("expected input field in responses body")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		events := []string{
			"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"status\":\"in_progress\"}}",
			"event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking...\"}",
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"2\"}",
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"1\"}",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"21\"}]}],\"usage\":{\"input_tokens\":18,\"output_tokens\":25}}}",
		}
		for _, e := range events {
			_, _ = fmt.Fprintf(w, "%s\n\n", e)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	resp, err := New().Complete(context.Background(), Request{
		BaseURL:         srv.URL,
		APIKey:          "sk-test",
		Protocol:        storage.ProbeProtocolOpenAIResponses,
		Model:           "gpt-6.2-sol",
		Prompt:          "answer 21",
		ReasoningEffort: "medium",
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Responses Complete failed: %v", err)
	}
	if !resp.Complete {
		t.Fatalf("expected Complete=true, got false")
	}
	// 严禁把 delta("21") 和 completed 里的 output("21") 重复拼成 "2121"。
	if resp.Content != "21" {
		t.Fatalf("expected exact content '21', got %q", resp.Content)
	}
	if resp.InputTokens != 18 || resp.OutputTokens != 25 {
		t.Fatalf("expected tokens 18/25, got %d/%d", resp.InputTokens, resp.OutputTokens)
	}
}

func TestAnthropicStreamTextAndThinkingSeparation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant" {
			t.Errorf("expected x-api-key header, got %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("expected anthropic-version header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":30,"output_tokens":1}}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hidden 42"}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"OK"}}`,
			`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`,
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		}
		for _, e := range events {
			_, _ = fmt.Fprintf(w, "%s\n\n", e)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	resp, err := New().Complete(context.Background(), Request{
		BaseURL:   srv.URL,
		APIKey:    "sk-ant",
		Protocol:  storage.ProbeProtocolAnthropic,
		AuthStyle: storage.GatewayProviderAuthXAPIKey,
		Model:     "claude-opus-5-5",
		Prompt:    "say OK",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Anthropic Complete failed: %v", err)
	}
	if !resp.Complete || resp.Content != "OK" {
		t.Fatalf("expected Complete=true content='OK', got complete=%v content=%q", resp.Complete, resp.Content)
	}
	if resp.InputTokens != 30 || resp.OutputTokens != 12 {
		t.Fatalf("expected tokens 30/12, got %d/%d", resp.InputTokens, resp.OutputTokens)
	}
}

func TestStreamTruncationAndHTTPErrorClassification(t *testing.T) {
	t.Run("truncated stream", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			// 缺失 [DONE] 且 finish_reason 为空。
			_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"))
		}))
		defer srv.Close()
		resp, err := New().Complete(context.Background(), Request{
			BaseURL:  srv.URL,
			APIKey:   "sk-test",
			Protocol: storage.ProbeProtocolOpenAIChat,
			Model:    "gpt-6.2-sol",
			Prompt:   "hi",
			Timeout:  5 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected transport err: %v", err)
		}
		if resp.Complete {
			t.Fatal("expected Complete=false for truncated SSE stream")
		}
		if resp.ErrorClass != ErrorClassTruncated {
			t.Fatalf("expected ErrorClass=%s, got %s", ErrorClassTruncated, resp.ErrorClass)
		}
	})

	cases := []struct {
		status int
		want   string
	}{
		{401, ErrorClassAuth},
		{403, ErrorClassAuth},
		{429, ErrorClassRateLimit},
		{400, ErrorClassConfig},
		{404, ErrorClassConfig},
		{502, ErrorClassUpstream},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("http_%d", tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.status == 502 {
					_, _ = w.Write([]byte(`{"title":"Error 502: Bad gateway","detail":"origin overloaded"}`))
					return
				}
				_, _ = w.Write([]byte(`{"error":{"message":"upstream error"}}`))
			}))
			defer srv.Close()

			resp, err := New().Complete(context.Background(), Request{
				BaseURL:  srv.URL,
				APIKey:   "sk-test",
				Protocol: storage.ProbeProtocolOpenAIChat,
				Model:    "gpt-6.2-sol",
				Prompt:   "hi",
				Timeout:  5 * time.Second,
			})
			if err == nil {
				t.Fatalf("expected error for HTTP %d", tc.status)
			}
			if resp == nil || resp.HTTPStatus != tc.status || resp.ErrorClass != tc.want {
				t.Fatalf("unexpected error classification: resp=%+v want=%s", resp, tc.want)
			}
		})
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6.2-sol"},{"id":"gpt-6.2-astra"}]}`))
	}))
	defer srv.Close()

	models, err := New().ListModels(context.Background(), Endpoint{
		BaseURL: srv.URL,
		APIKey:  "sk-test",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-6.2-sol" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestRetryAfterHeaderParsing(t *testing.T) {
	t.Run("integer_seconds", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "45")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
		}))
		defer srv.Close()

		resp, err := New().Complete(context.Background(), Request{
			BaseURL:  srv.URL,
			APIKey:   "sk-test",
			Protocol: storage.ProbeProtocolOpenAIChat,
			Model:    "gpt-6.2-sol",
			Prompt:   "hi",
			Timeout:  5 * time.Second,
		})
		if err == nil {
			t.Fatal("expected error for HTTP 429")
		}
		if resp == nil || resp.ErrorClass != ErrorClassRateLimit || resp.RetryAfterSec != 45 {
			t.Fatalf("expected rate_limit with RetryAfterSec=45, got %+v", resp)
		}
	})

	t.Run("http_date_and_cap", func(t *testing.T) {
		future := time.Now().UTC().Add(120 * time.Second).Format(http.TimeFormat)
		srvDate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", future)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
		}))
		defer srvDate.Close()

		resp, _ := New().Complete(context.Background(), Request{
			BaseURL:  srvDate.URL,
			APIKey:   "sk-test",
			Protocol: storage.ProbeProtocolOpenAIChat,
			Model:    "gpt-6.2-sol",
			Prompt:   "hi",
			Timeout:  5 * time.Second,
		})
		if resp == nil || resp.RetryAfterSec < 110 || resp.RetryAfterSec > 125 {
			t.Fatalf("expected HTTP-date RetryAfterSec in [110,125], got %+v", resp)
		}

		srvCap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "999999")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"way too long"}}`))
		}))
		defer srvCap.Close()

		respCap, _ := New().Complete(context.Background(), Request{
			BaseURL:  srvCap.URL,
			APIKey:   "sk-test",
			Protocol: storage.ProbeProtocolOpenAIChat,
			Model:    "gpt-6.2-sol",
			Prompt:   "hi",
			Timeout:  5 * time.Second,
		})
		if respCap == nil || respCap.RetryAfterSec != 86400 {
			t.Fatalf("expected capped RetryAfterSec=86400, got %+v", respCap)
		}
	})
}

// TestLiveEndpoint 仅在显式提供环境变量时运行，避免离线 CI 失败及密钥硬编码。
func TestLiveEndpoint(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("LIVE_TEST_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("LIVE_TEST_API_KEY"))
	model := strings.TrimSpace(os.Getenv("LIVE_TEST_MODEL"))
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("LIVE_TEST_BASE_URL / LIVE_TEST_API_KEY / LIVE_TEST_MODEL not set")
	}

	c := New()
	models, err := c.ListModels(context.Background(), Endpoint{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("live ListModels failed: %v", err)
	}
	t.Logf("live models count=%d sample=%v", len(models), models)

	for _, proto := range []string{storage.ProbeProtocolOpenAIChat, storage.ProbeProtocolOpenAIResponses} {
		resp, err := c.Complete(context.Background(), Request{
			BaseURL:   baseURL,
			APIKey:    apiKey,
			Protocol:  proto,
			Model:     model,
			Prompt:    "Reply with exactly: OK",
			MaxTokens: 32,
			Timeout:   30 * time.Second,
		})
		if err != nil {
			t.Fatalf("live Complete (%s) failed: %v (resp=%+v)", proto, err, resp)
		}
		if !resp.Complete || strings.TrimSpace(resp.Content) == "" {
			t.Fatalf("live Complete (%s) incomplete: %+v", proto, resp)
		}
		t.Logf("live [%s] content=%q ttft=%dms latency=%dms out_tokens=%d tps=%.1f",
			proto, resp.Content, resp.FirstTokenMS, resp.LatencyMS, resp.OutputTokens, resp.TPS)
	}
}

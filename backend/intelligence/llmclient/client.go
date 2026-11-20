// Package llmclient 提供面向外挂主动测活与模型智力监测的独立三协议流式客户端。
// 支持 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 三种上游协议。
package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	// MaxCaptureBytes 单次响应最大读取字节数（2 MiB），防止上游异常流导致内存暴涨。
	MaxCaptureBytes = 2 << 20
	// DefaultTimeout 默认单次请求超时时间。
	DefaultTimeout = 60 * time.Second
	// DefaultUserAgent 缺省 User-Agent。
	DefaultUserAgent = "upstream-ops-probe/1.0"
	// DefaultAnthropicVersion Anthropic 协议默认版本头。
	DefaultAnthropicVersion = "2023-06-01"
)

// 错误分类常量，供测活统计与告警策略消费。
const (
	ErrorClassOK        = "ok"
	ErrorClassTimeout   = "timeout"
	ErrorClassRateLimit = "rate_limit"
	ErrorClassAuth      = "auth"
	ErrorClassConfig    = "config"
	ErrorClassUpstream  = "upstream"
	ErrorClassNetwork   = "network"
	ErrorClassTruncated = "truncated"
)

// Endpoint 通用上游连接信息（用于拉取模型等非推理调用）。
type Endpoint struct {
	BaseURL      string
	APIKey       string
	AuthStyle    string
	UserAgent    string
	ExtraHeaders map[string]string
	ProxyURL     string
	Timeout      time.Duration
}

// Request 一次流式模型探测请求参数。
type Request struct {
	BaseURL         string
	APIKey          string
	Protocol        string
	AuthStyle       string
	Model           string
	SystemPrompt    string
	Prompt          string
	ReasoningEffort string
	MaxTokens       int
	Temperature     *float64
	UserAgent       string
	ExtraHeaders    map[string]string
	ProxyURL        string
	Timeout         time.Duration
}

// Response 一次探测的结构化结果（含延迟、首字、速率与思考分离文本）。
type Response struct {
	Content        string  `json:"content"`
	Reasoning      string  `json:"reasoning,omitempty"`
	Complete       bool    `json:"complete"`
	FinishReason   string  `json:"finish_reason,omitempty"`
	HTTPStatus     int     `json:"http_status"`
	FirstTokenMS   int64   `json:"first_token_ms"`
	LatencyMS      int64   `json:"latency_ms"`
	InputTokens    int     `json:"input_tokens"`
	OutputTokens   int     `json:"output_tokens"`
	UsageEstimated bool    `json:"usage_estimated,omitempty"`
	TPS            float64 `json:"tps"`
	ErrorClass     string  `json:"error_class"`
	ErrorMessage   string  `json:"error_message,omitempty"`
}

// Client 三协议流式客户端。
type Client struct {
	baseTransport *http.Transport
}

// New 创建客户端实例。
func New() *Client {
	return &Client{
		baseTransport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          64,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: 2 * time.Second,
		},
	}
}

func (c *Client) httpClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	tr := c.baseTransport.Clone()
	if raw := strings.TrimSpace(proxyURL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}, nil
}

// ListModels 调用 GET /v1/models 拉取上游可用模型 ID 列表。
func (c *Client) ListModels(ctx context.Context, ep Endpoint) ([]string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(ep.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("base_url is required")
	}
	targetURL := joinEndpointPath(baseURL, "/v1/models")
	timeout := ep.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	hc, err := c.httpClient(ep.ProxyURL, timeout)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	applyAuthHeaders(httpReq, ep.APIKey, ep.AuthStyle, storage.ProbeProtocolOpenAIChat)
	applyCommonHeaders(httpReq, ep.UserAgent, ep.ExtraHeaders)

	resp, err := hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxCaptureBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, extractErrorMessage(rawBody, resp.Status))
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	out := make([]string, 0, len(parsed.Data))
	seen := make(map[string]struct{}, len(parsed.Data))
	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

// Complete 向目标模型发送流式推理请求并采集完整性能与文本结果。
func (c *Client) Complete(ctx context.Context, req Request) (*Response, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		return &Response{ErrorClass: ErrorClassConfig, ErrorMessage: "base_url is required"}, errors.New("base_url is required")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return &Response{ErrorClass: ErrorClassConfig, ErrorMessage: "model is required"}, errors.New("model is required")
	}

	proto := normalizeProtocol(req.Protocol)
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	hc, err := c.httpClient(req.ProxyURL, timeout)
	if err != nil {
		return &Response{ErrorClass: ErrorClassConfig, ErrorMessage: err.Error()}, err
	}

	endpointPath, payload, err := buildRequestBody(proto, req)
	if err != nil {
		return &Response{ErrorClass: ErrorClassConfig, ErrorMessage: err.Error()}, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	targetURL := joinEndpointPath(baseURL, endpointPath)
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return &Response{ErrorClass: ErrorClassConfig, ErrorMessage: err.Error()}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream, application/json")
	applyAuthHeaders(httpReq, req.APIKey, req.AuthStyle, proto)
	applyCommonHeaders(httpReq, req.UserAgent, req.ExtraHeaders)
	if proto == storage.ProbeProtocolAnthropic && httpReq.Header.Get("anthropic-version") == "" {
		httpReq.Header.Set("anthropic-version", DefaultAnthropicVersion)
	}

	started := time.Now()
	httpResp, err := hc.Do(httpReq)
	if err != nil {
		latency := time.Since(started).Milliseconds()
		errClass := classifyTransportError(err)
		return &Response{
			LatencyMS:    latency,
			ErrorClass:   errClass,
			ErrorMessage: truncateString(err.Error(), 500),
		}, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 64*1024))
		latency := time.Since(started).Milliseconds()
		errClass := classifyHTTPStatus(httpResp.StatusCode)
		msg := extractErrorMessage(errBody, httpResp.Status)
		res := &Response{
			HTTPStatus:   httpResp.StatusCode,
			LatencyMS:    latency,
			ErrorClass:   errClass,
			ErrorMessage: msg,
		}
		return res, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, msg)
	}

	contentType := strings.ToLower(httpResp.Header.Get("Content-Type"))
	limited := io.LimitReader(httpResp.Body, MaxCaptureBytes)
	var res *Response
	if strings.Contains(contentType, "text/event-stream") {
		res, err = readSSEStream(limited, proto, started)
	} else {
		// 兼容部分上游无视 stream:true 直接返回 JSON，或未声明 text/event-stream 但实际返回 SSE。
		raw, readErr := io.ReadAll(limited)
		if readErr != nil {
			latency := time.Since(started).Milliseconds()
			return &Response{
				HTTPStatus:   httpResp.StatusCode,
				LatencyMS:    latency,
				ErrorClass:   classifyTransportError(readErr),
				ErrorMessage: truncateString(readErr.Error(), 500),
			}, readErr
		}
		if looksLikeSSE(raw) {
			res, err = readSSEStream(bytes.NewReader(raw), proto, started)
		} else {
			res = parseNonStreamJSON(raw, proto, started)
		}
	}
	if res == nil {
		res = &Response{}
	}
	res.HTTPStatus = httpResp.StatusCode
	if err != nil {
		if res.ErrorClass == "" {
			res.ErrorClass = classifyTransportError(err)
		}
		if res.ErrorMessage == "" {
			res.ErrorMessage = truncateString(err.Error(), 500)
		}
		return res, err
	}

	finalizeResponseMetrics(res)
	return res, nil
}

func normalizeProtocol(proto string) string {
	switch strings.ToLower(strings.TrimSpace(proto)) {
	case storage.ProbeProtocolOpenAIResponses, "responses":
		return storage.ProbeProtocolOpenAIResponses
	case storage.ProbeProtocolAnthropic, "claude":
		return storage.ProbeProtocolAnthropic
	default:
		return storage.ProbeProtocolOpenAIChat
	}
}

func normalizeReasoningEffort(effort string) string {
	e := strings.ToLower(strings.TrimSpace(effort))
	switch e {
	case "", "omit":
		return ""
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return e
	default:
		return e
	}
}

func buildRequestBody(proto string, req Request) (string, []byte, error) {
	effort := normalizeReasoningEffort(req.ReasoningEffort)
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	switch proto {
	case storage.ProbeProtocolOpenAIResponses:
		body := map[string]any{
			"model":             strings.TrimSpace(req.Model),
			"stream":            true,
			"input":             req.Prompt,
			"max_output_tokens": maxTokens,
		}
		if strings.TrimSpace(req.SystemPrompt) != "" {
			body["instructions"] = req.SystemPrompt
		}
		if effort != "" {
			body["reasoning"] = map[string]any{"effort": effort}
		}
		if req.Temperature != nil {
			body["temperature"] = *req.Temperature
		}
		raw, err := json.Marshal(body)
		return "/v1/responses", raw, err

	case storage.ProbeProtocolAnthropic:
		body := map[string]any{
			"model":      strings.TrimSpace(req.Model),
			"stream":     true,
			"max_tokens": maxTokens,
			"messages": []map[string]any{
				{"role": "user", "content": req.Prompt},
			},
		}
		if strings.TrimSpace(req.SystemPrompt) != "" {
			body["system"] = req.SystemPrompt
		}
		if req.Temperature != nil {
			body["temperature"] = *req.Temperature
		}
		raw, err := json.Marshal(body)
		return "/v1/messages", raw, err

	default:
		messages := make([]map[string]any, 0, 2)
		if strings.TrimSpace(req.SystemPrompt) != "" {
			messages = append(messages, map[string]any{"role": "system", "content": req.SystemPrompt})
		}
		messages = append(messages, map[string]any{"role": "user", "content": req.Prompt})
		body := map[string]any{
			"model":          strings.TrimSpace(req.Model),
			"stream":         true,
			"stream_options": map[string]any{"include_usage": true},
			"max_tokens":     maxTokens,
			"messages":       messages,
		}
		if effort != "" {
			body["reasoning_effort"] = effort
		}
		if req.Temperature != nil {
			body["temperature"] = *req.Temperature
		}
		raw, err := json.Marshal(body)
		return "/v1/chat/completions", raw, err
	}
}

func joinEndpointPath(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return base + path
}

func applyAuthHeaders(req *http.Request, apiKey, authStyle, proto string) {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return
	}
	style := strings.ToLower(strings.TrimSpace(authStyle))
	if style == "" {
		if proto == storage.ProbeProtocolAnthropic {
			style = storage.GatewayProviderAuthXAPIKey
		} else {
			style = storage.GatewayProviderAuthBearer
		}
	}
	switch style {
	case storage.GatewayProviderAuthXAPIKey:
		req.Header.Set("x-api-key", key)
	case storage.GatewayProviderAuthBoth:
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

func applyCommonHeaders(req *http.Request, ua string, extra map[string]string) {
	agent := strings.TrimSpace(ua)
	if agent == "" {
		agent = DefaultUserAgent
	}
	req.Header.Set("User-Agent", agent)
	for k, v := range extra {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		lower := strings.ToLower(k)
		if lower == "authorization" || lower == "x-api-key" || lower == "content-length" || lower == "host" {
			continue
		}
		req.Header.Set(k, v)
	}
}

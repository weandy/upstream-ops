package llmclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/gateway"
	"github.com/bejix/upstream-ops/backend/storage"
)

var thinkTagRe = regexp.MustCompile(`(?is)<think>(.*?)</think>`)

func readSSEStream(r io.Reader, proto string, started time.Time) (*Response, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		contentBuf   strings.Builder
		reasoningBuf strings.Builder
		eventName    string
		dataLines    []string
		usage        gateway.UsageTokens
		firstTokenMS int64
		terminalOK   bool
		finishReason string
		streamErrMsg string
		sawDeltaText bool
	)

	markFirstToken := func() {
		if firstTokenMS == 0 {
			ms := time.Since(started).Milliseconds()
			if ms <= 0 {
				ms = 1
			}
			firstTokenMS = ms
		}
	}

	flushEvent := func() {
		if len(dataLines) == 0 && eventName == "" {
			return
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		ev := strings.ToLower(strings.TrimSpace(eventName))
		eventName = ""
		dataLines = dataLines[:0]
		if data == "" {
			return
		}
		if data == "[DONE]" {
			if finishReason == "" || finishReason == "stop" || finishReason == "end_turn" || finishReason == "completed" {
				terminalOK = true
			}
			return
		}

		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			return
		}
		if t, _ := obj["type"].(string); t != "" {
			ev = strings.ToLower(strings.TrimSpace(t))
		}

		switch proto {
		case storage.ProbeProtocolOpenAIResponses:
			u := gateway.ParseOpenAIUsage([]byte(data))
			usage = mergeUsage(usage, u)
			switch ev {
			case "response.output_text.delta":
				if delta, _ := obj["delta"].(string); delta != "" {
					markFirstToken()
					sawDeltaText = true
					contentBuf.WriteString(delta)
				}
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				if delta, _ := obj["delta"].(string); delta != "" {
					reasoningBuf.WriteString(delta)
				}
			case "response.completed", "response.done":
				respObj, _ := obj["response"].(map[string]any)
				status := ""
				if respObj != nil {
					status = strings.ToLower(strings.TrimSpace(asString(respObj["status"])))
					if !sawDeltaText {
						if txt := extractResponsesOutputText(respObj); txt != "" {
							markFirstToken()
							contentBuf.WriteString(txt)
						}
					}
				}
				switch status {
				case "failed", "cancelled", "canceled":
					finishReason = status
					streamErrMsg = extractNestedError(respObj, "response failed")
					terminalOK = false
				case "incomplete":
					finishReason = "incomplete"
					terminalOK = false
				default:
					finishReason = "completed"
					terminalOK = true
				}
			case "response.incomplete":
				finishReason = "incomplete"
				terminalOK = false
			case "response.failed", "response.cancelled", "response.canceled", "error":
				finishReason = ev
				streamErrMsg = extractNestedError(obj, "upstream response failed")
				terminalOK = false
			}

		case storage.ProbeProtocolAnthropic:
			u := parseAnthropicEventUsage(obj, []byte(data))
			usage = mergeUsage(usage, u)
			switch ev {
			case "content_block_delta":
				deltaObj, _ := obj["delta"].(map[string]any)
				if deltaObj != nil {
					dt := strings.ToLower(asString(deltaObj["type"]))
					switch dt {
					case "text_delta":
						if txt := asString(deltaObj["text"]); txt != "" {
							markFirstToken()
							contentBuf.WriteString(txt)
						}
					case "thinking_delta":
						if txt := asString(deltaObj["thinking"]); txt != "" {
							reasoningBuf.WriteString(txt)
						}
					}
				}
			case "message_delta":
				if deltaObj, _ := obj["delta"].(map[string]any); deltaObj != nil {
					if sr := strings.TrimSpace(asString(deltaObj["stop_reason"])); sr != "" {
						finishReason = sr
					}
				}
			case "message_stop":
				if finishReason != "max_tokens" {
					terminalOK = true
				}
			case "error":
				streamErrMsg = extractNestedError(obj, "anthropic stream error")
				terminalOK = false
			}

		default: // openai_chat
			u := gateway.ParseOpenAIUsage([]byte(data))
			usage = mergeUsage(usage, u)
			if errObj, _ := obj["error"].(map[string]any); errObj != nil {
				streamErrMsg = extractNestedError(obj, "upstream stream error")
				terminalOK = false
				return
			}
			choices, _ := obj["choices"].([]any)
			if len(choices) > 0 {
				c0, _ := choices[0].(map[string]any)
				if c0 != nil {
					if fr := strings.TrimSpace(asString(c0["finish_reason"])); fr != "" {
						finishReason = fr
						if fr == "stop" || fr == "end_turn" {
							terminalOK = true
						} else if fr == "length" {
							terminalOK = false
						}
					}
					if delta, _ := c0["delta"].(map[string]any); delta != nil {
						if rc := asString(delta["reasoning_content"]); rc != "" {
							reasoningBuf.WriteString(rc)
						}
						if rc := asString(delta["reasoning"]); rc != "" {
							reasoningBuf.WriteString(rc)
						}
						if content := asString(delta["content"]); content != "" {
							markFirstToken()
							contentBuf.WriteString(content)
						}
					}
				}
			}
		}
	}

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			flushEvent()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flushEvent()
	if err := sc.Err(); err != nil {
		return &Response{
			Content:      contentBuf.String(),
			Reasoning:    reasoningBuf.String(),
			LatencyMS:    time.Since(started).Milliseconds(),
			FirstTokenMS: firstTokenMS,
			ErrorClass:   classifyTransportError(err),
			ErrorMessage: truncateString(err.Error(), 500),
		}, err
	}

	content, inlineReasoning := stripThinkTags(contentBuf.String())
	if inlineReasoning != "" {
		if reasoningBuf.Len() > 0 {
			reasoningBuf.WriteString("\n")
		}
		reasoningBuf.WriteString(inlineReasoning)
	}

	latency := time.Since(started).Milliseconds()
	if latency <= 0 {
		latency = 1
	}

	res := &Response{
		Content:      content,
		Reasoning:    reasoningBuf.String(),
		Complete:     terminalOK && streamErrMsg == "",
		FinishReason: finishReason,
		FirstTokenMS: firstTokenMS,
		LatencyMS:    latency,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		ErrorClass:   ErrorClassOK,
	}
	if streamErrMsg != "" {
		res.Complete = false
		res.ErrorClass = ErrorClassUpstream
		res.ErrorMessage = truncateString(streamErrMsg, 500)
	} else if !res.Complete {
		res.ErrorClass = ErrorClassTruncated
		if finishReason == "length" || finishReason == "max_tokens" || finishReason == "incomplete" {
			res.ErrorMessage = "response truncated: " + finishReason
		} else {
			res.ErrorMessage = "stream ended before terminal event"
		}
	}
	return res, nil
}

func parseNonStreamJSON(raw []byte, proto string, started time.Time) *Response {
	latency := time.Since(started).Milliseconds()
	if latency <= 0 {
		latency = 1
	}
	res := &Response{
		LatencyMS:    latency,
		FirstTokenMS: latency,
		ErrorClass:   ErrorClassOK,
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		res.ErrorClass = ErrorClassUpstream
		res.ErrorMessage = "invalid JSON response"
		return res
	}
	if errObj, _ := obj["error"].(map[string]any); errObj != nil {
		res.ErrorClass = ErrorClassUpstream
		res.ErrorMessage = extractNestedError(obj, "upstream error")
		return res
	}

	var rawContent, rawReasoning string
	switch proto {
	case storage.ProbeProtocolOpenAIResponses:
		u := gateway.ParseOpenAIUsage(raw)
		res.InputTokens, res.OutputTokens = u.InputTokens, u.OutputTokens
		rawContent = extractResponsesOutputText(obj)
		status := strings.ToLower(strings.TrimSpace(asString(obj["status"])))
		res.FinishReason = status
		res.Complete = status == "" || status == "completed"
	case storage.ProbeProtocolAnthropic:
		u := gateway.ParseAnthropicUsage(raw)
		res.InputTokens, res.OutputTokens = u.InputTokens, u.OutputTokens
		res.FinishReason = strings.TrimSpace(asString(obj["stop_reason"]))
		if blocks, _ := obj["content"].([]any); len(blocks) > 0 {
			var cb, rb strings.Builder
			for _, b := range blocks {
				bm, _ := b.(map[string]any)
				if bm == nil {
					continue
				}
				switch strings.ToLower(asString(bm["type"])) {
				case "text":
					cb.WriteString(asString(bm["text"]))
				case "thinking":
					rb.WriteString(asString(bm["thinking"]))
				}
			}
			rawContent = cb.String()
			rawReasoning = rb.String()
		}
		res.Complete = res.FinishReason != "max_tokens" && rawContent != ""
	default:
		u := gateway.ParseOpenAIUsage(raw)
		res.InputTokens, res.OutputTokens = u.InputTokens, u.OutputTokens
		if choices, _ := obj["choices"].([]any); len(choices) > 0 {
			c0, _ := choices[0].(map[string]any)
			if c0 != nil {
				res.FinishReason = strings.TrimSpace(asString(c0["finish_reason"]))
				if msg, _ := c0["message"].(map[string]any); msg != nil {
					rawContent = asString(msg["content"])
					rawReasoning = asString(msg["reasoning_content"])
					if rawReasoning == "" {
						rawReasoning = asString(msg["reasoning"])
					}
				}
			}
		}
		res.Complete = res.FinishReason != "length" && (res.FinishReason == "stop" || res.FinishReason == "end_turn" || rawContent != "")
	}

	content, inlineReasoning := stripThinkTags(rawContent)
	res.Content = content
	if inlineReasoning != "" {
		if rawReasoning != "" {
			rawReasoning += "\n"
		}
		rawReasoning += inlineReasoning
	}
	res.Reasoning = rawReasoning
	if !res.Complete && res.ErrorClass == ErrorClassOK {
		res.ErrorClass = ErrorClassTruncated
		res.ErrorMessage = "incomplete response"
	}
	return res
}

func finalizeResponseMetrics(res *Response) {
	if res == nil {
		return
	}
	if res.OutputTokens == 0 && len(res.Content) > 0 {
		// 上游未返回 usage 时保守估算输出 Token（按每 4 字节 ~1 token，至少 1）。
		est := len(res.Content) / 4
		if est < 1 {
			est = 1
		}
		res.OutputTokens = est
		res.UsageEstimated = true
	}
	if res.OutputTokens > 0 && res.LatencyMS > 0 {
		genMS := res.LatencyMS - res.FirstTokenMS
		if genMS <= 0 {
			genMS = res.LatencyMS
		}
		res.TPS = float64(res.OutputTokens) / (float64(genMS) / 1000.0)
	}
}

func stripThinkTags(s string) (clean, thinking string) {
	if !strings.Contains(strings.ToLower(s), "<think>") {
		return s, ""
	}
	var thinkParts []string
	matches := thinkTagRe.FindAllStringSubmatch(s, -1)
	for _, m := range matches {
		if len(m) > 1 {
			if part := strings.TrimSpace(m[1]); part != "" {
				thinkParts = append(thinkParts, part)
			}
		}
	}
	clean = strings.TrimSpace(thinkTagRe.ReplaceAllString(s, ""))
	return clean, strings.Join(thinkParts, "\n")
}

func extractResponsesOutputText(obj map[string]any) string {
	if obj == nil {
		return ""
	}
	if direct := asString(obj["output_text"]); direct != "" {
		return direct
	}
	outputs, _ := obj["output"].([]any)
	var b strings.Builder
	for _, item := range outputs {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		contents, _ := m["content"].([]any)
		for _, c := range contents {
			cm, _ := c.(map[string]any)
			if cm == nil {
				continue
			}
			typ := strings.ToLower(asString(cm["type"]))
			if typ == "output_text" || typ == "text" {
				b.WriteString(asString(cm["text"]))
			}
		}
	}
	return b.String()
}

func parseAnthropicEventUsage(obj map[string]any, raw []byte) gateway.UsageTokens {
	u := gateway.ParseAnthropicUsage(raw)
	if u.InputTokens > 0 || u.OutputTokens > 0 {
		return u
	}
	if msg, _ := obj["message"].(map[string]any); msg != nil {
		if usageObj, _ := msg["usage"].(map[string]any); usageObj != nil {
			b, _ := json.Marshal(map[string]any{"usage": usageObj})
			return gateway.ParseAnthropicUsage(b)
		}
	}
	return u
}

func mergeUsage(prev, next gateway.UsageTokens) gateway.UsageTokens {
	out := prev
	if next.InputTokens > 0 {
		out.InputTokens = next.InputTokens
	}
	if next.OutputTokens > 0 {
		out.OutputTokens = next.OutputTokens
	}
	if next.ReasoningTokens > 0 {
		out.ReasoningTokens = next.ReasoningTokens
	}
	return out
}

func looksLikeSSE(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:"))
}

func classifyHTTPStatus(code int) string {
	switch {
	case code == 401 || code == 403:
		return ErrorClassAuth
	case code == 429:
		return ErrorClassRateLimit
	case code == 408 || code == 504:
		return ErrorClassTimeout
	case code == 400 || code == 404 || code == 405 || code == 413 || code == 415 || code == 422:
		return ErrorClassConfig
	case code >= 500:
		return ErrorClassUpstream
	default:
		return ErrorClassUpstream
	}
}

func classifyTransportError(err error) string {
	if err == nil {
		return ErrorClassOK
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorClassTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrorClassTimeout
	}
	return ErrorClassNetwork
}

func extractErrorMessage(body []byte, fallback string) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return fallback
	}
	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err == nil {
		if msg := extractNestedError(obj, ""); msg != "" {
			return truncateString(msg, 500)
		}
	}
	// 避免把大段 HTML 错误页直接入库展示。
	lower := strings.ToLower(string(trimmed[:min(len(trimmed), 128)]))
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		return fallback
	}
	return truncateString(string(trimmed), 500)
}

func extractNestedError(obj map[string]any, fallback string) string {
	if obj == nil {
		return fallback
	}
	if respObj, _ := obj["response"].(map[string]any); respObj != nil {
		if msg := extractNestedError(respObj, ""); msg != "" {
			return msg
		}
	}
	if errObj, _ := obj["error"].(map[string]any); errObj != nil {
		if msg := strings.TrimSpace(asString(errObj["message"])); msg != "" {
			return msg
		}
		if typ := strings.TrimSpace(asString(errObj["type"])); typ != "" {
			return typ
		}
	}
	if msg := strings.TrimSpace(asString(obj["message"])); msg != "" {
		return msg
	}
	// 兼容 RFC 7807 / Cloudflare 错误体（title + detail）。
	title := strings.TrimSpace(asString(obj["title"]))
	detail := strings.TrimSpace(asString(obj["detail"]))
	if title != "" && detail != "" {
		return title + " - " + detail
	}
	if title != "" {
		return title
	}
	if detail != "" {
		return detail
	}
	return fallback
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func truncateString(s string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}

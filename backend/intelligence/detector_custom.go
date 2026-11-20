package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

type customPromptParams struct {
	Prompt        string `json:"prompt"`
	SystemPrompt  string `json:"system_prompt,omitempty"`
	MatchMode     string `json:"match_mode"` // exact_number | contains | regex
	Expected      string `json:"expected"`
	Forbid        string `json:"forbid,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxTokens     int    `json:"max_tokens,omitempty"`
}

// CustomPromptDetector 用户自定义题目与规则判定插件。
type CustomPromptDetector struct{}

func NewCustomPromptDetector() Detector { return CustomPromptDetector{} }

func (CustomPromptDetector) Key() string { return DetectorKeyCustomPrompt }

func (CustomPromptDetector) Meta() DetectorMeta {
	return DetectorMeta{
		Key:                DetectorKeyCustomPrompt,
		Name:               "自定义智力题",
		Category:           DetectorCategoryIntelligence,
		Description:        "自定义测试提示词与答案校验规则（独立整数匹配 / 包含子串 / 正则表达式 + 禁止词），灵活扩展私有验真题库。",
		DefaultIntervalSec: 3600,
		DefaultTimeoutSec:  90,
		DefaultAlertThresh: 3,
		SupportsEmbed:      false,
		SupportsReasoning:  true,
		ParamsSchema: []ParamField{
			{Key: "prompt", Label: "测试提示词", Type: "textarea", Required: true},
			{Key: "system_prompt", Label: "系统提示词（可选）", Type: "textarea"},
			{Key: "match_mode", Label: "匹配模式", Type: "select", Required: true, Default: "contains", Options: []string{"exact_number", "contains", "regex"}},
			{Key: "expected", Label: "期望结果/正则", Type: "string", Required: true},
			{Key: "forbid", Label: "禁止包含内容（可选）", Type: "string"},
			{Key: "case_sensitive", Label: "区分大小写", Type: "boolean", Default: false},
			{Key: "max_tokens", Label: "最大输出 Token", Type: "number", Default: 1024},
		},
	}
}

func (CustomPromptDetector) ValidateParams(rawJSON string) error {
	p, err := parseCustomPromptParams(rawJSON)
	if err != nil {
		return err
	}
	if p.MatchMode == "regex" {
		if _, err := regexp.Compile(p.Expected); err != nil {
			return fmt.Errorf("invalid regex pattern: %w", err)
		}
	}
	return nil
}

func parseCustomPromptParams(rawJSON string) (*customPromptParams, error) {
	if strings.TrimSpace(rawJSON) == "" {
		return nil, errors.New("custom_prompt params_json is required")
	}
	var p customPromptParams
	if err := json.Unmarshal([]byte(rawJSON), &p); err != nil {
		return nil, fmt.Errorf("invalid custom_prompt params: %w", err)
	}
	p.Prompt = strings.TrimSpace(p.Prompt)
	p.Expected = strings.TrimSpace(p.Expected)
	p.MatchMode = strings.ToLower(strings.TrimSpace(p.MatchMode))
	if p.Prompt == "" {
		return nil, errors.New("prompt is required")
	}
	if p.Expected == "" {
		return nil, errors.New("expected is required")
	}
	switch p.MatchMode {
	case "exact_number", "contains", "regex":
	default:
		return nil, fmt.Errorf("unsupported match_mode %q", p.MatchMode)
	}
	return &p, nil
}

func (CustomPromptDetector) Run(ctx context.Context, client LLMClient, req DetectorRunRequest) (*DetectorRunResult, error) {
	p, err := parseCustomPromptParams(req.ParamsJSON)
	if err != nil {
		return nil, err
	}

	llmReq := req.Request
	llmReq.Prompt = p.Prompt
	if strings.TrimSpace(p.SystemPrompt) != "" {
		llmReq.SystemPrompt = p.SystemPrompt
	}
	if p.MaxTokens > 0 {
		llmReq.MaxTokens = p.MaxTokens
	} else if llmReq.MaxTokens <= 0 {
		llmReq.MaxTokens = 1024
	}
	if llmReq.Timeout <= 0 {
		llmReq.Timeout = 90 * time.Second
	}

	resp, err := client.Complete(ctx, llmReq)
	out := &DetectorRunResult{}
	if resp != nil {
		out.FirstTokenMS = resp.FirstTokenMS
		out.LatencyMS = resp.LatencyMS
		out.OutputTokens = resp.OutputTokens
	}
	if err != nil {
		out.Status = storage.IntelligenceStatusError
		out.ErrorMessage = err.Error()
		out.Summary = "检测失败"
		return out, nil
	}
	if resp == nil || !resp.Complete {
		out.Status = storage.IntelligenceStatusError
		if resp != nil && resp.ErrorMessage != "" {
			out.ErrorMessage = resp.ErrorMessage
		} else {
			out.ErrorMessage = "incomplete response"
		}
		out.Summary = "响应未完整结束"
		return out, nil
	}

	answer := strings.TrimSpace(resp.Content)
	passed, reason := evaluateCustomRule(answer, p)
	detail, _ := json.Marshal(map[string]any{
		"match_mode": p.MatchMode,
		"expected":   p.Expected,
		"passed":     passed,
		"reason":     reason,
		"answer":     truncateRunes(answer, 2000),
	})
	out.DetailJSON = string(detail)
	if passed {
		out.Status = storage.IntelligenceStatusNormal
		out.Summary = "通过：" + reason
	} else {
		out.Status = storage.IntelligenceStatusSuspected
		out.Summary = "未通过：" + reason
	}
	return out, nil
}

func evaluateCustomRule(answer string, p *customPromptParams) (bool, string) {
	if answer == "" {
		return false, "模型返回空答"
	}
	if forbid := strings.TrimSpace(p.Forbid); forbid != "" {
		hay, needle := answer, forbid
		if !p.CaseSensitive {
			hay, needle = strings.ToLower(hay), strings.ToLower(needle)
		}
		if strings.Contains(hay, needle) {
			return false, "命中禁止词 " + forbid
		}
	}

	switch p.MatchMode {
	case "exact_number":
		nums := candyNumberToken.FindAllString(answer, -1)
		for _, n := range nums {
			if n == p.Expected {
				return true, "命中整数 " + p.Expected
			}
		}
		return false, "未命中整数 " + p.Expected
	case "contains":
		hay, needle := answer, p.Expected
		if !p.CaseSensitive {
			hay, needle = strings.ToLower(hay), strings.ToLower(needle)
		}
		if strings.Contains(hay, needle) {
			return true, "包含期望内容"
		}
		return false, "未包含期望内容"
	case "regex":
		pattern := p.Expected
		if !p.CaseSensitive && !strings.HasPrefix(pattern, "(?i)") {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, "正则编译失败"
		}
		if re.MatchString(answer) {
			return true, "正则匹配成功"
		}
		return false, "正则未匹配"
	default:
		return false, "不支持的匹配模式"
	}
}

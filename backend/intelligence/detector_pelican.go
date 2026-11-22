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

const (
	pelicanMaxArtifactBytes = 256 << 10
	pelicanBuiltinVersion   = "鹈鹕原版html"
	pelicanMaxPromptRunes   = 8000
	pelicanMaxVersions      = 20
	pelicanPreamble         = "不使用任何工具，也不要创建或修改文件，直接在回复中完成以下任务：\n\n"
	pelicanPromptOriginal   = pelicanPreamble +
		"创建一个完整的 HTML 文件，使用内联 SVG 绘制一个鹈鹕骑自行车的 2D 动画，不要使用任何外部图片、脚本或字体资源。\n\n" +
		"请在一个 ```html 代码块中给出完整的 HTML 文件。"
)

var (
	fencedCodeBlockRe = regexp.MustCompile("(?is)```(?:html|svg|xml)?\\s*\\n(.*?)```")
	rawSVGBlockRe     = regexp.MustCompile(`(?is)<svg\b[^>]*>.*?</svg>`)
)

type pelicanVersion struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

type pelicanParams struct {
	Variant   string           `json:"variant,omitempty"`
	Version   string           `json:"version,omitempty"`
	Prompt    string           `json:"prompt,omitempty"`
	Versions  []pelicanVersion `json:"versions,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
}

func (p pelicanParams) selectedName() string {
	if name := strings.TrimSpace(p.Version); name != "" {
		return name
	}
	if name := strings.TrimSpace(p.Variant); name != "" {
		return name
	}
	return pelicanBuiltinVersion
}

func knownPelicanVariant(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "original", "all-in-one", "鹈鹕原版html":
		return true
	default:
		return false
	}
}

func validatePelicanParams(p pelicanParams) error {
	if len(p.Versions) > pelicanMaxVersions {
		return fmt.Errorf("pelican versions exceed %d", pelicanMaxVersions)
	}
	seen := map[string]struct{}{}
	for _, item := range p.Versions {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return errors.New("pelican version name is empty")
		}
		if knownPelicanVariant(name) {
			return fmt.Errorf("pelican version %q is reserved", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate pelican version %q", name)
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(item.Prompt) == "" {
			return fmt.Errorf("pelican version %q has empty prompt", name)
		}
		if runeLen(item.Prompt) > pelicanMaxPromptRunes {
			return fmt.Errorf("pelican version %q prompt is too long", name)
		}
	}
	if runeLen(p.Prompt) > pelicanMaxPromptRunes {
		return errors.New("pelican prompt is too long")
	}
	name := p.selectedName()
	if knownPelicanVariant(name) {
		return nil
	}
	if _, ok := seen[name]; !ok {
		return fmt.Errorf("unsupported pelican variant %q", name)
	}
	return nil
}

func resolvePelicanPrompt(p pelicanParams) (string, string) {
	name := p.selectedName()
	if knownPelicanVariant(name) {
		prompt := strings.TrimSpace(p.Prompt)
		if prompt == "" || prompt == pelicanPromptOriginal {
			return pelicanBuiltinVersion, pelicanPromptOriginal
		}
		return pelicanBuiltinVersion, prompt
	}
	for _, item := range p.Versions {
		if strings.TrimSpace(item.Name) == name {
			return name, strings.TrimSpace(item.Prompt)
		}
	}
	return pelicanBuiltinVersion, pelicanPromptOriginal
}

func runeLen(s string) int {
	return len([]rune(s))
}

// PelicanDetector 「画鹈鹕」SVG 空间推理与视觉直观检测插件。
type PelicanDetector struct{}

func NewPelicanDetector() Detector { return PelicanDetector{} }

func (PelicanDetector) Key() string { return DetectorKeyPelican }

func (PelicanDetector) Meta() DetectorMeta {
	return DetectorMeta{
		Key:                DetectorKeyPelican,
		Name:               "画鹈鹕 SVG",
		Category:           DetectorCategoryIntelligence,
		Description:        "定时请求模型绘制「鹈鹕骑自行车」的 HTML 动画，结果落库并通过双层沙箱 iframe 隔离渲染。",
		DefaultIntervalSec: 3600,
		DefaultTimeoutSec:  180,
		DefaultAlertThresh: 3,
		SupportsEmbed:      true,
		SupportsReasoning:  true,
		ParamsSchema: []ParamField{
			{
				Key:     "prompt",
				Label:   "题目",
				Type:    "textarea",
				Default: pelicanPromptOriginal,
			},
			{
				Key:         "max_tokens",
				Label:       "最大输出 Token",
				Type:        "number",
				Default:     4096,
				Description: "默认 4096（兼容绝大多数中转站上限），如上游支持更高上限可按需调大。",
			},
		},
	}
}

func (PelicanDetector) ValidateParams(rawJSON string) error {
	raw := strings.TrimSpace(rawJSON)
	if raw == "" || raw == "{}" {
		return nil
	}
	var p pelicanParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("invalid pelican params: %w", err)
	}
	if err := validatePelicanParams(p); err != nil {
		return err
	}
	if p.MaxTokens < 0 || p.MaxTokens > 32768 {
		return errors.New("max_tokens must be between 0 and 32768")
	}
	return nil
}

func (PelicanDetector) Run(ctx context.Context, client LLMClient, req DetectorRunRequest) (*DetectorRunResult, error) {
	variant := pelicanBuiltinVersion
	maxTokens := 4096
	prompt := pelicanPromptOriginal
	if raw := strings.TrimSpace(req.ParamsJSON); raw != "" && raw != "{}" {
		var p pelicanParams
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("invalid pelican params: %w", err)
		}
		if err := validatePelicanParams(p); err != nil {
			return nil, err
		}
		variant, prompt = resolvePelicanPrompt(p)
		if p.MaxTokens > 0 {
			maxTokens = p.MaxTokens
		}
	}

	llmReq := req.Request
	llmReq.Prompt = prompt
	if llmReq.MaxTokens <= 0 {
		llmReq.MaxTokens = maxTokens
	}
	if llmReq.Timeout <= 0 {
		llmReq.Timeout = 180 * time.Second
	}

	resp, err := client.Complete(ctx, llmReq)
	out := &DetectorRunResult{}
	if resp != nil {
		out.FirstTokenMS = resp.FirstTokenMS
		out.LatencyMS = resp.LatencyMS
		out.OutputTokens = resp.OutputTokens
		out.ErrorClass = resp.ErrorClass
		out.RetryAfterSec = resp.RetryAfterSec
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
			out.ErrorMessage = "incomplete pelican response"
		}
		out.Summary = "响应未完整结束"
		return out, nil
	}

	htmlDoc, extractErr := extractPelicanHTMLDocument(resp.Content)
	detail, _ := json.Marshal(map[string]any{
		"variant": variant,
		"bytes":   len(htmlDoc),
		"preview": truncateRunes(resp.Content, 300),
	})
	out.DetailJSON = string(detail)
	if extractErr != nil {
		out.Status = storage.IntelligenceStatusSuspected
		out.Summary = "未生成有效 SVG 画面"
		out.ErrorMessage = extractErr.Error()
		return out, nil
	}

	out.Status = storage.IntelligenceStatusNormal
	out.Summary = "已生成画面"
	out.ArtifactSVG = htmlDoc
	return out, nil
}

func extractPelicanHTMLDocument(raw string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if m := fencedCodeBlockRe.FindStringSubmatch(candidate); len(m) > 1 {
		candidate = strings.TrimSpace(m[1])
	}
	if len(candidate) > pelicanMaxArtifactBytes {
		return "", errors.New("pelican artifact exceeds 256KiB limit")
	}

	lower := strings.ToLower(candidate)
	if !strings.Contains(lower, "<svg") || !strings.Contains(lower, "</svg>") {
		return "", errors.New("no valid <svg> block found in response")
	}

	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		return candidate, nil
	}

	svgOnly := rawSVGBlockRe.FindString(candidate)
	if svgOnly == "" {
		return "", errors.New("failed to extract <svg> block")
	}
	return wrapSVGInHTMLDocument(svgOnly), nil
}

func wrapSVGInHTMLDocument(svg string) string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<style>
*{box-sizing:border-box}
html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#f4f7f4}
body{display:grid;place-items:center}
svg{display:block;width:100%;height:100%;max-width:100vw;max-height:100vh}
</style>
</head>
<body>
` + svg + `
</body>
</html>`
}

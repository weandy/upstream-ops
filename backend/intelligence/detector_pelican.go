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
	pelicanPreamble         = "不使用任何工具，也不要创建或修改文件，直接在回复中完成以下任务：\n\n"
	pelicanPromptOriginal   = pelicanPreamble +
		"创建一个完整的 HTML 文件，使用内联 SVG 绘制一个鹈鹕骑自行车的 2D 动画，不要使用任何外部图片、脚本或字体资源。\n\n" +
		"请在一个 ```html 代码块中给出完整的 HTML 文件。"
	pelicanPromptAllInOne = pelicanPreamble +
		"做一张精细的 SVG 图片，内容是鹈鹕骑着自行车在孙悟空开的大型飞机机翼上骑行，鹈鹕展开翅膀托着骑在北极熊上打螺丝的秦始皇，同时在画面中标出标准糖果逻辑题答案 21。\n\n" +
		"不要使用任何外部资源，请在一个 ```svg 代码块中给出完整的 SVG 文件。"
)

var (
	fencedCodeBlockRe = regexp.MustCompile("(?is)```(?:html|svg|xml)?\\s*\\n(.*?)```")
	rawSVGBlockRe     = regexp.MustCompile(`(?is)<svg\b[^>]*>.*?</svg>`)
)

type pelicanParams struct {
	Variant   string `json:"variant,omitempty"` // original | all-in-one
	MaxTokens int    `json:"max_tokens,omitempty"`
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
		Description:        "定时请求模型绘制「鹈鹕骑自行车」SVG 动画，结果落库并通过双层沙箱 iframe 隔离渲染，直观观察模型空间与编码能力。",
		DefaultIntervalSec: 3600,
		DefaultTimeoutSec:  180,
		DefaultAlertThresh: 3,
		SupportsEmbed:      true,
		SupportsReasoning:  true,
		ParamsSchema: []ParamField{
			{
				Key:         "variant",
				Label:       "题型版本",
				Type:        "select",
				Default:     "original",
				Options:     []string{"original", "all-in-one"},
				Description: "original 为经典鹈鹕骑自行车动画；all-in-one 为复杂多元素场景 SVG。",
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
	switch strings.ToLower(strings.TrimSpace(p.Variant)) {
	case "", "original", "all-in-one":
	default:
		return fmt.Errorf("unsupported pelican variant %q (want original or all-in-one)", p.Variant)
	}
	if p.MaxTokens < 0 || p.MaxTokens > 32768 {
		return errors.New("max_tokens must be between 0 and 32768")
	}
	return nil
}

func (PelicanDetector) Run(ctx context.Context, client LLMClient, req DetectorRunRequest) (*DetectorRunResult, error) {
	variant := "original"
	maxTokens := 4096
	if raw := strings.TrimSpace(req.ParamsJSON); raw != "" && raw != "{}" {
		var p pelicanParams
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("invalid pelican params: %w", err)
		}
		if v := strings.ToLower(strings.TrimSpace(p.Variant)); v != "" {
			variant = v
		}
		if p.MaxTokens > 0 {
			maxTokens = p.MaxTokens
		}
	}

	llmReq := req.Request
	if variant == "all-in-one" {
		llmReq.Prompt = pelicanPromptAllInOne
	} else {
		llmReq.Prompt = pelicanPromptOriginal
	}
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

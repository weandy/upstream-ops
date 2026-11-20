package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	DetectorCategoryLiveness     = "liveness"
	DetectorCategoryIntelligence = "intelligence"
)

// ParamField 描述插件可配置的参数元信息，供前端动态渲染配置表单。
type ParamField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // string | textarea | select | number | boolean
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Description string   `json:"description,omitempty"`
}

// DetectorMeta 描述检测插件的展示元信息与默认运行参数。
type DetectorMeta struct {
	Key                string       `json:"key"`
	Name               string       `json:"name"`
	Category           string       `json:"category"` // liveness | intelligence
	Description        string       `json:"description"`
	DefaultIntervalSec int          `json:"default_interval_seconds"`
	DefaultTimeoutSec  int          `json:"default_timeout_seconds"`
	DefaultAlertThresh int          `json:"default_alert_threshold"`
	SupportsEmbed      bool         `json:"supports_embed"`
	SupportsReasoning  bool         `json:"supports_reasoning"`
	ParamsSchema       []ParamField `json:"params_schema,omitempty"`
}

// DetectorRunRequest 单次插件执行上下文入参。
type DetectorRunRequest struct {
	TargetID   uint
	Request    llmclient.Request
	ParamsJSON string
}

// DetectorRunResult 单次插件执行的统一输出。
//   - Category=liveness 时填充 LivenessSample；
//   - Category=intelligence 时填充 Status/Summary/DetailJSON/ArtifactSVG 等字段。
type DetectorRunResult struct {
	Status         string                       `json:"status"` // normal | suspected | error
	Summary        string                       `json:"summary"`
	ClaimedRank    int                          `json:"claimed_rank,omitempty"`
	TopCandidate   string                       `json:"top_candidate,omitempty"`
	FirstTokenMS   int64                        `json:"first_token_ms"`
	LatencyMS      int64                        `json:"latency_ms"`
	OutputTokens   int                          `json:"output_tokens"`
	DetailJSON     string                       `json:"detail_json,omitempty"`
	ArtifactSVG    string                       `json:"artifact_svg,omitempty"`
	ErrorMessage   string                       `json:"error_message,omitempty"`
	LivenessSample *storage.ProbeLivenessSample `json:"liveness_sample,omitempty"`
}

// Detector 可插拔监测插件统一接口。
type Detector interface {
	Key() string
	Meta() DetectorMeta
	ValidateParams(rawJSON string) error
	Run(ctx context.Context, client LLMClient, req DetectorRunRequest) (*DetectorRunResult, error)
}

// Registry 线程安全的插件注册中心，保持注册顺序输出。
type Registry struct {
	mu    sync.RWMutex
	order []string
	items map[string]Detector
}

func NewRegistry() *Registry {
	return &Registry{
		items: make(map[string]Detector),
	}
}

func (r *Registry) Register(d Detector) error {
	if d == nil {
		return errors.New("nil detector")
	}
	key := strings.TrimSpace(d.Key())
	if key == "" {
		return errors.New("detector key is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[key]; exists {
		return fmt.Errorf("detector %q already registered", key)
	}
	r.order = append(r.order, key)
	r.items[key] = d
	return nil
}

func (r *Registry) Get(key string) (Detector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.items[strings.TrimSpace(key)]
	return d, ok
}

func (r *Registry) ListMeta() []DetectorMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]DetectorMeta, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.items[k].Meta())
	}
	return out
}

// livenessParams 主动测活可选自定义参数。
type livenessParams struct {
	Prompt    string `json:"prompt,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// LivenessDetector 将主动测活包装为标准可插拔 Detector。
type LivenessDetector struct{}

func NewLivenessDetector() Detector { return LivenessDetector{} }

func (LivenessDetector) Key() string { return DetectorKeyLiveness }

func (LivenessDetector) Meta() DetectorMeta {
	return DetectorMeta{
		Key:                DetectorKeyLiveness,
		Name:               "主动测活与性能",
		Category:           DetectorCategoryLiveness,
		Description:        "定期发起轻量流式请求，采集首字延迟（TTFT）、平均延迟、TPS、24 小时成功率与近 90 分钟可用率趋势。",
		DefaultIntervalSec: DefaultLivenessIntervalSec,
		DefaultTimeoutSec:  DefaultLivenessTimeoutSec,
		DefaultAlertThresh: 3,
		SupportsEmbed:      true,
		SupportsReasoning:  false,
		ParamsSchema: []ParamField{
			{
				Key:         "prompt",
				Label:       "自定义测活提示词",
				Type:        "textarea",
				Description: "留空使用内置轻量英文短句提示词（约 20 词）。",
			},
			{
				Key:         "max_tokens",
				Label:       "最大输出 Token",
				Type:        "number",
				Default:     64,
				Description: "单次测活输出上限，建议 32~128。",
			},
		},
	}
}

func (LivenessDetector) ValidateParams(rawJSON string) error {
	raw := strings.TrimSpace(rawJSON)
	if raw == "" || raw == "{}" {
		return nil
	}
	var p livenessParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("invalid liveness params: %w", err)
	}
	if p.MaxTokens < 0 || p.MaxTokens > 1024 {
		return errors.New("max_tokens must be between 0 and 1024")
	}
	return nil
}

func (LivenessDetector) Run(ctx context.Context, client LLMClient, req DetectorRunRequest) (*DetectorRunResult, error) {
	llmReq := req.Request
	if raw := strings.TrimSpace(req.ParamsJSON); raw != "" && raw != "{}" {
		var p livenessParams
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("invalid liveness params: %w", err)
		}
		if strings.TrimSpace(p.Prompt) != "" {
			llmReq.Prompt = p.Prompt
		}
		if p.MaxTokens > 0 {
			llmReq.MaxTokens = p.MaxTokens
		}
	}
	sample := RunLivenessProbe(ctx, client, llmReq, req.TargetID)
	status := storage.IntelligenceStatusNormal
	summary := fmt.Sprintf("TTFT %dms · %.1f t/s", sample.FirstTokenMS, sample.TPS)
	if !sample.Success {
		status = storage.IntelligenceStatusError
		summary = sample.ErrorClass
		if sample.ErrorMessage != "" {
			summary = sample.ErrorMessage
		}
	}
	return &DetectorRunResult{
		Status:         status,
		Summary:        summary,
		FirstTokenMS:   sample.FirstTokenMS,
		LatencyMS:      sample.LatencyMS,
		OutputTokens:   sample.OutputTokens,
		ErrorMessage:   sample.ErrorMessage,
		LivenessSample: sample,
	}, nil
}

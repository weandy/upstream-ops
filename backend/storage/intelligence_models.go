package storage

import "time"

// 监测目标来源、密钥来源、协议与智力检测状态常量。
const (
	ProbeSourceChannel = "channel"
	ProbeSourceDirect  = "direct"

	ProbeAPIKeyManual         = "manual"
	ProbeAPIKeyAutoChannelKey = "auto_channel_key"

	ProbeProtocolOpenAIChat      = "openai_chat"
	ProbeProtocolOpenAIResponses = "openai_responses"
	ProbeProtocolAnthropic       = "anthropic"

	ProbeFamilyClaude   = "claude"
	ProbeFamilyCodex    = "codex"
	ProbeFamilyGrok     = "grok"
	ProbeFamilyDomestic = "domestic"
	ProbeFamilyOther    = "other"

	IntelligenceStatusNormal    = "normal"
	IntelligenceStatusSuspected = "suspected"
	IntelligenceStatusError     = "error"
)

// ProbeTarget 监测目标（外挂式纯运维对象，独立于转发网关）。
// 可绑定已有监控渠道+源分组，或直连任意 BaseURL + APIKey。
type ProbeTarget struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	Name             string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Slug             string    `gorm:"size:128;not null;uniqueIndex" json:"slug"`
	Family           string    `gorm:"size:32;not null;default:'other'" json:"family"`
	SourceKind       string    `gorm:"size:16;not null;default:'direct';index" json:"source_kind"`
	ChannelID        uint      `gorm:"not null;default:0;index" json:"channel_id"`
	SourceGroupID    *int64    `json:"source_group_id,omitempty"`
	SourceGroupName  string    `gorm:"size:256;not null;default:''" json:"source_group_name,omitempty"`
	BaseURL          string    `gorm:"size:512;not null;default:''" json:"base_url"`
	APIKeySource     string    `gorm:"size:32;not null;default:'manual'" json:"api_key_source"`
	APIKeyID         int64     `gorm:"not null;default:0" json:"api_key_id,omitempty"`
	APIKeyName       string    `gorm:"size:256;not null;default:''" json:"api_key_name,omitempty"`
	APIKeyCipher     string    `gorm:"type:text;not null;default:''" json:"-"`
	APIKeyHint       string    `gorm:"size:64;not null;default:''" json:"api_key_hint"`
	Protocol         string    `gorm:"size:32;not null;default:'openai_chat'" json:"protocol"`
	AuthStyle        string    `gorm:"size:16;not null;default:'bearer'" json:"auth_style"`
	ProxyEnabled     bool      `gorm:"not null;default:false" json:"proxy_enabled"`
	UserAgent        string    `gorm:"size:512;not null;default:''" json:"user_agent"`
	ExtraHeadersJSON string    `gorm:"type:text;not null;default:''" json:"extra_headers_json,omitempty"`
	SortOrder        int       `gorm:"not null;default:0;index" json:"sort_order"`
	Enabled          bool      `gorm:"not null;default:true;index" json:"enabled"`
	EmbedVisible     bool      `gorm:"not null;default:false" json:"embed_visible"`
	Notes            string    `gorm:"type:text;not null;default:''" json:"notes,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (ProbeTarget) TableName() string { return "probe_targets" }

// ProbeDetectorConfig 目标 × 检测插件配置（实现可插拔、按目标自由开关）。
type ProbeDetectorConfig struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	TargetID        uint       `gorm:"not null;uniqueIndex:idx_probe_target_detector" json:"target_id"`
	DetectorKey     string     `gorm:"size:64;not null;uniqueIndex:idx_probe_target_detector" json:"detector_key"`
	Enabled         bool       `gorm:"not null;default:false;index" json:"enabled"`
	ModelName       string     `gorm:"size:256;not null;default:''" json:"model_name"`
	ReasoningEffort string     `gorm:"size:32;not null;default:'omit'" json:"reasoning_effort"`
	IntervalSeconds int        `gorm:"not null;default:180" json:"interval_seconds"`
	TimeoutSeconds  int        `gorm:"not null;default:60" json:"timeout_seconds"`
	AlertEnabled    bool       `gorm:"not null;default:true" json:"alert_enabled"`
	AlertThreshold  int        `gorm:"not null;default:3" json:"alert_threshold"`
	ParamsJSON        string     `gorm:"type:text;not null;default:''" json:"params_json,omitempty"`
	EmbedVisible      bool       `gorm:"not null;default:true" json:"embed_visible"`
	ConsecutiveErrors int        `gorm:"not null;default:0" json:"consecutive_errors"`
	LastErrorClass    string     `gorm:"size:32;not null;default:''" json:"last_error_class,omitempty"`
	BackoffUntil      *time.Time `json:"backoff_until,omitempty"`
	LastRunAt         *time.Time `json:"last_run_at,omitempty"`
	NextRunAt         *time.Time `gorm:"index" json:"next_run_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (ProbeDetectorConfig) TableName() string { return "probe_detector_configs" }

// ProbeLivenessSample 主动测活与性能采样时间序列（用于 24h 指标与 60 分钟趋势条）。
type ProbeLivenessSample struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	TargetID     uint      `gorm:"not null;index:idx_liveness_target_observed" json:"target_id"`
	ModelName    string    `gorm:"size:256;not null;default:''" json:"model_name"`
	Success      bool      `gorm:"not null;default:false" json:"success"`
	HTTPStatus   int       `gorm:"not null;default:0" json:"http_status"`
	ErrorClass   string    `gorm:"size:32;not null;default:'ok'" json:"error_class"`
	ErrorMessage string    `gorm:"type:text;not null;default:''" json:"error_message,omitempty"`
	FirstTokenMS int64     `gorm:"not null;default:0" json:"first_token_ms"`
	LatencyMS    int64     `gorm:"not null;default:0" json:"latency_ms"`
	OutputTokens int       `gorm:"not null;default:0" json:"output_tokens"`
	TPS          float64   `gorm:"not null;default:0" json:"tps"`
	ObservedAt   time.Time `gorm:"not null;index:idx_liveness_target_observed;index" json:"observed_at"`
}

func (ProbeLivenessSample) TableName() string { return "probe_liveness_samples" }

// ProbeIntelligenceRun 智力与防降智检测单次运行记录。
type ProbeIntelligenceRun struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	RunUUID         string    `gorm:"size:64;not null;uniqueIndex" json:"run_uuid"`
	TargetID        uint      `gorm:"not null;index:idx_intel_target_det_time" json:"target_id"`
	DetectorKey     string    `gorm:"size:64;not null;index:idx_intel_target_det_time" json:"detector_key"`
	ModelName       string    `gorm:"size:256;not null;default:''" json:"model_name"`
	ReasoningEffort string    `gorm:"size:32;not null;default:''" json:"reasoning_effort,omitempty"`
	Status          string    `gorm:"size:32;not null;index" json:"status"`
	Summary         string    `gorm:"size:512;not null;default:''" json:"summary"`
	ClaimedRank     int       `gorm:"not null;default:0" json:"claimed_rank,omitempty"`
	TopCandidate    string    `gorm:"size:128;not null;default:''" json:"top_candidate,omitempty"`
	LatencyMS       int64     `gorm:"not null;default:0" json:"latency_ms"`
	FirstTokenMS    int64     `gorm:"not null;default:0" json:"first_token_ms"`
	OutputTokens    int       `gorm:"not null;default:0" json:"output_tokens"`
	DetailJSON      string    `gorm:"type:text;not null;default:''" json:"detail_json,omitempty"`
	ArtifactSVG     string    `gorm:"type:text;not null;default:''" json:"artifact_svg,omitempty"`
	ErrorMessage    string    `gorm:"type:text;not null;default:''" json:"error_message,omitempty"`
	SharedCacheKey  string    `gorm:"size:64;not null;default:'';index" json:"shared_cache_key,omitempty"`
	ReusedFromRunID *uint     `json:"reused_from_run_id,omitempty"`
	ObservedAt      time.Time `gorm:"not null;index:idx_intel_target_det_time;index" json:"observed_at"`
}

func (ProbeIntelligenceRun) TableName() string { return "probe_intelligence_runs" }

// EmbedSite 外部可嵌入性能看板站点配置（对应 /embed?site=<slug>）。
type EmbedSite struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Slug               string    `gorm:"size:64;not null;uniqueIndex" json:"slug"`
	Title              string    `gorm:"size:128;not null;default:'渠道性能'" json:"title"`
	Eyebrow            string    `gorm:"size:128;not null;default:'CHANNEL PERFORMANCE'" json:"eyebrow"`
	Enabled            bool      `gorm:"not null;default:true" json:"enabled"`
	RefreshSeconds     int       `gorm:"not null;default:15" json:"refresh_seconds"`
	AllowedOriginsJSON string    `gorm:"type:text;not null;default:'[]'" json:"allowed_origins_json"`
	CORSOriginsJSON    string    `gorm:"type:text;not null;default:'[]'" json:"cors_origins_json"`
	Theme              string    `gorm:"size:32;not null;default:'paper'" json:"theme"`
	TargetIDsJSON      string    `gorm:"type:text;not null;default:'[]'" json:"target_ids_json"`
	ShowIdentityColumn bool      `gorm:"not null;default:true" json:"show_identity_column"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (EmbedSite) TableName() string { return "embed_sites" }

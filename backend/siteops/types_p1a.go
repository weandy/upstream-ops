package siteops

import (
	"context"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// Wave 2 (P1-A) 新增端点与数据域常量。
const (
	EndpointOpsAlertEvents     = "ops.alert_events"
	EndpointOpsIngressHealth   = "ops.ingress_rejections_health"
	EndpointOpsAuthCacheHealth = "ops.auth_cache_invalidation_health"
	EndpointAccounts           = "accounts"

	DomainAuthProbe       = "auth_probe"
	DomainAuthCacheHealth = "auth_cache_health"
	DomainIngressHealth   = "ingress_health"
)

// P1-A 事件级别、状态与投递常量。
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"

	EventStateFiring   = "firing"
	EventStateResolved = "resolved"

	DeliveryKindAlert       = "alert"
	DeliveryKindRecovery    = "recovery"
	DeliveryKindDailyReport = "daily_report"

	DeliveryStatusPending         = "pending"
	DeliveryStatusSent            = "sent"
	DeliveryStatusFailed          = "failed"
	DeliveryStatusSkippedCooldown = "skipped_cooldown"
)

// P1-A 内置告警规则键。
const (
	RuleSiteOffline          = "site_offline"
	RuleAuthInvalid          = "auth_invalid"
	RuleInfraDown            = "infra_down"
	RuleGroupPoolExhausted   = "group_pool_exhausted"
	RuleSLADegraded          = "sla_degraded"
	RuleConcurrencySaturated = "concurrency_saturated"
	RuleHighSwitchSpike      = "high_switch_spike"
	RuleResourcePressure     = "resource_pressure"
	RuleJobOrCacheUnhealthy  = "job_or_cache_unhealthy"
	RuleAccountErrorNew      = "account_error_new"
	RuleAccountRateLimitWave = "account_rate_limit_wave"
)

// AlertConfig 单实例 P1-A 告警阈值、冷却与日报配置（存于 SiteOpsInstance.AlertConfigJSON）。
type AlertConfig struct {
	Enabled                       bool    `json:"enabled"`
	SLAThresholdPct               float64 `json:"sla_threshold_pct"`
	UpstreamErrorRateThresholdPct float64 `json:"upstream_error_rate_threshold_pct"`
	MinSamplesForSLA              int64   `json:"min_samples_for_sla"`
	ConcurrencyThresholdPct       float64 `json:"concurrency_threshold_pct"`
	SwitchCount1mThreshold        int64   `json:"switch_count_1m_threshold"`
	SwitchRate1mThresholdPct      float64 `json:"switch_rate_1m_threshold_pct"`
	ResourcePressurePct           float64 `json:"resource_pressure_pct"`
	RateLimitWaveRatioPct         float64 `json:"rate_limit_wave_ratio_pct"`
	CooldownMinutes               int     `json:"cooldown_minutes"`
	DailyReportEnabled            bool    `json:"daily_report_enabled"`
	DailyReportHourUTC8           int     `json:"daily_report_hour_utc8"`
	IncludeMaskedSummary          bool    `json:"include_masked_summary"`
}

func DefaultAlertConfig() AlertConfig {
	return AlertConfig{
		Enabled:                       true,
		SLAThresholdPct:               95,
		UpstreamErrorRateThresholdPct: 10,
		MinSamplesForSLA:              20,
		ConcurrencyThresholdPct:       85,
		SwitchCount1mThreshold:        15,
		SwitchRate1mThresholdPct:      15,
		ResourcePressurePct:           85,
		RateLimitWaveRatioPct:         50,
		CooldownMinutes:               30,
		DailyReportEnabled:            true,
		DailyReportHourUTC8:           9,
		IncludeMaskedSummary:          false,
	}
}

// SanitizedOpsAlertEvent 脱敏后的上游 /ops/alert-events 条目。
type SanitizedOpsAlertEvent struct {
	ID             int64      `json:"id"`
	RuleID         int64      `json:"rule_id"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	MetricValue    *float64   `json:"metric_value,omitempty"`
	ThresholdValue *float64   `json:"threshold_value,omitempty"`
	FiredAt        time.Time  `json:"fired_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

// SanitizedIngressRejectHealth 脱敏后的 /ops/ingress-rejections/health。
type SanitizedIngressRejectHealth struct {
	Cardinality    int64  `json:"cardinality"`
	Capacity       int    `json:"capacity"`
	PendingBatches int    `json:"pending_batches"`
	PendingRows    int    `json:"pending_rows"`
	Overflowed     uint64 `json:"overflowed_count"`
	Dropped        uint64 `json:"dropped_count"`
	Flushed        uint64 `json:"flushed_request_count"`
	FlushFailures  uint64 `json:"flush_failure_count"`
	Accepting      bool   `json:"accepting"`
	LastError      string `json:"last_error,omitempty"`
}

// SanitizedAuthCacheHealth 脱敏后的 /ops/auth-cache-invalidation/health。
type SanitizedAuthCacheHealth struct {
	OutboxRunning    bool   `json:"outbox_running"`
	OutboxPending    int64  `json:"outbox_pending"`
	OutboxFailures   uint64 `json:"outbox_failures"`
	OutboxOldestMS   int64  `json:"outbox_oldest_lag_ms"`
	OutboxHealthySLA int64  `json:"outbox_healthy_sla_ms"`
	OutboxLastError  string `json:"outbox_last_error,omitempty"`
	SubscriberOK     bool   `json:"subscriber_ok"`
	SubscriberError  string `json:"subscriber_error,omitempty"`
	Healthy          bool   `json:"healthy"`
}

// SanitizedAccountSummary 脱敏后的 /accounts 目录基础元数据（绝不含凭据、Token 或原始错误正文）。
type SanitizedAccountSummary struct {
	AccountID              int64      `json:"account_id"`
	Name                   string     `json:"name"`
	Platform               string     `json:"platform"`
	Type                   string     `json:"type"`
	Status                 string     `json:"status"`
	Schedulable            bool       `json:"schedulable"`
	Concurrency            int        `json:"concurrency"`
	Priority               int        `json:"priority"`
	GroupIDs               []int64    `json:"group_ids,omitempty"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	RateLimitResetAt       *time.Time `json:"rate_limit_reset_at,omitempty"`
	OverloadUntil          *time.Time `json:"overload_until,omitempty"`
	TempUnschedulableUntil *time.Time `json:"temp_unschedulable_until,omitempty"`
	ErrorSummary           string     `json:"error_summary,omitempty"`
}

// AccountsPagePayload 单页脱敏 /accounts 解析结果。
type AccountsPagePayload struct {
	Items    []SanitizedAccountSummary `json:"items"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
}

// ---------- P1-A 对外 HTTP API DTO ----------

// EventDeliveryView 单条告警事件发往单个通知渠道的投递明细。
type EventDeliveryView struct {
	ID              uint       `json:"id"`
	EventID         uint       `json:"event_id"`
	NotifyChannelID uint       `json:"notify_channel_id"`
	ChannelName     string     `json:"channel_name,omitempty"`
	ChannelType     string     `json:"channel_type,omitempty"`
	DeliveryKind    string     `json:"delivery_kind"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	NextRetryAt     *time.Time `json:"next_retry_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// EventItemView 单条站点运维状态跃迁事件视图。
type EventItemView struct {
	ID              uint                `json:"id"`
	InstanceID      uint                `json:"instance_id"`
	Severity        string              `json:"severity"`
	RuleKey         string              `json:"rule_key"`
	ResourceKey     string              `json:"resource_key"`
	RequiredDomains []string            `json:"required_domains"`
	State           string              `json:"state"`
	ConsecutiveHits int                 `json:"consecutive_hits"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary"`
	Detail          map[string]any      `json:"detail,omitempty"`
	FiredAt         time.Time           `json:"fired_at"`
	ResolvedAt      *time.Time          `json:"resolved_at,omitempty"`
	Deliveries      []EventDeliveryView `json:"deliveries"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// EventsResponse GET /api/site-ops/instances/:id/events 响应体。
type EventsResponse struct {
	InstanceID  uint            `json:"instance_id"`
	FiringCount int             `json:"firing_count"`
	Items       []EventItemView `json:"items"`
}

// AccountStatusItem 融合 /ops/account-availability 与 /accounts 元数据的脱敏账号状态行。
type AccountStatusItem struct {
	AccountID              int64      `json:"account_id"`
	Name                   string     `json:"name"`
	Platform               string     `json:"platform"`
	Type                   string     `json:"type,omitempty"`
	Status                 string     `json:"status,omitempty"`
	Schedulable            *bool      `json:"schedulable,omitempty"`
	OpsAvailable           bool       `json:"ops_available"`
	RateLimited            bool       `json:"rate_limited"`
	Overloaded             bool       `json:"overloaded"`
	HasError               bool       `json:"has_error"`
	ErrorSummary           string     `json:"error_summary,omitempty"`
	ConcUsed               *int64     `json:"conc_used,omitempty"`
	ConcMax                *int64     `json:"conc_max,omitempty"`
	ConcWaiting            *int64     `json:"conc_waiting,omitempty"`
	GroupIDs               []int64    `json:"group_ids,omitempty"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	RateLimitResetAt       *time.Time `json:"rate_limit_reset_at,omitempty"`
	OverloadUntil          *time.Time `json:"overload_until,omitempty"`
	TempUnschedulableUntil *time.Time `json:"temp_unschedulable_until,omitempty"`
}

// AccountsStatusResponse GET /api/site-ops/instances/:id/accounts?view=status 响应体。
type AccountsStatusResponse struct {
	InstanceID     uint                `json:"instance_id"`
	ObservedAt     *time.Time          `json:"observed_at,omitempty"`
	TrustState     FieldQualityState   `json:"trust_state"`
	TotalCount     int                 `json:"total_count"`
	AvailableCount int                 `json:"available_count"`
	RateLimitCount int                 `json:"rate_limit_count"`
	OverloadCount  int                 `json:"overload_count"`
	ErrorCount     int                 `json:"error_count"`
	Items          []AccountStatusItem `json:"items"`
}

// DailyReportMetrics 昨日 24h 同窗聚合运维指标（绝不对 1h SLA 求均值，缺失字段显式为 nil）。
type DailyReportMetrics struct {
	WindowStartUTC8      time.Time `json:"window_start_utc8"`
	WindowEndUTC8        time.Time `json:"window_end_utc8"`
	SampleCount          int       `json:"sample_count"`
	ReachableSampleCount int       `json:"reachable_sample_count"`
	SLAPercent24h        *float64  `json:"sla_percent_24h,omitempty"`
	SLANote              string    `json:"sla_note"`
	ReqTotal24h          *int64    `json:"req_total_24h,omitempty"`
	ErrTotal24h          *int64    `json:"err_total_24h,omitempty"`
	TTFTP95MS24h         *int      `json:"ttft_p95_ms_24h,omitempty"`
	DurationP95MS24h     *int      `json:"duration_p95_ms_24h,omitempty"`
	Latency24hNote       string    `json:"latency_24h_note"`
	PeakConcUsed         *int64    `json:"peak_conc_used,omitempty"`
	PeakConcMax          *int64    `json:"peak_conc_max,omitempty"`
	TotalSwitches1m      int64     `json:"total_switches_1m"`
	PeakSwitches1m       int64     `json:"peak_switches_1m"`
	CurrentAccountsTotal *int64    `json:"current_accounts_total,omitempty"`
	CurrentAccountsError *int64    `json:"current_accounts_error,omitempty"`
	ActiveFiringAlerts   int       `json:"active_firing_alerts"`
}

// DailyReportPreview POST /api/site-ops/instances/:id/report/preview 响应体。
type DailyReportPreview struct {
	InstanceID   uint               `json:"instance_id"`
	InstanceName string             `json:"instance_name"`
	DateUTC8     string             `json:"date_utc8"`
	GeneratedAt  time.Time          `json:"generated_at"`
	Subject      string             `json:"subject"`
	Body         string             `json:"body"`
	Metrics      DailyReportMetrics `json:"metrics"`
}

// DailyReportSendResult POST /api/site-ops/instances/:id/report/send 响应体。
type DailyReportSendResult struct {
	Preview        DailyReportPreview  `json:"preview"`
	MatchedTargets int                 `json:"matched_targets"`
	Deliveries     []EventDeliveryView `json:"deliveries"`
}

// ---------- Wave 2 内部协作接口（零破坏扩展 Wave 1 接口） ----------

// P1AClient 扩展上游拉取接口（由 W2-A client.go 实现）。
type P1AClient interface {
	FetchOpsAlertEvents(ctx context.Context, inst TargetEndpoint) ([]SanitizedOpsAlertEvent, UpstreamFetchMeta, error)
	FetchIngressRejectHealth(ctx context.Context, inst TargetEndpoint) (*SanitizedIngressRejectHealth, UpstreamFetchMeta, error)
	FetchAuthCacheHealth(ctx context.Context, inst TargetEndpoint) (*SanitizedAuthCacheHealth, UpstreamFetchMeta, error)
	FetchAccountsDirectory(ctx context.Context, inst TargetEndpoint) ([]SanitizedAccountSummary, UpstreamFetchMeta, error)
}

// EventStore P1-A 独立事件与投递持久化接口（由 W2-A backend/storage/site_ops.go 实现）。
type EventStore interface {
	ListActiveEvents(ctx context.Context, instanceID uint) ([]storage.SiteOpsEvent, error)
	UpsertEvent(ctx context.Context, evt *storage.SiteOpsEvent) error
	ListEvents(ctx context.Context, instanceID uint, state, severity string, limit int) ([]storage.SiteOpsEvent, error)
	CreateDelivery(ctx context.Context, d *storage.SiteOpsAlertDelivery) error
	HasActiveCooldown(ctx context.Context, instanceID, notifyChannelID uint, cooldownKey string, now time.Time) (bool, error)
	ListDeliveriesByEventIDs(ctx context.Context, eventIDs []uint) (map[uint][]storage.SiteOpsAlertDelivery, error)
	ListDueDeliveries(ctx context.Context, now time.Time, limit int) ([]storage.SiteOpsAlertDelivery, error)
	UpdateDelivery(ctx context.Context, d *storage.SiteOpsAlertDelivery) error
	DeleteEventsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error)
	DeleteDeliveriesBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error)
}

// DirectNotifier 专用直投通知接口（完全绕过旧 fanout 与 notification_cooldowns）。
type DirectNotifier interface {
	ListEnabledChannels() ([]storage.NotificationChannel, error)
	SendDirect(ctx context.Context, channelID uint, subject, body string) error
}

// Package siteops 提供自有 Sub2API 站点的多实例可观测、安全门禁、能力探测与容量分析。
package siteops

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/storage"
)

// 模块级错误码与哨兵错误。
const (
	ErrCodeAuthRequired     = "SITE_OPS_AUTH_REQUIRED"
	ErrCodeOriginNotAllowed = "SITE_OPS_ORIGIN_NOT_ALLOWED"
	ErrCodeRedirectBlocked  = "SITE_OPS_REDIRECT_BLOCKED"
	ErrCodeBudgetExceeded   = "SITE_OPS_BUDGET_EXCEEDED"
	ErrCodeQueueTimeout     = "SITE_OPS_QUEUE_TIMEOUT"
	ErrCodeRateLimited      = "SITE_OPS_RATE_LIMITED"
	ErrCodeUnauthorized     = "SITE_OPS_UNAUTHORIZED"
	ErrCodeForbidden        = "SITE_OPS_FORBIDDEN"
	ErrCodeOpsDisabled      = "SITE_OPS_DISABLED"
	ErrCodeUnsupportedRoute = "SITE_OPS_UNSUPPORTED_ROUTE"
	ErrCodeUpstreamError    = "SITE_OPS_UPSTREAM_ERROR"
)

var (
	ErrAuthRequired       = errors.New("site-ops requires auth.enabled=true (or explicit local-only read escape hatch)")
	ErrOriginNotAllowed   = errors.New("target origin or resolved IP is not in allowed siteOps origins/CIDRs")
	ErrRedirectBlocked    = errors.New("HTTP redirects are blocked for site-ops outbound requests")
	ErrBudgetExceeded     = errors.New("site-ops audit request budget exceeded")
	ErrQueueTimeout       = errors.New("site-ops outbound queue wait timeout")
	ErrInstanceNotFound   = errors.New("site-ops instance not found")
	ErrGenerationMismatch = errors.New("site-ops instance generation mismatch (stale worker write rejected)")
	ErrRateLimited        = errors.New("site-ops upstream rate limited (429)")
	ErrUnauthorized       = errors.New("site-ops upstream unauthorized (401)")
	ErrForbidden          = errors.New("site-ops upstream endpoint forbidden (403)")
	ErrOpsDisabled        = errors.New("site-ops target ops feature disabled")
	ErrUnsupportedRoute   = errors.New("site-ops target endpoint route unsupported (404)")
	ErrInvalidInput       = errors.New("invalid site-ops input")
)

// ReachabilityState 层 1：HTTP 可达性（仅反映 /health 进程可达，不代表 DB/Redis/Ops 健康）。
type ReachabilityState string

const (
	ReachabilityReachable   ReachabilityState = "reachable"
	ReachabilityUnreachable ReachabilityState = "unreachable"
	ReachabilityUnknown     ReachabilityState = "unknown"
)

// InstanceStatus 实例综合运行态。
type InstanceStatus string

const (
	StatusOnline       InstanceStatus = "online"
	StatusDegraded     InstanceStatus = "degraded"
	StatusOffline      InstanceStatus = "offline"
	StatusAuthError    InstanceStatus = "auth_error"
	StatusPausedNoAuth InstanceStatus = "paused_no_auth"
	StatusPending      InstanceStatus = "pending"
)

// CapabilityState 层 2：单个端点/操作的能力探测结果（可复探，不永久锁定）。
type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityForbidden   CapabilityState = "forbidden"
	CapabilityOpsDisabled CapabilityState = "ops_disabled"
	CapabilityUnsupported CapabilityState = "unsupported"
	CapabilityError       CapabilityState = "error"
	CapabilityUnknown     CapabilityState = "unknown"
)

// FieldQualityState 层 3：数据域级样本可信度。
type FieldQualityState string

const (
	QualityTrusted        FieldQualityState = "trusted"
	QualityUnverifiedZero FieldQualityState = "unverified_zero"
	QualityStale          FieldQualityState = "stale"
	QualityDisabled       FieldQualityState = "disabled"
	QualityUnauthorized   FieldQualityState = "unauthorized"
	QualityUnavailable    FieldQualityState = "unavailable"
)

// 端点键常量（精确到端点粒度）。
const (
	EndpointHealth          = "health"
	EndpointOpsSnapshotV2   = "ops.dashboard.snapshot_v2"
	EndpointOpsOverview     = "ops.dashboard.overview"
	EndpointOpsConcurrency  = "ops.concurrency"
	EndpointOpsAccountAvail = "ops.account_availability"
	EndpointGroupsAll       = "groups.all"
	EndpointSystemVersion   = "system.version"
)

// 数据域键常量（用于 field_quality 与后续同域恢复）。
const (
	DomainReachability        = "reachability"
	DomainOpsOverview         = "ops_overview"
	DomainInfraMetrics        = "infra_metrics"
	DomainConcurrency         = "concurrency"
	DomainAccountAvailability = "account_availability"
	DomainGroupCapacity       = "group_capacity"
	DomainSystemVersion       = "system_version"
)

const (
	KeyKindScoped = "scoped"
	KeyKindFull   = "full"

	SystemMetricsResolutionNote = "system_metrics 原始采集分辨率为 60s"
	SLADenominatorNote          = "基于 Sub2API 上游有效请求分母口径（具体排除项以目标实例 M0 核验为准）"
	OpsAvailabilityLabel        = "Ops 判定可用（不等同调度器 IsSchedulable，后者另校验到期与配额）"
	ConfiguredCapacityLabel     = "配置并发容量（账号跨组共享，各分组容量不可直接相加）"
)

// EndpointCapability 记录单个端点的探测态、回退路径与最近探测时间。
type EndpointCapability struct {
	State          CapabilityState `json:"state"`
	HTTPStatus     int             `json:"http_status,omitempty"`
	FallbackTarget string          `json:"fallback_target,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	CheckedAt      time.Time       `json:"checked_at"`
}

// CapabilitiesMap 按端点键记录实例能力矩阵。
type CapabilitiesMap map[string]EndpointCapability

// FieldQualityMap 按数据域记录本次快照可信度。
type FieldQualityMap map[string]FieldQualityState

// AuditBudgetUsage 实例级当日审计调用预算统计。
type AuditBudgetUsage struct {
	UsedToday     int        `json:"used_today"`
	DailyLimit    int        `json:"daily_limit"`
	UsedLastMin   int        `json:"used_last_min"`
	PerMinLimit   int        `json:"per_min_limit"`
	WindowDateUTC string     `json:"window_date_utc"`
	ResetAt       *time.Time `json:"reset_at,omitempty"`
}

// TargetEndpoint 出站请求所需的实例目标描述（明文 APIKey 仅存在于内存瞬态，绝不落日志/响应）。
type TargetEndpoint struct {
	InstanceID        uint
	BaseURL           string
	APIKey            string
	KeyKind           string
	CredGen           int64
	Generation        int64
	ProxyEnabled      bool
	AuditBudgetPerDay int
}

// HealthProbeResult L0 /health 探测结果（严禁携带 x-api-key）。
type HealthProbeResult struct {
	Reachable      bool      `json:"reachable"`
	HTTPStatus     int       `json:"http_status"`
	LatencyMS      int64     `json:"latency_ms"`
	CheckedAt      time.Time `json:"checked_at"`
	ErrorCode      string    `json:"error_code,omitempty"`
	SanitizedError string    `json:"sanitized_error,omitempty"`
}

// UpstreamFetchMeta 单次上游 Admin API 调用元信息。
type UpstreamFetchMeta struct {
	Endpoint       string         `json:"endpoint"`
	HTTPStatus     int            `json:"http_status"`
	ETag           string         `json:"etag,omitempty"`
	NotModified    bool           `json:"not_modified"`
	LatencyMS      int64          `json:"latency_ms"`
	ObservedAt     time.Time      `json:"observed_at"`
	VerifiedAt     time.Time      `json:"verified_at"`
	FallbackUsed   string         `json:"fallback_used,omitempty"`
	RetryAfter     *time.Duration `json:"retry_after,omitempty"`
	ErrorCode      string         `json:"error_code,omitempty"`
	SanitizedError string         `json:"sanitized_error,omitempty"`
}

// SanitizedJobHeartbeat 脱敏后的后台任务心跳。
type SanitizedJobHeartbeat struct {
	JobName       string     `json:"job_name"`
	Status        string     `json:"status"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	HasError      bool       `json:"has_error"`
	ErrorSummary  string     `json:"error_summary,omitempty"`
}

// SanitizedOpsOverview 经白名单投影后的 Ops 总览数据（来自 snapshot-v2 或 legacy overview）。
type SanitizedOpsOverview struct {
	OpsEnabled              *bool                   `json:"ops_enabled,omitempty"`
	HealthScore             *int                    `json:"health_score,omitempty"`
	UptimeSeconds           *int64                  `json:"uptime_seconds,omitempty"`
	CPUPercent              *float64                `json:"cpu_percent,omitempty"`
	MemPercent              *float64                `json:"mem_percent,omitempty"`
	MemUsedMB               *int64                  `json:"mem_used_mb,omitempty"`
	DBOK                    *bool                   `json:"db_ok,omitempty"`
	DBConnActive            *int                    `json:"db_conn_active,omitempty"`
	DBConnWaiting           *int                    `json:"db_conn_waiting,omitempty"`
	DBMaxOpenConns          *int                    `json:"db_max_open_conns,omitempty"`
	RedisOK                 *bool                   `json:"redis_ok,omitempty"`
	RedisConnTotal          *int                    `json:"redis_conn_total,omitempty"`
	RedisConnIdle           *int                    `json:"redis_conn_idle,omitempty"`
	RedisPoolSize           *int                    `json:"redis_pool_size,omitempty"`
	GoroutineCount          *int                    `json:"goroutine_count,omitempty"`
	QueueDepth              *int                    `json:"queue_depth,omitempty"`
	AccountSwitchCount1m    *int64                  `json:"account_switch_count_1m,omitempty"`
	SLAPercent1h            *float64                `json:"sla_percent_1h,omitempty"`
	ErrorRatePercent1h      *float64                `json:"error_rate_percent_1h,omitempty"`
	UpstreamErrorRatePct1h  *float64                `json:"upstream_error_rate_percent_1h,omitempty"`
	ReqTotal1h              *int64                  `json:"req_total_1h,omitempty"`
	ErrTotal1h              *int64                  `json:"err_total_1h,omitempty"`
	Upstream4291h           *int64                  `json:"upstream_429_1h,omitempty"`
	Upstream5291h           *int64                  `json:"upstream_529_1h,omitempty"`
	UpstreamErrExcl4295291h *int64                  `json:"upstream_err_excl_429_529_1h,omitempty"`
	QPSCurrent              *float64                `json:"qps_current,omitempty"`
	QPSPeak                 *float64                `json:"qps_peak,omitempty"`
	TPSCurrent              *float64                `json:"tps_current,omitempty"`
	RPM5m                   *int64                  `json:"rpm_5m,omitempty"`
	TPM5m                   *int64                  `json:"tpm_5m,omitempty"`
	TTFTP50MS               *int                    `json:"ttft_p50_ms,omitempty"`
	TTFTP95MS               *int                    `json:"ttft_p95_ms,omitempty"`
	DurationP95MS           *int                    `json:"duration_p95_ms,omitempty"`
	OutputTPSP50            *float64                `json:"output_tps_p50,omitempty"`
	SystemMetricsAt         *time.Time              `json:"system_metrics_at,omitempty"`
	JobHeartbeats           []SanitizedJobHeartbeat `json:"job_heartbeats,omitempty"`
}

// SanitizedAccountConcurrency 单个账号并发统计（按 account_id 去重汇总用）。
type SanitizedAccountConcurrency struct {
	AccountID      int64  `json:"account_id"`
	Platform       string `json:"platform,omitempty"`
	CurrentInUse   int64  `json:"current_in_use"`
	MaxCapacity    int64  `json:"max_capacity"`
	WaitingInQueue int64  `json:"waiting_in_queue"`
}

// SanitizedGroupConcurrency 服务端聚合的分组配置并发统计。
type SanitizedGroupConcurrency struct {
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name,omitempty"`
	Platform       string `json:"platform,omitempty"`
	CurrentInUse   int64  `json:"current_in_use"`
	MaxCapacity    int64  `json:"max_capacity"`
	WaitingInQueue int64  `json:"waiting_in_queue"`
}

// SanitizedConcurrencyPayload 经脱敏投影后的 /ops/concurrency 数据。
type SanitizedConcurrencyPayload struct {
	Enabled   *bool                                 `json:"enabled,omitempty"`
	Timestamp *time.Time                            `json:"timestamp,omitempty"`
	Accounts  map[int64]SanitizedAccountConcurrency `json:"accounts"`
	Groups    map[int64]SanitizedGroupConcurrency   `json:"groups"`
	RawEmpty  bool                                  `json:"raw_empty"`
}

// SanitizedAccountAvailability 单个账号 Ops 可用度（剥离原始 error_message，仅留枚举与脱敏短摘要）。
type SanitizedAccountAvailability struct {
	AccountID    int64  `json:"account_id"`
	Platform     string `json:"platform,omitempty"`
	Available    bool   `json:"available"`
	RateLimited  bool   `json:"rate_limited"`
	Overloaded   bool   `json:"overloaded"`
	HasError     bool   `json:"has_error"`
	ErrorSummary string `json:"error_summary,omitempty"`
}

// SanitizedGroupAvailability 服务端聚合的分组 Ops 可用度统计。
type SanitizedGroupAvailability struct {
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name,omitempty"`
	Platform       string `json:"platform,omitempty"`
	TotalCount     int64  `json:"total_count"`
	AvailableCount int64  `json:"available_count"`
	RateLimitCount int64  `json:"rate_limit_count"`
	OverloadCount  int64  `json:"overload_count"`
	ErrorCount     int64  `json:"error_count"`
}

// SanitizedAvailabilityPayload 经脱敏投影后的 /ops/account-availability 数据。
type SanitizedAvailabilityPayload struct {
	Enabled   *bool                                  `json:"enabled,omitempty"`
	Timestamp *time.Time                             `json:"timestamp,omitempty"`
	Accounts  map[int64]SanitizedAccountAvailability `json:"accounts"`
	Groups    map[int64]SanitizedGroupAvailability   `json:"groups"`
	RawEmpty  bool                                   `json:"raw_empty"`
}

// SanitizedGroupInfo 经白名单投影后的 /groups/all 元数据。
type SanitizedGroupInfo struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status,omitempty"`
}

// SanitizedVersionInfo 经白名单投影后的 /system/version。
type SanitizedVersionInfo struct {
	Version string `json:"version"`
}

// SnapshotDetailPayload 存于 SiteOpsSnapshot.DetailJSON 中的脱敏辅助明细。
type SnapshotDetailPayload struct {
	JobHeartbeats        []SanitizedJobHeartbeat `json:"job_heartbeats,omitempty"`
	SystemMetricsAt      *time.Time              `json:"system_metrics_at,omitempty"`
	PlatformBreakdown    []PlatformPoolSummary   `json:"platform_breakdown,omitempty"`
	FallbackUsed         string                  `json:"fallback_used,omitempty"`
	ConcTrustReason      string                  `json:"conc_trust_reason,omitempty"`
	AvailTrustReason     string                  `json:"avail_trust_reason,omitempty"`
	EstimatedSwitchPct1m *float64                `json:"estimated_switch_pct_1m,omitempty"`
}

// PlatformPoolSummary 按 (instance_id, account_id) 去重后的平台维汇总。
type PlatformPoolSummary struct {
	Platform             string   `json:"platform"`
	AccountsTotal        int64    `json:"accounts_total"`
	AccountsOpsAvailable int64    `json:"accounts_ops_available"`
	AccountsRateLimited  int64    `json:"accounts_rate_limited"`
	AccountsOverloaded   int64    `json:"accounts_overloaded"`
	AccountsError        int64    `json:"accounts_error"`
	ConcUsed             *int64   `json:"conc_used,omitempty"`
	ConcMax              *int64   `json:"conc_max,omitempty"`
	ConcWaiting          *int64   `json:"conc_waiting,omitempty"`
	LoadPercent          *float64 `json:"load_percent,omitempty"`
}

// ---------- 对外 HTTP API 请求/响应 DTO（与 frontend/lib/site-ops-types.ts 一一对应） ----------

// CreateInstanceInput POST /api/site-ops/instances 请求体。
type CreateInstanceInput struct {
	Name                string `json:"name"`
	Slug                string `json:"slug,omitempty"`
	BaseURL             string `json:"base_url"`
	APIKey              string `json:"api_key"`
	KeyKind             string `json:"key_kind,omitempty"`
	ProxyEnabled        bool   `json:"proxy_enabled"`
	Enabled             *bool  `json:"enabled,omitempty"`
	SortOrder           int    `json:"sort_order"`
	PollIntervalSeconds int    `json:"poll_interval_seconds,omitempty"`
	AuditBudgetPerDay   int    `json:"audit_budget_per_day,omitempty"`
}

// UpdateInstanceInput PUT /api/site-ops/instances/:id 请求体（APIKey 留空保留旧密文）。
type UpdateInstanceInput struct {
	Name                *string `json:"name,omitempty"`
	Slug                *string `json:"slug,omitempty"`
	BaseURL             *string `json:"base_url,omitempty"`
	APIKey              *string `json:"api_key,omitempty"`
	KeyKind             *string `json:"key_kind,omitempty"`
	ProxyEnabled        *bool   `json:"proxy_enabled,omitempty"`
	Enabled             *bool   `json:"enabled,omitempty"`
	SortOrder           *int    `json:"sort_order,omitempty"`
	PollIntervalSeconds *int    `json:"poll_interval_seconds,omitempty"`
	AuditBudgetPerDay   *int    `json:"audit_budget_per_day,omitempty"`
}

// InstanceView 返回给前端的脱敏实例视图（永不含密文或明文 APIKey）。
type InstanceView struct {
	ID                  uint              `json:"id"`
	Name                string            `json:"name"`
	Slug                string            `json:"slug"`
	BaseURL             string            `json:"base_url"`
	APIKeySet           bool              `json:"api_key_set"`
	APIKeyHint          string            `json:"api_key_hint"`
	KeyKind             string            `json:"key_kind"`
	CredGen             int64             `json:"cred_gen"`
	Generation          int64             `json:"generation"`
	ProxyEnabled        bool              `json:"proxy_enabled"`
	Enabled             bool              `json:"enabled"`
	SortOrder           int               `json:"sort_order"`
	PollIntervalSeconds int               `json:"poll_interval_seconds"`
	AuditBudgetPerDay   int               `json:"audit_budget_per_day"`
	AuditBudget         AuditBudgetUsage  `json:"audit_budget"`
	Capabilities        CapabilitiesMap   `json:"capabilities"`
	LastReachability    ReachabilityState `json:"last_reachability"`
	LastStatus          InstanceStatus    `json:"last_status"`
	LastHealthScore     *int              `json:"last_health_score,omitempty"`
	LastVersion         string            `json:"last_version"`
	LastObservedAt      *time.Time        `json:"last_observed_at,omitempty"`
	LastVerifiedAt      *time.Time        `json:"last_verified_at,omitempty"`
	BackoffUntil        *time.Time        `json:"backoff_until,omitempty"`
	LastErrorCode       string            `json:"last_error_code,omitempty"`
	LastErrorSummary    string            `json:"last_error_summary,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

// ProbeReport POST /api/site-ops/instances/:id/probe 响应体。
type ProbeReport struct {
	InstanceID   uint              `json:"instance_id"`
	Reachability ReachabilityState `json:"reachability"`
	Health       HealthProbeResult `json:"health"`
	Version      string            `json:"version,omitempty"`
	Capabilities CapabilitiesMap   `json:"capabilities"`
	ProbedAt     time.Time         `json:"probed_at"`
}

// InstanceSummaryItem 多实例顶部对比条的单项摘要。
type InstanceSummaryItem struct {
	ID                   uint              `json:"id"`
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug"`
	Enabled              bool              `json:"enabled"`
	LastReachability     ReachabilityState `json:"last_reachability"`
	LastStatus           InstanceStatus    `json:"last_status"`
	HealthScore          *int              `json:"health_score,omitempty"`
	SLAPercent1h         *float64          `json:"sla_percent_1h,omitempty"`
	QPSCurrent           *float64          `json:"qps_current,omitempty"`
	ConcUsed             *int64            `json:"conc_used,omitempty"`
	ConcMax              *int64            `json:"conc_max,omitempty"`
	ConcTrustState       FieldQualityState `json:"conc_trust_state"`
	AccountsTotal        *int64            `json:"accounts_total,omitempty"`
	AccountsOpsAvailable *int64            `json:"accounts_ops_available,omitempty"`
	AccountsError        *int64            `json:"accounts_error,omitempty"`
	LastVersion          string            `json:"last_version"`
	LastObservedAt       *time.Time        `json:"last_observed_at,omitempty"`
	LastVerifiedAt       *time.Time        `json:"last_verified_at,omitempty"`
	AuditBudget          AuditBudgetUsage  `json:"audit_budget"`
	LastErrorCode        string            `json:"last_error_code,omitempty"`
	LastErrorSummary     string            `json:"last_error_summary,omitempty"`
}

// SummaryResponse GET /api/site-ops/summary 响应体。
type SummaryResponse struct {
	PollingActive bool                  `json:"polling_active"`
	AuthEnabled   bool                  `json:"auth_enabled"`
	TotalCount    int                   `json:"total_count"`
	OnlineCount   int                   `json:"online_count"`
	DegradedCount int                   `json:"degraded_count"`
	OfflineCount  int                   `json:"offline_count"`
	Instances     []InstanceSummaryItem `json:"instances"`
}

// SnapshotView 返回给前端的标准化快照视图。
type SnapshotView struct {
	ObservedAt              time.Time               `json:"observed_at"`
	VerifiedAt              time.Time               `json:"verified_at"`
	Reachable               bool                    `json:"reachable"`
	ProbeLatencyMS          int64                   `json:"probe_latency_ms"`
	FieldQuality            FieldQualityMap         `json:"field_quality"`
	HealthScore             *int                    `json:"health_score,omitempty"`
	UptimeSeconds           *int64                  `json:"uptime_seconds,omitempty"`
	CPUPercent              *float64                `json:"cpu_percent,omitempty"`
	MemPercent              *float64                `json:"mem_percent,omitempty"`
	MemUsedMB               *int64                  `json:"mem_used_mb,omitempty"`
	DBOK                    *bool                   `json:"db_ok,omitempty"`
	DBConnActive            *int                    `json:"db_conn_active,omitempty"`
	DBConnWaiting           *int                    `json:"db_conn_waiting,omitempty"`
	DBMaxOpenConns          *int                    `json:"db_max_open_conns,omitempty"`
	RedisOK                 *bool                   `json:"redis_ok,omitempty"`
	RedisConnTotal          *int                    `json:"redis_conn_total,omitempty"`
	RedisConnIdle           *int                    `json:"redis_conn_idle,omitempty"`
	RedisPoolSize           *int                    `json:"redis_pool_size,omitempty"`
	GoroutineCount          *int                    `json:"goroutine_count,omitempty"`
	QueueDepth              *int                    `json:"queue_depth,omitempty"`
	AccountSwitchCount1m    *int64                  `json:"account_switch_count_1m,omitempty"`
	EstimatedSwitchPct1m    *float64                `json:"estimated_switch_pct_1m,omitempty"`
	SLAPercent1h            *float64                `json:"sla_percent_1h,omitempty"`
	ErrorRatePercent1h      *float64                `json:"error_rate_percent_1h,omitempty"`
	UpstreamErrorRatePct1h  *float64                `json:"upstream_error_rate_percent_1h,omitempty"`
	ReqTotal1h              *int64                  `json:"req_total_1h,omitempty"`
	ErrTotal1h              *int64                  `json:"err_total_1h,omitempty"`
	Upstream4291h           *int64                  `json:"upstream_429_1h,omitempty"`
	Upstream5291h           *int64                  `json:"upstream_529_1h,omitempty"`
	UpstreamErrExcl4295291h *int64                  `json:"upstream_err_excl_429_529_1h,omitempty"`
	QPSCurrent              *float64                `json:"qps_current,omitempty"`
	QPSPeak                 *float64                `json:"qps_peak,omitempty"`
	TPSCurrent              *float64                `json:"tps_current,omitempty"`
	RPM5m                   *int64                  `json:"rpm_5m,omitempty"`
	TPM5m                   *int64                  `json:"tpm_5m,omitempty"`
	TTFTP50MS               *int                    `json:"ttft_p50_ms,omitempty"`
	TTFTP95MS               *int                    `json:"ttft_p95_ms,omitempty"`
	DurationP95MS           *int                    `json:"duration_p95_ms,omitempty"`
	OutputTPSP50            *float64                `json:"output_tps_p50,omitempty"`
	ConcUsed                *int64                  `json:"conc_used,omitempty"`
	ConcMax                 *int64                  `json:"conc_max,omitempty"`
	ConcWaiting             *int64                  `json:"conc_waiting,omitempty"`
	ConcLoadPercent         *float64                `json:"conc_load_percent,omitempty"`
	ConcTrustState          FieldQualityState       `json:"conc_trust_state"`
	ConcTrustReason         string                  `json:"conc_trust_reason,omitempty"`
	AccountsTotal           *int64                  `json:"accounts_total,omitempty"`
	AccountsOpsAvailable    *int64                  `json:"accounts_ops_available,omitempty"`
	AccountsRateLimited     *int64                  `json:"accounts_rate_limited,omitempty"`
	AccountsOverloaded      *int64                  `json:"accounts_overloaded,omitempty"`
	AccountsError           *int64                  `json:"accounts_error,omitempty"`
	SystemMetricsAt         *time.Time              `json:"system_metrics_at,omitempty"`
	JobHeartbeats           []SanitizedJobHeartbeat `json:"job_heartbeats,omitempty"`
	PlatformBreakdown       []PlatformPoolSummary   `json:"platform_breakdown,omitempty"`
}

// OverviewResponse GET /api/site-ops/instances/:id/overview 响应体。
type OverviewResponse struct {
	Instance                 InstanceView  `json:"instance"`
	Latest                   *SnapshotView `json:"latest,omitempty"`
	SystemMetricsResolution  string        `json:"system_metrics_resolution"`
	SLADenominatorNote       string        `json:"sla_denominator_note"`
	OpsAvailabilityLabel     string        `json:"ops_availability_label"`
	ConfiguredCapacityLabel  string        `json:"configured_capacity_label"`
}

// TrendPoint GET /api/site-ops/instances/:id/trends 的单个时序点。
type TrendPoint struct {
	ObservedAt             time.Time         `json:"observed_at"`
	VerifiedAt             time.Time         `json:"verified_at"`
	Reachable              bool              `json:"reachable"`
	ProbeLatencyMS         int64             `json:"probe_latency_ms"`
	HealthScore            *int              `json:"health_score,omitempty"`
	SLAPercent1h           *float64          `json:"sla_percent_1h,omitempty"`
	ErrorRatePercent1h     *float64          `json:"error_rate_percent_1h,omitempty"`
	UpstreamErrorRatePct1h *float64          `json:"upstream_error_rate_percent_1h,omitempty"`
	AccountSwitchCount1m   *int64            `json:"account_switch_count_1m,omitempty"`
	QPSCurrent             *float64          `json:"qps_current,omitempty"`
	TTFTP95MS              *int              `json:"ttft_p95_ms,omitempty"`
	DurationP95MS          *int              `json:"duration_p95_ms,omitempty"`
	CPUPercent             *float64          `json:"cpu_percent,omitempty"`
	MemPercent             *float64          `json:"mem_percent,omitempty"`
	ConcUsed               *int64            `json:"conc_used,omitempty"`
	ConcMax                *int64            `json:"conc_max,omitempty"`
	ConcTrustState         FieldQualityState `json:"conc_trust_state"`
	AccountsTotal          *int64            `json:"accounts_total,omitempty"`
	AccountsOpsAvailable   *int64            `json:"accounts_ops_available,omitempty"`
}

// TrendsResponse GET /api/site-ops/instances/:id/trends 响应体。
type TrendsResponse struct {
	InstanceID uint         `json:"instance_id"`
	Range      string       `json:"range"`
	Points     []TrendPoint `json:"points"`
}

// GroupCapacityItem 单个分组的去重配置容量与 Ops 可用度（P0-B）。
type GroupCapacityItem struct {
	GroupID              int64             `json:"group_id"`
	GroupName            string            `json:"group_name"`
	Platform             string            `json:"platform"`
	ObservedAt           time.Time         `json:"observed_at"`
	TrustState           FieldQualityState `json:"trust_state"`
	TrustReason          string            `json:"trust_reason,omitempty"`
	AccountsTotal        int64             `json:"accounts_total"`
	AccountsOpsAvailable int64             `json:"accounts_ops_available"`
	AccountsRateLimited  int64             `json:"accounts_rate_limited"`
	AccountsOverloaded   int64             `json:"accounts_overloaded"`
	AccountsError        int64             `json:"accounts_error"`
	SinglePointRisk      bool              `json:"single_point_risk"`
	ConcUsed             *int64            `json:"conc_used,omitempty"`
	ConcMax              *int64            `json:"conc_max,omitempty"`
	ConcWaiting          *int64            `json:"conc_waiting,omitempty"`
	LoadPercent          *float64          `json:"load_percent,omitempty"`
}

// GroupsResponse GET /api/site-ops/instances/:id/groups 响应体。
type GroupsResponse struct {
	InstanceID              uint                  `json:"instance_id"`
	ObservedAt              *time.Time            `json:"observed_at,omitempty"`
	TrustState              FieldQualityState     `json:"trust_state"`
	TrustReason             string                `json:"trust_reason,omitempty"`
	OpsAvailabilityLabel    string                `json:"ops_availability_label"`
	ConfiguredCapacityLabel string                `json:"configured_capacity_label"`
	PlatformBreakdown       []PlatformPoolSummary `json:"platform_breakdown"`
	Groups                  []GroupCapacityItem   `json:"groups"`
}

// RefreshResult POST /api/site-ops/instances/:id/refresh 响应体。
type RefreshResult struct {
	InstanceID uint          `json:"instance_id"`
	Snapshot   *SnapshotView `json:"snapshot,omitempty"`
	Status     InstanceStatus `json:"status"`
	Message    string        `json:"message,omitempty"`
}

// ---------- 包内模块协作接口（确保 W1-A 与 W1-B 零冲突并行开发） ----------

// OutboundValidator 出站 Origin/CIDR 门禁与 Dial 复检接口（由 W1-A guard.go 实现）。
type OutboundValidator interface {
	ValidateBaseURL(rawURL string) (normalizedURL string, err error)
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	UpdatePolicy(allowedOrigins, allowedCIDRs []string)
}

// UpstreamClient Sub2API Admin API 客户端接口（由 W1-A client.go 实现）。
type UpstreamClient interface {
	ProbeHealth(ctx context.Context, inst TargetEndpoint) (*HealthProbeResult, error)
	FetchOpsSnapshot(ctx context.Context, inst TargetEndpoint, caps CapabilitiesMap) (*SanitizedOpsOverview, UpstreamFetchMeta, error)
	FetchConcurrency(ctx context.Context, inst TargetEndpoint) (*SanitizedConcurrencyPayload, UpstreamFetchMeta, error)
	FetchAccountAvailability(ctx context.Context, inst TargetEndpoint) (*SanitizedAvailabilityPayload, UpstreamFetchMeta, error)
	FetchGroupsAll(ctx context.Context, inst TargetEndpoint) ([]SanitizedGroupInfo, UpstreamFetchMeta, error)
	FetchSystemVersion(ctx context.Context, inst TargetEndpoint) (*SanitizedVersionInfo, UpstreamFetchMeta, error)
	BudgetUsage(instanceID uint, dailyLimit int) AuditBudgetUsage
	ResetInstanceState(instanceID uint)
	UpdateRuntimeConfig(siteCfg config.SiteOpsConfig, proxyCfg config.ProxyConfig, upstreamCfg config.UpstreamConfig)
}

// Prober 能力探测器接口（由 W1-A probe.go 实现）。
type Prober interface {
	ProbeInstance(ctx context.Context, inst TargetEndpoint, existing CapabilitiesMap) (*ProbeReport, error)
}

// Store 持久化仓储接口（由 W1-B backend/storage/site_ops.go 实现，供 poller/service 与 scheduler 留存使用）。
type Store interface {
	ListInstances(ctx context.Context) ([]storage.SiteOpsInstance, error)
	GetInstance(ctx context.Context, id uint) (*storage.SiteOpsInstance, error)
	CreateInstance(ctx context.Context, inst *storage.SiteOpsInstance) error
	UpdateInstance(ctx context.Context, inst *storage.SiteOpsInstance) error
	DeleteInstance(ctx context.Context, id uint) error
	// CommitCollection 原子提交一轮采集结果：事务内校验 instance_id 仍存在且 generation 匹配，
	// 防止换 Key / 改 URL / 删除实例后旧 worker 迟到响应写回。
	CommitCollection(ctx context.Context, instanceID uint, expectedGen int64, statusUpdate InstanceRuntimePatch, snap *storage.SiteOpsSnapshot, groups []storage.SiteOpsGroupSnapshot) error
	// TouchSnapshotVerified 命中 304 时仅更新最近快照与实例的 verified_at，保留原 observed_at。
	TouchSnapshotVerified(ctx context.Context, instanceID uint, expectedGen int64, verifiedAt time.Time, statusUpdate InstanceRuntimePatch) error
	GetLatestSnapshot(ctx context.Context, instanceID uint) (*storage.SiteOpsSnapshot, error)
	GetLatestSnapshotsByInstances(ctx context.Context, instanceIDs []uint) (map[uint]storage.SiteOpsSnapshot, error)
	ListSnapshotsSince(ctx context.Context, instanceID uint, since time.Time, limit int) ([]storage.SiteOpsSnapshot, error)
	GetLatestGroupSnapshots(ctx context.Context, instanceID uint) ([]storage.SiteOpsGroupSnapshot, error)
	DeleteSnapshotsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error)
	DeleteGroupSnapshotsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error)
}

// InstanceRuntimePatch 采集完成后回写实例状态字段的原子补丁（不含凭据或配置字段）。
type InstanceRuntimePatch struct {
	CapabilitiesJSON *string
	LastReachability *string
	LastStatus       *string
	LastHealthScore  **int
	LastVersion      *string
	LastObservedAt   *time.Time
	LastVerifiedAt   *time.Time
	BackoffUntil     **time.Time
	LastErrorCode    *string
	LastErrorSummary *string
}

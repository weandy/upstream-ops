package siteops

import (
	"context"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// Wave 3 (P1-B: M4 被动号池周期预测 + M5 单号成本效率) 新增端点、数据域与告警规则常量。
const (
	EndpointGroupsCapacitySummary   = "groups.capacity_summary"
	EndpointAccountsUsageBatch      = "accounts.usage.batch"
	EndpointAccountsUsagePassive    = "accounts.usage.passive"
	EndpointAccountsStats           = "accounts.stats"
	EndpointAccountsTodayStatsBatch = "accounts.today_stats.batch"

	DomainPoolForecast      = "pool_forecast"
	DomainAccountEfficiency = "account_efficiency"

	RulePoolCannotSustainReset = "pool_cannot_sustain_reset"

	PassiveUsageBatchMaxSize = 80
	PassiveUsageBatchMinGap  = 500 * time.Millisecond
)

// ForecastStatus 窗口周期预测分级状态（严格区分 will_exhaust 与高危告警 will_exhaust_high）。
type ForecastStatus string

const (
	ForecastStatusSafe             ForecastStatus = "safe"
	ForecastStatusTight            ForecastStatus = "tight"
	ForecastStatusWillExhaust      ForecastStatus = "will_exhaust"
	ForecastStatusWillExhaustHigh  ForecastStatus = "will_exhaust_high"
	ForecastStatusExhausted        ForecastStatus = "exhausted"
	ForecastStatusIdle             ForecastStatus = "idle"
	ForecastStatusInsufficientData ForecastStatus = "insufficient_data"
)

// ForecastConfidence 预测置信度。
type ForecastConfidence string

const (
	ConfidenceHigh   ForecastConfidence = "high"
	ConfidenceMedium ForecastConfidence = "medium"
	ConfidenceLow    ForecastConfidence = "low"
)

// PassiveSupportStatus M4 被动只读证明矩阵判定结果。
type PassiveSupportStatus string

const (
	// PassiveSupportProven 经 M0/源码实证零上游主动请求、零清错副作用，允许后台周期拉取。
	PassiveSupportProven PassiveSupportStatus = "passive_proven"
	// PassiveSupportUnverified 未证实纯被动，P1-B 默认禁止后台周期调用 usage/batch。
	PassiveSupportUnverified PassiveSupportStatus = "passive_unverified"
	// PassiveSupportExcludedPayg 按量 API Key / Upstream 转发号，无订阅重置周期，永久排除出周期预测分母。
	PassiveSupportExcludedPayg PassiveSupportStatus = "excluded_payg"
)

// EvaluatePassiveSupport 按平台与账号类型执行 M4 被动只读准入判定（对齐 Sub2API v0.2.24 源码）。
func EvaluatePassiveSupport(platform, accountType string) (PassiveSupportStatus, string) {
	p := strings.ToLower(strings.TrimSpace(platform))
	t := strings.ToLower(strings.TrimSpace(accountType))
	switch t {
	case "apikey", "api_key", "upstream":
		return PassiveSupportExcludedPayg, "按量 API Key / Upstream 转发号无订阅重置周期，永久排除出号池周期预测分母（仅参与单号效率）"
	}
	if p == "anthropic" && (t == "oauth" || t == "setup-token" || t == "setup_token") {
		return PassiveSupportProven, "Anthropic OAuth / SetupToken 经 Sub2API v0.2.24 源码核验走纯被动路径（零上游主动探测与写副作用）"
	}
	return PassiveSupportUnverified, "该平台/账号类型未通过纯被动无副作用证明，P1-B 默认不开启后台 usage/batch 自动拉取"
}

// WindowDurationForKey 返回已知窗口键的标准周期秒数；未知窗口返回 0（前置准入拒绝）。
func WindowDurationForKey(windowKey string) int64 {
	switch strings.ToLower(strings.TrimSpace(windowKey)) {
	case "five_hour", "5h":
		return 5 * 3600
	case "daily", "24h", "gemini_shared_daily", "gemini_pro_daily", "gemini_flash_daily":
		return 24 * 3600
	case "seven_day", "seven_day_sonnet", "seven_day_fable", "7d", "weekly":
		return 7 * 24 * 3600
	case "thirty_day", "30d", "monthly":
		return 30 * 24 * 3600
	default:
		return 0
	}
}

// ---------- 上游脱敏载荷结构（由 W3-A sanitizer.go / client.go 产出） ----------

// SanitizedGroupCapacitySummary 脱敏后的 /api/v1/admin/groups/capacity-summary 单组记录。
type SanitizedGroupCapacitySummary struct {
	GroupID         int64 `json:"group_id"`
	ConcurrencyUsed int64 `json:"concurrency_used"`
	ConcurrencyMax  int64 `json:"concurrency_max"`
	SessionsUsed    int64 `json:"sessions_used"`
	SessionsMax     int64 `json:"sessions_max"`
	RPMUsed         int64 `json:"rpm_used"`
	RPMMax          int64 `json:"rpm_max"`
}

// SanitizedWindowStats 窗口期费用与请求统计（Sub2API 三口径完整保留）。
type SanitizedWindowStats struct {
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	Cost         float64 `json:"cost"`
	StandardCost float64 `json:"standard_cost"`
	UserCost     float64 `json:"user_cost"`
}

// SanitizedUsageWindow 单个周期窗口的脱敏利用率与重置信息。
type SanitizedUsageWindow struct {
	WindowKey          string                `json:"window_key"`
	WindowDurationSec  int64                 `json:"window_duration_sec"`
	Utilization        float64               `json:"utilization"`
	UtilizationUnknown bool                  `json:"utilization_unknown"`
	ResetsAt           *time.Time            `json:"resets_at,omitempty"`
	RemainingSeconds   int64                 `json:"remaining_seconds"`
	WindowStats        *SanitizedWindowStats `json:"window_stats,omitempty"`
}

// SanitizedAccountUsage 单账号经脱敏投影后的被动用量快照。
type SanitizedAccountUsage struct {
	AccountID  int64                  `json:"account_id"`
	Source     string                 `json:"source"`
	UpdatedAt  *time.Time             `json:"updated_at,omitempty"`
	Windows    []SanitizedUsageWindow `json:"windows"`
}

// SanitizedBatchUsageResult POST /api/v1/admin/accounts/usage/batch 脱敏结果。
type SanitizedBatchUsageResult struct {
	Usage  map[int64]SanitizedAccountUsage `json:"usage"`
	Errors map[int64]string                `json:"errors,omitempty"`
}

// SanitizedAccountStats GET /api/v1/admin/accounts/:id/stats?days=30 脱敏结果。
// 明确区分 Sub2API 三口径：StandardCost（官方标价）、Cost（账号折算成本）、UserCost（用户账面扣费）。
type SanitizedAccountStats struct {
	AccountID         int64   `json:"account_id"`
	Days              int     `json:"days"`
	ActualDaysUsed    int     `json:"actual_days_used"`
	TotalRequests     int64   `json:"total_requests"`
	TotalTokens       int64   `json:"total_tokens"`
	TotalCost         float64 `json:"total_cost"`
	TotalStandardCost float64 `json:"total_standard_cost"`
	TotalUserCost     float64 `json:"total_user_cost"`
	TodayDate         string  `json:"today_date,omitempty"`
	TodayRequests     int64   `json:"today_requests"`
	TodayTokens       int64   `json:"today_tokens"`
	TodayCost         float64 `json:"today_cost"`
	TodayStandardCost float64 `json:"today_standard_cost"`
	TodayUserCost     float64 `json:"today_user_cost"`
}

// ---------- Wave 3 (P1-B) 对外 HTTP API DTO ----------

// WindowForecastView 单账号单个周期窗口的燃烧率与续航预测视图。
type WindowForecastView struct {
	WindowKey          string             `json:"window_key"`
	Source             string             `json:"source"`
	WindowDurationSec  int64              `json:"window_duration_sec"`
	Utilization        float64            `json:"utilization"`
	ResetsAt           *time.Time         `json:"resets_at,omitempty"`
	RemainingSeconds   int64              `json:"remaining_seconds"`
	UpstreamObservedAt time.Time          `json:"upstream_observed_at"`
	BurnRatePerHour    *float64           `json:"burn_rate_per_hour"`
	ETASeconds         *int64             `json:"eta_seconds"`
	PredictedEndUtil   *float64           `json:"predicted_end_util"`
	MarginHours        *float64           `json:"margin_hours"`
	ForecastStatus     ForecastStatus     `json:"forecast_status"`
	Confidence         ForecastConfidence `json:"confidence"`
	Note               string             `json:"note,omitempty"`
}

// AccountForecastView 单个账号的被动周期预测汇总（按最早阻塞窗口收敛）。
type AccountForecastView struct {
	AccountID             int64                `json:"account_id"`
	Name                  string               `json:"name"`
	Platform              string               `json:"platform"`
	AccountType           string               `json:"account_type"`
	GroupIDs              []int64              `json:"group_ids,omitempty"`
	PassiveSupport        PassiveSupportStatus `json:"passive_support"`
	PassiveSupportReason  string               `json:"passive_support_reason,omitempty"`
	ExpiresAt             *time.Time           `json:"expires_at,omitempty"`
	EarliestBlockingKey   string               `json:"earliest_blocking_window,omitempty"`
	OverallStatus         ForecastStatus       `json:"overall_status"`
	OverallConfidence     ForecastConfidence   `json:"overall_confidence"`
	EarliestETASeconds    *int64               `json:"earliest_eta_seconds"`
	EarliestMarginHours   *float64             `json:"earliest_margin_hours"`
	Windows               []WindowForecastView `json:"windows"`
}

// GroupForecastRiskView 分组级号池能否撑到重置的风险汇总。
type GroupForecastRiskView struct {
	GroupID                int64    `json:"group_id"`
	GroupName              string   `json:"group_name"`
	Platform               string   `json:"platform"`
	ProvenAccounts         int      `json:"proven_accounts"`
	UnverifiedAccounts     int      `json:"unverified_accounts"`
	ExcludedPaygAccounts   int      `json:"excluded_payg_accounts"`
	ExhaustedCount         int      `json:"exhausted_count"`
	WillExhaustHighCount   int      `json:"will_exhaust_high_count"`
	WillExhaustCount       int      `json:"will_exhaust_count"`
	TightCount             int      `json:"tight_count"`
	SafeOrIdleCount        int      `json:"safe_or_idle_count"`
	InsufficientDataCount  int      `json:"insufficient_data_count"`
	HighRiskRatioPct       *float64 `json:"high_risk_ratio_pct"`
	CannotSustainRisk      bool     `json:"cannot_sustain_risk"`
	RiskNote               string   `json:"risk_note,omitempty"`
}

// PoolForecastResponse GET /api/site-ops/instances/:id/pool-forecast 响应体。
type PoolForecastResponse struct {
	InstanceID           uint                    `json:"instance_id"`
	ObservedAt           *time.Time              `json:"observed_at,omitempty"`
	TrustState           FieldQualityState       `json:"trust_state"`
	ResetConstraintNote  string                  `json:"reset_constraint_note"`
	PassiveMatrixNote    string                  `json:"passive_matrix_note"`
	ProvenCount          int                     `json:"proven_count"`
	UnverifiedCount      int                     `json:"unverified_count"`
	ExcludedPaygCount    int                     `json:"excluded_payg_count"`
	ExhaustedCount       int                     `json:"exhausted_count"`
	WillExhaustHighCount int                     `json:"will_exhaust_high_count"`
	WillExhaustCount     int                     `json:"will_exhaust_count"`
	TightCount           int                     `json:"tight_count"`
	SafeOrIdleCount      int                     `json:"safe_or_idle_count"`
	InsufficientCount    int                     `json:"insufficient_count"`
	Groups               []GroupForecastRiskView `json:"groups"`
	Accounts             []AccountForecastView   `json:"accounts"`
}

// AccountEfficiencyItem 单个账号的 M5 成本效率评估视图。
type AccountEfficiencyItem struct {
	AccountID                int64      `json:"account_id"`
	Name                     string     `json:"name"`
	Platform                 string     `json:"platform"`
	AccountType              string     `json:"account_type"`
	Status                   string     `json:"status,omitempty"`
	CurrentConcurrency       int        `json:"current_concurrency"`
	StatsSource              string     `json:"stats_source"`
	DaysWindow               int        `json:"days_window"`
	// 三口径 30d 统计
	TotalRequests            int64      `json:"total_requests"`
	TotalTokens              int64      `json:"total_tokens"`
	StandardCost30d          float64    `json:"standard_cost_30d"`
	AccountCost30d           float64    `json:"account_cost_30d"`
	UserCost30d              float64    `json:"user_cost_30d"`
	// 今日三口径
	TodayRequests            int64      `json:"today_requests"`
	TodayStandardCost        float64    `json:"today_standard_cost"`
	TodayAccountCost         float64    `json:"today_account_cost"`
	TodayUserCost            float64    `json:"today_user_cost"`
	// 派生效率指标（零分母或未录入一律为 null）
	MonetizationRatio        *float64   `json:"monetization_ratio"`
	OutputPerConcurrencyUSD  *float64   `json:"output_per_concurrency_usd"`
	ReqPerConcurrency        *float64   `json:"req_per_concurrency"`
	WindowFullLeverageUSD    *float64   `json:"window_full_leverage_usd"`
	WindowLeverageKey        string     `json:"window_leverage_key,omitempty"`
	// 可选本地参考月租与杠杆（账面扣费 / 参考月租，绝不标现金 ROI）
	MonthlyCostUSD           *float64   `json:"monthly_cost_usd"`
	MonthlyCostNote          string     `json:"monthly_cost_note,omitempty"`
	StandardCostLeverage     *float64   `json:"standard_cost_leverage"`
	UserCostPerMonthlyRef    *float64   `json:"user_cost_per_monthly_ref"`
	CostUpdatedAt            *time.Time `json:"cost_updated_at,omitempty"`
}

// AccountsEfficiencyResponse GET /api/site-ops/instances/:id/accounts?view=efficiency 响应体。
type AccountsEfficiencyResponse struct {
	InstanceID              uint                    `json:"instance_id"`
	ObservedAt              *time.Time              `json:"observed_at,omitempty"`
	TrustState              FieldQualityState       `json:"trust_state"`
	TodayStatsBatchGated    bool                    `json:"today_stats_batch_gated"`
	TodayStatsBatchGateNote string                  `json:"today_stats_batch_gate_note"`
	MetricSemanticsNote     string                  `json:"metric_semantics_note"`
	Items                   []AccountEfficiencyItem `json:"items"`
}

// UpdateAccountCostInput PUT /api/site-ops/instances/:id/accounts/:account_id/cost 请求体。
type UpdateAccountCostInput struct {
	MonthlyCostUSD float64 `json:"monthly_cost_usd"`
	Note           string  `json:"note"`
}

// AccountCostView 参考月租成本保存返回体。
type AccountCostView struct {
	InstanceID     uint      `json:"instance_id"`
	AccountID      int64     `json:"account_id"`
	MonthlyCostUSD float64   `json:"monthly_cost_usd"`
	Note           string    `json:"note"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ---------- Wave 3 内部协作接口（零破坏扩展 Wave 1/2 接口） ----------

// P1BClient 扩展上游拉取接口（由 W3-A client.go 实现）。
type P1BClient interface {
	FetchGroupCapacitySummary(ctx context.Context, inst TargetEndpoint) ([]SanitizedGroupCapacitySummary, UpstreamFetchMeta, error)
	FetchAccountPassiveUsage(ctx context.Context, inst TargetEndpoint, accountID int64) (*SanitizedAccountUsage, UpstreamFetchMeta, error)
	// FetchAccountsUsageBatch 仅对调用方筛选后的被动证实账号分批（每批 <=80，批间 >=500ms）拉取 force=false 用量。
	FetchAccountsUsageBatch(ctx context.Context, inst TargetEndpoint, passiveAccountIDs []int64) (*SanitizedBatchUsageResult, UpstreamFetchMeta, error)
	// FetchAccountStats 走已逐账号鉴权的 GET /api/v1/admin/accounts/:id/stats?days=30（规避 v0.2.24 today-stats/batch 缓存先读风险）。
	FetchAccountStats(ctx context.Context, inst TargetEndpoint, accountID int64, days int) (*SanitizedAccountStats, UpstreamFetchMeta, error)
}

// P1BStore Wave 3 窗口样本与参考月租持久化接口（由 W3-A backend/storage/site_ops.go 实现）。
type P1BStore interface {
	UpsertWindowSamples(ctx context.Context, samples []storage.SiteOpsAccountWindowSample) error
	ListWindowSamples(ctx context.Context, instanceID uint, accountID int64, windowKey, source string, since time.Time, limit int) ([]storage.SiteOpsAccountWindowSample, error)
	ListLatestWindowSamplesByInstance(ctx context.Context, instanceID uint, since time.Time) ([]storage.SiteOpsAccountWindowSample, error)
	DeleteWindowSamplesBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error)
	UpsertAccountCost(ctx context.Context, cost *storage.SiteOpsAccountCost) error
	ListAccountCosts(ctx context.Context, instanceID uint) ([]storage.SiteOpsAccountCost, error)
}

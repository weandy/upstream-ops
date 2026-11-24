package siteops

import (
	"context"
	"github.com/bejix/upstream-ops/backend/storage"
	"time"
)

const (
	EndpointAnalyticsGroups      = "analytics.groups.all"
	EndpointUsersPage            = "users.page"
	EndpointUsersUsage           = "dashboard.users_usage"
	EndpointUserConcurrency      = "ops.user_concurrency"
	EndpointSubscriptionsPage    = "subscriptions.page"
	EndpointSubscriptionProgress = "subscriptions.progress"
	EndpointUsageStats           = "usage.stats"
	EndpointUserAPIKeys          = "users.api_keys"
	EndpointAPIKeysUsage         = "dashboard.api_keys_usage"
	EndpointDashboardModels      = "dashboard.models"
	EndpointModelMismatch        = "dashboard.models.mismatch"
	EndpointDashboardGroups      = "dashboard.groups"
	EndpointRiskControlStatus    = "risk_control.status"
	EndpointBackups              = "backups.list"
	EndpointChannelMonitorV2     = "channel_monitor_v2.snapshot"
)

type AnalyticsConfig struct {
	CNYPerUSD               float64 `json:"cny_per_usd"`               // 默认 7.20，范围 (0, 100]
	LeverageWarnThreshold   float64 `json:"leverage_warn_threshold"`   // 默认 3.0，范围 [0, 1000]；0 表示关闭警告上色
	LeverageDangerThreshold float64 `json:"leverage_danger_threshold"` // 默认 5.0，范围 [0, 1000]；0 表示关闭危险上色；两者均 >0 时须 danger >= warn
}

func DefaultAnalyticsConfig() AnalyticsConfig {
	return AnalyticsConfig{
		CNYPerUSD:               7.2,
		LeverageWarnThreshold:   3.0,
		LeverageDangerThreshold: 5.0,
	}
}

type AnalyticsStore interface {
	ListReferencePrices(ctx context.Context, instanceID uint, targetBinding string) ([]storage.SiteOpsReferencePrice, error)
	ListReferencePricesByIDs(ctx context.Context, instanceID uint, targetBinding, scope string, scopeIDs []int64) ([]storage.SiteOpsReferencePrice, error)
	PutReferencePrice(ctx context.Context, expectedGen int64, row *storage.SiteOpsReferencePrice) error
	ClearReferencePrice(ctx context.Context, instanceID uint, expectedGen int64, targetBinding, scope string, scopeID int64) error
	UpdateAnalyticsConfig(ctx context.Context, instanceID uint, configJSON string) error
	ExtendAdminBackoff(ctx context.Context, instanceID uint, expectedGen int64, until time.Time) error
}
type AnalyticsDateRange struct {
	StartDate string `json:"start_date"` // YYYY-MM-DD (Asia/Shanghai)
	EndDate   string `json:"end_date"`   // YYYY-MM-DD (Asia/Shanghai)
	Timezone  string `json:"timezone"`   // 固定 "Asia/Shanghai"
}

type AnalyticsStatsQuery struct {
	AnalyticsDateRange
	UserID      int64 // 必填 > 0
	GroupID     *int64
	BillingType *int // nil=不传, 0=余额, 1=订阅
}

type AnalyticsPage[T any] struct {
	Items     []T    `json:"items"`
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
	Total     *int64 `json:"total"`
	HasMore   *bool  `json:"has_more"`
	Truncated bool   `json:"truncated"`
}

type P2AnalyticsClient interface {
	FetchAnalyticsGroups(ctx context.Context, inst TargetEndpoint) ([]SanitizedGroupInfo, UpstreamFetchMeta, error)
	FetchUsersPage(ctx context.Context, inst TargetEndpoint, page int) (AnalyticsPage[SanitizedAnalyticsUser], UpstreamFetchMeta, error)
	FetchUsersUsageBatch(ctx context.Context, inst TargetEndpoint, userIDs []int64) (map[int64]SanitizedUserQuickUsage, UpstreamFetchMeta, error)
	FetchUserConcurrency(ctx context.Context, inst TargetEndpoint) (SanitizedUserConcurrencyMap, UpstreamFetchMeta, error)
	FetchSubscriptionsPage(ctx context.Context, inst TargetEndpoint, page int) (AnalyticsPage[SanitizedSubscription], UpstreamFetchMeta, error)
	FetchSubscriptionProgress(ctx context.Context, inst TargetEndpoint, subID int64) (*SanitizedSubscriptionProgress, UpstreamFetchMeta, error)
	FetchUsageStats(ctx context.Context, inst TargetEndpoint, q AnalyticsStatsQuery) (*SanitizedUsageStats, UpstreamFetchMeta, error)
	FetchUserAPIKeysPage(ctx context.Context, inst TargetEndpoint, userID int64, page int) (AnalyticsPage[SanitizedUserAPIKey], UpstreamFetchMeta, error)
	FetchAPIKeysUsageBatch(ctx context.Context, inst TargetEndpoint, keyIDs []int64) (map[int64]SanitizedAPIKeyUsage, UpstreamFetchMeta, error)
	FetchDashboardModels(ctx context.Context, inst TargetEndpoint, r AnalyticsDateRange, mismatchOnly bool) (SanitizedModelRows, UpstreamFetchMeta, error)
	FetchDashboardGroups(ctx context.Context, inst TargetEndpoint, r AnalyticsDateRange) (SanitizedGroupMarketRows, UpstreamFetchMeta, error)
	FetchRiskControlStatus(ctx context.Context, inst TargetEndpoint) (*SanitizedRiskControlStatus, UpstreamFetchMeta, error)
	FetchBackupsList(ctx context.Context, inst TargetEndpoint) (SanitizedBackupsResult, UpstreamFetchMeta, error)
	ProbeChannelMonitorV2(ctx context.Context, inst TargetEndpoint) (*SanitizedChannelAvailability, UpstreamFetchMeta, error)
}

// BackoffAwareClient 供 Service 与 Poller 协同共享实例级 429 退避、预算预留与 P2 内存能力叠加层。
type BackoffAwareClient interface {
	AdminBackoff(instanceID uint, credGen int64) (until time.Time, active bool)
	SeedAdminBackoff(instanceID uint, credGen int64, until time.Time)
	BudgetHeadroomForAnalytics(instanceID uint, dailyLimit int) (available int, minReserve int, dayReserve int)
	ObservedP2Capabilities(instanceID uint, generation, credGen int64) CapabilitiesMap
	InvalidateAnalyticsScope(instanceID uint, reason string)
}
type DomainMeta struct {
	Quality    FieldQualityState `json:"quality"`
	ObservedAt *time.Time        `json:"observed_at"`
	VerifiedAt *time.Time        `json:"verified_at"`
	ExpiresAt  *time.Time        `json:"expires_at"`
	Reason     string            `json:"reason,omitempty"`
}

type AnalyticsMeta struct {
	InstanceID   uint                  `json:"instance_id"`
	Generation   int64                 `json:"generation"`
	AsOf         time.Time             `json:"as_of"`
	FromCache    bool                  `json:"from_cache"`
	BackoffUntil *time.Time            `json:"backoff_until"`
	Domains      map[string]DomainMeta `json:"domains"`
}

type SafetyLightLevel string

const (
	SafetyLightGreen   SafetyLightLevel = "green"
	SafetyLightAmber   SafetyLightLevel = "amber"
	SafetyLightRed     SafetyLightLevel = "red"
	SafetyLightUnknown SafetyLightLevel = "unknown"
)

type RiskControlLampView struct {
	Level                    SafetyLightLevel  `json:"level"`
	Quality                  FieldQualityState `json:"quality"`
	Reasons                  []string          `json:"reasons"`
	ObservedAt               *time.Time        `json:"observed_at"`
	Engine                   string            `json:"engine,omitempty"`
	Enabled                  *bool             `json:"enabled"`
	RiskControlEnabled       *bool             `json:"risk_control_enabled"`
	Mode                     string            `json:"mode,omitempty"`
	QueueSize                *int64            `json:"queue_size"`
	QueueLength              *int64            `json:"queue_length"`
	QueueUsagePercent        *float64          `json:"queue_usage_percent"`
	Enqueued                 *int64            `json:"enqueued"`
	Dropped                  *int64            `json:"dropped"`
	Processed                *int64            `json:"processed"`
	Errors                   *int64            `json:"errors"`
	PreBlockChecked          *int64            `json:"pre_block_checked"`
	PreBlockAllowed          *int64            `json:"pre_block_allowed"`
	PreBlockBlocked          *int64            `json:"pre_block_blocked"`
	PreBlockErrors           *int64            `json:"pre_block_errors"`
	PreBlockAvgLatencyMS     *float64          `json:"pre_block_avg_latency_ms"`
	PreBlockAPIKeyAvailCount *int              `json:"pre_block_api_key_available_count"`
	FlaggedHashCount         *int64            `json:"flagged_hash_count"`
}

type BackupLampView struct {
	Level           SafetyLightLevel  `json:"level"`
	Quality         FieldQualityState `json:"quality"`
	Reasons         []string          `json:"reasons"`
	ObservedAt      *time.Time        `json:"observed_at"`
	TotalCount      int               `json:"total_count"`
	LatestStatus    string            `json:"latest_status,omitempty"`
	BackupType      string            `json:"backup_type,omitempty"`
	SizeBytes       *int64            `json:"size_bytes"`
	StartedAt       *time.Time        `json:"started_at"`
	FinishedAt      *time.Time        `json:"finished_at"`
	ExpiresAt       *time.Time        `json:"expires_at"`
	LastCompletedAt *time.Time        `json:"last_completed_at"`
	AgeHours        *float64          `json:"age_hours"`
}

type AnalyticsLightsResponse struct {
	AnalyticsMeta
	RiskControl    RiskControlLampView `json:"risk_control"`
	Backup         BackupLampView      `json:"backup"`
	ChannelMonitor EndpointCapability  `json:"channel_monitor"` // 仅反映已知叠加态；未查为 unknown
}

type AnalyticsChannelResponse struct {
	AnalyticsMeta
	Capability EndpointCapability `json:"capability"`
	Note       string             `json:"note"`
}

type CacheMetricsView struct {
	InputTokens         *int64            `json:"input_tokens"`
	OutputTokens        *int64            `json:"output_tokens"`
	CacheCreationTokens *int64            `json:"cache_creation_tokens"`
	CacheReadTokens     *int64            `json:"cache_read_tokens"`
	TotalTokens         *int64            `json:"total_tokens"`
	EffectiveInput      *int64            `json:"effective_input_tokens"`
	InputMode           string            `json:"input_mode"`    // "exclusive" | "inclusive_detected" | "unverified"
	TokenQuality        FieldQualityState `json:"token_quality"` // "trusted" | "unavailable"
	CacheReadHitPct     *float64          `json:"cache_read_hit_pct"`
	CacheWritePct       *float64          `json:"cache_write_pct"`
	CacheRWRatio        *float64          `json:"cache_rw_ratio"`
}

type UsageWindowView struct {
	StartDate         string            `json:"start_date"`
	EndDate           string            `json:"end_date"`
	Timezone          string            `json:"timezone"`
	ObservedAt        *time.Time        `json:"observed_at"`
	Quality           FieldQualityState `json:"quality"`
	Requests          *int64            `json:"requests"`
	StandardUSD       *float64          `json:"standard_usd"`
	ActualUSD         *float64          `json:"actual_usd"`
	AccountUSD        *float64          `json:"account_usd"`
	HasCostSpread     bool              `json:"has_cost_spread"`
	AverageDurationMS *float64          `json:"average_duration_ms"`
	Cache             CacheMetricsView  `json:"cache"`
}

type OperationalTagView struct {
	Key      string `json:"key"`                // high_value_at_risk | expiring_soon | expired_unrenewed | cache_anomaly | concurrency_queued | dormant_high_balance
	SubKind  string `json:"sub_kind,omitempty"` // cache_drop | low_cache_hit 等
	Label    string `json:"label"`
	Severity string `json:"severity"` // "critical" | "warning" | "info"
	Reason   string `json:"reason"`
}

type AnalyticsUserRow struct {
	UserID                   int64                `json:"user_id"`
	EmailMasked              *string              `json:"email_masked"`
	Role                     string               `json:"role"`
	Status                   string               `json:"status"`
	CreatedAt                *time.Time           `json:"created_at"`
	BalanceUSD               *float64             `json:"balance_usd"`
	ConcurrencyLimit         *int                 `json:"concurrency_limit"`
	TodayActualUSD           *float64             `json:"today_actual_usd"`
	RecentAggregateActualUSD *float64             `json:"recent_aggregate_actual_usd"` // 来自 users-usage，显式标非全时段
	ByPlatform               []PlatformCostBrief  `json:"by_platform"`
	LiveConcurrency          *UserConcurrencyView `json:"live_concurrency"`
	ActiveSubCount           int                  `json:"active_sub_count"`
	NearestSubExpiresDays    *float64             `json:"nearest_sub_expires_days"`
	EnrichmentState          string               `json:"enrichment_state"` // "loaded" | "not_loaded" | "deferred_budget" | "error"
	AllTime                  *UsageWindowView     `json:"all_time"`
	Last7d                   *UsageWindowView     `json:"last_7d"`
	Tags                     []OperationalTagView `json:"tags"`
}

type AnalyticsUsersSummary struct {
	UsersSeen            int            `json:"users_seen"`
	UsersCoverage        string         `json:"users_coverage"` // "complete" | "page" | "truncated"
	SubsCoverage         string         `json:"subs_coverage"`  // "complete" | "page" | "truncated"
	ActiveTodayUsers     int            `json:"active_today_users"`
	TodayActualUSD       *float64       `json:"today_actual_usd"`
	EvaluatedUsers       int            `json:"evaluated_users"`
	BudgetDeferredUsers  int            `json:"budget_deferred_users"`
	ActiveSubsSeen       int            `json:"active_subs_seen"`
	ExpiringSoonSubsSeen int            `json:"expiring_soon_subs_seen"`
	QueueingUsersSeen    int            `json:"queueing_users_seen"`
	TagCounts            map[string]int `json:"tag_counts"`
	BudgetNote           string         `json:"budget_note,omitempty"`
}

type AnalyticsUsersResponse struct {
	AnalyticsMeta
	TopN    int                             `json:"top_n"`
	Summary AnalyticsUsersSummary           `json:"summary"`
	Users   AnalyticsPage[AnalyticsUserRow] `json:"users"`
}

type UserAPIKeyCostView struct {
	APIKeyID               int64             `json:"api_key_id"`
	GroupID                *int64            `json:"group_id"`
	GroupName              string            `json:"group_name,omitempty"`
	Status                 string            `json:"status"`
	CreatedAt              *time.Time        `json:"created_at"`
	LastUsedAt             *time.Time        `json:"last_used_at"`
	TodayActualUSD         *float64          `json:"today_actual_usd"`
	UpstreamTotalActualUSD *float64          `json:"upstream_total_actual_usd"` // 上游累计口径
	Quality                FieldQualityState `json:"quality"`
}

type AnalyticsUserDetailResponse struct {
	AnalyticsMeta
	User          AnalyticsUserRow                  `json:"user"`
	Last30d       *UsageWindowView                  `json:"last_30d"`
	Subscriptions []UserSubscriptionBrief           `json:"subscriptions"`
	APIKeys       AnalyticsPage[UserAPIKeyCostView] `json:"api_keys"`
}

type RollingWindowView struct {
	LimitUSD        *float64          `json:"limit_usd"`
	UsedUSD         *float64          `json:"used_usd"`
	RemainingUSD    *float64          `json:"remaining_usd"`
	Percentage      *float64          `json:"percentage"`
	WindowStart     *time.Time        `json:"window_start"`
	ResetsAt        *time.Time        `json:"resets_at"`
	ResetsInSeconds *int64            `json:"resets_in_seconds"`
	Source          string            `json:"source"` // "progress" | "list_snapshot" | "group_limit"
	Quality         FieldQualityState `json:"quality"`
}

type SubscriptionTermLayerView struct {
	StartAt             time.Time        `json:"start_at"`
	EndAt               time.Time        `json:"end_at"`
	ObservedUntil       *time.Time       `json:"observed_until"`
	CycleIndex          int              `json:"cycle_index"`
	TotalCycles         int              `json:"total_cycles"`
	DurationDays        float64          `json:"duration_days"`
	ElapsedHours        float64          `json:"elapsed_hours"`
	RemainingHours      float64          `json:"remaining_hours"`
	ForecastStatus      string           `json:"forecast_status"` // "active" | "completed" | "not_started" | "revoked" | "insufficient"
	Usage               *UsageWindowView `json:"usage"`           // 仅 unique_group_term 填充
	GroupUsage          *UsageWindowView `json:"group_usage"`     // 仅 group_only 填充
	BoundaryApprox      bool             `json:"boundary_approx"`
	DayBoundaryNote     string           `json:"day_boundary_note,omitempty"`
	BurnSlowUSDDay      *float64         `json:"burn_slow_usd_day"`
	BurnFastUSDDay      *float64         `json:"burn_fast_usd_day"`
	BurnFastSource      string           `json:"burn_fast_source,omitempty"` // "weekly" | "daily"
	BurnEffectiveUSDDay *float64         `json:"burn_effective_usd_day"`
	UncappedEstimateUSD *float64         `json:"uncapped_estimate_usd"`
	ProjectedEndUSD     *float64         `json:"projected_end_usd"`
	TheoreticalMaxUSD   *float64         `json:"theoretical_max_usd"`
	CapState            string           `json:"cap_state"` // "verified" | "partial" | "unlimited" | "unknown"
	CappedByLimit       bool             `json:"capped_by_limit"`
	CapBindingWindow    string           `json:"cap_binding_window,omitempty"` // "daily" | "weekly" | "monthly"
	Confidence          string           `json:"confidence"`                   // "high" | "medium" | "low" | "insufficient"
	Reason              string           `json:"reason,omitempty"`
}

type ReferencePriceView struct {
	InstanceID      uint      `json:"instance_id"`
	Scope           string    `json:"scope"` // "group" | "subscription"
	ScopeID         int64     `json:"scope_id"`
	Currency        string    `json:"currency"` // "CNY" | "USD"
	MonthlyMinor    int64     `json:"monthly_minor"`
	CNYPerUSDMicros *int64    `json:"cny_per_usd_micros"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SubscriptionPricingCompare struct {
	PriceSource        string              `json:"price_source"` // "subscription" | "group" | "none"
	ReferencePrice     *ReferencePriceView `json:"reference_price"`
	EffectiveFX        float64             `json:"effective_fx"`
	Proration          *float64            `json:"proration"`
	CycleRefNative     *float64            `json:"cycle_ref_native"`
	CycleRefUSD        *float64            `json:"cycle_ref_usd"`
	TermRefUSD         *float64            `json:"term_ref_usd"`
	ActualLeverageX    *float64            `json:"actual_leverage_x"`
	ProjectedLeverageX *float64            `json:"projected_leverage_x"`
	MarginRisk         string              `json:"margin_risk"` // "unconfigured" | "free" | "threshold_disabled" | "normal" | "warn" | "danger" | "unknown"
}

type AnalyticsSubscriptionRow struct {
	SubscriptionID    int64                      `json:"subscription_id"`
	UserID            int64                      `json:"user_id"`
	UserEmailMasked   *string                    `json:"user_email_masked"`
	GroupID           int64                      `json:"group_id"`
	GroupName         string                     `json:"group_name"`
	Platform          string                     `json:"platform"`
	Status            string                     `json:"status"`
	CycleState        string                     `json:"cycle_state"` // "active" | "expired" | "revoked" | "not_started" | "invalid_dates"
	StartsAt          *time.Time                 `json:"starts_at"`
	ExpiresAt         *time.Time                 `json:"expires_at"`
	ExpiresInDays     *float64                   `json:"expires_in_days"`
	CycleIndex        int                        `json:"cycle_index"`
	TotalCycles       int                        `json:"total_cycles"`
	SubCycleStart     *time.Time                 `json:"sub_cycle_start"`
	SubCycleEnd       *time.Time                 `json:"sub_cycle_end"`
	SameAsTerm        bool                       `json:"same_as_term"`
	Attribution       string                     `json:"attribution"` // "unique_group_term" | "group_only"
	AttributionReason string                     `json:"attribution_reason,omitempty"`
	DailyWindow       RollingWindowView          `json:"daily_window"`
	WeeklyWindow      RollingWindowView          `json:"weekly_window"`
	MonthlyWindow     RollingWindowView          `json:"monthly_window"`
	PricingCompare    SubscriptionPricingCompare `json:"pricing_compare"`
}

type AnalyticsSubscriptionsResponse struct {
	AnalyticsMeta
	AnalyticsConfig AnalyticsConfig                         `json:"analytics_config"`
	Groups          []AnalyticsGroupOption                  `json:"groups"`
	Subscriptions   AnalyticsPage[AnalyticsSubscriptionRow] `json:"subscriptions"`
}

type AnalyticsSubscriptionDetailResponse struct {
	AnalyticsMeta
	AnalyticsConfig AnalyticsConfig            `json:"analytics_config"`
	Subscription    AnalyticsSubscriptionRow   `json:"subscription"`
	CurrentSubCycle *SubscriptionTermLayerView `json:"current_sub_cycle"`
	ContinuousTerm  *SubscriptionTermLayerView `json:"continuous_term"`
}

type AnalyticsModelRowView struct {
	Model            string            `json:"model"`
	Requests         *int64            `json:"requests"`
	StandardUSD      *float64          `json:"standard_usd"`
	ActualUSD        *float64          `json:"actual_usd"`
	AccountUSD       *float64          `json:"account_usd"`
	HasCostSpread    bool              `json:"has_cost_spread"`
	Cache            CacheMetricsView  `json:"cache"`
	MismatchRequests *int64            `json:"mismatch_requests"`
	HasMismatch      bool              `json:"has_mismatch"`
	Quality          FieldQualityState `json:"quality"`
}

type ModelMismatchRowView struct {
	RequestedModel string   `json:"requested_model"`
	UpstreamModel  string   `json:"upstream_model,omitempty"`
	Requests       *int64   `json:"requests"`
	ActualUSD      *float64 `json:"actual_usd"`
}

type ModelMismatchSummaryView struct {
	Quality       FieldQualityState      `json:"quality"`
	TotalRequests *int64                 `json:"total_requests"` // 严禁用数组长度冒充请求数
	ModelRows     int                    `json:"model_rows"`
	Rows          []ModelMismatchRowView `json:"rows"`
}

type AnalyticsGroupMarketRowView struct {
	GroupID          int64               `json:"group_id"`
	GroupName        string              `json:"group_name"`
	Platform         string              `json:"platform"`
	SubscriptionType string              `json:"subscription_type,omitempty"`
	RateMultiplier   *float64            `json:"rate_multiplier"`
	ActiveSubCount   *int                `json:"active_sub_count"` // 仅目录完整或可见页计数，配 coverage 说明
	Requests         *int64              `json:"requests"`
	StandardUSD      *float64            `json:"standard_usd"`
	ActualUSD        *float64            `json:"actual_usd"`
	AccountUSD       *float64            `json:"account_usd"`
	HasCostSpread    bool                `json:"has_cost_spread"`
	ReferencePrice   *ReferencePriceView `json:"reference_price"`
	RefMonthlyUSD    *float64            `json:"ref_monthly_usd"` // 单席参考月价折算 USD（非实收合计）
	Quality          FieldQualityState   `json:"quality"`
}

type AnalyticsMarketResponse struct {
	AnalyticsMeta
	Range           string                        `json:"range"` // "30d" | "7d"
	DateRange       AnalyticsDateRange            `json:"date_range"`
	AnalyticsConfig AnalyticsConfig               `json:"analytics_config"`
	TotalRequests   *int64                        `json:"total_requests"`
	TotalActualUSD  *float64                      `json:"total_actual_usd"`
	TotalAccountUSD *float64                      `json:"total_account_usd"`
	HasCostSpread   bool                          `json:"has_cost_spread"`
	Models          []AnalyticsModelRowView       `json:"models"`
	Mismatches      ModelMismatchSummaryView      `json:"mismatches"`
	Groups          []AnalyticsGroupMarketRowView `json:"groups"`
}

type AnalyticsPricingResponse struct {
	InstanceID uint                 `json:"instance_id"`
	Generation int64                `json:"generation"`
	Config     AnalyticsConfig      `json:"config"`
	Prices     []ReferencePriceView `json:"prices"`
}

// 辅助请求参数与嵌套视图结构体（消除前后端字段名歧义）
type AnalyticsUsersQuery struct {
	Page      int
	TopN      int
	Force     bool
	CacheOnly bool
}

type AnalyticsSubscriptionsQuery struct {
	Page      int
	Status    string // "active" | "all" | "expired"
	GroupID   *int64
	Force     bool
	CacheOnly bool
}

type PutReferencePriceInput struct {
	Generation      int64  `json:"generation"`
	Currency        string `json:"currency"` // "CNY" | "USD"
	MonthlyMinor    *int64 `json:"monthly_minor"`
	CNYPerUSDMicros *int64 `json:"cny_per_usd_micros,omitempty"`
}

type PlatformCostBrief struct {
	Platform        string   `json:"platform"`
	TodayActualUSD  *float64 `json:"today_actual_usd"`
	RecentActualUSD *float64 `json:"recent_actual_usd"`
}

type UserConcurrencyView struct {
	CurrentInUse   *int64   `json:"current_in_use"`
	MaxCapacity    *int64   `json:"max_capacity"`
	LoadPercentage *float64 `json:"load_percentage"`
	WaitingInQueue *int64   `json:"waiting_in_queue"`
}

type UserSubscriptionBrief struct {
	SubscriptionID int64      `json:"subscription_id"`
	GroupID        int64      `json:"group_id"`
	GroupName      string     `json:"group_name"`
	Platform       string     `json:"platform"`
	Status         string     `json:"status"`
	StartsAt       *time.Time `json:"starts_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	ExpiresInDays  *float64   `json:"expires_in_days"`
}

type AnalyticsGroupOption struct {
	GroupID        int64               `json:"group_id"`
	GroupName      string              `json:"group_name"`
	Platform       string              `json:"platform"`
	ReferencePrice *ReferencePriceView `json:"reference_price"`
}
type SanitizedAnalyticsUser struct {
	ID               int64
	EmailMasked      *string
	Role, Status     string
	CreatedAt        *time.Time
	BalanceUSD       *float64
	ConcurrencyLimit *int
}
type SanitizedUserQuickUsage struct {
	Today, Recent *float64
	ByPlatform    []PlatformCostBrief
}
type SanitizedUserConcurrencyMap map[int64]UserConcurrencyView
type SanitizedSubscription struct {
	ID, UserID, GroupID            int64
	EmailMasked                    *string
	Status                         string
	StartsAt, ExpiresAt, RevokedAt *time.Time
	Group                          SanitizedGroupInfo
	Daily, Weekly, Monthly         RollingWindowView
}
type SanitizedSubscriptionProgress struct {
	Daily, Weekly, Monthly *RollingWindowView
	ExpiresAt              *time.Time
}
type SanitizedUsageStats struct {
	Requests                              *int64
	Standard, Actual, Account, DurationMS *float64
	Tokens                                CacheMetricsView
}
type SanitizedUserAPIKey = UserAPIKeyCostView
type SanitizedAPIKeyUsage struct{ Today, Total *float64 }
type SanitizedModelRow struct {
	Model, UpstreamModel string
	Stats                SanitizedUsageStats
}
type SanitizedModelRows struct {
	Rows      []SanitizedModelRow
	Truncated bool
}
type SanitizedGroupMarketRows struct {
	Rows      []AnalyticsGroupMarketRowView
	Truncated bool
}
type SanitizedRiskControlStatus struct {
	Engine                   string   `json:"engine"`
	Enabled                  *bool    `json:"enabled"`
	RiskControlEnabled       *bool    `json:"risk_control_enabled"`
	Mode                     string   `json:"mode"`
	QueueSize                *int64   `json:"queue_size"`
	QueueLength              *int64   `json:"queue_length"`
	QueueUsagePercent        *float64 `json:"queue_usage_percent"`
	Enqueued                 *int64   `json:"enqueued"`
	Dropped                  *int64   `json:"dropped"`
	Processed                *int64   `json:"processed"`
	Errors                   *int64   `json:"errors"`
	PreBlockChecked          *int64   `json:"pre_block_checked"`
	PreBlockAllowed          *int64   `json:"pre_block_allowed"`
	PreBlockBlocked          *int64   `json:"pre_block_blocked"`
	PreBlockErrors           *int64   `json:"pre_block_errors"`
	PreBlockAvgLatencyMS     *float64 `json:"pre_block_avg_latency_ms"`
	PreBlockAPIKeyAvailCount *int     `json:"pre_block_api_key_available_count"`
	FlaggedHashCount         *int64   `json:"flagged_hash_count"`
}
type SanitizedBackup struct {
	Status     string     `json:"status"`
	BackupType string     `json:"backup_type"`
	SizeBytes  *int64     `json:"size_bytes"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}
type SanitizedBackupsResult struct {
	Rows      []SanitizedBackup
	Truncated bool
}
type SanitizedChannelAvailability struct{ Available bool }

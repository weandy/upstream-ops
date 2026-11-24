/**
 * 自有 Sub2API 站点运维模块（siteops · Wave 1 P0-A + P0-B）前端类型契约。
 * 与 backend/siteops/types.go 一一对应；所有缺失/未授权/未证实字段均可能为 null/undefined，绝不伪造为 0。
 */

export type SiteOpsReachabilityState = "reachable" | "unreachable" | "unknown"

export type SiteOpsInstanceStatus =
  | "online"
  | "degraded"
  | "offline"
  | "auth_error"
  | "paused_no_auth"
  | "pending"

export type SiteOpsCapabilityState =
  | "supported"
  | "forbidden"
  | "ops_disabled"
  | "unsupported"
  | "error"
  | "unknown"

export type SiteOpsFieldQualityState =
  | "trusted"
  | "unverified_zero"
  | "stale"
  | "disabled"
  | "unauthorized"
  | "unavailable"

export type SiteOpsTrendRange = "1h" | "6h" | "24h" | "7d" | "14d"

export interface SiteOpsEndpointCapability {
  state: SiteOpsCapabilityState
  http_status?: number
  fallback_target?: string
  reason?: string
  checked_at: string
}

export type SiteOpsCapabilitiesMap = Record<string, SiteOpsEndpointCapability>
export type SiteOpsFieldQualityMap = Record<string, SiteOpsFieldQualityState>

export interface SiteOpsAuditBudgetUsage {
  used_today: number
  daily_limit: number
  used_last_min: number
  per_min_limit: number
  window_date_utc: string
  reset_at?: string | null
}

export interface SiteOpsJobHeartbeat {
  job_name: string
  status: string
  last_success_at?: string | null
  last_run_at?: string | null
  has_error: boolean
  error_summary?: string
}

export interface SiteOpsPlatformPoolSummary {
  platform: string
  accounts_total: number
  accounts_ops_available: number
  accounts_rate_limited: number
  accounts_overloaded: number
  accounts_error: number
  conc_used?: number | null
  conc_max?: number | null
  conc_waiting?: number | null
  load_percent?: number | null
}

export interface SiteOpsInstanceView {
  id: number
  name: string
  slug: string
  base_url: string
  api_key_set: boolean
  api_key_hint: string
  key_kind: "scoped" | "full" | string
  cred_gen: number
  generation: number
  proxy_enabled: boolean
  enabled: boolean
  sort_order: number
  poll_interval_seconds: number
  audit_budget_per_day: number
  audit_budget: SiteOpsAuditBudgetUsage
  capabilities: SiteOpsCapabilitiesMap
  last_reachability: SiteOpsReachabilityState
  last_status: SiteOpsInstanceStatus
  last_health_score?: number | null
  last_version: string
  last_observed_at?: string | null
  last_verified_at?: string | null
  backoff_until?: string | null
  last_error_code?: string
  last_error_summary?: string
  created_at: string
  updated_at: string
}

export interface SiteOpsCreateInstanceInput {
  name: string
  slug?: string
  base_url: string
  api_key: string
  key_kind?: "scoped" | "full"
  proxy_enabled?: boolean
  enabled?: boolean
  sort_order?: number
  poll_interval_seconds?: number
  audit_budget_per_day?: number
}

export interface SiteOpsUpdateInstanceInput {
  name?: string
  slug?: string
  base_url?: string
  api_key?: string
  key_kind?: "scoped" | "full"
  proxy_enabled?: boolean
  enabled?: boolean
  sort_order?: number
  poll_interval_seconds?: number
  audit_budget_per_day?: number
}

export interface SiteOpsHealthProbeResult {
  reachable: boolean
  http_status: number
  latency_ms: number
  checked_at: string
  error_code?: string
  sanitized_error?: string
}

export interface SiteOpsProbeReport {
  instance_id: number
  reachability: SiteOpsReachabilityState
  health: SiteOpsHealthProbeResult
  version?: string
  capabilities: SiteOpsCapabilitiesMap
  probed_at: string
}

export interface SiteOpsInstanceSummaryItem {
  id: number
  name: string
  slug: string
  enabled: boolean
  last_reachability: SiteOpsReachabilityState
  last_status: SiteOpsInstanceStatus
  health_score?: number | null
  sla_percent_1h?: number | null
  qps_current?: number | null
  conc_used?: number | null
  conc_max?: number | null
  conc_trust_state: SiteOpsFieldQualityState
  accounts_total?: number | null
  accounts_ops_available?: number | null
  accounts_error?: number | null
  last_version: string
  last_observed_at?: string | null
  last_verified_at?: string | null
  audit_budget: SiteOpsAuditBudgetUsage
  last_error_code?: string
  last_error_summary?: string
}

export interface SiteOpsSummaryResponse {
  polling_active: boolean
  auth_enabled: boolean
  total_count: number
  online_count: number
  degraded_count: number
  offline_count: number
  instances: SiteOpsInstanceSummaryItem[]
}

export interface SiteOpsSnapshotView {
  observed_at: string
  verified_at: string
  reachable: boolean
  probe_latency_ms: number
  field_quality: SiteOpsFieldQualityMap
  health_score?: number | null
  uptime_seconds?: number | null
  cpu_percent?: number | null
  mem_percent?: number | null
  mem_used_mb?: number | null
  db_ok?: boolean | null
  db_conn_active?: number | null
  db_conn_waiting?: number | null
  db_max_open_conns?: number | null
  redis_ok?: boolean | null
  redis_conn_total?: number | null
  redis_conn_idle?: number | null
  redis_pool_size?: number | null
  goroutine_count?: number | null
  queue_depth?: number | null
  account_switch_count_1m?: number | null
  estimated_switch_pct_1m?: number | null
  sla_percent_1h?: number | null
  error_rate_percent_1h?: number | null
  upstream_error_rate_percent_1h?: number | null
  req_total_1h?: number | null
  err_total_1h?: number | null
  upstream_429_1h?: number | null
  upstream_529_1h?: number | null
  upstream_err_excl_429_529_1h?: number | null
  qps_current?: number | null
  qps_peak?: number | null
  tps_current?: number | null
  rpm_5m?: number | null
  tpm_5m?: number | null
  ttft_p50_ms?: number | null
  ttft_p95_ms?: number | null
  duration_p95_ms?: number | null
  output_tps_p50?: number | null
  conc_used?: number | null
  conc_max?: number | null
  conc_waiting?: number | null
  conc_load_percent?: number | null
  conc_trust_state: SiteOpsFieldQualityState
  conc_trust_reason?: string
  accounts_total?: number | null
  accounts_ops_available?: number | null
  accounts_rate_limited?: number | null
  accounts_overloaded?: number | null
  accounts_error?: number | null
  system_metrics_at?: string | null
  job_heartbeats?: SiteOpsJobHeartbeat[]
  platform_breakdown?: SiteOpsPlatformPoolSummary[]
}

export interface SiteOpsOverviewResponse {
  instance: SiteOpsInstanceView
  latest?: SiteOpsSnapshotView | null
  system_metrics_resolution: string
  sla_denominator_note: string
  ops_availability_label: string
  configured_capacity_label: string
}

export interface SiteOpsTrendPoint {
  observed_at: string
  verified_at: string
  reachable: boolean
  probe_latency_ms: number
  health_score?: number | null
  sla_percent_1h?: number | null
  error_rate_percent_1h?: number | null
  upstream_error_rate_percent_1h?: number | null
  account_switch_count_1m?: number | null
  qps_current?: number | null
  ttft_p95_ms?: number | null
  duration_p95_ms?: number | null
  cpu_percent?: number | null
  mem_percent?: number | null
  conc_used?: number | null
  conc_max?: number | null
  conc_trust_state: SiteOpsFieldQualityState
  accounts_total?: number | null
  accounts_ops_available?: number | null
}

export interface SiteOpsTrendsResponse {
  instance_id: number
  range: SiteOpsTrendRange | string
  points: SiteOpsTrendPoint[]
}

export interface SiteOpsGroupCapacityItem {
  group_id: number
  group_name: string
  platform: string
  observed_at: string
  trust_state: SiteOpsFieldQualityState
  trust_reason?: string
  accounts_total: number
  accounts_ops_available: number
  accounts_rate_limited: number
  accounts_overloaded: number
  accounts_error: number
  single_point_risk: boolean
  conc_used?: number | null
  conc_max?: number | null
  conc_waiting?: number | null
  load_percent?: number | null
  sessions_used?: number | null
  sessions_max?: number | null
  rpm_used?: number | null
  rpm_max?: number | null
  pool_exhaust_risk_count?: number
}

export interface SiteOpsGroupsResponse {
  instance_id: number
  observed_at?: string | null
  trust_state: SiteOpsFieldQualityState
  trust_reason?: string
  ops_availability_label: string
  configured_capacity_label: string
  platform_breakdown: SiteOpsPlatformPoolSummary[]
  groups: SiteOpsGroupCapacityItem[]
}

export interface SiteOpsRefreshResult {
  instance_id: number
  snapshot?: SiteOpsSnapshotView | null
  status: SiteOpsInstanceStatus
  message?: string
}

// ---------- Wave 2 (P1-A: M6 零广播告警 + 运维日报 + 异常号面板) ----------

export type SiteOpsEventSeverity = "critical" | "warning" | "info" | string
export type SiteOpsEventState = "firing" | "resolved" | string
export type SiteOpsDeliveryStatus = "pending" | "sent" | "failed" | "skipped_cooldown" | string

export interface SiteOpsSubscriptionConfig {
  enabled: boolean
  instance_ids: string[]
  events: string[]
}

export interface SiteOpsEventDelivery {
  id: number
  event_id: number
  notify_channel_id: number
  channel_name?: string
  channel_type?: string
  delivery_kind: "alert" | "recovery" | "daily_report" | string
  status: SiteOpsDeliveryStatus
  attempts: number
  next_retry_at?: string | null
  last_error?: string
  sent_at?: string | null
  created_at: string
}

export interface SiteOpsEventItem {
  id: number
  instance_id: number
  severity: SiteOpsEventSeverity
  rule_key: string
  resource_key: string
  required_domains: string[]
  state: SiteOpsEventState
  consecutive_hits: number
  title: string
  summary: string
  detail?: Record<string, unknown>
  fired_at: string
  resolved_at?: string | null
  deliveries: SiteOpsEventDelivery[]
  created_at: string
  updated_at: string
}

export interface SiteOpsEventsResponse {
  instance_id: number
  firing_count: number
  items: SiteOpsEventItem[]
}

export interface SiteOpsAccountStatusItem {
  account_id: number
  name: string
  platform: string
  type?: string
  status?: string
  schedulable?: boolean | null
  ops_available: boolean
  rate_limited: boolean
  overloaded: boolean
  has_error: boolean
  error_summary?: string
  conc_used?: number | null
  conc_max?: number | null
  conc_waiting?: number | null
  group_ids?: number[]
  expires_at?: string | null
  rate_limit_reset_at?: string | null
  overload_until?: string | null
  temp_unschedulable_until?: string | null
}

export interface SiteOpsAccountsStatusResponse {
  instance_id: number
  observed_at?: string | null
  trust_state: SiteOpsFieldQualityState
  total_count: number
  available_count: number
  rate_limit_count: number
  overload_count: number
  error_count: number
  items: SiteOpsAccountStatusItem[]
}

export interface SiteOpsDailyReportMetrics {
  window_start_utc8: string
  window_end_utc8: string
  sample_count: number
  reachable_sample_count: number
  sla_percent_24h?: number | null
  sla_note: string
  req_total_24h?: number | null
  err_total_24h?: number | null
  ttft_p95_ms_24h?: number | null
  duration_p95_ms_24h?: number | null
  latency_24h_note: string
  peak_conc_used?: number | null
  peak_conc_max?: number | null
  total_switches_1m: number
  peak_switches_1m: number
  current_accounts_total?: number | null
  current_accounts_error?: number | null
  active_firing_alerts: number
}

export interface SiteOpsDailyReportPreview {
  instance_id: number
  instance_name: string
  date_utc8: string
  generated_at: string
  subject: string
  body: string
  metrics: SiteOpsDailyReportMetrics
}

export interface SiteOpsDailyReportSendResult {
  preview: SiteOpsDailyReportPreview
  matched_targets: number
  deliveries: SiteOpsEventDelivery[]
}

// ---------- Wave 3 (P1-B: M4 被动号池周期预测 + M5 单号成本效率) ----------

export type SiteOpsForecastStatus =
  | "safe"
  | "tight"
  | "will_exhaust"
  | "will_exhaust_high"
  | "exhausted"
  | "idle"
  | "insufficient_data"
  | string

export type SiteOpsForecastConfidence = "high" | "medium" | "low" | string

export type SiteOpsPassiveSupport =
  | "passive_proven"
  | "passive_unverified"
  | "excluded_payg"
  | string

export interface SiteOpsWindowForecastView {
  window_key: string
  source: string
  window_duration_sec: number
  utilization: number
  resets_at?: string | null
  remaining_seconds: number
  upstream_observed_at: string
  burn_rate_per_hour?: number | null
  eta_seconds?: number | null
  predicted_end_util?: number | null
  margin_hours?: number | null
  forecast_status: SiteOpsForecastStatus
  confidence: SiteOpsForecastConfidence
  note?: string
}

export interface SiteOpsAccountForecastView {
  account_id: number
  name: string
  platform: string
  account_type: string
  group_ids?: number[]
  passive_support: SiteOpsPassiveSupport
  passive_support_reason?: string
  expires_at?: string | null
  earliest_blocking_window?: string
  overall_status: SiteOpsForecastStatus
  overall_confidence: SiteOpsForecastConfidence
  earliest_eta_seconds?: number | null
  earliest_margin_hours?: number | null
  windows: SiteOpsWindowForecastView[]
}

export interface SiteOpsGroupForecastRiskView {
  group_id: number
  group_name: string
  platform: string
  proven_accounts: number
  unverified_accounts: number
  excluded_payg_accounts: number
  exhausted_count: number
  will_exhaust_high_count: number
  will_exhaust_count: number
  tight_count: number
  safe_or_idle_count: number
  insufficient_data_count: number
  high_risk_ratio_pct?: number | null
  cannot_sustain_risk: boolean
  risk_note?: string
}

export interface SiteOpsPoolForecastResponse {
  instance_id: number
  observed_at?: string | null
  trust_state: SiteOpsFieldQualityState
  reset_constraint_note: string
  passive_matrix_note: string
  proven_count: number
  unverified_count: number
  excluded_payg_count: number
  exhausted_count: number
  will_exhaust_high_count: number
  will_exhaust_count: number
  tight_count: number
  safe_or_idle_count: number
  insufficient_count: number
  groups: SiteOpsGroupForecastRiskView[]
  accounts: SiteOpsAccountForecastView[]
}

export interface SiteOpsAccountEfficiencyItem {
  account_id: number
  name: string
  platform: string
  account_type: string
  status?: string
  current_concurrency: number
  stats_source: string
  days_window: number
  total_requests: number
  total_tokens: number
  standard_cost_30d: number
  account_cost_30d: number
  user_cost_30d: number
  today_requests: number
  today_standard_cost: number
  today_account_cost: number
  today_user_cost: number
  monetization_ratio?: number | null
  output_per_concurrency_usd?: number | null
  req_per_concurrency?: number | null
  window_full_leverage_usd?: number | null
  window_leverage_key?: string
  monthly_cost_usd?: number | null
  monthly_cost_note?: string
  standard_cost_leverage?: number | null
  user_cost_per_monthly_ref?: number | null
  cost_updated_at?: string | null
}

export interface SiteOpsAccountsEfficiencyResponse {
  instance_id: number
  observed_at?: string | null
  trust_state: SiteOpsFieldQualityState
  today_stats_batch_gated: boolean
  today_stats_batch_gate_note: string
  metric_semantics_note: string
  items: SiteOpsAccountEfficiencyItem[]
}

export interface SiteOpsUpdateAccountCostInput {
  monthly_cost_usd: number
  note: string
}

export interface SiteOpsAccountCostView {
  instance_id: number
  account_id: number
  monthly_cost_usd: number
  note: string
  updated_at: string
}

// 运营分析视图与 backend/siteops/types_analytics.go 的 JSON 字段对应。
export type SiteOpsAnalyticsSafetyLightLevel = "green" | "amber" | "red" | "unknown"
export type SiteOpsAnalyticsPriceScope = "group" | "subscription"
export type SiteOpsAnalyticsCurrency = "CNY" | "USD"
export type SiteOpsAnalyticsRange = "30d" | "7d"
export type SiteOpsAnalyticsSubscriptionStatus = "active" | "expired" | "all"

export interface SiteOpsAnalyticsConfig {
  cny_per_usd: number
  leverage_warn_threshold: number
  leverage_danger_threshold: number
}

export interface SiteOpsAnalyticsDateRange {
  start_date: string
  end_date: string
  timezone: string
}

export interface SiteOpsAnalyticsPage<T> {
  items: T[] | null
  page: number
  page_size: number
  total: number | null
  has_more: boolean | null
  truncated: boolean
}

export interface SiteOpsAnalyticsDomainMeta {
  quality: SiteOpsFieldQualityState
  observed_at: string | null
  verified_at: string | null
  expires_at: string | null
  reason?: string
}

export interface SiteOpsAnalyticsMeta {
  instance_id: number
  generation: number
  as_of: string
  from_cache: boolean
  backoff_until: string | null
  domains: Record<string, SiteOpsAnalyticsDomainMeta>
}

export interface SiteOpsAnalyticsRiskControlLampView {
  level: SiteOpsAnalyticsSafetyLightLevel
  quality: SiteOpsFieldQualityState
  reasons: string[] | null
  observed_at: string | null
  engine?: string
  enabled: boolean | null
  risk_control_enabled: boolean | null
  mode?: string
  queue_size: number | null
  queue_length: number | null
  queue_usage_percent: number | null
  enqueued: number | null
  dropped: number | null
  processed: number | null
  errors: number | null
  pre_block_checked: number | null
  pre_block_allowed: number | null
  pre_block_blocked: number | null
  pre_block_errors: number | null
  pre_block_avg_latency_ms: number | null
  pre_block_api_key_available_count: number | null
  flagged_hash_count: number | null
}

export interface SiteOpsAnalyticsBackupLampView {
  level: SiteOpsAnalyticsSafetyLightLevel
  quality: SiteOpsFieldQualityState
  reasons: string[] | null
  observed_at: string | null
  total_count: number
  latest_status?: string
  backup_type?: string
  size_bytes: number | null
  started_at: string | null
  finished_at: string | null
  expires_at: string | null
  last_completed_at: string | null
  age_hours: number | null
}

export interface SiteOpsAnalyticsLightsResponse extends SiteOpsAnalyticsMeta {
  risk_control: SiteOpsAnalyticsRiskControlLampView
  backup: SiteOpsAnalyticsBackupLampView
  channel_monitor: SiteOpsEndpointCapability
}

export interface SiteOpsAnalyticsChannelResponse extends SiteOpsAnalyticsMeta {
  capability: SiteOpsEndpointCapability
  note: string
}

export interface SiteOpsAnalyticsCacheMetricsView {
  input_tokens: number | null
  output_tokens: number | null
  cache_creation_tokens: number | null
  cache_read_tokens: number | null
  total_tokens: number | null
  effective_input_tokens: number | null
  input_mode: string
  token_quality: SiteOpsFieldQualityState
  cache_read_hit_pct: number | null
  cache_write_pct: number | null
  cache_rw_ratio: number | null
}

export interface SiteOpsAnalyticsUsageWindowView {
  start_date: string
  end_date: string
  timezone: string
  observed_at: string | null
  quality: SiteOpsFieldQualityState
  requests: number | null
  standard_usd: number | null
  actual_usd: number | null
  account_usd: number | null
  has_cost_spread: boolean
  average_duration_ms: number | null
  cache: SiteOpsAnalyticsCacheMetricsView
}

export interface SiteOpsAnalyticsOperationalTagView {
  key: string
  sub_kind?: string
  label: string
  severity: string
  reason: string
}

export interface SiteOpsAnalyticsUserRow {
  user_id: number
  email_masked: string | null
  role: string
  status: string
  created_at: string | null
  balance_usd: number | null
  concurrency_limit: number | null
  today_actual_usd: number | null
  recent_aggregate_actual_usd: number | null
  by_platform: SiteOpsAnalyticsPlatformCostBrief[] | null
  live_concurrency: SiteOpsAnalyticsUserConcurrencyView | null
  active_sub_count: number
  nearest_sub_expires_days: number | null
  enrichment_state: string
  all_time: SiteOpsAnalyticsUsageWindowView | null
  last_7d: SiteOpsAnalyticsUsageWindowView | null
  tags: SiteOpsAnalyticsOperationalTagView[] | null
}

export interface SiteOpsAnalyticsUsersSummary {
  users_seen: number
  users_coverage: string
  subs_coverage: string
  active_today_users: number
  today_actual_usd: number | null
  evaluated_users: number
  budget_deferred_users: number
  active_subs_seen: number
  expiring_soon_subs_seen: number
  queueing_users_seen: number
  tag_counts: Record<string, number>
  budget_note?: string
}

export interface SiteOpsAnalyticsUsersResponse extends SiteOpsAnalyticsMeta {
  top_n: number
  summary: SiteOpsAnalyticsUsersSummary
  users: SiteOpsAnalyticsPage<SiteOpsAnalyticsUserRow>
}

export interface SiteOpsAnalyticsUserAPIKeyCostView {
  api_key_id: number
  group_id: number | null
  group_name?: string
  status: string
  created_at: string | null
  last_used_at: string | null
  today_actual_usd: number | null
  upstream_total_actual_usd: number | null
  quality: SiteOpsFieldQualityState
}

export interface SiteOpsAnalyticsUserDetailResponse extends SiteOpsAnalyticsMeta {
  user: SiteOpsAnalyticsUserRow
  last_30d: SiteOpsAnalyticsUsageWindowView | null
  subscriptions: SiteOpsAnalyticsUserSubscriptionBrief[] | null
  api_keys: SiteOpsAnalyticsPage<SiteOpsAnalyticsUserAPIKeyCostView>
}

export interface SiteOpsAnalyticsRollingWindowView {
  limit_usd: number | null
  used_usd: number | null
  remaining_usd: number | null
  percentage: number | null
  window_start: string | null
  resets_at: string | null
  resets_in_seconds: number | null
  source: string
  quality: SiteOpsFieldQualityState
}

export interface SiteOpsAnalyticsSubscriptionTermLayerView {
  start_at: string
  end_at: string
  observed_until: string | null
  cycle_index: number
  total_cycles: number
  duration_days: number
  elapsed_hours: number
  remaining_hours: number
  forecast_status: string
  usage: SiteOpsAnalyticsUsageWindowView | null
  group_usage: SiteOpsAnalyticsUsageWindowView | null
  boundary_approx: boolean
  day_boundary_note?: string
  burn_slow_usd_day: number | null
  burn_fast_usd_day: number | null
  burn_fast_source?: string
  burn_effective_usd_day: number | null
  uncapped_estimate_usd: number | null
  projected_end_usd: number | null
  theoretical_max_usd: number | null
  cap_state: string
  capped_by_limit: boolean
  cap_binding_window?: string
  confidence: string
  reason?: string
}

export interface SiteOpsAnalyticsReferencePriceView {
  instance_id: number
  scope: SiteOpsAnalyticsPriceScope
  scope_id: number
  currency: SiteOpsAnalyticsCurrency
  monthly_minor: number
  cny_per_usd_micros: number | null
  updated_at: string
}

export interface SiteOpsAnalyticsSubscriptionPricingCompare {
  price_source: string
  reference_price: SiteOpsAnalyticsReferencePriceView | null
  effective_fx: number
  proration: number | null
  cycle_ref_native: number | null
  cycle_ref_usd: number | null
  term_ref_usd: number | null
  actual_leverage_x: number | null
  projected_leverage_x: number | null
  margin_risk: string
}

export interface SiteOpsAnalyticsSubscriptionRow {
  subscription_id: number
  user_id: number
  user_email_masked: string | null
  group_id: number
  group_name: string
  platform: string
  status: string
  cycle_state: string
  starts_at: string | null
  expires_at: string | null
  expires_in_days: number | null
  cycle_index: number
  total_cycles: number
  sub_cycle_start: string | null
  sub_cycle_end: string | null
  same_as_term: boolean
  attribution: string
  attribution_reason?: string
  daily_window: SiteOpsAnalyticsRollingWindowView
  weekly_window: SiteOpsAnalyticsRollingWindowView
  monthly_window: SiteOpsAnalyticsRollingWindowView
  pricing_compare: SiteOpsAnalyticsSubscriptionPricingCompare
}

export interface SiteOpsAnalyticsSubscriptionsResponse extends SiteOpsAnalyticsMeta {
  analytics_config: SiteOpsAnalyticsConfig
  groups: SiteOpsAnalyticsGroupOption[] | null
  subscriptions: SiteOpsAnalyticsPage<SiteOpsAnalyticsSubscriptionRow>
}

export interface SiteOpsAnalyticsSubscriptionDetailResponse extends SiteOpsAnalyticsMeta {
  analytics_config: SiteOpsAnalyticsConfig
  subscription: SiteOpsAnalyticsSubscriptionRow
  current_sub_cycle: SiteOpsAnalyticsSubscriptionTermLayerView | null
  continuous_term: SiteOpsAnalyticsSubscriptionTermLayerView | null
}

export interface SiteOpsAnalyticsModelRowView {
  model: string
  requests: number | null
  standard_usd: number | null
  actual_usd: number | null
  account_usd: number | null
  has_cost_spread: boolean
  cache: SiteOpsAnalyticsCacheMetricsView
  mismatch_requests: number | null
  has_mismatch: boolean
  quality: SiteOpsFieldQualityState
}

export interface SiteOpsAnalyticsModelMismatchRowView {
  requested_model: string
  upstream_model?: string
  requests: number | null
  actual_usd: number | null
}

export interface SiteOpsAnalyticsModelMismatchSummaryView {
  quality: SiteOpsFieldQualityState
  total_requests: number | null
  model_rows: number
  rows: SiteOpsAnalyticsModelMismatchRowView[] | null
}

export interface SiteOpsAnalyticsGroupMarketRowView {
  group_id: number
  group_name: string
  platform: string
  subscription_type?: string
  rate_multiplier: number | null
  active_sub_count: number | null
  requests: number | null
  standard_usd: number | null
  actual_usd: number | null
  account_usd: number | null
  has_cost_spread: boolean
  reference_price: SiteOpsAnalyticsReferencePriceView | null
  ref_monthly_usd: number | null
  quality: SiteOpsFieldQualityState
}

export interface SiteOpsAnalyticsMarketResponse extends SiteOpsAnalyticsMeta {
  range: SiteOpsAnalyticsRange
  date_range: SiteOpsAnalyticsDateRange
  analytics_config: SiteOpsAnalyticsConfig
  total_requests: number | null
  total_actual_usd: number | null
  total_account_usd: number | null
  has_cost_spread: boolean
  models: SiteOpsAnalyticsModelRowView[] | null
  mismatches: SiteOpsAnalyticsModelMismatchSummaryView
  groups: SiteOpsAnalyticsGroupMarketRowView[] | null
}

export interface SiteOpsAnalyticsPricingResponse {
  instance_id: number
  generation: number
  config: SiteOpsAnalyticsConfig
  prices: SiteOpsAnalyticsReferencePriceView[] | null
}

export interface SiteOpsAnalyticsPutReferencePriceInput {
  generation: number
  currency: SiteOpsAnalyticsCurrency
  monthly_minor: number
  cny_per_usd_micros?: number | null
}

export interface SiteOpsAnalyticsPlatformCostBrief {
  platform: string
  today_actual_usd: number | null
  recent_actual_usd: number | null
}

export interface SiteOpsAnalyticsUserConcurrencyView {
  current_in_use: number | null
  max_capacity: number | null
  load_percentage: number | null
  waiting_in_queue: number | null
}

export interface SiteOpsAnalyticsUserSubscriptionBrief {
  subscription_id: number
  group_id: number
  group_name: string
  platform: string
  status: string
  starts_at: string | null
  expires_at: string | null
  expires_in_days: number | null
}

export interface SiteOpsAnalyticsGroupOption {
  group_id: number
  group_name: string
  platform: string
  reference_price: SiteOpsAnalyticsReferencePriceView | null
}

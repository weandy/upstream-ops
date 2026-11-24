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

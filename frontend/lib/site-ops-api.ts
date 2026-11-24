/**
 * 自有 Sub2API 站点运维模块（/api/site-ops/*）API 封装（Wave 1 P0-A + P0-B）。
 */
import { apiFetch, type ApiError } from "@/lib/api"
import type {
  SiteOpsAccountCostView,
  SiteOpsAnalyticsLightsResponse,
  SiteOpsAnalyticsChannelResponse,
  SiteOpsAnalyticsUsersResponse,
  SiteOpsAnalyticsUserDetailResponse,
  SiteOpsAnalyticsSubscriptionsResponse,
  SiteOpsAnalyticsSubscriptionDetailResponse,
  SiteOpsAnalyticsMarketResponse,
  SiteOpsAnalyticsPricingResponse,
  SiteOpsAnalyticsConfig,
  SiteOpsAnalyticsReferencePriceView,
  SiteOpsAnalyticsPutReferencePriceInput,
  SiteOpsAnalyticsPriceScope,
  SiteOpsAnalyticsSubscriptionStatus,
  SiteOpsAnalyticsRange,
  SiteOpsAccountsEfficiencyResponse,
  SiteOpsAccountsStatusResponse,
  SiteOpsCreateInstanceInput,
  SiteOpsDailyReportPreview,
  SiteOpsDailyReportSendResult,
  SiteOpsEventsResponse,
  SiteOpsGroupsResponse,
  SiteOpsInstanceView,
  SiteOpsOverviewResponse,
  SiteOpsPoolForecastResponse,
  SiteOpsProbeReport,
  SiteOpsRefreshResult,
  SiteOpsSummaryResponse,
  SiteOpsTrendRange,
  SiteOpsTrendsResponse,
  SiteOpsUpdateAccountCostInput,
  SiteOpsUpdateInstanceInput,
} from "@/lib/site-ops-types"

export const SITE_OPS_AUTH_REQUIRED_CODE = "SITE_OPS_AUTH_REQUIRED"

/** 识别模块级 Fail-Closed 门禁拒绝（auth.enabled=false 且未开启本地只读逃生阀）。 */
export function isSiteOpsAuthRequiredError(err: unknown): boolean {
  if (!err || typeof err !== "object") return false
  const apiErr = err as ApiError
  if (apiErr.status !== 403) return false
  const body = apiErr.body as { code?: string; error?: string } | undefined
  if (body?.code === SITE_OPS_AUTH_REQUIRED_CODE) return true
  return typeof body?.error === "string" && body.error.includes(SITE_OPS_AUTH_REQUIRED_CODE)
}

export function fetchSiteOpsSummary(): Promise<SiteOpsSummaryResponse> {
  return apiFetch<SiteOpsSummaryResponse>("/site-ops/summary")
}

export function fetchSiteOpsInstances(): Promise<SiteOpsInstanceView[]> {
  return apiFetch<SiteOpsInstanceView[]>("/site-ops/instances")
}

export function createSiteOpsInstance(
  input: SiteOpsCreateInstanceInput,
): Promise<SiteOpsInstanceView> {
  return apiFetch<SiteOpsInstanceView>("/site-ops/instances", {
    method: "POST",
    body: JSON.stringify(input),
  })
}

export function updateSiteOpsInstance(
  id: number,
  input: SiteOpsUpdateInstanceInput,
): Promise<SiteOpsInstanceView> {
  return apiFetch<SiteOpsInstanceView>(`/site-ops/instances/${id}`, {
    method: "PUT",
    body: JSON.stringify(input),
  })
}

export function deleteSiteOpsInstance(id: number): Promise<{ deleted: boolean }> {
  return apiFetch<{ deleted: boolean }>(`/site-ops/instances/${id}`, {
    method: "DELETE",
  })
}

export function probeSiteOpsInstance(id: number): Promise<SiteOpsProbeReport> {
  return apiFetch<SiteOpsProbeReport>(`/site-ops/instances/${id}/probe`, {
    method: "POST",
  })
}

export function refreshSiteOpsInstance(id: number): Promise<SiteOpsRefreshResult> {
  return apiFetch<SiteOpsRefreshResult>(`/site-ops/instances/${id}/refresh`, {
    method: "POST",
  })
}

export function fetchSiteOpsOverview(id: number): Promise<SiteOpsOverviewResponse> {
  return apiFetch<SiteOpsOverviewResponse>(`/site-ops/instances/${id}/overview`)
}

export function fetchSiteOpsTrends(
  id: number,
  range: SiteOpsTrendRange = "6h",
): Promise<SiteOpsTrendsResponse> {
  const q = new URLSearchParams({ range })
  return apiFetch<SiteOpsTrendsResponse>(`/site-ops/instances/${id}/trends?${q.toString()}`)
}

export function fetchSiteOpsGroups(id: number): Promise<SiteOpsGroupsResponse> {
  return apiFetch<SiteOpsGroupsResponse>(`/site-ops/instances/${id}/groups`)
}

export function fetchSiteOpsEvents(
  id: number,
  params?: { state?: string; severity?: string; limit?: number },
): Promise<SiteOpsEventsResponse> {
  const q = new URLSearchParams()
  if (params?.state) q.set("state", params.state)
  if (params?.severity) q.set("severity", params.severity)
  if (params?.limit && params.limit > 0) q.set("limit", String(params.limit))
  const suffix = q.toString() ? `?${q.toString()}` : ""
  return apiFetch<SiteOpsEventsResponse>(`/site-ops/instances/${id}/events${suffix}`)
}

export function fetchSiteOpsAccountsStatus(
  id: number,
  params?: { status?: string; platform?: string },
): Promise<SiteOpsAccountsStatusResponse> {
  const q = new URLSearchParams({ view: "status" })
  if (params?.status) q.set("status", params.status)
  if (params?.platform) q.set("platform", params.platform)
  return apiFetch<SiteOpsAccountsStatusResponse>(
    `/site-ops/instances/${id}/accounts?${q.toString()}`,
  )
}

export function previewSiteOpsDailyReport(id: number): Promise<SiteOpsDailyReportPreview> {
  return apiFetch<SiteOpsDailyReportPreview>(`/site-ops/instances/${id}/report/preview`, {
    method: "POST",
  })
}

export function sendSiteOpsDailyReport(id: number): Promise<SiteOpsDailyReportSendResult> {
  return apiFetch<SiteOpsDailyReportSendResult>(`/site-ops/instances/${id}/report/send`, {
    method: "POST",
  })
}

export function fetchSiteOpsPoolForecast(id: number): Promise<SiteOpsPoolForecastResponse> {
  return apiFetch<SiteOpsPoolForecastResponse>(`/site-ops/instances/${id}/pool-forecast`)
}

export function fetchSiteOpsAccountsEfficiency(
  id: number,
): Promise<SiteOpsAccountsEfficiencyResponse> {
  return apiFetch<SiteOpsAccountsEfficiencyResponse>(
    `/site-ops/instances/${id}/accounts?view=efficiency`,
  )
}

export function updateSiteOpsAccountCost(
  id: number,
  accountId: number,
  input: SiteOpsUpdateAccountCostInput,
): Promise<SiteOpsAccountCostView> {
  return apiFetch<SiteOpsAccountCostView>(
    `/site-ops/instances/${id}/accounts/${accountId}/cost`,
    {
      method: "PUT",
      body: JSON.stringify(input),
    },
  )
}

type AnalyticsOptions = { signal?: AbortSignal }
type AnalyticsRead = { force?: boolean; cache_only?: boolean }

function analyticsPath(id: number, route: string, params: Record<string, string | number | boolean | undefined> = {}) {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined) query.set(key, typeof value === "boolean" ? String(Number(value)) : String(value))
  }
  const suffix = query.toString()
  return `/site-ops/instances/${id}/analytics/${route}${suffix ? "?" + suffix : ""}`
}

export function fetchSiteOpsAnalyticsLights(
  id: number,
  params: { force?: boolean } = {},
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsLightsResponse>(analyticsPath(id, "lights", params), options)
}

export function fetchSiteOpsAnalyticsChannel(
  id: number,
  params: { force?: boolean } = {},
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsChannelResponse>(analyticsPath(id, "channel-status", params), options)
}

export function fetchSiteOpsAnalyticsUsers(
  id: number,
  params: AnalyticsRead & { page?: number; top_n?: number } = {},
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsUsersResponse>(analyticsPath(id, "users", params), options)
}

export function fetchSiteOpsAnalyticsUserDetail(
  id: number,
  userId: number,
  params: { user_page?: number; key_page?: number; force?: boolean } = {},
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsUserDetailResponse>(analyticsPath(id, `users/${userId}`, params), options)
}

export function fetchSiteOpsAnalyticsSubscriptions(
  id: number,
  params: AnalyticsRead & { page?: number; status?: SiteOpsAnalyticsSubscriptionStatus; group_id?: number } = {},
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsSubscriptionsResponse>(analyticsPath(id, "subscriptions", params), options)
}

export function fetchSiteOpsAnalyticsSubscriptionDetail(
  id: number,
  subscriptionId: number,
  params: AnalyticsRead & { subscription_page?: number } = {},
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsSubscriptionDetailResponse>(analyticsPath(id, `subscriptions/${subscriptionId}`, params), options)
}

export function fetchSiteOpsAnalyticsMarket(
  id: number,
  params: AnalyticsRead & { range?: SiteOpsAnalyticsRange } = {},
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsMarketResponse>(analyticsPath(id, "market", params), options)
}

export function fetchSiteOpsAnalyticsPricing(
  id: number,
  options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsPricingResponse>(analyticsPath(id, "pricing"), options)
}

export function updateSiteOpsAnalyticsConfig(id: number, input: SiteOpsAnalyticsConfig, options: AnalyticsOptions = {}) {
  return apiFetch<SiteOpsAnalyticsConfig>(analyticsPath(id, "pricing/config"), {
    ...options, method: "PUT", body: JSON.stringify(input),
  })
}

export function putSiteOpsReferencePrice(
  id: number, scope: SiteOpsAnalyticsPriceScope, scopeId: number,
  input: SiteOpsAnalyticsPutReferencePriceInput, options: AnalyticsOptions = {},
) {
  return apiFetch<SiteOpsAnalyticsReferencePriceView>(analyticsPath(id, `reference-prices/${scope}/${scopeId}`), {
    ...options, method: "PUT", body: JSON.stringify(input),
  })
}

export function clearSiteOpsReferencePrice(
  id: number, scope: SiteOpsAnalyticsPriceScope, scopeId: number, generation: number, options: AnalyticsOptions = {},
) {
  return apiFetch<{ deleted: boolean }>(analyticsPath(id, `reference-prices/${scope}/${scopeId}`, { generation }), {
    ...options, method: "DELETE",
  })
}

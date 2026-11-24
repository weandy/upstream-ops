/**
 * 自有 Sub2API 站点运维模块（/api/site-ops/*）API 封装（Wave 1 P0-A + P0-B）。
 */
import { apiFetch, type ApiError } from "@/lib/api"
import type {
  SiteOpsCreateInstanceInput,
  SiteOpsGroupsResponse,
  SiteOpsInstanceView,
  SiteOpsOverviewResponse,
  SiteOpsProbeReport,
  SiteOpsRefreshResult,
  SiteOpsSummaryResponse,
  SiteOpsTrendRange,
  SiteOpsTrendsResponse,
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

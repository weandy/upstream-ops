import type {
  SiteOpsAccountStatusItem,
  SiteOpsAccountsStatusResponse,
  SiteOpsAnalyticsMeta,
  SiteOpsAnalyticsPricingResponse,
  SiteOpsOverviewResponse,
} from "./site-ops-types"

const personalDomains = [
  "users", "users_usage", "user_concurrency", "user_stats", "user_keys", "user_keys_usage",
  "subscriptions", "subscription_progress", "subscription_cycle", "subscription_term",
]

export function analyticsDeadline(data: SiteOpsAnalyticsMeta | SiteOpsAnalyticsPricingResponse, now = Date.now()) {
  if (!("domains" in data)) return undefined
  const keys = "models" in data.domains ? ["models", "model_mismatches", "market_groups"]
    : "risk_control" in data.domains || "backups" in data.domains ? ["risk_control", "backups"]
    : "channel_monitor" in data.domains ? ["channel_monitor"]
    : personalDomains
  const times = keys.map(key => Date.parse(data.domains[key]?.expires_at ?? "")).filter(Number.isFinite)
  if (!times.length) return undefined
  const minExpires = Math.min(...times)
  const asOf = Date.parse(data.as_of)
  return Number.isFinite(asOf) ? now + Math.max(0, minExpires - asOf) : minExpires
}

export type AnalyticsReadMode = "select" | "refresh" | "reprice"

export function subscriptionCacheOnly(mode: AnalyticsReadMode, backingOff: boolean) {
  return mode === "reprice" || (mode === "select" && backingOff)
}

export function isAbnormalAccountStatus(
  item: Pick<SiteOpsAccountStatusItem, "has_error" | "rate_limited" | "overloaded" | "schedulable" | "ops_available">,
): boolean {
  if (item.has_error || item.rate_limited || item.overloaded) return true
  return item.schedulable === true && !item.ops_available
}

export function countAbnormalAccounts(
  accounts: SiteOpsAccountsStatusResponse | undefined,
  latest: SiteOpsOverviewResponse["latest"] | undefined,
): number {
  if (accounts) {
    if (accounts.trust_state !== "trusted") return 0
    return (accounts.items ?? []).filter(isAbnormalAccountStatus).length
  }
  if (!latest || latest.field_quality?.account_availability !== "trusted") return 0
  return (
    (latest.accounts_error ?? 0) +
    (latest.accounts_rate_limited ?? 0) +
    (latest.accounts_overloaded ?? 0)
  )
}

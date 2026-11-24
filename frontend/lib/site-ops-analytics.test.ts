import assert from "node:assert/strict"
import test from "node:test"
import type {
  SiteOpsAccountStatusItem,
  SiteOpsAccountsStatusResponse,
  SiteOpsAnalyticsMeta,
  SiteOpsOverviewResponse,
} from "./site-ops-types"

const {
  analyticsDeadline,
  countAbnormalAccounts,
  isAbnormalAccountStatus,
  subscriptionCacheOnly,
}: typeof import("./site-ops-analytics") =
  await import(new URL("./site-ops-analytics.ts", import.meta.url).href)

test("浏览器时钟偏移时按业务域剩余 TTL 清屏", () => {
  const server = Date.UTC(2025, 0, 1)
  const domain = (ms: number) => ({
    quality: "trusted" as const, expires_at: new Date(server + ms).toISOString(),
    observed_at: null, verified_at: null, reason: "",
  })
  const base: SiteOpsAnalyticsMeta = {
    generation: 1, instance_id: 1, as_of: new Date(server).toISOString(),
    from_cache: true, backoff_until: null, domains: {},
  }
  for (const local of [server + 600_000, server - 600_000]) {
    for (const key of ["users", "subscriptions", "user_keys", "subscription_progress"]) {
      assert.equal(analyticsDeadline({ ...base, domains: { [key]: domain(120_000), groups: domain(1) } }, local), local + 120_000)
    }
    assert.equal(analyticsDeadline({ ...base, domains: { users: domain(120_000), user_stats: domain(30_000) } }, local), local + 30_000)
    assert.equal(analyticsDeadline({ ...base, domains: { models: domain(120_000), market_groups: domain(45_000), subscriptions: domain(1) } }, local), local + 45_000)
    assert.equal(analyticsDeadline({ ...base, domains: { risk_control: domain(120_000), backups: domain(20_000) } }, local), local + 20_000)
    assert.equal(analyticsDeadline({ ...base, domains: { users: domain(-1) } }, local), local)
    assert.equal(analyticsDeadline({ ...base, as_of: "", domains: { users: domain(10_000) } }, local), server + 10_000)
  }
})

test("进入订阅首面可在 LRU 未命中时读取上游", () => {
  assert.equal(subscriptionCacheOnly("select", false), false)
  assert.equal(subscriptionCacheOnly("select", true), true)
  assert.equal(subscriptionCacheOnly("refresh", false), false)
  assert.equal(subscriptionCacheOnly("refresh", true), false)
  assert.equal(subscriptionCacheOnly("reprice", false), true)
})

function makeAccount(partial: Partial<SiteOpsAccountStatusItem> = {}): SiteOpsAccountStatusItem {
  return {
    account_id: 1,
    name: "acc-1",
    platform: "deepseek",
    ops_available: true,
    rate_limited: false,
    overloaded: false,
    has_error: false,
    schedulable: true,
    ...partial,
  }
}

test("上游账号角标排除手动调度关闭，仅统计真实异常与开启调度但不可用", () => {
  assert.equal(isAbnormalAccountStatus(makeAccount()), false)
  // 手动关闭调度（无报错/限流/过载）不计入异常角标
  assert.equal(isAbnormalAccountStatus(makeAccount({ schedulable: false, ops_available: false })), false)
  // 未返回调度状态且无故障信号时不计入异常角标，与总览兜底口径一致
  assert.equal(isAbnormalAccountStatus(makeAccount({ schedulable: undefined, ops_available: false })), false)
  // 明确开启调度但不可用时计入异常
  assert.equal(isAbnormalAccountStatus(makeAccount({ schedulable: true, ops_available: false })), true)
  // 报错 / 限流 / 过载无论调度开关均计入异常
  assert.equal(isAbnormalAccountStatus(makeAccount({ schedulable: false, ops_available: false, has_error: true })), true)
  assert.equal(isAbnormalAccountStatus(makeAccount({ rate_limited: true })), true)
  assert.equal(isAbnormalAccountStatus(makeAccount({ overloaded: true })), true)

  const trustedAccounts: SiteOpsAccountsStatusResponse = {
    instance_id: 1,
    trust_state: "trusted",
    total_count: 4,
    available_count: 1,
    rate_limit_count: 1,
    overload_count: 0,
    error_count: 0,
    items: [
      makeAccount({ account_id: 1 }),
      makeAccount({ account_id: 2, schedulable: false, ops_available: false }),
      makeAccount({ account_id: 3, schedulable: true, ops_available: false }),
      makeAccount({ account_id: 4, rate_limited: true, ops_available: false }),
    ],
  }
  assert.equal(countAbnormalAccounts(trustedAccounts, undefined), 2)
  assert.equal(countAbnormalAccounts({ ...trustedAccounts, trust_state: "stale" }, undefined), 0)

  const overviewLatest: NonNullable<SiteOpsOverviewResponse["latest"]> = {
    observed_at: "2026-11-24T16:00:00Z",
    verified_at: "2026-11-24T16:00:00Z",
    reachable: true,
    probe_latency_ms: 120,
    field_quality: { account_availability: "trusted" },
    conc_trust_state: "trusted",
    accounts_total: 6,
    accounts_ops_available: 5,
    accounts_error: 0,
    accounts_rate_limited: 1,
    accounts_overloaded: 1,
  }
  assert.equal(countAbnormalAccounts(undefined, overviewLatest), 2)
  assert.equal(
    countAbnormalAccounts(undefined, { ...overviewLatest, field_quality: { account_availability: "stale" } }),
    0,
  )
})


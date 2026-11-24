import assert from "node:assert/strict"
import test from "node:test"
import type { SiteOpsAnalyticsMeta } from "./site-ops-types"

const { analyticsDeadline, subscriptionCacheOnly }: typeof import("./site-ops-analytics") =
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


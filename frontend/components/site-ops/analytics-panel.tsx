import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { analyticsDeadline, subscriptionCacheOnly, type AnalyticsReadMode as ReadMode } from "@/lib/site-ops-analytics"
import type { ApiError } from "@/lib/api"
import { money } from "@/lib/format"
import {
  clearSiteOpsReferencePrice, fetchSiteOpsAnalyticsChannel, fetchSiteOpsAnalyticsLights,
  fetchSiteOpsAnalyticsMarket, fetchSiteOpsAnalyticsPricing, fetchSiteOpsAnalyticsSubscriptionDetail,
  fetchSiteOpsAnalyticsSubscriptions, fetchSiteOpsAnalyticsUserDetail, fetchSiteOpsAnalyticsUsers,
  isSiteOpsAuthRequiredError, putSiteOpsReferencePrice, updateSiteOpsAnalyticsConfig,
} from "@/lib/site-ops-api"
import type {
  SiteOpsAnalyticsChannelResponse, SiteOpsAnalyticsLightsResponse, SiteOpsAnalyticsMarketResponse,
  SiteOpsAnalyticsMeta, SiteOpsAnalyticsPricingResponse, SiteOpsAnalyticsRange, SiteOpsAnalyticsSubscriptionDetailResponse,
  SiteOpsAnalyticsSubscriptionsResponse, SiteOpsAnalyticsSubscriptionStatus, SiteOpsAnalyticsUserDetailResponse,
  SiteOpsAnalyticsUsersResponse, SiteOpsInstanceView,
} from "@/lib/site-ops-types"
import { AnalyticsLightsBar, analyticsTime } from "./analytics-lights-bar"
import { AnalyticsMarketPanel } from "./analytics-market-panel"
import { AnalyticsPricingDialog, type PriceTarget, type PricingChange } from "./analytics-pricing-dialog"
import { AnalyticsSubscriptionDrawer } from "./analytics-subscription-drawer"
import { AnalyticsSubscriptionsTable } from "./analytics-subscriptions-table"
import { AnalyticsUserDrawer } from "./analytics-user-drawer"
import { AnalyticsUsersTable, analyticsTags } from "./analytics-users-table"
import { FieldQualityBadge, MetricValue } from "./quality-badge"

type View = "users" | "subscriptions" | "market"
type Result = SiteOpsAnalyticsMeta | SiteOpsAnalyticsPricingResponse
type Entry<T> = { data?: T; busy?: boolean; error?: string; expires?: number; expired?: boolean }
const expiredText = "缓存已过期，点击重新读取"
const conflictText = "站点配置已变更，请重新打开"

function errorMessage(err: unknown) {
  const { status, body } = (err ?? {}) as ApiError
  const code = (body as { code?: string } | undefined)?.code
  if (status === 409) return conflictText
  if (status === 502 && code === "SITE_OPS_UNAUTHORIZED") return "上游凭据已失效，请检查该站点的授权。"
  if (status === 429) return code === "SITE_OPS_BUDGET_EXCEEDED" ? "审计预算不足，请稍后重试。" : "上游正在限流，请稍后重试。"
  if (status === 404) return "当前目录页已找不到该记录，请重新读取列表。"
  if (status === 400) return "参数无效，请检查输入后重试。"
  return "读取或保存失败，请稍后重试。"
}

// 每个通道独立持有序号、请求和投影。只有模型视图保留两个窗口，其他通道只保留当前页。
function useAnalyticsChannel<T extends Result>(onResult: (data: Result) => void, onError: (err: unknown) => void, keepWindows = false) {
  const sequence = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const cache = useRef<Record<string, Entry<T>>>({})
  const [entries, setEntries] = useState(cache.current)
  const publish = useCallback((next: Record<string, Entry<T>>) => {
    cache.current = next
    setEntries(next)
  }, [])
  const abort = useCallback(() => {
    sequence.current++
    controller.current?.abort()
    controller.current = null
  }, [])
  useLayoutEffect(() => () => {
    abort()
    cache.current = {}
  }, [abort])

  const stop = useCallback((clear = false) => {
    abort()
    const next = clear ? {} : { ...cache.current }
    for (const [key, entry] of Object.entries(next)) {
      if (entry.busy) delete next[key]
    }
    publish(next)
  }, [abort, publish])

  const run = useCallback(async (key: string, request: (signal: AbortSignal) => Promise<T>, mode: ReadMode | "write" = "select") => {
    const old = cache.current[key]
    if (mode === "select" && old?.busy && controller.current) return
    if (mode === "select" && (old?.data || old?.expired)) {
      if (controller.current) stop()
      if (old.expires !== undefined && old.expires <= Date.now()) {
        publish({ ...cache.current, [key]: { expired: true } })
      }
      return
    }
    abort()
    const seq = sequence.current
    const active = new AbortController()
    controller.current = active
    const next = keepWindows ? { ...cache.current } : {}
    for (const [other, entry] of Object.entries(next)) {
      if (entry.busy) delete next[other]
    }
    const previous = mode === "write" ? old?.data : undefined
    publish({ ...next, [key]: { busy: true, data: previous } })
    try {
      const data = await request(active.signal)
      if (seq !== sequence.current) return
      onResult(data)
      const expires = analyticsDeadline(data)
      const expired = expires !== undefined && expires <= Date.now()
      publish({ ...cache.current, [key]: expired ? { expired: true } : { data, expires } })
      return !expired
    } catch (err) {
      if (seq !== sequence.current || (err as Error)?.name === "AbortError") return
      onError(err)
      publish({ ...cache.current, [key]: { data: previous, error: errorMessage(err) } })
    } finally {
      if (seq === sequence.current) controller.current = null
    }
  }, [abort, keepWindows, onError, onResult, publish, stop])

  const nextExpiry = Math.min(...Object.values(entries).map(e => e.expires ?? Infinity))
  useLayoutEffect(() => {
    if (!Number.isFinite(nextExpiry)) return
    function expire() {
      const next = { ...cache.current }
      let changed = false
      for (const [key, entry] of Object.entries(next)) {
        if (entry.expires !== undefined && entry.expires <= Date.now()) {
          next[key] = { expired: true }
          changed = true
        }
      }
      if (changed) publish(next)
    }
    const timer = window.setTimeout(expire, Math.max(0, nextExpiry - Date.now()))
    document.addEventListener("visibilitychange", expire)
    expire()
    return () => { window.clearTimeout(timer); document.removeEventListener("visibilitychange", expire) }
  }, [nextExpiry, publish])
  return { entries, run, stop }
}

const domainLabels: Record<string, string> = {
  users: "用户目录", users_usage: "近段用量", user_concurrency: "实时并发", user_stats: "精算统计",
  subscriptions: "订阅目录", groups: "分组目录", prices: "本地定价", user_keys: "Key 目录",
  user_keys_usage: "Key 消耗", subscription_progress: "滚动用量", subscription_cycle: "当前周期",
  subscription_term: "连续总期", market_groups: "分组消耗", models: "模型消耗", model_mismatches: "模型不一致",
  channel_monitor: "通道监控", risk_control: "风控", backups: "备份",
}
function ReadState({ entry, retry, disabled }: { entry?: Entry<SiteOpsAnalyticsMeta>; retry: () => void; disabled: boolean }) {
  if (entry?.busy) return <p role="status" className="text-sm text-muted-foreground">正在读取…</p>
  if (entry?.expired || entry?.error) return <p role="status" className="flex items-center gap-2 text-sm">
    {entry.error || expiredText}<Button size="sm" variant="outline" disabled={disabled} onClick={retry}>重新读取</Button></p>
  const degraded = Object.entries(entry?.data?.domains ?? {}).filter(([, m]) => m.quality !== "trusted")
  return degraded.length ? <div className="flex flex-wrap gap-2 text-xs">{degraded.map(([key, m]) =>
    <span key={key}>{domainLabels[key] ?? key} <FieldQualityBadge quality={m.quality} reason={m.reason} /></span>)}</div> : null
}

type Props = { instance: SiteOpsInstanceView; onAuthRequired: () => void }

export function AnalyticsPanel(props: Props) {
  const [scopeError, setScopeError] = useState("")
  if (scopeError) return <div role="alert" className="flex items-center gap-3 rounded-lg border p-4 text-sm">{scopeError}
    <Button size="sm" variant="outline" onClick={() => setScopeError("")}>重新打开</Button></div>
  return <AnalyticsSession {...props} onScopeError={setScopeError} />
}

function AnalyticsSession({ instance, onAuthRequired, onScopeError }: Props & { onScopeError: (message: string) => void }) {
  const { id, generation } = instance
  const [view, setView] = useState<View>("users")
  const [tag, setTag] = useState("")
  const [userQuery, setUserQuery] = useState({ page: 1, top_n: 5 })
  const [subQuery, setSubQuery] = useState<{ page: number; status: SiteOpsAnalyticsSubscriptionStatus; group_id?: number }>({ page: 1, status: "active" })
  const [range, setRange] = useState<SiteOpsAnalyticsRange>("30d")
  const [userId, setUserId] = useState<number>()
  const [keyPage, setKeyPage] = useState(1)
  const [subId, setSubId] = useState<number>()
  const [dialog, setDialog] = useState<{ target?: PriceTarget }>()
  const [observedBackoff, setObservedBackoff] = useState(0)
  const [now, setNow] = useState(Date.now)
  const backoff = Math.max(observedBackoff, Date.parse(instance.backoff_until ?? "") || 0)
  const backingOff = backoff > now
  useEffect(() => {
    if (!backingOff) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [backingOff])

  const received = useCallback((data: Result) => {
    if (data.generation !== generation || data.instance_id !== id) throw Object.assign(new Error(conflictText), { status: 409 })
    if ("backoff_until" in data && data.backoff_until) {
      const until = Date.parse(data.backoff_until)
      if (Number.isFinite(until)) { setNow(Date.now()); setObservedBackoff(previous => Math.max(previous, until)) }
    }
  }, [id, generation])
  const failed = useCallback((err: unknown) => {
    if (isSiteOpsAuthRequiredError(err)) onAuthRequired()
    const api = err as ApiError
    if (api?.status === 502 && (api.body as { code?: string })?.code === "SITE_OPS_UNAUTHORIZED") onScopeError(errorMessage(err))
  }, [onAuthRequired, onScopeError])
  const readFailed = useCallback((err: unknown) => {
    if ((err as ApiError)?.status === 409) onScopeError(conflictText)
    else failed(err)
  }, [failed, onScopeError])
  const lights = useAnalyticsChannel<SiteOpsAnalyticsLightsResponse>(received, readFailed)
  const channel = useAnalyticsChannel<SiteOpsAnalyticsChannelResponse>(received, readFailed)
  const users = useAnalyticsChannel<SiteOpsAnalyticsUsersResponse>(received, readFailed)
  const userDetail = useAnalyticsChannel<SiteOpsAnalyticsUserDetailResponse>(received, readFailed)
  const subscriptions = useAnalyticsChannel<SiteOpsAnalyticsSubscriptionsResponse>(received, readFailed)
  const subDetail = useAnalyticsChannel<SiteOpsAnalyticsSubscriptionDetailResponse>(received, readFailed)
  const market = useAnalyticsChannel<SiteOpsAnalyticsMarketResponse>(received, readFailed, true)
  const pricing = useAnalyticsChannel<SiteOpsAnalyticsPricingResponse>(received, failed)

  const runLights = lights.run
  const runUsers = users.run
  useEffect(() => {
    let mounted = true
    queueMicrotask(() => {
      if (!mounted) return
      void runLights("lights", signal => fetchSiteOpsAnalyticsLights(id, {}, { signal }))
      void runUsers("1:5", signal => fetchSiteOpsAnalyticsUsers(id, { page: 1, top_n: 5 }, { signal }))
    })
    return () => { mounted = false }
  }, [id, runLights, runUsers])

  const userKey = (q: typeof userQuery) => `${q.page}:${q.top_n}`
  const subKey = (q: typeof subQuery) => `${q.page}:${q.status}:${q.group_id ?? ""}`
  const u = users.entries[userKey(userQuery)]
  const s = subscriptions.entries[subKey(subQuery)]
  const m = market.entries[range]
  const ud = userDetail.entries[`${userId}:${keyPage}`]
  const sd = subDetail.entries[String(subId)]
  const price = pricing.entries.pricing
  // 保存时标记已读的隐藏视图，下次展示只用缓存重算。
  const repricing = useRef(new Set<string>())
  function readUsers(q = userQuery, mode: ReadMode = "select") {
    setUserQuery(q)
    void users.run(userKey(q), signal => fetchSiteOpsAnalyticsUsers(id, { ...q, force: mode === "refresh", cache_only: mode === "reprice" || backingOff }, { signal }), mode)
  }
  function readSubscriptions(q = subQuery, mode: ReadMode = "select") {
    setSubQuery(q)
    const key = subKey(q)
    if (repricing.current.delete("sub:" + key) && mode === "select" && !subscriptions.entries[key]?.expired) mode = "reprice"
    const cacheOnly = subscriptionCacheOnly(mode, backingOff)
    void subscriptions.run(key, signal => fetchSiteOpsAnalyticsSubscriptions(id, { ...q, force: mode === "refresh", cache_only: cacheOnly }, { signal }), mode)
  }
  function readMarket(r = range, mode: ReadMode = "select") {
    setRange(r)
    if (repricing.current.delete(r) && mode === "select" && !market.entries[r]?.expired) mode = "reprice"
    void market.run(r, signal => fetchSiteOpsAnalyticsMarket(id, { range: r, force: mode === "refresh", cache_only: mode === "reprice" || backingOff }, { signal }), mode)
  }
  function readUserDetail(uid: number, page: number, force = false) {
    setUserId(uid); setKeyPage(page)
    void userDetail.run(`${uid}:${page}`, signal => fetchSiteOpsAnalyticsUserDetail(id, uid, { user_page: userQuery.page, key_page: page, force }, { signal }), force ? "refresh" : "select")
  }
  function readSubDetail(sid: number, mode: ReadMode = "select") {
    setSubId(sid)
    void subDetail.run(String(sid), signal => fetchSiteOpsAnalyticsSubscriptionDetail(id, sid, { subscription_page: subQuery.page, force: mode === "refresh", cache_only: mode === "reprice" }, { signal }), mode)
  }
  function readLights() {
    void lights.run("lights", signal => fetchSiteOpsAnalyticsLights(id, { force: true }, { signal }), "refresh")
  }
  function checkChannel() {
    void channel.run("channel", signal => fetchSiteOpsAnalyticsChannel(id, { force: true }, { signal }), "refresh")
  }
  function switchView(next: View, localOnly = false) {
    if (next === view) return
    if (view === "users") users.stop()
    if (view === "subscriptions") subscriptions.stop()
    if (view === "market") market.stop()
    userDetail.stop(true); subDetail.stop(true)
    setUserId(undefined); setSubId(undefined); setView(next)
    if (localOnly) return
    if (next === "users") readUsers()
    if (next === "subscriptions") readSubscriptions()
    if (next === "market") readMarket()
  }
  function refreshView() {
    readLights()
    if (view === "users") readUsers(userQuery, "refresh")
    if (view === "subscriptions") readSubscriptions(subQuery, "refresh")
    if (view === "market") readMarket(range, "refresh")
  }
  function openPricing(target?: PriceTarget) {
    setDialog({ target })
    void pricing.run("pricing", signal => fetchSiteOpsAnalyticsPricing(id, { signal }), "refresh")
  }
  async function save(change: PricingChange) {
    if (!price?.data || price.busy) return
    const original = price.data
    const saved = await pricing.run("pricing", async signal => {
      if (change.kind === "config") await updateSiteOpsAnalyticsConfig(id, change.input, { signal })
      else if (change.kind === "price") await putSiteOpsReferencePrice(id, change.target.scope, change.target.id, change.input, { signal })
      else await clearSiteOpsReferencePrice(id, change.target.scope, change.target.id, change.generation, { signal })
      return original
    }, "write")
    if (!saved) return
    setDialog(undefined); pricing.stop(true)
    if (s?.data) repricing.current.add("sub:" + subKey(subQuery))
    for (const [r, entry] of Object.entries(market.entries)) if (entry.data) repricing.current.add(r)
    if (view === "subscriptions" && (s?.data || s?.busy)) readSubscriptions(subQuery, "reprice")
    if (view === "market" && (m?.data || m?.busy)) readMarket(range, "reprice")
    if (subId && (sd?.data || sd?.busy)) readSubDetail(subId, "reprice")
  }

  const summary = u?.data?.summary
  const domains = u?.data?.domains
  const summaryCards = [
    ["本页今日消耗", <MetricValue value={summary?.today_actual_usd} format={money} quality={domains?.users_usage?.quality} />, `活跃用户 ${domains?.users_usage?.quality === "trusted" ? summary?.active_today_users : "—"}`],
    ["可见订阅目录 · 活跃", <MetricValue value={summary?.active_subs_seen} quality={domains?.subscriptions?.quality} />, `7 天内临期 ${domains?.subscriptions?.quality === "trusted" ? summary?.expiring_soon_subs_seen : "—"}`],
    ["本页已精算 / 用户", <span>{domains?.users?.quality === "trusted" && summary ? `${summary.evaluated_users} / ${summary.users_seen}` : "—"}</span>, summary?.budget_note || "全时段与近 7 天补查"],
    ["本页排队用户", <MetricValue value={summary?.queueing_users_seen} quality={domains?.user_concurrency?.quality} />, "按并发队列观测"],
  ] as const
  const visible = view === "users" ? u : view === "subscriptions" ? s : m
  return <div className="min-w-0 space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="font-semibold">运营分析</h2>
      <Button size="sm" variant="outline" disabled={backingOff || lights.entries.lights?.busy || visible?.busy} onClick={refreshView}>重新读取</Button></div>
    {backingOff && <p role="status" className="rounded-md border border-amber-500/40 p-2 text-sm">上游退避至 {analyticsTime(new Date(backoff).toISOString())}（UTC+8）· 剩余 {Math.ceil((backoff - now) / 1000)} 秒</p>}
    <AnalyticsLightsBar data={lights.entries.lights?.data} channel={channel.entries.channel?.data} busy={!!channel.entries.channel?.busy} disabled={backingOff} onCheck={checkChannel} />
    <ReadState entry={lights.entries.lights} retry={readLights} disabled={backingOff} />
    <ReadState entry={channel.entries.channel} retry={checkChannel} disabled={backingOff} />
    <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">{summaryCards.map(([label, value, note]) =>
      <div key={label} className="rounded-lg border px-3 py-2"><p className="text-xs text-muted-foreground">{label}</p>
        <div className="my-1 text-lg font-semibold tabular-nums">{value}</div><p className="text-xs text-muted-foreground">{note}</p></div>)}</div>
    <div aria-label="本页运营提醒" className="flex flex-wrap gap-2">{Object.entries(analyticsTags).map(([key, label]) => {
      const count = summary?.tag_counts[key]
      const observed = domains?.users?.quality === "trusted" && (
        key === "concurrency_queued" ? domains?.user_concurrency?.quality === "trusted" :
        key === "cache_anomaly" ? (summary?.evaluated_users ?? 0) > 0 :
        key === "dormant_high_balance" || key === "high_value_at_risk" ? domains?.subscriptions?.quality === "trusted" && domains?.users_usage?.quality === "trusted" :
        key === "expired_unrenewed" ? summary?.subs_coverage === "complete" && domains?.subscriptions?.quality === "trusted" :
        domains?.subscriptions?.quality === "trusted"
      )
      return <Button key={key} size="sm" variant={tag === key ? "secondary" : "outline"} aria-pressed={tag === key} disabled={!summary}
        onClick={() => { setTag(tag === key ? "" : key); switchView("users", true) }}>{label} {count ?? (observed ? 0 : "—")}</Button>
    })}</div>
    <Tabs value={view} onValueChange={value => switchView(value as View)}>
      <div className="flex flex-wrap items-center justify-between gap-2"><TabsList className="h-auto max-w-full flex-wrap">
        <TabsTrigger value="users">用户运营画像</TabsTrigger><TabsTrigger value="subscriptions">订阅三层周期</TabsTrigger><TabsTrigger value="market">模型与分组</TabsTrigger>
      </TabsList><Button size="sm" variant="outline" onClick={() => openPricing()}>参考收款与汇率设置</Button></div>
      <ReadState entry={visible} disabled={backingOff} retry={() => {
        if (view === "users") readUsers(userQuery, "refresh")
        if (view === "subscriptions") readSubscriptions(subQuery, "refresh")
        if (view === "market") readMarket(range, "refresh")
      }} />
      <TabsContent value="users">{u?.data && <AnalyticsUsersTable data={u.data} tag={tag} onTag={setTag} disabled={backingOff}
        onPage={page => readUsers({ ...userQuery, page })} onTop={top_n => readUsers({ ...userQuery, top_n })} onDetail={uid => readUserDetail(uid, 1)} />}</TabsContent>
      <TabsContent value="subscriptions">{s?.data && <AnalyticsSubscriptionsTable data={s.data} status={subQuery.status} group={subQuery.group_id} disabled={backingOff}
        onPage={page => readSubscriptions({ ...subQuery, page })} onStatus={status => readSubscriptions({ ...subQuery, status, page: 1 })}
        onGroup={group_id => readSubscriptions({ ...subQuery, group_id, page: 1 })} onDetail={sid => readSubDetail(sid)} onPrice={sid => openPricing({ scope: "subscription", id: sid })} />}</TabsContent>
      <TabsContent value="market"><AnalyticsMarketPanel data={m?.data} range={range} disabled={backingOff} onRange={r => readMarket(r)}
        onPrice={gid => openPricing({ scope: "group", id: gid })} /></TabsContent>
    </Tabs>
    {userId && <AnalyticsUserDrawer id={userId} data={ud?.data} disabled={backingOff} onPage={p => readUserDetail(userId, p)} onClose={() => { userDetail.stop(true); setUserId(undefined) }}>
      <ReadState entry={ud} retry={() => readUserDetail(userId, keyPage, true)} disabled={backingOff} /></AnalyticsUserDrawer>}
    {subId && <AnalyticsSubscriptionDrawer id={subId} data={sd?.data} onPrice={() => openPricing({ scope: "subscription", id: subId })}
      onClose={() => { subDetail.stop(true); setSubId(undefined) }}>
      <ReadState entry={sd} retry={() => readSubDetail(subId, "refresh")} disabled={backingOff} /></AnalyticsSubscriptionDrawer>}
    {dialog && <AnalyticsPricingDialog initial={dialog.target} data={price?.data} busy={!!price?.busy} error={price?.error}
      onSave={change => void save(change)} onClose={() => { pricing.stop(true); setDialog(undefined) }} />}
  </div>
}

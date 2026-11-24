import { useMemo, useState } from "react"
import { ArrowDown, ArrowUp, ArrowUpDown, Check, Copy, Search } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { positiveIds, toggleValue } from "@/lib/site-ops-mail-state"
import { copyText } from "@/lib/clipboard"
import { decimal, money } from "@/lib/format"
import type {
  SiteOpsAnalyticsUserRow,
  SiteOpsAnalyticsUsersResponse,
  SiteOpsFieldQualityState,
  SiteOpsUserActivityState,
} from "@/lib/site-ops-types"
import {
  PageControls,
  SubscriptionPaceBar,
  selectClass,
} from "./analytics-subscriptions-table"
import { FieldQualityBadge, MetricNote, MetricValue } from "./quality-badge"

export function CacheRate({
  value,
  unit = "%",
  quality,
}: {
  value?: number | null
  unit?: string
  quality?: SiteOpsFieldQualityState
}) {
  if (quality && quality !== "trusted") return <FieldQualityBadge quality={quality} />
  if (value == null || !Number.isFinite(value)) {
    return (
      <span className="inline-flex items-center gap-1 text-muted-foreground">
        —
        <MetricNote label="缓存指标口径">Token 明细不足，暂无法计算缓存命中率。</MetricNote>
      </span>
    )
  }
  return <MetricValue value={value} unit={unit} />
}

type SortKey = "balance" | "today" | "d7" | "total" | "peakConc" | "cache"
type QuickCond = "will_hit" | "under_use" | "peak_high" | "low_cache" | "expiring"

function userDisplayEmail(u: SiteOpsAnalyticsUserRow) {
  return u.email || u.email_masked || `U#${u.user_id}`
}

function userActivity(u: SiteOpsAnalyticsUserRow): SiteOpsUserActivityState {
  if (
    u.activity_state === "active" ||
    u.activity_state === "drop" ||
    u.activity_state === "idle_balance" ||
    u.activity_state === "low"
  ) {
    return u.activity_state
  }
  if (u.tags?.some((t) => t.key === "high_value_at_risk")) return "drop"
  if (u.tags?.some((t) => t.key === "dormant_high_balance")) return "idle_balance"
  if (
    (u.today_actual_usd ?? 0) > 0 ||
    (u.last_7d?.actual_usd ?? 0) > 0 ||
    (u.recent_aggregate_actual_usd ?? 0) > 0
  ) {
    return "active"
  }
  return "low"
}

function userCachePct(u: SiteOpsAnalyticsUserRow): number | null {
  const w7 = u.last_7d
  if (w7?.quality === "trusted" && w7.cache.token_quality === "trusted" && w7.cache.cache_read_hit_pct != null) {
    return w7.cache.cache_read_hit_pct
  }
  const all = u.all_time
  if (all?.quality === "trusted" && all.cache.token_quality === "trusted" && all.cache.cache_read_hit_pct != null) {
    return all.cache.cache_read_hit_pct
  }
  return null
}

function userPeakConc(u: SiteOpsAnalyticsUserRow): number {
  const c = u.live_concurrency
  if (!c) return 0
  return Math.max(c.peak_in_use ?? 0, c.current_in_use ?? 0)
}

function userConcMax(u: SiteOpsAnalyticsUserRow): number {
  return u.live_concurrency?.max_capacity ?? u.concurrency_limit ?? 0
}

function userPeakWait(u: SiteOpsAnalyticsUserRow): number {
  const c = u.live_concurrency
  if (!c) return 0
  return Math.max(c.peak_waiting ?? 0, c.waiting_in_queue ?? 0)
}

function userTotalCost(u: SiteOpsAnalyticsUserRow): number {
  return u.all_time?.actual_usd ?? u.recent_aggregate_actual_usd ?? 0
}

function userD7Cost(u: SiteOpsAnalyticsUserRow): number {
  return u.last_7d?.actual_usd ?? u.today_actual_usd ?? 0
}

function userPrimaryPlan(u: SiteOpsAnalyticsUserRow): string {
  const firstSub = u.active_subscriptions?.[0]
  if (firstSub?.group_name) return firstSub.group_name
  if (u.active_sub_count > 0) return `订阅 · ${u.active_sub_count}份`
  return "按量余额"
}

const activityBadgeMeta: Record<SiteOpsUserActivityState, { label: string; cls: string }> = {
  active: {
    label: "持续活跃",
    cls: "border-emerald-500/35 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  },
  drop: {
    label: "消费骤降",
    cls: "border-destructive/40 bg-destructive/10 text-destructive",
  },
  idle_balance: {
    label: "余额闲置",
    cls: "border-sky-500/35 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  },
  low: {
    label: "低频",
    cls: "text-muted-foreground",
  },
}

const metricOptions: Array<{ key: SortKey; label: string }> = [
  { key: "today", label: "今日" },
  { key: "d7", label: "近7天" },
  { key: "total", label: "累计" },
  { key: "balance", label: "余额" },
  { key: "peakConc", label: "峰值并发" },
  { key: "cache", label: "缓存" },
]

export function AnalyticsUsersTable({
  data,
  onTop,
  onPage,
  onComposeMail,
  disabled,
}: {
  data: SiteOpsAnalyticsUsersResponse
  onTop: (n: number) => void
  onPage: (page: number) => void
  onComposeMail?: (userIds: number[], sourceLabel: string) => void
  disabled: boolean
}) {
  const [search, setSearch] = useState("")
  const [planFilter, setPlanFilter] = useState("all")
  const [activityFilter, setActivityFilter] = useState("all")
  const [conds, setConds] = useState<Set<QuickCond>>(() => new Set())
  const [sortKey, setSortKey] = useState<SortKey>("d7")
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc")
  const [hoverUserId, setHoverUserId] = useState<number | null>(null)
  const [copiedId, setCopiedId] = useState<number | null>(null)

  const highConcPct = data.analytics_config?.high_concurrency_pct ?? 85
  const rawItems = useMemo(() => data.users.items ?? [], [data.users.items])

  const groupNames = useMemo(() => {
    const set = new Set<string>()
    for (const u of rawItems) {
      for (const s of u.active_subscriptions ?? []) {
        if (s.group_name) set.add(s.group_name)
      }
    }
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [rawItems])

  const enriched = useMemo(() => {
    return rawItems.map((u) => {
      const act = userActivity(u)
      const cache = userCachePct(u)
      const peakConc = userPeakConc(u)
      const concMax = userConcMax(u)
      const peakWait = userPeakWait(u)
      const peakHigh =
        peakWait > 0 || (concMax > 0 && peakConc * 100 >= concMax * highConcPct)
      const subs = u.active_subscriptions ?? []
      const hasWillHit = subs.some(
        (s) => s.forecast?.verdict === "will_hit" || s.forecast?.verdict === "exhausted",
      )
      const hasUnderUse = subs.some((s) => s.forecast?.verdict === "under_use")
      const expiringSoon =
        (u.nearest_sub_expires_days != null && u.nearest_sub_expires_days <= 7) ||
        subs.some((s) => (s.forecast?.remaining_days ?? Infinity) <= 7)
      return {
        raw: u,
        email: userDisplayEmail(u),
        plan: userPrimaryPlan(u),
        act,
        cache,
        peakConc,
        concMax,
        peakWait,
        peakHigh,
        balance: u.balance_usd ?? 0,
        today: u.today_actual_usd ?? 0,
        d7: userD7Cost(u),
        total: userTotalCost(u),
        subs,
        hasWillHit,
        hasUnderUse,
        expiringSoon,
      }
    })
  }, [rawItems, highConcPct])

  const baseFiltered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return enriched.filter((item) => {
      if (q) {
        const hit =
          item.email.toLowerCase().includes(q) ||
          (item.raw.email_masked ?? "").toLowerCase().includes(q) ||
          `u#${item.raw.user_id}`.includes(q) ||
          String(item.raw.user_id).includes(q)
        if (!hit) return false
      }
      if (planFilter === "has_sub" && item.raw.active_sub_count <= 0) return false
      if (planFilter === "payg" && item.raw.active_sub_count > 0) return false
      if (
        planFilter !== "all" &&
        planFilter !== "has_sub" &&
        planFilter !== "payg" &&
        !item.subs.some((s) => s.group_name === planFilter)
      ) {
        return false
      }
      if (activityFilter !== "all" && item.act !== activityFilter) return false
      return true
    })
  }, [enriched, search, planFilter, activityFilter])

  const condCounts = useMemo(
    () => ({
      will_hit: baseFiltered.filter((x) => x.hasWillHit).length,
      under_use: baseFiltered.filter((x) => x.hasUnderUse).length,
      peak_high: baseFiltered.filter((x) => x.peakHigh).length,
      low_cache: baseFiltered.filter((x) => x.cache != null && x.cache < 30).length,
      expiring: baseFiltered.filter((x) => x.expiringSoon).length,
    }),
    [baseFiltered],
  )

  const filtered = useMemo(() => {
    const matched = baseFiltered.filter((item) => {
      if (conds.size === 0) return true
      for (const c of conds) {
        if (c === "will_hit" && item.hasWillHit) return true
        if (c === "under_use" && item.hasUnderUse) return true
        if (c === "peak_high" && item.peakHigh) return true
        if (c === "low_cache" && item.cache != null && item.cache < 30) return true
        if (c === "expiring" && item.expiringSoon) return true
      }
      return false
    })
    const mult = sortDir === "desc" ? -1 : 1
    return [...matched].sort((a, b) => {
      const av = sortKey === "cache" ? (a.cache ?? -1) : a[sortKey]
      const bv = sortKey === "cache" ? (b.cache ?? -1) : b[sortKey]
      return (av - bv) * mult || a.raw.user_id - b.raw.user_id
    })
  }, [baseFiltered, conds, sortKey, sortDir])

  const [checked, setChecked] = useState<number[]>([])
  const matchedIds = positiveIds(filtered.map(item => item.raw.user_id))
  const selectedIds = matchedIds.filter(id => checked.includes(id))
  const mailIds = selectedIds.length ? selectedIds : matchedIds
  const mailSource = selectedIds.length ? "当前页已勾选" : "当前页筛选命中"

  const kpis = useMemo(() => {
    const subUsers = filtered.filter((x) => x.raw.active_sub_count > 0)
    const allSubs = filtered.flatMap((x) => x.subs)
    const sumD7 = filtered.reduce((s, x) => s + x.d7, 0)
    const sumToday = filtered.reduce((s, x) => s + x.today, 0)
    const peakHighCnt = filtered.filter((x) => x.peakHigh).length
    const maxPeak = filtered.reduce((m, x) => Math.max(m, x.peakConc), 0)
    const willHitCnt = allSubs.filter(
      (s) => s.forecast?.verdict === "will_hit" || s.forecast?.verdict === "exhausted",
    ).length
    const nearCnt = allSubs.filter((s) => s.forecast?.verdict === "near_full").length
    const underCnt = allSubs.filter((s) => s.forecast?.verdict === "under_use").length
    return {
      total: filtered.length,
      subUserCount: subUsers.length,
      paygCount: filtered.length - subUsers.length,
      sumD7,
      sumToday,
      peakHighCnt,
      maxPeak,
      allSubs,
      willHitCnt,
      nearCnt,
      underCnt,
    }
  }, [filtered])

  function toggleCond(c: QuickCond) {
    setConds((prev) => {
      const next = new Set(prev)
      if (next.has(c)) next.delete(c)
      else next.add(c)
      return next
    })
  }

  function handleSort(key: SortKey, defaultAsc = false) {
    if (sortKey === key) {
      setSortDir((prev) => (prev === "desc" ? "asc" : "desc"))
    } else {
      setSortKey(key)
      setSortDir(defaultAsc ? "asc" : "desc")
    }
  }

  function resetFilters() {
    setSearch("")
    setPlanFilter("all")
    setActivityFilter("all")
    setConds(new Set())
    setSortKey("d7")
    setSortDir("desc")
  }

  async function handleCopyEmail(userId: number, email: string) {
    try {
      await copyText(email)
      setCopiedId(userId)
      window.setTimeout(() => setCopiedId((cur) => (cur === userId ? null : cur)), 1200)
    } catch {
      // ignore clipboard errors
    }
  }

  function SortIcon({ k }: { k: SortKey }) {
    if (sortKey !== k) return <ArrowUpDown className="ml-1 inline size-3 opacity-45" />
    return sortDir === "desc" ? (
      <ArrowDown className="ml-1 inline size-3 text-primary" />
    ) : (
      <ArrowUp className="ml-1 inline size-3 text-primary" />
    )
  }

  // 左图排行数据（展示前 top_n 条，与表格排序完全同步）
  const rankRows = filtered.slice(0, Math.max(6, data.top_n))
  const maxPrimary =
    sortKey === "cache"
      ? 100
      : Math.max(...rankRows.map((r) => r[sortKey]), 1)
  const maxTotal = Math.max(...rankRows.map((r) => r.total), 1)
  const currentMetricLabel =
    metricOptions.find((m) => m.key === sortKey)?.label ?? "近7天"

  return (
    <div className="space-y-3">
      {/* 共享筛选栏：同时驱动 4 KPI + 两张核心图 + 下方全量表 */}
      <div className="space-y-2 rounded-lg border bg-card p-3">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-44 flex-1 sm:max-w-xs">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              aria-label="搜索客户邮箱或 ID"
              autoComplete="off"
              className="h-8 pl-8 text-xs"
              placeholder="搜索邮箱或 ID…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>

          <select
            aria-label="按套餐与分组筛选"
            className={selectClass}
            value={planFilter}
            onChange={(e) => setPlanFilter(e.target.value)}
          >
            <option value="all">全部套餐与分组</option>
            <option value="has_sub">仅订阅客户</option>
            <option value="payg">仅按量余额</option>
            {groupNames.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>

          <select
            aria-label="按活跃度筛选"
            className={selectClass}
            value={activityFilter}
            onChange={(e) => setActivityFilter(e.target.value)}
          >
            <option value="all">全部活跃度</option>
            <option value="active">持续活跃</option>
            <option value="drop">消费骤降</option>
            <option value="idle_balance">余额闲置</option>
            <option value="low">低频</option>
          </select>

          <select
            aria-label="排行统计人数"
            className={selectClass}
            value={data.top_n}
            disabled={disabled}
            onChange={(e) => onTop(Number(e.target.value))}
          >
            {[5, 10, 20].map((n) => (
              <option key={n} value={n}>
                统计前 {n} 位
              </option>
            ))}
          </select>

          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-8 px-2 text-xs text-muted-foreground"
            onClick={resetFilters}
          >
            重置
          </Button>
          {onComposeMail && <Button size="sm" variant="outline" disabled={disabled || !mailIds.length || mailIds.length > 500}
            onClick={() => onComposeMail(mailIds, `${mailSource} ${mailIds.length} 人`)}>
            给{mailSource}客户发邮件（{mailIds.length}）
          </Button>}
        </div>

        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[11px] text-muted-foreground">快捷条件</span>
          {(
            [
              ["will_hit", "预计提前用完", condCounts.will_hit, "danger"],
              ["under_use", "订阅用量偏低", condCounts.under_use, "info"],
              ["peak_high", "并发曾打满", condCounts.peak_high, "warn"],
              ["low_cache", "缓存 < 30%", condCounts.low_cache, "warn"],
              ["expiring", "7天内到期", condCounts.expiring, "warn"],
            ] as const
          ).map(([key, label, count, tone]) => {
            const active = conds.has(key)
            const activeCls =
              tone === "danger"
                ? "border-destructive/50 bg-destructive/10 text-destructive"
                : tone === "warn"
                  ? "border-amber-500/50 bg-amber-500/10 text-amber-700 dark:text-amber-300"
                  : "border-primary/50 bg-primary/10 text-primary"
            return (
              <Button
                key={key}
                type="button"
                size="sm"
                variant="outline"
                aria-pressed={active}
                className={`h-6 rounded-full px-2.5 text-[11px] tabular-nums ${active ? activeCls : "text-muted-foreground"}`}
                onClick={() => toggleCond(key)}
              >
                {label} <span className="ml-1 opacity-80">{count}</span>
              </Button>
            )
          })}
        </div>
      </div>

      {/* 随筛选实时重算的 4 个汇总数 */}
      <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
        <div className="rounded-lg border bg-card px-3 py-2.5">
          <p className="text-[11px] text-muted-foreground">命中客户</p>
          <p className="mt-0.5 text-lg font-bold tabular-nums">{kpis.total} 位</p>
          <p className="text-[11px] text-muted-foreground tabular-nums">
            订阅 {kpis.subUserCount} 位 · 按量 {kpis.paygCount} 位
          </p>
        </div>
        <div className="rounded-lg border bg-card px-3 py-2.5">
          <p className="text-[11px] text-muted-foreground">近 7 天消耗</p>
          <p className="mt-0.5 text-lg font-bold tabular-nums">{money(kpis.sumD7)}</p>
          <p className="text-[11px] text-muted-foreground tabular-nums">
            今日 {money(kpis.sumToday)}
          </p>
        </div>
        <div className="rounded-lg border bg-card px-3 py-2.5">
          <p className="text-[11px] text-muted-foreground">并发峰值压力</p>
          <p className="mt-0.5 text-lg font-bold tabular-nums">{kpis.peakHighCnt} 位曾高载</p>
          <p className="text-[11px] text-muted-foreground tabular-nums">
            单户最高峰值 {kpis.maxPeak} 并发
          </p>
        </div>
        <div className="rounded-lg border bg-card px-3 py-2.5">
          <p className="text-[11px] text-muted-foreground">订阅预测重点</p>
          <p className="mt-0.5 text-lg font-bold tabular-nums">
            {kpis.willHitCnt > 0 ? `${kpis.willHitCnt} 份提前满` : `${kpis.nearCnt} 份接近满`}
          </p>
          <p className="text-[11px] text-muted-foreground tabular-nums">
            接近用满 {kpis.nearCnt} · 用量偏低 {kpis.underCnt}
          </p>
        </div>
      </div>

      {/* 双核心图：左客户消费排行 + 右订阅周期消耗预测 */}
      <div className="grid gap-3 lg:grid-cols-2">
        <div className="flex flex-col gap-2.5 rounded-lg border bg-card p-3.5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-semibold">客户消费排行</h3>
            <div className="inline-flex flex-wrap gap-0.5 rounded-md bg-muted p-0.5">
              {metricOptions.map(({ key, label }) => (
                <button
                  key={key}
                  type="button"
                  onClick={() => handleSort(key, key === "cache")}
                  className={`rounded px-2 py-0.5 text-[11px] font-medium transition-colors ${
                    sortKey === key
                      ? "bg-card text-foreground shadow-xs"
                      : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>

          {rankRows.length === 0 ? (
            <p className="rounded-md border border-dashed py-8 text-center text-xs text-muted-foreground">
              当前筛选条件下无匹配客户
            </p>
          ) : (
            <div className="space-y-1.5">
              {rankRows.map((item) => {
                const showDual = sortKey === "today" || sortKey === "d7"
                const wBg = showDual
                  ? Math.max(4, Math.round((item.total / maxTotal) * 100))
                  : 0
                const primaryVal = sortKey === "cache" ? (item.cache ?? 0) : item[sortKey]
                const wFg =
                  sortKey === "peakConc"
                    ? item.concMax > 0
                      ? Math.min(100, Math.round((item.peakConc / item.concMax) * 100))
                      : 0
                    : primaryVal > 0
                      ? Math.max(
                          3,
                          Math.round(
                            (primaryVal / maxPrimary) * (sortKey === "cache" ? 100 : 88),
                          ),
                        )
                      : 0

                const fgClass =
                  item.act === "drop"
                    ? "bg-destructive"
                    : sortKey === "peakConc"
                      ? item.peakHigh
                        ? "bg-amber-500"
                        : "bg-violet-500"
                      : sortKey === "balance"
                        ? "bg-emerald-500"
                        : sortKey === "cache" && item.cache != null && item.cache < 30
                          ? "bg-amber-500"
                          : "bg-primary"

                const isHi = hoverUserId === item.raw.user_id
                const lowCache = item.cache != null && item.cache < 30

                return (
                  <div
                    key={item.raw.user_id}
                    onMouseEnter={() => setHoverUserId(item.raw.user_id)}
                    onMouseLeave={() => setHoverUserId(null)}
                    className={`space-y-1 rounded-md border px-2 py-1.5 transition-colors ${
                      isHi
                        ? "border-primary/40 bg-primary/5"
                        : "border-transparent hover:bg-muted/40"
                    }`}
                  >
                    <div className="flex items-baseline justify-between gap-2 text-xs">
                      <span className="inline-flex min-w-0 items-center gap-1.5">
                        <span className="truncate font-mono font-medium" title={item.email}>
                          {item.email}
                        </span>
                        {item.act === "drop" && (
                          <span className="shrink-0 rounded bg-destructive/15 px-1.5 py-0 text-[10px] font-semibold text-destructive">
                            骤降
                          </span>
                        )}
                        {item.act === "idle_balance" && (
                          <span className="shrink-0 rounded bg-amber-500/15 px-1.5 py-0 text-[10px] font-semibold text-amber-700 dark:text-amber-300">
                            闲置
                          </span>
                        )}
                      </span>
                      {/* 缓存 xx% 永远固定在右侧，绝不被任何标签覆盖 */}
                      <span
                        className={`shrink-0 text-[11px] tabular-nums ${
                          lowCache
                            ? "font-semibold text-amber-700 dark:text-amber-300"
                            : "text-muted-foreground"
                        }`}
                      >
                        缓存 {item.cache != null ? `${decimal(item.cache, 0)}%` : "—"}
                      </span>
                    </div>

                    <div className="text-[11px] text-muted-foreground tabular-nums">
                      {sortKey === "peakConc" ? (
                        <>
                          峰值 <strong className="text-foreground">{item.peakConc} / {item.concMax || "—"}</strong>
                          {" · "}当前 {item.raw.live_concurrency?.current_in_use ?? 0}
                          {item.peakWait > 0 ? ` · 曾排队 ${item.peakWait}` : ""}
                        </>
                      ) : sortKey === "cache" ? (
                        <>
                          缓存命中{" "}
                          <strong className={lowCache ? "text-amber-700 dark:text-amber-300" : "text-foreground"}>
                            {item.cache != null ? `${decimal(item.cache, 1)}%` : "—"}
                          </strong>
                          {" · "}近7天 {money(item.d7)}
                        </>
                      ) : sortKey === "balance" ? (
                        <>
                          余额 <strong className="text-foreground">{money(item.balance)}</strong>
                          {" · "}累计 {money(item.total)}
                        </>
                      ) : showDual ? (
                        <>
                          {currentMetricLabel}{" "}
                          <strong className="text-foreground">{money(primaryVal)}</strong>
                          {" · "}累计 {money(item.total)}
                        </>
                      ) : (
                        <>
                          累计 <strong className="text-foreground">{money(item.total)}</strong>
                          {" · "}近7天 {money(item.d7)}
                        </>
                      )}
                    </div>

                    <div className="relative h-2 w-full overflow-hidden rounded-full bg-muted">
                      {showDual && (
                        <div
                          className="absolute inset-y-0 left-0 rounded-full bg-primary/25"
                          style={{ width: `${wBg}%` }}
                        />
                      )}
                      <div
                        className={`absolute inset-y-0 left-0 rounded-full ${fgClass}`}
                        style={{ width: `${wFg}%` }}
                      />
                    </div>
                  </div>
                )
              })}
            </div>
          )}

          <div className="mt-auto flex flex-wrap items-center gap-3 pt-1 text-[11px] text-muted-foreground">
            {sortKey === "today" || sortKey === "d7" ? (
              <>
                <span className="inline-flex items-center gap-1">
                  <span className="size-2 rounded-full bg-primary/30" />
                  累计
                </span>
                <span className="inline-flex items-center gap-1">
                  <span className="size-2 rounded-full bg-primary" />
                  {currentMetricLabel}
                </span>
                <span className="inline-flex items-center gap-1">
                  <span className="size-2 rounded-full bg-destructive" />
                  骤降
                </span>
              </>
            ) : sortKey === "peakConc" ? (
              <>
                <span className="inline-flex items-center gap-1">
                  <span className="size-2 rounded-full bg-violet-500" />
                  峰值占上限比例
                </span>
                <span className="inline-flex items-center gap-1">
                  <span className="size-2 rounded-full bg-amber-500" />
                  曾达高载门槛
                </span>
              </>
            ) : (
              <span className="inline-flex items-center gap-1">
                <span className="size-2 rounded-full bg-primary" />
                {currentMetricLabel}
              </span>
            )}
            <span className="ml-auto">
              {currentMetricLabel}
              {sortDir === "desc" ? "降序" : "升序"}
            </span>
          </div>
        </div>

        {/* 右图：订阅周期消耗预测 */}
        <div className="flex flex-col gap-2.5 rounded-lg border bg-card p-3.5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-semibold">订阅周期消耗预测</h3>
            <span className="text-xs text-muted-foreground tabular-nums">
              {kpis.allSubs.length} 份订阅
            </span>
          </div>
          {kpis.allSubs.length === 0 ? (
            <p className="rounded-md border border-dashed py-8 text-center text-xs text-muted-foreground">
              当前筛选结果中无生效订阅
            </p>
          ) : (
            <div className="space-y-1.5">
              {kpis.allSubs.slice(0, 6).map((sub) => (
                <SubscriptionPaceBar
                  key={sub.subscription_id}
                  row={sub}
                  highlighted={hoverUserId === sub.user_id}
                  onHover={setHoverUserId}
                />
              ))}
            </div>
          )}
          <div className="mt-auto flex flex-wrap items-center gap-3 pt-1 text-[11px] text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-emerald-500" />
              深色 本期已用
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-emerald-500/35" />
              浅色 期末预计
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-block h-2.5 w-0.5 rounded-full bg-foreground/75" />
              时间进度
            </span>
          </div>
        </div>
      </div>

      {/* 下方常驻全量客户明细表 */}
      <div className="rounded-lg border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3.5 py-2.5">
          <h3 className="text-sm font-semibold">客户明细</h3>
          <span className="text-xs text-muted-foreground tabular-nums">
            显示 {filtered.length} / {rawItems.length} 位 · 点任意数字列头切换升降序
          </span>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              {onComposeMail && <TableHead><input type="checkbox" aria-label="全选当前页筛选命中客户"
                checked={matchedIds.length > 0 && selectedIds.length === matchedIds.length}
                onChange={event => setChecked(event.target.checked ? matchedIds : [])} /></TableHead>}
              <TableHead>客户</TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleSort("balance")}
              >
                余额
                <SortIcon k="balance" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleSort("today")}
              >
                今日
                <SortIcon k="today" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleSort("d7")}
              >
                近7天
                <SortIcon k="d7" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleSort("total")}
              >
                累计
                <SortIcon k="total" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleSort("peakConc")}
              >
                并发峰值
                <SortIcon k="peakConc" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleSort("cache")}
              >
                缓存
                <SortIcon k="cache" />
              </TableHead>
              <TableHead>活跃度</TableHead>
              {onComposeMail && <TableHead>操作</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={onComposeMail ? 10 : 8} className="py-6 text-center text-xs text-muted-foreground">
                  当前筛选条件下无匹配客户
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((item) => {
                const u = item.raw
                const actMeta = activityBadgeMeta[item.act]
                const isHi = hoverUserId === u.user_id
                const lowCache = item.cache != null && item.cache < 30
                const expDays = u.nearest_sub_expires_days

                return (
                  <TableRow
                    key={u.user_id}
                    onMouseEnter={() => setHoverUserId(u.user_id)}
                    onMouseLeave={() => setHoverUserId(null)}
                    className={isHi ? "bg-primary/5" : undefined}
                  >
                    {onComposeMail && <TableCell><input type="checkbox" aria-label={`选择客户 #${u.user_id}`}
                      disabled={!matchedIds.includes(u.user_id)} checked={selectedIds.includes(u.user_id)}
                      onChange={() => setChecked(ids => toggleValue(ids, u.user_id))} /></TableCell>}
                    <TableCell>
                      <div className="flex flex-col gap-0.5">
                        <div className="inline-flex items-center gap-1.5">
                          <span className="font-mono text-xs font-medium">{item.email}</span>
                          <button
                            type="button"
                            aria-label={`复制 ${item.email}`}
                            onClick={() => {
                              void handleCopyEmail(u.user_id, item.email)
                            }}
                            className="inline-flex items-center gap-0.5 rounded border px-1 py-0 text-[10px] text-muted-foreground hover:bg-secondary hover:text-foreground"
                          >
                            {copiedId === u.user_id ? (
                              <>
                                <Check className="size-2.5" />
                                已复制
                              </>
                            ) : (
                              <>
                                <Copy className="size-2.5" />
                                复制
                              </>
                            )}
                          </button>
                        </div>
                        <span className="text-[11px] text-muted-foreground">
                          {item.plan}
                          {expDays != null && expDays <= 7
                            ? ` · 剩${Math.max(0, Math.round(expDays))}天`
                            : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {u.balance_usd != null ? money(u.balance_usd) : "—"}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {u.today_actual_usd != null ? money(u.today_actual_usd) : "—"}
                    </TableCell>
                    <TableCell className="text-right text-xs font-semibold tabular-nums">
                      {u.last_7d?.actual_usd != null
                        ? money(u.last_7d.actual_usd)
                        : u.today_actual_usd != null
                          ? money(u.today_actual_usd)
                          : "—"}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {u.all_time?.actual_usd != null
                        ? money(u.all_time.actual_usd)
                        : u.recent_aggregate_actual_usd != null
                          ? money(u.recent_aggregate_actual_usd)
                          : "—"}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <span
                          className={
                            item.peakHigh
                              ? "font-semibold text-amber-700 dark:text-amber-300"
                              : undefined
                          }
                        >
                          峰 {item.peakConc} / {item.concMax || "—"}
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          现 {u.live_concurrency?.current_in_use ?? 0}
                          {item.peakWait > 0 ? ` · 曾排${item.peakWait}` : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell
                      className={`text-right text-xs tabular-nums ${
                        lowCache ? "font-semibold text-amber-700 dark:text-amber-300" : ""
                      }`}
                    >
                      {item.cache != null ? `${decimal(item.cache, 0)}%` : "—"}
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className={`text-[11px] ${actMeta.cls}`}>
                        {actMeta.label}
                      </Badge>
                    </TableCell>
                    {onComposeMail && <TableCell><Button size="sm" variant="ghost" disabled={disabled || !matchedIds.includes(u.user_id)}
                      onClick={() => onComposeMail([u.user_id], "当前页单个客户")}>发邮件</Button></TableCell>}
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      <PageControls
        page={data.users}
        onPage={(page) => {
          setSearch("")
          setChecked([])
          onPage(page)
        }}
        disabled={disabled}
      />
    </div>
  )
}

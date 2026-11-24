import { useMemo, useState } from "react"
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { decimal, money } from "@/lib/format"
import type {
  SiteOpsAnalyticsPage,
  SiteOpsAnalyticsReferencePriceView,
  SiteOpsAnalyticsSubscriptionRow,
  SiteOpsAnalyticsSubscriptionsResponse,
  SiteOpsAnalyticsSubscriptionStatus,
  SiteOpsSubscriptionForecastSummary,
} from "@/lib/site-ops-types"

export const selectClass =
  "h-8 rounded-md border border-input bg-background px-2.5 text-xs outline-none focus-visible:border-ring"

export function PageControls<T>({
  page,
  onPage,
  disabled,
}: {
  page: SiteOpsAnalyticsPage<T>
  onPage: (page: number) => void
  disabled?: boolean
}) {
  const hasNext =
    page.has_more ?? (page.total == null || page.page * page.page_size < page.total)
  if (page.page <= 1 && !hasNext && !page.truncated) return null
  return (
    <nav aria-label="目录分页" className="flex flex-wrap items-center justify-end gap-2 text-xs text-muted-foreground">
      <span className="tabular-nums">
        第 {page.page} 页{page.total != null ? ` · 共 ${page.total} 条` : ""}
        {page.truncated ? " · 已截断" : ""}
      </span>
      <Button
        size="sm"
        variant="outline"
        className="h-7 px-2 text-xs"
        disabled={disabled || page.page <= 1}
        onClick={() => onPage(page.page - 1)}
      >
        上一页
      </Button>
      <Button
        size="sm"
        variant="outline"
        className="h-7 px-2 text-xs"
        disabled={disabled || !hasNext || page.page < 1 || page.page >= 10000}
        onClick={() => onPage(page.page + 1)}
      >
        下一页
      </Button>
    </nav>
  )
}

export function nativePrice(price?: SiteOpsAnalyticsReferencePriceView | null) {
  if (!price) return "未设置"
  const val = (price.monthly_minor / 100).toFixed(2)
  const sym = price.currency === "CNY" ? "¥" : "$"
  return price.monthly_minor === 0 ? "赠送" : `${sym}${val}`
}

export function subDisplayEmail(row: SiteOpsAnalyticsSubscriptionRow) {
  return row.user_email || row.user_email_masked || `U#${row.user_id}`
}

export type VerdictMeta = {
  label: string
  tone: "danger" | "warn" | "ok" | "low" | "info" | "muted"
  pillClass: string
  textClass: string
  usedBarClass: string
  projBarClass: string
}

export function verdictMeta(
  forecast?: SiteOpsSubscriptionForecastSummary | null,
): VerdictMeta {
  const verdict = forecast?.verdict ?? "observing"
  switch (verdict) {
    case "exhausted":
      return {
        label: "已撞限额",
        tone: "danger",
        pillClass: "border-destructive/40 bg-destructive/10 text-destructive",
        textClass: "text-destructive",
        usedBarClass: "bg-destructive",
        projBarClass: "bg-destructive/35",
      }
    case "will_hit":
      return {
        label: forecast?.is_unlimited ? "超期望上限" : "预计提前用完",
        tone: "danger",
        pillClass: "border-destructive/40 bg-destructive/10 text-destructive",
        textClass: "text-destructive",
        usedBarClass: "bg-destructive",
        projBarClass: "bg-destructive/35",
      }
    case "near_full":
      return {
        label: "接近用满",
        tone: "warn",
        pillClass:
          "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300",
        textClass: "text-amber-700 dark:text-amber-300",
        usedBarClass: "bg-amber-500",
        projBarClass: "bg-amber-500/35",
      }
    case "under_use":
      return {
        label: "用量偏低",
        tone: "low",
        pillClass: "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300",
        textClass: "text-sky-700 dark:text-sky-300",
        usedBarClass: "bg-sky-500",
        projBarClass: "bg-sky-500/30",
      }
    case "on_track":
      return {
        label: "进度正常",
        tone: "ok",
        pillClass:
          "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
        textClass: "text-emerald-700 dark:text-emerald-300",
        usedBarClass: "bg-emerald-500",
        projBarClass: "bg-emerald-500/35",
      }
    case "expired":
      return {
        label: "周期已结束",
        tone: "muted",
        pillClass: "text-muted-foreground",
        textClass: "text-muted-foreground",
        usedBarClass: "bg-muted-foreground/60",
        projBarClass: "bg-muted-foreground/25",
      }
    default:
      return {
        label: "待观察",
        tone: "info",
        pillClass: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300",
        textClass: "text-muted-foreground",
        usedBarClass: "bg-sky-500/80",
        projBarClass: "bg-sky-500/25",
      }
  }
}

export function SubscriptionPaceBar({
  row,
  highlighted,
  onHover,
}: {
  row: SiteOpsAnalyticsSubscriptionRow
  highlighted?: boolean
  onHover?: (id: number | null) => void
}) {
  const f = row.forecast
  const meta = verdictMeta(f)
  const usedPct = Math.min(100, Math.max(0, f?.used_pct ?? 0))
  const projPct = Math.min(100, Math.max(usedPct, f?.projected_pct ?? usedPct))
  const timePct = Math.min(100, Math.max(0, f?.time_progress_pct ?? 0))
  const periodLabel =
    row.total_cycles > 0 ? `第 ${row.cycle_index}/${row.total_cycles} 期` : "当前周期"
  const elapsedLabel =
    f && f.cycle_days > 0
      ? `已过 ${Math.round(f.elapsed_days)}/${Math.round(f.cycle_days)} 天 · 剩 ${Math.max(0, Math.round(f.remaining_days))} 天`
      : row.expires_in_days != null
        ? `剩 ${Math.max(0, Math.round(row.expires_in_days))} 天`
        : "周期未定"

  const rightSummary =
    f?.verdict === "will_hit" && f.days_to_exhaust != null
      ? `${meta.label} · 约${Math.max(1, Math.round(f.days_to_exhaust))}天后满`
      : f?.verdict === "expired"
        ? `周期已结束 · 实耗 ${Math.round(usedPct)}%`
        : f?.verdict === "observing"
          ? "待观察 · 开通不足2天"
          : `${meta.label} · 预计 ${Math.min(999, Math.round(f?.projected_pct ?? 0))}%`

  return (
    <div
      onMouseEnter={() => onHover?.(row.user_id)}
      onMouseLeave={() => onHover?.(null)}
      className={`space-y-1 rounded-md border px-2 py-1.5 transition-colors ${
        highlighted
          ? "border-primary/40 bg-primary/5"
          : "border-transparent hover:bg-muted/40"
      }`}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2 text-xs">
        <span className="min-w-0 truncate font-mono font-medium" title={subDisplayEmail(row)}>
          {periodLabel} · {subDisplayEmail(row)}
        </span>
        <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
          已用 {f?.used_usd != null ? money(f.used_usd) : "—"} /{" "}
          {f?.quota_cap_usd != null ? money(f.quota_cap_usd) : "—"}
          {" → "}
          <strong className={meta.textClass}>
            预计 {f?.projected_end_usd != null ? money(f.projected_end_usd) : "—"}
          </strong>
        </span>
      </div>
      <div className="relative h-2.5 w-full rounded-full bg-muted">
        <div
          className={`absolute inset-y-0.5 left-0 rounded-full transition-all ${meta.projBarClass}`}
          style={{ width: `${projPct}%` }}
        />
        <div
          className={`absolute inset-y-0.5 left-0 rounded-full transition-all ${meta.usedBarClass}`}
          style={{ width: `${usedPct}%` }}
        />
        <div
          className="absolute -top-0.5 -bottom-0.5 w-0.5 rounded-full bg-foreground/75"
          style={{ left: `${timePct}%` }}
          title={`当前已过周期 ${decimal(timePct, 0)}%`}
        />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] tabular-nums">
        <span className="text-muted-foreground">
          {row.group_name || `分组 #${row.group_id}`} · {elapsedLabel}
        </span>
        <span className={`font-medium ${meta.textClass}`}>{rightSummary}</span>
      </div>
    </div>
  )
}

type SubSortKey = "days_left" | "used_pct" | "proj_pct"

export function AnalyticsSubscriptionsTable({
  data,
  status,
  group,
  disabled,
  onStatus,
  onGroup,
  onPage,
}: {
  data: SiteOpsAnalyticsSubscriptionsResponse
  status: SiteOpsAnalyticsSubscriptionStatus
  group?: number
  disabled: boolean
  onStatus: (v: SiteOpsAnalyticsSubscriptionStatus) => void
  onGroup: (id?: number) => void
  onPage: (page: number) => void
}) {
  const [verdictFilter, setVerdictFilter] = useState("all")
  const [sortKey, setSortKey] = useState<SubSortKey>("proj_pct")
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc")
  const [hoverSubId, setHoverSubId] = useState<number | null>(null)

  const rawItems = useMemo(() => data.subscriptions.items ?? [], [data.subscriptions.items])

  const filtered = useMemo(() => {
    const list = rawItems.filter((row) => {
      if (verdictFilter !== "all" && (row.forecast?.verdict ?? "observing") !== verdictFilter) {
        return false
      }
      return true
    })
    const mult = sortDir === "desc" ? -1 : 1
    return [...list].sort((a, b) => {
      const av =
        sortKey === "days_left"
          ? (a.forecast?.remaining_days ?? a.expires_in_days ?? Infinity)
          : sortKey === "used_pct"
            ? (a.forecast?.used_pct ?? -1)
            : (a.forecast?.projected_pct ?? -1)
      const bv =
        sortKey === "days_left"
          ? (b.forecast?.remaining_days ?? b.expires_in_days ?? Infinity)
          : sortKey === "used_pct"
            ? (b.forecast?.used_pct ?? -1)
            : (b.forecast?.projected_pct ?? -1)
      return (av - bv) * mult || a.subscription_id - b.subscription_id
    })
  }, [rawItems, verdictFilter, sortKey, sortDir])

  function toggleSort(next: SubSortKey) {
    if (sortKey === next) {
      setSortDir((prev) => (prev === "desc" ? "asc" : "desc"))
    } else {
      setSortKey(next)
      setSortDir(next === "days_left" ? "asc" : "desc")
    }
  }

  function SortIcon({ k }: { k: SubSortKey }) {
    if (sortKey !== k) return <ArrowUpDown className="ml-1 inline size-3 opacity-45" />
    return sortDir === "desc" ? (
      <ArrowDown className="ml-1 inline size-3 text-primary" />
    ) : (
      <ArrowUp className="ml-1 inline size-3 text-primary" />
    )
  }

  return (
    <section className="space-y-3">
      {/* 共享筛选条 */}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card px-3 py-2">
        <div className="flex flex-wrap gap-1">
          {(
            [
              ["active", "生效中"],
              ["expired", "已到期"],
              ["all", "全部"],
            ] as const
          ).map(([val, label]) => (
            <Button
              key={val}
              size="sm"
              variant={status === val ? "secondary" : "outline"}
              className="h-7 rounded-full px-2.5 text-xs"
              disabled={disabled}
              onClick={() => onStatus(val)}
            >
              {label}
            </Button>
          ))}
        </div>

        <select
          aria-label="按分组筛选订阅"
          value={group ?? ""}
          className={selectClass}
          disabled={disabled}
          onChange={(e) => onGroup(Number(e.target.value) || undefined)}
        >
          <option value="">全部分组</option>
          {data.groups?.map((g) => (
            <option key={g.group_id} value={g.group_id}>
              {g.group_name || `分组 #${g.group_id}`}
            </option>
          ))}
        </select>

        <select
          aria-label="按预测结论筛选订阅"
          value={verdictFilter}
          className={selectClass}
          onChange={(e) => setVerdictFilter(e.target.value)}
        >
          <option value="all">全部预测结论</option>
          <option value="exhausted">已撞限额</option>
          <option value="will_hit">预计提前用完</option>
          <option value="near_full">接近用满</option>
          <option value="on_track">进度正常</option>
          <option value="under_use">用量偏低</option>
          <option value="observing">待观察</option>
        </select>

        <span className="ml-auto text-xs text-muted-foreground tabular-nums">
          显示 {filtered.length} / {rawItems.length} 份订阅
        </span>
      </div>

      {/* 上方图表：周期消耗与可用总量对比 */}
      <div className="space-y-2.5 rounded-lg border bg-card p-3.5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-sm font-semibold">周期消耗与可用总量对比</h3>
          <span className="text-xs text-muted-foreground">
            竖线表示当前已过时间比例，对比柱条看清用快还是用慢
          </span>
        </div>
        {filtered.length === 0 ? (
          <p className="rounded-md border border-dashed py-6 text-center text-xs text-muted-foreground">
            当前筛选条件下无匹配订阅
          </p>
        ) : (
          <div className="space-y-2">
            {filtered.map((row) => (
              <div
                key={row.subscription_id}
                onMouseEnter={() => setHoverSubId(row.subscription_id)}
                onMouseLeave={() => setHoverSubId(null)}
              >
                <SubscriptionPaceBar
                  row={row}
                  highlighted={hoverSubId === row.subscription_id}
                />
              </div>
            ))}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-4 pt-1 text-[11px] text-muted-foreground">
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
            当前时间进度
          </span>
        </div>
      </div>

      {/* 下方常驻全量订阅周期明细表 */}
      <div className="rounded-lg border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3.5 py-2.5">
          <h3 className="text-sm font-semibold">订阅周期明细</h3>
          <span className="text-xs text-muted-foreground">
            点列头可按剩余天数、已用比例或预计占比排序
          </span>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>订阅与客户</TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => toggleSort("days_left")}
              >
                周期进度
                <SortIcon k="days_left" />
              </TableHead>
              <TableHead className="text-right">日 / 周限额</TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => toggleSort("used_pct")}
              >
                本期已用 / 总量
                <SortIcon k="used_pct" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => toggleSort("proj_pct")}
              >
                日均与期末预计
                <SortIcon k="proj_pct" />
              </TableHead>
              <TableHead>预测结论</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="py-6 text-center text-xs text-muted-foreground">
                  当前筛选条件下无匹配订阅
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((row) => {
                const f = row.forecast
                const meta = verdictMeta(f)
                const refPrice = row.pricing_compare.reference_price
                const daysLeft = f?.remaining_days ?? row.expires_in_days
                const etaText =
                  f?.verdict === "will_hit" && f.days_to_exhaust != null
                    ? `日均 ${f.burn_usd_day != null ? money(f.burn_usd_day) : "—"} · 约${Math.max(1, Math.round(f.days_to_exhaust))}天后满`
                    : `日均 ${f?.burn_usd_day != null ? money(f.burn_usd_day) : "—"} · 预计 ${f?.projected_pct != null ? `${Math.round(f.projected_pct)}%` : "—"}`

                return (
                  <TableRow
                    key={row.subscription_id}
                    onMouseEnter={() => setHoverSubId(row.subscription_id)}
                    onMouseLeave={() => setHoverSubId(null)}
                    className={hoverSubId === row.subscription_id ? "bg-primary/5" : undefined}
                  >
                    <TableCell>
                      <div className="flex flex-col gap-0.5">
                        <span className="font-mono text-xs font-medium">
                          {subDisplayEmail(row)}
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          {row.group_name || `分组 #${row.group_id}`}
                          {row.total_cycles > 0 ? ` · 第 ${row.cycle_index}/${row.total_cycles} 期` : ""}
                          {refPrice ? ` · 月收 ${nativePrice(refPrice)}` : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <span>
                          {f && f.cycle_days > 0
                            ? `${Math.round(f.elapsed_days)}/${Math.round(f.cycle_days)}天 · ${Math.round(f.time_progress_pct)}%`
                            : "—"}
                        </span>
                        <span
                          className={`text-[11px] ${
                            row.cycle_state === "active" && daysLeft != null && daysLeft <= 7
                              ? "font-medium text-amber-700 dark:text-amber-300"
                              : "text-muted-foreground"
                          }`}
                        >
                          {row.cycle_state === "expired" || row.cycle_state === "revoked"
                            ? "已到期"
                            : daysLeft != null
                              ? `剩 ${Math.max(0, Math.round(daysLeft))} 天`
                              : "—"}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <span>
                          日 {row.daily_window.used_usd != null ? money(row.daily_window.used_usd) : "—"}
                          {row.daily_window.limit_usd != null ? ` / ${money(row.daily_window.limit_usd)}` : ""}
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          周 {row.weekly_window.used_usd != null ? money(row.weekly_window.used_usd) : "—"}
                          {row.weekly_window.limit_usd != null ? ` / ${money(row.weekly_window.limit_usd)}` : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <strong className="font-semibold">
                          {f?.used_usd != null ? money(f.used_usd) : "—"} /{" "}
                          {f?.quota_cap_usd != null ? money(f.quota_cap_usd) : "—"}
                        </strong>
                        <span className="text-[11px] text-muted-foreground">
                          {f?.used_pct != null ? `已占 ${Math.round(f.used_pct)}%` : "—"}
                          {f?.is_unlimited ? " · 按期望上限" : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <strong>
                          {f?.projected_end_usd != null ? money(f.projected_end_usd) : "—"}
                        </strong>
                        <span className="text-[11px] text-muted-foreground">{etaText}</span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className={`text-[11px] ${meta.pillClass}`}>
                        {meta.label}
                      </Badge>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      <PageControls page={data.subscriptions} onPage={onPage} disabled={disabled} />
    </section>
  )
}

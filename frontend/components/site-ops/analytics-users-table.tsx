import { useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { money } from "@/lib/format"
import type { SiteOpsAnalyticsPage, SiteOpsAnalyticsUsersResponse, SiteOpsFieldQualityState } from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote, MetricValue } from "./quality-badge"

export const analyticsTags: Record<string, string> = {
  high_value_at_risk: "高危回访", expiring_soon: "临期续费", expired_unrenewed: "已到期未续",
  cache_anomaly: "缓存异常", concurrency_queued: "并发排队", dormant_high_balance: "休眠高余额",
}
export const selectClass = "h-9 rounded-md border bg-background px-2 text-sm"

export function PageControls<T>({ page, onPage, disabled }: {
  page: SiteOpsAnalyticsPage<T>; onPage: (page: number) => void; disabled?: boolean
}) {
  // 服务端先分页后筛选，当前页的可见行数不能判断目录是否结束。
  const hasNext = page.has_more ?? (page.total == null || page.page * page.page_size < page.total)
  return <nav aria-label="目录分页" className="flex flex-wrap items-center justify-end gap-2 text-xs">
    <span>目录第 {page.page} 页{page.total != null && ` · 共 ${page.total} 条`}{page.truncated && " · 目录截断"}</span>
    <Button size="sm" variant="outline" disabled={disabled || page.page <= 1} onClick={() => onPage(page.page - 1)}>上一页</Button>
    <Button size="sm" variant="outline" disabled={disabled || !hasNext || page.page < 1 || page.page >= 10000} onClick={() => onPage(page.page + 1)}>下一页</Button>
  </nav>
}

export function CacheRate({ value, unit = "%", quality }: {
  value?: number | null; unit?: string; quality?: SiteOpsFieldQualityState
}) {
  if (quality && quality !== "trusted") return <FieldQualityBadge quality={quality} />
  if (value == null || !Number.isFinite(value)) return <span className="inline-flex items-center gap-1">—
    <MetricNote label="缓存指标口径">分母为零或 Token 明细不足，无法计算该指标。</MetricNote>
  </span>
  return <MetricValue value={value} unit={unit} />
}

type Order = "today_actual_usd" | "recent_aggregate_actual_usd" | "balance_usd" | "user_id"

export function AnalyticsUsersTable({ data, tag, onTag, onTop, onPage, onDetail, disabled }: {
  data: SiteOpsAnalyticsUsersResponse; tag: string; onTag: (tag: string) => void
  onTop: (n: number) => void; onPage: (page: number) => void; onDetail: (id: number) => void; disabled: boolean
}) {
  const [search, setSearch] = useState("")
  const [order, setOrder] = useState<Order>("today_actual_usd")
  const query = search.trim().toLowerCase()
  const rows = (data.users.items ?? []).filter(row =>
    (!tag || row.tags?.some(t => t.key === tag)) &&
    (`u#${row.user_id}`.includes(query) || row.email_masked?.toLowerCase().includes(query))
  ).sort((a, b) => {
    if (order === "user_id") return a.user_id - b.user_id
    return (b[order] ?? -Infinity) - (a[order] ?? -Infinity) || a.user_id - b.user_id
  })
  return <div className="space-y-3">
    <div className="flex flex-wrap items-end gap-2">
      <Input aria-label="本页用户搜索" autoComplete="off" className="w-60" placeholder="本页搜索 U#ID 或脱敏邮箱" value={search} onChange={e => setSearch(e.target.value)} />
      <select aria-label="本页运营标签" className={selectClass} value={tag} onChange={e => onTag(e.target.value)}>
        <option value="">全部运营标签</option>{Object.entries(analyticsTags).map(([key, label]) => <option key={key} value={key}>{label}</option>)}
      </select>
      <select aria-label="本页排序" className={selectClass} value={order} onChange={e => setOrder(e.target.value as Order)}>
        <option value="today_actual_usd">今日消耗降序</option><option value="recent_aggregate_actual_usd">近段消耗降序</option>
        <option value="balance_usd">余额降序</option><option value="user_id">用户 ID 升序</option>
      </select>
      <label className="flex items-center gap-2 text-sm">精算 Top N
        <select className={selectClass} value={data.top_n} disabled={disabled} onChange={e => onTop(Number(e.target.value))}>
          {[5, 10, 20].map(n => <option key={n}>{n}</option>)}
        </select>
      </label>
    </div>
    <Table>
      <TableHeader><TableRow>{["用户", "余额 / 实时并发", "今日 / 上游近段汇总（非全时段）", "真实全时段 / 7d 消耗", "全时段 / 7d 缓存读取率", "运营标签", "详情"].map(x => <TableHead key={x}>{x}</TableHead>)}</TableRow></TableHeader>
      <TableBody>{rows.map(row => {
        const stats = data.domains.user_stats
        const pending = row.enrichment_state === "error"
          ? <FieldQualityBadge quality={stats?.quality === "trusted" ? "unavailable" : stats?.quality} reason={stats?.reason} />
          : row.enrichment_state === "loaded" ? <FieldQualityBadge quality="unavailable" />
          : row.enrichment_state === "deferred_budget" ? "预算延后（可点详情单查）" : "查看详情补查"
        const windows = [row.all_time, row.last_7d]
        return <TableRow key={row.user_id}>
          <TableCell><strong>U#{row.user_id}</strong><div className="text-muted-foreground">{row.email_masked ?? "—"}</div></TableCell>
          <TableCell>{money(row.balance_usd)}<div><MetricValue value={row.live_concurrency?.current_in_use} quality={data.domains.user_concurrency?.quality} /> / <MetricValue value={row.live_concurrency?.max_capacity ?? row.concurrency_limit} /></div></TableCell>
          <TableCell><MetricValue value={row.today_actual_usd} format={money} quality={data.domains.users_usage?.quality} /><div><MetricValue value={row.recent_aggregate_actual_usd} format={money} quality={data.domains.users_usage?.quality} /></div></TableCell>
          <TableCell>{windows.some(Boolean) ? windows.map((w, i) => <div key={i}>{w ? <MetricValue value={w.actual_usd} quality={w.quality} format={money} /> : pending}</div>) : pending}</TableCell>
          <TableCell>{windows.some(Boolean) ? windows.map((w, i) => <div key={i}>{w ? <CacheRate value={w.cache.cache_read_hit_pct} quality={w.quality === "trusted" ? w.cache.token_quality : w.quality} /> : pending}</div>) : pending}</TableCell>
          <TableCell className="max-w-64 whitespace-normal">{row.tags?.length ? row.tags.map(t => <Badge key={t.key} variant="outline" title={t.reason}>{t.label}</Badge>) : "—"}</TableCell>
          <TableCell><Button size="sm" variant="outline" disabled={disabled} onClick={() => onDetail(row.user_id)}>查看详情</Button></TableCell>
        </TableRow>
      })}</TableBody>
    </Table>
    {!rows.length && <p className="text-sm text-muted-foreground">本页无匹配用户。</p>}
    <PageControls page={data.users} onPage={page => { setSearch(""); onPage(page) }} disabled={disabled} />
  </div>
}

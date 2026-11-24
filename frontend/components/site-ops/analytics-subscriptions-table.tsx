import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type {
  SiteOpsAnalyticsReferencePriceView, SiteOpsAnalyticsSubscriptionRow, SiteOpsAnalyticsSubscriptionsResponse,
  SiteOpsAnalyticsSubscriptionStatus,
} from "@/lib/site-ops-types"
import { analyticsTime } from "./analytics-lights-bar"
import { PageControls, selectClass } from "./analytics-users-table"
import { MetricValue } from "./quality-badge"
import { money } from "@/lib/format"

export function nativePrice(price?: SiteOpsAnalyticsReferencePriceView | null) {
  if (!price) return "未设置"
  return `${price.currency} ${(price.monthly_minor / 100).toFixed(2)}${price.monthly_minor === 0 ? "（赠送）" : ""}`
}

export function rollingWindows(row: SiteOpsAnalyticsSubscriptionRow) {
  return [["日", row.daily_window], ["周", row.weekly_window], ["月", row.monthly_window]] as const
}

export function Attribution({ row }: { row: SiteOpsAnalyticsSubscriptionRow }) {
  return <Badge variant="outline" title={row.attribution_reason}>
    {row.attribution === "unique_group_term" ? "可归因至订阅" : "仅用户×分组汇总"}
  </Badge>
}

export function AnalyticsSubscriptionsTable({ data, status, group, disabled, onStatus, onGroup, onPage, onDetail, onPrice }: {
  data: SiteOpsAnalyticsSubscriptionsResponse; status: SiteOpsAnalyticsSubscriptionStatus; group?: number; disabled: boolean
  onStatus: (v: SiteOpsAnalyticsSubscriptionStatus) => void; onGroup: (id?: number) => void
  onPage: (page: number) => void; onDetail: (id: number) => void; onPrice: (id: number) => void
}) {
  return <section className="space-y-3">
    <div className="flex flex-wrap items-center gap-2">
      <select aria-label="订阅状态" value={status} className={selectClass} disabled={disabled} onChange={e => onStatus(e.target.value as SiteOpsAnalyticsSubscriptionStatus)}>
        <option value="active">活跃</option><option value="expired">已到期 / 撤销</option><option value="all">全部状态</option>
      </select>
      <select aria-label="订阅分组" className={selectClass} value={group ?? ""} disabled={disabled} onChange={e => onGroup(Number(e.target.value) || undefined)}>
        <option value="">全部分组</option>{data.groups?.map(g => <option key={g.group_id} value={g.group_id}>{g.group_name || `分组 #${g.group_id}`}</option>)}
      </select>
      <span className="text-xs text-muted-foreground">目录页筛选 · 日历时间 UTC+8</span>
    </div>
    <Table><TableHeader><TableRow>{["订阅 / 用户与分组", "L1 滚动快照（用量 / 限额）", "L2 当前周期 / L3 总期", "归因预检", "本地参考月收款", "操作"].map(x => <TableHead key={x}>{x}</TableHead>)}</TableRow></TableHeader>
      <TableBody>{data.subscriptions.items?.map(row => <TableRow key={row.subscription_id}>
        <TableCell><strong>#{row.subscription_id} · U#{row.user_id}</strong><div>{row.user_email_masked || "—"}</div><div>{row.group_name || `分组 #${row.group_id}`}</div></TableCell>
        <TableCell>{rollingWindows(row).map(([label, w]) => <div key={label}>{label} · <MetricValue value={w.used_usd} quality={w.quality} format={money} /> / {money(w.limit_usd)}</div>)}</TableCell>
        <TableCell><strong>{row.total_cycles > 0 ? `第 ${row.cycle_index}/${row.total_cycles} 期` : "周期待确认"}</strong>
          <div>{analyticsTime(row.sub_cycle_start)} 至 {analyticsTime(row.sub_cycle_end)}</div>
          <div className="text-xs text-muted-foreground">总期 {analyticsTime(row.starts_at)} 至 {analyticsTime(row.expires_at)}</div>
        </TableCell>
        <TableCell><Attribution row={row} /></TableCell>
        <TableCell>{nativePrice(row.pricing_compare.reference_price)}
          <div className="text-xs text-muted-foreground">{{ group: "分组默认", subscription: "订阅覆盖" }[row.pricing_compare.price_source]}</div></TableCell>
        <TableCell><div className="flex flex-col gap-1">
          <Button size="sm" variant="outline" disabled={disabled} onClick={() => onDetail(row.subscription_id)}>三层精算详情</Button>
          <Button size="sm" variant="ghost" onClick={() => onPrice(row.subscription_id)}>设参考收款</Button>
        </div></TableCell>
      </TableRow>)}</TableBody>
    </Table>
    {!data.subscriptions.items?.length && <p className="text-sm text-muted-foreground">当前目录页无匹配订阅。</p>}
    <PageControls page={data.subscriptions} onPage={onPage} disabled={disabled} />
  </section>
}

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { money } from "@/lib/format"
import type { SiteOpsAnalyticsMarketResponse, SiteOpsAnalyticsRange } from "@/lib/site-ops-types"
import { nativePrice } from "./analytics-subscriptions-table"
import { CacheRate } from "./analytics-users-table"
import { FieldQualityBadge, MetricValue } from "./quality-badge"

export function AnalyticsMarketPanel({ range, data, disabled, onRange, onPrice }: {
  range: SiteOpsAnalyticsRange; data?: SiteOpsAnalyticsMarketResponse; disabled: boolean
  onRange: (range: SiteOpsAnalyticsRange) => void; onPrice: (groupId: number) => void
}) {
  const spread = data?.models?.some(m => m.has_cost_spread)
  return <section className="space-y-4 text-sm">
    <div className="flex flex-wrap items-center gap-2">{(["30d", "7d"] as const).map(r =>
      <Button key={r} size="sm" aria-pressed={range === r} disabled={disabled} variant={range === r ? "secondary" : "outline"} onClick={() => onRange(r)}>
        近 {r === "7d" ? 7 : 30} 天
      </Button>)}
      {data && <span className="text-xs text-muted-foreground">{data.date_range.start_date} 至 {data.date_range.end_date}（UTC+8）</span>}
    </div>
    {data && <>
      <section className="space-y-2"><h3 className="font-medium">模型使用</h3>
        <p>总请求 <MetricValue value={data.total_requests} quality={data.domains.models?.quality} /> · 账面消耗 {money(data.total_actual_usd)}</p>
        <Table><TableHeader><TableRow>{["请求模型", "请求数", "账面消耗", "缓存读取率", ...(spread ? ["账面与账号差额"] : [])].map(x => <TableHead key={x}>{x}</TableHead>)}</TableRow></TableHeader>
          <TableBody>{data.models?.map((m, i) => <TableRow key={`${m.model}:${i}`}>
            <TableCell>{m.model}{m.has_mismatch && <div><Badge variant="outline" className="text-amber-700">请求模型与上游模型不一致</Badge> · {m.mismatch_requests ?? "—"} 次</div>}</TableCell>
            <TableCell><MetricValue value={m.requests} quality={m.quality} /></TableCell><TableCell>{money(m.actual_usd)}</TableCell>
            <TableCell><CacheRate value={m.cache.cache_read_hit_pct} quality={m.cache.token_quality} /></TableCell>
            {spread && <TableCell>{m.has_cost_spread && m.actual_usd != null && m.account_usd != null ? money(m.actual_usd - m.account_usd) : "—"}</TableCell>}
          </TableRow>)}</TableBody>
        </Table>
        {!data.models?.length && <p className="text-muted-foreground">暂无模型记录。</p>}
        <details className="rounded-md border p-3">
          <summary className="cursor-pointer">请求模型与上游模型不一致 · {data.mismatches.total_requests ?? "—"} 次请求 · {data.mismatches.quality === "trusted" ? data.mismatches.model_rows : "—"} 行</summary>
          <FieldQualityBadge quality={data.mismatches.quality} />
          <Table><TableHeader><TableRow>{["请求模型", "上游模型", "异常请求数", "账面消耗"].map(x => <TableHead key={x}>{x}</TableHead>)}</TableRow></TableHeader>
            <TableBody>{data.mismatches.rows?.map((m, i) => <TableRow key={i}>
              <TableCell>{m.requested_model}</TableCell><TableCell>{m.upstream_model || "—"}</TableCell><TableCell>{m.requests ?? "—"}</TableCell><TableCell>{money(m.actual_usd)}</TableCell>
            </TableRow>)}</TableBody>
          </Table>
        </details>
      </section>
      <section className="space-y-2"><h3 className="font-medium">分组使用</h3>
        <Table><TableHeader><TableRow>{["分组", "请求数", "账面消耗", "活跃订阅（可见目录页）", "单席参考月价 / 折算 USD", "操作"].map(x => <TableHead key={x}>{x}</TableHead>)}</TableRow></TableHeader>
          <TableBody>{data.groups?.map(g => <TableRow key={g.group_id}>
            <TableCell>{g.group_name || `分组 #${g.group_id}`}</TableCell><TableCell>{g.requests ?? "—"}</TableCell><TableCell>{money(g.actual_usd)}</TableCell>
            <TableCell><MetricValue value={g.active_sub_count} quality={data.domains.subscriptions?.quality} /></TableCell>
            <TableCell>{nativePrice(g.reference_price)} / {money(g.ref_monthly_usd)}</TableCell>
            <TableCell><Button size="sm" variant="outline" onClick={() => onPrice(g.group_id)}>编辑分组默认价</Button></TableCell>
          </TableRow>)}</TableBody>
        </Table>
        {!data.groups?.length && <p className="text-muted-foreground">暂无分组记录。</p>}
      </section>
    </>}
  </section>
}

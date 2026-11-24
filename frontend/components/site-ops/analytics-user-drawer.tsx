import type { ReactNode } from "react"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { money } from "@/lib/format"
import type { SiteOpsAnalyticsUserDetailResponse } from "@/lib/site-ops-types"
import { analyticsTime } from "./analytics-lights-bar"
import { CacheRate, PageControls } from "./analytics-users-table"
import { FieldQualityBadge, MetricValue } from "./quality-badge"

export function AnalyticsUserDrawer({ id, data, children, disabled, onClose, onPage }: {
  id: number; data?: SiteOpsAnalyticsUserDetailResponse; children?: ReactNode
  disabled: boolean; onClose: () => void; onPage: (page: number) => void
}) {
  const windows = [data?.user.all_time, data?.last_30d, data?.user.last_7d]
  const spread = windows.some(w => w?.has_cost_spread)
  return <Sheet open onOpenChange={open => { if (!open) onClose() }}>
    <SheetContent className="w-full gap-0 sm:max-w-3xl">
      <SheetHeader><SheetTitle>用户详情{data?.user.user_id === id && ` · U#${id}`}</SheetTitle>
        <SheetDescription>{data?.user.email_masked ?? "用户使用与订阅记录"}</SheetDescription></SheetHeader>
      <div className="min-h-0 flex-1 space-y-5 overflow-auto px-4 pb-4 text-sm">
        {children}
        {data?.user.user_id === id && <>
          <section><h3 className="mb-2 font-medium">三窗消耗对照（USD）</h3>
            <Table><TableHeader><TableRow>{["窗口", "请求", "标准价", "账面消耗", ...(spread ? ["账面与账号差额"] : []), "缓存读取率", "写入率", "读写比"].map(x => <TableHead key={x}>{x}</TableHead>)}</TableRow></TableHeader>
              <TableBody>{windows.map((w, i) => <TableRow key={i}>
                <TableCell>{["全时段 All", "近 30 天", "近 7 天"][i]}{w?.start_date && <div className="text-xs text-muted-foreground">{w.start_date} 至 {w.end_date}</div>}</TableCell>
                {!w || w.quality !== "trusted" ? <TableCell colSpan={spread ? 7 : 6}>
                  <FieldQualityBadge quality={w?.quality ?? (data.domains.user_stats?.quality === "trusted" ? "unavailable" : data.domains.user_stats?.quality)} reason={w ? undefined : data.domains.user_stats?.reason} />
                </TableCell> : <>
                <TableCell><MetricValue value={w.requests} /></TableCell><TableCell>{money(w?.standard_usd)}</TableCell><TableCell>{money(w?.actual_usd)}</TableCell>
                {spread && <TableCell>{w?.has_cost_spread && w.actual_usd != null && w.account_usd != null ? money(w.actual_usd - w.account_usd) : "—"}</TableCell>}
                <TableCell><CacheRate quality={w?.cache.token_quality} value={w?.cache.cache_read_hit_pct} /></TableCell>
                <TableCell><CacheRate quality={w?.cache.token_quality} value={w?.cache.cache_write_pct} /></TableCell>
                <TableCell><CacheRate quality={w?.cache.token_quality} value={w?.cache.cache_rw_ratio} unit="×" /></TableCell>
              </>}
              </TableRow>)}</TableBody>
            </Table>
          </section>
          <section><h3 className="mb-2 font-medium">名下订阅（可见目录页）</h3>
            <ul className="divide-y">{data.subscriptions?.map(s => <li key={s.subscription_id} className="py-2">
              #{s.subscription_id} · {s.group_name || `分组 #${s.group_id}`} · {s.status}
              <div className="text-xs text-muted-foreground">{analyticsTime(s.starts_at)} 至 {analyticsTime(s.expires_at)}（UTC+8）</div>
            </li>)}</ul>
            {!data.subscriptions?.length && <p className="text-muted-foreground">本页暂无可见订阅。</p>}
          </section>
          <section className="space-y-2"><h3 className="font-medium">Key 消耗拆解</h3>
            <Table><TableHeader><TableRow>{["Key", "分组", "状态", "今日账面消耗", "上游累计消耗"].map(x => <TableHead key={x}>{x}</TableHead>)}</TableRow></TableHeader>
              <TableBody>{data.api_keys.items?.map(k => <TableRow key={k.api_key_id}>
                <TableCell>Key #{k.api_key_id}</TableCell><TableCell>{k.group_name || (k.group_id ? `#${k.group_id}` : "—")}</TableCell><TableCell>{k.status || "—"}</TableCell>
                <TableCell><MetricValue value={k.today_actual_usd} format={money} quality={k.quality} /></TableCell>
                <TableCell><MetricValue value={k.upstream_total_actual_usd} format={money} quality={k.quality} /></TableCell>
              </TableRow>)}</TableBody>
            </Table>
            {!data.api_keys.items?.length && <p>暂无 Key 记录 <FieldQualityBadge quality={data.domains.user_keys?.quality} /></p>}
            <PageControls page={data.api_keys} onPage={onPage} disabled={disabled} />
          </section>
        </>}
      </div>
    </SheetContent>
  </Sheet>
}

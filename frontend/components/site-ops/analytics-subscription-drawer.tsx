import { useEffect, useState, type ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { decimal, money } from "@/lib/format"
import type { SiteOpsAnalyticsSubscriptionDetailResponse, SiteOpsAnalyticsSubscriptionTermLayerView } from "@/lib/site-ops-types"
import { analyticsTime } from "./analytics-lights-bar"
import { Attribution, nativePrice, rollingWindows } from "./analytics-subscriptions-table"
import { FieldQualityBadge, MetricNote } from "./quality-badge"

const labels: Record<string, string> = {
  active: "进行中", completed: "已完成", expired: "已到期", revoked: "已撤销", not_started: "待开始",
  insufficient: "样本不足", high: "高", medium: "中", low: "低", unknown: "待确认", partial: "部分验证",
  verified: "已验证", unlimited: "无限额", daily: "日窗", weekly: "周窗", monthly: "月窗",
  unconfigured: "未定价", free: "赠送", threshold_disabled: "阈值已关闭", normal: "正常", warn: "预警", danger: "高风险",
}

function TermLayer({ title, layer, groupOnly }: { title: string; layer: SiteOpsAnalyticsSubscriptionTermLayerView | null; groupOnly: boolean }) {
  const usage = groupOnly ? layer?.group_usage : layer?.usage
  return <section className="space-y-2 rounded-lg border p-3">
    <h3 className="font-medium">{title}</h3>
    {!layer ? <FieldQualityBadge /> : <>
      <p className="text-xs text-muted-foreground">{analyticsTime(layer.start_at)} 至 {analyticsTime(layer.end_at)}</p>
      <p>{labels[layer.forecast_status] ?? layer.forecast_status} · 全长 {decimal(layer.duration_days)} 天
        {layer.observed_until && ` · 剩余 ${decimal(layer.remaining_hours)} 小时`}</p>
      <p>{groupOnly ? "用户×分组实耗" : "本期账面实耗"} <strong>{money(usage?.actual_usd)}</strong></p>
      {!groupOnly && <dl className="grid grid-cols-[1fr_auto] gap-1 text-xs">
        <dt>全期日耗（慢速）</dt><dd>{money(layer.burn_slow_usd_day)}</dd>
        <dt>近期日耗（快速{layer.burn_fast_source && ` · ${labels[layer.burn_fast_source] ?? layer.burn_fast_source}`}）</dt><dd>{money(layer.burn_fast_usd_day)}</dd>
        <dt>加权日耗</dt><dd>{money(layer.burn_effective_usd_day)}</dd>
        <dt>未封顶预估</dt><dd>{money(layer.uncapped_estimate_usd)}</dd>
        <dt>预估期末</dt><dd>{money(layer.projected_end_usd)}</dd>
        <dt>理论封顶</dt><dd>{money(layer.theoretical_max_usd)}</dd>
        <dt>封顶依据</dt><dd>{labels[layer.cap_state] ?? "待确认"}</dd>
        <dt>封顶窗</dt><dd>{labels[layer.cap_binding_window ?? ""] ?? "—"}{layer.capped_by_limit && " · 已约束"}</dd>
        <dt>置信度</dt><dd>{labels[layer.confidence] ?? "待确认"}</dd>
      </dl>}
      {layer.reason && <p className="text-xs">{layer.reason}</p>}
      <p className="flex items-center gap-1 text-xs text-muted-foreground">自然日边界
        <MetricNote label="自然日边界说明">{layer.day_boundary_note || "统计窗口按上海自然日结算。"}</MetricNote>
        {layer.boundary_approx && " · 边界日近似"}
      </p>
    </>}
  </section>
}

export function AnalyticsSubscriptionDrawer({ id, data, children, onClose, onPrice }: {
  id: number; data?: SiteOpsAnalyticsSubscriptionDetailResponse; children?: ReactNode; onClose: () => void; onPrice: () => void
}) {
  const [now, setNow] = useState(Date.now)
  const row = data?.subscription.subscription_id === id ? data.subscription : undefined
  const resets = row && rollingWindows(row).some(([, w]) => w.resets_at)
  useEffect(() => {
    if (!resets) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [resets])
  const groupOnly = row?.attribution !== "unique_group_term"
  const price = row?.pricing_compare
  return <Sheet open onOpenChange={open => { if (!open) onClose() }}>
    <SheetContent className="w-full gap-0 sm:max-w-5xl">
      <SheetHeader><SheetTitle>订阅{row && ` #${id}`} · 三层精算详情</SheetTitle>
        <SheetDescription>{row && `U#${row.user_id} · ${row.group_name} · `}日历与重置时间均为 UTC+8</SheetDescription></SheetHeader>
      <div className="min-h-0 flex-1 space-y-4 overflow-auto px-4 pb-4 text-sm">
        {children}
        {row && data && <>
          <div className="space-y-1"><Attribution row={row} />
            {groupOnly && <p>仅展示用户×分组订阅汇总。{row.attribution_reason || "订阅归属无法唯一确认。"} 专属预测与杠杆暂停计算。</p>}
          </div>
          <div className="grid items-start gap-2 lg:grid-cols-3">
            <section className="space-y-2 rounded-lg border p-3"><h3 className="font-medium">L1 · 滚动用量</h3>
              {rollingWindows(row).map(([label, w]) => {
                const end = Date.parse(w.resets_at ?? "")
                const seconds = Math.max(0, Math.ceil((end - now) / 1000))
                return <div key={label} className="space-y-1 border-t pt-2 text-xs">
                  <div>{label}窗 · {w.source === "progress" ? "精确进度" : "目录快照"} <FieldQualityBadge quality={w.quality} /></div>
                  <p>已用 {money(w.used_usd)} / 限额 {money(w.limit_usd)} · 剩余 {money(w.remaining_usd)}</p>
                  <p>重置 {analyticsTime(w.resets_at)}{Number.isFinite(end) &&
                    `（${Math.floor(seconds / 3600)}时${Math.floor(seconds % 3600 / 60)}分${seconds % 60}秒）`}</p>
                </div>
              })}
            </section>
            <TermLayer title={`L2 · 当前 30 天子周期（${row.cycle_index || "—"}/${row.total_cycles || "—"}）`} layer={data.current_sub_cycle} groupOnly={groupOnly} />
            <TermLayer title="L3 · 连续总期" layer={data.continuous_term} groupOnly={groupOnly} />
          </div>
          {price && <section className="space-y-2 rounded-lg border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="font-medium">本地参考收款</h3><Button size="sm" variant="outline" onClick={onPrice}>设参考收款</Button></div>
            <p>参考月价 {nativePrice(price.reference_price)} · 当期折算 {money(price.cycle_ref_usd)} · 总期折算 {money(price.term_ref_usd)}</p>
            {!groupOnly && <p className="flex flex-wrap items-center gap-2">实际杠杆 {decimal(price.actual_leverage_x)}× · 预计杠杆 {decimal(price.projected_leverage_x)}×
              <Badge variant={price.margin_risk === "danger" ? "destructive" : "outline"} className={price.margin_risk === "warn" ? "text-amber-700" : ""}>{labels[price.margin_risk] ?? "待确认"}</Badge>
            </p>}
          </section>}
        </>}
      </div>
    </SheetContent>
  </Sheet>
}

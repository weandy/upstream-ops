import { CircleCheck, CircleHelp, CircleX, TriangleAlert } from "lucide-react"
import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { decimal } from "@/lib/format"
import type {
  SiteOpsAnalyticsChannelResponse, SiteOpsAnalyticsLightsResponse, SiteOpsAnalyticsRiskControlLampView,
  SiteOpsAnalyticsSafetyLightLevel, SiteOpsFieldQualityState,
} from "@/lib/site-ops-types"
import { CapabilityStateBadge, FieldQualityBadge } from "./quality-badge"

const shanghai = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai", dateStyle: "short", timeStyle: "medium", hourCycle: "h23",
})

export function analyticsTime(iso?: string | null) {
  const ms = Date.parse(iso ?? "")
  return Number.isFinite(ms) ? shanghai.format(ms) : "—"
}

const levels = {
  green: [CircleCheck, "正常", "text-emerald-600"],
  amber: [TriangleAlert, "预警", "text-amber-600"],
  red: [CircleX, "告警", "text-destructive"],
  unknown: [CircleHelp, "待确认", "text-muted-foreground"],
} as const

function riskLevel(risk: SiteOpsAnalyticsRiskControlLampView) {
  if (!risk.enabled || !risk.risk_control_enabled) return "unknown"
  if (risk.level !== "green") return risk.level
  const required = [risk.dropped, risk.errors, risk.processed, risk.queue_usage_percent,
    risk.pre_block_avg_latency_ms, risk.pre_block_checked, risk.pre_block_errors]
  if (risk.pre_block_checked) required.push(risk.pre_block_api_key_available_count)
  return required.every(v => v !== null && Number.isFinite(v)) ? "green" : "unknown"
}

function Lamp({ title, level, quality, observed, reasons, children }: {
  title: string; level?: SiteOpsAnalyticsSafetyLightLevel; quality?: SiteOpsFieldQualityState
  observed?: string | null; reasons?: string[] | null; children: ReactNode
}) {
  const [Icon, label, color] = levels[quality === "trusted" && level ? level : "unknown"]
  return <article className="space-y-2 rounded-lg border p-3">
    <div className="flex flex-wrap items-center gap-2">
      <h3 className="font-medium">{title}</h3>
      <span className={`inline-flex items-center gap-1 ${color}`}><Icon className="size-4" />{label}</span>
      <FieldQualityBadge quality={quality} reason={reasons?.join(" · ")} />
    </div>
    <p className="text-xs">{children}</p>
    {reasons?.length ? <p className="text-xs">{reasons.join(" · ")}</p> : null}
    <p className="text-xs text-muted-foreground">观测于 {analyticsTime(observed)}（UTC+8）</p>
  </article>
}

export function AnalyticsLightsBar({ data, channel, busy, disabled, onCheck }: {
  data?: SiteOpsAnalyticsLightsResponse; channel?: SiteOpsAnalyticsChannelResponse
  busy: boolean; disabled: boolean; onCheck: () => void
}) {
  const risk = data?.risk_control
  const backup = data?.backup
  const capability = channel?.capability ?? data?.channel_monitor
  return <section aria-label="站点安全状态" className="grid gap-2 text-sm lg:grid-cols-3">
    <Lamp title="风控引擎" level={risk && riskLevel(risk)} quality={risk?.quality}
      observed={risk?.observed_at} reasons={risk?.reasons}>
      队列 {decimal(risk?.queue_usage_percent)}% · 拦截均值 {decimal(risk?.pre_block_avg_latency_ms)} ms · 可用 Key {decimal(risk?.pre_block_api_key_available_count)}
    </Lamp>
    <Lamp title="数据备份" level={backup?.level} quality={backup?.quality}
      observed={backup?.observed_at} reasons={backup?.reasons}>
      最近完成 {analyticsTime(backup?.last_completed_at)} · {backup?.backup_type || "类型未知"} · {decimal(backup?.size_bytes == null ? null : backup.size_bytes / 1048576)} MiB
    </Lamp>
    <article className="space-y-2 rounded-lg border p-3">
      <div className="flex items-center gap-2"><h3 className="font-medium">通道监控</h3><CapabilityStateBadge state={capability?.state ?? "unknown"} /></div>
      {channel?.note && <p className="text-xs">{channel.note}</p>}
      <Button variant="outline" size="sm" onClick={onCheck} disabled={busy || disabled}>{busy ? "检查中…" : "检查通道监控"}</Button>
    </article>
  </section>
}

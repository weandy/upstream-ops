import { CircleCheck, CircleHelp, CircleX, TriangleAlert } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { decimal } from "@/lib/format"
import type {
  SiteOpsAnalyticsChannelResponse,
  SiteOpsAnalyticsConfig,
  SiteOpsAnalyticsLightsResponse,
  SiteOpsAnalyticsRiskControlLampView,
  SiteOpsAnalyticsSafetyLightLevel,
  SiteOpsFieldQualityState,
} from "@/lib/site-ops-types"

const shanghai = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai",
  dateStyle: "short",
  timeStyle: "medium",
  hourCycle: "h23",
})

export function analyticsTime(iso?: string | null) {
  const ms = Date.parse(iso ?? "")
  return Number.isFinite(ms) ? shanghai.format(ms) : "—"
}

const levels = {
  green: [CircleCheck, "正常", "border-emerald-500/35 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"],
  amber: [TriangleAlert, "预警", "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"],
  red: [CircleX, "异常", "border-destructive/40 bg-destructive/10 text-destructive"],
  unknown: [CircleHelp, "待确认", "text-muted-foreground"],
} as const

function riskLevel(risk?: SiteOpsAnalyticsRiskControlLampView): SiteOpsAnalyticsSafetyLightLevel {
  if (!risk || !risk.enabled || !risk.risk_control_enabled) return "unknown"
  if (risk.level !== "green") return risk.level
  const required = [
    risk.dropped,
    risk.errors,
    risk.processed,
    risk.queue_usage_percent,
    risk.pre_block_avg_latency_ms,
    risk.pre_block_checked,
    risk.pre_block_errors,
  ]
  if (risk.pre_block_checked) required.push(risk.pre_block_api_key_available_count)
  return required.every((v) => v !== null && Number.isFinite(v)) ? "green" : "unknown"
}

function StatusPill({
  title,
  level,
  quality,
  detail,
  tooltip,
}: {
  title: string
  level: SiteOpsAnalyticsSafetyLightLevel
  quality?: SiteOpsFieldQualityState
  detail?: string
  tooltip: string
}) {
  const effective: SiteOpsAnalyticsSafetyLightLevel =
    quality === "trusted" ? level : "unknown"
  const [Icon, stateLabel, cls] = levels[effective]
  const suffix =
    quality === "disabled"
      ? "已关闭"
      : quality === "unauthorized"
        ? "未授权"
        : quality === "trusted"
          ? detail || stateLabel
          : "待确认"

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge tabIndex={0} variant="outline" className={`gap-1.5 py-1 text-xs font-medium ${cls}`}>
          <Icon className="size-3.5 shrink-0" />
          <span>{title}</span>
          <span className="opacity-85">· {suffix}</span>
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs text-xs">{tooltip}</TooltipContent>
    </Tooltip>
  )
}

export function AnalyticsLightsBar({
  data,
  channel,
  config,
  busy,
  disabled,
  onCheck,
}: {
  data?: SiteOpsAnalyticsLightsResponse
  channel?: SiteOpsAnalyticsChannelResponse
  config?: SiteOpsAnalyticsConfig
  busy: boolean
  disabled: boolean
  onCheck: () => void
}) {
  const risk = data?.risk_control
  const backup = data?.backup
  const capability = channel?.capability ?? data?.channel_monitor

  const riskDetail =
    risk?.quality === "trusted" && risk.queue_usage_percent != null
      ? `正常 · 队列 ${decimal(risk.queue_usage_percent, 0)}%`
      : undefined
  const riskTooltip = risk?.reasons?.length
    ? risk.reasons.join(" · ")
    : `队列 ${decimal(risk?.queue_usage_percent)}% · 拦截均值 ${decimal(risk?.pre_block_avg_latency_ms)} ms · 观测于 ${analyticsTime(risk?.observed_at)}`

  const backupAge =
    backup?.age_hours != null && Number.isFinite(backup.age_hours)
      ? backup.age_hours < 1
        ? "1小时内"
        : `${Math.round(backup.age_hours)}小时前`
      : undefined
  const backupDetail =
    backup?.quality === "trusted" && backup.level === "green"
      ? backupAge
        ? `正常 · ${backupAge}`
        : "正常"
      : undefined
  const backupTooltip = backup?.reasons?.length
    ? backup.reasons.join(" · ")
    : `最近完成 ${analyticsTime(backup?.last_completed_at)} · ${backup?.backup_type || "默认备份"} · ${decimal(backup?.size_bytes == null ? null : backup.size_bytes / 1048576)} MiB`

  const nearPct = config?.near_full_pct ?? 85
  const lowPct = config?.under_use_pct ?? 35

  return (
    <section
      aria-label="站点运行状态与预测规则"
      className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-card px-3 py-2 text-xs"
    >
      <div className="flex flex-wrap items-center gap-2">
        <StatusPill
          title="风控"
          level={riskLevel(risk)}
          quality={risk?.quality}
          detail={riskDetail}
          tooltip={riskTooltip}
        />
        <StatusPill
          title="备份"
          level={backup?.level ?? "unknown"}
          quality={backup?.quality}
          detail={backupDetail}
          tooltip={backupTooltip}
        />
        <Button
          variant="ghost"
          size="sm"
          className="h-7 px-2 text-xs text-muted-foreground"
          onClick={onCheck}
          disabled={busy || disabled}
        >
          {busy
            ? "检查通道监控…"
            : capability?.state === "supported"
              ? "通道监控已开启"
              : capability?.state === "ops_disabled"
                ? "通道监控未开启"
                : "检查通道监控"}
        </Button>
      </div>
      <span className="text-xs text-muted-foreground tabular-nums">
        接近用满 ≥{nearPct}% · 偏低 &lt;{lowPct}% · 峰值并发每分钟采样
      </span>
    </section>
  )
}

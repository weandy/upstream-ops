import { Info } from "lucide-react"
import type { ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { decimal } from "@/lib/format"
import type {
  SiteOpsCapabilityState,
  SiteOpsFieldQualityState,
  SiteOpsInstanceStatus,
} from "@/lib/site-ops-types"

const qualityLabels: Record<SiteOpsFieldQualityState, [string, string]> = {
  trusted: ["已证实", "采集端已验证该数据域"],
  unverified_zero: ["未证实零值", "实时计数未证实，无法判断当前负载"],
  stale: ["已过期", "最近一次可信采样已超过有效时限"],
  disabled: ["Ops 已关闭", "站点未启用此 Ops 数据域"],
  unauthorized: ["未授权", "当前凭据无法访问此数据域"],
  unavailable: ["无数据", "暂无可用样本"],
}

const statusLabels: Record<SiteOpsInstanceStatus, [string, string]> = {
  online: ["在线", "bg-emerald-500"],
  degraded: ["降级", "bg-amber-500"],
  offline: ["离线", "bg-red-500"],
  auth_error: ["鉴权异常", "bg-red-500"],
  paused_no_auth: ["已暂停", "bg-muted-foreground"],
  pending: ["待观测", "bg-muted-foreground"],
}

const capabilityLabels: Record<SiteOpsCapabilityState, string> = {
  supported: "支持",
  forbidden: "未授权",
  ops_disabled: "Ops 已关闭",
  unsupported: "不支持",
  error: "探测异常",
  unknown: "待探测",
}

export function MetricNote({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button type="button" aria-label={label} className="inline-flex text-muted-foreground">
          <Info className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{children}</TooltipContent>
    </Tooltip>
  )
}

export function FieldQualityBadge({
  quality = "unavailable",
  reason,
}: {
  quality?: SiteOpsFieldQualityState
  reason?: string
}) {
  const [label, description] = qualityLabels[quality] ?? qualityLabels.unavailable
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge tabIndex={0} variant="outline" className={
          quality === "unverified_zero" || quality === "stale"
            ? "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"
            : "text-muted-foreground"
        }>
          {label}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{reason || description}</TooltipContent>
    </Tooltip>
  )
}

export function InstanceStatusBadge({ status }: { status: SiteOpsInstanceStatus }) {
  const [label, color] = statusLabels[status] ?? statusLabels.pending
  return (
    <Badge variant="outline">
      <span aria-hidden="true" className={`size-1.5 rounded-full ${color}`} />
      {label}
    </Badge>
  )
}

export function CapabilityStateBadge({ state }: { state: SiteOpsCapabilityState }) {
  return <Badge variant="outline">{capabilityLabels[state] ?? capabilityLabels.unknown}</Badge>
}

export function MetricValue({
  value,
  quality = "trusted",
  reason,
  format = decimal,
  unit,
}: {
  value?: number | null
  quality?: SiteOpsFieldQualityState
  reason?: string
  format?: (value: number) => string
  unit?: string
}) {
  if (quality !== "trusted") return <FieldQualityBadge quality={quality} reason={reason} />
  if (value == null || !Number.isFinite(value)) return <FieldQualityBadge reason={reason} />
  return <span className="tabular-nums">{format(value)}{unit}</span>
}

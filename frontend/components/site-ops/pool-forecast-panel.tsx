import { Fragment, useMemo, useState } from "react"
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Info,
  RefreshCw,
  Search,
  ShieldAlert,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { decimal } from "@/lib/format"
import type {
  SiteOpsAccountForecastView,
  SiteOpsFieldQualityState,
  SiteOpsForecastConfidence,
  SiteOpsForecastStatus,
  SiteOpsPassiveSupport,
  SiteOpsPoolForecastResponse,
  SiteOpsWindowForecastView,
} from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote } from "./quality-badge"

export interface PoolForecastPanelProps {
  data?: SiteOpsPoolForecastResponse
  loading: boolean
  error?: string
  onReload?: () => void
}

type PassiveFilter = "all" | "passive_proven" | "passive_unverified" | "excluded_payg"
type RiskFilter =
  | "all"
  | "at_risk"
  | "exhausted"
  | "will_exhaust_high"
  | "will_exhaust"
  | "tight"
  | "safe_idle"
  | "insufficient_data"

const defaultResetConstraintNote =
  "单个窗口到点重置仅代表该项约束可能解除，不保证账号一定恢复可用（仍可能受更长周期窗口或账号状态约束）。"
const defaultPassiveMatrixNote =
  "当前站点暂无通过纯被动只读证明矩阵的订阅号。为避免对未证实平台触发主动上游请求或写副作用，系统不会盲目调用 usage/batch 自动拉取。"
const expiresSeparationNote =
  "账号本身到期时间（expires_at）独立于各用量窗口重置时间（resets_at），两者不混用。"

function formatUTC8DateTime(iso?: string | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).formatToParts(d)
  const map = new Map(parts.map((part) => [part.type, part.value]))
  return `${map.get("year")}/${map.get("month")}/${map.get("day")} ${map.get("hour")}:${map.get("minute")}:${map.get("second")}`
}

function formatWindowKey(key?: string): string {
  if (!key) return "—"
  switch (key.toLowerCase()) {
    case "five_hour":
    case "5h":
      return "5 小时窗"
    case "daily":
    case "24h":
      return "24 小时窗"
    case "seven_day":
    case "7d":
    case "weekly":
      return "7 日窗"
    case "seven_day_sonnet":
      return "7 日 Sonnet 窗"
    case "seven_day_fable":
      return "7 日 Fable 窗"
    case "thirty_day":
    case "30d":
    case "monthly":
      return "30 日窗"
    default:
      return key
  }
}

function formatDurationSeconds(seconds?: number | null): string {
  if (seconds == null || !Number.isFinite(seconds)) return "—"
  if (seconds <= 0) return "0 分钟"
  const totalMinutes = Math.round(seconds / 60)
  if (totalMinutes < 60) return `${totalMinutes} 分钟`
  const hours = seconds / 3600
  if (hours < 48) return `${decimal(hours, 1)} 小时`
  const days = Math.floor(hours / 24)
  const remHours = hours - days * 24
  return remHours >= 0.5 ? `${days} 天 ${decimal(remHours, 1)} 小时` : `${days} 天`
}

function formatEtaCell(
  etaSeconds: number | null | undefined,
  status: SiteOpsForecastStatus,
): string {
  if (etaSeconds == null || !Number.isFinite(etaSeconds)) return "—"
  if (etaSeconds <= 0 && status === "exhausted") return "已耗尽"
  return formatDurationSeconds(etaSeconds)
}

function formatMarginHours(marginHours?: number | null): string {
  if (marginHours == null || !Number.isFinite(marginHours)) return "—"
  const sign = marginHours > 0 ? "+" : ""
  return `${sign}${decimal(marginHours, 1)}h`
}

function PassiveSupportBadge({
  support,
  reason,
}: {
  support: SiteOpsPassiveSupport
  reason?: string
}) {
  let label = support || "未知"
  let className = "text-muted-foreground"
  let defaultReason = "当前账号的被动只读准入状态"

  switch (support) {
    case "passive_proven":
      label = "被动证实"
      className =
        "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
      defaultReason = "已通过纯被动只读证明矩阵，允许后台周期拉取"
      break
    case "passive_unverified":
      label = "未证实被动"
      className =
        "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"
      defaultReason = "尚未通过纯被动无副作用证明，默认不开启后台 usage/batch 自动拉取"
      break
    case "excluded_payg":
      label = "按量排除"
      className = "border-border bg-muted/60 text-muted-foreground"
      defaultReason = "按量 API Key / Upstream 转发号无订阅重置周期，永久排除出周期预测分母"
      break
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge tabIndex={0} variant="outline" className={className}>
          {label}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{reason || defaultReason}</TooltipContent>
    </Tooltip>
  )
}

function ForecastStatusBadge({ status }: { status: SiteOpsForecastStatus }) {
  switch (status) {
    case "exhausted":
      return <Badge variant="destructive">已耗尽</Badge>
    case "will_exhaust_high":
      return <Badge variant="destructive">高危耗尽</Badge>
    case "will_exhaust":
      return (
        <Badge
          variant="outline"
          className="border-orange-500/40 bg-orange-500/10 text-orange-700 dark:text-orange-300"
        >
          预计耗尽
        </Badge>
      )
    case "tight":
      return (
        <Badge
          variant="outline"
          className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"
        >
          偏紧
        </Badge>
      )
    case "safe":
      return (
        <Badge
          variant="outline"
          className="border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
        >
          安全
        </Badge>
      )
    case "idle":
      return (
        <Badge
          variant="outline"
          className="border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300"
        >
          闲置
        </Badge>
      )
    case "insufficient_data":
      return (
        <Badge variant="outline" className="text-muted-foreground">
          数据不足
        </Badge>
      )
    default:
      return <Badge variant="outline">{status || "未知"}</Badge>
  }
}

function ConfidenceBadge({ confidence }: { confidence: SiteOpsForecastConfidence }) {
  switch (confidence) {
    case "high":
      return (
        <Badge
          variant="outline"
          className="border-emerald-500/30 bg-emerald-500/5 text-[11px] text-emerald-700 dark:text-emerald-300"
        >
          置信高
        </Badge>
      )
    case "medium":
      return (
        <Badge
          variant="outline"
          className="border-sky-500/30 bg-sky-500/5 text-[11px] text-sky-700 dark:text-sky-300"
        >
          置信中
        </Badge>
      )
    case "low":
      return (
        <Badge variant="outline" className="text-[11px] text-muted-foreground">
          置信低
        </Badge>
      )
    default:
      return (
        <Badge variant="outline" className="text-[11px] text-muted-foreground">
          {confidence || "—"}
        </Badge>
      )
  }
}

function matchesRiskFilter(account: SiteOpsAccountForecastView, filter: RiskFilter): boolean {
  const status = account.overall_status
  switch (filter) {
    case "all":
      return true
    case "at_risk":
      return status === "exhausted" || status === "will_exhaust_high" || status === "will_exhaust"
    case "exhausted":
      return status === "exhausted"
    case "will_exhaust_high":
      return status === "will_exhaust_high"
    case "will_exhaust":
      return status === "will_exhaust"
    case "tight":
      return status === "tight"
    case "safe_idle":
      return status === "safe" || status === "idle"
    case "insufficient_data":
      return status === "insufficient_data"
  }
}

function WindowDetailSubtable({ windows }: { windows: SiteOpsWindowForecastView[] }) {
  if (windows.length === 0) {
    return (
      <p className="px-2 py-2 text-xs text-muted-foreground">
        暂无被动窗口预测样本。
      </p>
    )
  }

  return (
    <div className="overflow-x-auto rounded-md border bg-background">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/30">
            <TableHead className="h-8 text-xs">窗口</TableHead>
            <TableHead className="h-8 text-xs">当前利用率</TableHead>
            <TableHead className="h-8 text-xs">燃烧率</TableHead>
            <TableHead className="h-8 text-xs">预测期末利用率</TableHead>
            <TableHead className="h-8 text-xs">预计耗尽 ETA</TableHead>
            <TableHead className="h-8 text-xs">距重置余量</TableHead>
            <TableHead className="h-8 text-xs">窗口重置时间（UTC+8）</TableHead>
            <TableHead className="h-8 text-xs">状态 / 置信度</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {windows.map((win) => {
            const clampedUtil = Number.isFinite(win.utilization)
              ? Math.min(100, Math.max(0, win.utilization))
              : 0
            return (
              <TableRow key={`${win.window_key}-${win.source}`}>
                <TableCell className="py-2 text-xs">
                  <div className="flex flex-col">
                    <span className="font-medium">{formatWindowKey(win.window_key)}</span>
                    <span className="font-mono text-[11px] text-muted-foreground">
                      {win.window_key}
                      {win.source ? ` · ${win.source}` : ""}
                    </span>
                  </div>
                </TableCell>
                <TableCell className="py-2 text-xs">
                  <div className="min-w-24 space-y-1">
                    <span className="tabular-nums">
                      {Number.isFinite(win.utilization) ? `${decimal(win.utilization, 1)}%` : "—"}
                    </span>
                    <Progress
                      className="h-1.5"
                      aria-label={`${formatWindowKey(win.window_key)} 利用率`}
                      value={clampedUtil}
                    />
                  </div>
                </TableCell>
                <TableCell className="py-2 text-xs tabular-nums">
                  {win.burn_rate_per_hour != null && Number.isFinite(win.burn_rate_per_hour)
                    ? `${decimal(win.burn_rate_per_hour, 2)}%/h`
                    : "—"}
                </TableCell>
                <TableCell className="py-2 text-xs tabular-nums">
                  {win.predicted_end_util != null && Number.isFinite(win.predicted_end_util) ? (
                    <span
                      className={
                        win.predicted_end_util >= 105
                          ? "font-semibold text-destructive"
                          : win.predicted_end_util >= 100
                            ? "font-semibold text-orange-700 dark:text-orange-300"
                            : win.predicted_end_util >= 85
                              ? "text-amber-700 dark:text-amber-300"
                              : undefined
                      }
                    >
                      {decimal(win.predicted_end_util, 1)}%
                    </span>
                  ) : (
                    "—"
                  )}
                </TableCell>
                <TableCell className="py-2 text-xs tabular-nums">
                  {formatEtaCell(win.eta_seconds, win.forecast_status)}
                </TableCell>
                <TableCell className="py-2 text-xs tabular-nums">
                  <span
                    className={
                      win.margin_hours != null && Number.isFinite(win.margin_hours) && win.margin_hours < 0
                        ? "font-medium text-destructive"
                        : undefined
                    }
                  >
                    {formatMarginHours(win.margin_hours)}
                  </span>
                </TableCell>
                <TableCell className="py-2 text-xs tabular-nums">
                  <div className="flex flex-col">
                    <span className="whitespace-nowrap">{formatUTC8DateTime(win.resets_at)}</span>
                    {win.remaining_seconds > 0 ? (
                      <span className="text-[11px] text-muted-foreground">
                        距重置 {formatDurationSeconds(win.remaining_seconds)}
                      </span>
                    ) : null}
                  </div>
                </TableCell>
                <TableCell className="py-2 text-xs">
                  <div className="flex flex-col gap-1">
                    <div className="flex flex-wrap items-center gap-1">
                      <ForecastStatusBadge status={win.forecast_status} />
                      <ConfidenceBadge confidence={win.confidence} />
                    </div>
                    {win.note ? (
                      <span className="max-w-52 text-[11px] text-muted-foreground">
                        {win.note}
                      </span>
                    ) : null}
                  </div>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

export function PoolForecastPanel({
  data,
  loading,
  error,
  onReload,
}: PoolForecastPanelProps) {
  const [passiveFilter, setPassiveFilter] = useState<PassiveFilter>("all")
  const [riskFilter, setRiskFilter] = useState<RiskFilter>("all")
  const [keyword, setKeyword] = useState("")
  const [expandedIds, setExpandedIds] = useState<Record<number, boolean>>({})

  const groups = useMemo(() => data?.groups ?? [], [data?.groups])
  const accounts = useMemo(() => data?.accounts ?? [], [data?.accounts])
  const riskGroups = useMemo(
    () => groups.filter((group) => group.cannot_sustain_risk),
    [groups],
  )

  const filteredAccounts = useMemo(() => {
    const query = keyword.trim().toLowerCase()
    return accounts.filter((account) => {
      if (passiveFilter !== "all" && account.passive_support !== passiveFilter) {
        return false
      }
      if (!matchesRiskFilter(account, riskFilter)) {
        return false
      }
      if (query) {
        const nameMatch = account.name.toLowerCase().includes(query)
        const idMatch = String(account.account_id).includes(query)
        const platformMatch = account.platform.toLowerCase().includes(query)
        if (!nameMatch && !idMatch && !platformMatch) return false
      }
      return true
    })
  }, [accounts, passiveFilter, riskFilter, keyword])

  function toggleExpanded(accountId: number) {
    setExpandedIds((current) => ({ ...current, [accountId]: !current[accountId] }))
  }

  if (error && !data) {
    return (
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-destructive/30 bg-destructive/5 p-4">
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
        {onReload && (
          <Button size="sm" variant="outline" onClick={onReload} disabled={loading}>
            <RefreshCw className={loading ? "animate-spin" : undefined} />
            重试
          </Button>
        )}
      </div>
    )
  }

  if (!data && loading) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        正在加载号池重置周期预测…
      </p>
    )
  }

  if (!data) {
    return (
      <p
        role="status"
        className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground"
      >
        暂无号池周期预测数据。
      </p>
    )
  }

  const trustState: SiteOpsFieldQualityState = data.trust_state || "unavailable"
  const resetNote = data.reset_constraint_note || defaultResetConstraintNote
  const passiveNote = data.passive_matrix_note || defaultPassiveMatrixNote

  return (
    <section className="space-y-3">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
          <h2 className="text-sm font-semibold">被动号池重置周期预测</h2>
          <FieldQualityBadge quality={trustState} />
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            窗口重置口径
            <MetricNote label="窗口重置约束说明">{resetNote}</MetricNote>
          </span>
          <span className="text-xs text-muted-foreground">
            最近观测（UTC+8）{formatUTC8DateTime(data.observed_at)}
          </span>
        </div>
        {onReload && (
          <Button size="sm" variant="outline" onClick={onReload} disabled={loading}>
            <RefreshCw className={loading ? "animate-spin" : undefined} />
            刷新
          </Button>
        )}
      </header>

      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}

      {/* 顶部断档预警横幅 */}
      {riskGroups.length > 0 && (
        <div
          role="alert"
          className="flex items-start gap-2.5 rounded-md border border-destructive/40 bg-destructive/10 px-3.5 py-2.5 text-xs text-destructive"
        >
          <ShieldAlert className="mt-0.5 size-4 shrink-0" />
          <div className="space-y-1">
            <p className="font-semibold">
              检测到 {riskGroups.length} 个分组存在号池断档风险（预计无法支撑到窗口重置）
            </p>
            <div className="flex flex-wrap gap-x-3 gap-y-1 text-destructive/90">
              {riskGroups.map((group) => (
                <span key={group.group_id}>
                  <strong>{group.group_name}</strong>（{group.platform}
                  {group.high_risk_ratio_pct != null && Number.isFinite(group.high_risk_ratio_pct)
                    ? `，高危占比 ${decimal(group.high_risk_ratio_pct, 1)}%`
                    : ""}
                  {group.risk_note ? `：${group.risk_note}` : ""}）
                </span>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* 被动矩阵说明条：proven_count === 0 时清晰说明而不报错或造假 */}
      {data.proven_count === 0 ? (
        <div
          role="status"
          className="flex items-start gap-2.5 rounded-md border border-sky-500/35 bg-sky-500/10 px-3.5 py-2.5 text-xs text-sky-900 dark:text-sky-200"
        >
          <Info className="mt-0.5 size-4 shrink-0 text-sky-600 dark:text-sky-300" />
          <div className="space-y-1">
            <p className="font-medium">当前站点尚无被动证实的订阅号（被动证实 0 个）</p>
            <p className="leading-relaxed text-sky-900/90 dark:text-sky-200/90">
              {passiveNote}
            </p>
            <p className="text-[11px] text-sky-800/80 dark:text-sky-300/80 tabular-nums">
              准入矩阵分布：被动证实 {data.proven_count} · 未证实被动 {data.unverified_count} ·
              按量排除 {data.excluded_payg_count}
            </p>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border bg-muted/30 px-3 py-1.5 text-xs text-muted-foreground">
          <span className="tabular-nums">
            被动矩阵准入：被动证实 <strong className="text-foreground">{data.proven_count}</strong>{" "}
            · 未证实被动 <strong className="text-foreground">{data.unverified_count}</strong> ·
            按量排除 <strong className="text-foreground">{data.excluded_payg_count}</strong>
          </span>
          <span className="inline-flex items-center gap-1">
            被动矩阵说明
            <MetricNote label="被动只读证明矩阵说明">{passiveNote}</MetricNote>
          </span>
        </div>
      )}

      {/* 顶部紧凑风险状态统计卡片 */}
      <div className="grid gap-2 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6">
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">已耗尽</span>
          <span
            className={
              data.exhausted_count > 0
                ? "text-sm font-semibold text-destructive tabular-nums"
                : "text-sm font-semibold tabular-nums"
            }
          >
            {data.exhausted_count}
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">高危耗尽</span>
          <span
            className={
              data.will_exhaust_high_count > 0
                ? "text-sm font-semibold text-destructive tabular-nums"
                : "text-sm font-semibold tabular-nums"
            }
          >
            {data.will_exhaust_high_count}
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">预计耗尽</span>
          <span
            className={
              data.will_exhaust_count > 0
                ? "text-sm font-semibold text-orange-700 tabular-nums dark:text-orange-300"
                : "text-sm font-semibold tabular-nums"
            }
          >
            {data.will_exhaust_count}
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">偏紧</span>
          <span
            className={
              data.tight_count > 0
                ? "text-sm font-semibold text-amber-700 tabular-nums dark:text-amber-300"
                : "text-sm font-semibold tabular-nums"
            }
          >
            {data.tight_count}
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">安全 / 闲置</span>
          <span className="text-sm font-semibold text-emerald-700 tabular-nums dark:text-emerald-300">
            {data.safe_or_idle_count}
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">数据不足</span>
          <span className="text-sm font-semibold text-muted-foreground tabular-nums">
            {data.insufficient_count}
          </span>
        </div>
      </div>

      {/* 分组断档风险概览（仅在有分组时显示） */}
      {groups.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-xs font-medium text-muted-foreground">分组断档风险概览</h3>
          <div className="rounded-lg border bg-card">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>分组</TableHead>
                  <TableHead>平台</TableHead>
                  <TableHead>被动证实号</TableHead>
                  <TableHead>未证实 / 按量排除</TableHead>
                  <TableHead>耗尽 / 高危 / 预计 / 偏紧</TableHead>
                  <TableHead>安全 / 不足</TableHead>
                  <TableHead>高危占比</TableHead>
                  <TableHead>断档评估</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groups.map((group) => (
                  <TableRow key={group.group_id}>
                    <TableCell className="font-medium">{group.group_name}</TableCell>
                    <TableCell>{group.platform || "—"}</TableCell>
                    <TableCell className="tabular-nums">{group.proven_accounts}</TableCell>
                    <TableCell className="text-xs text-muted-foreground tabular-nums">
                      {group.unverified_accounts} / {group.excluded_payg_accounts}
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">
                      <span className={group.exhausted_count > 0 ? "font-semibold text-destructive" : undefined}>
                        {group.exhausted_count}
                      </span>
                      {" / "}
                      <span className={group.will_exhaust_high_count > 0 ? "font-semibold text-destructive" : undefined}>
                        {group.will_exhaust_high_count}
                      </span>
                      {" / "}
                      <span className={group.will_exhaust_count > 0 ? "font-medium text-orange-700 dark:text-orange-300" : undefined}>
                        {group.will_exhaust_count}
                      </span>
                      {" / "}
                      <span className={group.tight_count > 0 ? "text-amber-700 dark:text-amber-300" : undefined}>
                        {group.tight_count}
                      </span>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground tabular-nums">
                      {group.safe_or_idle_count} / {group.insufficient_data_count}
                    </TableCell>
                    <TableCell className="tabular-nums">
                      {group.high_risk_ratio_pct != null && Number.isFinite(group.high_risk_ratio_pct)
                        ? `${decimal(group.high_risk_ratio_pct, 1)}%`
                        : "—"}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-col gap-0.5">
                        {group.cannot_sustain_risk ? (
                          <Badge variant="destructive" className="w-fit">
                            <AlertTriangle className="size-3" />
                            可能断档
                          </Badge>
                        ) : (
                          <Badge
                            variant="outline"
                            className="w-fit border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                          >
                            可稳撑
                          </Badge>
                        )}
                        {group.risk_note ? (
                          <span className="max-w-52 text-[11px] text-muted-foreground">
                            {group.risk_note}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
      )}

      {/* 账号窗口周期预测筛选条 */}
      <div className="flex flex-wrap items-center gap-2">
        <Select
          value={passiveFilter}
          onValueChange={(value) => setPassiveFilter(value as PassiveFilter)}
        >
          <SelectTrigger className="h-8 w-40 text-xs" aria-label="按被动准入态筛选">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部准入态</SelectItem>
            <SelectItem value="passive_proven">被动证实</SelectItem>
            <SelectItem value="passive_unverified">未证实被动</SelectItem>
            <SelectItem value="excluded_payg">按量排除</SelectItem>
          </SelectContent>
        </Select>

        <Select
          value={riskFilter}
          onValueChange={(value) => setRiskFilter(value as RiskFilter)}
        >
          <SelectTrigger className="h-8 w-40 text-xs" aria-label="按预测风险态筛选">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部风险态</SelectItem>
            <SelectItem value="at_risk">仅耗尽 / 预计耗尽</SelectItem>
            <SelectItem value="exhausted">已耗尽</SelectItem>
            <SelectItem value="will_exhaust_high">高危耗尽</SelectItem>
            <SelectItem value="will_exhaust">预计耗尽</SelectItem>
            <SelectItem value="tight">偏紧</SelectItem>
            <SelectItem value="safe_idle">安全 / 闲置</SelectItem>
            <SelectItem value="insufficient_data">数据不足</SelectItem>
          </SelectContent>
        </Select>

        <div className="relative min-w-48 flex-1 sm:max-w-xs">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="搜索账号名称、ID 或平台…"
            aria-label="按名称、ID 或平台搜索预测账号"
            className="h-8 pl-8 text-xs"
          />
        </div>

        <span className="ml-auto text-xs text-muted-foreground tabular-nums">
          显示 {filteredAccounts.length} / {accounts.length} 个账号
        </span>
      </div>

      {/* 账号窗口周期预测明细表 */}
      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>账号 / ID</TableHead>
              <TableHead>平台 / 类型</TableHead>
              <TableHead>被动准入态</TableHead>
              <TableHead>综合预测 / 置信度</TableHead>
              <TableHead>最早阻塞窗</TableHead>
              <TableHead>距耗尽 ETA / 距重置余量</TableHead>
              <TableHead>窗口利用率概览</TableHead>
              <TableHead>
                <span className="inline-flex items-center gap-1">
                  账号到期（UTC+8）
                  <MetricNote label="账号到期与窗口重置口径分离">
                    {expiresSeparationNote}
                  </MetricNote>
                </span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredAccounts.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9} className="py-6 text-center text-muted-foreground">
                  {accounts.length === 0
                    ? "当前站点暂无账号周期预测记录。"
                    : "没有符合当前筛选条件的账号。"}
                </TableCell>
              </TableRow>
            ) : (
              filteredAccounts.map((account) => {
                const windows = account.windows ?? []
                const hasWindows = windows.length > 0
                const expanded = Boolean(expandedIds[account.account_id])
                return (
                  <Fragment key={account.account_id}>
                    <TableRow>
                      <TableCell className="pr-0">
                        {hasWindows ? (
                          <Button
                            type="button"
                            size="icon-sm"
                            variant="ghost"
                            className="size-6"
                            aria-expanded={expanded}
                            aria-label={expanded ? "收起窗口明细" : "展开窗口明细"}
                            onClick={() => toggleExpanded(account.account_id)}
                          >
                            {expanded ? (
                              <ChevronDown className="size-3.5" />
                            ) : (
                              <ChevronRight className="size-3.5" />
                            )}
                          </Button>
                        ) : (
                          <span className="inline-block w-6 text-center text-muted-foreground">
                            ·
                          </span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col">
                          <span className="max-w-44 truncate font-medium" title={account.name}>
                            {account.name || `账号 #${account.account_id}`}
                          </span>
                          <span className="font-mono text-xs text-muted-foreground">
                            #{account.account_id}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap items-center gap-1">
                          <span>{account.platform || "—"}</span>
                          {account.account_type ? (
                            <Badge variant="secondary" className="text-[11px]">
                              {account.account_type}
                            </Badge>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell>
                        <PassiveSupportBadge
                          support={account.passive_support}
                          reason={account.passive_support_reason}
                        />
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap items-center gap-1">
                          <ForecastStatusBadge status={account.overall_status} />
                          <ConfidenceBadge confidence={account.overall_confidence} />
                        </div>
                      </TableCell>
                      <TableCell className="text-xs">
                        {account.earliest_blocking_window ? (
                          <Badge variant="outline" className="font-normal">
                            {formatWindowKey(account.earliest_blocking_window)}
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs tabular-nums">
                        <div className="flex flex-col gap-0.5">
                          <span>
                            <span className="text-muted-foreground">ETA </span>
                            {formatEtaCell(account.earliest_eta_seconds, account.overall_status)}
                          </span>
                          <span>
                            <span className="text-muted-foreground">余量 </span>
                            <span
                              className={
                                account.earliest_margin_hours != null &&
                                Number.isFinite(account.earliest_margin_hours) &&
                                account.earliest_margin_hours < 0
                                  ? "font-medium text-destructive"
                                  : undefined
                              }
                            >
                              {formatMarginHours(account.earliest_margin_hours)}
                            </span>
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        {!hasWindows ? (
                          <span className="text-xs text-muted-foreground">—</span>
                        ) : (
                          <div className="flex min-w-44 flex-col gap-1.5">
                            {windows.map((win) => {
                              const pct = Number.isFinite(win.utilization)
                                ? Math.min(100, Math.max(0, win.utilization))
                                : 0
                              return (
                                <div key={`${win.window_key}-${win.source}`} className="space-y-0.5">
                                  <div className="flex items-center justify-between gap-2 text-[11px] tabular-nums">
                                    <span className="text-muted-foreground">
                                      {formatWindowKey(win.window_key)}
                                    </span>
                                    <span>
                                      {Number.isFinite(win.utilization)
                                        ? `${decimal(win.utilization, 1)}%`
                                        : "—"}
                                      {win.burn_rate_per_hour != null &&
                                      Number.isFinite(win.burn_rate_per_hour)
                                        ? ` · ${decimal(win.burn_rate_per_hour, 1)}%/h`
                                        : ""}
                                    </span>
                                  </div>
                                  <Progress
                                    className="h-1.5"
                                    aria-label={`${account.name || account.account_id} ${formatWindowKey(win.window_key)} 利用率`}
                                    value={pct}
                                  />
                                </div>
                              )
                            })}
                          </div>
                        )}
                      </TableCell>
                      <TableCell className="text-xs whitespace-nowrap text-muted-foreground tabular-nums">
                        {formatUTC8DateTime(account.expires_at)}
                      </TableCell>
                    </TableRow>
                    {expanded && hasWindows && (
                      <TableRow className="bg-muted/20 hover:bg-muted/20">
                        <TableCell colSpan={9} className="p-3">
                          <WindowDetailSubtable windows={windows} />
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}

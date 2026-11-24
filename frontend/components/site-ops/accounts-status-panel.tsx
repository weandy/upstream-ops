import { useMemo, useState } from "react"
import { AlertTriangle, RefreshCw, Search } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
import type {
  SiteOpsAccountStatusItem,
  SiteOpsAccountsStatusResponse,
  SiteOpsFieldQualityState,
} from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote, MetricValue } from "./quality-badge"

export interface AccountsStatusPanelProps {
  data?: SiteOpsAccountsStatusResponse
  loading: boolean
  error?: string
  onRefresh?: () => void
}

type StatusFilter = "all" | "abnormal" | "error" | "rate_limited" | "overloaded" | "normal"

const availabilityNote =
  "Ops 判定可用基于最近一次可信采样，不等同于调度器 IsSchedulable()；限流 (429)、过载 (529) 与报错状态单独标注。"

const trustBannerMessages: Partial<Record<SiteOpsFieldQualityState, string>> = {
  unverified_zero: "当前账号可用性为未证实零值，请勿将空列表或零计数视为号池健康。",
  stale: "当前账号状态采样已过期，展示结果仅供参考，请勿视为实时健康状态。",
  unauthorized: "当前凭据无权访问账号可用性数据域，请检查 Admin API Key 权限。",
  disabled: "目标站点未开启对应 Ops 数据域，无法核实账号健康状态。",
  unavailable: "暂无可信的账号状态采样，请先刷新或核查站点可达性。",
}

function formatUTC8DateTime(iso?: string | null): string {
  if (!iso) return "-"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "-"
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

function isAbnormalAccount(item: SiteOpsAccountStatusItem): boolean {
  return item.has_error || item.rate_limited || item.overloaded || !item.ops_available
}

function matchesStatusFilter(item: SiteOpsAccountStatusItem, filter: StatusFilter): boolean {
  switch (filter) {
    case "all":
      return true
    case "abnormal":
      return isAbnormalAccount(item)
    case "error":
      return item.has_error
    case "rate_limited":
      return item.rate_limited
    case "overloaded":
      return item.overloaded
    case "normal":
      return item.ops_available && !item.has_error && !item.rate_limited && !item.overloaded
  }
}

function renderOpsStatusBadges(item: SiteOpsAccountStatusItem, trustState: SiteOpsFieldQualityState) {
  if (trustState !== "trusted") {
    return (
      <div className="flex flex-wrap items-center gap-1">
        <FieldQualityBadge quality={trustState} />
        {item.has_error && <Badge variant="destructive">报错</Badge>}
        {item.rate_limited && (
          <Badge variant="outline" className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300">
            限流 (429)
          </Badge>
        )}
        {item.overloaded && (
          <Badge variant="outline" className="border-orange-500/40 bg-orange-500/10 text-orange-700 dark:text-orange-300">
            过载 (529)
          </Badge>
        )}
      </div>
    )
  }

  const badges: Array<{ key: string; node: React.ReactNode }> = []
  if (item.has_error) {
    badges.push({
      key: "error",
      node: <Badge variant="destructive">报错</Badge>,
    })
  }
  if (item.rate_limited) {
    badges.push({
      key: "rate_limited",
      node: (
        <Badge
          variant="outline"
          className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"
        >
          限流 (429)
        </Badge>
      ),
    })
  }
  if (item.overloaded) {
    badges.push({
      key: "overloaded",
      node: (
        <Badge
          variant="outline"
          className="border-orange-500/40 bg-orange-500/10 text-orange-700 dark:text-orange-300"
        >
          过载 (529)
        </Badge>
      ),
    })
  }
  if (badges.length === 0) {
    if (item.ops_available) {
      badges.push({
        key: "available",
        node: (
          <Badge
            variant="outline"
            className="border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
          >
            <span aria-hidden="true" className="size-1.5 rounded-full bg-emerald-500" />
            Ops 可用
          </Badge>
        ),
      })
    } else {
      badges.push({
        key: "unavailable",
        node: (
          <Badge variant="outline" className="text-muted-foreground">
            Ops 不可用
          </Badge>
        ),
      })
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-1">
      {badges.map((entry) => (
        <span key={entry.key}>{entry.node}</span>
      ))}
    </div>
  )
}

function renderConcurrency(item: SiteOpsAccountStatusItem, isTrusted: boolean) {
  if (!isTrusted) return <span className="text-muted-foreground">-</span>
  const hasUsed = item.conc_used != null && Number.isFinite(item.conc_used)
  const hasMax = item.conc_max != null && Number.isFinite(item.conc_max)
  if (!hasUsed && !hasMax) return <span className="text-muted-foreground">-</span>
  const usedLabel = hasUsed ? String(item.conc_used) : "-"
  const maxLabel = hasMax ? String(item.conc_max) : "-"
  const waiting = item.conc_waiting != null && Number.isFinite(item.conc_waiting) && item.conc_waiting > 0
    ? item.conc_waiting
    : null
  return (
    <span className="tabular-nums">
      {usedLabel} / {maxLabel}
      {waiting !== null ? (
        <span className="ml-1 text-xs text-muted-foreground">(等 {waiting})</span>
      ) : null}
    </span>
  )
}

export function AccountsStatusPanel({
  data,
  loading,
  error,
  onRefresh,
}: AccountsStatusPanelProps) {
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all")
  const [platformFilter, setPlatformFilter] = useState<string>("all")
  const [keyword, setKeyword] = useState("")

  const items = useMemo(() => data?.items ?? [], [data?.items])
  const trustState: SiteOpsFieldQualityState = data?.trust_state || "unavailable"
  const isTrusted = trustState === "trusted"

  const platforms = useMemo(() => {
    const set = new Set<string>()
    for (const item of items) {
      if (item.platform) set.add(item.platform)
    }
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [items])

  const effectivePlatformFilter =
    platformFilter === "all" || platforms.includes(platformFilter) ? platformFilter : "all"

  const filteredItems = useMemo(() => {
    const query = keyword.trim().toLowerCase()
    return items.filter((item) => {
      if (!matchesStatusFilter(item, statusFilter)) return false
      if (effectivePlatformFilter !== "all" && item.platform !== effectivePlatformFilter) {
        return false
      }
      if (query) {
        const nameMatch = item.name.toLowerCase().includes(query)
        const idMatch = String(item.account_id).includes(query)
        if (!nameMatch && !idMatch) return false
      }
      return true
    })
  }, [items, statusFilter, effectivePlatformFilter, keyword])

  if (error && !data) {
    return (
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-destructive/30 bg-destructive/5 p-4">
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
        {onRefresh && (
          <Button size="sm" variant="outline" onClick={onRefresh} disabled={loading}>
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
        正在加载账号状态…
      </p>
    )
  }

  if (!data) {
    return (
      <p role="status" className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground">
        暂无账号状态数据。
      </p>
    )
  }

  const bannerText = !isTrusted
    ? trustBannerMessages[trustState] ?? "当前账号状态未处于已证实状态，请勿视为号池健康。"
    : null

  return (
    <section className="space-y-3">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
          <h2 className="text-sm font-semibold">账号状态明细</h2>
          <FieldQualityBadge quality={trustState} />
          <span className="text-xs text-muted-foreground">
            最近观测（UTC+8）{formatUTC8DateTime(data.observed_at)}
          </span>
        </div>
        {onRefresh && (
          <Button size="sm" variant="outline" onClick={onRefresh} disabled={loading}>
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

      {bannerText && (
        <div
          role="status"
          className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200"
        >
          <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          <span>{bannerText}</span>
        </div>
      )}

      {/* 顶部紧凑统计摘要条 */}
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">总账号</span>
          <span className="text-sm font-semibold">
            <MetricValue value={data.total_count} quality={trustState} />
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="inline-flex items-center gap-1 text-muted-foreground">
            Ops 判定可用
            <MetricNote label="Ops 判定可用口径">{availabilityNote}</MetricNote>
          </span>
          <span
            className={
              isTrusted
                ? "text-sm font-semibold text-emerald-700 dark:text-emerald-300"
                : "text-sm font-semibold"
            }
          >
            <MetricValue value={data.available_count} quality={trustState} />
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">限流中 (429)</span>
          <span
            className={
              isTrusted && data.rate_limit_count > 0
                ? "text-sm font-semibold text-amber-700 dark:text-amber-300"
                : "text-sm font-semibold"
            }
          >
            <MetricValue value={data.rate_limit_count} quality={trustState} />
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">过载中 (529)</span>
          <span
            className={
              isTrusted && data.overload_count > 0
                ? "text-sm font-semibold text-orange-700 dark:text-orange-300"
                : "text-sm font-semibold"
            }
          >
            <MetricValue value={data.overload_count} quality={trustState} />
          </span>
        </div>
        <div className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-xs">
          <span className="text-muted-foreground">报错中</span>
          <span
            className={
              isTrusted && data.error_count > 0
                ? "text-sm font-semibold text-destructive"
                : "text-sm font-semibold"
            }
          >
            <MetricValue value={data.error_count} quality={trustState} />
          </span>
        </div>
      </div>

      {/* 本地筛选条 */}
      <div className="flex flex-wrap items-center gap-2">
        <Select
          value={statusFilter}
          onValueChange={(value) => setStatusFilter(value as StatusFilter)}
        >
          <SelectTrigger className="h-8 w-36 text-xs" aria-label="按状态筛选">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部状态</SelectItem>
            <SelectItem value="abnormal">仅异常</SelectItem>
            <SelectItem value="error">报错</SelectItem>
            <SelectItem value="rate_limited">限流 (429)</SelectItem>
            <SelectItem value="overloaded">过载 (529)</SelectItem>
            <SelectItem value="normal">正常</SelectItem>
          </SelectContent>
        </Select>

        <Select value={effectivePlatformFilter} onValueChange={setPlatformFilter}>
          <SelectTrigger className="h-8 w-40 text-xs" aria-label="按平台筛选">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部平台</SelectItem>
            {platforms.map((platform) => (
              <SelectItem key={platform} value={platform}>
                {platform}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <div className="relative min-w-48 flex-1 sm:max-w-xs">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="搜索账号名称或 ID…"
            aria-label="按名称或 ID 搜索账号"
            className="h-8 pl-8 text-xs"
          />
        </div>

        <span className="ml-auto text-xs text-muted-foreground tabular-nums">
          显示 {filteredItems.length} / {items.length} 个账号
        </span>
      </div>

      {/* 账号明细表格 */}
      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>账号 / ID</TableHead>
              <TableHead>平台 / 类型</TableHead>
              <TableHead>Ops 状态</TableHead>
              <TableHead>调度与到期（UTC+8）</TableHead>
              <TableHead>限流 / 过载 / 暂不可调度截止（UTC+8）</TableHead>
              <TableHead>并发占用</TableHead>
              <TableHead>脱敏错误摘要</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredItems.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-muted-foreground">
                  {items.length === 0
                    ? "当前站点暂无账号状态记录。"
                    : "没有符合当前筛选条件的账号。"}
                </TableCell>
              </TableRow>
            ) : (
              filteredItems.map((item) => {
                const limitDeadlines: Array<{ label: string; time: string }> = []
                if (item.rate_limit_reset_at) {
                  limitDeadlines.push({
                    label: "限流至",
                    time: formatUTC8DateTime(item.rate_limit_reset_at),
                  })
                }
                if (item.overload_until) {
                  limitDeadlines.push({
                    label: "过载至",
                    time: formatUTC8DateTime(item.overload_until),
                  })
                }
                if (item.temp_unschedulable_until) {
                  limitDeadlines.push({
                    label: "暂不可调度至",
                    time: formatUTC8DateTime(item.temp_unschedulable_until),
                  })
                }

                return (
                  <TableRow key={item.account_id}>
                    <TableCell>
                      <div className="flex flex-col">
                        <span className="max-w-48 truncate font-medium" title={item.name}>
                          {item.name || `账号 #${item.account_id}`}
                        </span>
                        <span className="font-mono text-xs text-muted-foreground">
                          #{item.account_id}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap items-center gap-1">
                        <span>{item.platform || "-"}</span>
                        {item.type ? (
                          <Badge variant="secondary" className="text-[11px]">
                            {item.type}
                          </Badge>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell>{renderOpsStatusBadges(item, trustState)}</TableCell>
                    <TableCell>
                      <div className="flex flex-col gap-0.5 text-xs">
                        <span>
                          {item.schedulable === true ? (
                            <Badge variant="outline" className="text-[11px]">
                              可调度
                            </Badge>
                          ) : item.schedulable === false ? (
                            <Badge
                              variant="outline"
                              className="border-amber-500/40 text-[11px] text-amber-700 dark:text-amber-300"
                            >
                              调度关闭
                            </Badge>
                          ) : (
                            <span className="text-muted-foreground">-</span>
                          )}
                        </span>
                        {item.expires_at ? (
                          <span className="text-muted-foreground tabular-nums">
                            到期 {formatUTC8DateTime(item.expires_at)}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      {limitDeadlines.length === 0 ? (
                        <span className="text-muted-foreground">-</span>
                      ) : (
                        <div className="flex flex-col gap-0.5 text-xs tabular-nums">
                          {limitDeadlines.map((entry) => (
                            <span key={entry.label} className="whitespace-nowrap">
                              <span className="text-muted-foreground">{entry.label} </span>
                              {entry.time}
                            </span>
                          ))}
                        </div>
                      )}
                    </TableCell>
                    <TableCell>{renderConcurrency(item, isTrusted)}</TableCell>
                    <TableCell>
                      {item.error_summary ? (
                        <p
                          className="max-w-64 truncate text-xs text-muted-foreground"
                          title={item.error_summary}
                        >
                          {item.error_summary}
                        </p>
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}

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
import { FieldQualityBadge } from "./quality-badge"

export interface AccountsStatusPanelProps {
  data?: SiteOpsAccountsStatusResponse
  loading: boolean
  error?: string
  onRefresh?: () => void
}

type StatusFilter = "all" | "normal" | "error" | "rate_limited" | "overloaded" | "disabled"

const trustBannerMessages: Partial<Record<SiteOpsFieldQualityState, string>> = {
  unverified_zero: "当前账号状态为未证实零值，请勿将空列表视为号池健康。",
  stale: "当前账号状态采样已过期，展示结果仅供参考。",
  unauthorized: "当前凭据无权读取账号状态，请检查站点 Admin API Key 权限。",
  disabled: "目标站点未开启账号状态监测。",
  unavailable: "暂无可信的账号状态采样，请刷新或核查站点可达性。",
}

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
    hourCycle: "h23",
  }).formatToParts(d)
  const map = new Map(parts.map((part) => [part.type, part.value]))
  return `${map.get("year")}-${map.get("month")}-${map.get("day")} ${map.get("hour")}:${map.get("minute")}`
}

function classifyAccount(item: SiteOpsAccountStatusItem): Exclude<StatusFilter, "all"> {
  if (item.has_error) return "error"
  if (item.rate_limited) return "rate_limited"
  if (item.overloaded) return "overloaded"
  if (item.schedulable === false || !item.ops_available) return "disabled"
  return "normal"
}

function renderStatusBadge(item: SiteOpsAccountStatusItem, trustState: SiteOpsFieldQualityState) {
  const kind = classifyAccount(item)
  if (trustState !== "trusted" && kind === "normal") {
    return <FieldQualityBadge quality={trustState} />
  }
  switch (kind) {
    case "error":
      return <Badge variant="destructive">报错</Badge>
    case "rate_limited":
      return (
        <Badge
          variant="outline"
          className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"
        >
          限流 429
        </Badge>
      )
    case "overloaded":
      return (
        <Badge
          variant="outline"
          className="border-orange-500/40 bg-orange-500/10 text-orange-700 dark:text-orange-300"
        >
          过载 529
        </Badge>
      )
    case "disabled":
      return (
        <Badge variant="outline" className="text-muted-foreground">
          {item.schedulable === false ? "调度关闭" : "不可用"}
        </Badge>
      )
    default:
      return (
        <Badge
          variant="outline"
          className="border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
        >
          正常
        </Badge>
      )
  }
}

export function AccountsStatusPanel({
  data,
  loading,
  error,
  onRefresh,
}: AccountsStatusPanelProps) {
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all")
  const [platformFilter, setPlatformFilter] = useState("all")
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

  const effectivePlatform =
    platformFilter === "all" || platforms.includes(platformFilter) ? platformFilter : "all"

  const counts = useMemo(() => {
    const map = { normal: 0, error: 0, rate_limited: 0, overloaded: 0, disabled: 0 }
    for (const item of items) {
      map[classifyAccount(item)]++
    }
    return map
  }, [items])

  const filteredItems = useMemo(() => {
    const query = keyword.trim().toLowerCase()
    const priority: Record<Exclude<StatusFilter, "all">, number> = {
      error: 0,
      overloaded: 1,
      rate_limited: 2,
      disabled: 3,
      normal: 4,
    }
    return items
      .filter((item) => {
        if (statusFilter !== "all" && classifyAccount(item) !== statusFilter) return false
        if (effectivePlatform !== "all" && item.platform !== effectivePlatform) return false
        if (query) {
          const hit =
            item.name.toLowerCase().includes(query) ||
            String(item.account_id).includes(query) ||
            item.platform.toLowerCase().includes(query)
          if (!hit) return false
        }
        return true
      })
      .sort(
        (a, b) =>
          priority[classifyAccount(a)] - priority[classifyAccount(b)] ||
          a.account_id - b.account_id,
      )
  }, [items, statusFilter, effectivePlatform, keyword])

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
        正在加载上游账号状态…
      </p>
    )
  }

  if (!data) {
    return (
      <p role="status" className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground">
        暂无上游账号数据。
      </p>
    )
  }

  const bannerText = !isTrusted
    ? trustBannerMessages[trustState] ?? "当前账号状态未证实，请勿视为号池健康。"
    : null
  const total = Math.max(items.length, 1)

  return (
    <section className="space-y-3">
      {bannerText && (
        <div
          role="status"
          className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200"
        >
          <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          <span>{bannerText}</span>
        </div>
      )}

      {/* 上方健康分布条 + 筛选栏 */}
      <div className="space-y-3 rounded-lg border bg-card p-3.5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-semibold">上游账号健康分布</h2>
            <FieldQualityBadge quality={trustState} />
            <span className="text-xs text-muted-foreground tabular-nums">
              观测 {formatUTC8DateTime(data.observed_at)}
            </span>
          </div>
          {onRefresh && (
            <Button size="sm" variant="outline" className="h-7 text-xs" onClick={onRefresh} disabled={loading}>
              <RefreshCw className={`size-3 ${loading ? "animate-spin" : ""}`} />
              刷新
            </Button>
          )}
        </div>

        {/* 可点击堆叠状态条 */}
        {items.length > 0 && (
          <div className="flex h-3 w-full gap-0.5 overflow-hidden rounded-full bg-muted">
            {counts.normal > 0 && (
              <button
                type="button"
                title={`正常 ${counts.normal}`}
                onClick={() => setStatusFilter(statusFilter === "normal" ? "all" : "normal")}
                className="h-full bg-emerald-500 transition-opacity hover:opacity-80"
                style={{ width: `${(counts.normal / total) * 100}%` }}
              />
            )}
            {counts.rate_limited > 0 && (
              <button
                type="button"
                title={`限流 429 ${counts.rate_limited}`}
                onClick={() =>
                  setStatusFilter(statusFilter === "rate_limited" ? "all" : "rate_limited")
                }
                className="h-full bg-amber-500 transition-opacity hover:opacity-80"
                style={{ width: `${(counts.rate_limited / total) * 100}%` }}
              />
            )}
            {counts.overloaded > 0 && (
              <button
                type="button"
                title={`过载 529 ${counts.overloaded}`}
                onClick={() =>
                  setStatusFilter(statusFilter === "overloaded" ? "all" : "overloaded")
                }
                className="h-full bg-orange-500 transition-opacity hover:opacity-80"
                style={{ width: `${(counts.overloaded / total) * 100}%` }}
              />
            )}
            {counts.error > 0 && (
              <button
                type="button"
                title={`报错 ${counts.error}`}
                onClick={() => setStatusFilter(statusFilter === "error" ? "all" : "error")}
                className="h-full bg-destructive transition-opacity hover:opacity-80"
                style={{ width: `${(counts.error / total) * 100}%` }}
              />
            )}
            {counts.disabled > 0 && (
              <button
                type="button"
                title={`调度关闭 ${counts.disabled}`}
                onClick={() => setStatusFilter(statusFilter === "disabled" ? "all" : "disabled")}
                className="h-full bg-muted-foreground/50 transition-opacity hover:opacity-80"
                style={{ width: `${(counts.disabled / total) * 100}%` }}
              />
            )}
          </div>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <div className="flex flex-wrap gap-1">
            {(
              [
                ["all", `全部 ${items.length}`],
                ["normal", `正常 ${counts.normal}`],
                ["error", `报错 ${counts.error}`],
                ["rate_limited", `限流 ${counts.rate_limited}`],
                ["overloaded", `过载 ${counts.overloaded}`],
                ["disabled", `调度关闭 ${counts.disabled}`],
              ] as const
            )
              .filter(([k, ,]) => k === "all" || k === "normal" || counts[k] > 0)
              .map(([k, label]) => (
                <Button
                  key={k}
                  size="sm"
                  variant={statusFilter === k ? "secondary" : "outline"}
                  className="h-7 rounded-full px-2.5 text-xs tabular-nums"
                  onClick={() => setStatusFilter(k)}
                >
                  {label}
                </Button>
              ))}
          </div>

          <Select value={effectivePlatform} onValueChange={setPlatformFilter}>
            <SelectTrigger className="h-8 w-36 text-xs" aria-label="按平台筛选">
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

          <div className="relative min-w-44 flex-1 sm:max-w-xs">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              placeholder="搜索账号名、ID 或平台…"
              aria-label="按名称、ID 或平台搜索账号"
              className="h-8 pl-8 text-xs"
            />
          </div>

          <span className="ml-auto text-xs text-muted-foreground tabular-nums">
            显示 {filteredItems.length} / {items.length}
          </span>
        </div>
      </div>

      {/* 下方常驻全量账号明细 */}
      <div className="rounded-lg border bg-card">
        <div className="flex items-center justify-between border-b px-3.5 py-2.5">
          <h3 className="text-sm font-semibold">上游账号明细</h3>
          <span className="text-xs text-muted-foreground">报错与停用优先排列</span>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>账号</TableHead>
              <TableHead>当前状态</TableHead>
              <TableHead>调度与到期</TableHead>
              <TableHead>限流与解除时间</TableHead>
              <TableHead className="text-right">并发</TableHead>
              <TableHead>异常原因</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredItems.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="py-6 text-center text-xs text-muted-foreground">
                  {items.length === 0 ? "当前站点暂无上游账号" : "没有符合筛选条件的账号"}
                </TableCell>
              </TableRow>
            ) : (
              filteredItems.map((item) => {
                const deadlines: string[] = []
                if (item.rate_limit_reset_at) {
                  deadlines.push(`限流至 ${formatUTC8DateTime(item.rate_limit_reset_at)}`)
                }
                if (item.overload_until) {
                  deadlines.push(`过载至 ${formatUTC8DateTime(item.overload_until)}`)
                }
                if (item.temp_unschedulable_until) {
                  deadlines.push(`暂停至 ${formatUTC8DateTime(item.temp_unschedulable_until)}`)
                }

                return (
                  <TableRow key={item.account_id}>
                    <TableCell>
                      <div className="flex flex-col gap-0.5">
                        <span className="max-w-48 truncate text-xs font-medium" title={item.name}>
                          {item.name || `账号 #${item.account_id}`}
                        </span>
                        <span className="font-mono text-[11px] text-muted-foreground">
                          #{item.account_id} · {item.platform || "—"}
                          {item.type ? ` · ${item.type}` : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell>{renderStatusBadge(item, trustState)}</TableCell>
                    <TableCell className="text-xs">
                      <div className="flex flex-col gap-0.5">
                        <span>
                          {item.schedulable === true
                            ? "可调度"
                            : item.schedulable === false
                              ? "调度关闭"
                              : "—"}
                        </span>
                        {item.expires_at && (
                          <span className="text-[11px] text-muted-foreground tabular-nums">
                            到期 {formatUTC8DateTime(item.expires_at)}
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground tabular-nums">
                      {deadlines.length ? deadlines.join(" · ") : "—"}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {isTrusted && (item.conc_used != null || item.conc_max != null) ? (
                        <>
                          {item.conc_used ?? "—"} / {item.conc_max ?? "—"}
                          {(item.conc_waiting ?? 0) > 0 ? (
                            <span className="ml-1 text-amber-700 dark:text-amber-300">
                              · 排{item.conc_waiting}
                            </span>
                          ) : null}
                        </>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell>
                      {item.error_summary ? (
                        <p
                          className={`max-w-64 truncate text-xs ${
                            item.has_error ? "text-destructive" : "text-muted-foreground"
                          }`}
                          title={item.error_summary}
                        >
                          {item.error_summary}
                        </p>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
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

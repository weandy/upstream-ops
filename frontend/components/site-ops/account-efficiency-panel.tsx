import { useMemo, useState, type FormEvent } from "react"
import { ArrowDown, ArrowUp, ArrowUpDown, Pencil, RefreshCw, Search } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { decimal, formatCompactNumber, money } from "@/lib/format"
import type {
  SiteOpsAccountEfficiencyItem,
  SiteOpsAccountsEfficiencyResponse,
  SiteOpsFieldQualityState,
  SiteOpsUpdateAccountCostInput,
} from "@/lib/site-ops-types"
import { FieldQualityBadge } from "./quality-badge"

export interface AccountEfficiencyPanelProps {
  data?: SiteOpsAccountsEfficiencyResponse
  loading: boolean
  error?: string
  onReload?: () => void
  onSaveCost: (accountId: number, input: SiteOpsUpdateAccountCostInput) => Promise<void>
}

type CostMetric = "d30" | "today" | "perConc"

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

function hasSpread(a: number, b: number, c: number) {
  const maxV = Math.max(Math.abs(a), Math.abs(b), Math.abs(c), 1)
  const tol = Math.max(0.01, maxV * 1e-4)
  return Math.abs(a - b) > tol || Math.abs(b - c) > tol || Math.abs(a - c) > tol
}

function CostEditDialog({
  item,
  onClose,
  onSave,
}: {
  item: SiteOpsAccountEfficiencyItem
  onClose: () => void
  onSave: (accountId: number, input: SiteOpsUpdateAccountCostInput) => Promise<void>
}) {
  const [costText, setCostText] = useState(
    item.monthly_cost_usd != null && Number.isFinite(item.monthly_cost_usd)
      ? String(item.monthly_cost_usd)
      : "",
  )
  const [note, setNote] = useState(item.monthly_cost_note ?? "")
  const [saving, setSaving] = useState(false)
  const [submitError, setSubmitError] = useState("")

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (saving) return

    const trimmed = costText.trim()
    const parsed = trimmed === "" ? 0 : Number(trimmed)
    if (!Number.isFinite(parsed) || parsed < 0) {
      setSubmitError("参考月租需为大于等于 0 的有效数字，填 0 表示清除。")
      return
    }

    setSaving(true)
    setSubmitError("")
    try {
      await onSave(item.account_id, {
        monthly_cost_usd: parsed,
        note: note.trim(),
      })
      onClose()
    } catch (err) {
      const msg = err instanceof Error ? err.message.trim() : ""
      setSubmitError(msg ? `保存失败：${msg.slice(0, 200)}` : "保存参考月租失败，请重试。")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !saving) onClose() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>设置本地参考月租</DialogTitle>
          <DialogDescription>
            账号 {item.name || `#${item.account_id}`} · {item.platform}。仅保存在本地，用于对比 30 日产出与月租成本。
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <fieldset disabled={saving} className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-monthly-cost">参考月租 USD</Label>
              <Input
                id="site-ops-monthly-cost"
                type="number"
                min={0}
                step="0.01"
                value={costText}
                onChange={(e) => setCostText(e.target.value)}
                placeholder="例如 200，填 0 表示清除"
                required
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-monthly-note">备注</Label>
              <Input
                id="site-ops-monthly-note"
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="如 Team 月付 / Max 订阅"
                maxLength={256}
              />
            </div>
          </fieldset>
          {submitError && (
            <p role="alert" className="text-xs text-destructive">
              {submitError}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saving} onClick={onClose}>
              取消
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "保存中…" : "保存月租"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

const metricLabels: Record<CostMetric, string> = {
  d30: "30日消耗",
  today: "今日消耗",
  perConc: "单并发日产",
}

export function AccountEfficiencyPanel({
  data,
  loading,
  error,
  onReload,
  onSaveCost,
}: AccountEfficiencyPanelProps) {
  const [platformFilter, setPlatformFilter] = useState("all")
  const [keyword, setKeyword] = useState("")
  const [metric, setMetric] = useState<CostMetric>("d30")
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc")
  const [hoverId, setHoverId] = useState<number | null>(null)
  const [editingItem, setEditingItem] = useState<SiteOpsAccountEfficiencyItem | null>(null)

  const items = useMemo(() => data?.items ?? [], [data?.items])

  const platforms = useMemo(() => {
    const set = new Set<string>()
    for (const item of items) {
      if (item.platform) set.add(item.platform)
    }
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [items])

  const effectivePlatform =
    platformFilter === "all" || platforms.includes(platformFilter) ? platformFilter : "all"

  const filteredItems = useMemo(() => {
    const query = keyword.trim().toLowerCase()
    const mult = sortDir === "desc" ? -1 : 1
    return items
      .filter((item) => {
        if (effectivePlatform !== "all" && item.platform !== effectivePlatform) return false
        if (query) {
          const hit =
            item.name.toLowerCase().includes(query) || String(item.account_id).includes(query)
          if (!hit) return false
        }
        return true
      })
      .sort((a, b) => {
        const av =
          metric === "today"
            ? a.today_user_cost
            : metric === "perConc"
              ? (a.output_per_concurrency_usd ?? -1)
              : a.user_cost_30d
        const bv =
          metric === "today"
            ? b.today_user_cost
            : metric === "perConc"
              ? (b.output_per_concurrency_usd ?? -1)
              : b.user_cost_30d
        return (av - bv) * mult || a.account_id - b.account_id
      })
  }, [items, effectivePlatform, keyword, metric, sortDir])

  function handleMetric(next: CostMetric) {
    if (metric === next) setSortDir((d) => (d === "desc" ? "asc" : "desc"))
    else {
      setMetric(next)
      setSortDir("desc")
    }
  }

  function SortIcon({ k }: { k: CostMetric }) {
    if (metric !== k) return <ArrowUpDown className="ml-1 inline size-3 opacity-45" />
    return sortDir === "desc" ? (
      <ArrowDown className="ml-1 inline size-3 text-primary" />
    ) : (
      <ArrowUp className="ml-1 inline size-3 text-primary" />
    )
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
        正在加载单号成本数据…
      </p>
    )
  }

  if (!data) {
    return (
      <p role="status" className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground">
        暂无单号成本数据。
      </p>
    )
  }

  const trustState: SiteOpsFieldQualityState = data.trust_state || "unavailable"
  const maxVal = Math.max(
    ...filteredItems.map((i) =>
      metric === "today"
        ? i.today_user_cost
        : metric === "perConc"
          ? (i.output_per_concurrency_usd ?? 0)
          : i.user_cost_30d,
    ),
    1,
  )

  return (
    <section className="space-y-3">
      {/* 共享筛选栏：同时驱动上方排行条和下方全表 */}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card px-3 py-2">
        <div className="flex flex-wrap gap-1">
          <Button
            size="sm"
            variant={effectivePlatform === "all" ? "secondary" : "outline"}
            className="h-7 rounded-full px-2.5 text-xs"
            onClick={() => setPlatformFilter("all")}
          >
            全部平台
          </Button>
          {platforms.map((p) => (
            <Button
              key={p}
              size="sm"
              variant={effectivePlatform === p ? "secondary" : "outline"}
              className="h-7 rounded-full px-2.5 text-xs"
              onClick={() => setPlatformFilter(p)}
            >
              {p}
            </Button>
          ))}
        </div>

        <div className="relative min-w-40 flex-1 sm:max-w-xs">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="搜索账号名或 ID…"
            aria-label="按名称或 ID 搜索账号成本"
            className="h-8 pl-8 text-xs"
          />
        </div>

        <div className="ml-auto flex flex-wrap items-center gap-2">
          <div className="inline-flex gap-0.5 rounded-md bg-muted p-0.5">
            {(["d30", "today", "perConc"] as const).map((k) => (
              <button
                key={k}
                type="button"
                onClick={() => handleMetric(k)}
                className={`rounded px-2 py-0.5 text-[11px] font-medium transition-colors ${
                  metric === k
                    ? "bg-card text-foreground shadow-xs"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {metricLabels[k]}
              </button>
            ))}
          </div>
          {onReload && (
            <Button size="sm" variant="outline" className="h-7 text-xs" onClick={onReload} disabled={loading}>
              <RefreshCw className={`size-3 ${loading ? "animate-spin" : ""}`} />
              刷新
            </Button>
          )}
        </div>
      </div>

      {/* 上方图表：单号产出排行（随筛选和口径实时重画） */}
      <div className="space-y-2 rounded-lg border bg-card p-3.5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">单号产出排行</h2>
            <FieldQualityBadge quality={trustState} />
          </div>
          <span className="text-xs text-muted-foreground">
            按{metricLabels[metric]}
            {sortDir === "desc" ? "降序" : "升序"} · 观测 {formatUTC8DateTime(data.observed_at)}
          </span>
        </div>

        {filteredItems.length === 0 ? (
          <p className="rounded-md border border-dashed py-6 text-center text-xs text-muted-foreground">
            没有符合当前筛选条件的账号
          </p>
        ) : (
          <div className="space-y-1.5">
            {filteredItems.map((item) => {
              const val =
                metric === "today"
                  ? item.today_user_cost
                  : metric === "perConc"
                    ? (item.output_per_concurrency_usd ?? 0)
                    : item.user_cost_30d
              const w = val > 0 ? Math.max(3, Math.round((val / maxVal) * 100)) : 0
              const hi = hoverId === item.account_id
              return (
                <div
                  key={item.account_id}
                  onMouseEnter={() => setHoverId(item.account_id)}
                  onMouseLeave={() => setHoverId(null)}
                  className={`space-y-1 rounded-md border px-2 py-1 transition-colors ${
                    hi
                      ? "border-primary/40 bg-primary/5"
                      : "border-transparent hover:bg-muted/40"
                  }`}
                >
                  <div className="flex flex-wrap items-baseline justify-between gap-2 text-xs">
                    <span className="font-medium">
                      {item.name || `账号 #${item.account_id}`}{" "}
                      <span className="text-[11px] font-normal text-muted-foreground">
                        {item.platform} · {item.current_concurrency} 并发
                      </span>
                    </span>
                    <span className="text-[11px] font-semibold tabular-nums">
                      {metric === "perConc"
                        ? item.output_per_concurrency_usd != null
                          ? `${money(item.output_per_concurrency_usd)} / 并发`
                          : "—"
                        : money(val)}
                    </span>
                  </div>
                  <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-primary"
                      style={{ width: `${w}%` }}
                    />
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* 下方常驻全量账号成本与月租明细 */}
      <div className="rounded-lg border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3.5 py-2.5">
          <h3 className="text-sm font-semibold">账号成本与月租明细</h3>
          <span className="text-xs text-muted-foreground tabular-nums">
            显示 {filteredItems.length} / {items.length} 个账号
          </span>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>账号</TableHead>
              <TableHead className="text-right">并发</TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleMetric("today")}
              >
                今日消耗
                <SortIcon k="today" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleMetric("d30")}
              >
                30日消耗
                <SortIcon k="d30" />
              </TableHead>
              <TableHead
                className="cursor-pointer text-right select-none"
                onClick={() => handleMetric("perConc")}
              >
                单并发日产
                <SortIcon k="perConc" />
              </TableHead>
              <TableHead className="text-right">本地参考月租</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredItems.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-xs text-muted-foreground">
                  {items.length === 0 ? "暂无账号成本记录" : "没有符合筛选条件的账号"}
                </TableCell>
              </TableRow>
            ) : (
              filteredItems.map((item) => {
                const hasRent =
                  item.monthly_cost_usd != null &&
                  Number.isFinite(item.monthly_cost_usd) &&
                  item.monthly_cost_usd > 0
                const show30Spread = hasSpread(
                  item.standard_cost_30d,
                  item.account_cost_30d,
                  item.user_cost_30d,
                )
                return (
                  <TableRow
                    key={item.account_id}
                    onMouseEnter={() => setHoverId(item.account_id)}
                    onMouseLeave={() => setHoverId(null)}
                    className={hoverId === item.account_id ? "bg-primary/5" : undefined}
                  >
                    <TableCell>
                      <div className="flex flex-col gap-0.5">
                        <span className="max-w-44 truncate text-xs font-medium" title={item.name}>
                          {item.name || `账号 #${item.account_id}`}
                        </span>
                        <span className="font-mono text-[11px] text-muted-foreground">
                          #{item.account_id} · {item.platform || "—"}
                          {item.account_type ? ` · ${item.account_type}` : ""}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {item.current_concurrency}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <span>{money(item.today_user_cost)}</span>
                        <span className="text-[11px] text-muted-foreground">
                          {formatCompactNumber(item.today_requests)} 次
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <strong className="font-semibold">{money(item.user_cost_30d)}</strong>
                        {show30Spread ? (
                          <span className="text-[11px] text-muted-foreground">
                            标 {money(item.standard_cost_30d)} · 本 {money(item.account_cost_30d)}
                          </span>
                        ) : (
                          <span className="text-[11px] text-muted-foreground">
                            {formatCompactNumber(item.total_requests)} 次
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <span>
                          {item.output_per_concurrency_usd != null &&
                          Number.isFinite(item.output_per_concurrency_usd)
                            ? money(item.output_per_concurrency_usd)
                            : "—"}
                        </span>
                        {item.req_per_concurrency != null &&
                        Number.isFinite(item.req_per_concurrency) ? (
                          <span className="text-[11px] text-muted-foreground">
                            {decimal(item.req_per_concurrency, 1)} 次/槽
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <div className="flex flex-col items-end gap-0.5">
                        <span className="font-medium">
                          {hasRent ? money(item.monthly_cost_usd) : "未设置"}
                        </span>
                        {hasRent && item.user_cost_per_monthly_ref != null ? (
                          <span className="text-[11px] text-muted-foreground">
                            产/租 {decimal(item.user_cost_per_monthly_ref, 1)} 倍
                          </span>
                        ) : item.monthly_cost_note ? (
                          <span className="max-w-28 truncate text-[11px] text-muted-foreground">
                            {item.monthly_cost_note}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        className="h-7 px-2 text-xs"
                        onClick={() => setEditingItem(item)}
                      >
                        <Pencil className="size-3" />
                        月租
                      </Button>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      {editingItem && (
        <CostEditDialog
          item={editingItem}
          onClose={() => setEditingItem(null)}
          onSave={onSaveCost}
        />
      )}
    </section>
  )
}

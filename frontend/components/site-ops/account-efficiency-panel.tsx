import { useMemo, useState, type FormEvent } from "react"
import { Info, Pencil, RefreshCw, Search } from "lucide-react"
import { Badge } from "@/components/ui/badge"
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
import { decimal, formatCompactNumber, money } from "@/lib/format"
import type {
  SiteOpsAccountEfficiencyItem,
  SiteOpsAccountsEfficiencyResponse,
  SiteOpsFieldQualityState,
  SiteOpsUpdateAccountCostInput,
} from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote } from "./quality-badge"

export interface AccountEfficiencyPanelProps {
  data?: SiteOpsAccountsEfficiencyResponse
  loading: boolean
  error?: string
  onReload?: () => void
  onSaveCost: (accountId: number, input: SiteOpsUpdateAccountCostInput) => Promise<void>
}

const defaultBatchGateNote =
  "已规避 today-stats/batch 共享缓存鉴权风险，当前采用逐账号鉴权的单号统计接口（/accounts/:id/stats）。"
const defaultThreeCostNote =
  "严格区分 Sub2API 三口径成本：官方标价（standard_cost）、账号折算成本（cost）、用户账面扣费（user_cost）。参考月租衍生指标为「30 日官方标价 ÷ 参考月租」与「30 日账面扣费 ÷ 参考月租」，不等同于实际现金收益。"
const leverageNote =
  "基于本地录入的参考月租（USD）计算：标准价杠杆 = 30 日官方标价 ÷ 参考月租；账面扣费 / 参考月租 = 30 日用户账面扣费 ÷ 参考月租。参考月租为 0 或未设置时显示 —。"
const capacityNormNote =
  "当前容量归一化产出：今日用户账面扣费 ÷ 当前并发槽位、今日请求数 ÷ 当前并发槽位；并发槽位 ≤ 0 时显示 —。"
const windowLeverageNote =
  "仅在同一窗口、同一费用口径且样本充足时计算 standard_cost ÷ (utilization / 100)，否则显示 —。"

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

function formatMultiplier(value?: number | null): string {
  if (value == null || !Number.isFinite(value)) return "—"
  return `${decimal(value, 2)}x`
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
      setSubmitError("参考月租需为大于等于 0 的有效数字（填 0 表示清除参考月租杠杆）。")
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
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !saving) onClose()
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>设置本地参考月租</DialogTitle>
          <DialogDescription>
            账号：{item.name || `账号 #${item.account_id}`}（#{item.account_id} · {item.platform}
            ）。仅保存在本地运维库，用于计算「标准价杠杆」与「账面扣费 / 参考月租」。
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <fieldset disabled={saving} className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-monthly-cost">参考月租（USD）</Label>
              <Input
                id="site-ops-monthly-cost"
                type="number"
                min={0}
                step="0.01"
                value={costText}
                onChange={(e) => setCostText(e.target.value)}
                placeholder="例如 200，填 0 表示清除参考月租杠杆"
                required
              />
              <p className="text-xs text-muted-foreground">
                允许 ≥ 0；填 0 将清除参考月租与对应杠杆展示。
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="site-ops-monthly-note">备注</Label>
              <Input
                id="site-ops-monthly-note"
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="如：Team 月付 / Max 20x 订阅"
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

export function AccountEfficiencyPanel({
  data,
  loading,
  error,
  onReload,
  onSaveCost,
}: AccountEfficiencyPanelProps) {
  const [platformFilter, setPlatformFilter] = useState("all")
  const [keyword, setKeyword] = useState("")
  const [editingItem, setEditingItem] = useState<SiteOpsAccountEfficiencyItem | null>(null)

  const items = useMemo(() => data?.items ?? [], [data?.items])

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
  }, [items, effectivePlatformFilter, keyword])

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
        正在加载单号成本效率…
      </p>
    )
  }

  if (!data) {
    return (
      <p
        role="status"
        className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground"
      >
        暂无单号成本效率数据。
      </p>
    )
  }

  const trustState: SiteOpsFieldQualityState = data.trust_state || "unavailable"
  const semanticsNote = data.metric_semantics_note || defaultThreeCostNote
  const batchGateNote = data.today_stats_batch_gate_note || defaultBatchGateNote

  return (
    <section className="space-y-3">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
          <h2 className="text-sm font-semibold">单号成本效率</h2>
          <FieldQualityBadge quality={trustState} />
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            三口径与账面扣费 / 参考月租说明
            <MetricNote label="三口径成本与参考月租口径说明">{semanticsNote}</MetricNote>
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

      {/* 顶部紧凑提示条：today-stats/batch 门禁与三口径说明 */}
      {data.today_stats_batch_gated && (
        <div
          role="status"
          className="flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-1.5 text-xs text-muted-foreground"
        >
          <Info className="size-3.5 shrink-0" />
          <span>{batchGateNote}</span>
        </div>
      )}

      {/* 筛选条 */}
      <div className="flex flex-wrap items-center gap-2">
        <Select value={effectivePlatformFilter} onValueChange={setPlatformFilter}>
          <SelectTrigger className="h-8 w-40 text-xs" aria-label="按平台筛选效率列表">
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
            aria-label="按名称或 ID 搜索单号效率"
            className="h-8 pl-8 text-xs"
          />
        </div>

        <span className="ml-auto text-xs text-muted-foreground tabular-nums">
          显示 {filteredItems.length} / {items.length} 个账号
        </span>
      </div>

      {/* 单号成本效率表格 */}
      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>账号 / 平台</TableHead>
              <TableHead>当前并发</TableHead>
              <TableHead>今日请求与三口径</TableHead>
              <TableHead>30 日三口径（标价 / 成本 / 扣费）</TableHead>
              <TableHead>
                <span className="inline-flex items-center gap-1">
                  变现倍率比
                  <MetricNote label="变现倍率比口径">
                    用户账面扣费 ÷ 账号折算成本（user_cost / cost）；账号折算成本 ≤ 0 时显示 —。
                  </MetricNote>
                </span>
              </TableHead>
              <TableHead>
                <span className="inline-flex items-center gap-1">
                  当前容量归一化产出
                  <MetricNote label="当前容量归一化产出口径">{capacityNormNote}</MetricNote>
                </span>
              </TableHead>
              <TableHead>
                <span className="inline-flex items-center gap-1">
                  同窗满额撬动价值
                  <MetricNote label="同窗满额撬动价值口径">{windowLeverageNote}</MetricNote>
                </span>
              </TableHead>
              <TableHead>
                <span className="inline-flex items-center gap-1">
                  本地参考月租与杠杆
                  <MetricNote label="参考月租杠杆口径">{leverageNote}</MetricNote>
                </span>
              </TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredItems.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9} className="py-6 text-center text-muted-foreground">
                  {items.length === 0
                    ? "当前站点暂无单号成本效率记录。"
                    : "没有符合当前筛选条件的账号。"}
                </TableCell>
              </TableRow>
            ) : (
              filteredItems.map((item) => {
                const hasMonthlyCost =
                  item.monthly_cost_usd != null &&
                  Number.isFinite(item.monthly_cost_usd) &&
                  item.monthly_cost_usd > 0

                return (
                  <TableRow key={item.account_id}>
                    <TableCell>
                      <div className="flex flex-col gap-0.5">
                        <span className="max-w-44 truncate font-medium" title={item.name}>
                          {item.name || `账号 #${item.account_id}`}
                        </span>
                        <div className="flex flex-wrap items-center gap-1 text-[11px] text-muted-foreground">
                          <span className="font-mono">#{item.account_id}</span>
                          <span>· {item.platform || "—"}</span>
                          {item.account_type ? (
                            <Badge variant="secondary" className="h-4 px-1 text-[10px]">
                              {item.account_type}
                            </Badge>
                          ) : null}
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="tabular-nums">{item.current_concurrency}</TableCell>
                    <TableCell className="text-xs tabular-nums">
                      <div className="flex flex-col gap-0.5">
                        <span className="font-medium">
                          {formatCompactNumber(item.today_requests)} 次
                        </span>
                        <span className="text-muted-foreground">
                          标 {money(item.today_standard_cost)} · 本 {money(item.today_account_cost)}
                        </span>
                        <span>扣 {money(item.today_user_cost)}</span>
                      </div>
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">
                      <div className="flex flex-col gap-0.5">
                        <span className="text-muted-foreground">
                          {item.days_window > 0 ? `${item.days_window} 日` : "30 日"} ·{" "}
                          {formatCompactNumber(item.total_requests)} 次
                        </span>
                        <span>
                          <span className="text-muted-foreground">标 </span>
                          {money(item.standard_cost_30d)}
                        </span>
                        <span>
                          <span className="text-muted-foreground">本 </span>
                          {money(item.account_cost_30d)}
                          <span className="text-muted-foreground"> · 扣 </span>
                          {money(item.user_cost_30d)}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">
                      {formatMultiplier(item.monetization_ratio)}
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">
                      <div className="flex flex-col gap-0.5">
                        <span>
                          <span className="text-muted-foreground">扣费/并发 </span>
                          {item.output_per_concurrency_usd != null &&
                          Number.isFinite(item.output_per_concurrency_usd)
                            ? money(item.output_per_concurrency_usd)
                            : "—"}
                        </span>
                        <span>
                          <span className="text-muted-foreground">请求/并发 </span>
                          {item.req_per_concurrency != null &&
                          Number.isFinite(item.req_per_concurrency)
                            ? decimal(item.req_per_concurrency, 1)
                            : "—"}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">
                      {item.window_full_leverage_usd != null &&
                      Number.isFinite(item.window_full_leverage_usd) ? (
                        <div className="flex flex-col gap-0.5">
                          <span>{money(item.window_full_leverage_usd)}</span>
                          {item.window_leverage_key ? (
                            <span className="font-mono text-[11px] text-muted-foreground">
                              {item.window_leverage_key}
                            </span>
                          ) : null}
                        </div>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">
                      <div className="flex flex-col gap-0.5">
                        <div className="flex flex-wrap items-center gap-1">
                          <span className="font-medium">
                            {hasMonthlyCost ? money(item.monthly_cost_usd) : "未设置"}
                          </span>
                          {item.monthly_cost_note ? (
                            <span
                              className="max-w-28 truncate text-[11px] text-muted-foreground"
                              title={item.monthly_cost_note}
                            >
                              ({item.monthly_cost_note})
                            </span>
                          ) : null}
                        </div>
                        <span className="text-muted-foreground">
                          标准价杠杆 {formatMultiplier(item.standard_cost_leverage)}
                        </span>
                        <span className="text-muted-foreground">
                          账面扣费 / 参考月租 {formatMultiplier(item.user_cost_per_monthly_ref)}
                        </span>
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
                        设置月租
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

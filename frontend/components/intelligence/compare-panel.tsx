import { useEffect, useMemo, useState } from "react"
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { Loader2 } from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { apiFetch } from "@/lib/api"
import { formatDurationMS } from "@/lib/format"
import type {
  PerformanceTrendWindow,
  ProbeFamily,
  ProbeTarget,
  TargetCompareResult,
} from "@/lib/api-types"
import {
  FAMILY_META,
  formatSuccessRateClass,
  statusBadgeProps,
} from "@/components/intelligence/shared"
import { cn } from "@/lib/utils"

const SERIES_COLORS = [
  "#0ea5e9",
  "#10b981",
  "#f59e0b",
  "#8b5cf6",
  "#ef4444",
  "#14b8a6",
]

type CompareMetric = "avg_first_token_ms" | "p95_first_token_ms" | "tps" | "success_rate"

const METRIC_META: Record<
  CompareMetric,
  { label: string; format: (v: number) => string; lowerBetter?: boolean }
> = {
  avg_first_token_ms: {
    label: "平均首字 TTFT",
    format: (v) => (v >= 1000 ? `${(v / 1000).toFixed(2)}s` : `${Math.round(v)}ms`),
    lowerBetter: true,
  },
  p95_first_token_ms: {
    label: "P95 首字 TTFT",
    format: (v) => (v >= 1000 ? `${(v / 1000).toFixed(2)}s` : `${Math.round(v)}ms`),
    lowerBetter: true,
  },
  tps: {
    label: "生成速率 TPS",
    format: (v) => `${v.toFixed(1)} tok/s`,
  },
  success_rate: {
    label: "可用率",
    format: (v) => `${v.toFixed(1)}%`,
  },
}

function formatXLabel(iso: string, windowVal: PerformanceTrendWindow): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  if (windowVal === "7d") {
    return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:00`
  }
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`
}

export function ComparePanel({ targets }: { targets: ProbeTarget[] }) {
  const [selectedIDs, setSelectedIDs] = useState<number[]>([])
  const [windowVal, setWindowVal] = useState<PerformanceTrendWindow>("24h")
  const [metric, setMetric] = useState<CompareMetric>("avg_first_token_ms")
  const [data, setData] = useState<TargetCompareResult | null>(null)
  const [loading, setLoading] = useState(false)

  // 默认自动选中前 4 个启用目标，便于开箱即对比。
  useEffect(() => {
    if (targets.length === 0) return
    setSelectedIDs((prev) => {
      const valid = prev.filter((id) => targets.some((t) => t.id === id))
      if (valid.length > 0) return valid
      const enabled = targets.filter((t) => t.enabled).slice(0, 4).map((t) => t.id)
      return enabled.length > 0 ? enabled : targets.slice(0, 4).map((t) => t.id)
    })
  }, [targets])

  const idsParam = selectedIDs.join(",")

  useEffect(() => {
    if (!idsParam) {
      setData(null)
      return
    }
    let cancelled = false
    setLoading(true)
    apiFetch<TargetCompareResult>(
      `/intelligence/compare?ids=${encodeURIComponent(idsParam)}&window=${windowVal}`,
    )
      .then((res) => {
        if (!cancelled) setData(res)
      })
      .catch(() => {
        if (!cancelled) setData(null)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [idsParam, windowVal])

  function toggleTarget(id: number) {
    setSelectedIDs((prev) => {
      if (prev.includes(id)) {
        return prev.filter((x) => x !== id)
      }
      if (prev.length >= 6) {
        return [...prev.slice(1), id]
      }
      return [...prev, id]
    })
  }

  function selectByFamily(fam: ProbeFamily) {
    const matched = targets
      .filter((t) => t.family === fam)
      .slice(0, 6)
      .map((t) => t.id)
    if (matched.length > 0) {
      setSelectedIDs(matched)
    }
  }

  const items = useMemo(() => data?.items ?? [], [data])

  const chartData = useMemo(() => {
    if (items.length === 0) return []
    const basePoints = items[0].trend ?? []
    return basePoints.map((pt, idx) => {
      const row: Record<string, string | number | null> = {
        label: formatXLabel(pt.timestamp, windowVal),
        timestamp: pt.timestamp,
      }
      for (const item of items) {
        const p = item.trend?.[idx]
        const key = `t_${item.target.id}`
        if (!p || p.sample_count === 0) {
          row[key] = null
          continue
        }
        if (metric === "success_rate") {
          row[key] = p.success_rate
        } else {
          const val = p[metric]
          row[key] = p.success_count > 0 && val > 0 ? val : null
        }
      }
      return row
    })
  }, [items, metric, windowVal])

  const hasChartSeries = useMemo(
    () =>
      chartData.some((row) =>
        items.some((it) => row[`t_${it.target.id}`] !== null),
      ),
    [chartData, items],
  )

  const bestTPS = useMemo(() => {
    const vals = items.map((i) => i.performance.tps).filter((v) => v > 0)
    return vals.length > 1 ? Math.max(...vals) : 0
  }, [items])

  const bestTTFT = useMemo(() => {
    const vals = items
      .map((i) => i.performance.avg_first_token_ms)
      .filter((v) => v > 0)
    return vals.length > 1 ? Math.min(...vals) : 0
  }, [items])

  if (targets.length === 0) {
    return (
      <Card className="border-dashed border-border shadow-none">
        <CardContent className="py-12 text-center text-xs text-muted-foreground">
          暂无监测目标可供横向对比，请先创建监测目标。
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <Card className="border-border shadow-none">
        <CardHeader className="space-y-3 border-b border-border px-4 py-3.5 sm:px-5">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-sm font-semibold">
                多渠道横向对比（最多同时对比 6 个目标）
              </CardTitle>
              <p className="text-xs text-muted-foreground">
                在统一时间轴下对比不同渠道的首字延迟、P95 抖动、生成速率与模型智力稳定性，辅助选择主力上游。
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              {(["6h", "24h", "7d"] as PerformanceTrendWindow[]).map((w) => (
                <Button
                  key={w}
                  type="button"
                  size="sm"
                  variant={windowVal === w ? "default" : "outline"}
                  className="h-7 px-2.5 text-xs"
                  onClick={() => setWindowVal(w)}
                >
                  {w === "6h" ? "近 6 小时" : w === "24h" ? "近 24 小时" : "近 7 天"}
                </Button>
              ))}
            </div>
          </div>

          {/* 渠道选择与按家族快选 */}
          <div className="flex flex-wrap items-center gap-1.5">
            {(["claude", "codex", "grok", "domestic"] as ProbeFamily[]).map(
              (fam) => {
                const count = targets.filter((t) => t.family === fam).length
                if (count === 0) return null
                return (
                  <Button
                    key={fam}
                    type="button"
                    size="sm"
                    variant="ghost"
                    className="h-6 px-2 text-[11px] text-muted-foreground hover:text-foreground"
                    onClick={() => selectByFamily(fam)}
                  >
                    快选 {FAMILY_META[fam].label} ({count})
                  </Button>
                )
              },
            )}
          </div>

          <div className="flex flex-wrap gap-1.5">
            {targets.map((t) => {
              const idx = selectedIDs.indexOf(t.id)
              const selected = idx >= 0
              const color = selected
                ? SERIES_COLORS[idx % SERIES_COLORS.length]
                : undefined
              return (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => toggleTarget(t.id)}
                  className={cn(
                    "inline-flex cursor-pointer items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs transition-colors",
                    selected
                      ? "border-foreground/30 bg-muted font-medium text-foreground"
                      : "border-border bg-background text-muted-foreground hover:bg-muted/40",
                  )}
                >
                  <span
                    className="size-2 rounded-full"
                    style={{
                      backgroundColor: color ?? "var(--muted-foreground)",
                      opacity: selected ? 1 : 0.35,
                    }}
                  />
                  <span>{t.name}</span>
                </button>
              )
            })}
          </div>
        </CardHeader>

        <CardContent className="space-y-4 p-4 sm:p-5">
          {/* 指标维度切换 */}
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap gap-1.5">
              {(Object.keys(METRIC_META) as CompareMetric[]).map((m) => (
                <Button
                  key={m}
                  type="button"
                  size="sm"
                  variant={metric === m ? "secondary" : "outline"}
                  className="h-7 px-2.5 text-xs"
                  onClick={() => setMetric(m)}
                >
                  {METRIC_META[m].label}
                </Button>
              ))}
            </div>
            {loading ? (
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                <Loader2 className="size-3.5 animate-spin" />
                聚合中...
              </span>
            ) : null}
          </div>

          {/* 对比折线图 */}
          <div className="h-72 w-full">
            {selectedIDs.length === 0 ? (
              <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-border text-xs text-muted-foreground">
                请至少勾选 1 个监测目标
              </div>
            ) : !hasChartSeries ? (
              <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-border text-xs text-muted-foreground">
                所选目标在当前时间窗口内暂无主动测活性能样本
              </div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart
                  data={chartData}
                  margin={{ top: 8, right: 12, left: 0, bottom: 0 }}
                >
                  <CartesianGrid
                    strokeDasharray="3 3"
                    stroke="var(--border)"
                    vertical={false}
                  />
                  <XAxis
                    dataKey="label"
                    tickLine={false}
                    axisLine={false}
                    minTickGap={24}
                    tick={{ fill: "var(--muted-foreground)", fontSize: 11 }}
                  />
                  <YAxis
                    tickLine={false}
                    axisLine={false}
                    width={56}
                    tick={{ fill: "var(--muted-foreground)", fontSize: 11 }}
                    tickFormatter={(v: number) => METRIC_META[metric].format(v)}
                    domain={metric === "success_rate" ? [0, 100] : ["auto", "auto"]}
                  />
                  <Tooltip
                    formatter={(value: unknown, name: string) => {
                      const numVal = Number(value)
                      return [
                        Number.isFinite(numVal)
                          ? METRIC_META[metric].format(numVal)
                          : "—",
                        name,
                      ]
                    }}
                    contentStyle={{
                      backgroundColor: "var(--popover)",
                      borderColor: "var(--border)",
                      borderRadius: "8px",
                      fontSize: "12px",
                    }}
                  />
                  {items.map((item, idx) => (
                    <Line
                      key={item.target.id}
                      type="monotone"
                      dataKey={`t_${item.target.id}`}
                      name={item.target.name}
                      stroke={SERIES_COLORS[idx % SERIES_COLORS.length]}
                      strokeWidth={2}
                      dot={false}
                      activeDot={{ r: 4 }}
                      connectNulls={false}
                    />
                  ))}
                </LineChart>
              </ResponsiveContainer>
            )}
          </div>

          {/* 决策对比明细表 */}
          {items.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full text-left text-xs">
                <thead className="border-b border-border bg-muted/40 text-muted-foreground">
                  <tr>
                    <th className="px-3 py-2 font-medium">渠道目标</th>
                    <th className="px-3 py-2 font-medium">24h TPS</th>
                    <th className="px-3 py-2 font-medium">平均首字</th>
                    <th className="px-3 py-2 font-medium">平均延迟</th>
                    <th className="px-3 py-2 font-medium">24h 成功率</th>
                    <th className="px-3 py-2 font-medium">Candy 通过率</th>
                    <th className="px-3 py-2 font-medium">指纹 #1 命中</th>
                    <th className="px-3 py-2 font-medium">最新指纹</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {items.map((item, idx) => {
                    const color = SERIES_COLORS[idx % SERIES_COLORS.length]
                    const fam =
                      FAMILY_META[item.target.family] ?? FAMILY_META.other
                    const isBestTPS =
                      bestTPS > 0 && item.performance.tps === bestTPS
                    const isBestTTFT =
                      bestTTFT > 0 &&
                      item.performance.avg_first_token_ms === bestTTFT
                    const fpRun = item.latest_runs?.lmfpd
                    return (
                      <tr key={item.target.id} className="hover:bg-muted/20">
                        <td className="px-3 py-2.5">
                          <div className="flex items-center gap-2">
                            <span
                              className="size-2.5 shrink-0 rounded-full"
                              style={{ backgroundColor: color }}
                            />
                            <Badge
                              variant="outline"
                              className={cn("text-[10px]", fam.badgeClass)}
                            >
                              {fam.label}
                            </Badge>
                            <span className="font-medium text-foreground">
                              {item.target.name}
                            </span>
                          </div>
                        </td>
                        <td
                          className={cn(
                            "px-3 py-2.5 font-mono",
                            isBestTPS &&
                              "font-semibold text-emerald-600 dark:text-emerald-400",
                          )}
                        >
                          {item.performance.tps > 0
                            ? `${item.performance.tps.toFixed(1)}`
                            : "—"}
                        </td>
                        <td
                          className={cn(
                            "px-3 py-2.5 font-mono",
                            isBestTTFT &&
                              "font-semibold text-emerald-600 dark:text-emerald-400",
                          )}
                        >
                          {item.performance.avg_first_token_ms > 0
                            ? formatDurationMS(item.performance.avg_first_token_ms)
                            : "—"}
                        </td>
                        <td className="px-3 py-2.5 font-mono">
                          {item.performance.avg_latency_ms > 0
                            ? formatDurationMS(item.performance.avg_latency_ms)
                            : "—"}
                        </td>
                        <td
                          className={cn(
                            "px-3 py-2.5 font-mono font-medium",
                            formatSuccessRateClass(
                              item.performance.success_rate,
                              item.performance.sample_count,
                            ),
                          )}
                        >
                          {item.performance.sample_count > 0
                            ? `${item.performance.success_rate.toFixed(1)}%`
                            : "—"}
                        </td>
                        <td className="px-3 py-2.5 font-mono">
                          {item.candy_checked_24h > 0
                            ? `${item.candy_passed_24h}/${item.candy_checked_24h} (${Math.round((item.candy_passed_24h * 100) / item.candy_checked_24h)}%)`
                            : "—"}
                        </td>
                        <td className="px-3 py-2.5 font-mono">
                          {item.lmfpd_checked > 0
                            ? `${item.lmfpd_rank1_count}/${item.lmfpd_checked} (${Math.round((item.lmfpd_rank1_count * 100) / item.lmfpd_checked)}%)`
                            : "—"}
                        </td>
                        <td className="px-3 py-2.5">
                          {fpRun ? (
                            <Badge
                              variant="outline"
                              className={cn(
                                "text-[10px]",
                                statusBadgeProps(fpRun.status).className,
                              )}
                            >
                              {fpRun.claimed_rank ? `#${fpRun.claimed_rank} ` : ""}
                              {fpRun.top_candidate || fpRun.summary}
                            </Badge>
                          ) : (
                            <span className="text-muted-foreground">—</span>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          ) : null}
        </CardContent>
      </Card>
    </div>
  )
}

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
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import { formatDurationMS } from "@/lib/format"
import type {
  PerformanceTrendPoint,
  PerformanceTrendSeries,
  PerformanceTrendWindow,
} from "@/lib/api-types"

const WINDOW_OPTIONS: Array<{ value: PerformanceTrendWindow; label: string }> = [
  { value: "6h", label: "近 6 小时" },
  { value: "24h", label: "近 24 小时" },
  { value: "7d", label: "近 7 天" },
]

function formatTickTime(iso: string, window: PerformanceTrendWindow): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  if (window === "7d") {
    return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, "0")}:00`
  }
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`
}

interface ChartRow {
  label: string
  raw: PerformanceTrendPoint
  avgTTFT: number | null
  p95TTFT: number | null
  tps: number | null
}

function TrendTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: Array<{ payload?: ChartRow }>
}) {
  if (!active || !payload?.length || !payload[0]?.payload) return null
  const row = payload[0].payload
  const p = row.raw
  const ts = new Date(p.timestamp)
  const timeText = Number.isNaN(ts.getTime())
    ? row.label
    : ts.toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      })
  return (
    <div className="rounded-lg border border-border bg-popover px-3 py-2 text-xs shadow-md">
      <p className="font-mono font-medium text-foreground">{timeText}</p>
      {p.sample_count === 0 ? (
        <p className="mt-1 text-muted-foreground">该时段无采样</p>
      ) : (
        <div className="mt-1.5 space-y-1 font-mono">
          <p className="text-muted-foreground">
            可用率：{p.success_rate.toFixed(1)}% ({p.success_count}/{p.sample_count})
          </p>
          <p className="text-sky-600 dark:text-sky-400">
            平均首字：{p.avg_first_token_ms > 0 ? formatDurationMS(p.avg_first_token_ms) : "—"}
          </p>
          <p className="text-amber-600 dark:text-amber-400">
            P95 首字：{p.p95_first_token_ms > 0 ? formatDurationMS(p.p95_first_token_ms) : "—"}
          </p>
          <p className="text-emerald-600 dark:text-emerald-400">
            生成速率：{p.tps > 0 ? `${p.tps.toFixed(1)} tok/s` : "—"}
          </p>
        </div>
      )}
    </div>
  )
}

export function PerformanceTrendChart({
  targetID,
  refreshKey = 0,
}: {
  targetID: number
  refreshKey?: number
}) {
  const [windowVal, setWindowVal] = useState<PerformanceTrendWindow>("24h")
  const [series, setSeries] = useState<PerformanceTrendSeries | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    apiFetch<PerformanceTrendSeries>(
      `/intelligence/targets/${targetID}/trend?window=${windowVal}`,
    )
      .then((res) => {
        if (!cancelled) setSeries(res)
      })
      .catch(() => {
        if (!cancelled) setSeries(null)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [targetID, windowVal, refreshKey])

  const rows = useMemo<ChartRow[]>(() => {
    return (series?.points ?? []).map((pt) => {
      const hasSuccess = pt.success_count > 0
      return {
        label: formatTickTime(pt.timestamp, windowVal),
        raw: pt,
        avgTTFT: hasSuccess && pt.avg_first_token_ms > 0 ? pt.avg_first_token_ms : null,
        p95TTFT: hasSuccess && pt.p95_first_token_ms > 0 ? pt.p95_first_token_ms : null,
        tps: hasSuccess && pt.tps > 0 ? pt.tps : null,
      }
    })
  }, [series, windowVal])

  const hasAnyPoint = useMemo(
    () => rows.some((r) => r.avgTTFT !== null || r.tps !== null),
    [rows],
  )

  return (
    <div className="rounded-lg border border-border bg-card p-3.5">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="space-y-0.5">
          <p className="text-xs font-semibold text-foreground">
            首字延迟（TTFT）与生成速率（TPS）趋势
          </p>
          <div className="flex flex-wrap items-center gap-3 text-[11px] text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-sky-500" />
              平均首字 (ms)
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="h-0.5 w-3 border-t-2 border-dashed border-amber-500" />
              P95 首字 (ms)
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-emerald-500" />
              TPS (tok/s)
            </span>
          </div>
        </div>
        <div className="inline-flex items-center gap-1">
          {WINDOW_OPTIONS.map((opt) => (
            <Button
              key={opt.value}
              type="button"
              size="sm"
              variant={windowVal === opt.value ? "default" : "outline"}
              className="h-7 px-2 text-[11px]"
              onClick={() => setWindowVal(opt.value)}
            >
              {opt.label}
            </Button>
          ))}
        </div>
      </div>

      <div className="h-52 w-full">
        {loading && !series ? (
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            <Loader2 className="mr-1.5 size-3.5 animate-spin" />
            加载性能趋势...
          </div>
        ) : !hasAnyPoint ? (
          <div className="flex h-full items-center justify-center rounded-md border border-dashed border-border text-xs text-muted-foreground">
            当前时间窗口内暂无有效测活性能样本
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <LineChart
              data={rows}
              margin={{ top: 6, right: 8, left: 0, bottom: 0 }}
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
                tick={{ fill: "var(--muted-foreground)", fontSize: 10 }}
              />
              <YAxis
                yAxisId="ms"
                tickLine={false}
                axisLine={false}
                width={48}
                tick={{ fill: "var(--muted-foreground)", fontSize: 10 }}
                tickFormatter={(v: number) =>
                  v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v}ms`
                }
              />
              <YAxis
                yAxisId="tps"
                orientation="right"
                tickLine={false}
                axisLine={false}
                width={40}
                tick={{ fill: "var(--muted-foreground)", fontSize: 10 }}
                tickFormatter={(v: number) => `${v}`}
              />
              <Tooltip
                content={<TrendTooltip />}
                cursor={{ stroke: "var(--border)", strokeDasharray: "4 4" }}
              />
              <Line
                yAxisId="ms"
                type="monotone"
                dataKey="avgTTFT"
                stroke="#0ea5e9"
                strokeWidth={2}
                dot={false}
                activeDot={{ r: 4 }}
                connectNulls={false}
              />
              <Line
                yAxisId="ms"
                type="monotone"
                dataKey="p95TTFT"
                stroke="#f59e0b"
                strokeWidth={1.5}
                strokeDasharray="4 3"
                dot={false}
                activeDot={{ r: 3.5 }}
                connectNulls={false}
              />
              <Line
                yAxisId="tps"
                type="monotone"
                dataKey="tps"
                stroke="#10b981"
                strokeWidth={2}
                dot={false}
                activeDot={{ r: 4 }}
                connectNulls={false}
              />
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  )
}

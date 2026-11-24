import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { Button } from "@/components/ui/button"
import { dateTime, decimal, fullDateTime, shortTime } from "@/lib/format"
import type { SiteOpsTrendPoint, SiteOpsTrendRange } from "@/lib/site-ops-types"
import { FieldQualityBadge } from "./quality-badge"

const ranges: { value: SiteOpsTrendRange; label: string }[] = [
  { value: "1h", label: "1 小时" },
  { value: "6h", label: "6 小时" },
  { value: "24h", label: "24 小时" },
  { value: "7d", label: "7 天" },
  { value: "14d", label: "14 天" },
]

type MetricKey = "sla_percent_1h" | "error_rate_percent_1h" | "upstream_error_rate_percent_1h" |
  "qps_current" | "account_switch_count_1m" | "ttft_p95_ms" | "duration_p95_ms" | "conc_used" | "conc_max"

interface Series {
  key: MetricKey
  label: string
  unit: string
  color: string
  axis?: "right"
}

const charts: { title: string; series: Series[] }[] = [
  {
    title: "SLA 与错误率",
    series: [
      { key: "sla_percent_1h", label: "近 1 小时 SLA", unit: "%", color: "#0ea5e9" },
      { key: "error_rate_percent_1h", label: "整体错误率", unit: "%", color: "#d97706", axis: "right" },
      { key: "upstream_error_rate_percent_1h", label: "上游错误率", unit: "%", color: "#a855f7", axis: "right" },
    ],
  },
  {
    title: "QPS 与切号",
    series: [
      { key: "qps_current", label: "QPS", unit: " 请求/秒", color: "#0ea5e9" },
      { key: "account_switch_count_1m", label: "近 1 分钟切号", unit: " 次", color: "#d97706", axis: "right" },
    ],
  },
  {
    title: "P95 延迟",
    series: [
      { key: "ttft_p95_ms", label: "首字（TTFT）", unit: " ms", color: "#0ea5e9" },
      { key: "duration_p95_ms", label: "总耗时", unit: " ms", color: "#a855f7" },
    ],
  },
  {
    title: "去重并发",
    series: [
      { key: "conc_used", label: "使用", unit: "", color: "#0ea5e9" },
      { key: "conc_max", label: "上限", unit: "", color: "#a855f7" },
    ],
  },
]

function chartValue(point: SiteOpsTrendPoint, key: MetricKey) {
  if (!point.reachable) return null
  if ((key === "conc_used" || key === "conc_max") && point.conc_trust_state !== "trusted") return null
  const value = point[key]
  return value != null && Number.isFinite(value) ? value : null
}

type ChartRow = Record<MetricKey, number | null> & { time: number; point: SiteOpsTrendPoint }

function ChartTooltip({ active, payload, series }: {
  active?: boolean
  payload?: Array<{ payload?: ChartRow }>
  series: Series[]
}) {
  const row = payload?.[0]?.payload
  if (!active || !row) return null
  const showQuality = series.some(({ key }) => key === "conc_used") && row.point.conc_trust_state !== "trusted"
  return (
    <div className="space-y-1 rounded-md border bg-popover px-3 py-2 text-xs shadow-sm">
      <p className="font-medium">{fullDateTime(row.point.observed_at)}</p>
      {showQuality ? <FieldQualityBadge quality={row.point.conc_trust_state} /> : series.map(({ key, label, unit }) => (
        <p key={key} className="text-muted-foreground">{label} {row[key] == null ? "无数据" : `${decimal(row[key])}${unit}`}</p>
      ))}
    </div>
  )
}

export function TrendsPanel({ points, range, onRangeChange, loading, error }: {
  points?: SiteOpsTrendPoint[]
  range: SiteOpsTrendRange
  onRangeChange: (range: SiteOpsTrendRange) => void
  loading: boolean
  error?: string
}) {
  const rows: ChartRow[] = (points ?? []).map((point) => ({
    point,
    time: Date.parse(point.observed_at),
    sla_percent_1h: chartValue(point, "sla_percent_1h"),
    error_rate_percent_1h: chartValue(point, "error_rate_percent_1h"),
    upstream_error_rate_percent_1h: chartValue(point, "upstream_error_rate_percent_1h"),
    qps_current: chartValue(point, "qps_current"),
    account_switch_count_1m: chartValue(point, "account_switch_count_1m"),
    ttft_p95_ms: chartValue(point, "ttft_p95_ms"),
    duration_p95_ms: chartValue(point, "duration_p95_ms"),
    conc_used: chartValue(point, "conc_used"),
    conc_max: chartValue(point, "conc_max"),
  })).filter(({ time }) => Number.isFinite(time)).sort((a, b) => a.time - b.time)
  const unverified = rows.filter(({ point }) => point.conc_trust_state === "unverified_zero").length

  return (
    <section aria-busy={loading} className="min-w-0 space-y-3">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">历史趋势</h2>
        <div role="group" aria-label="趋势时间范围" className="flex flex-wrap gap-1">
          {ranges.map(({ value, label }) => (
            <Button key={value} size="sm" className="h-7 px-2 text-xs" variant={range === value ? "default" : "outline"} aria-pressed={range === value} onClick={() => onRangeChange(value)}>
              {label}
            </Button>
          ))}
        </div>
      </header>
      {error ? <p role="alert" className="text-sm text-destructive">{error}</p>
        : loading && !points ? <p role="status" className="text-sm text-muted-foreground">正在加载趋势…</p>
          : <div className="grid gap-2.5 xl:grid-cols-2">
            {charts.map(({ title, series }) => {
              const hasValues = rows.some((row) => series.some(({ key }) => row[key] != null))
              const rightAxis = series.find(({ axis }) => axis === "right")
              const singleSample = rows.length === 1
              return (
                <article key={title} className="min-w-0 space-y-2 rounded-md border bg-card p-3">
                  <h3 className="text-xs font-semibold">{title}</h3>
                  <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    {series.map(({ key, label, color }) => (
                      <span key={key} className="inline-flex items-center gap-1.5">
                        <span aria-hidden="true" className="size-2 rounded-full" style={{ backgroundColor: color }} />
                        {label}
                      </span>
                    ))}
                  </div>
                  {title === "去重并发" && unverified > 0 && (
                    <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                      <FieldQualityBadge quality="unverified_zero" /> {unverified} 个样本已留空
                    </p>
                  )}
                  {hasValues ? (
                    <div className="h-48">
                      <ResponsiveContainer width="100%" height="100%">
                        <LineChart data={rows}>
                          <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" vertical={false} />
                          <XAxis
                            dataKey="time" type="number" scale="time"
                            domain={singleSample ? [rows[0].time - 60_000, rows[0].time + 60_000] : ["dataMin", "dataMax"]}
                            ticks={singleSample ? [rows[0].time] : undefined}
                            tickFormatter={(value: number) => (range === "7d" || range === "14d" ? dateTime : shortTime)(new Date(value).toISOString())}
                            tick={{ fill: "var(--muted-foreground)", fontSize: 10 }}
                            minTickGap={24}
                          />
                          <YAxis yAxisId="left" width={55} unit={series[0].unit === "%" ? "%" : undefined} tick={{ fill: "var(--muted-foreground)", fontSize: 10 }} />
                          {rightAxis && <YAxis yAxisId="right" orientation="right" width={38} unit={rightAxis.unit === "%" ? "%" : undefined} tick={{ fill: "var(--muted-foreground)", fontSize: 10 }} />}
                          <Tooltip content={<ChartTooltip series={series} />} filterNull={false} />
                          {series.map(({ key, color, axis }) => {
                            const sampleCount = rows.filter((row) => row[key] != null).length
                            return <Line key={key} dataKey={key} yAxisId={axis ?? "left"} stroke={color} strokeWidth={2} dot={sampleCount <= 30 ? { r: 2 } : false} connectNulls={false} isAnimationActive={false} />
                          })}
                        </LineChart>
                      </ResponsiveContainer>
                    </div>
                  ) : <div className="grid h-48 place-items-center text-xs text-muted-foreground">当前范围内暂无可信样本</div>}
                </article>
              )
            })}
          </div>}
    </section>
  )
}

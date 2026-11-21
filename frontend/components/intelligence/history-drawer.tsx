import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"
import {
  Activity,
  BrainCircuit,
  Fingerprint,
  Image as ImageIcon,
  Loader2,
  Play,
  SlidersHorizontal,
} from "lucide-react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import { dateTime, formatDurationMS, fullDateTime } from "@/lib/format"
import type {
  DetectorRunResult,
  ProbeIntelligenceRun,
  ProbeTarget,
  TargetOverview,
} from "@/lib/api-types"
import {
  FAMILY_META,
  formatSuccessRateClass,
  PelicanSandboxFrame,
  statusBadgeProps,
  TrendStrip,
} from "@/components/intelligence/shared"
import { PerformanceTrendChart } from "@/components/intelligence/performance-trend-chart"
import { cn } from "@/lib/utils"

interface HistoryDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  target: ProbeTarget | null
  initialTab?: string
  onChanged: () => void
}

interface ParsedFingerprintDetail {
  claimed_model?: string
  claimed_rank?: number
  claimed_in_bank?: boolean
  family_prediction?: string
  probability?: number
  top_candidates?: Array<{
    id: string
    display_name?: string
    probability: number
  }>
}

interface ParsedCandyDetail {
  answer?: string
  reasoning?: string
  passed?: boolean
}

interface ParsedCustomDetail {
  match_mode?: string
  expected?: string
  reason?: string
  answer?: string
}

function safeJSON<T>(raw?: string): T | null {
  if (!raw || !raw.trim()) return null
  try {
    return JSON.parse(raw) as T
  } catch {
    return null
  }
}

export function HistoryDrawer({
  open,
  onOpenChange,
  target,
  initialTab = "liveness",
  onChanged,
}: HistoryDrawerProps) {
  const [tab, setTab] = useState(initialTab)
  const [overview, setOverview] = useState<TargetOverview | null>(null)
  const [runs, setRuns] = useState<ProbeIntelligenceRun[]>([])
  const [loading, setLoading] = useState(false)
  const [runningKey, setRunningKey] = useState<string | null>(null)
  const [trendRefreshKey, setTrendRefreshKey] = useState(0)

  useEffect(() => {
    if (open) {
      setTab(initialTab || "liveness")
    }
  }, [open, initialTab])

  const loadData = useCallback(async () => {
    if (!target) return
    setLoading(true)
    try {
      const ov = await apiFetch<TargetOverview>(
        `/intelligence/targets/${target.id}/overview`,
      )
      setOverview(ov)
      if (tab !== "liveness") {
        const list = await apiFetch<ProbeIntelligenceRun[]>(
          `/intelligence/targets/${target.id}/runs?detector=${encodeURIComponent(tab)}&limit=48`,
        )
        setRuns(Array.isArray(list) ? list : [])
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "加载历史失败")
    } finally {
      setLoading(false)
    }
  }, [target, tab])

  useEffect(() => {
    if (!open || !target) return
    void loadData()
  }, [open, target, tab, loadData])

  async function handleRunDetector(detectorKey: string) {
    if (!target) return
    setRunningKey(detectorKey)
    try {
      const res = await apiFetch<DetectorRunResult>(
        `/intelligence/targets/${target.id}/run`,
        {
          method: "POST",
          body: JSON.stringify({ detector_key: detectorKey }),
        },
      )
      if (res.status === "normal") {
        toast.success(`${detectorKey} 检测通过：${res.summary}`)
      } else if (res.status === "suspected") {
        toast.warning(`${detectorKey} 疑似异常：${res.summary}`)
      } else {
        toast.error(`${detectorKey} 探测失败：${res.error_message || res.summary}`)
      }
      await loadData()
      setTrendRefreshKey((k) => k + 1)
      onChanged()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "执行检测失败")
    } finally {
      setRunningKey(null)
    }
  }

  const family = target ? FAMILY_META[target.family] ?? FAMILY_META.other : FAMILY_META.other
  const perf = overview?.performance

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 p-0 sm:max-w-3xl"
      >
        <SheetHeader className="border-b border-border px-5 py-4">
          <div className="flex flex-wrap items-center gap-2 pr-6">
            <Badge variant="outline" className={cn("text-[11px]", family.badgeClass)}>
              {family.label}
            </Badge>
            <SheetTitle>{target?.name ?? "监测历史"}</SheetTitle>
            {target?.slug ? (
              <span className="font-mono text-xs text-muted-foreground">
                /{target.slug}
              </span>
            ) : null}
          </div>
          <SheetDescription>
            查看近 24 小时性能聚合、60 分钟趋势与各插拔检测项历史记录，支持即时手动单测。
          </SheetDescription>

          {perf ? (
            <div className="mt-2 space-y-2">
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <MetricTile
                  label="24h TPS"
                  value={perf.tps > 0 ? perf.tps.toFixed(1) : "—"}
                />
                <MetricTile
                  label="平均首字 TTFT"
                  value={
                    perf.avg_first_token_ms > 0
                      ? formatDurationMS(perf.avg_first_token_ms)
                      : "—"
                  }
                />
                <MetricTile
                  label="平均总延迟"
                  value={
                    perf.avg_latency_ms > 0
                      ? formatDurationMS(perf.avg_latency_ms)
                      : "—"
                  }
                />
                <MetricTile
                  label={`24h 成功率 (${perf.success_count}/${perf.sample_count})`}
                  value={
                    perf.sample_count > 0
                      ? `${perf.success_rate.toFixed(1)}%`
                      : "—"
                  }
                  valueClass={formatSuccessRateClass(
                    perf.success_rate,
                    perf.sample_count,
                  )}
                />
              </div>
              <div className="rounded-lg border border-border/70 bg-muted/15 px-3 py-2">
                <TrendStrip buckets={overview?.trend ?? []} />
              </div>
            </div>
          ) : null}
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col px-5 py-3">
          <Tabs
            value={tab}
            onValueChange={setTab}
            className="flex min-h-0 flex-1 flex-col"
          >
            <div className="flex flex-wrap items-center justify-between gap-2 pb-3">
              <TabsList className="h-auto flex-wrap justify-start">
                <TabsTrigger value="liveness" className="gap-1.5 text-xs">
                  <Activity className="size-3.5" />
                  主动测活
                </TabsTrigger>
                <TabsTrigger value="candy" className="gap-1.5 text-xs">
                  <BrainCircuit className="size-3.5" />
                  Candy
                </TabsTrigger>
                <TabsTrigger value="lmfpd" className="gap-1.5 text-xs">
                  <Fingerprint className="size-3.5" />
                  数字指纹
                </TabsTrigger>
                <TabsTrigger value="pelican" className="gap-1.5 text-xs">
                  <ImageIcon className="size-3.5" />
                  画鹈鹕
                </TabsTrigger>
                <TabsTrigger value="custom_prompt" className="gap-1.5 text-xs">
                  <SlidersHorizontal className="size-3.5" />
                  自定义题
                </TabsTrigger>
              </TabsList>

              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={runningKey !== null || !target}
                onClick={() => handleRunDetector(tab)}
                className="h-8 gap-1.5 text-xs"
              >
                {runningKey === tab ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <Play className="size-3.5" />
                )}
                {runningKey === tab ? "探测中..." : "立即测本项"}
              </Button>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto pr-1">
              {loading && !overview ? (
                <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
                  <Loader2 className="mr-2 size-4 animate-spin" />
                  加载记录中...
                </div>
              ) : null}

              <TabsContent value="liveness" className="mt-0 space-y-3">
                {target ? (
                  <PerformanceTrendChart
                    targetID={target.id}
                    refreshKey={trendRefreshKey}
                  />
                ) : null}
                {(overview?.recent_samples ?? []).length === 0 ? (
                  <EmptyHint text="暂无主动测活记录。请先在插件配置中为 liveness 设置模型并开启，或点右上角立即测本项。" />
                ) : (
                  <div className="overflow-x-auto rounded-lg border border-border">
                    <table className="w-full text-left text-xs">
                      <thead className="border-b border-border bg-muted/40 text-muted-foreground">
                        <tr>
                          <th className="px-3 py-2 font-medium">状态</th>
                          <th className="px-3 py-2 font-medium">模型</th>
                          <th className="px-3 py-2 font-medium">首字</th>
                          <th className="px-3 py-2 font-medium">总耗时</th>
                          <th className="px-3 py-2 font-medium">TPS</th>
                          <th className="px-3 py-2 font-medium">时间</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border">
                        {(overview?.recent_samples ?? []).map((s) => (
                          <tr key={s.id} className="hover:bg-muted/20">
                            <td className="px-3 py-2">
                              <Badge
                                variant="outline"
                                className={cn(
                                  "text-[10px]",
                                  s.success
                                    ? "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                                    : "border-rose-500/30 bg-rose-500/15 text-rose-700 dark:text-rose-300",
                                )}
                              >
                                {s.success
                                  ? `OK ${s.http_status || 200}`
                                  : `${s.error_class} ${s.http_status || ""}`.trim()}
                              </Badge>
                              {!s.success && s.error_message ? (
                                <p className="mt-1 max-w-xs truncate text-[11px] text-rose-600 dark:text-rose-400">
                                  {s.error_message}
                                </p>
                              ) : null}
                            </td>
                            <td className="px-3 py-2 font-mono">{s.model_name}</td>
                            <td className="px-3 py-2 font-mono">
                              {s.first_token_ms > 0
                                ? formatDurationMS(s.first_token_ms)
                                : "—"}
                            </td>
                            <td className="px-3 py-2 font-mono">
                              {formatDurationMS(s.latency_ms)}
                            </td>
                            <td className="px-3 py-2 font-mono">
                              {s.tps > 0 ? s.tps.toFixed(1) : "—"}
                            </td>
                            <td className="px-3 py-2 font-mono text-muted-foreground">
                              {dateTime(s.observed_at)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </TabsContent>

              <TabsContent value="candy" className="mt-0 space-y-2.5">
                <RunStatusStrip runs={runs} />
                {runs.length === 0 ? (
                  <EmptyHint text="暂无 Candy 智力题检测记录。" />
                ) : (
                  runs.map((r) => {
                    const st = statusBadgeProps(r.status)
                    const detail = safeJSON<ParsedCandyDetail>(r.detail_json)
                    return (
                      <div
                        key={r.id}
                        className="rounded-lg border border-border bg-card p-3 text-xs"
                      >
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge variant="outline" className={st.className}>
                              {st.label}
                            </Badge>
                            <span className="font-medium text-foreground">
                              {r.summary}
                            </span>
                            {r.reused_from_run_id ? (
                              <Badge variant="secondary" className="text-[10px]">
                                同源复用
                              </Badge>
                            ) : null}
                          </div>
                          <span className="font-mono text-muted-foreground">
                            {formatDurationMS(r.latency_ms)} ·{" "}
                            {fullDateTime(r.observed_at)}
                          </span>
                        </div>
                        <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                          模型：{r.model_name} · 首字：
                          {formatDurationMS(r.first_token_ms)} · 输出：
                          {r.output_tokens} tokens
                        </p>
                        {r.error_message ? (
                          <pre className="mt-2 whitespace-pre-wrap rounded-md border border-rose-500/20 bg-rose-500/5 p-2 font-mono text-[11px] text-rose-700 dark:text-rose-300">
                            {r.error_message}
                          </pre>
                        ) : detail?.answer ? (
                          <pre className="mt-2 whitespace-pre-wrap rounded-md border border-border bg-muted/30 p-2 font-mono text-[11px]">
                            {detail.answer}
                          </pre>
                        ) : null}
                      </div>
                    )
                  })
                )}
              </TabsContent>

              <TabsContent value="lmfpd" className="mt-0 space-y-2.5">
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                  数字指纹基于 3 轮 1-355 随机整数分布统计特征与参考库比对，排名与概率仅供参考，不作为绝对身份证明。
                </div>
                <RunStatusStrip runs={runs} />
                {runs.length === 0 ? (
                  <EmptyHint text="暂无 lmfpd 数字指纹检测记录。" />
                ) : (
                  runs.map((r) => {
                    const st = statusBadgeProps(r.status)
                    const detail = safeJSON<ParsedFingerprintDetail>(r.detail_json)
                    const topK = (detail?.top_candidates ?? []).slice(0, 6)
                    return (
                      <div
                        key={r.id}
                        className="rounded-lg border border-border bg-card p-3 text-xs"
                      >
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge variant="outline" className={st.className}>
                              {st.label}
                            </Badge>
                            <span className="font-medium">{r.summary}</span>
                            {r.claimed_rank ? (
                              <Badge variant="secondary" className="font-mono text-[10px]">
                                声称排名 #{r.claimed_rank}
                              </Badge>
                            ) : null}
                          </div>
                          <span className="font-mono text-muted-foreground">
                            {formatDurationMS(r.latency_ms)} ·{" "}
                            {fullDateTime(r.observed_at)}
                          </span>
                        </div>
                        {r.error_message ? (
                          <p className="mt-2 text-rose-600 dark:text-rose-400">
                            {r.error_message}
                          </p>
                        ) : null}
                        {topK.length > 0 ? (
                          <div className="mt-2.5 space-y-1.5">
                            {topK.map((cand, idx) => {
                              const pct = Math.min(
                                100,
                                Math.max(0, cand.probability * 100),
                              )
                              return (
                                <div
                                  key={`${cand.id}-${idx}`}
                                  className="grid grid-cols-[24px_minmax(130px,1.2fr)_1fr_48px] items-center gap-2 font-mono text-[11px]"
                                >
                                  <span className="text-muted-foreground">
                                    #{idx + 1}
                                  </span>
                                  <span className="truncate text-foreground">
                                    {cand.display_name || cand.id}
                                  </span>
                                  <div className="h-2 overflow-hidden rounded-full bg-muted">
                                    <div
                                      className={cn(
                                        "h-full rounded-full",
                                        idx === 0
                                          ? "bg-emerald-500"
                                          : "bg-foreground/40",
                                      )}
                                      style={{ width: `${pct.toFixed(1)}%` }}
                                    />
                                  </div>
                                  <span className="text-right text-muted-foreground">
                                    {pct.toFixed(1)}%
                                  </span>
                                </div>
                              )
                            })}
                          </div>
                        ) : null}
                      </div>
                    )
                  })
                )}
              </TabsContent>

              <TabsContent value="pelican" className="mt-0 space-y-2.5">
                <div className="rounded-lg border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                  所有 AI 生成的画鹈鹕 SVG 均在空 sandbox iframe 中隔离渲染，彻底阻断脚本与外链请求。
                </div>
                {runs.length === 0 ? (
                  <EmptyHint text="暂无画鹈鹕 SVG 记录。" />
                ) : (
                  <div className="grid gap-3 sm:grid-cols-2">
                    {runs.slice(0, 12).map((r) => {
                      const st = statusBadgeProps(r.status)
                      return (
                        <div
                          key={r.id}
                          className="overflow-hidden rounded-xl border border-border bg-card"
                        >
                          {r.status === "normal" ? (
                            <PelicanSandboxFrame
                              runUUID={r.run_uuid}
                              className="rounded-none border-0 border-b"
                            />
                          ) : (
                            <div className="flex aspect-[4/3] items-center justify-center border-b border-border bg-muted/30 p-4 text-center text-xs text-muted-foreground">
                              {r.error_message || r.summary}
                            </div>
                          )}
                          <div className="space-y-1 p-3 text-xs">
                            <div className="flex items-center justify-between gap-2">
                              <Badge variant="outline" className={st.className}>
                                {st.label}
                              </Badge>
                              <span className="font-mono text-[11px] text-muted-foreground">
                                {formatDurationMS(r.latency_ms)} ·{" "}
                                {dateTime(r.observed_at)}
                              </span>
                            </div>
                            <p className="truncate font-mono text-[11px] text-muted-foreground">
                              {r.model_name} · {r.output_tokens} tokens
                            </p>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </TabsContent>

              <TabsContent value="custom_prompt" className="mt-0 space-y-2.5">
                <RunStatusStrip runs={runs} />
                {runs.length === 0 ? (
                  <EmptyHint text="暂无自定义智力题记录。请在插件配置中编写题目与校验规则后开启。" />
                ) : (
                  runs.map((r) => {
                    const st = statusBadgeProps(r.status)
                    const detail = safeJSON<ParsedCustomDetail>(r.detail_json)
                    return (
                      <div
                        key={r.id}
                        className="rounded-lg border border-border bg-card p-3 text-xs"
                      >
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge variant="outline" className={st.className}>
                              {st.label}
                            </Badge>
                            <span className="font-medium">{r.summary}</span>
                          </div>
                          <span className="font-mono text-muted-foreground">
                            {formatDurationMS(r.latency_ms)} ·{" "}
                            {fullDateTime(r.observed_at)}
                          </span>
                        </div>
                        {detail?.expected ? (
                          <p className="mt-1 text-[11px] text-muted-foreground">
                            规则：{detail.match_mode} · 期望：{detail.expected}
                          </p>
                        ) : null}
                        {r.error_message ? (
                          <pre className="mt-2 whitespace-pre-wrap rounded-md border border-rose-500/20 bg-rose-500/5 p-2 font-mono text-[11px] text-rose-700 dark:text-rose-300">
                            {r.error_message}
                          </pre>
                        ) : detail?.answer ? (
                          <pre className="mt-2 whitespace-pre-wrap rounded-md border border-border bg-muted/30 p-2 font-mono text-[11px]">
                            {detail.answer}
                          </pre>
                        ) : null}
                      </div>
                    )
                  })
                )}
              </TabsContent>
            </div>
          </Tabs>
        </div>
      </SheetContent>
    </Sheet>
  )
}

function MetricTile({
  label,
  value,
  valueClass,
}: {
  label: string
  value: string
  valueClass?: string
}) {
  return (
    <div className="rounded-lg border border-border bg-muted/30 px-3 py-2">
      <p className="truncate text-[11px] text-muted-foreground">{label}</p>
      <p className={cn("mt-0.5 font-mono text-sm font-semibold", valueClass)}>
        {value}
      </p>
    </div>
  )
}

function RunStatusStrip({ runs }: { runs: ProbeIntelligenceRun[] }) {
  if (runs.length === 0) return null
  return (
    <div className="flex flex-wrap gap-1 rounded-lg border border-border bg-muted/20 p-2">
      {runs.map((r) => (
        <span
          key={r.id}
          title={`${fullDateTime(r.observed_at)} · ${r.summary}`}
          className={cn(
            "h-5 w-3 rounded-[3px]",
            r.status === "normal"
              ? "bg-emerald-500/85"
              : r.status === "suspected"
                ? "bg-amber-500/90"
                : "bg-rose-500/90",
          )}
        />
      ))}
    </div>
  )
}

function EmptyHint({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-dashed border-border px-4 py-10 text-center text-xs text-muted-foreground">
      {text}
    </div>
  )
}

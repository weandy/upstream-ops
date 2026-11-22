import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import {
  Activity,
  AlertTriangle,
  BarChart3,
  BrainCircuit,
  Globe,
  History,
  LayoutList,
  Loader2,
  PauseCircle,
  PencilLine,
  Play,
  Plus,
  RefreshCw,
  Search,
  SlidersHorizontal,
  Trash2,
} from "lucide-react"
import { Card, CardContent } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { apiFetch } from "@/lib/api"
import { formatDurationMS, relativeTime } from "@/lib/format"
import {
  useChannels,
  useIntelligenceDetectors,
  useIntelligenceOverviews,
} from "@/lib/queries"
import type {
  DetectorRunResult,
  ProbeDetectorConfig,
  ProbeFamily,
  ProbeTarget,
  TargetOverview,
} from "@/lib/api-types"
import {
  FAMILY_META,
  formatSuccessRateClass,
  PelicanSandboxFrame,
  PROTOCOL_LABELS,
  statusBadgeProps,
  TrendStrip,
  trendWindowLabel,
} from "@/components/intelligence/shared"
import { TargetFormDialog } from "@/components/intelligence/target-form-dialog"
import { DetectorsDrawer } from "@/components/intelligence/detectors-drawer"
import { HistoryDrawer } from "@/components/intelligence/history-drawer"
import { ComparePanel } from "@/components/intelligence/compare-panel"
import { cn } from "@/lib/utils"

export default function IntelligencePage() {
  const overviewsQuery = useIntelligenceOverviews()
  const detectorsQuery = useIntelligenceDetectors()
  const channelsQuery = useChannels()
  const { confirm, dialog: confirmDialog } = useConfirm()

  const [search, setSearch] = useState("")
  const [familyFilter, setFamilyFilter] = useState<ProbeFamily | "all">("all")
  const [onlyAlert, setOnlyAlert] = useState(false)
  const [viewMode, setViewMode] = useState<"list" | "compare">("list")

  const [formOpen, setFormOpen] = useState(false)
  const [editingTarget, setEditingTarget] = useState<ProbeTarget | null>(null)
  const [detectorsTarget, setDetectorsTarget] = useState<ProbeTarget | null>(
    null,
  )
  const [historyState, setHistoryState] = useState<{
    target: ProbeTarget | null
    tab: string
  }>({ target: null, tab: "liveness" })
  const [runningTargetIDs, setRunningTargetIDs] = useState<Set<number>>(
    () => new Set(),
  )
  const [nowMs, setNowMs] = useState<number>(() => Date.now())

  useEffect(() => {
    const timer = window.setInterval(() => {
      setNowMs(Date.now())
    }, 30_000)
    return () => window.clearInterval(timer)
  }, [])

  const overviewList = useMemo(
    () => overviewsQuery.data ?? [],
    [overviewsQuery.data],
  )
  const targets = useMemo(
    () => overviewList.map((ov) => ov.target),
    [overviewList],
  )
  const overviews = useMemo(() => {
    const map: Record<number, TargetOverview> = {}
    for (const ov of overviewList) {
      map[ov.target.id] = ov
    }
    return map
  }, [overviewList])
  const loadingOverviews = overviewsQuery.loading
  const detectors = useMemo(
    () => detectorsQuery.data ?? [],
    [detectorsQuery.data],
  )
  const channelNameMap = useMemo(() => {
    const map = new Map<number, string>()
    for (const c of channelsQuery.data ?? []) {
      map.set(c.id, c.name)
    }
    return map
  }, [channelsQuery.data])

  const kpi = useMemo(() => {
    let enabledCount = 0
    let embedCount = 0
    let totalSamples = 0
    let totalSuccess = 0
    let alertTargets = 0

    for (const t of targets) {
      if (t.enabled) enabledCount++
      if (t.embed_visible) embedCount++
      const ov = overviews[t.id]
      if (!ov) continue
      totalSamples += ov.performance.sample_count
      totalSuccess += ov.performance.success_count
      const hasSuspected = Object.values(ov.latest_runs ?? {}).some(
        (r) => r.status === "suspected" || r.status === "error",
      )
      const lowRate =
        ov.performance.sample_count > 0 && ov.performance.success_rate < 90
      if (hasSuspected || lowRate) alertTargets++
    }
    const avgRate =
      totalSamples > 0 ? (totalSuccess * 100) / totalSamples : null
    return {
      total: targets.length,
      enabledCount,
      embedCount,
      avgRate,
      alertTargets,
    }
  }, [targets, overviews])

  const filteredTargets = useMemo(() => {
    const q = search.trim().toLowerCase()
    return targets.filter((t) => {
      if (familyFilter !== "all" && t.family !== familyFilter) return false
      if (q) {
        const chName = channelNameMap.get(t.channel_id) ?? ""
        const hay = `${t.name} ${t.slug} ${t.base_url} ${chName} ${t.source_group_name ?? ""}`.toLowerCase()
        if (!hay.includes(q)) return false
      }
      if (onlyAlert) {
        const ov = overviews[t.id]
        if (!ov) return false
        const hasIssue =
          (ov.performance.sample_count > 0 &&
            ov.performance.success_rate < 90) ||
          Object.values(ov.latest_runs ?? {}).some(
            (r) => r.status === "suspected" || r.status === "error",
          )
        if (!hasIssue) return false
      }
      return true
    })
  }, [targets, familyFilter, search, onlyAlert, overviews, channelNameMap])

  async function handleRunAllEnabled(target: ProbeTarget) {
    setRunningTargetIDs((prev) => {
      const next = new Set(prev)
      next.add(target.id)
      return next
    })
    try {
      const results = await apiFetch<Record<string, DetectorRunResult>>(
        `/intelligence/targets/${target.id}/run`,
        { method: "POST", body: JSON.stringify({}) },
      )
      const keys = Object.keys(results ?? {})
      if (keys.length === 0) {
        toast.info(`${target.name} 尚未启用任何检测插件，请先点击「插件配置」`)
      } else {
        const failed = keys.filter((k) => results[k]?.status !== "normal")
        if (failed.length === 0) {
          toast.success(`${target.name} 全部 ${keys.length} 项检测通过`)
        } else {
          toast.warning(
            `${target.name} 完成 ${keys.length} 项检测，其中 ${failed.length} 项异常`,
          )
        }
      }
      overviewsQuery.refetch()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "触发检测失败")
    } finally {
      setRunningTargetIDs((prev) => {
        const next = new Set(prev)
        next.delete(target.id)
        return next
      })
    }
  }

  async function handleDelete(target: ProbeTarget) {
    const ok = await confirm({
      title: `删除监测目标 ${target.name}？`,
      description:
        "将同时清理该目标的全部检测配置、主动测活样本与历史智力记录，且不可撤销。",
      confirmLabel: "删除",
      destructive: true,
    })
    if (!ok) return
    try {
      await apiFetch(`/intelligence/targets/${target.id}`, {
        method: "DELETE",
      })
      toast.success(`已删除监测目标 ${target.name}`)
      overviewsQuery.refetch()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败")
    }
  }

  function handleRefreshAll() {
    overviewsQuery.refetch()
  }

  return (
    <section className="space-y-5">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div className="space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-lg font-semibold tracking-tight text-foreground sm:text-xl">
              模型智力与主动测活监测
            </h1>
            <Badge
              variant="outline"
              className="border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
            >
              外挂式独立引擎 · {detectors.length} 款内置插件
            </Badge>
          </div>
          <p className="max-w-3xl text-xs leading-5 text-muted-foreground sm:text-sm">
            独立于转发网关直接探测监控渠道或直连端点，按目标自由插拔开启主动测活、Candy 逻辑题、纯 Go 数字指纹（lmfpd）、画鹈鹕 SVG 与自定义题。
          </p>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <div className="inline-flex rounded-md border border-border bg-muted/30 p-0.5">
            <Button
              type="button"
              size="sm"
              variant={viewMode === "list" ? "secondary" : "ghost"}
              className="h-7 gap-1 px-2.5 text-xs"
              onClick={() => setViewMode("list")}
            >
              <LayoutList className="size-3.5" />
              目标列表
            </Button>
            <Button
              type="button"
              size="sm"
              variant={viewMode === "compare" ? "secondary" : "ghost"}
              className="h-7 gap-1 px-2.5 text-xs"
              onClick={() => setViewMode("compare")}
            >
              <BarChart3 className="size-3.5" />
              横向对比
            </Button>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={handleRefreshAll}
            disabled={loadingOverviews}
            className="gap-1.5"
          >
            <RefreshCw
              className={cn("size-3.5", loadingOverviews && "animate-spin")}
            />
            刷新快照
          </Button>
          <Button
            size="sm"
            onClick={() => {
              setEditingTarget(null)
              setFormOpen(true)
            }}
            className="gap-1.5"
          >
            <Plus className="size-3.5" />
            新增监测目标
          </Button>
        </div>
      </header>

      {/* KPI 总览条 */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <KpiCard
          icon={<BrainCircuit className="size-4 text-emerald-600 dark:text-emerald-400" />}
          label="监测目标"
          value={`${kpi.enabledCount} / ${kpi.total}`}
          hint="已启用 / 总计"
        />
        <KpiCard
          icon={<Activity className="size-4 text-sky-600 dark:text-sky-400" />}
          label="24h 平均可用率"
          value={kpi.avgRate != null ? `${kpi.avgRate.toFixed(1)}%` : "—"}
          valueClass={
            kpi.avgRate != null
              ? formatSuccessRateClass(kpi.avgRate, 1)
              : undefined
          }
          hint="基于全部活跃目标测活采样"
        />
        <KpiCard
          icon={<AlertTriangle className="size-4 text-amber-600 dark:text-amber-400" />}
          label="需关注 / 疑似异常"
          value={String(kpi.alertTargets)}
          valueClass={
            kpi.alertTargets > 0
              ? "text-amber-600 dark:text-amber-400"
              : "text-emerald-600 dark:text-emerald-400"
          }
          hint="可用率 <90% 或智力检测异常"
        />
        <KpiCard
          icon={<Globe className="size-4 text-violet-600 dark:text-violet-400" />}
          label="对外 Embed 公开"
          value={String(kpi.embedCount)}
          hint="可在外部嵌入看板中展示"
        />
      </div>

      {viewMode === "compare" ? (
        <ComparePanel targets={targets} />
      ) : (
        <>
          {/* 筛选栏 */}
          <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-center gap-1.5">
          {(["all", "claude", "codex", "grok", "domestic", "other"] as const).map(
            (fam) => (
              <Button
                key={fam}
                type="button"
                size="sm"
                variant={familyFilter === fam ? "default" : "outline"}
                className="h-7 px-2.5 text-xs"
                onClick={() => setFamilyFilter(fam)}
              >
                {fam === "all" ? "全部家族" : FAMILY_META[fam].label}
              </Button>
            ),
          )}
          <Button
            type="button"
            size="sm"
            variant={onlyAlert ? "destructive" : "outline"}
            className="h-7 px-2.5 text-xs"
            onClick={() => setOnlyAlert((v) => !v)}
          >
            仅看异常
          </Button>
        </div>
        <div className="relative w-full sm:w-64">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索目标名、渠道或域名..."
            className="h-8 pl-8 text-xs"
          />
        </div>
      </div>

      {/* 目标列表 */}
      {overviewsQuery.loading && targets.length === 0 ? (
        <Card className="border-border shadow-none">
          <CardContent className="flex items-center justify-center py-14 text-sm text-muted-foreground">
            <Loader2 className="mr-2 size-4 animate-spin" />
            正在加载监测目标...
          </CardContent>
        </Card>
      ) : filteredTargets.length === 0 ? (
        <Card className="border-dashed border-border shadow-none">
          <CardContent className="flex flex-col items-center justify-center gap-3 py-14 text-center">
            <BrainCircuit className="size-8 text-muted-foreground/50" />
            <div className="space-y-1">
              <p className="text-sm font-medium text-foreground">
                {targets.length === 0
                  ? "还没有配置任何监测目标"
                  : "当前筛选条件下没有匹配的监测目标"}
              </p>
              <p className="text-xs text-muted-foreground">
                点击右上角「新增监测目标」绑定已有监控渠道或直连 API 端点，再自由勾选需要开启的检测插件。
              </p>
            </div>
            {targets.length === 0 ? (
              <Button
                size="sm"
                onClick={() => {
                  setEditingTarget(null)
                  setFormOpen(true)
                }}
              >
                <Plus className="mr-1.5 size-3.5" />
                创建第一个监测目标
              </Button>
            ) : null}
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {filteredTargets.map((target) => {
            const ov = overviews[target.id]
            const family = FAMILY_META[target.family] ?? FAMILY_META.other
            const enabledCfgs = (ov?.detector_configs ?? []).filter(
              (c) => c.enabled,
            )
            const activeBackoffs = getActiveBackoffs(enabledCfgs, nowMs)
            const candyRun = ov?.latest_runs?.candy
            const lmfpdRun = ov?.latest_runs?.lmfpd
            const pelicanRun = ov?.latest_runs?.pelican
            const customRun = ov?.latest_runs?.custom_prompt
            const sourceText =
              target.source_kind === "channel"
                ? `${channelNameMap.get(target.channel_id) ?? `渠道 #${target.channel_id}`}${target.source_group_name ? ` / ${target.source_group_name}` : ""}`
                : target.base_url

            return (
              <Card
                key={target.id}
                className={cn(
                  "overflow-hidden border-border shadow-none transition-colors hover:border-foreground/20",
                  !target.enabled && "opacity-65",
                )}
              >
                <CardContent className="p-4">
                  <div className="flex flex-col gap-3.5 xl:flex-row xl:items-center xl:justify-between">
                    {/* 左侧：身份与元信息 */}
                    <div className="min-w-0 space-y-1 xl:w-[250px] xl:shrink-0">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <Badge
                          variant="outline"
                          className={cn("text-[10px]", family.badgeClass)}
                        >
                          {family.label}
                        </Badge>
                        <span className="truncate text-sm font-semibold text-foreground">
                          {target.name}
                        </span>
                        {!target.enabled ? (
                          <Badge variant="secondary" className="text-[10px]">
                            已暂停
                          </Badge>
                        ) : null}
                        {target.embed_visible ? (
                          <Badge
                            variant="outline"
                            className="border-sky-500/30 bg-sky-500/10 text-[10px] text-sky-700 dark:text-sky-300"
                          >
                            Embed
                          </Badge>
                        ) : null}
                      </div>
                      <p className="truncate font-mono text-[11px] text-muted-foreground">
                        {sourceText}
                      </p>
                      <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
                        <span>
                          {PROTOCOL_LABELS[target.protocol] ?? target.protocol}
                        </span>
                        <span>·</span>
                        <span className="font-mono">
                          {target.api_key_hint || "未配置 Key"}
                        </span>
                        <span>·</span>
                        <span>已启 {enabledCfgs.length} 插件</span>
                      </div>
                    </div>

                    {/* 中部：4 项性能指标 + 略加宽的等高 60 分钟胶囊条 */}
                    <div className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between xl:justify-start xl:gap-5">
                      <div className="grid grid-cols-4 gap-3 sm:w-[270px] sm:shrink-0">
                        <div>
                          <p className="text-[10px] text-muted-foreground">TPS</p>
                          <p className="font-mono text-sm font-semibold tabular-nums">
                            {ov && ov.performance.tps > 0
                              ? ov.performance.tps.toFixed(1)
                              : "—"}
                          </p>
                        </div>
                        <div>
                          <p className="text-[10px] text-muted-foreground">首字</p>
                          <p className="font-mono text-sm font-semibold tabular-nums">
                            {ov && ov.performance.avg_first_token_ms > 0
                              ? formatDurationMS(ov.performance.avg_first_token_ms)
                              : "—"}
                          </p>
                        </div>
                        <div>
                          <p className="text-[10px] text-muted-foreground">延迟</p>
                          <p className="font-mono text-sm font-semibold tabular-nums">
                            {ov && ov.performance.avg_latency_ms > 0
                              ? formatDurationMS(ov.performance.avg_latency_ms)
                              : "—"}
                          </p>
                        </div>
                        <div>
                          <p className="text-[10px] text-muted-foreground">24h 成功</p>
                          <p
                            className={cn(
                              "font-mono text-sm font-semibold tabular-nums",
                              ov
                                ? formatSuccessRateClass(
                                    ov.performance.success_rate,
                                    ov.performance.sample_count,
                                  )
                                : "text-muted-foreground",
                            )}
                          >
                            {ov && ov.performance.sample_count > 0
                              ? `${ov.performance.success_rate.toFixed(1)}%`
                              : "—"}
                          </p>
                        </div>
                      </div>

                      <div className="min-w-0 flex-1">
                        <div className="mb-1 flex items-center justify-between text-[10px] text-muted-foreground">
                          <span>{trendWindowLabel(ov?.trend_window_minutes)}</span>
                          <span>
                            {ov?.recent_samples?.[0]
                              ? relativeTime(ov.recent_samples[0].observed_at)
                              : "待采样"}
                          </span>
                        </div>
                        <TrendStrip
                          buckets={ov?.trend ?? []}
                          compact
                          windowMinutes={ov?.trend_window_minutes || 60}
                        />
                      </div>
                    </div>

                    {/* 右侧：操作按钮 */}
                    <div className="flex shrink-0 flex-wrap items-center gap-1.5">
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-8 gap-1 text-xs"
                        disabled={runningTargetIDs.has(target.id)}
                        onClick={() => handleRunAllEnabled(target)}
                      >
                        {runningTargetIDs.has(target.id) ? (
                          <Loader2 className="size-3.5 animate-spin" />
                        ) : (
                          <Play className="size-3.5" />
                        )}
                        立即测
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-8 gap-1 text-xs"
                        onClick={() => setDetectorsTarget(target)}
                      >
                        <SlidersHorizontal className="size-3.5" />
                        插件配置
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-8 gap-1 text-xs"
                        onClick={() =>
                          setHistoryState({ target, tab: "liveness" })
                        }
                      >
                        <History className="size-3.5" />
                        详情
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8"
                        aria-label="编辑目标"
                        onClick={() => {
                          setEditingTarget(target)
                          setFormOpen(true)
                        }}
                      >
                        <PencilLine className="size-3.5" />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8 text-muted-foreground hover:text-destructive"
                        aria-label="删除目标"
                        onClick={() => handleDelete(target)}
                      >
                        <Trash2 className="size-3.5" />
                      </Button>
                    </div>
                  </div>

                  {/* 底部：模型智力检测结果摘要行 */}
                  <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-border/60 pt-2.5 text-xs">
                    <span className="text-[11px] font-medium text-muted-foreground">
                      模型智力检测：
                    </span>
                    {candyRun ||
                    enabledCfgs.some((c) => c.detector_key === "candy") ? (
                      <button
                        type="button"
                        onClick={() => setHistoryState({ target, tab: "candy" })}
                        className="cursor-pointer"
                      >
                        <Badge
                          variant="outline"
                          className={cn(
                            "text-[11px] hover:opacity-80",
                            statusBadgeProps(candyRun?.status).className,
                          )}
                        >
                          Candy{" "}
                          {ov && ov.candy_checked_24h > 0
                            ? `${ov.candy_passed_24h}/${ov.candy_checked_24h}`
                            : statusBadgeProps(candyRun?.status).label}
                        </Badge>
                      </button>
                    ) : null}

                    {lmfpdRun ||
                    enabledCfgs.some((c) => c.detector_key === "lmfpd") ? (
                      <button
                        type="button"
                        onClick={() => setHistoryState({ target, tab: "lmfpd" })}
                        className="cursor-pointer"
                      >
                        <Badge
                          variant="outline"
                          className={cn(
                            "text-[11px] hover:opacity-80",
                            statusBadgeProps(lmfpdRun?.status).className,
                          )}
                        >
                          指纹{" "}
                          {lmfpdRun?.top_candidate
                            ? `${lmfpdRun.claimed_rank ? `#${lmfpdRun.claimed_rank} ` : ""}${lmfpdRun.top_candidate}`
                            : statusBadgeProps(lmfpdRun?.status).label}
                        </Badge>
                      </button>
                    ) : null}

                    {pelicanRun ||
                    enabledCfgs.some((c) => c.detector_key === "pelican") ? (
                      <button
                        type="button"
                        onClick={() =>
                          setHistoryState({ target, tab: "pelican" })
                        }
                        className="inline-flex cursor-pointer items-center gap-1.5"
                      >
                        {pelicanRun?.status === "normal" &&
                        pelicanRun.run_uuid ? (
                          <PelicanSandboxFrame
                            runUUID={pelicanRun.run_uuid}
                            variant="thumbnail"
                          />
                        ) : null}
                        <Badge
                          variant="outline"
                          className={cn(
                            "text-[11px] hover:opacity-80",
                            statusBadgeProps(pelicanRun?.status).className,
                          )}
                        >
                          画鹈鹕 {statusBadgeProps(pelicanRun?.status).label}
                        </Badge>
                      </button>
                    ) : null}

                    {customRun ||
                    enabledCfgs.some(
                      (c) => c.detector_key === "custom_prompt",
                    ) ? (
                      <button
                        type="button"
                        onClick={() =>
                          setHistoryState({ target, tab: "custom_prompt" })
                        }
                        className="cursor-pointer"
                      >
                        <Badge
                          variant="outline"
                          className={cn(
                            "text-[11px] hover:opacity-80",
                            statusBadgeProps(customRun?.status).className,
                          )}
                        >
                          自定义题{" "}
                          {customRun?.summary ||
                            statusBadgeProps(customRun?.status).label}
                        </Badge>
                      </button>
                    ) : null}

                    {!candyRun &&
                    !lmfpdRun &&
                    !pelicanRun &&
                    !customRun &&
                    enabledCfgs.filter((c) => c.detector_key !== "liveness")
                      .length === 0 ? (
                      <span className="text-[11px] text-muted-foreground">
                        暂未开启智力监测插件，点击「插件配置」自由勾选
                      </span>
                    ) : null}

                    {activeBackoffs.length > 0 ? (
                      <div className="ml-auto flex flex-wrap items-center gap-1.5">
                        {activeBackoffs.map((item) => (
                          <Badge
                            key={item.detectorKey}
                            variant="outline"
                            title={`已连续失败 ${item.consecutiveErrors} 次，退避至 ${item.untilText}；点击「立即测」或更新密钥后自动恢复`}
                            className="gap-1 border-amber-500/40 bg-amber-500/10 text-[11px] text-amber-700 dark:text-amber-300"
                          >
                            <PauseCircle className="size-3" />
                            <span>
                              {item.detectorLabel} 退避中（{item.reasonLabel} · 剩{" "}
                              {item.remainingText}）
                            </span>
                          </Badge>
                        ))}
                      </div>
                    ) : null}
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </div>
      )}
        </>
      )}

      <TargetFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        target={editingTarget}
        onSaved={() => {
          overviewsQuery.refetch()
        }}
      />

      <DetectorsDrawer
        open={Boolean(detectorsTarget)}
        onOpenChange={(open) => {
          if (!open) setDetectorsTarget(null)
        }}
        target={detectorsTarget}
        detectors={detectors}
        onSaved={() => {
          overviewsQuery.refetch()
        }}
      />

      <HistoryDrawer
        open={Boolean(historyState.target)}
        onOpenChange={(open) => {
          if (!open) setHistoryState({ target: null, tab: "liveness" })
        }}
        target={historyState.target}
        initialTab={historyState.tab}
        onChanged={() => {
          overviewsQuery.refetch()
        }}
      />

      {confirmDialog}
    </section>
  )
}

function KpiCard({
  icon,
  label,
  value,
  hint,
  valueClass,
}: {
  icon: React.ReactNode
  label: string
  value: string
  hint: string
  valueClass?: string
}) {
  return (
    <Card className="border-border shadow-none">
      <CardContent className="p-4">
        <div className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground">{label}</span>
          {icon}
        </div>
        <p
          className={cn(
            "mt-2 font-mono text-2xl font-semibold tracking-tight text-foreground",
            valueClass,
          )}
        >
          {value}
        </p>
        <p className="mt-1 truncate text-[11px] text-muted-foreground">
          {hint}
        </p>
      </CardContent>
    </Card>
  )
}

interface ActiveBackoffItem {
  detectorKey: string
  detectorLabel: string
  reasonLabel: string
  consecutiveErrors: number
  remainingText: string
  untilText: string
}

const DETECTOR_SHORT_LABELS: Record<string, string> = {
  liveness: "测活",
  candy: "Candy",
  lmfpd: "指纹",
  pelican: "画鹈鹕",
  custom_prompt: "自定义题",
}

const ERROR_CLASS_LABELS: Record<string, string> = {
  rate_limit: "429限流",
  auth: "鉴权失败",
  config: "配置错误",
  timeout: "请求超时",
  network: "网络异常",
  upstream: "上游异常",
  truncated: "流式截断",
}

function formatRemainingDuration(ms: number): string {
  const sec = Math.max(0, Math.floor(ms / 1000))
  if (sec < 60) return "<1m"
  const mins = Math.ceil(sec / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  const remMins = mins % 60
  return remMins > 0 ? `${hours}h${remMins}m` : `${hours}h`
}

function getActiveBackoffs(
  cfgs: ProbeDetectorConfig[],
  nowMs: number,
): ActiveBackoffItem[] {
  const out: ActiveBackoffItem[] = []
  for (const cfg of cfgs) {
    const errs = cfg.consecutive_errors ?? 0
    if (!cfg.enabled || errs <= 0 || !cfg.backoff_until) continue
    const untilMs = Date.parse(cfg.backoff_until)
    if (!Number.isFinite(untilMs) || untilMs <= nowMs) continue
    out.push({
      detectorKey: cfg.detector_key,
      detectorLabel: DETECTOR_SHORT_LABELS[cfg.detector_key] ?? cfg.detector_key,
      reasonLabel:
        ERROR_CLASS_LABELS[(cfg.last_error_class ?? "").toLowerCase()] ??
        (cfg.last_error_class || "连续错误"),
      consecutiveErrors: errs,
      remainingText: formatRemainingDuration(untilMs - nowMs),
      untilText: new Date(untilMs).toLocaleTimeString(),
    })
  }
  return out
}

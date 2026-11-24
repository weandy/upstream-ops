import { useEffect, useMemo, useState } from "react"
import {
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  FileText,
  RefreshCw,
  Send,
} from "lucide-react"
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { decimal, formatDurationMS } from "@/lib/format"
import type {
  SiteOpsDailyReportPreview,
  SiteOpsDailyReportSendResult,
  SiteOpsDeliveryStatus,
  SiteOpsEventDelivery,
  SiteOpsEventItem,
  SiteOpsEventSeverity,
  SiteOpsEventsResponse,
} from "@/lib/site-ops-types"

export interface EventsTimelinePanelProps {
  instanceId: number
  instanceName: string
  data?: SiteOpsEventsResponse
  loading: boolean
  error?: string
  onReload: () => void
  onPreviewReport: () => Promise<SiteOpsDailyReportPreview>
  onSendReport: () => Promise<SiteOpsDailyReportSendResult>
}

type StateFilter = "all" | "firing" | "resolved"
type SeverityFilter = "all" | "critical" | "warning" | "info"

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

function SeverityBadge({ severity }: { severity: SiteOpsEventSeverity }) {
  switch (severity) {
    case "critical":
      return <Badge variant="destructive">严重</Badge>
    case "warning":
      return (
        <Badge
          variant="outline"
          className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"
        >
          预警
        </Badge>
      )
    case "info":
      return (
        <Badge
          variant="outline"
          className="border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300"
        >
          提示
        </Badge>
      )
    default:
      return <Badge variant="outline">{severity || "未知"}</Badge>
  }
}

function EventStateBadge({ state }: { state: string }) {
  if (state === "firing") {
    return (
      <Badge
        variant="outline"
        className="border-destructive/40 bg-destructive/10 text-destructive"
      >
        <span aria-hidden="true" className="size-1.5 rounded-full bg-destructive" />
        告警中
      </Badge>
    )
  }
  if (state === "resolved") {
    return (
      <Badge
        variant="outline"
        className="border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
      >
        <span aria-hidden="true" className="size-1.5 rounded-full bg-emerald-500" />
        已恢复
      </Badge>
    )
  }
  return <Badge variant="outline">{state || "未知"}</Badge>
}

function deliveryKindLabel(kind: string): string {
  switch (kind) {
    case "alert":
      return "告警"
    case "recovery":
      return "恢复"
    case "daily_report":
      return "日报"
    default:
      return kind || "通知"
  }
}

function DeliveryStatusBadge({ status }: { status: SiteOpsDeliveryStatus }) {
  switch (status) {
    case "sent":
      return (
        <Badge
          variant="outline"
          className="border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
        >
          已送达
        </Badge>
      )
    case "pending":
      return (
        <Badge
          variant="outline"
          className="border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300"
        >
          待发送
        </Badge>
      )
    case "failed":
      return <Badge variant="destructive">失败待重试</Badge>
    case "skipped_cooldown":
      return (
        <Badge
          variant="outline"
          className="border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300"
        >
          冷却跳过
        </Badge>
      )
    default:
      return <Badge variant="outline">{status || "未知"}</Badge>
  }
}

function DeliveryRow({ delivery }: { delivery: SiteOpsEventDelivery }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded border border-border/70 bg-muted/20 px-2.5 py-1.5 text-xs">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="font-medium">
          {delivery.channel_name || `渠道 #${delivery.notify_channel_id}`}
        </span>
        {delivery.channel_type ? (
          <Badge variant="secondary" className="text-[10px]">
            {delivery.channel_type}
          </Badge>
        ) : null}
        <Badge variant="outline" className="text-[10px]">
          {deliveryKindLabel(delivery.delivery_kind)}
        </Badge>
        <DeliveryStatusBadge status={delivery.status} />
        <span className="text-muted-foreground tabular-nums">
          尝试 {delivery.attempts} 次
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground">
        {delivery.sent_at ? (
          <span className="tabular-nums">送达 {formatUTC8DateTime(delivery.sent_at)}</span>
        ) : null}
        {delivery.next_retry_at ? (
          <span className="tabular-nums">下次重试 {formatUTC8DateTime(delivery.next_retry_at)}</span>
        ) : null}
        {delivery.last_error ? (
          <span
            className="max-w-72 truncate text-destructive"
            title={delivery.last_error}
          >
            {delivery.last_error}
          </span>
        ) : null}
      </div>
    </div>
  )
}

function EventCard({ event }: { event: SiteOpsEventItem }) {
  const deliveries = event.deliveries ?? []
  const [expanded, setExpanded] = useState(deliveries.length > 0 && deliveries.length <= 3)

  return (
    <article className="space-y-2.5 rounded-lg border bg-card p-3.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-1.5">
          <SeverityBadge severity={event.severity} />
          <EventStateBadge state={event.state} />
          {event.resource_key ? (
            <span className="rounded border px-1.5 py-0.5 text-xs text-muted-foreground">
              {event.resource_key}
            </span>
          ) : null}
          {event.consecutive_hits > 1 ? (
            <Badge variant="secondary" className="text-[11px] tabular-nums">
              连续命中 {event.consecutive_hits} 次
            </Badge>
          ) : null}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground tabular-nums">
          <span>触发 {formatUTC8DateTime(event.fired_at)}</span>
          {event.resolved_at ? (
            <span>恢复 {formatUTC8DateTime(event.resolved_at)}</span>
          ) : null}
        </div>
      </div>

      <div className="space-y-1">
        <h3 className="text-sm font-medium">{event.title}</h3>
        {event.summary ? (
          <p className="text-xs leading-relaxed text-muted-foreground">{event.summary}</p>
        ) : null}
      </div>

      {event.required_domains && event.required_domains.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
          <span className="text-muted-foreground">依赖数据域：</span>
          {event.required_domains.map((domain) => (
            <Badge key={domain} variant="outline" className="font-mono text-[10px]">
              {domain}
            </Badge>
          ))}
        </div>
      ) : null}

      <div className="border-t pt-2">
        {deliveries.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            无渠道投递记录（未命中已启用自有站点运维订阅的通知渠道）
          </p>
        ) : (
          <div className="space-y-1.5">
            <button
              type="button"
              onClick={() => setExpanded((prev) => !prev)}
              className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground"
            >
              {expanded ? (
                <ChevronDown className="size-3.5" />
              ) : (
                <ChevronRight className="size-3.5" />
              )}
              投递明细（{deliveries.length} 条）
            </button>
            {expanded && (
              <div className="space-y-1.5">
                {deliveries.map((delivery) => (
                  <DeliveryRow key={delivery.id} delivery={delivery} />
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </article>
  )
}

export function EventsTimelinePanel({
  instanceId,
  instanceName,
  data,
  loading,
  error,
  onReload,
  onPreviewReport,
  onSendReport,
}: EventsTimelinePanelProps) {
  const [stateFilter, setStateFilter] = useState<StateFilter>("all")
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all")

  const [reportOpen, setReportOpen] = useState(false)
  const [reportLoading, setReportLoading] = useState(false)
  const [reportError, setReportError] = useState<string | null>(null)
  const [reportPreview, setReportPreview] = useState<SiteOpsDailyReportPreview | null>(null)
  const [sendingReport, setSendingReport] = useState(false)
  const [sendError, setSendError] = useState<string | null>(null)
  const [sendResult, setSendResult] = useState<SiteOpsDailyReportSendResult | null>(null)
  const [copied, setCopied] = useState(false)

  // 切换实例时重置日报弹窗状态，避免跨实例串台
  useEffect(() => {
    setReportOpen(false)
    setReportLoading(false)
    setReportError(null)
    setReportPreview(null)
    setSendingReport(false)
    setSendError(null)
    setSendResult(null)
    setCopied(false)
  }, [instanceId])

  const items = useMemo(() => data?.items ?? [], [data?.items])
  const firingCount = data?.firing_count ?? 0

  const filteredItems = useMemo(() => {
    return items.filter((item) => {
      if (stateFilter !== "all" && item.state !== stateFilter) return false
      if (severityFilter !== "all" && item.severity !== severityFilter) return false
      return true
    })
  }, [items, stateFilter, severityFilter])

  async function handleOpenReportPreview() {
    setReportOpen(true)
    setReportLoading(true)
    setReportError(null)
    setSendError(null)
    setSendResult(null)
    setCopied(false)
    try {
      const preview = await onPreviewReport()
      setReportPreview(preview)
    } catch (err) {
      const message = err instanceof Error ? err.message : "加载运维日报预览失败，请重试。"
      setReportError(message || "加载运维日报预览失败，请重试。")
    } finally {
      setReportLoading(false)
    }
  }

  async function handleCopyMarkdown() {
    if (!reportPreview) return
    const content = reportPreview.subject
      ? `# ${reportPreview.subject}\n\n${reportPreview.body}`
      : reportPreview.body
    try {
      await navigator.clipboard.writeText(content)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      setSendError("复制到剪贴板失败，请手动选中正文复制。")
    }
  }

  async function handleSendReport() {
    if (sendingReport) return
    setSendingReport(true)
    setSendError(null)
    try {
      const result = await onSendReport()
      setSendResult(result)
      if (result.preview) {
        setReportPreview(result.preview)
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : "推送运维日报失败，请重试。"
      setSendError(message || "推送运维日报失败，请重试。")
    } finally {
      setSendingReport(false)
    }
  }

  const metrics = reportPreview?.metrics

  return (
    <section className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-card px-3 py-2">
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex flex-wrap gap-1">
            {(
              [
                ["all", `全部 ${items.length}`],
                ["firing", `告警中 ${firingCount}`],
                ["resolved", `已恢复 ${Math.max(0, items.length - firingCount)}`],
              ] as const
            ).map(([val, label]) => (
              <Button
                key={val}
                size="sm"
                variant={stateFilter === val ? "secondary" : "outline"}
                className="h-7 rounded-full px-2.5 text-xs tabular-nums"
                onClick={() => setStateFilter(val)}
              >
                {label}
              </Button>
            ))}
          </div>

          <Select
            value={severityFilter}
            onValueChange={(value) => setSeverityFilter(value as SeverityFilter)}
          >
            <SelectTrigger className="h-8 w-28 text-xs" aria-label="按告警级别筛选">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部级别</SelectItem>
              <SelectItem value="critical">严重</SelectItem>
              <SelectItem value="warning">预警</SelectItem>
              <SelectItem value="info">提示</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" className="h-8 text-xs" onClick={onReload} disabled={loading}>
            <RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} />
            刷新
          </Button>

          <Button size="sm" className="h-8 text-xs" onClick={() => { void handleOpenReportPreview() }}>
            <FileText className="size-3.5" />
            预览昨日日报
          </Button>
        </div>
      </div>

      {error && (
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-destructive/30 bg-destructive/5 p-3">
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
          <Button size="sm" variant="outline" onClick={onReload} disabled={loading}>
            <RefreshCw className={loading ? "animate-spin" : undefined} />
            重试
          </Button>
        </div>
      )}

      {!data && loading ? (
        <p role="status" className="text-sm text-muted-foreground">
          正在加载事件时间轴…
        </p>
      ) : filteredItems.length === 0 ? (
        <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
          {items.length === 0
            ? "当前站点暂无状态跃迁或告警事件。"
            : "没有符合当前筛选条件的事件。"}
        </div>
      ) : (
        <div className="space-y-2.5">
          {filteredItems.map((event) => (
            <EventCard key={event.id} event={event} />
          ))}
        </div>
      )}

      {/* 运维日报预览与推送弹窗 */}
      <Dialog open={reportOpen} onOpenChange={setReportOpen}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>
              运维日报预览 · {reportPreview?.instance_name || instanceName}
            </DialogTitle>
            <DialogDescription>
              {reportPreview
                ? `统计日（UTC+8）：${reportPreview.date_utc8} · 生成于 ${formatUTC8DateTime(reportPreview.generated_at)}`
                : "正在生成昨日 24 小时同窗聚合运维简报…"}
            </DialogDescription>
          </DialogHeader>

          {reportLoading ? (
            <p role="status" className="py-6 text-center text-sm text-muted-foreground">
              正在汇总昨日 24h 指标并生成日报…
            </p>
          ) : reportError ? (
            <div className="space-y-3 rounded-md border border-destructive/30 bg-destructive/5 p-4">
              <p role="alert" className="text-sm text-destructive">
                {reportError}
              </p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => { void handleOpenReportPreview() }}
              >
                <RefreshCw />
                重新生成预览
              </Button>
            </div>
          ) : reportPreview && metrics ? (
            <div className="space-y-4">
              {/* 昨日 24h 指标卡片 */}
              <div className="space-y-2">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>
                    统计窗口（UTC+8）：{formatUTC8DateTime(metrics.window_start_utc8)} ~{" "}
                    {formatUTC8DateTime(metrics.window_end_utc8)}
                  </span>
                  <span className="tabular-nums">
                    可达样本 {metrics.reachable_sample_count} / {metrics.sample_count}
                  </span>
                </div>

                <div className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-3">
                  <div className="space-y-1 rounded-md border bg-card p-3">
                    <p className="text-xs text-muted-foreground">24h 加权 SLA</p>
                    <p className="text-base font-semibold tabular-nums">
                      {metrics.sla_percent_24h != null && Number.isFinite(metrics.sla_percent_24h) ? (
                        `${decimal(metrics.sla_percent_24h)}%`
                      ) : (
                        <span className="text-amber-700 dark:text-amber-300">缺失</span>
                      )}
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      {metrics.sla_note || "按 24h 有效请求量加权计算"}
                    </p>
                    {metrics.req_total_24h != null ? (
                      <p className="text-[11px] text-muted-foreground tabular-nums">
                        请求 {metrics.req_total_24h} / 错误 {metrics.err_total_24h ?? 0}
                      </p>
                    ) : null}
                  </div>

                  <div className="space-y-1 rounded-md border bg-card p-3">
                    <p className="text-xs text-muted-foreground">24h P95 延迟</p>
                    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm font-semibold tabular-nums">
                      <span>
                        TTFT{" "}
                        {metrics.ttft_p95_ms_24h != null ? (
                          formatDurationMS(metrics.ttft_p95_ms_24h)
                        ) : (
                          <span className="text-amber-700 dark:text-amber-300">缺失</span>
                        )}
                      </span>
                      <span>
                        总耗时{" "}
                        {metrics.duration_p95_ms_24h != null ? (
                          formatDurationMS(metrics.duration_p95_ms_24h)
                        ) : (
                          <span className="text-amber-700 dark:text-amber-300">缺失</span>
                        )}
                      </span>
                    </div>
                    <p className="text-[11px] text-muted-foreground">
                      {metrics.latency_24h_note || "无 24h 原始分位数据时显式标缺失"}
                    </p>
                  </div>

                  <div className="space-y-1 rounded-md border bg-card p-3">
                    <p className="text-xs text-muted-foreground">峰值并发</p>
                    <p className="text-base font-semibold tabular-nums">
                      {metrics.peak_conc_used != null || metrics.peak_conc_max != null ? (
                        <>
                          {metrics.peak_conc_used ?? "缺失"} / {metrics.peak_conc_max ?? "缺失"}
                        </>
                      ) : (
                        <span className="text-amber-700 dark:text-amber-300">缺失</span>
                      )}
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      当前号池：
                      {metrics.current_accounts_total != null
                        ? `总 ${metrics.current_accounts_total} / 报错 ${metrics.current_accounts_error ?? 0}`
                        : "缺失"}
                    </p>
                  </div>

                  <div className="space-y-1 rounded-md border bg-card p-3">
                    <p className="text-xs text-muted-foreground">切号统计</p>
                    <p className="text-base font-semibold tabular-nums">
                      累计 {metrics.total_switches_1m} 次
                    </p>
                    <p className="text-[11px] text-muted-foreground tabular-nums">
                      单分钟峰值 {metrics.peak_switches_1m} 次
                    </p>
                  </div>

                  <div className="space-y-1 rounded-md border bg-card p-3">
                    <p className="text-xs text-muted-foreground">当前活跃告警</p>
                    <p
                      className={
                        metrics.active_firing_alerts > 0
                          ? "text-base font-semibold text-destructive tabular-nums"
                          : "text-base font-semibold tabular-nums"
                      }
                    >
                      {metrics.active_firing_alerts} 条
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      统计时刻仍处于 firing 状态的事件
                    </p>
                  </div>
                </div>
              </div>

              {/* Markdown 正文 */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <p className="text-xs font-medium text-muted-foreground">
                    日报主题与 Markdown 正文
                  </p>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    className="h-7 text-xs"
                    onClick={() => { void handleCopyMarkdown() }}
                  >
                    {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                    {copied ? "已复制" : "复制 Markdown"}
                  </Button>
                </div>
                {reportPreview.subject ? (
                  <div className="rounded-md border bg-muted/40 px-3 py-2 text-xs font-medium">
                    {reportPreview.subject}
                  </div>
                ) : null}
                <pre className="max-h-72 overflow-y-auto rounded-md border bg-muted/20 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-words">
                  {reportPreview.body}
                </pre>
              </div>

              {/* 推送结果 */}
              {sendError ? (
                <p role="alert" className="text-xs text-destructive">
                  {sendError}
                </p>
              ) : null}

              {sendResult ? (
                <div className="space-y-2 rounded-md border bg-muted/20 p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                    <span className="font-medium">推送结果</span>
                    <span className="text-muted-foreground tabular-nums">
                      命中订阅渠道 {sendResult.matched_targets} 个
                    </span>
                  </div>
                  {sendResult.matched_targets === 0 ? (
                    <p className="text-xs text-muted-foreground">
                      未命中任何通知渠道。请先在通知渠道中开启「自有站点运维订阅」并勾选该站点与「每日运维简报 (daily_report)」。
                    </p>
                  ) : (
                    <div className="space-y-1.5">
                      {(sendResult.deliveries ?? []).map((delivery) => (
                        <DeliveryRow key={`${delivery.id}-${delivery.notify_channel_id}`} delivery={delivery} />
                      ))}
                    </div>
                  )}
                </div>
              ) : null}
            </div>
          ) : null}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setReportOpen(false)}
              disabled={sendingReport}
            >
              关闭
            </Button>
            {reportPreview && (
              <Button
                type="button"
                onClick={() => { void handleSendReport() }}
                disabled={sendingReport || reportLoading}
              >
                <Send className="size-3.5" />
                {sendingReport ? "推送中…" : "立即推送到订阅渠道"}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}

import type { ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { Progress } from "@/components/ui/progress"
import { decimal, formatDurationMS, fullDateTime } from "@/lib/format"
import type { SiteOpsFieldQualityState, SiteOpsOverviewResponse } from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote, MetricValue } from "./quality-badge"

function Stat({ label, note, children }: { label: string; note?: string; children: ReactNode }) {
  return (
    <div className="space-y-2 rounded-md border bg-card p-3">
      <h3 className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        {label}
        {note && <MetricNote label={`${label}口径`}>{note}</MetricNote>}
      </h3>
      <div className="flex flex-wrap items-center gap-2 text-sm font-medium">{children}</div>
    </div>
  )
}

function percentBar(value?: number | null, max = 100) {
  if (value == null || !Number.isFinite(value) || value < 0 || !Number.isFinite(max) || max <= 0) return null
  return Math.min(100, value / max * 100)
}

function UsageBar({ label, value, quality }: {
  label: string
  value: number | null
  quality: SiteOpsFieldQualityState
}) {
  return quality === "trusted" && value !== null
    ? <Progress aria-label={label} value={value} className="h-1.5" />
    : null
}

function formatUptime(seconds: number) {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds / 3600) % 24
  const minutes = Math.floor(seconds / 60) % 60
  return days > 0 ? `${days} 天 ${hours} 小时`
    : hours > 0 ? `${hours} 小时 ${minutes} 分钟`
      : minutes > 0 ? `${minutes} 分钟` : `${seconds} 秒`
}

function ServiceState({ ok, quality }: { ok?: boolean | null; quality: SiteOpsFieldQualityState }) {
  if (quality !== "trusted" || ok == null) return <FieldQualityBadge quality={quality === "trusted" ? "unavailable" : quality} />
  return <Badge variant={ok ? "outline" : "destructive"}>{ok ? "正常" : "异常"}</Badge>
}

export function OverviewPanel({ overview }: { overview: SiteOpsOverviewResponse }) {
  const snapshot = overview.latest
  if (!snapshot) return <p className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground">暂无实时采样。</p>

  const ops = snapshot.field_quality?.ops_overview ?? "unavailable"
  const infra = snapshot.field_quality?.infra_metrics ?? "unavailable"
  const availability = snapshot.field_quality?.account_availability ?? "unavailable"
  const conc = snapshot.conc_trust_state || snapshot.field_quality?.concurrency || "unavailable"
  const switchRate = infra === "trusted" && ops === "trusted" &&
    snapshot.estimated_switch_pct_1m != null && Number.isFinite(snapshot.estimated_switch_pct_1m)
    ? `${decimal(snapshot.estimated_switch_pct_1m)}%` : "—"
  const redisActive = snapshot.redis_conn_total != null && snapshot.redis_conn_idle != null &&
    snapshot.redis_conn_total >= snapshot.redis_conn_idle
    ? snapshot.redis_conn_total - snapshot.redis_conn_idle : null
  const heartbeats = snapshot.job_heartbeats ?? []

  return (
    <div className="space-y-5">
      <section className="space-y-2.5">
        <h2 className="text-sm font-semibold">实时 SRE 总览</h2>
        <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-4">
          <Stat label={snapshot.uptime_seconds != null ? "健康分与 Uptime" : "综合健康分"}>
            <MetricValue value={snapshot.health_score} quality={ops} unit=" 分" />
            {snapshot.uptime_seconds != null && (
              <MetricValue value={snapshot.uptime_seconds} quality={ops} format={formatUptime} />
            )}
          </Stat>
          <Stat label="近 1 小时 SLA" note={overview.sla_denominator_note}>
            <MetricValue value={snapshot.sla_percent_1h} quality={ops} unit="%" />
          </Stat>
          <Stat label="近 1 小时错误率">
            <span>整体 <MetricValue value={snapshot.error_rate_percent_1h} quality={ops} unit="%" /></span>
            <span>上游 <MetricValue value={snapshot.upstream_error_rate_percent_1h} quality={ops} unit="%" /></span>
            <div className="flex w-full flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span>429 <MetricValue value={snapshot.upstream_429_1h} quality={ops} /></span>
              <span>529 <MetricValue value={snapshot.upstream_529_1h} quality={ops} /></span>
              <span>其他上游错误 <MetricValue value={snapshot.upstream_err_excl_429_529_1h} quality={ops} /></span>
            </div>
          </Stat>
          <Stat label="近 1 分钟切号次数" note={`近 1m 估算切号率 ${switchRate}。使用当前 QPS × 60 估算同分钟请求量，低流量或缺少可信数据时留空。`}>
            <MetricValue value={snapshot.account_switch_count_1m} quality={infra} unit=" 次" />
          </Stat>
          <Stat label="实时请求与生成速率">
            <span>QPS <MetricValue value={snapshot.qps_current} quality={ops} /></span>
            <span>TPS <MetricValue value={snapshot.tps_current} quality={ops} /></span>
          </Stat>
          <Stat label="P95 延迟">
            <span>首字（TTFT） <MetricValue value={snapshot.ttft_p95_ms} quality={ops} format={formatDurationMS} /></span>
            <span>总耗时 <MetricValue value={snapshot.duration_p95_ms} quality={ops} format={formatDurationMS} /></span>
          </Stat>
          <Stat label="去重真实并发" note="全站按实例与账号去重。">
            {conc === "trusted" ? (
              <>
                <span>使用 <MetricValue value={snapshot.conc_used} /></span>
                <span>上限 <MetricValue value={snapshot.conc_max} /></span>
                <span>等待 <MetricValue value={snapshot.conc_waiting} /></span>
              </>
            ) : <FieldQualityBadge quality={conc} reason={snapshot.conc_trust_reason} />}
          </Stat>
          <Stat label="去重 Ops 可用号数" note="按实例与账号去重。Ops 口径不等同于调度器 IsSchedulable()。">
            <span>可用 <MetricValue value={snapshot.accounts_ops_available} quality={availability} /></span>
            <span>总数 <MetricValue value={snapshot.accounts_total} quality={availability} /></span>
          </Stat>
        </div>
      </section>
      <section className="space-y-2.5">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 className="text-sm font-semibold">主机与中间件</h2>
          <p className="text-xs text-muted-foreground">
            {overview.system_metrics_resolution || "system_metrics 原始采集分辨率为 60s"} · 采集时间 {fullDateTime(snapshot.system_metrics_at)}
          </p>
        </div>
        <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-4">
          <Stat label="CPU">
            <MetricValue value={snapshot.cpu_percent} quality={infra} unit="%" />
            <UsageBar label="CPU 使用率" value={percentBar(snapshot.cpu_percent)} quality={infra} />
          </Stat>
          <Stat label="内存">
            <MetricValue value={snapshot.mem_percent} quality={infra} unit="%" />
            <MetricValue value={snapshot.mem_used_mb} quality={infra} unit=" MB" />
            <UsageBar label="内存使用率" value={percentBar(snapshot.mem_percent)} quality={infra} />
          </Stat>
          <Stat label="DB 连接池">
            <ServiceState ok={snapshot.db_ok} quality={infra} />
            <span>活跃 <MetricValue value={snapshot.db_conn_active} quality={infra} /></span>
            <span>等待 <MetricValue value={snapshot.db_conn_waiting} quality={infra} /></span>
            <span>上限 <MetricValue value={snapshot.db_max_open_conns} quality={infra} /></span>
            <UsageBar label="DB 连接池使用率" value={snapshot.db_max_open_conns == null ? null : percentBar(snapshot.db_conn_active, snapshot.db_max_open_conns)} quality={infra} />
          </Stat>
          <Stat label="Redis 连接池">
            <ServiceState ok={snapshot.redis_ok} quality={infra} />
            <span>使用 <MetricValue value={redisActive} quality={infra} /></span>
            <span>空闲 <MetricValue value={snapshot.redis_conn_idle} quality={infra} /></span>
            <span>上限 <MetricValue value={snapshot.redis_pool_size} quality={infra} /></span>
            <UsageBar label="Redis 连接池使用率" value={snapshot.redis_pool_size == null ? null : percentBar(redisActive, snapshot.redis_pool_size)} quality={infra} />
          </Stat>
          <Stat label="Goroutine"><MetricValue value={snapshot.goroutine_count} quality={infra} /></Stat>
          <Stat label="排队深度"><MetricValue value={snapshot.queue_depth} quality={infra} /></Stat>
          <div className="sm:col-span-2">
            <Stat label="后台任务心跳">
              {ops !== "trusted" || heartbeats.length === 0 ? (
                <FieldQualityBadge quality={ops === "trusted" ? "unavailable" : ops} />
              ) : heartbeats.map((job) => (
                <Badge key={job.job_name} variant={job.has_error ? "destructive" : "outline"} title={
                  `最近运行 ${fullDateTime(job.last_run_at)}；最近成功 ${fullDateTime(job.last_success_at)}${job.error_summary ? `；${job.error_summary}` : ""}`
                }>
                  {job.job_name} · {job.has_error || job.status === "error" ? "异常" : job.status === "ok" ? "正常" : "状态未确认"}
                </Badge>
              ))}
            </Stat>
          </div>
        </div>
      </section>
    </div>
  )
}

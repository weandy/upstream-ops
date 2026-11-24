import type { ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { decimal, formatDurationMS, fullDateTime } from "@/lib/format"
import type { SiteOpsFieldQualityState, SiteOpsOverviewResponse } from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote, MetricValue } from "./quality-badge"

function KpiCard({
  label,
  note,
  value,
  sub,
}: {
  label: string
  note?: string
  value: ReactNode
  sub?: ReactNode
}) {
  return (
    <div className="space-y-1 rounded-lg border bg-card px-3.5 py-2.5">
      <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
        <span>{label}</span>
        {note && <MetricNote label={label}>{note}</MetricNote>}
      </div>
      <div className="text-lg font-bold tabular-nums">{value}</div>
      {sub && <div className="text-[11px] text-muted-foreground tabular-nums">{sub}</div>}
    </div>
  )
}

function formatUptime(seconds: number) {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds / 3600) % 24
  const minutes = Math.floor(seconds / 60) % 60
  return days > 0
    ? `${days}天 ${hours}小时`
    : hours > 0
      ? `${hours}小时 ${minutes}分`
      : minutes > 0
        ? `${minutes}分钟`
        : `${seconds}秒`
}

export function OverviewPanel({ overview }: { overview: SiteOpsOverviewResponse }) {
  const snapshot = overview.latest
  if (!snapshot) {
    return (
      <p className="rounded-lg border border-dashed p-5 text-sm text-muted-foreground">
        暂无实时采样数据。
      </p>
    )
  }

  const ops: SiteOpsFieldQualityState = snapshot.field_quality?.ops_overview ?? "unavailable"
  const infra: SiteOpsFieldQualityState = snapshot.field_quality?.infra_metrics ?? "unavailable"
  const availability: SiteOpsFieldQualityState =
    snapshot.field_quality?.account_availability ?? "unavailable"
  const conc: SiteOpsFieldQualityState =
    snapshot.conc_trust_state || snapshot.field_quality?.concurrency || "unavailable"

  const redisActive =
    snapshot.redis_conn_total != null &&
    snapshot.redis_conn_idle != null &&
    snapshot.redis_conn_total >= snapshot.redis_conn_idle
      ? snapshot.redis_conn_total - snapshot.redis_conn_idle
      : null
  const heartbeats = snapshot.job_heartbeats ?? []
  const okJobs = heartbeats.filter((j) => !j.has_error && j.status === "ok").length
  const abnormalAccounts =
    (snapshot.accounts_error ?? 0) +
    (snapshot.accounts_rate_limited ?? 0) +
    (snapshot.accounts_overloaded ?? 0)

  return (
    <div className="space-y-3">
      {/* 4 个核心运行指标 */}
      <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
        <KpiCard
          label="综合健康分"
          value={<MetricValue value={snapshot.health_score} quality={ops} />}
          sub={
            <span>
              可用账号{" "}
              <MetricValue value={snapshot.accounts_ops_available} quality={availability} /> /{" "}
              <MetricValue value={snapshot.accounts_total} quality={availability} />
              {abnormalAccounts > 0 ? ` · 异常 ${abnormalAccounts}` : ""}
              {snapshot.uptime_seconds != null
                ? ` · 运行 ${formatUptime(snapshot.uptime_seconds)}`
                : ""}
            </span>
          }
        />

        <KpiCard
          label="近 1 小时可用率"
          note={overview.sla_denominator_note}
          value={
            <span
              className={
                ops === "trusted" && (snapshot.sla_percent_1h ?? 0) >= 99
                  ? "text-emerald-600 dark:text-emerald-400"
                  : undefined
              }
            >
              <MetricValue value={snapshot.sla_percent_1h} quality={ops} unit="%" />
            </span>
          }
          sub={
            <span>
              请求 <MetricValue value={snapshot.req_total_1h} quality={ops} /> · 错误{" "}
              <MetricValue value={snapshot.err_total_1h} quality={ops} />
              {(snapshot.upstream_429_1h ?? 0) > 0 ? ` · 429 ${snapshot.upstream_429_1h}` : ""}
              {(snapshot.upstream_529_1h ?? 0) > 0 ? ` · 529 ${snapshot.upstream_529_1h}` : ""}
            </span>
          }
        />

        <KpiCard
          label="P95 响应耗时"
          value={
            <MetricValue
              value={snapshot.duration_p95_ms}
              quality={ops}
              format={formatDurationMS}
            />
          }
          sub={
            <span>
              首字{" "}
              <MetricValue value={snapshot.ttft_p95_ms} quality={ops} format={formatDurationMS} /> ·
              近1分切号 <MetricValue value={snapshot.account_switch_count_1m} quality={infra} />
            </span>
          }
        />

        <KpiCard
          label="全站实时并发"
          note="全站按实例与账号去重统计"
          value={
            conc === "trusted" ? (
              <span>
                <MetricValue value={snapshot.conc_used} /> / <MetricValue value={snapshot.conc_max} />
              </span>
            ) : (
              <FieldQualityBadge quality={conc} reason={snapshot.conc_trust_reason} />
            )
          }
          sub={
            conc === "trusted" ? (
              <span>
                负载{" "}
                {snapshot.conc_load_percent != null
                  ? `${decimal(snapshot.conc_load_percent, 1)}%`
                  : "—"}
                {" · "}排队 <MetricValue value={snapshot.conc_waiting} />
                {snapshot.qps_current != null ? ` · QPS ${decimal(snapshot.qps_current, 1)}` : ""}
              </span>
            ) : (
              <span>等待可信并发采样</span>
            )
          }
        />
      </div>

      {/* 紧凑服务器与中间件资源条 */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border bg-card px-3.5 py-2 text-xs tabular-nums">
        <span className="inline-flex items-center gap-1.5">
          <span className="text-muted-foreground">CPU</span>
          <strong className="font-semibold">
            <MetricValue value={snapshot.cpu_percent} quality={infra} unit="%" />
          </strong>
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="text-muted-foreground">内存</span>
          <strong className="font-semibold">
            <MetricValue value={snapshot.mem_percent} quality={infra} unit="%" />
          </strong>
          {snapshot.mem_used_mb != null && infra === "trusted" ? (
            <span className="text-muted-foreground">· {snapshot.mem_used_mb} MB</span>
          ) : null}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span
            className={`size-2 rounded-full ${
              infra === "trusted" && snapshot.db_ok === true
                ? "bg-emerald-500"
                : infra === "trusted" && snapshot.db_ok === false
                  ? "bg-destructive"
                  : "bg-muted-foreground"
            }`}
          />
          <span className="text-muted-foreground">数据库</span>
          <span>
            <MetricValue value={snapshot.db_conn_active} quality={infra} /> /{" "}
            <MetricValue value={snapshot.db_max_open_conns} quality={infra} />
          </span>
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span
            className={`size-2 rounded-full ${
              infra === "trusted" && snapshot.redis_ok === true
                ? "bg-emerald-500"
                : infra === "trusted" && snapshot.redis_ok === false
                  ? "bg-destructive"
                  : "bg-muted-foreground"
            }`}
          />
          <span className="text-muted-foreground">Redis</span>
          <span>
            <MetricValue value={redisActive} quality={infra} /> /{" "}
            <MetricValue value={snapshot.redis_pool_size} quality={infra} />
          </span>
        </span>
        {heartbeats.length > 0 && ops === "trusted" && (
          <span className="inline-flex items-center gap-1.5">
            <span
              className={`size-2 rounded-full ${
                okJobs === heartbeats.length ? "bg-emerald-500" : "bg-amber-500"
              }`}
            />
            <span className="text-muted-foreground">后台任务</span>
            <span className="inline-flex flex-wrap items-center gap-1">
              {okJobs} / {heartbeats.length} 正常
              {okJobs < heartbeats.length &&
                heartbeats
                  .filter((j) => j.has_error || j.status !== "ok")
                  .map((j) => (
                    <Badge
                      key={j.job_name}
                      variant="destructive"
                      className="h-4 px-1 text-[10px]"
                      title={j.error_summary || `最近运行 ${fullDateTime(j.last_run_at)}`}
                    >
                      {j.job_name}
                    </Badge>
                  ))}
            </span>
          </span>
        )}
        <span className="ml-auto text-[11px] text-muted-foreground">
          采样于 {fullDateTime(snapshot.system_metrics_at || snapshot.observed_at)}
        </span>
      </div>
    </div>
  )
}

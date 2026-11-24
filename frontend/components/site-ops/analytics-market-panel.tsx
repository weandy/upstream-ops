import { useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { decimal, formatCompactNumber, money } from "@/lib/format"
import type { SiteOpsAnalyticsMarketResponse, SiteOpsAnalyticsRange } from "@/lib/site-ops-types"
import { nativePrice } from "./analytics-subscriptions-table"
import { CacheRate } from "./analytics-users-table"
import { MetricValue } from "./quality-badge"

export function AnalyticsMarketPanel({
  range,
  data,
  disabled,
  onRange,
  onPrice,
}: {
  range: SiteOpsAnalyticsRange
  data?: SiteOpsAnalyticsMarketResponse
  disabled: boolean
  onRange: (range: SiteOpsAnalyticsRange) => void
  onPrice: (groupId: number) => void
}) {
  const [hoverModel, setHoverModel] = useState<string | null>(null)
  const [hoverGroup, setHoverGroup] = useState<number | null>(null)

  const models = data?.models ?? []
  const groups = data?.groups ?? []
  const spread = models.some((m) => m.has_cost_spread)
  const maxModelUsd = Math.max(...models.map((m) => m.actual_usd ?? 0), 1)
  const maxGroupUsd = Math.max(...groups.map((g) => g.actual_usd ?? 0), 1)

  return (
    <section className="space-y-3 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-card px-3 py-2">
        <div className="flex flex-wrap items-center gap-1.5">
          {(["30d", "7d"] as const).map((r) => (
            <Button
              key={r}
              size="sm"
              className="h-7 rounded-full px-2.5 text-xs"
              aria-pressed={range === r}
              disabled={disabled}
              variant={range === r ? "secondary" : "outline"}
              onClick={() => onRange(r)}
            >
              近 {r === "7d" ? 7 : 30} 天
            </Button>
          ))}
          {data && (
            <span className="text-xs text-muted-foreground tabular-nums">
              {data.date_range.start_date} 至 {data.date_range.end_date}
            </span>
          )}
        </div>
        {data && (
          <span className="text-xs text-muted-foreground tabular-nums">
            总请求{" "}
            <MetricValue
              value={data.total_requests}
              format={formatCompactNumber}
              quality={data.domains.models?.quality}
            />{" "}
            · 总消耗 {money(data.total_actual_usd)}
          </span>
        )}
      </div>

      {data && (
        <>
          {/* 双横条图：模型消耗排行 & 分组消耗排行 */}
          <div className="grid gap-3 lg:grid-cols-2">
            <div className="space-y-2 rounded-lg border bg-card p-3.5">
              <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-semibold">模型消耗排行</h3>
                <span className="text-xs text-muted-foreground">
                  近 {range === "7d" ? 7 : 30} 天
                </span>
              </div>
              {models.length === 0 ? (
                <p className="rounded-md border border-dashed py-6 text-center text-xs text-muted-foreground">
                  暂无模型消耗记录
                </p>
              ) : (
                <div className="space-y-1.5">
                  {models.slice(0, 8).map((m) => {
                    const usd = m.actual_usd ?? 0
                    const w = usd > 0 ? Math.max(4, Math.round((usd / maxModelUsd) * 100)) : 0
                    const hi = hoverModel === m.model
                    return (
                      <div
                        key={m.model}
                        onMouseEnter={() => setHoverModel(m.model)}
                        onMouseLeave={() => setHoverModel(null)}
                        className={`space-y-1 rounded-md border px-2 py-1 transition-colors ${
                          hi
                            ? "border-primary/40 bg-primary/5"
                            : "border-transparent hover:bg-muted/40"
                        }`}
                      >
                        <div className="flex items-baseline justify-between gap-2 text-xs">
                          <span className="truncate font-mono font-medium">{m.model}</span>
                          <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                            <strong className="text-foreground">{money(m.actual_usd)}</strong>
                            {" · "}缓存{" "}
                            {m.cache.cache_read_hit_pct != null
                              ? `${decimal(m.cache.cache_read_hit_pct, 0)}%`
                              : "—"}
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

            <div className="space-y-2 rounded-lg border bg-card p-3.5">
              <div className="flex items-center justify-between gap-2">
                <h3 className="text-sm font-semibold">分组消耗排行</h3>
                <span className="text-xs text-muted-foreground">按账面消耗</span>
              </div>
              {groups.length === 0 ? (
                <p className="rounded-md border border-dashed py-6 text-center text-xs text-muted-foreground">
                  暂无分组消耗记录
                </p>
              ) : (
                <div className="space-y-1.5">
                  {groups.slice(0, 8).map((g) => {
                    const usd = g.actual_usd ?? 0
                    const w = usd > 0 ? Math.max(4, Math.round((usd / maxGroupUsd) * 100)) : 0
                    const hi = hoverGroup === g.group_id
                    return (
                      <div
                        key={g.group_id}
                        onMouseEnter={() => setHoverGroup(g.group_id)}
                        onMouseLeave={() => setHoverGroup(null)}
                        className={`space-y-1 rounded-md border px-2 py-1 transition-colors ${
                          hi
                            ? "border-primary/40 bg-primary/5"
                            : "border-transparent hover:bg-muted/40"
                        }`}
                      >
                        <div className="flex items-baseline justify-between gap-2 text-xs">
                          <span className="truncate font-medium">
                            {g.group_name || `分组 #${g.group_id}`}
                          </span>
                          <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                            <strong className="text-foreground">{money(g.actual_usd)}</strong>
                            {g.active_sub_count != null ? ` · ${g.active_sub_count} 份订阅` : ""}
                          </span>
                        </div>
                        <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full rounded-full bg-emerald-500"
                            style={{ width: `${w}%` }}
                          />
                        </div>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          </div>

          {/* 常驻全量模型明细 */}
          <div className="rounded-lg border bg-card">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3.5 py-2.5">
              <h3 className="text-sm font-semibold">模型明细</h3>
              <span className="text-xs text-muted-foreground tabular-nums">
                {(data.mismatches.total_requests ?? 0) > 0
                  ? `发现 ${data.mismatches.total_requests} 次上游路由模型不一致`
                  : "全部请求路由一致"}
              </span>
            </div>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>请求模型</TableHead>
                  <TableHead className="text-right">请求数</TableHead>
                  <TableHead className="text-right">消耗</TableHead>
                  <TableHead className="text-right">缓存命中</TableHead>
                  {spread && <TableHead className="text-right">账面与账号差额</TableHead>}
                  <TableHead>上游路由</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {models.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={spread ? 6 : 5}
                      className="py-6 text-center text-xs text-muted-foreground"
                    >
                      暂无模型记录
                    </TableCell>
                  </TableRow>
                ) : (
                  models.map((m, i) => (
                    <TableRow
                      key={`${m.model}:${i}`}
                      onMouseEnter={() => setHoverModel(m.model)}
                      onMouseLeave={() => setHoverModel(null)}
                      className={hoverModel === m.model ? "bg-primary/5" : undefined}
                    >
                      <TableCell className="font-mono text-xs font-medium">{m.model}</TableCell>
                      <TableCell className="text-right text-xs tabular-nums">
                        <MetricValue
                          value={m.requests}
                          format={formatCompactNumber}
                          quality={m.quality}
                        />
                      </TableCell>
                      <TableCell className="text-right text-xs font-semibold tabular-nums">
                        {money(m.actual_usd)}
                      </TableCell>
                      <TableCell className="text-right text-xs tabular-nums">
                        <CacheRate
                          value={m.cache.cache_read_hit_pct}
                          quality={m.cache.token_quality}
                        />
                      </TableCell>
                      {spread && (
                        <TableCell className="text-right text-xs tabular-nums">
                          {m.has_cost_spread && m.actual_usd != null && m.account_usd != null
                            ? money(m.actual_usd - m.account_usd)
                            : "—"}
                        </TableCell>
                      )}
                      <TableCell>
                        {m.has_mismatch ? (
                          <Badge
                            variant="outline"
                            className="border-amber-500/40 bg-amber-500/10 text-[11px] text-amber-700 dark:text-amber-300"
                          >
                            不一致 {m.mismatch_requests ?? "—"} 次
                          </Badge>
                        ) : (
                          <Badge
                            variant="outline"
                            className="border-emerald-500/35 bg-emerald-500/10 text-[11px] text-emerald-700 dark:text-emerald-300"
                          >
                            一致
                          </Badge>
                        )}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
            {(data.mismatches.rows?.length ?? 0) > 0 && (
              <div className="border-t px-3.5 py-2.5">
                <p className="mb-1.5 text-xs font-medium text-amber-700 dark:text-amber-300">
                  路由不一致明细
                </p>
                <div className="flex flex-wrap gap-2 text-xs">
                  {data.mismatches.rows?.map((r, idx) => (
                    <span
                      key={idx}
                      className="rounded border bg-muted/40 px-2 py-0.5 font-mono text-[11px] tabular-nums"
                    >
                      {r.requested_model} → {r.upstream_model || "—"} · {r.requests ?? "—"} 次 ·{" "}
                      {money(r.actual_usd)}
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* 常驻全量分组明细 */}
          <div className="rounded-lg border bg-card">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3.5 py-2.5">
              <h3 className="text-sm font-semibold">分组明细</h3>
              <span className="text-xs text-muted-foreground">按需维护分组默认月收款</span>
            </div>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>分组</TableHead>
                  <TableHead>平台</TableHead>
                  <TableHead className="text-right">请求数</TableHead>
                  <TableHead className="text-right">消耗</TableHead>
                  <TableHead className="text-right">生效订阅</TableHead>
                  <TableHead className="text-right">默认月收款</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groups.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={7} className="py-6 text-center text-xs text-muted-foreground">
                      暂无分组记录
                    </TableCell>
                  </TableRow>
                ) : (
                  groups.map((g) => (
                    <TableRow
                      key={g.group_id}
                      onMouseEnter={() => setHoverGroup(g.group_id)}
                      onMouseLeave={() => setHoverGroup(null)}
                      className={hoverGroup === g.group_id ? "bg-primary/5" : undefined}
                    >
                      <TableCell className="text-xs font-medium">
                        {g.group_name || `分组 #${g.group_id}`}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {g.platform || "—"}
                      </TableCell>
                      <TableCell className="text-right text-xs tabular-nums">
                        {g.requests != null ? formatCompactNumber(g.requests) : "—"}
                      </TableCell>
                      <TableCell className="text-right text-xs font-semibold tabular-nums">
                        {money(g.actual_usd)}
                      </TableCell>
                      <TableCell className="text-right text-xs tabular-nums">
                        <MetricValue
                          value={g.active_sub_count}
                          quality={data.domains.subscriptions?.quality}
                        />
                      </TableCell>
                      <TableCell className="text-right text-xs tabular-nums">
                        {nativePrice(g.reference_price)}
                        {g.ref_monthly_usd != null ? (
                          <span className="ml-1 text-muted-foreground">
                            · {money(g.ref_monthly_usd)}
                          </span>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="outline"
                          className="h-7 px-2 text-xs"
                          onClick={() => onPrice(g.group_id)}
                        >
                          设参考价
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </section>
  )
}

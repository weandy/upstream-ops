import { useMemo, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { fullDateTime } from "@/lib/format"
import type { SiteOpsGroupsResponse } from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote, MetricValue } from "./quality-badge"

const capacityNote = "账号跨组共享，各分组并发上限不可直接相加。"

export function GroupsMatrixPanel({ data }: { data: SiteOpsGroupsResponse }) {
  const [filter, setFilter] = useState("all")
  const [hoverId, setHoverId] = useState<number | null>(null)

  const groups = useMemo(() => data.groups ?? [], [data.groups])
  const quality = data.trust_state || "unavailable"

  const platforms = useMemo(() => {
    const set = new Set<string>()
    for (const g of groups) {
      if (g.platform) set.add(g.platform)
    }
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [groups])

  const filtered = useMemo(() => {
    return groups.filter((g) => {
      if (filter === "all") return true
      if (filter === "risk") return g.single_point_risk || (g.pool_exhaust_risk_count ?? 0) > 0
      return g.platform === filter
    })
  }, [groups, filter])

  return (
    <section className="space-y-3 rounded-lg border bg-card p-3.5">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-sm font-semibold">分组容量与负载</h2>
          <FieldQualityBadge quality={quality} reason={data.trust_reason} />
          <MetricNote label="并发容量口径">{capacityNote}</MetricNote>
          <span className="text-xs text-muted-foreground">
            观测 {fullDateTime(data.observed_at)}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-1">
          <Button
            size="sm"
            variant={filter === "all" ? "secondary" : "outline"}
            className="h-7 rounded-full px-2.5 text-xs"
            onClick={() => setFilter("all")}
          >
            全部分组
          </Button>
          {platforms.map((p) => (
            <Button
              key={p}
              size="sm"
              variant={filter === p ? "secondary" : "outline"}
              className="h-7 rounded-full px-2.5 text-xs"
              onClick={() => setFilter(p)}
            >
              {p}
            </Button>
          ))}
          <Button
            size="sm"
            variant={filter === "risk" ? "secondary" : "outline"}
            className="h-7 rounded-full px-2.5 text-xs"
            onClick={() => setFilter("risk")}
          >
            仅看风险
          </Button>
        </div>
      </header>

      {/* 上方分组负载进度条（随筛选实时变动） */}
      {filtered.length > 0 && (
        <div className="space-y-1.5">
          {filtered.map((g) => {
            const rowQuality = quality === "trusted" ? g.trust_state || "unavailable" : quality
            const pct =
              rowQuality === "trusted" && g.load_percent != null && Number.isFinite(g.load_percent)
                ? Math.min(100, Math.max(0, g.load_percent))
                : 0
            const hasRisk = g.single_point_risk || (g.pool_exhaust_risk_count ?? 0) > 0
            const hi = hoverId === g.group_id
            return (
              <div
                key={g.group_id}
                onMouseEnter={() => setHoverId(g.group_id)}
                onMouseLeave={() => setHoverId(null)}
                className={`space-y-1 rounded-md border px-2 py-1.5 transition-colors ${
                  hi
                    ? "border-primary/40 bg-primary/5"
                    : "border-transparent hover:bg-muted/40"
                }`}
              >
                <div className="flex flex-wrap items-baseline justify-between gap-2 text-xs">
                  <span className="font-medium">
                    {g.group_name}{" "}
                    <span className="text-[11px] font-normal text-muted-foreground tabular-nums">
                      可用 {g.accounts_ops_available}/{g.accounts_total}
                    </span>
                  </span>
                  <span
                    className={`text-[11px] tabular-nums ${
                      hasRisk ? "font-medium text-destructive" : "text-muted-foreground"
                    }`}
                  >
                    {g.single_point_risk
                      ? "仅剩 1 个可用号"
                      : rowQuality === "trusted"
                        ? `并发 ${g.conc_used ?? 0} / ${g.conc_max ?? 0}`
                        : "待核实"}
                  </span>
                </div>
                <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className={`h-full rounded-full ${hasRisk ? "bg-destructive" : "bg-primary"}`}
                    style={{ width: `${Math.max(pct > 0 ? 3 : 0, pct)}%` }}
                  />
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* 下方常驻分组明细表 */}
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>分组</TableHead>
              <TableHead>平台</TableHead>
              <TableHead className="text-right">可用 / 总数</TableHead>
              <TableHead className="text-right">并发使用 / 上限</TableHead>
              <TableHead>负载</TableHead>
              <TableHead>会话 / RPM</TableHead>
              <TableHead>风险</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-xs text-muted-foreground">
                  暂无匹配的分组记录
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((group) => {
                const rowQuality =
                  quality === "trusted" ? group.trust_state || "unavailable" : quality
                const { load_percent: load } = group
                const hasSessionsCap =
                  rowQuality === "trusted" &&
                  group.sessions_max != null &&
                  Number.isFinite(group.sessions_max) &&
                  group.sessions_max > 0
                const hasRpmCap =
                  rowQuality === "trusted" &&
                  group.rpm_max != null &&
                  Number.isFinite(group.rpm_max) &&
                  group.rpm_max > 0
                const exhaustRiskCount = group.pool_exhaust_risk_count ?? 0

                return (
                  <TableRow
                    key={group.group_id}
                    onMouseEnter={() => setHoverId(group.group_id)}
                    onMouseLeave={() => setHoverId(null)}
                    className={hoverId === group.group_id ? "bg-primary/5" : undefined}
                  >
                    <TableCell className="text-xs font-medium">{group.group_name}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {group.platform || "—"}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      <MetricValue value={group.accounts_ops_available} quality={rowQuality} /> /{" "}
                      <MetricValue value={group.accounts_total} quality={rowQuality} />
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums">
                      {rowQuality === "trusted" ? (
                        <span>
                          <MetricValue value={group.conc_used} /> /{" "}
                          <MetricValue value={group.conc_max} />
                          {(group.conc_waiting ?? 0) > 0 ? (
                            <span className="ml-1 text-amber-700 dark:text-amber-300">
                              · 排{group.conc_waiting}
                            </span>
                          ) : null}
                        </span>
                      ) : (
                        <FieldQualityBadge
                          quality={rowQuality}
                          reason={group.trust_reason || data.trust_reason}
                        />
                      )}
                    </TableCell>
                    <TableCell className="text-xs">
                      <div className="min-w-20 space-y-1">
                        <MetricValue
                          value={load}
                          quality={rowQuality}
                          reason={group.trust_reason || data.trust_reason}
                          unit="%"
                        />
                        {rowQuality === "trusted" &&
                          load != null &&
                          Number.isFinite(load) &&
                          load >= 0 && (
                            <Progress
                              className="h-1.5"
                              aria-label={`${group.group_name} 并发负载`}
                              value={Math.min(100, load)}
                            />
                          )}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground tabular-nums">
                      {hasSessionsCap || hasRpmCap ? (
                        <div className="flex flex-col gap-0.5">
                          {hasSessionsCap && (
                            <span>
                              会话 {group.sessions_used ?? "—"} / {group.sessions_max}
                            </span>
                          )}
                          {hasRpmCap && (
                            <span>
                              RPM {group.rpm_used ?? "—"} / {group.rpm_max}
                            </span>
                          )}
                        </div>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell>
                      {group.single_point_risk || exhaustRiskCount > 0 ? (
                        <div className="flex flex-wrap items-center gap-1">
                          {group.single_point_risk &&
                            (rowQuality === "trusted" ? (
                              <Badge variant="destructive" className="text-[11px]">
                                单点风险
                              </Badge>
                            ) : (
                              <Badge
                                variant="outline"
                                className="border-amber-500/40 text-[11px] text-amber-700 dark:text-amber-300"
                              >
                                待核实
                              </Badge>
                            ))}
                          {exhaustRiskCount > 0 && (
                            <Badge
                              variant="outline"
                              className="border-amber-500/40 bg-amber-500/10 text-[11px] text-amber-700 dark:text-amber-300"
                            >
                              预测高危 {exhaustRiskCount}
                            </Badge>
                          )}
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">正常</span>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}

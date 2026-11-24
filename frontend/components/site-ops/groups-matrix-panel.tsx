import { Badge } from "@/components/ui/badge"
import { Progress } from "@/components/ui/progress"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { fullDateTime } from "@/lib/format"
import type { SiteOpsGroupsResponse } from "@/lib/site-ops-types"
import { FieldQualityBadge, MetricNote, MetricValue } from "./quality-badge"

const availabilityNote = "Ops 判定可用与调度器 IsSchedulable() 口径不同。调度器还会检查账号过期与账号级配额。"
const capacityNote = "账号跨组共享，各分组容量不可直接相加。"

export function GroupsMatrixPanel({ data }: { data: SiteOpsGroupsResponse }) {
  const platforms = data.platform_breakdown ?? []
  const groups = data.groups ?? []
  const quality = data.trust_state || "unavailable"

  return (
    <section className="space-y-3">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h2 className="text-sm font-semibold">去重号池与分组容量</h2>
        <FieldQualityBadge quality={quality} reason={data.trust_reason} />
        <span className="text-xs text-muted-foreground">最近观测 {fullDateTime(data.observed_at)}</span>
      </header>
      <div className="space-y-2">
        <h3 className="text-xs text-muted-foreground">平台号池（按实例与账号去重）</h3>
        {platforms.length === 0 && <p className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">暂无平台号池记录。</p>}
        <div className="flex flex-wrap gap-2">
          {platforms.map((pool) => (
            <div key={pool.platform} className="space-y-1.5 rounded-md border bg-card p-3 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <strong>{pool.platform}</strong>
                <span>总数 <MetricValue value={pool.accounts_total} quality={quality} /></span>
                <span className="inline-flex items-center gap-1">
                  Ops 判定可用 <MetricNote label="平台账号可用口径">{availabilityNote}</MetricNote>
                  <MetricValue value={pool.accounts_ops_available} quality={quality} />
                </span>
              </div>
              <div className="flex flex-wrap gap-2 text-muted-foreground">
                <span>限流 <MetricValue value={pool.accounts_rate_limited} quality={quality} /></span>
                <span>过载 <MetricValue value={pool.accounts_overloaded} quality={quality} /></span>
                <span>异常 <MetricValue value={pool.accounts_error} quality={quality} /></span>
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="rounded-lg border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>分组</TableHead>
              <TableHead>平台</TableHead>
              <TableHead>账号总数</TableHead>
              <TableHead><span className="inline-flex items-center gap-1">Ops 判定可用 <MetricNote label="分组账号可用口径">{availabilityNote}</MetricNote></span></TableHead>
              <TableHead><span className="inline-flex items-center gap-1">配置并发容量 <MetricNote label="配置并发容量口径">{capacityNote}</MetricNote></span></TableHead>
              <TableHead>并发使用 / 等待</TableHead>
              <TableHead>负载</TableHead>
              <TableHead>单点风险</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.length === 0 && <TableRow><TableCell colSpan={8} className="text-center text-muted-foreground">暂无分组容量记录。</TableCell></TableRow>}
            {groups.map((group) => {
              const rowQuality = quality === "trusted" ? group.trust_state || "unavailable" : quality
              const { load_percent: load } = group
              return (
                <TableRow key={group.group_id}>
                  <TableCell className="font-medium">{group.group_name}</TableCell>
                  <TableCell>{group.platform}</TableCell>
                  <TableCell><MetricValue value={group.accounts_total} quality={rowQuality} /></TableCell>
                  <TableCell><MetricValue value={group.accounts_ops_available} quality={rowQuality} /></TableCell>
                  <TableCell><MetricValue value={group.conc_max} quality={rowQuality} /></TableCell>
                  <TableCell>
                    {rowQuality === "trusted" ? (
                      <span className="inline-flex items-center gap-1"><MetricValue value={group.conc_used} /> / <MetricValue value={group.conc_waiting} /></span>
                    ) : <FieldQualityBadge quality={rowQuality} reason={group.trust_reason || data.trust_reason} />}
                  </TableCell>
                  <TableCell>
                    <div className="min-w-20 space-y-1">
                      <MetricValue value={load} quality={rowQuality} reason={group.trust_reason || data.trust_reason} unit="%" />
                      {rowQuality === "trusted" && load != null && Number.isFinite(load) && load >= 0 && (
                        <Progress className="h-1.5" aria-label={`${group.group_name} 并发负载`} value={Math.min(100, load)} />
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    {group.single_point_risk ? (
                      rowQuality === "trusted"
                        ? <Badge variant="destructive">可用号 ≤ 1</Badge>
                        : <Badge variant="outline" className="border-amber-500/40 text-amber-700 dark:text-amber-300">单点风险待核实</Badge>
                    ) : <span className="text-muted-foreground">—</span>}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}

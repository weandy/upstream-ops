import { Activity, PencilLine, Plus, RefreshCw, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { TabsList, TabsTrigger } from "@/components/ui/tabs"
import { decimal, fullDateTime } from "@/lib/format"
import type { SiteOpsAuditBudgetUsage, SiteOpsInstanceSummaryItem, SiteOpsInstanceView } from "@/lib/site-ops-types"
import { InstanceStatusBadge, MetricValue } from "./quality-badge"

function AuditBudget({ budget }: { budget?: SiteOpsAuditBudgetUsage }) {
  return (
    <div className="min-w-28 space-y-1 text-xs">
      <div className="text-muted-foreground">
        今日审计预算 {budget ? `${budget.used_today} / ${budget.daily_limit}` : "无数据"}
      </div>
      {budget && budget.daily_limit > 0 && (
        <Progress
          aria-label="今日审计预算使用比例"
          title={`UTC 日期 ${budget.window_date_utc}`}
          className="h-1.5"
          value={Math.min(100, 100 * budget.used_today / budget.daily_limit)}
        />
      )}
    </div>
  )
}

export function InstanceSwitcher({
  instances, summaries, selected, readOnly, busy, onAdd, onEdit, onDelete, onDiagnose, onRefresh,
}: {
  instances: SiteOpsInstanceView[]
  summaries: SiteOpsInstanceSummaryItem[]
  selected: SiteOpsInstanceView
  readOnly: boolean
  busy: boolean
  onAdd: () => void
  onEdit: () => void
  onDelete: () => void
  onDiagnose: () => void
  onRefresh: () => void
}) {
  const summary = summaries.find((item) => item.id === selected.id)
  return (
    <section aria-label="站点实例" className="space-y-3 rounded-lg border bg-card p-3.5">
      {instances.length > 1 && (
        <TabsList aria-label="切换站点" className="h-auto flex-wrap justify-start">
          {instances.map((instance) => {
            const item = summaries.find((entry) => entry.id === instance.id)
            return (
              <TabsTrigger key={instance.id} value={String(instance.id)} className="flex-wrap">
                {instance.name}
                <InstanceStatusBadge status={instance.last_status} />
                <span className="text-xs text-muted-foreground">
                  QPS {item?.qps_current != null && Number.isFinite(item.qps_current) ? decimal(item.qps_current) : "无数据"}
                </span>
                <span className="text-xs text-muted-foreground">
                  预算 {instance.audit_budget.used_today}/{instance.audit_budget.daily_limit}
                </span>
              </TabsTrigger>
            )
          })}
        </TabsList>
      )}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
          <h2 id="site-ops-selected-name" className="text-sm font-semibold">{selected.name}</h2>
          <InstanceStatusBadge status={selected.last_status} />
          {!selected.enabled && <span className="text-muted-foreground">轮询已关闭</span>}
          <span>健康分 <MetricValue value={summary?.health_score} /></span>
          <span className="text-muted-foreground">版本 {selected.last_version || "无数据"}</span>
          <span className="text-muted-foreground">观测 {fullDateTime(selected.last_observed_at)}</span>
          <span className="text-muted-foreground">验证 {fullDateTime(selected.last_verified_at)}</span>
          <AuditBudget budget={selected.audit_budget} />
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" disabled={readOnly || busy} onClick={onEdit}><PencilLine />编辑</Button>
          <Button variant="outline" size="sm" className="text-destructive" disabled={readOnly || busy} onClick={onDelete}><Trash2 />删除</Button>
          <Button variant="outline" size="sm" onClick={onDiagnose}><Activity />能力诊断</Button>
          <Button variant="outline" size="sm" disabled={readOnly || busy} onClick={onRefresh}>
            <RefreshCw />立即刷新
          </Button>
          <Button size="sm" disabled={readOnly} onClick={onAdd}><Plus />添加站点</Button>
        </div>
      </div>
      {instances.length > 1 && (
        <details className="border-t pt-3">
          <summary className="cursor-pointer text-sm font-medium">全部实例对比（{instances.length}）</summary>
          <div className="mt-3 flex gap-2 overflow-x-auto pb-1">
            {instances.map((instance) => {
              const item = summaries.find((entry) => entry.id === instance.id)
              return (
                <div key={instance.id} className="min-w-44 space-y-2 rounded-md border p-3 text-xs">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <strong>{instance.name}</strong>
                    <InstanceStatusBadge status={instance.last_status} />
                  </div>
                  <div>健康分 <MetricValue value={item?.health_score} /></div>
                  <div>近 1 小时 SLA <MetricValue value={item?.sla_percent_1h} unit="%" /></div>
                  <div>QPS <MetricValue value={item?.qps_current} /></div>
                  <div>去重并发 <MetricValue value={item?.conc_used} quality={item?.conc_trust_state} /></div>
                  <AuditBudget budget={instance.audit_budget} />
                </div>
              )
            })}
          </div>
        </details>
      )}
    </section>
  )
}

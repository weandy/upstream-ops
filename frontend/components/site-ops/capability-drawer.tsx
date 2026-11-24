import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { fullDateTime } from "@/lib/format"
import type { SiteOpsInstanceView, SiteOpsProbeReport } from "@/lib/site-ops-types"
import { CapabilityStateBadge } from "./quality-badge"

export function CapabilityDrawer({
  instance, report, busy, probeDisabled, error, onClose, onProbe,
}: {
  instance: SiteOpsInstanceView
  report?: SiteOpsProbeReport
  busy: boolean
  probeDisabled: boolean
  error?: string
  onClose: () => void
  onProbe: () => void
}) {
  const endpoints = Object.entries(report?.capabilities ?? instance.capabilities ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
  const reachability = report?.reachability ?? instance.last_reachability
  return (
    <Sheet open onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent className="w-full gap-0 sm:max-w-lg">
        <SheetHeader className="pr-12">
          <SheetTitle>能力诊断</SheetTitle>
          <SheetDescription>{instance.name} · {instance.base_url}</SheetDescription>
        </SheetHeader>
        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 pb-4">
          <div className="flex flex-wrap items-center justify-between gap-2 border-y py-3">
            <div className="text-xs text-muted-foreground">
              HTTP {reachability === "reachable" ? "可达" : reachability === "unreachable" ? "不可达" : "待验证"}
              {report && <div>最近探测 {fullDateTime(report.probed_at)}</div>}
            </div>
            <Button size="sm" variant="outline" disabled={probeDisabled || busy} onClick={onProbe}>
              <RefreshCw className={busy ? "animate-spin" : undefined} />
              {busy ? "探测中…" : endpoints.length ? "重新探测" : "能力探测"}
            </Button>
          </div>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          {endpoints.length === 0 && <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">暂无端点探测记录。</p>}
          {endpoints.map(([endpoint, capability]) => (
            <article key={endpoint} className="space-y-2 rounded-md border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <code className="min-w-0 break-all text-xs">{endpoint}</code>
                <CapabilityStateBadge state={capability.state} />
              </div>
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs text-muted-foreground">
                <dt>HTTP 状态</dt><dd>{capability.http_status || "无数据"}</dd>
                <dt>回退目标</dt><dd className="break-all">{capability.fallback_target || "—"}</dd>
                <dt>探测时间</dt><dd>{fullDateTime(capability.checked_at)}</dd>
              </dl>
              {capability.reason && <p className="break-words text-xs text-muted-foreground">{capability.reason}</p>}
            </article>
          ))}
        </div>
      </SheetContent>
    </Sheet>
  )
}

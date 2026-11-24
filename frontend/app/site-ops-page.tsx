import { useCallback, useEffect, useRef, useState } from "react"
import { Plus, RefreshCw, ShieldAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { CapabilityDrawer } from "@/components/site-ops/capability-drawer"
import { GroupsMatrixPanel } from "@/components/site-ops/groups-matrix-panel"
import { InstanceFormDialog } from "@/components/site-ops/instance-form-dialog"
import { InstanceSwitcher } from "@/components/site-ops/instance-switcher"
import { OverviewPanel } from "@/components/site-ops/overview-panel"
import { TrendsPanel } from "@/components/site-ops/trends-panel"
import { useRefreshTick } from "@/lib/refresh-context"
import {
  deleteSiteOpsInstance,
  fetchSiteOpsGroups,
  fetchSiteOpsInstances,
  fetchSiteOpsOverview,
  fetchSiteOpsSummary,
  fetchSiteOpsTrends,
  isSiteOpsAuthRequiredError,
  probeSiteOpsInstance,
  refreshSiteOpsInstance,
} from "@/lib/site-ops-api"
import type {
  SiteOpsGroupsResponse,
  SiteOpsInstanceView,
  SiteOpsOverviewResponse,
  SiteOpsProbeReport,
  SiteOpsSummaryResponse,
  SiteOpsTrendRange,
  SiteOpsTrendsResponse,
} from "@/lib/site-ops-types"

interface InstanceDetails {
  id: number
  range: SiteOpsTrendRange
  overview?: SiteOpsOverviewResponse
  overviewError?: string
  trends?: SiteOpsTrendsResponse
  trendsError?: string
  groups?: SiteOpsGroupsResponse
  groupsError?: string
}

type Action = "refresh" | "probe"

function ReadState({ error, loading, empty }: { error?: string; loading: boolean; empty: string }) {
  return (
    <p role={error ? "alert" : "status"} className={error ? "text-sm text-destructive" : "text-sm text-muted-foreground"}>
      {error ?? (loading ? "正在加载…" : empty)}
    </p>
  )
}

export default function SiteOpsPage() {
  const tick = useRefreshTick()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const [reload, setReload] = useState(0)
  const [blocked, setBlocked] = useState(false)
  const [catalog, setCatalog] = useState<{ summary: SiteOpsSummaryResponse; instances: SiteOpsInstanceView[] } | null>(null)
  const [catalogError, setCatalogError] = useState("")
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [range, setRange] = useState<SiteOpsTrendRange>("6h")
  const [details, setDetails] = useState<InstanceDetails | null>(null)
  const [loadingDetails, setLoadingDetails] = useState(false)
  const [form, setForm] = useState<{ instance: SiteOpsInstanceView | null } | null>(null)
  const [drawerId, setDrawerId] = useState<number | null>(null)
  const [report, setReport] = useState<SiteOpsProbeReport>()
  const [activeAction, setActiveAction] = useState<{ id: number; action: Action | "delete" } | null>(null)
  const [notice, setNotice] = useState<{ id: number; action: Action | "delete"; message: string; error: boolean } | null>(null)
  const catalogRequest = useRef(0)
  const detailRequest = useRef(0)

  const selected = catalog?.instances.find((instance) => instance.id === selectedId) ?? catalog?.instances[0]
  const id = selected?.id
  const readOnly = catalog?.summary.auth_enabled !== true
  const currentDetails = details?.id === id ? details : null
  const currentTrends = currentDetails?.range === range ? currentDetails : null
  const currentNotice = notice?.id === id ? notice : null

  const requireAuth = useCallback(() => {
    catalogRequest.current += 1
    detailRequest.current += 1
    setBlocked(true)
    setCatalog(null)
    setDetails(null)
    setReport(undefined)
    setNotice(null)
    setForm(null)
    setDrawerId(null)
  }, [])

  useEffect(() => {
    const sequence = ++catalogRequest.current
    Promise.all([fetchSiteOpsSummary(), fetchSiteOpsInstances()])
      .then(([summary, instances]) => {
        if (sequence !== catalogRequest.current) return
        setCatalog({ summary, instances: instances ?? [] })
        setCatalogError("")
        setBlocked(false)
        if (!summary.auth_enabled) setForm(null)
        setSelectedId((current) => {
          if (instances?.some((instance) => instance.id === current)) return current
          return instances?.[0]?.id ?? null
        })
      })
      .catch((err: unknown) => {
        if (sequence !== catalogRequest.current) return
        if (isSiteOpsAuthRequiredError(err)) {
          requireAuth()
        } else {
          setCatalogError("无法加载站点列表，请重试。")
          setCatalog(null)
          setDetails(null)
          setForm(null)
          setDrawerId(null)
        }
      })
    return () => { catalogRequest.current += 1 }
  }, [tick, reload, requireAuth])

  useEffect(() => {
    if (id === undefined || blocked) return
    const sequence = ++detailRequest.current
    setLoadingDetails(true)
    function checkAccess<T>(request: Promise<T>) {
      return request.catch((err: unknown) => {
        if (sequence === detailRequest.current && isSiteOpsAuthRequiredError(err)) requireAuth()
        throw err
      })
    }
    Promise.allSettled([
      checkAccess(fetchSiteOpsOverview(id)),
      checkAccess(fetchSiteOpsTrends(id, range)),
      checkAccess(fetchSiteOpsGroups(id)),
    ]).then(([overview, trends, groups]) => {
      if (sequence !== detailRequest.current) return
      setDetails({
        id,
        range,
        overview: overview.status === "fulfilled" ? overview.value : undefined,
        overviewError: overview.status === "rejected" ? "无法加载实时总览。" : undefined,
        trends: trends.status === "fulfilled" ? trends.value : undefined,
        trendsError: trends.status === "rejected" ? "无法加载历史趋势。" : undefined,
        groups: groups.status === "fulfilled" ? groups.value : undefined,
        groupsError: groups.status === "rejected" ? "无法加载分组容量。" : undefined,
      })
      setLoadingDetails(false)
    })
    return () => { detailRequest.current += 1 }
  }, [id, range, tick, reload, blocked, requireAuth])

  function selectInstance(nextId: number) {
    if (nextId === id) return
    detailRequest.current += 1
    setSelectedId(nextId)
    setDetails(null)
    setDrawerId(null)
    setNotice(null)
  }

  function changeRange(nextRange: SiteOpsTrendRange) {
    if (nextRange === range) return
    detailRequest.current += 1
    setRange(nextRange)
  }

  function onSaved(instance: SiteOpsInstanceView) {
    catalogRequest.current += 1
    selectInstance(instance.id)
    setReport((current) => current?.instance_id === instance.id ? undefined : current)
    setCatalog((current) => current && {
      ...current,
      instances: current.instances.some((item) => item.id === instance.id)
        ? current.instances.map((item) => item.id === instance.id ? instance : item)
        : [...current.instances, instance],
    })
    setForm(null)
    setReload((current) => current + 1)
  }

  async function runAction(action: Action) {
    if (id === undefined || activeAction || (readOnly && action === "refresh")) return
    setActiveAction({ id, action })
    setNotice(null)
    try {
      if (action === "probe") {
        setReport(await probeSiteOpsInstance(id))
      } else {
        const result = await refreshSiteOpsInstance(id)
        setNotice({
          id,
          action,
          message: result.message || (result.snapshot ? "已刷新站点数据。" : "刷新未返回新样本。"),
          error: !result.snapshot,
        })
      }
    } catch (err) {
      if (isSiteOpsAuthRequiredError(err)) {
        requireAuth()
      } else {
        setNotice({ id, action, message: action === "probe" ? "能力探测失败，请重试。" : "站点刷新失败，请重试。", error: true })
      }
    } finally {
      setActiveAction(null)
      setReload((current) => current + 1)
    }
  }

  async function deleteInstance(instance: SiteOpsInstanceView) {
    if (readOnly || activeAction) return
    const { id: deletedId, name } = instance
    setActiveAction({ id: deletedId, action: "delete" })
    try {
      const confirmed = await confirm({
        title: "删除自有站点？",
        description: `将从运维列表移除「${name}」并停止采集该站点。`,
        confirmLabel: "删除站点",
        destructive: true,
      })
      if (!confirmed) return

      setNotice(null)
      const result = await deleteSiteOpsInstance(deletedId)
      if (!result.deleted) {
        setNotice({ id: deletedId, action: "delete", message: "删除未确认，正在重新加载站点列表。", error: true })
        setReload((current) => current + 1)
        return
      }
      catalogRequest.current += 1
      detailRequest.current += 1
      setCatalog((current) => current && {
        ...current,
        instances: current.instances.filter((item) => item.id !== deletedId),
      })
      setSelectedId((current) => current === deletedId ? null : current)
      setDetails((current) => current?.id === deletedId ? null : current)
      setDrawerId((current) => current === deletedId ? null : current)
      setReport((current) => current?.instance_id === deletedId ? undefined : current)
      setForm((current) => current?.instance?.id === deletedId ? null : current)
      setReload((current) => current + 1)
    } catch (err) {
      if (isSiteOpsAuthRequiredError(err)) {
        requireAuth()
      } else {
        setNotice({ id: deletedId, action: "delete", message: "站点删除失败，请重试。", error: true })
      }
    } finally {
      setActiveAction(null)
    }
  }

  return (
    <div className="space-y-5 pb-6">
      <header className="space-y-1">
        <h1 className="text-lg font-semibold tracking-tight sm:text-xl">自有站点运维</h1>
        <p className="text-xs text-muted-foreground sm:text-sm">查看自有 Sub2API 站点的运行指标、端点能力与号池容量。</p>
      </header>

      {blocked ? (
        <Card>
          <CardContent role="status" className="flex items-start gap-3 py-5">
            <ShieldAlert className="mt-0.5 size-5 shrink-0 text-amber-600" />
            <div className="space-y-2">
              <h2 className="font-semibold">站点运维需要开启管理鉴权</h2>
              <p className="text-sm text-muted-foreground">
                请在部署配置中设置 <code>auth.enabled: true</code>。本机单机只读调试可显式设置 <code>siteOps.allowUnauthenticatedLocalOnly: true</code>，并从回环地址访问。
              </p>
              <Button size="sm" variant="outline" onClick={() => setReload((current) => current + 1)}>重新检查</Button>
            </div>
          </CardContent>
        </Card>
      ) : !catalog ? (
        <div className="flex flex-wrap items-center gap-3">
          <ReadState error={catalogError || undefined} loading={!catalogError} empty="" />
          {catalogError && <Button size="sm" variant="outline" onClick={() => setReload((current) => current + 1)}><RefreshCw />重试</Button>}
        </div>
      ) : (
        <>
          {readOnly && <p className="rounded-md border bg-muted/40 p-3 text-sm text-muted-foreground">当前处于本机只读调试模式。启用管理鉴权后可添加或编辑站点并立即刷新。</p>}
          {!selected ? (
            <Card className="border-dashed">
              <CardContent className="space-y-3 py-5">
                <h2 className="font-semibold">还没有自有站点</h2>
                <p className="text-sm text-muted-foreground">添加已加入部署白名单的 Sub2API 站点，开始采集运行指标与号池容量。</p>
                <Button size="sm" disabled={readOnly} onClick={() => setForm({ instance: null })}><Plus />添加自有站点</Button>
              </CardContent>
            </Card>
          ) : (
            <Tabs value={String(selected.id)} onValueChange={(value) => selectInstance(Number(value))} className="gap-5">
              <InstanceSwitcher
                instances={catalog.instances}
                summaries={catalog.summary.instances ?? []}
                selected={selected}
                readOnly={readOnly}
                busy={activeAction !== null}
                onAdd={() => setForm({ instance: null })}
                onEdit={() => setForm({ instance: selected })}
                onDelete={() => { void deleteInstance(selected) }}
                onDiagnose={() => {
                  setReport(undefined)
                  setDrawerId(selected.id)
                }}
                onRefresh={() => { void runAction("refresh") }}
              />
              <TabsContent
                value={String(selected.id)}
                className="space-y-5"
                {...(catalog.instances.length === 1 ? { role: "region", "aria-labelledby": "site-ops-selected-name" } : {})}
              >
                {activeAction?.id === selected.id && activeAction.action === "refresh" && <p role="status" className="text-sm text-muted-foreground">正在刷新站点数据…</p>}
                {currentNotice?.action === "refresh" && <p role={currentNotice.error ? "alert" : "status"} className={currentNotice.error ? "text-sm text-destructive" : "text-sm text-muted-foreground"}>{currentNotice.message}</p>}
                {currentNotice?.action === "delete" && <p role="alert" className="text-sm text-destructive">{currentNotice.message}</p>}
                {currentDetails?.overview
                  ? <OverviewPanel overview={currentDetails.overview} />
                  : <ReadState error={currentDetails?.overviewError} loading={!currentDetails} empty="暂无实时总览。" />}
                <TrendsPanel
                  range={range}
                  onRangeChange={changeRange}
                  points={currentTrends?.trends?.points}
                  error={currentTrends?.trendsError}
                  loading={loadingDetails || !currentTrends}
                />
                {currentDetails?.groups
                  ? <GroupsMatrixPanel data={currentDetails.groups} />
                  : <ReadState error={currentDetails?.groupsError} loading={!currentDetails} empty="暂无分组容量。" />}
              </TabsContent>
              {drawerId === selected.id && (
                <CapabilityDrawer
                  instance={selected}
                  report={report?.instance_id === selected.id ? report : undefined}
                  busy={activeAction?.id === selected.id && activeAction.action === "probe"}
                  probeDisabled={readOnly || activeAction !== null}
                  error={currentNotice?.action === "probe" ? currentNotice.message : undefined}
                  onClose={() => setDrawerId(null)}
                  onProbe={() => { void runAction("probe") }}
                />
              )}
            </Tabs>
          )}
          {form && !readOnly && <InstanceFormDialog instance={form.instance} onClose={() => setForm(null)} onSaved={onSaved} onAuthRequired={requireAuth} />}
        </>
      )}
      {confirmDialog}
    </div>
  )
}

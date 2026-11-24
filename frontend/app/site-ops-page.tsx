import { useCallback, useEffect, useRef, useState } from "react"
import { Plus, RefreshCw, ShieldAlert } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { useConfirm } from "@/components/ui/confirm-dialog"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { AccountEfficiencyPanel } from "@/components/site-ops/account-efficiency-panel"
import { AnalyticsPanel } from "@/components/site-ops/analytics-panel"
import { AccountsStatusPanel } from "@/components/site-ops/accounts-status-panel"
import { CapabilityDrawer } from "@/components/site-ops/capability-drawer"
import { EventsTimelinePanel } from "@/components/site-ops/events-timeline-panel"
import { GroupsMatrixPanel } from "@/components/site-ops/groups-matrix-panel"
import { InstanceFormDialog } from "@/components/site-ops/instance-form-dialog"
import { InstanceSwitcher } from "@/components/site-ops/instance-switcher"
import { OverviewPanel } from "@/components/site-ops/overview-panel"
import { PoolForecastPanel } from "@/components/site-ops/pool-forecast-panel"
import { TrendsPanel } from "@/components/site-ops/trends-panel"
import { useRefreshTick } from "@/lib/refresh-context"
import {
  deleteSiteOpsInstance,
  fetchSiteOpsAccountsEfficiency,
  fetchSiteOpsAccountsStatus,
  fetchSiteOpsEvents,
  fetchSiteOpsGroups,
  fetchSiteOpsInstances,
  fetchSiteOpsOverview,
  fetchSiteOpsPoolForecast,
  fetchSiteOpsSummary,
  fetchSiteOpsTrends,
  isSiteOpsAuthRequiredError,
  previewSiteOpsDailyReport,
  probeSiteOpsInstance,
  refreshSiteOpsInstance,
  sendSiteOpsDailyReport,
  updateSiteOpsAccountCost,
} from "@/lib/site-ops-api"
import type {
  SiteOpsAccountsEfficiencyResponse,
  SiteOpsAccountsStatusResponse,
  SiteOpsEventsResponse,
  SiteOpsGroupsResponse,
  SiteOpsInstanceView,
  SiteOpsOverviewResponse,
  SiteOpsPoolForecastResponse,
  SiteOpsProbeReport,
  SiteOpsSummaryResponse,
  SiteOpsTrendRange,
  SiteOpsTrendsResponse,
  SiteOpsUpdateAccountCostInput,
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

interface ForecastSectionState {
  id: number
  data?: SiteOpsPoolForecastResponse
  error?: string
}

interface EfficiencySectionState {
  id: number
  data?: SiteOpsAccountsEfficiencyResponse
  error?: string
}

interface AccountsSectionState {
  id: number
  data?: SiteOpsAccountsStatusResponse
  error?: string
}

interface EventsSectionState {
  id: number
  data?: SiteOpsEventsResponse
  error?: string
}

type Action = "refresh" | "probe"
type DetailSection = "overview" | "forecast" | "accounts" | "events" | "analytics"

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
  const [activeSection, setActiveSection] = useState<DetailSection>("overview")
  const [range, setRange] = useState<SiteOpsTrendRange>("6h")
  const [details, setDetails] = useState<InstanceDetails | null>(null)
  const [loadingDetails, setLoadingDetails] = useState(false)
  const [forecastState, setForecastState] = useState<ForecastSectionState | null>(null)
  const [loadingForecast, setLoadingForecast] = useState(false)
  const [forecastReload, setForecastReload] = useState(0)
  const [efficiencyState, setEfficiencyState] = useState<EfficiencySectionState | null>(null)
  const [loadingEfficiency, setLoadingEfficiency] = useState(false)
  const [efficiencyReload, setEfficiencyReload] = useState(0)
  const [accountsState, setAccountsState] = useState<AccountsSectionState | null>(null)
  const [loadingAccounts, setLoadingAccounts] = useState(false)
  const [accountsReload, setAccountsReload] = useState(0)
  const [eventsState, setEventsState] = useState<EventsSectionState | null>(null)
  const [loadingEvents, setLoadingEvents] = useState(false)
  const [eventsReload, setEventsReload] = useState(0)
  const [form, setForm] = useState<{ instance: SiteOpsInstanceView | null } | null>(null)
  const [drawerId, setDrawerId] = useState<number | null>(null)
  const [report, setReport] = useState<SiteOpsProbeReport>()
  const [activeAction, setActiveAction] = useState<{ id: number; action: Action | "delete" } | null>(null)
  const [notice, setNotice] = useState<{ id: number; action: Action | "delete"; message: string; error: boolean } | null>(null)
  const catalogRequest = useRef(0)
  const detailRequest = useRef(0)
  const forecastRequest = useRef(0)
  const efficiencyRequest = useRef(0)
  const accountsRequest = useRef(0)
  const eventsRequest = useRef(0)

  const selected = catalog?.instances.find((instance) => instance.id === selectedId) ?? catalog?.instances[0]
  const id = selected?.id
  const readOnly = false
  const currentDetails = details?.id === id ? details : null
  const currentTrends = currentDetails?.range === range ? currentDetails : null
  const currentForecast = forecastState?.id === id ? forecastState : null
  const currentEfficiency = efficiencyState?.id === id ? efficiencyState : null
  const currentAccounts = accountsState?.id === id ? accountsState : null
  const currentEvents = eventsState?.id === id ? eventsState : null
  const currentNotice = notice?.id === id ? notice : null

  const forecastRiskCount =
    (currentForecast?.data?.will_exhaust_high_count ?? 0) +
    (currentForecast?.data?.exhausted_count ?? 0)

  const abnormalAccountsCount = (() => {
    if (currentAccounts?.data) {
      if (currentAccounts.data.trust_state !== "trusted") return 0
      return (currentAccounts.data.items ?? []).filter(
        (item) => item.has_error || item.rate_limited || item.overloaded || !item.ops_available,
      ).length
    }
    const latest = currentDetails?.overview?.latest
    if (!latest || latest.field_quality?.account_availability !== "trusted") return 0
    return (
      (latest.accounts_error ?? 0) +
      (latest.accounts_rate_limited ?? 0) +
      (latest.accounts_overloaded ?? 0)
    )
  })()

  const firingEventsCount = currentEvents?.data?.firing_count ?? 0

  const requireAuth = useCallback(() => {
    catalogRequest.current += 1
    detailRequest.current += 1
    forecastRequest.current += 1
    efficiencyRequest.current += 1
    accountsRequest.current += 1
    eventsRequest.current += 1
    setBlocked(true)
    setCatalog(null)
    setDetails(null)
    setForecastState(null)
    setEfficiencyState(null)
    setAccountsState(null)
    setEventsState(null)
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

  useEffect(() => {
    if (id === undefined || blocked || activeSection !== "forecast") return
    const sequence = ++forecastRequest.current
    setLoadingForecast(true)
    fetchSiteOpsPoolForecast(id)
      .then((data) => {
        if (sequence !== forecastRequest.current) return
        setForecastState({ id, data })
      })
      .catch((err: unknown) => {
        if (sequence !== forecastRequest.current) return
        if (isSiteOpsAuthRequiredError(err)) {
          requireAuth()
        } else {
          setForecastState((current) => ({
            id,
            data: current?.id === id ? current.data : undefined,
            error: "无法加载号池周期预测，请重试。",
          }))
        }
      })
      .finally(() => {
        if (sequence === forecastRequest.current) setLoadingForecast(false)
      })
    return () => { forecastRequest.current += 1 }
  }, [id, activeSection, tick, reload, forecastReload, blocked, requireAuth])

  useEffect(() => {
    if (id === undefined || blocked || activeSection !== "forecast") return
    const sequence = ++efficiencyRequest.current
    setLoadingEfficiency(true)
    fetchSiteOpsAccountsEfficiency(id)
      .then((data) => {
        if (sequence !== efficiencyRequest.current) return
        setEfficiencyState({ id, data })
      })
      .catch((err: unknown) => {
        if (sequence !== efficiencyRequest.current) return
        if (isSiteOpsAuthRequiredError(err)) {
          requireAuth()
        } else {
          setEfficiencyState((current) => ({
            id,
            data: current?.id === id ? current.data : undefined,
            error: "无法加载单号成本效率，请重试。",
          }))
        }
      })
      .finally(() => {
        if (sequence === efficiencyRequest.current) setLoadingEfficiency(false)
      })
    return () => { efficiencyRequest.current += 1 }
  }, [id, activeSection, tick, reload, efficiencyReload, blocked, requireAuth])

  useEffect(() => {
    if (id === undefined || blocked || activeSection !== "accounts") return
    const sequence = ++accountsRequest.current
    setLoadingAccounts(true)
    fetchSiteOpsAccountsStatus(id)
      .then((data) => {
        if (sequence !== accountsRequest.current) return
        setAccountsState({ id, data })
      })
      .catch((err: unknown) => {
        if (sequence !== accountsRequest.current) return
        if (isSiteOpsAuthRequiredError(err)) {
          requireAuth()
        } else {
          setAccountsState((current) => ({
            id,
            data: current?.id === id ? current.data : undefined,
            error: "无法加载账号状态，请重试。",
          }))
        }
      })
      .finally(() => {
        if (sequence === accountsRequest.current) setLoadingAccounts(false)
      })
    return () => { accountsRequest.current += 1 }
  }, [id, activeSection, tick, reload, accountsReload, blocked, requireAuth])

  useEffect(() => {
    if (id === undefined || blocked) return
    const sequence = ++eventsRequest.current
    setLoadingEvents(true)
    fetchSiteOpsEvents(id)
      .then((data) => {
        if (sequence !== eventsRequest.current) return
        setEventsState({ id, data })
      })
      .catch((err: unknown) => {
        if (sequence !== eventsRequest.current) return
        if (isSiteOpsAuthRequiredError(err)) {
          requireAuth()
        } else {
          setEventsState((current) => ({
            id,
            data: current?.id === id ? current.data : undefined,
            error: "无法加载事件列表，请重试。",
          }))
        }
      })
      .finally(() => {
        if (sequence === eventsRequest.current) setLoadingEvents(false)
      })
    return () => { eventsRequest.current += 1 }
  }, [id, tick, reload, eventsReload, blocked, requireAuth])

  function selectInstance(nextId: number) {
    if (nextId === id) return
    detailRequest.current += 1
    forecastRequest.current += 1
    efficiencyRequest.current += 1
    accountsRequest.current += 1
    eventsRequest.current += 1
    setSelectedId(nextId)
    setDetails(null)
    setForecastState(null)
    setEfficiencyState(null)
    setAccountsState(null)
    setEventsState(null)
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
      forecastRequest.current += 1
      efficiencyRequest.current += 1
      accountsRequest.current += 1
      eventsRequest.current += 1
      setCatalog((current) => current && {
        ...current,
        instances: current.instances.filter((item) => item.id !== deletedId),
      })
      setSelectedId((current) => current === deletedId ? null : current)
      setDetails((current) => current?.id === deletedId ? null : current)
      setForecastState((current) => current?.id === deletedId ? null : current)
      setEfficiencyState((current) => current?.id === deletedId ? null : current)
      setAccountsState((current) => current?.id === deletedId ? null : current)
      setEventsState((current) => current?.id === deletedId ? null : current)
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
              <h2 className="font-semibold">当前操作需要开启管理鉴权</h2>
              <p className="text-sm text-muted-foreground">
                请在部署配置中设置 <code>auth.enabled: true</code> 并登录后重试。
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
          {!selected ? (
            <Card className="border-dashed">
              <CardContent className="space-y-3 py-5">
                <h2 className="font-semibold">还没有自有站点</h2>
                <p className="text-sm text-muted-foreground">添加自有 Sub2API 站点的公网地址与 Admin API Key，开始采集运行指标与号池容量。</p>
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
                <Tabs
                  value={activeSection}
                  onValueChange={(value) => setActiveSection(value as DetailSection)}
                  className="gap-4"
                >
                  <TabsList className="h-auto max-w-full flex-wrap justify-start">
                    <TabsTrigger value="overview">总览与容量</TabsTrigger>
                    <TabsTrigger value="forecast">
                      号池预测与效率
                      {forecastRiskCount > 0 ? (
                        <Badge
                          variant="destructive"
                          className="ml-1 h-4.5 px-1.5 text-[11px] tabular-nums"
                        >
                          {forecastRiskCount}
                        </Badge>
                      ) : null}
                    </TabsTrigger>
                    <TabsTrigger value="accounts">
                      账号状态
                      {abnormalAccountsCount > 0 ? (
                        <Badge
                          variant="outline"
                          className="ml-1 h-4.5 border-amber-500/40 bg-amber-500/10 px-1.5 text-[11px] text-amber-700 tabular-nums dark:text-amber-300"
                        >
                          {abnormalAccountsCount}
                        </Badge>
                      ) : null}
                    </TabsTrigger>
                    <TabsTrigger value="events">
                      事件与日报
                      {firingEventsCount > 0 ? (
                        <Badge
                          variant="destructive"
                          className="ml-1 h-4.5 px-1.5 text-[11px] tabular-nums"
                        >
                          {firingEventsCount}
                        </Badge>
                      ) : null}
                    </TabsTrigger>
                    <TabsTrigger value="analytics">运营分析</TabsTrigger>
                  </TabsList>

                  <TabsContent value="overview" className="space-y-5">
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

                  <TabsContent value="forecast" className="space-y-5">
                    <PoolForecastPanel
                      data={currentForecast?.data}
                      loading={loadingForecast}
                      error={currentForecast?.error}
                      onReload={() => setForecastReload((current) => current + 1)}
                    />
                    <AccountEfficiencyPanel
                      data={currentEfficiency?.data}
                      loading={loadingEfficiency}
                      error={currentEfficiency?.error}
                      onReload={() => setEfficiencyReload((current) => current + 1)}
                      onSaveCost={async (accountId: number, input: SiteOpsUpdateAccountCostInput) => {
                        try {
                          await updateSiteOpsAccountCost(selected.id, accountId, input)
                          setEfficiencyReload((current) => current + 1)
                        } catch (err) {
                          if (isSiteOpsAuthRequiredError(err)) requireAuth()
                          throw err
                        }
                      }}
                    />
                  </TabsContent>

                  <TabsContent value="accounts">
                    <AccountsStatusPanel
                      data={currentAccounts?.data}
                      loading={loadingAccounts}
                      error={currentAccounts?.error}
                      onRefresh={() => setAccountsReload((current) => current + 1)}
                    />
                  </TabsContent>

                  <TabsContent value="events">
                    <EventsTimelinePanel
                      instanceId={selected.id}
                      instanceName={selected.name}
                      data={currentEvents?.data}
                      loading={loadingEvents}
                      error={currentEvents?.error}
                      onReload={() => setEventsReload((current) => current + 1)}
                      onPreviewReport={async () => {
                        try {
                          return await previewSiteOpsDailyReport(selected.id)
                        } catch (err) {
                          if (isSiteOpsAuthRequiredError(err)) requireAuth()
                          throw err
                        }
                      }}
                      onSendReport={async () => {
                        try {
                          return await sendSiteOpsDailyReport(selected.id)
                        } catch (err) {
                          if (isSiteOpsAuthRequiredError(err)) requireAuth()
                          throw err
                        }
                      }}
                    />
                  </TabsContent>
                  {selected && activeSection === "analytics" && !blocked && (
                    <TabsContent value="analytics">
                      <AnalyticsPanel
                        key={`${selected.id}:${selected.generation}:${selected.cred_gen}`}
                        instance={selected}
                        onAuthRequired={requireAuth}
                      />
                    </TabsContent>
                  )}
                </Tabs>
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

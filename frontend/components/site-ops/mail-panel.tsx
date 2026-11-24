import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Mail, List, FileText, Settings2, ShieldAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent } from "@/components/ui/card"
import { mailApi, useMailRead } from "@/lib/site-ops-mail-api"
import { confirmMailDiscard, leaveMailDraft, matchesMailDraft } from "@/lib/site-ops-mail-state"
import type { SiteOpsMailDraft } from "@/lib/site-ops-mail-types"
import type { SiteOpsInstanceView } from "@/lib/site-ops-types"
import { MailCompose } from "./mail-compose"
import { MailBatches } from "./mail-batches"
import { MailTemplates } from "./mail-templates"
import { MailSMTPDialog } from "./mail-smtp-dialog"
import { MailSuppressionDialog } from "./mail-suppression-dialog"

type View = "compose" | "batches" | "templates"
const views = [
  { id: "compose", label: "发起群发", icon: Mail },
  { id: "batches", label: "发送批次", icon: List },
  { id: "templates", label: "邮件模板", icon: FileText },
] as const

export function MailPanel({ instance, draft, onConsume, onDirty, onActiveBatch }: {
  instance: SiteOpsInstanceView; draft: SiteOpsMailDraft | null
  onActiveBatch: (id: number | null) => void
  onConsume: (nonce: string) => void; onDirty: (dirty: boolean) => void
}) {
  const [denied, setDenied] = useState(false)
  const [accessCheck, setAccessCheck] = useState(0)
  const api = useMemo(() => mailApi(instance.id, () => setDenied(true)), [instance.id, accessCheck])
  const scope = useMemo(() => ({ generation: instance.generation, cred_gen: instance.cred_gen }), [instance.generation, instance.cred_gen])
  const overview = useMailRead(api.overview, !denied)
  const [view, setView] = useState<View | null>(null)
  const [batchId, setBatchId] = useState<number | null>(null)
  const [dialog, setDialog] = useState<"smtp" | "suppressions" | null>(null)
  const [seed, setSeed] = useState<SiteOpsMailDraft | null>(null)
  const consumed = useRef<string | null>(null)
  const dirty = useRef(false)
  const dialogDirty = useRef(false)
  const markDialogDirty = useCallback((value: boolean) => {
    dialogDirty.current = value
    onDirty(value || dirty.current)
  }, [onDirty])
  const markDirty = useCallback((value: boolean) => {
    dirty.current = value
    onDirty(value || dialogDirty.current)
  }, [onDirty])
  useEffect(() => () => onDirty(false), [onDirty])
  const data = overview.data
  const validScope = data?.generation === scope.generation && data.cred_gen === scope.cred_gen
  useEffect(() => {
    onActiveBatch(validScope && !denied ? data?.active_batch?.id ?? null : null)
  }, [validScope, denied, data, onActiveBatch])
  useEffect(() => {
    if (validScope && matchesMailDraft(draft, instance.id, scope, consumed.current)) {
      consumed.current = draft.nonce
      setSeed(draft)
      setView("compose")
      onConsume(draft.nonce)
    }
  }, [validScope, draft, instance.id, scope, onConsume])

  function changeView(next: View, bid: number | null = null) {
    if (next === view && next !== "batches") return
    if (!leaveMailDraft(dirty.current, confirmMailDiscard)) return
    markDirty(false)
    setSeed(null)
    setBatchId(bid)
    setView(next)
  }
  function created(id: number) {
    markDirty(false)
    setSeed(null)
    setBatchId(id)
    setView("batches")
    overview.reload()
  }

  if (denied) return <Card><CardContent className="space-y-2 pt-4">
    <h3 className="flex items-center gap-2 font-semibold"><ShieldAlert size={18} />营销邮件需要管理鉴权或本机访问</h3>
    <p className="text-sm">请从本机回环地址访问。公网或反向代理部署请开启 auth.enabled 并登录。</p>
    <Button size="sm" variant="outline" onClick={() => { setDialog(null); setView(null); setSeed(null); setAccessCheck(n => n + 1); setDenied(false) }}>重新检查</Button>
  </CardContent></Card>
  if (!data || !validScope) return <div role="status" className="space-y-2 text-sm">
    {overview.error || (data ? "站点版本已变化，请刷新站点数据。" : "正在读取邮件概览…")}
    <Button size="sm" variant="outline" onClick={overview.reload}>重新读取</Button>
  </div>

  return <section className="space-y-4">
    <Card><CardContent className="space-y-4 pt-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline"><span className={`size-2 rounded-full ${data.smtp_ready ? "bg-emerald-500" : "bg-amber-500"}`} />
          SMTP {data.smtp_ready ? `就绪 ${data.smtp_from_masked}` : "待配置"}</Badge>
        {data.active_batch && <Button size="sm" variant="outline" onClick={() => changeView("batches", data.active_batch!.id)}>活跃批次 #{data.active_batch.id}</Button>}
        <Button className="sm:ml-auto" size="sm" variant="outline" onClick={() => setDialog("smtp")}><Settings2 />SMTP 设置</Button>
        <Button size="sm" variant="outline" onClick={() => setDialog("suppressions")}>退订名单（{data.suppression_count}）</Button>
      </div>
      {data.security_mode === "local_loopback_only" && <p className="text-sm text-amber-700 dark:text-amber-300">本机回环模式。公网或反向代理部署请开启管理鉴权。</p>}
      <nav aria-label="营销邮件功能" className="flex flex-wrap gap-2">{views.map(({ id, label, icon: Icon }) =>
        <Button key={id} size="sm" aria-pressed={view === id} variant={view === id ? "default" : "secondary"} onClick={() => changeView(id)}><Icon />{label}</Button>)}</nav>
    </CardContent></Card>
    {overview.error && <p role="alert" className="text-sm">{overview.error} <Button size="sm" variant="outline" onClick={overview.reload}>刷新概览</Button></p>}
    {view === null && <Card><CardContent className="space-y-4 pt-4 text-sm">
      <div className="grid gap-3 sm:grid-cols-3">
        <div className="rounded-md border p-3 space-y-1">
          <p className="text-xs text-muted-foreground">发信通道（SMTP）</p>
          <p className="font-medium">{data.smtp_ready ? `已就绪 · ${data.smtp_from_masked}` : "尚未配置"}</p>
          <Button size="sm" variant="outline" onClick={() => setDialog("smtp")}>{data.smtp_ready ? "查看或测试 SMTP" : "去配置 SMTP"}</Button>
        </div>
        <div className="rounded-md border p-3 space-y-1">
          <p className="text-xs text-muted-foreground">本站邮件模板</p>
          <p className="font-medium tabular-nums">{data.template_count} 套可用模板</p>
          <Button size="sm" variant="outline" onClick={() => changeView("templates")}>管理模板与安全预览</Button>
        </div>
        <div className="rounded-md border p-3 space-y-1">
          <p className="text-xs text-muted-foreground">本站退订名单</p>
          <p className="font-medium tabular-nums">{data.suppression_count} 个已排除地址</p>
          <Button size="sm" variant="outline" onClick={() => setDialog("suppressions")}>管理退订名单</Button>
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={() => changeView("compose")}>发起新群发</Button>
        <Button size="sm" variant="outline" onClick={() => changeView("batches", data.active_batch?.id ?? null)}>
          {data.active_batch ? `查看活跃批次 #${data.active_batch.id}` : "查看历史发送批次"}
        </Button>
      </div>
    </CardContent></Card>}
    {view === "compose" && <MailCompose key={seed?.nonce ?? "manual"} api={api} scope={scope} seed={seed} smtpReady={data.smtp_ready} onDirty={markDirty} onCreated={created} />}
    {view === "batches" && <MailBatches api={api} scope={scope} batchId={batchId} onSelect={setBatchId} visible={!dialog} onChanged={overview.reload} />}
    {view === "templates" && <MailTemplates api={api} scope={scope} sources={data.copyable_sources ?? []} onDirty={markDirty} onChanged={overview.reload} />}
    {dialog === "smtp" && <MailSMTPDialog api={api} scope={scope} sources={data.copyable_sources ?? []} onDirty={markDialogDirty} onClose={() => setDialog(null)} onChanged={overview.reload} />}
    {dialog === "suppressions" && <MailSuppressionDialog api={api} scope={scope} onDirty={markDialogDirty} onClose={() => setDialog(null)} onChanged={overview.reload} />}
  </section>
}

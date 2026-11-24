import { useCallback, useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Badge } from "@/components/ui/badge"
import { copyScope, useMailAction, useMailRead, type MailContext } from "@/lib/site-ops-mail-api"
import { byteLength, confirmMailDiscard, leaveMailDraft, mailPreviewDocument } from "@/lib/site-ops-mail-state"
import type { MailCopySource, MailTemplateCategory, MailTemplatePreview, MailTemplateVar, MailTemplateView } from "@/lib/site-ops-mail-types"
import { selectClass } from "./analytics-subscriptions-table"

const categories: Record<MailTemplateCategory, string> = { marketing: "营销", renewal: "续费", reactivation: "召回", announcement: "公告" }
const variables: MailTemplateVar[] = ["SiteName", "SiteURL", "UserID", "EmailMasked", "BalanceUSD", "PlanName", "ExpiresDate", "ExpiresInDays", "TodayDate"]
const empty: MailTemplateView = { id: 0, name: "", subject: "", category: "marketing", html_body: "", text_body: "",
  revision: 0, updated_at: "", is_built_in: false }

export function MailPreview({ preview }: { preview: MailTemplatePreview }) {
  return <section className="space-y-2 rounded-md border p-3">
    <h4 className="font-semibold">{preview.subject}</h4>
    <iframe title="邮件 HTML 安全预览" className="h-72 w-full rounded border bg-white" sandbox="" referrerPolicy="no-referrer" srcDoc={mailPreviewDocument(preview.preview_html)} />
    <details><summary>纯文本预览</summary><pre className="whitespace-pre-wrap break-words">{preview.text}</pre></details>
  </section>
}

export function MailTemplates({ api, scope, sources, onDirty, onChanged }: MailContext & {
  sources: MailCopySource[]; onDirty: (dirty: boolean) => void; onChanged: () => void
}) {
  const list = useMailRead(api.templates)
  const [sourceId, setSourceId] = useState(0)
  const source = sources.find(x => x.instance_id === sourceId)
  const sourceList = useMailRead(useCallback((s: AbortSignal) => api.templates(s, "", sourceId), [api, sourceId]), !!source)
  const [sourceTid, setSourceTid] = useState(0)
  const [form, setForm] = useState<MailTemplateView | null>(null)
  const [base, setBase] = useState<MailTemplateView | null>(null)
  const [preview, setPreview] = useState<MailTemplatePreview | null>(null)
  const [notice, setNotice] = useState("")
  const focus = useRef<"subject" | "html_body" | "text_body">("html_body")
  const fields = useRef<Partial<Record<typeof focus.current, HTMLInputElement | HTMLTextAreaElement | null>>>({})
  const dirty = JSON.stringify(form) !== JSON.stringify(base)
  useEffect(() => { onDirty(dirty); return () => onDirty(false) }, [dirty, onDirty])
  const action = useMailAction()
  const custom = list.data?.items?.filter(t => !t.is_built_in).length ?? 0
  const htmlBytes = byteLength(form?.html_body ?? "")
  const textBytes = byteLength(form?.text_body ?? "")
  const tooLong = htmlBytes > 24 * 1024 || textBytes > 12 * 1024

  function open(next: MailTemplateView | null) {
    setForm(next); setBase(next); setPreview(null); setNotice("")
  }
  function saved(next: MailTemplateView) {
    open(next); setNotice("模板已保存。"); list.reload(); onChanged()
  }
  function update(patch: Partial<MailTemplateView>) {
    if (form) setForm({ ...form, ...patch })
    setPreview(null); setNotice("")
  }
  function insert(variable: MailTemplateVar) {
    if (!form) return
    const key = focus.current
    const field = fields.current[key]
    const start = field?.selectionStart ?? form[key].length
    const end = field?.selectionEnd ?? start
    const token = "{{." + variable + "}}"
    update({ [key]: form[key].slice(0, start) + token + form[key].slice(end) })
    requestAnimationFrame(() => { field?.focus(); field?.setSelectionRange(start + token.length, start + token.length) })
  }
  const selectedSource = sourceList.data?.items?.find(t => t.id === sourceTid)
  return <fieldset disabled={action.busy} className="grid items-start gap-3 text-sm lg:grid-cols-[minmax(14rem,1fr)_3fr]">
    <Card><CardContent className="space-y-3 pt-4">
      <h3 className="font-semibold">邮件模板 · 自定义 {list.data ? custom : "…"} / 50</h3>
      <Button size="sm" disabled={custom >= 50} onClick={() => { if (leaveMailDraft(dirty, confirmMailDiscard)) open(empty) }}>新建模板</Button>
      {list.error && <Button variant="outline" size="sm" onClick={list.reload}>{list.error} 重试</Button>}
      <div className="grid gap-1">{list.data?.items?.map(t => <button key={t.id} aria-pressed={form?.id === t.id}
        className="flex items-center justify-between gap-2 rounded-md border p-2 text-left hover:bg-muted aria-pressed:bg-muted"
        onClick={() => { if (leaveMailDraft(dirty, confirmMailDiscard)) void action.run(s => api.template(t.id, s), open) }}>
        {t.name}{t.is_built_in && <Badge variant="outline">内置</Badge>}</button>)}</div>
      {!!sources.length && <div className="grid gap-2 border-t pt-3">
        <label>从其他站点复制<select className={selectClass + " w-full"} value={sourceId} onChange={e => { setSourceId(Number(e.target.value)); setSourceTid(0) }}>
          <option value={0}>选择来源站点</option>{sources.map(x => <option key={x.instance_id} value={x.instance_id}>{x.name}</option>)}</select></label>
        {source && <><select aria-label="来源模板" className={selectClass} value={sourceTid} onChange={e => setSourceTid(Number(e.target.value))}>
          <option value={0}>选择来源模板</option>{sourceList.data?.items?.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select>
          {sourceList.error && <Button size="sm" variant="outline" onClick={sourceList.reload}>{sourceList.error} 重试</Button>}
          <Button size="sm" variant="outline" disabled={!selectedSource || custom >= 50} onClick={() => {
            if (selectedSource && leaveMailDraft(dirty, confirmMailDiscard)) void action.run(s => api.copyTemplate({
              ...copyScope(source, scope, 0, selectedSource.revision), source_template_id: selectedSource.id,
            }, s), saved)
          }}>复制为本站模板</Button></>}
      </div>}
    </CardContent></Card>
    <Card><CardContent className="space-y-3 pt-4">
      {action.error && <p role="alert">{action.error}</p>}{notice && <p role="status">{notice}</p>}
      {!form ? <p>选择一个模板查看内容，或新建模板。</p> : <>
        <div className="grid gap-2 sm:grid-cols-2">
          <label>名称<Input value={form.name} onChange={e => update({ name: e.target.value })} /></label>
          <label className="grid">分类<select className={selectClass} value={form.category} onChange={e => update({ category: e.target.value as MailTemplateCategory })}>
            {Object.entries(categories).map(([v, label]) => <option key={v} value={v}>{label}</option>)}</select></label>
        </div>
        <label className="block">主题<Input ref={el => { fields.current.subject = el }} onFocus={() => { focus.current = "subject" }}
          value={form.subject} onChange={e => update({ subject: e.target.value })} /></label>
        <div className="flex flex-wrap items-center gap-1"><span>插入变量</span>{variables.map(v => <Button key={v} size="sm" variant="outline" onClick={() => insert(v)}>{v}</Button>)}</div>
        <label className="block">HTML 正文（{htmlBytes} / 24576 字节）<Textarea className="min-h-48 font-mono" value={form.html_body}
          ref={el => { fields.current.html_body = el }} onFocus={() => { focus.current = "html_body" }} onChange={e => update({ html_body: e.target.value })} /></label>
        <label className="block">纯文本（{textBytes} / 12288 字节）<Textarea value={form.text_body}
          ref={el => { fields.current.text_body = el }} onFocus={() => { focus.current = "text_body" }} onChange={e => update({ text_body: e.target.value })} /></label>
        {tooLong && <p role="alert">请缩短超出字节上限的正文。</p>}
        <div className="flex flex-wrap gap-2"><Button size="sm" disabled={tooLong || !form.name.trim() || !form.subject.trim() || !form.html_body.trim()}
          onClick={() => action.run(s => api.saveTemplate(form.id, { ...scope, expected_revision: form.revision, name: form.name,
            category: form.category, subject: form.subject, html_body: form.html_body, text_body: form.text_body }, s), saved)}>保存模板</Button>
          <Button size="sm" variant="outline" disabled={tooLong || !form.subject || !form.html_body} onClick={() => action.run(s => api.previewTemplate({
            ...scope, subject: form.subject, html_body: form.html_body, text_body: form.text_body }, s), setPreview)}>安全预览</Button>
          {!!form.id && !form.is_built_in && <Button size="sm" variant="ghost" onClick={() => {
            if (window.confirm(`删除模板「${form.name}」并放弃其编辑内容？`)) void action.run(s => api.deleteTemplate(form.id, scope, form.revision, s), () => {
              open(null); list.reload(); onChanged()
            })
          }}>删除模板</Button>}
        </div>
        {preview && <MailPreview preview={preview} />}
      </>}
    </CardContent></Card>
  </fieldset>
}

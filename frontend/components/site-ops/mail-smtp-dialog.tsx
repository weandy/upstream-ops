import { useCallback, useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { copyScope, mailErrorDetails, useMailAction, useMailRead, type MailContext } from "@/lib/site-ops-mail-api"
import { mailRequestId, confirmMailDiscard, leaveMailDraft } from "@/lib/site-ops-mail-state"
import type { MailCopySource, MailSMTPView, SaveMailSMTPInput } from "@/lib/site-ops-mail-types"
import { selectClass } from "./analytics-subscriptions-table"

const defaults: MailSMTPView = { revision: 0, host: "", port: 465, tls_mode: "implicit", auth_mode: "auto",
  from_name: "", from_masked: "", has_username: false, has_password: false, reply_to: "", unsubscribe_url: "",
  rate_per_minute: 30, max_concurrency: 1, reconnect_every: 50 }

type Props = MailContext & { sources: MailCopySource[]; onClose: () => void; onChanged: () => void; onDirty: (dirty: boolean) => void }

export function MailSMTPDialog(props: Props) {
  const { api } = props
  const read = useMailRead(useCallback(async (s: AbortSignal) => {
    try {
      const view = await api.smtp(s)
      return view?.revision ? view : defaults
    } catch (err) {
      const { code } = mailErrorDetails(err)
      if (code === "MAIL_RESOURCE_NOT_FOUND" || code === "MAIL_SMTP_NOT_READY") return defaults
      throw err
    }
  }, [api]))
  const guard = useRef<() => boolean>(() => true)
  return <Dialog open onOpenChange={open => { if (!open && guard.current()) props.onClose() }}><DialogContent className="sm:max-w-2xl">
    <DialogTitle>本站 SMTP 设置</DialogTitle>
    <DialogDescription>邮件通过本站保存的发件账号发送，测试信寄至已保存的 From 地址。</DialogDescription>
    {read.loading && <p>正在读取配置…</p>}
    {read.error && <Button size="sm" variant="outline" onClick={read.reload}>{read.error} 重试</Button>}
    {read.data && <SMTPForm {...props} initial={read.data} guard={guard} />}
  </DialogContent></Dialog>
}

function SMTPForm({ api, scope, initial, guard, sources, onChanged, onDirty }: Props & {
  initial: MailSMTPView; guard: { current: () => boolean }
}) {
  const [saved, setSaved] = useState(initial)
  const [form, setForm] = useState(initial)
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [from, setFrom] = useState("")
  const [sourceId, setSourceId] = useState(0)
  const [notice, setNotice] = useState("")
  const action = useMailAction()
  const requestId = useRef("")
  const dirty = JSON.stringify(form) !== JSON.stringify(saved) || !!(username || password || from)
  const auth = form.auth_mode !== "none"
  const passwordRequired = auth && (!saved.has_password || !!username.trim() || form.host.trim() !== saved.host || form.port !== saved.port)
  const source = sources.find(item => item.instance_id === sourceId)
  useEffect(() => {
    onDirty(dirty)
    guard.current = () => !action.busy && leaveMailDraft(dirty, confirmMailDiscard)
    return () => { guard.current = () => true; onDirty(false) }
  }, [guard, dirty, action.busy, onDirty])
  function accept(value: MailSMTPView) {
    setSaved(value); setForm(value); setUsername(""); setPassword(""); setFrom("")
    requestId.current = ""
    setNotice("已保存 SMTP 设置。"); onChanged()
  }
  function save() {
    const input: SaveMailSMTPInput = {
      ...scope, expected_revision: saved.revision, host: form.host.trim(), port: form.port, tls_mode: form.tls_mode,
      auth_mode: form.auth_mode, from_name: form.from_name, reply_to: form.reply_to, unsubscribe_url: form.unsubscribe_url,
      rate_per_minute: form.rate_per_minute, max_concurrency: form.max_concurrency, reconnect_every: form.reconnect_every,
      username: auth && username.trim() || undefined, password: auth && password || undefined, from_email: from.trim() || undefined,
    }
    void action.run(s => api.saveSMTP(input, s), accept)
  }
  return <fieldset disabled={action.busy} className="space-y-3 text-sm"><form className="space-y-3" onSubmit={e => { e.preventDefault(); save() }}>
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <label>SMTP 主机<Input required value={form.host} onChange={e => setForm({ ...form, host: e.target.value })} /></label>
      <label>端口<Input type="number" required min={1} max={65535} value={form.port || ""} onChange={e => setForm({ ...form, port: Number(e.target.value) })} /></label>
      <label className="grid">传输加密<select className={selectClass} value={form.tls_mode} onChange={e => setForm({ ...form, tls_mode: e.target.value as typeof form.tls_mode })}>
        <option value="implicit">隐式 TLS</option><option value="starttls">STARTTLS</option></select></label>
      <label className="grid">认证方式<select className={selectClass} value={form.auth_mode} onChange={e => setForm({ ...form, auth_mode: e.target.value as typeof form.auth_mode })}>
        {["auto", "plain", "login", "none"].map(mode => <option key={mode} value={mode}>{mode.toUpperCase()}</option>)}</select></label>
      {auth && <><label>用户名<Input autoComplete="off" required={!saved.has_username} value={username} placeholder={saved.has_username ? "留空保留已保存用户名" : ""}
        onChange={e => setUsername(e.target.value)} /></label>
        <label>密码<Input type="password" autoComplete="new-password" required={passwordRequired} value={password}
          placeholder={passwordRequired ? "请填写密码" : "留空保留已保存密码"} onChange={e => setPassword(e.target.value)} /></label></>}
      <label>发件邮箱（From）<Input type="email" required={!saved.from_masked} value={from} placeholder={saved.from_masked ? `留空保留 ${saved.from_masked}` : ""} onChange={e => setFrom(e.target.value)} /></label>
      <label>发件人名称<Input value={form.from_name} onChange={e => setForm({ ...form, from_name: e.target.value })} /></label>
      <label>回复邮箱<Input type="email" value={form.reply_to} onChange={e => setForm({ ...form, reply_to: e.target.value })} /></label>
      <label>退订链接<Input value={form.unsubscribe_url} placeholder="https:// 或 mailto:" onChange={e => setForm({ ...form, unsubscribe_url: e.target.value })} /></label>
      {([["rate_per_minute", "每分钟发送上限", 300], ["max_concurrency", "并发连接数", 4], ["reconnect_every", "每连接发送封数", 200]] as const).map(([key, label, max]) =>
        <label key={key}>{label}<Input required type="number" min={1} max={max} value={form[key] || ""} onChange={e => setForm({ ...form, [key]: Number(e.target.value) })} /></label>)}
    </div>
    {auth && <p className="text-muted-foreground">修改主机、端口或用户名后须重填密码。</p>}
    <div className="flex flex-wrap gap-2"><Button size="sm" type="submit">保存设置</Button>
      <Button size="sm" variant="outline" type="button" disabled={dirty || !saved.revision} onClick={() => {
        requestId.current ||= mailRequestId()
        void action.run(s => api.testSMTP({ ...scope, expected_revision: saved.revision, request_id: requestId.current }, s), result => {
          requestId.current = ""
          setNotice(`测试批次 #${result.batch_id} · ${result.from_masked} · ${result.status} ${result.smtp_stage} ${result.smtp_code || ""} ${result.error_code || ""}`)
          onChanged()
        })
      }}>测试发信至 {saved.from_masked || "已保存 From"}</Button></div>
  </form>
    {sources.some(x => x.smtp_revision > 0) && <div className="flex flex-wrap gap-2 border-t pt-3">
      <select aria-label="复制 SMTP 来源站点" className={selectClass} value={sourceId} onChange={e => setSourceId(Number(e.target.value))}>
        <option value={0}>选择来源站点</option>{sources.filter(x => x.smtp_revision > 0).map(x => <option key={x.instance_id} value={x.instance_id}>{x.name}</option>)}</select>
      <Button size="sm" variant="outline" disabled={!source} onClick={() => {
        if (source && window.confirm(`用「${source.name}」的 SMTP 设置覆盖本站配置？`)) void action.run(s =>
          api.copySMTP(copyScope(source, scope, saved.revision, source.smtp_revision), s), accept)
      }}>复制 SMTP 配置</Button>
    </div>}
    {action.error && <p role="alert">{action.error}</p>}{notice && <p role="status">{notice}</p>}
  </fieldset>
}

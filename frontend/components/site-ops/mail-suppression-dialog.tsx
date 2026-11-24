import { useCallback, useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { Textarea } from "@/components/ui/textarea"
import { useMailAction, useMailRead, type MailContext } from "@/lib/site-ops-mail-api"
import { confirmMailDiscard, leaveMailDraft } from "@/lib/site-ops-mail-state"
import type { MailSuppressionReason } from "@/lib/site-ops-mail-types"
import { selectClass } from "./analytics-subscriptions-table"
import { CursorControls } from "./mail-batches"

const reasons: Record<MailSuppressionReason, string> = { admin_added: "管理员添加", user_unsubscribed: "用户退订", hard_bounce: "硬退信" }

export function MailSuppressionDialog({ api, scope, onClose, onChanged, onDirty }: MailContext & {
  onClose: () => void; onChanged: () => void; onDirty: (dirty: boolean) => void
}) {
  const [history, setHistory] = useState([""])
  const cursor = history[history.length - 1]
  const list = useMailRead(useCallback((s: AbortSignal) => api.suppressions(cursor, s), [api, cursor]))
  const [raw, setRaw] = useState("")
  const [reason, setReason] = useState<MailSuppressionReason>("admin_added")
  const [notice, setNotice] = useState("")
  const action = useMailAction()
  const dirty = !!raw.trim()
  useEffect(() => { onDirty(dirty); return () => onDirty(false) }, [onDirty, dirty])
  const emails = [...new Set(raw.trim().split(/[\s,，;；]+/).filter(Boolean))]
  function refresh() { setHistory([""]); list.reload(); onChanged() }
  return <Dialog open onOpenChange={open => { if (!open && !action.busy && leaveMailDraft(dirty, confirmMailDiscard)) onClose() }}>
    <DialogContent className="sm:max-w-xl"><DialogTitle>本站退订名单</DialogTitle>
      <DialogDescription>名单内的地址将从群发受众中排除。每次最多添加 50 个地址。</DialogDescription>
      <fieldset disabled={action.busy} className="space-y-3 text-sm">
        <label className="block">新增地址，每行一个<Textarea value={raw} autoComplete="off" onChange={e => setRaw(e.target.value)} /></label>
        <div className="flex flex-wrap gap-2"><select aria-label="退订原因" className={selectClass} value={reason} onChange={e => setReason(e.target.value as MailSuppressionReason)}>
          {Object.entries(reasons).map(([v, name]) => <option key={v} value={v}>{name}</option>)}</select>
          <Button size="sm" disabled={!emails.length || emails.length > 50} onClick={() => {
            if (window.confirm(`将这 ${emails.length} 个地址加入本站退订名单？`)) void action.run(s => api.addSuppressions({ ...scope, emails, reason }, s), result => {
              setRaw(""); setNotice(`已添加 ${result.added} 个，已存在 ${result.existing} 个。`); refresh()
            })
          }}>添加 {emails.length} 个地址</Button></div>
        {action.error && <p role="alert">{action.error}</p>}{notice && <p role="status">{notice}</p>}
        {list.error && <Button size="sm" variant="outline" onClick={list.reload}>{list.error} 重试</Button>}
        {list.loading && <p>正在读取名单…</p>}{list.data && !list.data.items?.length && <p>本页暂无退订记录。</p>}
        <ul className="divide-y">{list.data?.items?.map(item => <li className="flex items-center gap-2 py-2" key={item.id}>
          <span>{item.email_masked}</span><span className="text-muted-foreground">{reasons[item.reason]}</span>
          <Button className="ml-auto" size="sm" variant="outline" onClick={() => {
            if (window.confirm(`从退订名单移除 ${item.email_masked}？该地址将可再次接收群发。`)) void action.run(s => api.removeSuppression(item.id, scope, s), () => {
              setNotice("已移除退订记录。"); refresh()
            })
          }}>移除</Button>
        </li>)}</ul>
        <CursorControls history={history} next={list.data?.next_cursor} onChange={setHistory} />
      </fieldset>
    </DialogContent>
  </Dialog>
}

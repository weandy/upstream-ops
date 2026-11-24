import { useCallback, useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { mailErrorDetails, mailErrorMessage, useMailAction, useMailRead, type MailContext } from "@/lib/site-ops-mail-api"
import { mailRequestId, activeMailBatch, mailPollDelay } from "@/lib/site-ops-mail-state"
import type { MailBatchStatus, MailBatchView, MailCursorPage, MailItemStatus, MailItemView, RetryMailBatchInput } from "@/lib/site-ops-mail-types"
import { selectClass } from "./analytics-subscriptions-table"

const batchStates: Record<MailBatchStatus, string> = {
  preparing: "准备中", queued: "排队中", running: "发送中", cancelling: "取消中", cancelled: "已取消",
  completed: "已完成", completed_with_errors: "完成但有异常", failed: "失败",
}
const itemStates: Record<MailItemStatus, string> = {
  pending: "待发送", sending: "发送中", sent: "已发送", failed: "确定失败", uncertain: "结果未知", cancelled: "已取消", suppressed: "已退订",
}

export function CursorControls({ history, next, onChange }: { history: string[]; next?: string | null; onChange: (value: string[]) => void }) {
  return <div className="flex items-center gap-2 text-xs">
    <Button size="sm" variant="outline" disabled={history.length <= 1} onClick={() => onChange(history.slice(0, -1))}>上一页</Button>
    第 {history.length} 页
    <Button size="sm" variant="outline" disabled={!next} onClick={() => { if (next) onChange([...history, next]) }}>下一页</Button>
  </div>
}

type Props = MailContext & { batchId: number | null; onSelect: (id: number | null) => void; visible: boolean; onChanged: () => void }

export function MailBatches(props: Props) {
  const { api, batchId, onSelect } = props
  const [history, setHistory] = useState([""])
  const [status, setStatus] = useState<MailBatchStatus | "">("")
  const cursor = history[history.length - 1]
  const list = useMailRead(useCallback((s: AbortSignal) => api.batches(cursor, status, s), [api, cursor, status]), batchId === null)
  if (batchId !== null) return <BatchDetail key={batchId} {...props} id={batchId} />
  return <Card><CardContent className="space-y-3 pt-4 text-sm">
    <div className="flex flex-wrap items-center gap-2"><h3 className="mr-auto font-semibold">发送批次</h3>
      <select className={selectClass} aria-label="筛选批次状态" value={status} onChange={e => { setStatus(e.target.value as typeof status); setHistory([""]) }}>
        <option value="">全部批次状态</option>{Object.entries(batchStates).map(([v, name]) => <option key={v} value={v}>{name}</option>)}
      </select><Button size="sm" variant="outline" onClick={list.reload}>刷新</Button></div>
    {list.error && <p role="alert">{list.error}</p>}
    {list.loading && <p role="status">正在读取批次…</p>}
    {list.data && !list.data.items?.length && <p>暂无匹配批次。</p>}
    {list.data?.items?.map(b => <button key={b.id} onClick={() => onSelect(b.id)} className="flex w-full flex-wrap items-center gap-3 rounded-md border p-3 text-left hover:bg-muted">
      <strong>#{b.id} {b.template_name}</strong><Badge variant="secondary">{batchStates[b.status]}</Badge><span>已发送 {b.sent_count} / {b.recipient_count}</span>
    </button>)}
    <CursorControls history={history} next={list.data?.next_cursor} onChange={setHistory} />
  </CardContent></Card>
}

function BatchDetail({ api, scope, id, visible, onSelect, onChanged }: Props & { id: number }) {
  const [history, setHistory] = useState([""])
  const cursor = history[history.length - 1]
  const [status, setStatus] = useState<MailItemStatus | "">("")
  const [iteration, setIteration] = useState(0)
  const [batch, setBatch] = useState<MailBatchView>()
  const [page, setPage] = useState<{ cursor: string; status: string; data: MailCursorPage<MailItemView> }>()
  const [error, setError] = useState("")
  const last = useRef<MailBatchView | undefined>(undefined)
  const retryInput = useRef<RetryMailBatchInput | null>(null)
  const action = useMailAction()
  useEffect(() => {
    if (!visible || action.busy) return
    let timer: ReturnType<typeof setTimeout> | undefined
    let current: AbortController | undefined
    let failures = 0
    function stop() {
      clearTimeout(timer)
      current?.abort()
    }
    async function read() {
      if (document.hidden) return
      const controller = new AbortController()
      current = controller
      let delay: number | null = null
      try {
        const detail = await api.batch(id, controller.signal)
        if (controller.signal.aborted) return
        const previous = last.current
        last.current = detail
        setBatch(detail)
        if (previous && activeMailBatch(previous.status) && !activeMailBatch(detail.status)) onChanged()
        const items = await api.items(id, cursor, status, controller.signal)
        if (controller.signal.aborted) return
        setPage({ cursor, status, data: items })
        setError("")
        failures = 0
        if (activeMailBatch(detail.status)) delay = mailPollDelay(0)
      } catch (err) {
        if (controller.signal.aborted) return
        setError(mailErrorMessage(err))
        const details = mailErrorDetails(err)
        if (last.current && activeMailBatch(last.current.status)) delay = mailPollDelay(++failures, details.status, details.retrySeconds)
      }
      if (delay !== null && !document.hidden && !controller.signal.aborted) timer = setTimeout(read, delay)
    }
    function visibilityChanged() {
      stop()
      if (!document.hidden) void read()
    }
    void read()
    document.addEventListener("visibilitychange", visibilityChanged)
    return () => { stop(); document.removeEventListener("visibilitychange", visibilityChanged) }
  }, [api, id, cursor, status, iteration, visible, action.busy, onChanged])
  function retryFailed() {
    if (!batch?.can_retry_failed || activeMailBatch(batch.status)) return
    if (!window.confirm("新建补发批次？仅补发确定失败项，排除结果未知（uncertain）的邮件。")) return
    void action.run(async s => {
      if (!retryInput.current) {
        const smtp = await api.smtp(s)
        retryInput.current = { ...scope, request_id: mailRequestId(), smtp_revision: smtp.revision }
      }
      return api.retryFailed(id, retryInput.current, s)
    }, child => { onSelect(child.id); onChanged() })
  }
  const items = page?.cursor === cursor && page.status === status ? page.data : undefined
  return <Card><CardContent className="space-y-4 pt-4 text-sm">
    <div className="flex flex-wrap items-center gap-2"><Button size="sm" variant="outline" onClick={() => onSelect(null)}>返回批次列表</Button>
      <h3 className="font-semibold">发送批次 #{id}</h3>{batch && <Badge variant="secondary">{batchStates[batch.status]}</Badge>}
      <Button size="sm" variant="ghost" onClick={() => setIteration(n => n + 1)}>刷新</Button></div>
    {(error || action.error) && <p role="alert">{action.error || error}</p>}
    {batch ? <>
      <p>{batch.template_name} · 已扫描 {batch.pages} 页 / {batch.scanned} 人 · 收件人 {batch.recipient_count} · 排除 {batch.excluded_count} · 重复 {batch.duplicate_count}</p>
      <p>已发送 {batch.sent_count} · 确定失败 {batch.failed_count} · 结果未知 {batch.uncertain_count} · 已取消 {batch.cancelled_count} · 退订 {batch.suppressed_count}</p>
      {batch.abort_reason && <p role="status">{batch.abort_reason}</p>}
      {batch.can_cancel && <Button size="sm" variant="outline" disabled={action.busy} onClick={() => {
        if (window.confirm("取消此批次尚未发送的邮件？")) void action.run(s => api.cancelBatch(id, scope, s), result => {
          last.current = result; setBatch(result); onChanged()
        })
      }}>取消批次</Button>}
      {!activeMailBatch(batch.status) && batch.can_retry_failed && <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" disabled={action.busy} onClick={retryFailed}>一键补发确定失败项</Button><span>补发范围排除结果未知（uncertain）的邮件。</span></div>}
    </> : <p>正在读取批次详情…</p>}
    <label className="flex items-center gap-2">收件明细<select className={selectClass} value={status} onChange={e => {
      setStatus(e.target.value as typeof status); setHistory([""])
    }}><option value="">全部状态</option>{Object.entries(itemStates).map(([v, name]) => <option key={v} value={v}>{name}</option>)}</select></label>
    <Table><TableHeader><TableRow>{["客户", "邮箱", "状态", "阶段 / SMTP", "响应码", "错误码", "退信提示"].map(name => <TableHead key={name}>{name}</TableHead>)}</TableRow></TableHeader>
      <TableBody>{items?.items?.map(item => <TableRow key={item.id}><TableCell>#{item.user_id}</TableCell><TableCell>{item.email_masked}</TableCell>
        <TableCell>{itemStates[item.status]}</TableCell><TableCell>{item.phase || "—"} / {item.smtp_stage || "—"}</TableCell>
        <TableCell>{item.smtp_code || "—"}</TableCell><TableCell>{item.error_code || "—"}</TableCell><TableCell>{item.hard_bounce_hint ? "疑似硬退信" : "—"}</TableCell>
      </TableRow>)}</TableBody></Table>
    {items && !items.items?.length && <p>当前状态下暂无收件明细。</p>}
    <CursorControls history={history} next={items?.next_cursor} onChange={setHistory} />
  </CardContent></Card>
}

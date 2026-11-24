import { useCallback, useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { useMailAction, useMailRead, type MailContext } from "@/lib/site-ops-mail-api"
import { mailRequestId, audienceError, audienceEstimate, positiveIds, toggleValue } from "@/lib/site-ops-mail-state"
import type { CreateMailBatchInput, MailActivityState, MailAudienceMode, MailAudiencePreview, MailAudienceSpec, MailSegmentTag, MailTemplatePreview, SiteOpsMailDraft } from "@/lib/site-ops-mail-types"
import { MailPreview } from "./mail-templates"
import { selectClass } from "./analytics-subscriptions-table"

const modes: Record<MailAudienceMode, string> = { selected: "指定客户", groups: "订阅分组", segment: "标签与活跃度", all: "全部活跃客户" }
const tags: Record<MailSegmentTag, string> = {
  high_value_at_risk: "高价值流失风险", expiring_soon: "即将到期", expired_unrenewed: "到期未续",
  cache_anomaly: "缓存异常", concurrency_queued: "并发排队", dormant_high_balance: "高余额沉睡",
}
const states: Record<MailActivityState, string> = { drop: "消费骤降", idle_balance: "余额闲置", active: "持续活跃", low: "低频" }

export function MailCompose({ api, scope, seed, smtpReady, onDirty, onCreated }: MailContext & {
  seed: SiteOpsMailDraft | null; smtpReady: boolean; onDirty: (dirty: boolean) => void; onCreated: (id: number) => void
}) {
  const [audience, setAudience] = useState<MailAudienceSpec>(() => ({ mode: "selected", user_ids: positiveIds(seed?.userIds ?? []) }))
  const [source, setSource] = useState(seed?.sourceLabel ?? "")
  const [candidatePage, setCandidatePage] = useState(1)
  const [picker, setPicker] = useState(false)
  const [templateId, setTemplateId] = useState(0)
  const [estimate, setEstimate] = useState<MailAudiencePreview>()
  const [preview, setPreview] = useState<MailTemplatePreview>()
  const [confirmation, setConfirmation] = useState<CreateMailBatchInput | null>(null)
  const templates = useMailRead(api.templates)
  const groups = useMailRead(api.groups, audience.mode === "groups")
  const candidates = useMailRead(useCallback((s: AbortSignal) => api.candidates(candidatePage, s), [api, candidatePage]), picker && audience.mode === "selected")
  const action = useMailAction()
  const selected = templates.data?.items?.find(t => t.id === templateId)
  const invalid = audienceError(audience)
  useEffect(() => {
    if (seed) onDirty(true)
    return () => onDirty(false)
  }, [seed, onDirty])

  function change(next: MailAudienceSpec) {
    setAudience(next)
    setEstimate(undefined)
    setSource("")
    onDirty(true)
  }
  function showPreview() {
    if (!selected) return
    void action.run(async s => {
      const detail = await api.template(selected.id, s)
      return api.previewTemplate({ ...scope, subject: detail.subject, html_body: detail.html_body, text_body: detail.text_body }, s)
    }, setPreview)
  }
  function prepareConfirmation() {
    if (invalid || !selected) return
    void action.run(async s => {
      const smtp = await api.smtp(s)
      if (!smtp.revision) throw Object.assign(new Error("MAIL_SMTP_NOT_READY"), { status: 409, body: { code: "MAIL_SMTP_NOT_READY" } })
      return smtp
    }, smtp => setConfirmation({
      ...scope, request_id: mailRequestId(), smtp_revision: smtp.revision, template_id: selected.id,
      template_revision: selected.revision, audience,
    }))
  }

  return <Card><CardContent className="space-y-4 pt-4 text-sm">
    <h3 className="font-semibold">发起群发</h3>
    <fieldset disabled={action.busy || !!confirmation} className="space-y-4">
      <legend className="sr-only">受众与模板</legend>
      <label className="grid max-w-xs gap-1">受众范围
        <select className={selectClass} value={audience.mode} onChange={e => change({ mode: e.target.value as MailAudienceMode })}>
          {Object.entries(modes).map(([key, label]) => <option key={key} value={key}>{label}</option>)}
        </select>
      </label>
      {audience.mode === "selected" && <div className="space-y-2">
        <p>{source || "已选择客户"} · {audience.user_ids?.length ?? 0} 人</p>
        {!!audience.user_ids?.length && <p className="max-h-20 overflow-auto break-words text-xs text-muted-foreground">UID {audience.user_ids.join("、")}</p>}
        <Button size="sm" variant="outline" onClick={() => setPicker(!picker)}>{picker ? "收起客户目录" : "从客户目录选择"}</Button>
        {picker && <div className="space-y-2 rounded-md border p-3">
          {candidates.loading && <p>正在读取客户…</p>}
          {candidates.error && <Button variant="outline" size="sm" onClick={candidates.reload}>{candidates.error} 重试</Button>}
          {candidates.data?.items?.map(c => <label className="flex items-center gap-2" key={c.user_id}>
            <input type="checkbox" checked={audience.user_ids?.includes(c.user_id) ?? false}
              onChange={() => change({ mode: "selected", user_ids: toggleValue(audience.user_ids ?? [], c.user_id) })} />#{c.user_id} · {c.email_masked}
          </label>)}
          <div className="flex items-center gap-2"><Button size="sm" variant="outline" disabled={candidatePage <= 1} onClick={() => setCandidatePage(n => n - 1)}>上一页</Button>
            第 {candidatePage} 页<Button size="sm" variant="outline" disabled={!candidates.data || candidatePage >= 100 ||
              !(candidates.data.has_more ?? (candidates.data.items?.length >= candidates.data.page_size))}
              onClick={() => setCandidatePage(n => n + 1)}>下一页</Button></div>
        </div>}
      </div>}
      {audience.mode === "groups" && <div className="space-y-2">
        <p>所选分组中当前订阅有效的客户</p>
        {groups.loading && <p>正在读取分组…</p>}
        {groups.error && <Button size="sm" variant="outline" onClick={groups.reload}>{groups.error} 重试</Button>}
        <div className="flex flex-wrap gap-3">{groups.data?.map(g => <label key={g.id}><input type="checkbox"
          checked={audience.group_ids?.includes(g.id) ?? false} onChange={() => change({ mode: "groups", group_ids: toggleValue(audience.group_ids ?? [], g.id) })} /> {g.name}</label>)}</div>
      </div>}
      {audience.mode === "segment" && <div className="space-y-2">
        <p>同类条件命中任一项即可，标签与活跃度须同时满足。</p>
        <div className="flex flex-wrap gap-3">{(Object.keys(tags) as MailSegmentTag[]).map(t => <label key={t}><input type="checkbox"
          checked={audience.tags?.includes(t) ?? false} onChange={() => change({ ...audience, tags: toggleValue(audience.tags ?? [], t) })} /> {tags[t]}</label>)}</div>
        <div className="flex flex-wrap gap-3">{(Object.keys(states) as MailActivityState[]).map(t => <label key={t}><input type="checkbox"
          checked={audience.activity_states?.includes(t) ?? false} onChange={() => change({ ...audience, activity_states: toggleValue(audience.activity_states ?? [], t) })} /> {states[t]}</label>)}</div>
      </div>}
      {audience.mode === "all" && <p>选取角色为普通用户或成员的活跃客户，自动排除退订与无效地址。</p>}
      <Button size="sm" variant="outline" disabled={!!invalid} onClick={() => action.run(s => api.previewAudience({ ...scope, audience }, s), setEstimate)}>估算受众</Button>
      {invalid && <p className="text-muted-foreground">{invalid}</p>}
      {estimate && <div role="status"><p>{audienceEstimate(estimate)}</p><p className="text-muted-foreground">{estimate.samples?.map(x => x.email_masked).join("、")}</p></div>}
      <label className="grid max-w-md gap-1">邮件模板<select className={selectClass} value={templateId} onChange={e => {
        setTemplateId(Number(e.target.value)); setPreview(undefined); onDirty(true)
      }}>
        <option value={0}>请选择模板</option>{templates.data?.items?.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
      </select></label>
      {templates.error && <Button size="sm" variant="outline" onClick={templates.reload}>{templates.error} 重试</Button>}
      <div className="flex gap-2"><Button size="sm" variant="outline" disabled={!selected} onClick={showPreview}>预览邮件</Button>
        <Button size="sm" disabled={!!invalid || !selected || !smtpReady} onClick={prepareConfirmation}>核对并群发</Button></div>
      {!smtpReady && <p>请先保存本站 SMTP 设置。</p>}
      {preview && <MailPreview preview={preview} />}
    </fieldset>
    {action.error && !confirmation && <p role="alert">{action.error}</p>}
    {confirmation && <Dialog open onOpenChange={open => { if (!open && !action.busy) setConfirmation(null) }}><DialogContent>
      <DialogTitle>确认创建发送批次？</DialogTitle>
      <DialogDescription>模板「{selected?.name}」，受众为{modes[audience.mode]}。{estimate ? audienceEstimate(estimate) : "最终人数由服务端准备批次时确定。"}</DialogDescription>
      {action.error && <p role="alert">{action.error}</p>}
      <div className="flex gap-2"><Button variant="outline" disabled={action.busy} onClick={() => setConfirmation(null)}>返回编辑</Button>
        <Button disabled={action.busy} onClick={() => action.run(s => api.createBatch(confirmation, s), batch => onCreated(batch.id))}>
          {action.busy ? "正在提交…" : "确认创建批次"}
        </Button></div>
    </DialogContent></Dialog>}
  </CardContent></Card>
}

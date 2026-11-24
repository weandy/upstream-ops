import { useCallback, useEffect, useRef, useState } from "react"
import { apiFetch, type ApiError } from "./api"
import type * as M from "./site-ops-mail-types"

export function mailErrorDetails(err: unknown) {
  const failure = err as Partial<ApiError> | null
  const body = failure?.body as Partial<M.MailErrorResponse> | undefined
  const seconds = body?.retry_after_seconds
  return {
    status: failure?.status,
    code: body?.code,
    retrySeconds: typeof seconds === "number" && Number.isFinite(seconds) && seconds > 0 ? seconds : undefined,
  }
}

export function isSiteOpsMailAuthRequiredError(err: unknown) {
  const { status, code } = mailErrorDetails(err)
  return status === 403 && code === "SITE_OPS_MAIL_AUTH_REQUIRED"
}

export function mailErrorMessage(err: unknown) {
  const { status, code, retrySeconds } = mailErrorDetails(err)
  const message = status === 409 ? "站点数据或批次状态已变化，请重新加载。" : "邮件请求失败，请重试。"
  return [message, code, retrySeconds && `请在 ${retrySeconds} 秒后重试。`].filter(Boolean).join(" ")
}

type Query = Record<string, string | number | undefined>

export function mailApi(instanceId: number, onDenied: () => void) {
  async function request<T>(path: string, signal: AbortSignal, method = "GET", input?: object, params?: Query, id = instanceId) {
    const query = new URLSearchParams()
    for (const [key, value] of Object.entries(params ?? {})) {
      if (value !== undefined && value !== "") query.set(key, String(value))
    }
    const suffix = query.size ? "?" + query : ""
    try {
      return await apiFetch<T>(`/site-ops/instances/${id}/mail/${path}${suffix}`, {
        method, signal, cache: "no-store",
        headers: method !== "GET" ? { "Content-Type": "application/json" } : undefined,
        body: input && JSON.stringify(input),
      })
    } catch (err) {
      if (!signal.aborted && isSiteOpsMailAuthRequiredError(err)) onDenied()
      throw err
    }
  }

  return {
    overview: (s: AbortSignal) => request<M.MailOverview>("overview", s),
    smtp: (s: AbortSignal) => request<M.MailSMTPView>("smtp", s),
    saveSMTP: (input: M.SaveMailSMTPInput, s: AbortSignal) => request<M.MailSMTPView>("smtp", s, "PUT", input),
    testSMTP: (input: M.MailTestInput, s: AbortSignal) => request<M.MailTestResult>("smtp/test", s, "POST", input),
    copySMTP: (input: M.MailCopyInput, s: AbortSignal) => request<M.MailSMTPView>("smtp/copy", s, "POST", input),
    templates: (s: AbortSignal, cursor = "", id = instanceId) =>
      request<M.MailCursorPage<M.MailTemplateSummary>>("templates", s, "GET", undefined, { cursor, limit: 100 }, id),
    template: (tid: number, s: AbortSignal) => request<M.MailTemplateView>(`templates/${tid}`, s),
    saveTemplate: (tid: number, input: M.SaveMailTemplateInput, s: AbortSignal) =>
      request<M.MailTemplateView>(tid ? `templates/${tid}` : "templates", s, tid ? "PUT" : "POST", input),
    deleteTemplate: (tid: number, scope: M.MailScopeInput, revision: number, s: AbortSignal) =>
      request<M.MailDeletedView>(`templates/${tid}`, s, "DELETE", undefined, { ...scope, expected_revision: revision }),
    previewTemplate: (input: M.MailTemplatePreviewInput, s: AbortSignal) =>
      request<M.MailTemplatePreview>("templates/preview", s, "POST", input),
    copyTemplate: (input: M.MailCopyInput, s: AbortSignal) => request<M.MailTemplateView>("templates/copy", s, "POST", input),
    candidates: (page: number, s: AbortSignal) => request<M.MailCandidatePage>("candidates", s, "GET", undefined, { page }),
    groups: (s: AbortSignal) => request<M.MailGroupOption[]>("groups", s),
    previewAudience: (input: M.MailAudiencePreviewInput, s: AbortSignal) => request<M.MailAudiencePreview>("audience/preview", s, "POST", input),
    createBatch: (input: M.CreateMailBatchInput, s: AbortSignal) => request<M.MailBatchView>("batches", s, "POST", input),
    batches: (cursor: string, status: M.MailBatchStatus | "", s: AbortSignal) =>
      request<M.MailCursorPage<M.MailBatchView>>("batches", s, "GET", undefined, { cursor, status, limit: 25 }),
    batch: (bid: number, s: AbortSignal) => request<M.MailBatchView>(`batches/${bid}`, s),
    items: (bid: number, cursor: string, status: M.MailItemStatus | "", s: AbortSignal) =>
      request<M.MailCursorPage<M.MailItemView>>(`batches/${bid}/items`, s, "GET", undefined, { cursor, status, limit: 50 }),
    cancelBatch: (bid: number, input: M.MailScopeInput, s: AbortSignal) => request<M.MailBatchView>(`batches/${bid}/cancel`, s, "POST", input),
    retryFailed: (bid: number, input: M.RetryMailBatchInput, s: AbortSignal) => request<M.MailBatchView>(`batches/${bid}/retry-failed`, s, "POST", input),
    suppressions: (cursor: string, s: AbortSignal) => request<M.MailCursorPage<M.MailSuppressionView>>("suppressions", s, "GET", undefined, { cursor, limit: 50 }),
    addSuppressions: (input: M.AddMailSuppressionInput, s: AbortSignal) => request<M.MailSuppressionAddResult>("suppressions", s, "POST", input),
    removeSuppression: (sid: number, scope: M.MailScopeInput, s: AbortSignal) =>
      request<M.MailDeletedView>(`suppressions/${sid}`, s, "DELETE", undefined, { ...scope }),
  }
}

export type MailClient = ReturnType<typeof mailApi>
export type MailContext = { api: MailClient; scope: M.MailScopeInput }

export function copyScope(source: M.MailCopySource, scope: M.MailScopeInput, destinationRevision: number, sourceRevision: number): M.MailCopyInput {
  return { ...scope, expected_revision: destinationRevision, source_instance_id: source.instance_id,
    source_generation: source.generation, source_cred_gen: source.cred_gen, source_revision: sourceRevision }
}

// A read result belongs to the exact query that produced it, including its cursor and site.
export function useMailRead<T>(read: (signal: AbortSignal) => Promise<T>, enabled = true) {
  const [attempt, setAttempt] = useState(0)
  const [result, setResult] = useState<{ read: typeof read; attempt: number; data?: T; error?: string }>()
  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    read(controller.signal).then(data => {
      if (!controller.signal.aborted) setResult({ read, attempt, data })
    }).catch(err => {
      if (!controller.signal.aborted) setResult(previous => ({
        read, attempt, error: mailErrorMessage(err), data: previous?.read === read ? previous.data : undefined,
      }))
    })
    return () => controller.abort()
  }, [read, enabled, attempt])
  const current = enabled && result?.read === read && result.attempt === attempt ? result : undefined
  return { data: enabled && result?.read === read ? result.data : undefined, error: current?.error, loading: enabled && !current,
    reload: useCallback(() => setAttempt(n => n + 1), []) }
}

export function useMailAction() {
  const current = useRef<AbortController | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  useEffect(() => () => current.current?.abort(), [])
  async function run<T>(work: (signal: AbortSignal) => Promise<T>, done: (value: T) => void) {
    if (current.current) return
    const controller = new AbortController()
    current.current = controller
    setBusy(true)
    setError("")
    try {
      const value = await work(controller.signal)
      if (!controller.signal.aborted) done(value)
    } catch (err) {
      if (!controller.signal.aborted) setError(mailErrorMessage(err))
    } finally {
      current.current = null
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  return { busy, error, run }
}

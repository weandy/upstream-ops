import type { MailAudienceSpec, MailAudiencePreview, MailBatchStatus, MailScopeInput, SiteOpsMailDraft } from "./site-ops-mail-types"

const previewPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; script-src 'none'; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'"

// Only pass preview_html returned by the server's link-neutralizing renderer.
export function mailPreviewDocument(previewHtml: string) {
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${previewPolicy}"></head><body>${previewHtml}</body></html>`
}

export function activeMailBatch(status: MailBatchStatus) {
  return ["preparing", "queued", "running", "cancelling"].includes(status)
}

export function mailPollDelay(failures: number, status?: number, retrySeconds?: number): number | null {
  if (!failures) return 3000
  if (status === 429 && retrySeconds && Number.isFinite(retrySeconds) && retrySeconds > 0) {
    return Math.min(2147483647, Math.max(3000, Math.ceil(retrySeconds * 1000)))
  }
  if (status && status !== 429 && status < 500) return null
  return Math.min(30000, 3000 * 2 ** Math.min(failures, 4))
}

export function matchesMailDraft(draft: SiteOpsMailDraft | null, id: number, scope: MailScopeInput, consumed: string | null): draft is SiteOpsMailDraft {
  return !!draft && !!draft.nonce && draft.nonce !== consumed && draft.instanceId === id &&
    draft.generation === scope.generation && draft.credGen === scope.cred_gen
}

export function clearMailDraft(draft: SiteOpsMailDraft | null, nonce: string) {
  return draft?.nonce === nonce ? null : draft
}

export function leaveMailDraft(dirty: boolean, confirmDiscard: () => boolean) {
  return !dirty || confirmDiscard()
}
export function confirmMailDiscard() {
  return window.confirm("放弃尚未保存的邮件草稿？")
}

// getRandomValues also works on authenticated self-hosted HTTP consoles.
export function mailRequestId() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, b => b.toString(16).padStart(2, "0")).join("")
  return [hex.slice(0, 8), hex.slice(8, 12), hex.slice(12, 16), hex.slice(16, 20), hex.slice(20)].join("-")
}

export function positiveIds(values: number[]) {
  return [...new Set(values.filter(id => Number.isSafeInteger(id) && id > 0))]
}

export function audienceError(audience: MailAudienceSpec) {
  if (audience.mode === "selected" || audience.mode === "groups") {
    const users = audience.mode === "selected"
    const ids = (users ? audience.user_ids : audience.group_ids) ?? []
    if (!ids.length || ids.length > (users ? 500 : 50) || positiveIds(ids).length !== ids.length) {
      return users ? "请选择 1 至 500 位不同的客户。" : "请选择 1 至 50 个不同的分组。"
    }
  }
  if (audience.mode === "segment" && !audience.tags?.length && !audience.activity_states?.length) return "请至少选择一个标签或活跃度。"
  return ""
}

export function audienceEstimate(result: MailAudiencePreview) {
  return `${result.is_estimate ? "预计" : "干跑匹配"} ${result.recipient_count} 人，排除 ${result.excluded_count}，重复 ${result.duplicate_count}，退订 ${result.suppressed_count}。`
}

export function byteLength(value: string) {
  return new TextEncoder().encode(value).length
}

export function toggleValue<T>(list: T[], value: T) {
  return list.includes(value) ? list.filter(item => item !== value) : [...list, value]
}

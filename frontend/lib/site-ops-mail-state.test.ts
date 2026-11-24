import assert from "node:assert/strict"
import test from "node:test"
import type { MailBatchStatus, SiteOpsMailDraft } from "./site-ops-mail-types"

const { mailRequestId, mailPreviewDocument, mailPollDelay, activeMailBatch, matchesMailDraft, clearMailDraft,
  leaveMailDraft, audienceError, audienceEstimate, positiveIds, byteLength, toggleValue }: typeof import("./site-ops-mail-state") =
  await import(new URL("./site-ops-mail-state.ts", import.meta.url).href)

test("预览文档在服务端正文前应用固定 CSP，仅允许内联样式", () => {
  const body = '<p style="color:blue">站点</p>'
  const doc = mailPreviewDocument(body)
  const policy = "default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; script-src 'none'; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'"
  assert.ok(doc.includes(`<meta http-equiv="Content-Security-Policy" content="${policy}"></head><body>${body}</body>`))
})

test("写操作使用小写 UUID v4 请求键", () => {
  const id = mailRequestId()
  assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
  assert.notEqual(id, mailRequestId())
})

test("活跃状态与终态分开，轮询按失败类型退避并在成功时恢复", () => {
  for (const status of ["preparing", "queued", "running", "cancelling"] as MailBatchStatus[]) assert.equal(activeMailBatch(status), true)
  for (const status of ["cancelled", "completed", "completed_with_errors", "failed"] as MailBatchStatus[]) assert.equal(activeMailBatch(status), false)
  assert.equal(mailPollDelay(0), 3000)
  assert.equal(mailPollDelay(1), 6000)
  assert.equal(mailPollDelay(2, 503), 12000)
  assert.equal(mailPollDelay(8, 500), 30000)
  assert.equal(mailPollDelay(1, 429, 49), 49000)
  assert.equal(mailPollDelay(2, 429, NaN), 12000)
  assert.equal(mailPollDelay(2, 403), null)
})

test("草稿只在站点和两个代际匹配时消费一次，旧回调无法清除新草稿", () => {
  const draft: SiteOpsMailDraft = { nonce: "first", instanceId: 3, generation: 4, credGen: 5, sourceLabel: "当前页已勾选 1 人", userIds: [7] }
  const scope = { generation: 4, cred_gen: 5 }
  assert.ok(matchesMailDraft(draft, 3, scope, null))
  assert.ok(!matchesMailDraft(draft, 3, scope, "first"))
  assert.ok(!matchesMailDraft(draft, 2, scope, null))
  assert.ok(!matchesMailDraft(draft, 3, { ...scope, generation: 5 }, null))
  assert.ok(!matchesMailDraft(draft, 3, { ...scope, cred_gen: 6 }, null))
  assert.ok(!matchesMailDraft(null, 3, scope, null))
  assert.equal(clearMailDraft(draft, "previous"), draft)
  assert.equal(clearMailDraft(draft, "first"), null)
})

test("离开脏草稿须确认，干净草稿直接放行", () => {
  assert.ok(leaveMailDraft(false, () => assert.fail("不应弹窗")))
  assert.ok(!leaveMailDraft(true, () => false))
  assert.ok(leaveMailDraft(true, () => true))
})

test("客户与分组校验去重、正整数和范围边界", () => {
  assert.deepEqual(positiveIds([3, 0, -1, NaN, 3, 5, 2.5, Infinity]), [3, 5])
  for (const [mode, field, max] of [["selected", "user_ids", 500], ["groups", "group_ids", 50]] as const) {
    const ids = Array.from({ length: max }, (_, i) => i + 1)
    assert.equal(audienceError({ mode, [field]: ids }), "")
    for (const bad of [[], [1, 1], [0], [1.5], [...ids, max + 1]]) assert.ok(audienceError({ mode, [field]: bad }))
  }
  assert.ok(audienceError({ mode: "segment" }))
  assert.equal(audienceError({ mode: "segment", tags: ["expiring_soon"] }), "")
  assert.equal(audienceError({ mode: "segment", activity_states: ["active"] }), "")
  assert.equal(audienceError({ mode: "all" }), "")
  assert.deepEqual(toggleValue([1, 2], 2), [1])
  assert.deepEqual(toggleValue([1], 2), [1, 2])
})

test("区别估算与完整干跑，计入重复和退订；模板大小按 UTF-8 字节", () => {
  const result = { recipient_count: 4, excluded_count: 3, duplicate_count: 2, suppressed_count: 1, samples: [], is_estimate: true }
  assert.equal(audienceEstimate(result), "预计 4 人，排除 3，重复 2，退订 1。")
  assert.match(audienceEstimate({ ...result, is_estimate: false }), /^干跑匹配 4 人/)
  assert.equal(byteLength("A中🙂"), 8)
  assert.equal(byteLength("中".repeat(8192)), 24576)
})

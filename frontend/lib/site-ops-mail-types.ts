export type MailSecurityMode = "authenticated" | "local_loopback_only"
export type MailTLSMode = "implicit" | "starttls"
export type MailAuthMode = "auto" | "plain" | "login" | "none"
export type MailBatchStatus = "preparing" | "queued" | "running" | "cancelling" | "cancelled" | "completed" | "completed_with_errors" | "failed"
export type MailItemStatus = "pending" | "sending" | "sent" | "failed" | "uncertain" | "cancelled" | "suppressed"
export type MailItemPhase = "" | "envelope" | "data" | "done"
export type MailSMTPStage = "" | "config" | "render" | "guard" | "dial" | "tls" | "ehlo" | "auth" | "mail_from" | "rcpt_to" | "data_cmd" | "data_write" | "data_commit"
export type MailAudienceMode = "selected" | "groups" | "segment" | "all"
export type MailTemplateCategory = "marketing" | "renewal" | "reactivation" | "announcement"
export type MailSuppressionReason = "admin_added" | "user_unsubscribed" | "hard_bounce"
export type MailSegmentTag = "high_value_at_risk" | "expiring_soon" | "expired_unrenewed" | "cache_anomaly" | "concurrency_queued" | "dormant_high_balance"
export type MailActivityState = "drop" | "idle_balance" | "active" | "low"
export type MailTemplateVar = "SiteName" | "SiteURL" | "UserID" | "EmailMasked" | "BalanceUSD" | "PlanName" | "ExpiresDate" | "ExpiresInDays" | "TodayDate"

export type MailBatchMode = MailAudienceMode | "retry_failed" | "test"

export type ErrorCode =
  | "SITE_OPS_MAIL_AUTH_REQUIRED"
  | "MAIL_SMTP_NOT_READY"
  | "MAIL_RESOURCE_NOT_FOUND"
  | "MAIL_TEMPLATE_INVALID"
  | "MAIL_CRYPTO_UNAVAILABLE"
  | "MAIL_INVALID_INPUT"
  | "MAIL_PREP_COVERAGE_INCOMPLETE"
  | "MAIL_PREP_LIMIT_EXCEEDED"
  | "MAIL_PREP_PAGE_DRIFT"
  | "MAIL_PREP_DEADLINE_EXCEEDED"
  | "MAIL_SMTP_CONFIG_BLOCKED"
  | "MAIL_SMTP_REJECTED"
  | "MAIL_SMTP_UNAVAILABLE"
  | "MAIL_DELIVERY_UNCERTAIN"
  | "SITE_OPS_INTERNAL_ERROR"
  | "SITE_OPS_BUDGET_EXCEEDED"
  | "SITE_OPS_RATE_LIMITED"
  | "MAIL_ACTIVE_BATCH_EXISTS"
  | "MAIL_IDEMPOTENCY_CONFLICT"
  | "MAIL_REVISION_MISMATCH"
  | "MAIL_RETRY_ALREADY_EXISTS"
  | "MAIL_NO_RETRYABLE_ITEMS"
  | "MAIL_TEMPLATE_LIMIT_REACHED"
  | "MAIL_TEMPLATE_BUILTIN_PROTECTED"
  | "MAIL_TEST_RATE_LIMITED"
  | "MAIL_BATCH_NOT_CANCELLABLE"
  | "MAIL_INSTANCE_DISABLED"
  | "MAIL_CLAIM_LOST"
  | "MAIL_LEASE_LOST"
  | "MAIL_GENERATION_MISMATCH"

export interface MailScopeInput {
  generation: number
  cred_gen: number
}

export interface MailCopySource {
  instance_id: number
  name: string
  generation: number
  cred_gen: number
  smtp_revision: number
}

export interface MailOverview {
  security_mode: MailSecurityMode
  generation: number
  cred_gen: number
  smtp_ready: boolean
  smtp_from_masked: string
  template_count: number
  suppression_count: number
  active_batch: MailBatchView | null
  copyable_sources: MailCopySource[]
}

export interface MailSMTPView {
  revision: number
  host: string
  port: number
  tls_mode: MailTLSMode
  auth_mode: MailAuthMode
  from_name: string
  from_masked: string
  has_username: boolean
  has_password: boolean
  reply_to: string
  unsubscribe_url: string
  rate_per_minute: number
  max_concurrency: number
  reconnect_every: number
}

export interface SaveMailSMTPInput extends MailScopeInput {
  expected_revision: number
  host: string
  port: number
  tls_mode: MailTLSMode
  auth_mode: MailAuthMode
  username?: string
  password?: string
  from_email?: string
  from_name: string
  reply_to: string
  unsubscribe_url: string
  rate_per_minute: number
  max_concurrency: number
  reconnect_every: number
}

export interface MailTestInput extends MailScopeInput {
  expected_revision: number
  request_id: string
}

export interface MailTestResult {
  batch_id: number
  status: MailBatchStatus
  from_masked: string
  smtp_stage: MailSMTPStage
  smtp_code: number
  error_code?: ErrorCode
}

export interface MailCopyInput extends MailScopeInput {
  expected_revision: number
  source_instance_id: number
  source_generation: number
  source_cred_gen: number
  source_revision: number
  source_template_id?: number
}

export interface MailTemplateSummary {
  id: number
  name: string
  category: MailTemplateCategory
  subject: string
  is_built_in: boolean
  revision: number
  updated_at: string
}

export interface MailTemplateView extends MailTemplateSummary {
  html_body: string
  text_body: string
}

export interface SaveMailTemplateInput extends MailScopeInput {
  expected_revision: number
  name: string
  category: MailTemplateCategory
  subject: string
  html_body: string
  text_body: string
}

export interface MailTemplatePreviewInput extends MailScopeInput {
  subject: string
  html_body: string
  text_body: string
}

export interface MailTemplatePreview {
  subject: string
  preview_html: string
  text: string
  used_vars: MailTemplateVar[]
}

export interface MailCandidate {
  user_id: number
  email_masked: string
}

export interface MailCandidatePage {
  items: MailCandidate[]
  page: number
  page_size: number
  total: number | null
  has_more: boolean | null
}

export interface MailGroupOption {
  id: number
  name: string
}

export interface MailAudienceSpec {
  mode: MailAudienceMode
  user_ids?: number[]
  group_ids?: number[]
  tags?: MailSegmentTag[]
  activity_states?: MailActivityState[]
}

export interface MailAudiencePreviewInput extends MailScopeInput {
  audience: MailAudienceSpec
}

export interface MailAudienceSample extends MailCandidate {
  plan_name?: string
  activity_state?: MailActivityState
  tags?: MailSegmentTag[]
}

export interface MailAudiencePreview {
  recipient_count: number
  excluded_count: number
  duplicate_count: number
  suppressed_count: number
  is_estimate: boolean
  samples: MailAudienceSample[]
}

export interface CreateMailBatchInput extends MailScopeInput {
  request_id: string
  smtp_revision: number
  template_id: number
  template_revision: number
  audience: MailAudienceSpec
}

export interface MailBatchView {
  id: number
  parent_batch_id: number | null
  mode: MailBatchMode
  status: MailBatchStatus
  template_id: number
  template_revision: number
  template_name: string
  smtp_revision: number
  pages: number
  scanned: number
  recipient_count: number
  excluded_count: number
  duplicate_count: number
  suppressed_count: number
  sent_count: number
  failed_count: number
  uncertain_count: number
  cancelled_count: number
  abort_reason?: ErrorCode
  created_at: string
  started_at: string | null
  finished_at: string | null
  cancelled_at: string | null
  can_cancel: boolean
  can_retry_failed: boolean
}

export interface MailItemView {
  id: number
  user_id: number
  email_masked: string
  status: MailItemStatus
  phase: MailItemPhase
  smtp_stage: MailSMTPStage
  smtp_code: number
  error_code?: ErrorCode
  hard_bounce_hint: boolean
}

export interface RetryMailBatchInput extends MailScopeInput {
  request_id: string
  smtp_revision: number
}

export interface MailSuppressionView {
  id: number
  email_masked: string
  reason: MailSuppressionReason
  created_at: string
}

export interface AddMailSuppressionInput extends MailScopeInput {
  emails?: string[]
  item_id?: number
  reason: MailSuppressionReason
}

export interface MailSuppressionAddResult {
  added: number
  existing: number
}

export interface MailCursorPage<T> {
  items: T[]
  next_cursor: string | null
}

export interface MailDeletedView {
  deleted: boolean
}

export interface MailTemplateVars {
  site_name: string
  site_url: string
  user_id: string
  email_masked: string
  balance_usd: string
  plan_name: string
  expires_date: string
  expires_in_days: string
  today_date: string
}

export interface MailErrorResponse {
  code: ErrorCode
  error: string
  field?: string
  active_batch_id?: number
  retry_after_seconds?: number
}

export interface SiteOpsMailDraft {
  nonce: string
  instanceId: number
  generation: number
  credGen: number
  userIds: number[]
  sourceLabel: string
}

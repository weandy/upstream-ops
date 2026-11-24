package siteops

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	MailMaxHTMLBytes            = 24 << 10
	MailMaxTextBytes            = 12 << 10
	MailMaxSnapshotBytes        = 60 << 10
	MailMaxMIMEBytes            = 256 << 10
	MailMaxCustomTemplates      = 50
	MailMaxSelectedUsers        = 500
	MailMaxGroups               = 50
	MailMaxRecipients           = 2000
	MailMaxDirectoryPages       = 100
	MailMaxDirectoryRows        = 10000
	MailPrepareTimeout          = 30 * time.Minute
	MailStaleClaimAge           = 10 * time.Minute
	MailDefaultRatePerMinute    = 30
	MailMaxRatePerMinute        = 300
	MailDefaultConcurrency      = 1
	MailMaxConcurrency          = 4
	MailDefaultReconnectEvery   = 50
	MailMaxReconnectEvery       = 200
	MailMaxProcessConnections   = 8
	MailPreviewMaxPages         = 5
	MailPreviewMaxSamples       = 20
	MailPreviewTimeout          = 5 * time.Second
	MailTestInterval            = time.Minute
	MailTestWait                = 20 * time.Second
	MailDialTimeout             = 5 * time.Second
	MailSendTimeout             = 30 * time.Second
	MailIdleTimeout             = 30 * time.Second
	MailShutdownGrace           = 8 * time.Second
	MailNetworkFailureLimit     = 3
	MailMaxRequestBytes         = 16 << 10
	MailMaxTemplateRequestBytes = 64 << 10
	MailMaxListLimit            = 100
	MailMaxBatchListLimit       = 50
	MailMaxSuppressionEmails    = 50
	MailMaxSafeID               = 1<<53 - 1
)

type MailSecurityMode string

const (
	MailSecurityModeAuthenticated     MailSecurityMode = "authenticated"
	MailSecurityModeLocalLoopbackOnly MailSecurityMode = "local_loopback_only"
)

type MailTLSMode string

const (
	MailTLSModeImplicit MailTLSMode = "implicit"
	MailTLSModeStarttls MailTLSMode = "starttls"
)

type MailAuthMode string

const (
	MailAuthModeAuto  MailAuthMode = "auto"
	MailAuthModePlain MailAuthMode = "plain"
	MailAuthModeLogin MailAuthMode = "login"
	MailAuthModeNone  MailAuthMode = "none"
)

type MailBatchStatus string

const (
	MailBatchStatusPreparing           MailBatchStatus = "preparing"
	MailBatchStatusQueued              MailBatchStatus = "queued"
	MailBatchStatusRunning             MailBatchStatus = "running"
	MailBatchStatusCancelling          MailBatchStatus = "cancelling"
	MailBatchStatusCancelled           MailBatchStatus = "cancelled"
	MailBatchStatusCompleted           MailBatchStatus = "completed"
	MailBatchStatusCompletedWithErrors MailBatchStatus = "completed_with_errors"
	MailBatchStatusFailed              MailBatchStatus = "failed"
)

type MailItemStatus string

const (
	MailItemStatusPending    MailItemStatus = "pending"
	MailItemStatusSending    MailItemStatus = "sending"
	MailItemStatusSent       MailItemStatus = "sent"
	MailItemStatusFailed     MailItemStatus = "failed"
	MailItemStatusUncertain  MailItemStatus = "uncertain"
	MailItemStatusCancelled  MailItemStatus = "cancelled"
	MailItemStatusSuppressed MailItemStatus = "suppressed"
)

type MailItemPhase string

const (
	MailItemPhaseUnset    MailItemPhase = ""
	MailItemPhaseEnvelope MailItemPhase = "envelope"
	MailItemPhaseData     MailItemPhase = "data"
	MailItemPhaseDone     MailItemPhase = "done"
)

type MailSMTPStage string

const (
	MailSMTPStageUnset      MailSMTPStage = ""
	MailSMTPStageConfig     MailSMTPStage = "config"
	MailSMTPStageRender     MailSMTPStage = "render"
	MailSMTPStageGuard      MailSMTPStage = "guard"
	MailSMTPStageDial       MailSMTPStage = "dial"
	MailSMTPStageTLS        MailSMTPStage = "tls"
	MailSMTPStageEHLO       MailSMTPStage = "ehlo"
	MailSMTPStageAuth       MailSMTPStage = "auth"
	MailSMTPStageMailFrom   MailSMTPStage = "mail_from"
	MailSMTPStageRCPTTo     MailSMTPStage = "rcpt_to"
	MailSMTPStageDataCmd    MailSMTPStage = "data_cmd"
	MailSMTPStageDataWrite  MailSMTPStage = "data_write"
	MailSMTPStageDataCommit MailSMTPStage = "data_commit"
)

type MailAudienceMode string

const (
	MailAudienceModeSelected MailAudienceMode = "selected"
	MailAudienceModeGroups   MailAudienceMode = "groups"
	MailAudienceModeSegment  MailAudienceMode = "segment"
	MailAudienceModeAll      MailAudienceMode = "all"
)

type MailTemplateCategory string

const (
	MailTemplateCategoryMarketing    MailTemplateCategory = "marketing"
	MailTemplateCategoryRenewal      MailTemplateCategory = "renewal"
	MailTemplateCategoryReactivation MailTemplateCategory = "reactivation"
	MailTemplateCategoryAnnouncement MailTemplateCategory = "announcement"
)

type MailSuppressionReason string

const (
	MailSuppressionReasonAdminAdded       MailSuppressionReason = "admin_added"
	MailSuppressionReasonUserUnsubscribed MailSuppressionReason = "user_unsubscribed"
	MailSuppressionReasonHardBounce       MailSuppressionReason = "hard_bounce"
)

type MailSegmentTag string

const (
	MailSegmentTagHighValueAtRisk    MailSegmentTag = "high_value_at_risk"
	MailSegmentTagExpiringSoon       MailSegmentTag = "expiring_soon"
	MailSegmentTagExpiredUnrenewed   MailSegmentTag = "expired_unrenewed"
	MailSegmentTagCacheAnomaly       MailSegmentTag = "cache_anomaly"
	MailSegmentTagConcurrencyQueued  MailSegmentTag = "concurrency_queued"
	MailSegmentTagDormantHighBalance MailSegmentTag = "dormant_high_balance"
)

type MailActivityState string

const (
	MailActivityStateDrop        MailActivityState = "drop"
	MailActivityStateIdleBalance MailActivityState = "idle_balance"
	MailActivityStateActive      MailActivityState = "active"
	MailActivityStateLow         MailActivityState = "low"
)

type MailTemplateVar string

const (
	MailTemplateVarSiteName      MailTemplateVar = "SiteName"
	MailTemplateVarSiteURL       MailTemplateVar = "SiteURL"
	MailTemplateVarUserID        MailTemplateVar = "UserID"
	MailTemplateVarEmailMasked   MailTemplateVar = "EmailMasked"
	MailTemplateVarBalanceUSD    MailTemplateVar = "BalanceUSD"
	MailTemplateVarPlanName      MailTemplateVar = "PlanName"
	MailTemplateVarExpiresDate   MailTemplateVar = "ExpiresDate"
	MailTemplateVarExpiresInDays MailTemplateVar = "ExpiresInDays"
	MailTemplateVarTodayDate     MailTemplateVar = "TodayDate"
)

type MailBatchMode string

const (
	MailBatchModeSelected                  = MailBatchMode(MailAudienceModeSelected)
	MailBatchModeGroups                    = MailBatchMode(MailAudienceModeGroups)
	MailBatchModeSegment                   = MailBatchMode(MailAudienceModeSegment)
	MailBatchModeAll                       = MailBatchMode(MailAudienceModeAll)
	MailBatchModeRetryFailed MailBatchMode = "retry_failed"
	MailBatchModeTest        MailBatchMode = "test"
)

// ErrorCode is safe for API responses and persisted SMTP evidence.
type ErrorCode string

const (
	ErrCodeMailAuthRequired             ErrorCode = "SITE_OPS_MAIL_AUTH_REQUIRED"
	ErrCodeMailSMTPNotReady             ErrorCode = "MAIL_SMTP_NOT_READY"
	ErrCodeMailResourceNotFound         ErrorCode = "MAIL_RESOURCE_NOT_FOUND"
	ErrCodeMailTemplateInvalid          ErrorCode = "MAIL_TEMPLATE_INVALID"
	ErrCodeMailCryptoUnavailable        ErrorCode = "MAIL_CRYPTO_UNAVAILABLE"
	ErrCodeMailInvalidInput             ErrorCode = "MAIL_INVALID_INPUT"
	ErrCodeMailPrepCoverageIncomplete   ErrorCode = "MAIL_PREP_COVERAGE_INCOMPLETE"
	ErrCodeMailPrepLimitExceeded        ErrorCode = "MAIL_PREP_LIMIT_EXCEEDED"
	ErrCodeMailPrepPageDrift            ErrorCode = "MAIL_PREP_PAGE_DRIFT"
	ErrCodeMailPrepDeadlineExceeded     ErrorCode = "MAIL_PREP_DEADLINE_EXCEEDED"
	ErrCodeMailSMTPConfigBlocked        ErrorCode = "MAIL_SMTP_CONFIG_BLOCKED"
	ErrCodeMailSMTPRejected             ErrorCode = "MAIL_SMTP_REJECTED"
	ErrCodeMailSMTPUnavailable          ErrorCode = "MAIL_SMTP_UNAVAILABLE"
	ErrCodeMailDeliveryUncertain        ErrorCode = "MAIL_DELIVERY_UNCERTAIN"
	ErrCodeMailInternal                 ErrorCode = "SITE_OPS_INTERNAL_ERROR"
	ErrCodeMailBudgetExceeded           ErrorCode = "SITE_OPS_BUDGET_EXCEEDED"
	ErrCodeMailRateLimited              ErrorCode = "SITE_OPS_RATE_LIMITED"
	ErrCodeMailActiveBatchExists        ErrorCode = "MAIL_ACTIVE_BATCH_EXISTS"
	ErrCodeMailIdempotencyConflict      ErrorCode = "MAIL_IDEMPOTENCY_CONFLICT"
	ErrCodeMailRevisionMismatch         ErrorCode = "MAIL_REVISION_MISMATCH"
	ErrCodeMailRetryAlreadyExists       ErrorCode = "MAIL_RETRY_ALREADY_EXISTS"
	ErrCodeMailNoRetryableItems         ErrorCode = "MAIL_NO_RETRYABLE_ITEMS"
	ErrCodeMailTemplateLimitReached     ErrorCode = "MAIL_TEMPLATE_LIMIT_REACHED"
	ErrCodeMailTemplateBuiltinProtected ErrorCode = "MAIL_TEMPLATE_BUILTIN_PROTECTED"
	ErrCodeMailTestRateLimited          ErrorCode = "MAIL_TEST_RATE_LIMITED"
	ErrCodeMailBatchNotCancellable      ErrorCode = "MAIL_BATCH_NOT_CANCELLABLE"
	ErrCodeMailInstanceDisabled         ErrorCode = "MAIL_INSTANCE_DISABLED"
	ErrCodeMailClaimLost                ErrorCode = "MAIL_CLAIM_LOST"
	ErrCodeMailLeaseLost                ErrorCode = "MAIL_LEASE_LOST"
	ErrCodeMailGenerationMismatch       ErrorCode = "MAIL_GENERATION_MISMATCH"
)

var (
	ErrMailResourceNotFound         = storage.ErrMailNotFound
	ErrMailActiveBatchExists        = storage.ErrMailActiveBatchExists
	ErrMailIdempotencyConflict      = storage.ErrMailIdempotencyConflict
	ErrMailRevisionMismatch         = storage.ErrMailRevisionMismatch
	ErrMailRetryAlreadyExists       = storage.ErrMailRetryAlreadyExists
	ErrMailNoRetryableItems         = storage.ErrMailNoRetryableItems
	ErrMailTemplateLimitReached     = storage.ErrMailTemplateLimitReached
	ErrMailTemplateBuiltinProtected = storage.ErrMailTemplateBuiltinProtected
	ErrMailTestRateLimited          = storage.ErrMailTestRateLimited
	ErrMailBatchNotCancellable      = storage.ErrMailBatchNotCancellable
	ErrMailInstanceDisabled         = storage.ErrMailInstanceDisabled
	ErrMailClaimLost                = storage.ErrMailClaimLost
	ErrMailLeaseLost                = storage.ErrMailLeaseLost
	ErrMailGenerationMismatch       = storage.ErrMailGenerationMismatch
	ErrMailAuthRequired             = errors.New(string(ErrCodeMailAuthRequired))
	ErrMailSMTPNotReady             = errors.New(string(ErrCodeMailSMTPNotReady))
	ErrMailTemplateInvalid          = errors.New(string(ErrCodeMailTemplateInvalid))
	ErrMailCryptoUnavailable        = errors.New(string(ErrCodeMailCryptoUnavailable))
	ErrMailInvalidInput             = errors.New(string(ErrCodeMailInvalidInput))
	ErrMailPrepCoverageIncomplete   = errors.New(string(ErrCodeMailPrepCoverageIncomplete))
	ErrMailPrepLimitExceeded        = errors.New(string(ErrCodeMailPrepLimitExceeded))
	ErrMailPrepPageDrift            = errors.New(string(ErrCodeMailPrepPageDrift))
	ErrMailPrepDeadlineExceeded     = errors.New(string(ErrCodeMailPrepDeadlineExceeded))
	ErrMailSMTPConfigBlocked        = errors.New(string(ErrCodeMailSMTPConfigBlocked))
	ErrMailSMTPRejected             = errors.New(string(ErrCodeMailSMTPRejected))
	ErrMailSMTPUnavailable          = errors.New(string(ErrCodeMailSMTPUnavailable))
	ErrMailDeliveryUncertain        = errors.New(string(ErrCodeMailDeliveryUncertain))
)

type MailScopeInput struct {
	Generation int64 `json:"generation"`
	CredGen    int64 `json:"cred_gen"`
}

type MailCopySource struct {
	InstanceID   uint   `json:"instance_id"`
	Name         string `json:"name"`
	Generation   int64  `json:"generation"`
	CredGen      int64  `json:"cred_gen"`
	SMTPRevision int64  `json:"smtp_revision"`
}

type MailOverview struct {
	SecurityMode     MailSecurityMode `json:"security_mode"`
	Generation       int64            `json:"generation"`
	CredGen          int64            `json:"cred_gen"`
	SMTPReady        bool             `json:"smtp_ready"`
	SMTPFromMasked   string           `json:"smtp_from_masked"`
	TemplateCount    int              `json:"template_count"`
	SuppressionCount int              `json:"suppression_count"`
	ActiveBatch      *MailBatchView   `json:"active_batch"`
	CopyableSources  []MailCopySource `json:"copyable_sources"`
}

type MailSMTPView struct {
	Revision       int64        `json:"revision"`
	Host           string       `json:"host"`
	Port           int          `json:"port"`
	TLSMode        MailTLSMode  `json:"tls_mode"`
	AuthMode       MailAuthMode `json:"auth_mode"`
	FromName       string       `json:"from_name"`
	FromMasked     string       `json:"from_masked"`
	HasUsername    bool         `json:"has_username"`
	HasPassword    bool         `json:"has_password"`
	ReplyTo        string       `json:"reply_to"`
	UnsubscribeURL string       `json:"unsubscribe_url"`
	RatePerMinute  int          `json:"rate_per_minute"`
	MaxConcurrency int          `json:"max_concurrency"`
	ReconnectEvery int          `json:"reconnect_every"`
}

type SaveMailSMTPInput struct {
	MailScopeInput
	ExpectedRevision int64        `json:"expected_revision"`
	Host             string       `json:"host"`
	Port             int          `json:"port"`
	TLSMode          MailTLSMode  `json:"tls_mode"`
	AuthMode         MailAuthMode `json:"auth_mode"`
	Username         *string      `json:"username,omitempty"`
	Password         *string      `json:"password,omitempty"`
	FromEmail        *string      `json:"from_email,omitempty"`
	FromName         string       `json:"from_name"`
	ReplyTo          string       `json:"reply_to"`
	UnsubscribeURL   string       `json:"unsubscribe_url"`
	RatePerMinute    int          `json:"rate_per_minute"`
	MaxConcurrency   int          `json:"max_concurrency"`
	ReconnectEvery   int          `json:"reconnect_every"`
}

type MailTestInput struct {
	MailScopeInput
	ExpectedRevision int64  `json:"expected_revision"`
	RequestID        string `json:"request_id"`
}

type MailTestResult struct {
	BatchID    uint            `json:"batch_id"`
	Status     MailBatchStatus `json:"status"`
	FromMasked string          `json:"from_masked"`
	SMTPStage  MailSMTPStage   `json:"smtp_stage"`
	SMTPCode   int             `json:"smtp_code"`
	ErrorCode  ErrorCode       `json:"error_code,omitempty"`
}

type MailCopyInput struct {
	MailScopeInput
	ExpectedRevision int64 `json:"expected_revision"`
	SourceInstanceID uint  `json:"source_instance_id"`
	SourceGeneration int64 `json:"source_generation"`
	SourceCredGen    int64 `json:"source_cred_gen"`
	SourceRevision   int64 `json:"source_revision"`
	SourceTemplateID uint  `json:"source_template_id,omitempty"`
}

type MailTemplateSummary struct {
	ID        uint                 `json:"id"`
	Name      string               `json:"name"`
	Category  MailTemplateCategory `json:"category"`
	Subject   string               `json:"subject"`
	IsBuiltIn bool                 `json:"is_built_in"`
	Revision  int64                `json:"revision"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type MailTemplateView struct {
	MailTemplateSummary
	HTMLBody string `json:"html_body"`
	TextBody string `json:"text_body"`
}

type SaveMailTemplateInput struct {
	MailScopeInput
	ExpectedRevision int64                `json:"expected_revision"`
	Name             string               `json:"name"`
	Category         MailTemplateCategory `json:"category"`
	Subject          string               `json:"subject"`
	HTMLBody         string               `json:"html_body"`
	TextBody         string               `json:"text_body"`
}

type MailTemplatePreviewInput struct {
	MailScopeInput
	Subject  string `json:"subject"`
	HTMLBody string `json:"html_body"`
	TextBody string `json:"text_body"`
}

type MailTemplatePreview struct {
	Subject     string            `json:"subject"`
	PreviewHTML string            `json:"preview_html"`
	Text        string            `json:"text"`
	UsedVars    []MailTemplateVar `json:"used_vars"`
}

type MailCandidate struct {
	UserID      int64  `json:"user_id"`
	EmailMasked string `json:"email_masked"`
}

type MailCandidatePage struct {
	Items    []MailCandidate `json:"items"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    *int64          `json:"total"`
	HasMore  *bool           `json:"has_more"`
}

type MailGroupOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type MailAudienceSpec struct {
	Mode           MailAudienceMode    `json:"mode"`
	UserIDs        []int64             `json:"user_ids,omitempty"`
	GroupIDs       []int64             `json:"group_ids,omitempty"`
	Tags           []MailSegmentTag    `json:"tags,omitempty"`
	ActivityStates []MailActivityState `json:"activity_states,omitempty"`
}

type MailAudiencePreviewInput struct {
	MailScopeInput
	Audience MailAudienceSpec `json:"audience"`
}

type MailAudienceSample struct {
	MailCandidate
	PlanName      string            `json:"plan_name,omitempty"`
	ActivityState MailActivityState `json:"activity_state,omitempty"`
	Tags          []MailSegmentTag  `json:"tags,omitempty"`
}

type MailAudiencePreview struct {
	RecipientCount  int                  `json:"recipient_count"`
	ExcludedCount   int                  `json:"excluded_count"`
	DuplicateCount  int                  `json:"duplicate_count"`
	SuppressedCount int                  `json:"suppressed_count"`
	IsEstimate      bool                 `json:"is_estimate"`
	Samples         []MailAudienceSample `json:"samples"`
}

type CreateMailBatchInput struct {
	MailScopeInput
	RequestID        string           `json:"request_id"`
	SMTPRevision     int64            `json:"smtp_revision"`
	TemplateID       uint             `json:"template_id"`
	TemplateRevision int64            `json:"template_revision"`
	Audience         MailAudienceSpec `json:"audience"`
}

type MailBatchView struct {
	ID               uint            `json:"id"`
	ParentBatchID    *uint           `json:"parent_batch_id"`
	Mode             MailBatchMode   `json:"mode"`
	Status           MailBatchStatus `json:"status"`
	TemplateID       uint            `json:"template_id"`
	TemplateRevision int64           `json:"template_revision"`
	TemplateName     string          `json:"template_name"`
	SMTPRevision     int64           `json:"smtp_revision"`
	Pages            int             `json:"pages"`
	Scanned          int             `json:"scanned"`
	RecipientCount   int             `json:"recipient_count"`
	ExcludedCount    int             `json:"excluded_count"`
	DuplicateCount   int             `json:"duplicate_count"`
	SuppressedCount  int             `json:"suppressed_count"`
	SentCount        int             `json:"sent_count"`
	FailedCount      int             `json:"failed_count"`
	UncertainCount   int             `json:"uncertain_count"`
	CancelledCount   int             `json:"cancelled_count"`
	AbortReason      ErrorCode       `json:"abort_reason,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	StartedAt        *time.Time      `json:"started_at"`
	FinishedAt       *time.Time      `json:"finished_at"`
	CancelledAt      *time.Time      `json:"cancelled_at"`
	CanCancel        bool            `json:"can_cancel"`
	CanRetryFailed   bool            `json:"can_retry_failed"`
}

type MailItemView struct {
	ID             uint           `json:"id"`
	UserID         int64          `json:"user_id"`
	EmailMasked    string         `json:"email_masked"`
	Status         MailItemStatus `json:"status"`
	Phase          MailItemPhase  `json:"phase"`
	SMTPStage      MailSMTPStage  `json:"smtp_stage"`
	SMTPCode       int            `json:"smtp_code"`
	ErrorCode      ErrorCode      `json:"error_code,omitempty"`
	HardBounceHint bool           `json:"hard_bounce_hint"`
}

type RetryMailBatchInput struct {
	MailScopeInput
	RequestID    string `json:"request_id"`
	SMTPRevision int64  `json:"smtp_revision"`
}

type MailSuppressionView struct {
	ID          uint                  `json:"id"`
	EmailMasked string                `json:"email_masked"`
	Reason      MailSuppressionReason `json:"reason"`
	CreatedAt   time.Time             `json:"created_at"`
}

type AddMailSuppressionInput struct {
	MailScopeInput
	Emails []string              `json:"emails,omitempty"`
	ItemID uint                  `json:"item_id,omitempty"`
	Reason MailSuppressionReason `json:"reason"`
}

type MailSuppressionAddResult struct {
	Added    int `json:"added"`
	Existing int `json:"existing"`
}

type MailCursorPage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type MailDeletedView struct {
	Deleted bool `json:"deleted"`
}

type MailTemplateVars struct {
	SiteName      string `json:"site_name"`
	SiteURL       string `json:"site_url"`
	UserID        string `json:"user_id"`
	EmailMasked   string `json:"email_masked"`
	BalanceUSD    string `json:"balance_usd"`
	PlanName      string `json:"plan_name"`
	ExpiresDate   string `json:"expires_date"`
	ExpiresInDays string `json:"expires_in_days"`
	TodayDate     string `json:"today_date"`
}

type MailErrorResponse struct {
	Code              ErrorCode `json:"code"`
	Error             string    `json:"error"`
	Field             string    `json:"field,omitempty"`
	ActiveBatchID     uint      `json:"active_batch_id,omitempty"`
	RetryAfterSeconds int       `json:"retry_after_seconds,omitempty"`
}

// MailStore composes the mail-specific persistence contracts used by the service.
// Workers and settings handlers can depend on their respective subsets.
type MailStore interface {
	MailSettingsStore
	MailBatchStore
	MailWorkerStore
}

type MailSettingsStore interface {
	GetMailOverview(ctx context.Context, scope storage.MailScopeKey) (storage.MailOverviewRows, error)
	// Return the winning row on concurrent creation. Never replace an existing seed.
	EnsureMailScope(ctx context.Context, scope storage.MailScopeKey, seedCipher string) (storage.SiteOpsMailScope, error)
	GetMailSMTP(ctx context.Context, scope storage.MailScopeKey) (storage.SiteOpsMailSMTP, error)
	SaveMailSMTP(ctx context.Context, scope storage.MailScopeKey, expectedRevision int64, row storage.SiteOpsMailSMTP) (storage.SiteOpsMailSMTP, error)
	CopyMailSMTP(ctx context.Context, p storage.MailCopySMTPParams) (storage.SiteOpsMailSMTP, error)

	EnsureMailBuiltinTemplates(ctx context.Context, scope storage.MailScopeKey, rows []storage.SiteOpsMailTemplate) error
	ListMailTemplates(ctx context.Context, p storage.MailListParams) ([]storage.SiteOpsMailTemplate, error)
	GetMailTemplate(ctx context.Context, scope storage.MailScopeKey, id uint) (storage.SiteOpsMailTemplate, error)
	SaveMailTemplate(ctx context.Context, scope storage.MailScopeKey, expectedRevision int64, row storage.SiteOpsMailTemplate) (storage.SiteOpsMailTemplate, error)
	CopyMailTemplate(ctx context.Context, p storage.MailCopyTemplateParams) (storage.SiteOpsMailTemplate, error)
	DeleteMailTemplate(ctx context.Context, scope storage.MailScopeKey, id uint, expectedRevision int64) error

	ListMailSuppressions(ctx context.Context, p storage.MailListParams) ([]storage.SiteOpsMailSuppression, error)
	FindSuppressedMailHashes(ctx context.Context, scope storage.MailScopeKey, hashes []string) (map[string]bool, error)
	AddMailSuppressions(ctx context.Context, scope storage.MailScopeKey, rows []storage.MailSuppressionEntry) (added int, err error)
	AddMailSuppressionFromItem(ctx context.Context, scope storage.MailScopeKey, itemID uint, reason string) (added bool, err error)
	DeleteMailSuppression(ctx context.Context, scope storage.MailScopeKey, id uint) error
}

type MailBatchStore interface {
	CreateMailBatch(ctx context.Context, p storage.MailBatchCreateParams) (storage.MailBatchCreateResult, error)
	GetMailBatch(ctx context.Context, scope storage.MailScopeKey, id uint) (storage.SiteOpsMailBatch, error)
	ListMailBatches(ctx context.Context, p storage.MailListParams) ([]storage.SiteOpsMailBatch, error)
	ListMailItems(ctx context.Context, batchID uint, p storage.MailListParams) ([]storage.SiteOpsMailItem, error)
	CancelMailBatch(ctx context.Context, scope storage.MailScopeKey, batchID uint, now time.Time) (storage.MailCancelResult, error)
}

type MailWorkerStore interface {
	// Lease the next due batch and transition queued to running. On preparing takeover, clear staging and keep the original deadline.
	LeaseMailBatch(ctx context.Context, owner string, now, leaseUntil time.Time) (*storage.SiteOpsMailBatch, error)
	RenewMailLease(ctx context.Context, work storage.MailWorkKey, leaseUntil time.Time) error
	StageMailItems(ctx context.Context, work storage.MailWorkKey, rows []storage.MailItemStageRow) error
	FinishMailPreparation(ctx context.Context, work storage.MailWorkKey, counts storage.MailPrepareCounts) error
	AbortMailBatch(ctx context.Context, work storage.MailWorkKey, errorCode string) error

	ClaimMailItem(ctx context.Context, p storage.MailClaimParams) (*storage.SiteOpsMailItem, error)
	// A false permission commits the cancellation/suppression and forbids the DATA command.
	AuthorizeMailData(ctx context.Context, p storage.MailAuthorizeDataParams) (bool, error)
	CompleteMailItem(ctx context.Context, p storage.MailCompleteItemParams) error
	RecoverStaleMailItems(ctx context.Context, before, now time.Time) (storage.MailRecoverResult, error)
}

// Missing pagination metadata stays distinguishable from zero/false during drift checks.
type MailDirectoryPage[T any] struct {
	Items          []T
	Page, PageSize *int
	Total          *int64
	HasMore        *bool
	Truncated      bool
}

// Project an address inside the parser, while the original bytes are still private.
// The caller can seal it for staging, or return just its hash and mask for a dry run.
type MailAddressProjection struct {
	EmailHash, EmailMasked, PayloadCipher string
}
type MailProjectAddress func(user MailDirectoryUser, email string) (MailAddressProjection, error)

type MailDirectoryUser struct {
	ID               int64
	Role, Status     string
	BalanceUSD       *float64
	CreatedAt        *time.Time
	ConcurrencyLimit *int
	Address          MailAddressProjection
}

type MailDirectorySubscription struct {
	ID, UserID, GroupID            int64
	GroupName, Status              string
	StartsAt, ExpiresAt, RevokedAt *time.Time
}

type MailAudienceClient interface {
	FetchMailUsersPage(ctx context.Context, inst TargetEndpoint, page int, project MailProjectAddress) (MailDirectoryPage[MailDirectoryUser], UpstreamFetchMeta, error)
	FetchMailSubscriptionsPage(ctx context.Context, inst TargetEndpoint, page int) (MailDirectoryPage[MailDirectorySubscription], UpstreamFetchMeta, error)
	FetchAnalyticsGroups(ctx context.Context, inst TargetEndpoint) ([]SanitizedGroupInfo, UpstreamFetchMeta, error)
	FetchUserConcurrency(ctx context.Context, inst TargetEndpoint) (SanitizedUserConcurrencyMap, UpstreamFetchMeta, error)
	FetchUsersUsageBatch(ctx context.Context, inst TargetEndpoint, ids []int64) (map[int64]SanitizedUserQuickUsage, UpstreamFetchMeta, error)
	FetchUsageStats(ctx context.Context, inst TargetEndpoint, q AnalyticsStatsQuery) (*SanitizedUsageStats, UpstreamFetchMeta, error)
	BudgetHeadroomForAnalytics(instanceID uint, dailyLimit int) (available, minuteReserve, dayReserve int)
}

type SMTPOutboundValidator interface {
	ValidateSMTPHostPort(host string, port int) error
	DialSMTPContext(ctx context.Context, host string, port int) (net.Conn, error)
}

// Decrypted authentication values exist only for the lifetime of a sending session.
type MailSMTPConnectionConfig struct {
	Host               string
	Port               int
	TLSMode            MailTLSMode
	AuthMode           MailAuthMode
	Username, Password string `json:"-"`
}

// MailSMTPFailure carries only the evidence used to classify an SMTP failure.
type MailSMTPFailure struct {
	Stage    MailSMTPStage
	SMTPCode int // Explicit peer reply, or zero for a local/transport error.
	Code     ErrorCode
}

func (f *MailSMTPFailure) Error() string { return string(f.Code) }

type MailSMTPSender interface {
	Connect(ctx context.Context, cfg MailSMTPConnectionConfig) (MailSMTPSession, error)
}

type MailSMTPSession interface {
	Envelope(ctx context.Context, from, to string) error
	// SendData is called only after the durable authorization transaction commits.
	// A nil error means the server confirmed receipt with its final success reply.
	SendData(ctx context.Context, message []byte) error
	Reset(ctx context.Context) error
	// Close interrupts a blocked send during forced shutdown.
	Close() error
}

package storage

import (
	"errors"
	"time"
)

// SiteOpsMailScope 单站点目标绑定的独立加密域，持有 32 字节随机 HMAC 种子密文。
// 同一实例改址 A→B→A 时按 TargetBinding 隔离；切回 A 自动恢复 A 的退订哈希域。
type SiteOpsMailScope struct {
	InstanceID    uint      `gorm:"primaryKey;autoIncrement:false"`
	TargetBinding string    `gorm:"primaryKey;size:64;not null"`
	SeedCipher    string    `gorm:"type:text;not null"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
}

func (SiteOpsMailScope) TableName() string { return "site_ops_mail_scopes" }

// SiteOpsMailSMTP 当前实例的 SMTP 发信配置（单实例单行，改址事务内销毁旧行防误用旧凭据）。
type SiteOpsMailSMTP struct {
	InstanceID     uint      `gorm:"primaryKey;autoIncrement:false"`
	TargetBinding  string    `gorm:"size:64;not null"`
	Revision       int64     `gorm:"not null"`
	Host           string    `gorm:"size:253;not null"`
	Port           int       `gorm:"not null"`
	TLSMode        string    `gorm:"size:12;not null"`   // "implicit" | "starttls"
	AuthMode       string    `gorm:"size:8;not null"`    // "auto" | "plain" | "login" | "none"
	IdentityCipher string    `gorm:"type:text;not null"` // 密封 {purpose,instance_id,target_binding,host,port,revision,username,from_email}
	PasswordCipher string    `gorm:"type:text;not null"` // 密封 {purpose,instance_id,target_binding,host,port,revision,username,password}，无密码为空串
	HasUsername    bool      `gorm:"not null"`
	HasPassword    bool      `gorm:"not null"`
	FromMasked     string    `gorm:"size:160;not null"`
	FromName       string    `gorm:"size:120;not null"`
	ReplyTo        string    `gorm:"size:254;not null"`
	UnsubscribeURL string    `gorm:"size:512;not null"` // 可选同站 HTTPS 或 mailto: 静态退订地址
	RatePerMinute  int       `gorm:"not null"`          // 默认 30 [1, 300]
	MaxConcurrency int       `gorm:"not null"`          // 默认 1  [1, 4]
	ReconnectEvery int       `gorm:"not null"`          // 默认 50 [1, 200]
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (SiteOpsMailSMTP) TableName() string { return "site_ops_mail_smtp" }

// SiteOpsMailTemplate 目标绑定的邮件模板（每目标内置 3 套 + 自定义上限 50 套）。
type SiteOpsMailTemplate struct {
	ID            uint      `gorm:"primaryKey"`
	InstanceID    uint      `gorm:"not null;uniqueIndex:uniq_som_tpl_key,priority:1;index:idx_som_tpl_scope,priority:1"`
	TargetBinding string    `gorm:"size:64;not null;uniqueIndex:uniq_som_tpl_key,priority:2;index:idx_som_tpl_scope,priority:2"`
	TemplateKey   string    `gorm:"size:64;not null;uniqueIndex:uniq_som_tpl_key,priority:3"` // "builtin:<slug>" | "custom:<hex16>"
	Revision      int64     `gorm:"not null"`
	Name          string    `gorm:"size:96;not null"`
	Category      string    `gorm:"size:24;not null"` // "marketing" | "renewal" | "reactivation" | "announcement"
	Subject       string    `gorm:"size:255;not null"`
	HTMLBody      string    `gorm:"type:text;not null"` // <= 24 KiB（适配 MySQL TEXT）
	TextBody      string    `gorm:"type:text;not null"` // <= 12 KiB，可空（自动降级生成）
	IsBuiltIn     bool      `gorm:"not null"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
}

func (SiteOpsMailTemplate) TableName() string { return "site_ops_mail_templates" }

// SiteOpsMailSuppression 目标绑定的本地退订/免打扰名单（永久零明文邮箱）。
type SiteOpsMailSuppression struct {
	ID            uint      `gorm:"primaryKey"`
	InstanceID    uint      `gorm:"not null;uniqueIndex:uniq_som_supp,priority:1;index:idx_som_supp_list,priority:1"`
	TargetBinding string    `gorm:"size:64;not null;uniqueIndex:uniq_som_supp,priority:2;index:idx_som_supp_list,priority:2"`
	EmailHash     string    `gorm:"size:64;not null;uniqueIndex:uniq_som_supp,priority:3"`
	EmailMasked   string    `gorm:"size:160;not null"`
	Reason        string    `gorm:"size:32;not null"` // "admin_added" | "user_unsubscribed" | "hard_bounce"
	CreatedAt     time.Time `gorm:"not null;index:idx_som_supp_list,priority:3"`
}

func (SiteOpsMailSuppression) TableName() string { return "site_ops_mail_suppressions" }

// SiteOpsMailBatch 营销/测试批次主表（终态后默认保留 30 天）。
// ActiveSlot 在活跃态 (preparing/queued/running/cancelling) 恒为 &InstanceID，
// 仅当全部 pending 与 in-flight DATA 归零进入终态时在同事务置 nil；跨三库严格保证每实例至多 1 个活跃批。
type SiteOpsMailBatch struct {
	ID               uint   `gorm:"primaryKey"`
	InstanceID       uint   `gorm:"not null;uniqueIndex:uniq_som_req,priority:1;index:idx_som_batch_list,priority:1"`
	TargetBinding    string `gorm:"size:64;not null;uniqueIndex:uniq_som_req,priority:2;index:idx_som_batch_list,priority:2"`
	RequestID        string `gorm:"size:64;not null;uniqueIndex:uniq_som_req,priority:3"`
	RequestDigest    string `gorm:"size:64;not null"`
	ActiveSlot       *uint  `gorm:"uniqueIndex:uniq_som_active_slot"`  // 活跃 = &InstanceID；终态 = nil
	ParentBatchID    *uint  `gorm:"uniqueIndex:uniq_som_parent_retry"` // 每终态批至多 1 个补发子批（多 NULL 三库兼容）
	Generation       int64  `gorm:"not null"`
	CredGen          int64  `gorm:"not null"`
	SMTPRevision     int64  `gorm:"not null"`
	TemplateID       uint   `gorm:"not null"`
	TemplateRevision int64  `gorm:"not null"`
	TemplateName     string `gorm:"size:96;not null"`
	Mode             string `gorm:"size:20;not null"`   // "selected" | "groups" | "segment" | "all" | "retry_failed" | "test"
	SnapshotJSON     string `gorm:"type:text;not null"` // <= 60 KiB: {template_name,subject,html,text,site_name,site_url,send_date}
	AudienceJSON     string `gorm:"type:text;not null"` // 封闭选择器；终态清理手选 user_ids
	Status           string `gorm:"size:24;not null;index:idx_som_work,priority:1"`
	AbortReason      string `gorm:"size:64;not null"`
	Owner            string `gorm:"size:36;not null"`
	LeaseUntil       *time.Time
	NextWorkAt       time.Time `gorm:"not null;index:idx_som_work,priority:2"`
	PrepareBy        time.Time `gorm:"not null"`
	Pages            int       `gorm:"not null"`
	Scanned          int       `gorm:"not null"`
	RecipientCount   int       `gorm:"not null"`
	ExcludedCount    int       `gorm:"not null"`
	DuplicateCount   int       `gorm:"not null"`
	SuppressedCount  int       `gorm:"not null"`
	SentCount        int       `gorm:"not null"`
	FailedCount      int       `gorm:"not null"`
	UncertainCount   int       `gorm:"not null"`
	CancelledCount   int       `gorm:"not null"`
	CreatedAt        time.Time `gorm:"not null"`
	UpdatedAt        time.Time `gorm:"not null"`
	StartedAt        *time.Time
	FinishedAt       *time.Time `gorm:"index:idx_som_finished"`
	CancelledAt      *time.Time
}

func (SiteOpsMailBatch) TableName() string { return "site_ops_mail_batches" }

// SiteOpsMailItem 单收件人投递记录。
// PayloadCipher 仅在 pending/sending 阶段非 nil，密封收件邮箱及个性化变量；进入任意终态同事务置 nil。
type SiteOpsMailItem struct {
	ID            uint       `gorm:"primaryKey"`
	InstanceID    uint       `gorm:"not null;index:idx_som_item_inst"`
	BatchID       uint       `gorm:"not null;uniqueIndex:uniq_som_item_addr,priority:1;index:idx_som_item_claim,priority:1"`
	UserID        int64      `gorm:"not null"` // test 批固定为 0，客户批 > 0
	EmailHash     string     `gorm:"size:64;not null;uniqueIndex:uniq_som_item_addr,priority:2"`
	EmailMasked   string     `gorm:"size:160;not null"`
	PayloadCipher *string    `gorm:"type:text"` // 密封 {purpose,instance_id,target_binding,batch_id,user_id,email_hash,email,vars}；终态必为 nil
	Status        string     `gorm:"size:16;not null;index:idx_som_item_claim,priority:2;index:idx_som_item_stale,priority:1"`
	Phase         string     `gorm:"size:16;not null"` // "" | "envelope" | "data" | "done"
	ClaimToken    string     `gorm:"size:36;not null"`
	SMTPStage     string     `gorm:"size:24;not null"`
	SMTPCode      int        `gorm:"not null"`
	ErrorCode     string     `gorm:"size:64;not null"`
	ClaimedAt     *time.Time `gorm:"index:idx_som_item_stale,priority:2"`
	SentAt        *time.Time
	FinishedAt    *time.Time
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
}

func (SiteOpsMailItem) TableName() string { return "site_ops_mail_items" }

var (
	ErrMailNotFound                 = errors.New("mail resource not found")
	ErrMailActiveBatchExists        = errors.New("active mail batch exists")
	ErrMailIdempotencyConflict      = errors.New("mail request id reused with different parameters")
	ErrMailRevisionMismatch         = errors.New("mail revision mismatch")
	ErrMailRetryAlreadyExists       = errors.New("mail retry batch already exists")
	ErrMailNoRetryableItems         = errors.New("mail batch has no retryable items")
	ErrMailTemplateLimitReached     = errors.New("mail template limit reached")
	ErrMailTemplateBuiltinProtected = errors.New("built-in mail template is protected")
	ErrMailTestRateLimited          = errors.New("mail test rate limit reached")
	ErrMailBatchNotCancellable      = errors.New("mail batch cannot be cancelled")
	ErrMailInstanceDisabled         = errors.New("mail instance is disabled")
	ErrMailClaimLost                = errors.New("mail item claim lost")
	ErrMailLeaseLost                = errors.New("mail batch lease lost")
	ErrMailGenerationMismatch       = errors.New("mail instance generation mismatch")
)

// MailScopeKey binds each operation to the selected target and both instance epochs.
type MailScopeKey struct {
	InstanceID          uint
	TargetBinding       string
	Generation, CredGen int64
}

// MailListParams is an exclusive descending-ID cursor. Zero starts the first page.
type MailListParams struct {
	Scope    MailScopeKey
	BeforeID uint
	Limit    int
	Status   string
}

type MailBatchCreateParams struct {
	Scope                                    MailScopeKey
	RequestID, RequestDigest                 string
	Mode                                     string
	ParentBatchID                            *uint
	SMTPRevision                             int64
	TemplateID                               uint
	TemplateRevision                         int64
	TemplateName, SnapshotJSON, AudienceJSON string
	Now, PrepareBy                           time.Time
}

type MailBatchCreateResult struct {
	Batch             SiteOpsMailBatch
	IdempotentReplay  bool
	ActiveBatchID     uint // Set on ErrMailActiveBatchExists.
	RetryAfterSeconds int  // Set on ErrMailTestRateLimited.
}

// Convert the recipient to a bound ciphertext after the batch ID is known.
type MailItemStageRow struct {
	UserID                                int64
	EmailHash, EmailMasked, PayloadCipher string
}

type MailWorkKey struct {
	Scope   MailScopeKey
	BatchID uint
	Owner   string
	Now     time.Time
}

type MailPrepareCounts struct {
	Pages, Scanned, Excluded, Duplicate, Suppressed int
}

type MailClaimParams struct {
	MailWorkKey
	SMTPRevision int64
	ClaimToken   string
}

type MailAuthorizeDataParams struct {
	MailClaimParams
	ItemID uint
}

// Completion can settle an authorized DATA send after cancellation or a generation change.
type MailCompleteItemParams struct {
	InstanceID, BatchID, ItemID              uint
	ClaimToken, Status, SMTPStage, ErrorCode string
	SMTPCode                                 int
	Now                                      time.Time
}

type MailCancelResult struct {
	Batch        SiteOpsMailBatch
	InFlightData int
}

// Check both scopes and both revisions while holding the instance locks in ID order.
type MailCopyParams struct {
	Source, Target                   MailScopeKey
	SourceRevision, ExpectedRevision int64
}

type MailCopySMTPParams struct {
	MailCopyParams
	TargetSMTP SiteOpsMailSMTP // Identity and password already sealed for target revision.
}

type MailCopyTemplateParams struct {
	MailCopyParams
	SourceTemplateID uint
	TargetTemplate   SiteOpsMailTemplate
}

type MailRecoverResult struct {
	UncertainItems, FinishedBatches int64
}

type MailSuppressionEntry struct {
	EmailHash, EmailMasked, Reason string
}

type MailOverviewRows struct {
	SMTP                            *SiteOpsMailSMTP
	ActiveBatch                     *SiteOpsMailBatch
	TemplateCount, SuppressionCount int64
}

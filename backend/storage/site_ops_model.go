package storage

import "time"

// SiteOpsInstance 自有 Sub2API 站点实例配置与最新运行态（P0-A 核心表）。
// 仅保存通过部署侧 Origin/CIDR 门禁校验的站点地址与 AES-GCM 加密的 Admin API Key；
// P3 写操作开关与独立高危凭据槽位推迟至 P3 再扩列，零死字段。
type SiteOpsInstance struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	Name                string     `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Slug                string     `gorm:"size:64;not null;uniqueIndex" json:"slug"`
	BaseURL             string     `gorm:"size:512;not null" json:"base_url"`
	APIKeyCipher        string     `gorm:"type:text;not null" json:"-"`
	APIKeyHint          string     `gorm:"size:64;not null;default:''" json:"api_key_hint"`
	KeyKind             string     `gorm:"size:16;not null;default:'scoped'" json:"key_kind"`
	CredGen             int64      `gorm:"not null;default:1" json:"cred_gen"`
	Generation          int64      `gorm:"not null;default:1" json:"generation"`
	ProxyEnabled        bool       `gorm:"not null;default:false" json:"proxy_enabled"`
	Enabled             bool       `gorm:"not null;default:true;index" json:"enabled"`
	SortOrder           int        `gorm:"not null;default:0" json:"sort_order"`
	PollIntervalSeconds int        `gorm:"not null;default:60" json:"poll_interval_seconds"`
	AuditBudgetPerDay   int        `gorm:"not null;default:8000" json:"audit_budget_per_day"`
	CapabilitiesJSON    string     `gorm:"type:text" json:"capabilities_json,omitempty"`
	LastReachability    string     `gorm:"size:24;not null;default:'unknown'" json:"last_reachability"`
	LastStatus          string     `gorm:"size:24;not null;default:'pending'" json:"last_status"`
	LastHealthScore     *int       `json:"last_health_score,omitempty"`
	LastVersion         string     `gorm:"size:64;not null;default:''" json:"last_version"`
	LastObservedAt      *time.Time `json:"last_observed_at,omitempty"`
	LastVerifiedAt      *time.Time `json:"last_verified_at,omitempty"`
	BackoffUntil        *time.Time `json:"backoff_until,omitempty"`
	LastErrorCode       string     `gorm:"size:64;not null;default:''" json:"last_error_code"`
	LastErrorSummary    string     `gorm:"size:256;not null;default:''" json:"last_error_summary"`
	AlertConfigJSON     string     `gorm:"type:text;not null;default:''" json:"alert_config_json,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (SiteOpsInstance) TableName() string { return "site_ops_instances" }

// SiteOpsSnapshot 站点级时序快照（P0-A + P0-B，默认保留 14 天）。
// 所有可能缺失/未授权/未证实的指标使用指针（nil 表示缺失，严禁用 0 冒充）。
type SiteOpsSnapshot struct {
	ID                       uint      `gorm:"primaryKey" json:"id"`
	InstanceID               uint      `gorm:"not null;uniqueIndex:uniq_site_ops_snap_inst_time,priority:1" json:"instance_id"`
	ObservedAt               time.Time `gorm:"not null;uniqueIndex:uniq_site_ops_snap_inst_time,priority:2;index:idx_site_ops_snap_observed" json:"observed_at"`
	VerifiedAt               time.Time `gorm:"not null" json:"verified_at"`
	Reachable                bool      `gorm:"not null;default:false" json:"reachable"`
	ProbeLatencyMS           int64     `gorm:"not null;default:0" json:"probe_latency_ms"`
	FieldQualityJSON         string    `gorm:"type:text;not null;default:'{}'" json:"field_quality_json"`
	HealthScore              *int      `json:"health_score,omitempty"`
	UptimeSeconds            *int64    `json:"uptime_seconds,omitempty"`
	CPUPercent               *float64  `json:"cpu_percent,omitempty"`
	MemPercent               *float64  `json:"mem_percent,omitempty"`
	MemUsedMB                *int64    `json:"mem_used_mb,omitempty"`
	DBOK                     *bool     `json:"db_ok,omitempty"`
	DBConnActive             *int      `json:"db_conn_active,omitempty"`
	DBConnWaiting            *int      `json:"db_conn_waiting,omitempty"`
	DBMaxOpenConns           *int      `json:"db_max_open_conns,omitempty"`
	RedisOK                  *bool     `json:"redis_ok,omitempty"`
	RedisConnTotal           *int      `json:"redis_conn_total,omitempty"`
	RedisConnIdle            *int      `json:"redis_conn_idle,omitempty"`
	RedisPoolSize            *int      `json:"redis_pool_size,omitempty"`
	GoroutineCount           *int      `json:"goroutine_count,omitempty"`
	QueueDepth               *int      `json:"queue_depth,omitempty"`
	AccountSwitchCount1m     *int64    `json:"account_switch_count_1m,omitempty"`
	SLAPercent1h             *float64  `json:"sla_percent_1h,omitempty"`
	ErrorRatePercent1h       *float64  `json:"error_rate_percent_1h,omitempty"`
	UpstreamErrorRatePct1h   *float64  `json:"upstream_error_rate_percent_1h,omitempty"`
	ReqTotal1h               *int64    `json:"req_total_1h,omitempty"`
	ErrTotal1h               *int64    `json:"err_total_1h,omitempty"`
	Upstream4291h            *int64    `json:"upstream_429_1h,omitempty"`
	Upstream5291h            *int64    `json:"upstream_529_1h,omitempty"`
	UpstreamErrExcl4295291h  *int64    `json:"upstream_err_excl_429_529_1h,omitempty"`
	QPSCurrent               *float64  `json:"qps_current,omitempty"`
	QPSPeak                  *float64  `json:"qps_peak,omitempty"`
	TPSCurrent               *float64  `json:"tps_current,omitempty"`
	RPM5m                    *int64    `json:"rpm_5m,omitempty"`
	TPM5m                    *int64    `json:"tpm_5m,omitempty"`
	TTFTP50MS                *int      `json:"ttft_p50_ms,omitempty"`
	TTFTP95MS                *int      `json:"ttft_p95_ms,omitempty"`
	DurationP95MS            *int      `json:"duration_p95_ms,omitempty"`
	OutputTPSP50             *float64  `json:"output_tps_p50,omitempty"`
	// P0-B 去重全站真实并发与 Ops 判定号池状态
	ConcUsed                 *int64    `json:"conc_used,omitempty"`
	ConcMax                  *int64    `json:"conc_max,omitempty"`
	ConcWaiting              *int64    `json:"conc_waiting,omitempty"`
	ConcTrustState           string    `gorm:"size:24;not null;default:'unavailable'" json:"conc_trust_state"`
	AccountsTotal            *int64    `json:"accounts_total,omitempty"`
	AccountsOpsAvailable     *int64    `json:"accounts_ops_available,omitempty"`
	AccountsRateLimited      *int64    `json:"accounts_rate_limited,omitempty"`
	AccountsOverloaded       *int64    `json:"accounts_overloaded,omitempty"`
	AccountsError            *int64    `json:"accounts_error,omitempty"`
	DetailJSON               string    `gorm:"type:text" json:"detail_json,omitempty"`
}

func (SiteOpsSnapshot) TableName() string { return "site_ops_snapshots" }

// SiteOpsGroupSnapshot 分组级配置并发容量与 Ops 可用度快照（P0-B，默认 5m 归档、保留 7 天）。
// 注意：账号跨组共享，各分组配置并发容量不可直接相加。
type SiteOpsGroupSnapshot struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	InstanceID           uint      `gorm:"not null;uniqueIndex:uniq_site_ops_grp_snap,priority:1" json:"instance_id"`
	GroupID              int64     `gorm:"not null;uniqueIndex:uniq_site_ops_grp_snap,priority:2" json:"group_id"`
	GroupName            string    `gorm:"size:128;not null;default:''" json:"group_name"`
	Platform             string    `gorm:"size:32;not null;default:''" json:"platform"`
	ObservedAt           time.Time `gorm:"not null;uniqueIndex:uniq_site_ops_grp_snap,priority:3;index:idx_site_ops_grp_observed" json:"observed_at"`
	TrustState           string    `gorm:"size:24;not null;default:'unavailable'" json:"trust_state"`
	AccountsTotal        int64     `gorm:"not null;default:0" json:"accounts_total"`
	AccountsOpsAvailable int64     `gorm:"not null;default:0" json:"accounts_ops_available"`
	AccountsRateLimited  int64     `gorm:"not null;default:0" json:"accounts_rate_limited"`
	AccountsOverloaded   int64     `gorm:"not null;default:0" json:"accounts_overloaded"`
	AccountsError        int64     `gorm:"not null;default:0" json:"accounts_error"`
	ConcUsed             *int64    `json:"conc_used,omitempty"`
	ConcMax              *int64    `json:"conc_max,omitempty"`
	ConcWaiting          *int64    `json:"conc_waiting,omitempty"`
}

func (SiteOpsGroupSnapshot) TableName() string { return "site_ops_group_snapshots" }

// SiteOpsEvent 站点运维状态跃迁事件流（P1-A，默认保留 30 天）。
// 仅管理规则状态机（firing/resolved、连续命中次数与同域证据），不耦合具体通知渠道投递状态。
type SiteOpsEvent struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	InstanceID          uint       `gorm:"not null;index:idx_site_ops_evt_inst_created,priority:1;index:idx_site_ops_evt_active,priority:1" json:"instance_id"`
	Severity            string     `gorm:"size:16;not null;default:'warning'" json:"severity"`
	RuleKey             string     `gorm:"size:64;not null;index:idx_site_ops_evt_active,priority:2" json:"rule_key"`
	ResourceKey         string     `gorm:"size:128;not null;default:'site';index:idx_site_ops_evt_active,priority:3" json:"resource_key"`
	RequiredDomainsJSON string     `gorm:"type:text;not null;default:'[]'" json:"required_domains_json"`
	State               string     `gorm:"size:16;not null;default:'firing';index:idx_site_ops_evt_active,priority:4" json:"state"`
	ConsecutiveHits     int        `gorm:"not null;default:1" json:"consecutive_hits"`
	Title               string     `gorm:"size:256;not null;default:''" json:"title"`
	Summary             string     `gorm:"size:512;not null;default:''" json:"summary"`
	DetailJSON          string     `gorm:"type:text;not null;default:'{}'" json:"detail_json,omitempty"`
	FiredAt             time.Time  `gorm:"not null" json:"fired_at"`
	ResolvedAt          *time.Time `json:"resolved_at,omitempty"`
	CreatedAt           time.Time  `gorm:"index:idx_site_ops_evt_inst_created,priority:2;index:idx_site_ops_evt_created" json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (SiteOpsEvent) TableName() string { return "site_ops_events" }

// SiteOpsAlertDelivery 独立冷却占位与按目的地投递状态表（P1-A，默认保留 30 天）。
// 与旧 notification_cooldowns / notification_logs 完全隔离，避免 Channels.Delete(id) 串扰，
// 同时支持部分渠道失败后的独立退避重试与重启续发。
type SiteOpsAlertDelivery struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	EventID         uint       `gorm:"not null;index:idx_site_ops_deliv_event" json:"event_id"`
	InstanceID      uint       `gorm:"not null;index:idx_site_ops_deliv_cooldown,priority:1" json:"instance_id"`
	NotifyChannelID uint       `gorm:"not null;index:idx_site_ops_deliv_cooldown,priority:2" json:"notify_channel_id"`
	DeliveryKind    string     `gorm:"size:24;not null;default:'alert'" json:"delivery_kind"`
	CooldownKey     string     `gorm:"size:128;not null;default:'';index:idx_site_ops_deliv_cooldown,priority:3" json:"cooldown_key"`
	CooldownUntil   *time.Time `gorm:"index:idx_site_ops_deliv_cooldown,priority:4" json:"cooldown_until,omitempty"`
	Status          string     `gorm:"size:24;not null;default:'pending';index:idx_site_ops_deliv_retry,priority:1" json:"status"`
	Attempts        int        `gorm:"not null;default:0" json:"attempts"`
	NextRetryAt     *time.Time `gorm:"index:idx_site_ops_deliv_retry,priority:2" json:"next_retry_at,omitempty"`
	Subject         string     `gorm:"size:256;not null;default:''" json:"subject"`
	Body            string     `gorm:"type:text;not null;default:''" json:"body"`
	LastError       string     `gorm:"size:256;not null;default:''" json:"last_error,omitempty"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	CreatedAt       time.Time  `gorm:"index:idx_site_ops_deliv_created" json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (SiteOpsAlertDelivery) TableName() string { return "site_ops_alert_deliveries" }

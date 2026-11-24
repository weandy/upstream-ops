// Package config 定义应用配置结构体与默认值（含 gateway 运行时参数）。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Config struct {
	App           AppConfig           `mapstructure:"app" yaml:"app" json:"app"`
	Server        ServerConfig        `mapstructure:"server" yaml:"server" json:"server"`
	Database      DatabaseConfig      `mapstructure:"database" yaml:"database" json:"database"`
	Security      SecurityConfig      `mapstructure:"security" yaml:"security" json:"security"`
	Auth          AuthConfig          `mapstructure:"auth" yaml:"auth" json:"auth"`
	Scheduler     SchedulerConfig     `mapstructure:"scheduler" yaml:"scheduler" json:"scheduler"`
	Notifications NotificationsConfig `mapstructure:"notifications" yaml:"notifications" json:"notifications"`
	Proxy         ProxyConfig         `mapstructure:"proxy" yaml:"proxy" json:"proxy"`
	Upstream      UpstreamConfig      `mapstructure:"upstream" yaml:"upstream" json:"upstream"`
	Gateway       GatewayConfig       `mapstructure:"gateway" yaml:"gateway" json:"gateway"`
	Pricing       PricingConfig       `mapstructure:"pricing" yaml:"pricing" json:"pricing"`
	Intelligence  IntelligenceConfig  `mapstructure:"intelligence" yaml:"intelligence" json:"intelligence"`
	Embed         EmbedConfig         `mapstructure:"embed" yaml:"embed" json:"embed"`
	SiteOps       SiteOpsConfig       `mapstructure:"siteOps" yaml:"siteOps" json:"siteOps"`
	Log           LogConfig           `mapstructure:"log" yaml:"log" json:"log"`
}

type AppConfig struct {
	Title              string `mapstructure:"title" yaml:"title" json:"title"`
	NotificationPrefix string `mapstructure:"notificationPrefix" yaml:"notificationPrefix" json:"notificationPrefix"`
}

type ServerConfig struct {
	Port           int      `mapstructure:"port" yaml:"port" json:"port"`
	Mode           string   `mapstructure:"mode" yaml:"mode" json:"mode"`
	TrustedProxies []string `mapstructure:"trustedProxies" yaml:"trustedProxies" json:"trustedProxies"`
	BaseURL        string   `mapstructure:"baseURL" yaml:"baseURL" json:"baseURL"`
}

type DatabaseConfig struct {
	Driver       string `mapstructure:"driver" yaml:"driver" json:"driver"`
	Path         string `mapstructure:"path" yaml:"path" json:"path"`
	DSN          string `mapstructure:"dsn" yaml:"dsn" json:"dsn"`
	Host         string `mapstructure:"host" yaml:"host" json:"host"`
	Port         int    `mapstructure:"port" yaml:"port" json:"port"`
	User         string `mapstructure:"user" yaml:"user" json:"user"`
	Password     string `mapstructure:"password" yaml:"password" json:"password"`
	Name         string `mapstructure:"name" yaml:"name" json:"name"`
	SSLMode      string `mapstructure:"sslMode" yaml:"sslMode" json:"sslMode"`
	TimeZone     string `mapstructure:"timeZone" yaml:"timeZone" json:"timeZone"`
	MaxOpenConns int    `mapstructure:"maxOpenConns" yaml:"maxOpenConns" json:"maxOpenConns"`
	MaxIdleConns int    `mapstructure:"maxIdleConns" yaml:"maxIdleConns" json:"maxIdleConns"`
}

func (d DatabaseConfig) ToStorageConfig() storage.DBConfig {
	return storage.DBConfig{
		Driver:       storage.DBDriver(d.Driver),
		Path:         d.Path,
		DSN:          d.DSN,
		Host:         d.Host,
		Port:         d.Port,
		User:         d.User,
		Password:     d.Password,
		Name:         d.Name,
		SSLMode:      d.SSLMode,
		TimeZone:     d.TimeZone,
		MaxOpenConns: d.MaxOpenConns,
		MaxIdleConns: d.MaxIdleConns,
	}
}

type SecurityConfig struct {
	// AppSecret 主密钥，用于 AES-GCM。优先从 APP_SECRET 环境变量读取。
	AppSecret string `mapstructure:"appSecret" yaml:"appSecret" json:"appSecret"`
}

// AuthConfig 后台单用户登录配置。
//
// Enabled = false（默认）时整套鉴权被关掉：/api/* 全部免 token，前端检测后跳过登录页。
// 适合纯内网 / 反代后面的部署。需要公网暴露时必须显式 Enabled=true 并设强密码。
//
// Enabled=true 时 Username/Password 是写死的管理员凭据，TokenSecret 用于签发 HMAC token。
// 如果 TokenSecret 为空，会回退使用 Security.AppSecret，保证有合理默认。
type AuthConfig struct {
	Enabled         bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Username        string `mapstructure:"username" yaml:"username" json:"username"`
	Password        string `mapstructure:"password" yaml:"password" json:"password"`
	TokenSecret     string `mapstructure:"tokenSecret" yaml:"tokenSecret" json:"tokenSecret"`
	SessionTTLHours int    `mapstructure:"sessionTTLHours" yaml:"sessionTTLHours" json:"sessionTTLHours"`
}

type SchedulerConfig struct {
	BalanceCron string          `mapstructure:"balanceCron" yaml:"balanceCron" json:"balanceCron"`
	RateCron    string          `mapstructure:"rateCron" yaml:"rateCron" json:"rateCron"`
	Concurrency int             `mapstructure:"concurrency" yaml:"concurrency" json:"concurrency"`
	Retention   RetentionConfig `mapstructure:"retention" yaml:"retention" json:"retention"`
}

// RetentionConfig 历史数据保留策略。
//
// 字段为 0 表示该表不清理，永久保留（默认 rate_change_logs 永远保留，是核心业务数据）。
// Cron 为空时不启动清理任务。
type RetentionConfig struct {
	Cron                      string `mapstructure:"cron" yaml:"cron" json:"cron"`
	MonitorLogsDays           int    `mapstructure:"monitorLogsDays" yaml:"monitorLogsDays" json:"monitorLogsDays"`
	BalanceSnapshotsDays      int    `mapstructure:"balanceSnapshotsDays" yaml:"balanceSnapshotsDays" json:"balanceSnapshotsDays"`
	NotificationLogsDays      int    `mapstructure:"notificationLogsDays" yaml:"notificationLogsDays" json:"notificationLogsDays"`
	AnnouncementsDays         int    `mapstructure:"announcementsDays" yaml:"announcementsDays" json:"announcementsDays"`
	ProbeLivenessDays         int    `mapstructure:"probeLivenessDays" yaml:"probeLivenessDays" json:"probeLivenessDays"`
	ProbeIntelligenceRunsDays int    `mapstructure:"probeIntelligenceRunsDays" yaml:"probeIntelligenceRunsDays" json:"probeIntelligenceRunsDays"`
	SiteOpsSnapshotsDays      int    `mapstructure:"siteOpsSnapshotsDays" yaml:"siteOpsSnapshotsDays" json:"siteOpsSnapshotsDays"`
	SiteOpsGroupSnapshotsDays int    `mapstructure:"siteOpsGroupSnapshotsDays" yaml:"siteOpsGroupSnapshotsDays" json:"siteOpsGroupSnapshotsDays"`
}

// NotificationsConfig 通知去抖策略。所有字段都是"少烦我"取向，默认不丢消息只合并。
//
//   - MinChangePct：涨跌幅 < X% 的 rate_changed 跳过推送（仍会写入 rate_change_logs）。
//     0 = 全发，对应原始行为。
//   - BalanceLowCooldownMinutes：同一渠道的 balance_low 在 X 分钟内不重复推送。
//     0 = 不冷却（每次扫描发现仍 < 阈值都发）。冷却状态持久化在数据库的
//     notification_cooldowns 表，跨重启生效。
//   - SendMaxAttempts：单条通知发送失败时最多尝试次数（含首次）。
//     1 = 不重试。重试采用指数退避：1s / 2s / 4s …，上限 30s。
type NotificationsConfig struct {
	MinChangePct                             float64 `mapstructure:"minChangePct" yaml:"minChangePct" json:"minChangePct"`
	BalanceLowCooldownMinutes                int     `mapstructure:"balanceLowCooldownMinutes" yaml:"balanceLowCooldownMinutes" json:"balanceLowCooldownMinutes"`
	SubscriptionDailyRemainingThresholdPct   float64 `mapstructure:"subscriptionDailyRemainingThresholdPct" yaml:"subscriptionDailyRemainingThresholdPct" json:"subscriptionDailyRemainingThresholdPct"`
	SubscriptionWeeklyRemainingThresholdPct  float64 `mapstructure:"subscriptionWeeklyRemainingThresholdPct" yaml:"subscriptionWeeklyRemainingThresholdPct" json:"subscriptionWeeklyRemainingThresholdPct"`
	SubscriptionMonthlyRemainingThresholdPct float64 `mapstructure:"subscriptionMonthlyRemainingThresholdPct" yaml:"subscriptionMonthlyRemainingThresholdPct" json:"subscriptionMonthlyRemainingThresholdPct"`
	SubscriptionExpiryThresholdHours         int     `mapstructure:"subscriptionExpiryThresholdHours" yaml:"subscriptionExpiryThresholdHours" json:"subscriptionExpiryThresholdHours"`
	SubscriptionAlertCooldownMinutes         int     `mapstructure:"subscriptionAlertCooldownMinutes" yaml:"subscriptionAlertCooldownMinutes" json:"subscriptionAlertCooldownMinutes"`
	IntelligenceAlertCooldownMinutes         int     `mapstructure:"intelligenceAlertCooldownMinutes" yaml:"intelligenceAlertCooldownMinutes" json:"intelligenceAlertCooldownMinutes"`
	SendMaxAttempts                          int     `mapstructure:"sendMaxAttempts" yaml:"sendMaxAttempts" json:"sendMaxAttempts"`
}

type ProxyConfig struct {
	Enabled             bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	VersionCheckEnabled bool   `mapstructure:"versionCheckEnabled" yaml:"versionCheckEnabled" json:"versionCheckEnabled"`
	Protocol            string `mapstructure:"protocol" yaml:"protocol" json:"protocol"`
	Host                string `mapstructure:"host" yaml:"host" json:"host"`
	Port                int    `mapstructure:"port" yaml:"port" json:"port"`
	Username            string `mapstructure:"username" yaml:"username" json:"username"`
	Password            string `mapstructure:"password" yaml:"password" json:"password"`
}

const (
	DefaultUpstreamTimeoutSeconds = 30
	DefaultUpstreamUserAgent      = "upstream-ops/0.1"

	// 网关默认值（设置页可改；0/空表示使用下列默认）。
	DefaultGatewayTempPauseSeconds           = 30
	DefaultGatewayForwardTimeoutSeconds      = 600 // 10 分钟
	DefaultGatewayModelsCacheTTLSeconds      = 60
	DefaultGatewayMaxFailoverSwitches        = 8
	DefaultGatewayRouteBatchConcurrency      = 8
	DefaultGatewayUsageErrorBodyBytes        = 32 * 1024
	DefaultGatewayUsageErrorMsgRunes         = 500
	DefaultGatewayUsageErrorHeaderValueRunes = 8 * 1024
	DefaultGatewayUsageErrorHeadersJSONBytes = 64 * 1024
	DefaultGatewayStreamKeepaliveSeconds     = 15
	DefaultGatewayStreamIdleTimeoutSeconds   = 120
	DefaultGatewayStreamMaxLineBytes         = 8 << 20
)

type UpstreamConfig struct {
	TimeoutSeconds int    `mapstructure:"timeoutSeconds" yaml:"timeoutSeconds" json:"timeoutSeconds"`
	UserAgent      string `mapstructure:"userAgent" yaml:"userAgent" json:"userAgent"`
}

// PricingConfig 是网关模型价目表的远程同步配置。
type PricingConfig struct {
	Enabled                  bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	RemoteURL                string `mapstructure:"remoteURL" yaml:"remoteURL" json:"remoteURL"`
	HashURL                  string `mapstructure:"hashURL" yaml:"hashURL" json:"hashURL"`
	DataDir                  string `mapstructure:"dataDir" yaml:"dataDir" json:"dataDir"`
	FallbackFile             string `mapstructure:"fallbackFile" yaml:"fallbackFile" json:"fallbackFile"`
	UpdateIntervalHours      int    `mapstructure:"updateIntervalHours" yaml:"updateIntervalHours" json:"updateIntervalHours"`
	HashCheckIntervalMinutes int    `mapstructure:"hashCheckIntervalMinutes" yaml:"hashCheckIntervalMinutes" json:"hashCheckIntervalMinutes"`
}

const (
	DefaultPricingRemoteURL = "https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json"
	DefaultPricingHashURL   = "https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.sha256"
)

func (p PricingConfig) WithDefaults() PricingConfig {
	if strings.TrimSpace(p.RemoteURL) == "" {
		p.RemoteURL = DefaultPricingRemoteURL
	}
	if strings.TrimSpace(p.HashURL) == "" {
		p.HashURL = DefaultPricingHashURL
	}
	if strings.TrimSpace(p.DataDir) == "" {
		p.DataDir = "./data"
	}
	if strings.TrimSpace(p.FallbackFile) == "" {
		p.FallbackFile = "./backend/gateway/pricing/default_prices.json"
	}
	if p.UpdateIntervalHours <= 0 {
		p.UpdateIntervalHours = 24
	}
	if p.HashCheckIntervalMinutes <= 0 {
		p.HashCheckIntervalMinutes = 10
	}
	return p
}

func (u UpstreamConfig) WithDefaults() UpstreamConfig {
	if u.TimeoutSeconds <= 0 {
		u.TimeoutSeconds = DefaultUpstreamTimeoutSeconds
	}
	if strings.TrimSpace(u.UserAgent) == "" {
		u.UserAgent = DefaultUpstreamUserAgent
	}
	return u
}

// GatewayConfig 网关运行时参数（转发超时、批量运维并发、用量错误落库截断等）。
// 可在设置页保存并「应用配置」后立即生效；字段 ≤0 时回退默认值。
type GatewayConfig struct {
	// TempPauseSeconds 新建组默认临时暂停时长（秒），对应路由冷却。
	TempPauseSeconds int `mapstructure:"tempPauseSeconds" yaml:"tempPauseSeconds" json:"tempPauseSeconds"`
	// ForwardTimeoutSeconds 单次上游转发/流式 drain 超时（秒）。
	ForwardTimeoutSeconds int `mapstructure:"forwardTimeoutSeconds" yaml:"forwardTimeoutSeconds" json:"forwardTimeoutSeconds"`
	// ModelsCacheTTLSeconds 公开 /v1/models 列表缓存 TTL（秒）。
	ModelsCacheTTLSeconds int `mapstructure:"modelsCacheTTLSeconds" yaml:"modelsCacheTTLSeconds" json:"modelsCacheTTLSeconds"`
	// MaxFailoverSwitches 新建组默认最大顺延切换次数。
	MaxFailoverSwitches int `mapstructure:"maxFailoverSwitches" yaml:"maxFailoverSwitches" json:"maxFailoverSwitches"`
	// RouteBatchConcurrency 批量运维并发（探测模型 / ensure 密钥 / 同步模型 / 拉源分组）。
	RouteBatchConcurrency int `mapstructure:"routeBatchConcurrency" yaml:"routeBatchConcurrency" json:"routeBatchConcurrency"`
	// UsageError* 用量错误明细落库截断上限（字节或 rune）。
	UsageErrorBodyBytes        int `mapstructure:"usageErrorBodyBytes" yaml:"usageErrorBodyBytes" json:"usageErrorBodyBytes"`
	UsageErrorMsgRunes         int `mapstructure:"usageErrorMsgRunes" yaml:"usageErrorMsgRunes" json:"usageErrorMsgRunes"`
	UsageErrorHeaderValueRunes int `mapstructure:"usageErrorHeaderValueRunes" yaml:"usageErrorHeaderValueRunes" json:"usageErrorHeaderValueRunes"`
	UsageErrorHeadersJSONBytes int `mapstructure:"usageErrorHeadersJSONBytes" yaml:"usageErrorHeadersJSONBytes" json:"usageErrorHeadersJSONBytes"`
	// StreamKeepaliveSeconds 已提交 SSE 流的下游保活间隔（秒）。
	StreamKeepaliveSeconds int `mapstructure:"streamKeepaliveSeconds" yaml:"streamKeepaliveSeconds" json:"streamKeepaliveSeconds"`
	// StreamIdleTimeoutSeconds 上游 SSE 收到有效数据后的最大空闲时长（秒）。
	StreamIdleTimeoutSeconds int `mapstructure:"streamIdleTimeoutSeconds" yaml:"streamIdleTimeoutSeconds" json:"streamIdleTimeoutSeconds"`
	// StreamMaxLineBytes 上游单条 SSE 行最大字节数。
	StreamMaxLineBytes int `mapstructure:"streamMaxLineBytes" yaml:"streamMaxLineBytes" json:"streamMaxLineBytes"`
}

func (g GatewayConfig) WithDefaults() GatewayConfig {
	if g.TempPauseSeconds <= 0 {
		g.TempPauseSeconds = DefaultGatewayTempPauseSeconds
	}
	if g.ForwardTimeoutSeconds <= 0 {
		g.ForwardTimeoutSeconds = DefaultGatewayForwardTimeoutSeconds
	}
	if g.ModelsCacheTTLSeconds <= 0 {
		g.ModelsCacheTTLSeconds = DefaultGatewayModelsCacheTTLSeconds
	}
	if g.MaxFailoverSwitches <= 0 {
		g.MaxFailoverSwitches = DefaultGatewayMaxFailoverSwitches
	}
	if g.RouteBatchConcurrency <= 0 {
		g.RouteBatchConcurrency = DefaultGatewayRouteBatchConcurrency
	}
	if g.RouteBatchConcurrency > 64 {
		g.RouteBatchConcurrency = 64
	}
	if g.UsageErrorBodyBytes <= 0 {
		g.UsageErrorBodyBytes = DefaultGatewayUsageErrorBodyBytes
	}
	if g.UsageErrorMsgRunes <= 0 {
		g.UsageErrorMsgRunes = DefaultGatewayUsageErrorMsgRunes
	}
	if g.UsageErrorHeaderValueRunes <= 0 {
		g.UsageErrorHeaderValueRunes = DefaultGatewayUsageErrorHeaderValueRunes
	}
	if g.UsageErrorHeadersJSONBytes <= 0 {
		g.UsageErrorHeadersJSONBytes = DefaultGatewayUsageErrorHeadersJSONBytes
	}
	if g.StreamKeepaliveSeconds <= 0 {
		g.StreamKeepaliveSeconds = DefaultGatewayStreamKeepaliveSeconds
	}
	if g.StreamIdleTimeoutSeconds <= 0 {
		g.StreamIdleTimeoutSeconds = DefaultGatewayStreamIdleTimeoutSeconds
	}
	if g.StreamMaxLineBytes <= 0 {
		g.StreamMaxLineBytes = DefaultGatewayStreamMaxLineBytes
	}
	return g
}

func (g GatewayConfig) TempPause() time.Duration {
	g = g.WithDefaults()
	return time.Duration(g.TempPauseSeconds) * time.Second
}

func (g GatewayConfig) ForwardTimeout() time.Duration {
	g = g.WithDefaults()
	return time.Duration(g.ForwardTimeoutSeconds) * time.Second
}

func (g GatewayConfig) ModelsCacheTTL() time.Duration {
	g = g.WithDefaults()
	return time.Duration(g.ModelsCacheTTLSeconds) * time.Second
}

func (g GatewayConfig) StreamKeepalive() time.Duration {
	g = g.WithDefaults()
	return time.Duration(g.StreamKeepaliveSeconds) * time.Second
}

func (g GatewayConfig) StreamIdleTimeout() time.Duration {
	g = g.WithDefaults()
	return time.Duration(g.StreamIdleTimeoutSeconds) * time.Second
}

func (g GatewayConfig) StreamMaxLineSize() int {
	return g.WithDefaults().StreamMaxLineBytes
}

type LogConfig struct {
	Level  string `mapstructure:"level" yaml:"level" json:"level"`
	Format string `mapstructure:"format" yaml:"format" json:"format"`
}

// IntelligenceConfig 外挂式主动测活与模型智力监测运行时配置。
type IntelligenceConfig struct {
	LMFPDDataDir               string `mapstructure:"lmfpdDataDir" yaml:"lmfpdDataDir" json:"lmfpdDataDir"`
	TickIntervalSeconds        int    `mapstructure:"tickIntervalSeconds" yaml:"tickIntervalSeconds" json:"tickIntervalSeconds"`
	Concurrency                int    `mapstructure:"concurrency" yaml:"concurrency" json:"concurrency"`
	BackoffEnabled             *bool  `mapstructure:"backoffEnabled" yaml:"backoffEnabled" json:"backoffEnabled"`
	BackoffTransientMaxSeconds int    `mapstructure:"backoffTransientMaxSeconds" yaml:"backoffTransientMaxSeconds" json:"backoffTransientMaxSeconds"`
	BackoffRateLimitMaxSeconds int    `mapstructure:"backoffRateLimitMaxSeconds" yaml:"backoffRateLimitMaxSeconds" json:"backoffRateLimitMaxSeconds"`
	BackoffAuthMaxSeconds      int    `mapstructure:"backoffAuthMaxSeconds" yaml:"backoffAuthMaxSeconds" json:"backoffAuthMaxSeconds"`
}

const (
	DefaultBackoffTransientMaxSeconds = 3600  // 1 小时
	DefaultBackoffRateLimitMaxSeconds = 7200  // 2 小时
	DefaultBackoffAuthMaxSeconds      = 21600 // 6 小时

	DefaultEmbedCacheTTLSeconds = 5
	DefaultEmbedRateLimitRPS    = 5.0
	DefaultEmbedRateLimitBurst  = 30
)

func boolPtr(v bool) *bool { return &v }

// IsBackoffEnabled 返回是否开启异常目标指数退避（未显式配置时默认开启）。
func (c IntelligenceConfig) IsBackoffEnabled() bool {
	if c.BackoffEnabled == nil {
		return true
	}
	return *c.BackoffEnabled
}

func (c IntelligenceConfig) WithDefaults() IntelligenceConfig {
	if strings.TrimSpace(c.LMFPDDataDir) == "" {
		c.LMFPDDataDir = "./data/lmfpd"
	}
	if c.TickIntervalSeconds <= 0 {
		c.TickIntervalSeconds = 5
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 3
	}
	if c.Concurrency > 32 {
		c.Concurrency = 32
	}
	if c.BackoffEnabled == nil {
		c.BackoffEnabled = boolPtr(true)
	}
	if c.BackoffTransientMaxSeconds <= 0 {
		c.BackoffTransientMaxSeconds = DefaultBackoffTransientMaxSeconds
	}
	if c.BackoffRateLimitMaxSeconds <= 0 {
		c.BackoffRateLimitMaxSeconds = DefaultBackoffRateLimitMaxSeconds
	}
	if c.BackoffAuthMaxSeconds <= 0 {
		c.BackoffAuthMaxSeconds = DefaultBackoffAuthMaxSeconds
	}
	return c
}

// EmbedConfig 公开 /embed 与 /api/public/status 的微缓存与按 IP 限流配置。
type EmbedConfig struct {
	CacheTTLSeconds  *int    `mapstructure:"cacheTTLSeconds" yaml:"cacheTTLSeconds" json:"cacheTTLSeconds"`
	RateLimitEnabled *bool   `mapstructure:"rateLimitEnabled" yaml:"rateLimitEnabled" json:"rateLimitEnabled"`
	RateLimitRPS     float64 `mapstructure:"rateLimitRPS" yaml:"rateLimitRPS" json:"rateLimitRPS"`
	RateLimitBurst   int     `mapstructure:"rateLimitBurst" yaml:"rateLimitBurst" json:"rateLimitBurst"`
}

func intPtr(v int) *int { return &v }

func (c EmbedConfig) EffectiveCacheTTLSeconds() int {
	if c.CacheTTLSeconds == nil {
		return DefaultEmbedCacheTTLSeconds
	}
	if *c.CacheTTLSeconds < 0 {
		return 0
	}
	if *c.CacheTTLSeconds > 60 {
		return 60
	}
	return *c.CacheTTLSeconds
}

func (c EmbedConfig) IsRateLimitEnabled() bool {
	if c.RateLimitEnabled == nil {
		return true
	}
	return *c.RateLimitEnabled
}

func (c EmbedConfig) WithDefaults() EmbedConfig {
	if c.CacheTTLSeconds == nil {
		c.CacheTTLSeconds = intPtr(DefaultEmbedCacheTTLSeconds)
	} else {
		v := c.EffectiveCacheTTLSeconds()
		c.CacheTTLSeconds = intPtr(v)
	}
	if c.RateLimitEnabled == nil {
		c.RateLimitEnabled = boolPtr(true)
	}
	if c.RateLimitRPS <= 0 {
		c.RateLimitRPS = DefaultEmbedRateLimitRPS
	}
	if c.RateLimitBurst <= 0 {
		c.RateLimitBurst = DefaultEmbedRateLimitBurst
	}
	return c
}

const (
	DefaultSiteOpsPollIntervalSeconds        = 60
	MinSiteOpsPollIntervalSeconds            = 30
	DefaultSiteOpsAuditBudgetPerDay          = 8000
	DefaultSiteOpsGlobalMaxConcurrency       = 12
	DefaultSiteOpsPerInstanceMaxConcurrency  = 4
	DefaultSiteOpsPerInstanceQueueLimit      = 32
	DefaultSiteOpsQueueWaitTimeoutSeconds    = 3
	DefaultSiteOpsRequestTimeoutSeconds      = 8
	DefaultSiteOpsBatchTimeoutSeconds        = 15
	DefaultSiteOpsMaxResponseBytes           = 8 << 20
	DefaultSiteOpsGroupArchiveIntervalSecond = 300
)

// SiteOpsConfig 自有 Sub2API 站点运维模块运行时配置。
// SiteOpsConfig 配置自有 Sub2API 站点运维模块（backend/siteops）的出站安全、并发与预算策略。
// P0–P2 鉴权跟随全局 AuthMiddleware；出站默认放行公网地址并拦截内网/回环/保留与云元数据地址。
// 同机或内网直连可通过 allowLoopback、allowedOrigins、allowedCIDRs 显式配置例外（云元数据/链路本地/未指定地址永久硬拦截）。
type SiteOpsConfig struct {
	AllowUnauthenticatedLocalOnly bool     `mapstructure:"allowUnauthenticatedLocalOnly" yaml:"allowUnauthenticatedLocalOnly" json:"allowUnauthenticatedLocalOnly"`
	AllowLoopback                 bool     `mapstructure:"allowLoopback" yaml:"allowLoopback" json:"allowLoopback"`
	AllowedOrigins                []string `mapstructure:"allowedOrigins" yaml:"allowedOrigins" json:"allowedOrigins"`
	AllowedCIDRs                  []string `mapstructure:"allowedCIDRs" yaml:"allowedCIDRs" json:"allowedCIDRs"`
	DefaultPollIntervalSeconds    int      `mapstructure:"defaultPollIntervalSeconds" yaml:"defaultPollIntervalSeconds" json:"defaultPollIntervalSeconds"`
	DefaultAuditBudgetPerDay      int      `mapstructure:"defaultAuditBudgetPerDay" yaml:"defaultAuditBudgetPerDay" json:"defaultAuditBudgetPerDay"`
	GlobalMaxConcurrency          int      `mapstructure:"globalMaxConcurrency" yaml:"globalMaxConcurrency" json:"globalMaxConcurrency"`
	PerInstanceMaxConcurrency     int      `mapstructure:"perInstanceMaxConcurrency" yaml:"perInstanceMaxConcurrency" json:"perInstanceMaxConcurrency"`
	PerInstanceQueueLimit         int      `mapstructure:"perInstanceQueueLimit" yaml:"perInstanceQueueLimit" json:"perInstanceQueueLimit"`
	QueueWaitTimeoutSeconds       int      `mapstructure:"queueWaitTimeoutSeconds" yaml:"queueWaitTimeoutSeconds" json:"queueWaitTimeoutSeconds"`
	RequestTimeoutSeconds         int      `mapstructure:"requestTimeoutSeconds" yaml:"requestTimeoutSeconds" json:"requestTimeoutSeconds"`
	BatchTimeoutSeconds           int      `mapstructure:"batchTimeoutSeconds" yaml:"batchTimeoutSeconds" json:"batchTimeoutSeconds"`
	MaxResponseBytes              int64    `mapstructure:"maxResponseBytes" yaml:"maxResponseBytes" json:"maxResponseBytes"`
	GroupArchiveIntervalSeconds   int      `mapstructure:"groupArchiveIntervalSeconds" yaml:"groupArchiveIntervalSeconds" json:"groupArchiveIntervalSeconds"`
}

func (c SiteOpsConfig) WithDefaults() SiteOpsConfig {
	if c.DefaultPollIntervalSeconds <= 0 {
		c.DefaultPollIntervalSeconds = DefaultSiteOpsPollIntervalSeconds
	}
	if c.DefaultPollIntervalSeconds < MinSiteOpsPollIntervalSeconds {
		c.DefaultPollIntervalSeconds = MinSiteOpsPollIntervalSeconds
	}
	if c.DefaultAuditBudgetPerDay <= 0 {
		c.DefaultAuditBudgetPerDay = DefaultSiteOpsAuditBudgetPerDay
	}
	if c.GlobalMaxConcurrency <= 0 {
		c.GlobalMaxConcurrency = DefaultSiteOpsGlobalMaxConcurrency
	}
	if c.GlobalMaxConcurrency > 64 {
		c.GlobalMaxConcurrency = 64
	}
	if c.PerInstanceMaxConcurrency <= 0 {
		c.PerInstanceMaxConcurrency = DefaultSiteOpsPerInstanceMaxConcurrency
	}
	if c.PerInstanceMaxConcurrency > 16 {
		c.PerInstanceMaxConcurrency = 16
	}
	if c.PerInstanceQueueLimit <= 0 {
		c.PerInstanceQueueLimit = DefaultSiteOpsPerInstanceQueueLimit
	}
	if c.QueueWaitTimeoutSeconds <= 0 {
		c.QueueWaitTimeoutSeconds = DefaultSiteOpsQueueWaitTimeoutSeconds
	}
	if c.RequestTimeoutSeconds <= 0 {
		c.RequestTimeoutSeconds = DefaultSiteOpsRequestTimeoutSeconds
	}
	if c.BatchTimeoutSeconds <= 0 {
		c.BatchTimeoutSeconds = DefaultSiteOpsBatchTimeoutSeconds
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = DefaultSiteOpsMaxResponseBytes
	}
	if c.GroupArchiveIntervalSeconds <= 0 {
		c.GroupArchiveIntervalSeconds = DefaultSiteOpsGroupArchiveIntervalSecond
	}
	return c
}

// Load 读取 config.yaml（可选）+ APP_SECRET / * 环境变量覆盖。
//
// 关键映射：
//
//	APP_SECRET                       -> security.appSecret
//	DATABASE_DRIVER      -> database.driver
//	DATABASE_PATH        -> database.path
//	DATABASE_HOST        -> database.host
//	SERVER_PORT          -> server.port
//	SCHEDULER_BALANCECRON-> scheduler.balanceCron
func Load(path string) (*Config, error) {
	cfg, _, err := load(path, true)
	return cfg, err
}

func LoadWithPath(path string) (*Config, string, error) {
	return load(path, true)
}

func LoadFile(path string) (*Config, error) {
	cfg, _, err := load(path, false)
	return cfg, err
}

func load(path string, withEnv bool) (*Config, string, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		for _, p := range configSearchPaths() {
			v.AddConfigPath(p)
		}
		v.AddConfigPath("/etc/upstream-ops")
	}

	setDefaults(v)

	if withEnv {
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
		v.AutomaticEnv()
		// APP_SECRET / ADMIN_USERNAME / ADMIN_PASSWORD / AUTH_ENABLED 是独立约定的环境变量名，不带前缀。
		_ = v.BindEnv("security.appSecret", "APP_SECRET")
		_ = v.BindEnv("auth.enabled", "AUTH_ENABLED")
		_ = v.BindEnv("auth.username", "ADMIN_USERNAME")
		_ = v.BindEnv("auth.password", "ADMIN_PASSWORD")
		_ = v.BindEnv("auth.tokenSecret", "AUTH_TOKEN_SECRET")
		// Viper 坑：AutomaticEnv 只对已通过 SetDefault / BindEnv / 配置文件注册过的 key 生效；
		// 数据库的 user/password 没有合理的默认值（拒绝写"change-me"作默认），
		// 因此显式 BindEnv 以确保从环境变量读取。
		_ = v.BindEnv("database.driver", "DATABASE_DRIVER")
		_ = v.BindEnv("database.path", "DATABASE_PATH")
		_ = v.BindEnv("database.dsn", "DATABASE_DSN")
		_ = v.BindEnv("database.host", "DATABASE_HOST")
		_ = v.BindEnv("database.port", "DATABASE_PORT")
		_ = v.BindEnv("database.user", "DATABASE_USER")
		_ = v.BindEnv("database.password", "DATABASE_PASSWORD")
		_ = v.BindEnv("database.name", "DATABASE_NAME")
		_ = v.BindEnv("database.sslMode", "DATABASE_SSLMODE")
		_ = v.BindEnv("database.timeZone", "DATABASE_TIMEZONE")
		_ = v.BindEnv("server.port", "SERVER_PORT")
		_ = v.BindEnv("server.mode", "SERVER_MODE")
		_ = v.BindEnv("log.level", "LOG_LEVEL")
		_ = v.BindEnv("siteOps.allowLoopback", "SITEOPS_ALLOW_LOOPBACK")
	}

	if err := v.ReadInConfig(); err != nil {
		if path != "" {
			if !os.IsNotExist(err) {
				return nil, "", fmt.Errorf("read config: %w", err)
			}
		} else {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return nil, "", fmt.Errorf("read config: %w", err)
			}
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, "", fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.Upstream = cfg.Upstream.WithDefaults()
	cfg.Gateway = cfg.Gateway.WithDefaults()
	cfg.Pricing = cfg.Pricing.WithDefaults()
	cfg.Intelligence = cfg.Intelligence.WithDefaults()
	cfg.Embed = cfg.Embed.WithDefaults()
	cfg.SiteOps = cfg.SiteOps.WithDefaults()
	return cfg, v.ConfigFileUsed(), nil
}

func Save(path string, cfg *Config) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// 先写入同目录临时文件（收紧权限 0600）再原子替换，避免写入中断损坏原配置文件或权限过宽。
	tmpFile, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	_ = tmpFile.Chmod(0o600)
	if _, err := tmpFile.Write(body); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	cleanup = false
	return nil
}

func ResolvePath(requested, used string) string {
	if requested != "" {
		return requested
	}
	if used != "" {
		return used
	}
	for _, candidate := range configSearchPaths() {
		candidate = filepath.Join(candidate, "config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if wd, err := os.Getwd(); err == nil && filepath.Base(wd) == "backend" {
		return "../config.yaml"
	}
	return "config.yaml"
}

func configSearchPaths() []string {
	if wd, err := os.Getwd(); err == nil && filepath.Base(wd) == "backend" {
		return []string{"..", "."}
	}
	return []string{"."}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.title", "UpstreamOps")
	v.SetDefault("app.notificationPrefix", "[AI 聚合监控] ")

	v.SetDefault("server.port", 8418)
	v.SetDefault("server.mode", "debug")
	v.SetDefault("server.baseURL", "http://localhost:8418")

	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.path", "./data/upstream-ops.db")
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 0)
	v.SetDefault("database.name", "upstreamops")
	v.SetDefault("database.sslMode", "disable")
	v.SetDefault("database.timeZone", "Asia/Shanghai")
	v.SetDefault("database.maxOpenConns", 20)
	v.SetDefault("database.maxIdleConns", 5)

	// CLAUDE.md 默认建议：余额 15 分钟，倍率 30 分钟。
	v.SetDefault("scheduler.balanceCron", "37 */15 * * * *")
	v.SetDefault("scheduler.rateCron", "13 */30 * * * *")
	v.SetDefault("scheduler.concurrency", 4)

	// 历史清理：每天凌晨 3:17 跑一次（6 字段 cron 含秒），
	// monitor 30 天 / balance 90 天 / notify 90 天。rate_change_logs 不清理（业务核心数据）。
	v.SetDefault("scheduler.retention.cron", "0 17 3 * * *")
	v.SetDefault("scheduler.retention.monitorLogsDays", 30)
	v.SetDefault("scheduler.retention.balanceSnapshotsDays", 90)
	v.SetDefault("scheduler.retention.notificationLogsDays", 90)
	v.SetDefault("scheduler.retention.announcementsDays", 90)
	v.SetDefault("scheduler.retention.probeLivenessDays", 14)
	v.SetDefault("scheduler.retention.probeIntelligenceRunsDays", 90)
	v.SetDefault("scheduler.retention.siteOpsSnapshotsDays", 14)
	v.SetDefault("scheduler.retention.siteOpsGroupSnapshotsDays", 7)

	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.username", "admin")
	v.SetDefault("auth.sessionTTLHours", 168) // 7 天

	// 通知去抖：不过滤涨跌幅、balance_low 1h 内不重复、失败重试 3 次。
	// 全渠道倍率变化始终在一次扫描结束后合并发送。
	v.SetDefault("notifications.minChangePct", 0)
	v.SetDefault("notifications.balanceLowCooldownMinutes", 60)
	v.SetDefault("notifications.subscriptionDailyRemainingThresholdPct", 0)
	v.SetDefault("notifications.subscriptionWeeklyRemainingThresholdPct", 0)
	v.SetDefault("notifications.subscriptionMonthlyRemainingThresholdPct", 0)
	v.SetDefault("notifications.subscriptionExpiryThresholdHours", 0)
	v.SetDefault("notifications.subscriptionAlertCooldownMinutes", 1440)
	v.SetDefault("notifications.intelligenceAlertCooldownMinutes", 120)
	v.SetDefault("notifications.sendMaxAttempts", 3)

	v.SetDefault("proxy.protocol", "http")
	v.SetDefault("proxy.port", 0)
	v.SetDefault("proxy.enabled", false)
	v.SetDefault("proxy.versionCheckEnabled", false)

	v.SetDefault("upstream.timeoutSeconds", DefaultUpstreamTimeoutSeconds)
	v.SetDefault("upstream.userAgent", DefaultUpstreamUserAgent)
	v.SetDefault("pricing.enabled", true)
	v.SetDefault("pricing.remoteURL", DefaultPricingRemoteURL)
	v.SetDefault("pricing.hashURL", DefaultPricingHashURL)
	v.SetDefault("pricing.dataDir", "./data")
	v.SetDefault("pricing.fallbackFile", "./backend/gateway/pricing/default_prices.json")
	v.SetDefault("pricing.updateIntervalHours", 24)
	v.SetDefault("pricing.hashCheckIntervalMinutes", 10)

	v.SetDefault("gateway.tempPauseSeconds", DefaultGatewayTempPauseSeconds)
	v.SetDefault("gateway.forwardTimeoutSeconds", DefaultGatewayForwardTimeoutSeconds)
	v.SetDefault("gateway.modelsCacheTTLSeconds", DefaultGatewayModelsCacheTTLSeconds)
	v.SetDefault("gateway.maxFailoverSwitches", DefaultGatewayMaxFailoverSwitches)
	v.SetDefault("gateway.routeBatchConcurrency", DefaultGatewayRouteBatchConcurrency)
	v.SetDefault("gateway.usageErrorBodyBytes", DefaultGatewayUsageErrorBodyBytes)
	v.SetDefault("gateway.usageErrorMsgRunes", DefaultGatewayUsageErrorMsgRunes)
	v.SetDefault("gateway.usageErrorHeaderValueRunes", DefaultGatewayUsageErrorHeaderValueRunes)
	v.SetDefault("gateway.usageErrorHeadersJSONBytes", DefaultGatewayUsageErrorHeadersJSONBytes)
	v.SetDefault("gateway.streamKeepaliveSeconds", DefaultGatewayStreamKeepaliveSeconds)
	v.SetDefault("gateway.streamIdleTimeoutSeconds", DefaultGatewayStreamIdleTimeoutSeconds)
	v.SetDefault("gateway.streamMaxLineBytes", DefaultGatewayStreamMaxLineBytes)

	v.SetDefault("intelligence.lmfpdDataDir", "./data/lmfpd")
	v.SetDefault("intelligence.tickIntervalSeconds", 5)
	v.SetDefault("intelligence.concurrency", 3)
	v.SetDefault("intelligence.backoffEnabled", true)
	v.SetDefault("intelligence.backoffTransientMaxSeconds", DefaultBackoffTransientMaxSeconds)
	v.SetDefault("intelligence.backoffRateLimitMaxSeconds", DefaultBackoffRateLimitMaxSeconds)
	v.SetDefault("intelligence.backoffAuthMaxSeconds", DefaultBackoffAuthMaxSeconds)

	v.SetDefault("embed.cacheTTLSeconds", DefaultEmbedCacheTTLSeconds)
	v.SetDefault("embed.rateLimitEnabled", true)
	v.SetDefault("embed.rateLimitRPS", DefaultEmbedRateLimitRPS)
	v.SetDefault("embed.rateLimitBurst", DefaultEmbedRateLimitBurst)

	v.SetDefault("siteOps.allowUnauthenticatedLocalOnly", false)
	v.SetDefault("siteOps.allowLoopback", false)
	v.SetDefault("siteOps.defaultPollIntervalSeconds", DefaultSiteOpsPollIntervalSeconds)
	v.SetDefault("siteOps.defaultAuditBudgetPerDay", DefaultSiteOpsAuditBudgetPerDay)
	v.SetDefault("siteOps.globalMaxConcurrency", DefaultSiteOpsGlobalMaxConcurrency)
	v.SetDefault("siteOps.perInstanceMaxConcurrency", DefaultSiteOpsPerInstanceMaxConcurrency)
	v.SetDefault("siteOps.perInstanceQueueLimit", DefaultSiteOpsPerInstanceQueueLimit)
	v.SetDefault("siteOps.queueWaitTimeoutSeconds", DefaultSiteOpsQueueWaitTimeoutSeconds)
	v.SetDefault("siteOps.requestTimeoutSeconds", DefaultSiteOpsRequestTimeoutSeconds)
	v.SetDefault("siteOps.batchTimeoutSeconds", DefaultSiteOpsBatchTimeoutSeconds)
	v.SetDefault("siteOps.maxResponseBytes", DefaultSiteOpsMaxResponseBytes)
	v.SetDefault("siteOps.groupArchiveIntervalSeconds", DefaultSiteOpsGroupArchiveIntervalSecond)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "text")
}

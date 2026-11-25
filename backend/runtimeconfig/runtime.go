// Package runtimeconfig 管理可热更新的运行时配置。
package runtimeconfig

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/auth"
	"github.com/bejix/upstream-ops/backend/channel"
	"github.com/bejix/upstream-ops/backend/config"
	embedsvc "github.com/bejix/upstream-ops/backend/embed"
	"github.com/bejix/upstream-ops/backend/gateway"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/notify"
	"github.com/bejix/upstream-ops/backend/scheduler"
	"github.com/bejix/upstream-ops/backend/siteops"
	"github.com/gin-gonic/gin"
)

type SchedulerFactory func(config.SchedulerConfig, config.ProxyConfig) *scheduler.Scheduler

type Manager struct {
	mu               sync.RWMutex
	applyMu          sync.Mutex
	configPath       string
	securitySecret   string
	log              *slog.Logger
	dispatcher       *notify.Dispatcher
	channelSvc       *channel.Service
	gatewaySvc       *gateway.Service
	intelligenceSvc  *intelligence.Service
	embedSvc         *embedsvc.Service
	siteOpsSvc       *siteops.Service
	schedulerFactory SchedulerFactory
	auth             *auth.Service
	scheduler        *scheduler.Scheduler
	proxyConfig      config.ProxyConfig
	upstreamConfig   config.UpstreamConfig
	gatewayConfig    config.GatewayConfig
}

type ApplyResult struct {
	AppliedSections []string `json:"applied_sections"`
	Message         string   `json:"message"`
}

func New(
	configPath string,
	securitySecret string,
	log *slog.Logger,
	dispatcher *notify.Dispatcher,
	channelSvc *channel.Service,
	gatewaySvc *gateway.Service,
	authSvc *auth.Service,
	schedulerSvc *scheduler.Scheduler,
	proxyConfig config.ProxyConfig,
	upstreamConfig config.UpstreamConfig,
	gatewayConfig config.GatewayConfig,
	schedulerFactory SchedulerFactory,
) *Manager {
	return &Manager{
		configPath:       configPath,
		securitySecret:   securitySecret,
		log:              log,
		dispatcher:       dispatcher,
		channelSvc:       channelSvc,
		gatewaySvc:       gatewaySvc,
		schedulerFactory: schedulerFactory,
		auth:             authSvc,
		scheduler:        schedulerSvc,
		proxyConfig:      proxyConfig,
		upstreamConfig:   upstreamConfig.WithDefaults(),
		gatewayConfig:    gatewayConfig.WithDefaults(),
	}
}

// SetIntelligenceService 绑定外挂式监测服务，用于热重载代理、上游、退避策略、调度器与外挂指纹库目录配置。
func (m *Manager) SetIntelligenceService(svc *intelligence.Service) {
	m.mu.Lock()
	m.intelligenceSvc = svc
	m.mu.Unlock()
}

// SetEmbedService 绑定外部嵌入服务，用于热重载微缓存 TTL 与按 IP 限流配置。
func (m *Manager) SetEmbedService(svc *embedsvc.Service) {
	m.mu.Lock()
	m.embedSvc = svc
	m.mu.Unlock()
}

// SetSiteOpsService 绑定自有站点运维服务，用于白名单参数与鉴权开关热重载（不双跑 worker）。
func (m *Manager) SetSiteOpsService(svc *siteops.Service) {
	m.mu.Lock()
	m.siteOpsSvc = svc
	m.mu.Unlock()
}

func (m *Manager) ConfigPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configPath
}

func (m *Manager) CurrentAuth() *auth.Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auth
}

func (m *Manager) CurrentProxy() config.ProxyConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.proxyConfig
}

func (m *Manager) CurrentUpstream() config.UpstreamConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upstreamConfig
}

func (m *Manager) CurrentGateway() config.GatewayConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gatewayConfig
}

func (m *Manager) AuthMiddleware() gin.HandlerFunc {
	whitelist := map[string]struct{}{
		"/healthz":        {},
		"/api/version":    {},
		"/api/auth/login": {},
	}
	return func(c *gin.Context) {
		if _, ok := whitelist[c.FullPath()]; ok {
			c.Next()
			return
		}
		if _, ok := whitelist[c.Request.URL.Path]; ok {
			c.Next()
			return
		}
		svc := m.CurrentAuth()
		if svc == nil {
			c.Next()
			return
		}
		svc.Middleware()(c)
	}
}

func (m *Manager) ApplyFromFile() (*ApplyResult, error) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	m.mu.RLock()
	path := m.configPath
	secret := m.securitySecret
	dispatcher := m.dispatcher
	channelSvc := m.channelSvc
	gatewaySvc := m.gatewaySvc
	intelligenceSvc := m.intelligenceSvc
	embedSvc := m.embedSvc
	siteOpsSvc := m.siteOpsSvc
	factory := m.schedulerFactory
	oldScheduler := m.scheduler
	m.mu.RUnlock()

	cfg, err := config.LoadFile(path)
	if err != nil {
		return nil, err
	}

	authSvc, err := BuildAuth(cfg.Auth, secret)
	if err != nil {
		return nil, err
	}

	// 先构建并启动新 scheduler，确保所有可能失败的步骤完成后再统一切换各子服务配置，避免半应用状态。
	newScheduler := factory(cfg.Scheduler, cfg.Proxy)
	if err := newScheduler.Start(); err != nil {
		return nil, err
	}

	if dispatcher != nil {
		dispatcher.UpdatePolicy(notify.Policy{
			NotificationPrefix:                       cfg.App.NotificationPrefix,
			MinChangePct:                             cfg.Notifications.MinChangePct,
			BalanceLowCooldown:                       time.Duration(cfg.Notifications.BalanceLowCooldownMinutes) * time.Minute,
			SubscriptionDailyRemainingThresholdPct:   cfg.Notifications.SubscriptionDailyRemainingThresholdPct,
			SubscriptionWeeklyRemainingThresholdPct:  cfg.Notifications.SubscriptionWeeklyRemainingThresholdPct,
			SubscriptionMonthlyRemainingThresholdPct: cfg.Notifications.SubscriptionMonthlyRemainingThresholdPct,
			SubscriptionExpiryThreshold:              time.Duration(cfg.Notifications.SubscriptionExpiryThresholdHours) * time.Hour,
			SubscriptionAlertCooldown:                time.Duration(cfg.Notifications.SubscriptionAlertCooldownMinutes) * time.Minute,
			IntelligenceAlertCooldown:                time.Duration(cfg.Notifications.IntelligenceAlertCooldownMinutes) * time.Minute,
			SendMaxAttempts:                          cfg.Notifications.SendMaxAttempts,
		})
		dispatcher.UpdateProxyConfig(cfg.Proxy)
	}
	if channelSvc != nil {
		channelSvc.UpdateProxyConfig(cfg.Proxy)
		channelSvc.UpdateUpstreamConfig(cfg.Upstream)
	}
	gwCfg := cfg.Gateway.WithDefaults()
	if gatewaySvc != nil {
		gatewaySvc.UpdateProxyConfig(cfg.Proxy)
		gatewaySvc.UpdateUpstreamConfig(cfg.Upstream)
		gatewaySvc.UpdateGatewayConfig(gwCfg)
		gatewaySvc.UpdatePricingConfig(cfg.Pricing)
	}
	if intelligenceSvc != nil {
		intelCfg := cfg.Intelligence.WithDefaults()
		intelligenceSvc.UpdateProxyConfig(cfg.Proxy)
		intelligenceSvc.UpdateUpstreamConfig(cfg.Upstream)
		intelligenceSvc.SetLMFPDDataDir(intelCfg.LMFPDDataDir)
		intelligenceSvc.UpdateBackoffConfig(intelCfg)
		if err := intelligenceSvc.ReloadLMFPD(); err != nil && m.log != nil {
			m.log.Warn("reload lmfpd reference bank failed, fallback active", "dir", intelCfg.LMFPDDataDir, "err", err)
		}
		if rebuilt := intelligenceSvc.Reconfigure(time.Duration(intelCfg.TickIntervalSeconds)*time.Second, intelCfg.Concurrency); rebuilt && m.log != nil {
			m.log.Info("intelligence runner reconfigured",
				"tick_interval_seconds", intelCfg.TickIntervalSeconds,
				"concurrency", intelCfg.Concurrency,
			)
		}
	}
	if embedSvc != nil {
		embedSvc.UpdateRuntimeConfig(cfg.Embed.WithDefaults())
	}
	if siteOpsSvc != nil {
		siteOpsSvc.ApplyRuntimeConfig(authSvc != nil, cfg.SiteOps.WithDefaults(), cfg.Proxy, cfg.Upstream.WithDefaults())
	}

	m.mu.Lock()
	m.auth = authSvc
	m.scheduler = newScheduler
	m.proxyConfig = cfg.Proxy
	m.upstreamConfig = cfg.Upstream.WithDefaults()
	m.gatewayConfig = gwCfg
	m.mu.Unlock()

	if oldScheduler != nil {
		oldScheduler.Stop()
	}

	sections := []string{"app", "auth", "scheduler", "notifications", "retention", "proxy", "upstream", "gateway", "pricing", "intelligence", "embed", "siteOps"}
	if m.log != nil {
		m.log.Info("runtime config applied",
			"sections", sections,
			"config_path", path,
		)
	}

	return &ApplyResult{
		AppliedSections: sections,
		Message:         "app、auth、scheduler、notifications、retention、proxy、upstream、gateway、intelligence、embed、siteOps 已立即生效",
	}, nil
}

// Stop 优雅停止当前生效的最新 scheduler（包含热重载替换后的实例），支持幂等调用。
func (m *Manager) Stop() {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	m.mu.Lock()
	sch := m.scheduler
	m.scheduler = nil
	m.mu.Unlock()

	if sch != nil {
		sch.Stop()
	}
}

// UpdateAuthCredentials 更新配置文件中的管理员账号密码并立即热切换当前认证服务。
func (m *Manager) UpdateAuthCredentials(username, password string) (*auth.Service, error) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	m.mu.RLock()
	path := m.configPath
	secret := m.securitySecret
	siteOpsSvc := m.siteOpsSvc
	proxyCfg := m.proxyConfig
	upstreamCfg := m.upstreamConfig
	m.mu.RUnlock()

	cfg, err := config.LoadFile(path)
	if err != nil {
		return nil, err
	}
	cfg.Auth.Enabled = true
	cfg.Auth.Username = username
	cfg.Auth.Password = password
	if cfg.Auth.SessionTTLHours <= 0 {
		cfg.Auth.SessionTTLHours = 168
	}

	authSvc, err := BuildAuth(cfg.Auth, secret)
	if err != nil {
		return nil, err
	}
	if err := config.Save(path, cfg); err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.auth = authSvc
	m.mu.Unlock()

	if siteOpsSvc != nil {
		siteOpsSvc.ApplyRuntimeConfig(authSvc != nil, cfg.SiteOps.WithDefaults(), proxyCfg, upstreamCfg)
	}
	if m.log != nil {
		m.log.Info("admin credentials initialized", "username", username)
	}
	return authSvc, nil
}

// BuildAuth 根据 AuthConfig 与回退密钥构建认证服务；关闭鉴权时返回 nil, nil。
func BuildAuth(cfg config.AuthConfig, securitySecret string) (*auth.Service, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	tokenSecret := cfg.TokenSecret
	if tokenSecret == "" {
		tokenSecret = securitySecret
	}
	svc, err := auth.NewWithOptions(
		cfg.Username,
		cfg.Password,
		tokenSecret,
		time.Duration(cfg.SessionTTLHours)*time.Hour,
		cfg.IsDefaultPassword(),
	)
	if err != nil {
		return nil, fmt.Errorf("init auth: %w", err)
	}
	return svc, nil
}

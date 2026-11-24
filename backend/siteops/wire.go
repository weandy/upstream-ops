package siteops

import (
	"log/slog"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

// NewDefaultService 使用真实出站门禁、Admin 客户端、能力探测器与 GORM 仓储组装默认 SiteOps 服务。
func NewDefaultService(
	db *gorm.DB,
	cipher *crypto.Cipher,
	siteCfg config.SiteOpsConfig,
	proxyCfg config.ProxyConfig,
	upstreamCfg config.UpstreamConfig,
	authActive bool,
	log *slog.Logger,
) (*Service, *storage.SiteOpsStore) {
	cfg := copySiteOpsConfig(siteCfg)
	rawStore := storage.NewSiteOpsStore(db)
	guard := NewOutboundGuard(cfg.AllowedOrigins, cfg.AllowedCIDRs)
	client := NewClient(cfg, proxyCfg, upstreamCfg, guard, log)
	prober := NewProber(client)
	svc := NewService(ServiceDeps{
		Store:          NewStorageStore(rawStore),
		Cipher:         cipher,
		Validator:      guard,
		Client:         client,
		Prober:         prober,
		Log:            log,
		InitialConfig:  cfg,
		AuthActive:     authActive,
		MaskAPIKeyHint: MaskAPIKeyHint,
		SanitizeError:  SanitizeErrorText,
	})
	return svc, rawStore
}

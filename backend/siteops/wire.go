package siteops

import (
	"context"
	"gorm.io/gorm/logger"
	"log/slog"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/notify"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

var _ DirectNotifier = (*notify.Dispatcher)(nil)

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
	guard := NewOutboundGuard(cfg.AllowLoopback, cfg.AllowedOrigins, cfg.AllowedCIDRs)
	client := NewClient(cfg, proxyCfg, upstreamCfg, guard, log)
	prober := NewProber(client)
	svc := NewService(ServiceDeps{
		Store:          NewStorageStore(rawStore),
		mailEvidence:   mailTestEvidence(db),
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

// Customer item lists exclude test deliveries. Read only the public SMTP evidence.
func mailTestEvidence(db *gorm.DB) func(context.Context, storage.MailScopeKey, uint, *MailTestResult) error {
	silent := db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	return func(ctx context.Context, key storage.MailScopeKey, batch uint, out *MailTestResult) error {
		var row storage.SiteOpsMailItem
		err := silent.WithContext(ctx).Model(&storage.SiteOpsMailItem{}).Select("smtp_stage", "smtp_code", "error_code").Where("instance_id = ? AND batch_id = ? AND user_id = 0", key.InstanceID, batch).Limit(1).Find(&row).Error
		if err != nil {
			return err
		}
		out.SMTPStage, out.SMTPCode = MailSMTPStage(row.SMTPStage), row.SMTPCode
		if row.ErrorCode != "" {
			out.ErrorCode = ErrorCode(row.ErrorCode)
		}
		return nil
	}
}

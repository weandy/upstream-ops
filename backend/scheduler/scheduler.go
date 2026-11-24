// Package scheduler 用 robfig/cron 触发周期性扫描。
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/bejix/upstream-ops/backend/captcha"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/monitor"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/robfig/cron/v3"
)

// Scheduler 周期性任务调度器。
type Scheduler struct {
	cfg           config.SchedulerConfig
	log           *slog.Logger
	cron          *cron.Cron
	monitor       *monitor.Service
	monLogs       *storage.MonitorLogs
	rates         *storage.Rates
	notifies      *storage.Notifications
	announcements *storage.UpstreamAnnouncements
	captchas      *storage.Captchas
	cipher        *crypto.Cipher
	gatewayResort gatewayRateResortService
	probeLiveness *storage.ProbeLivenessSamples
	probeRuns     *storage.ProbeIntelligenceRuns
	siteOps       *storage.SiteOpsStore
	proxy         config.ProxyConfig
}

// gatewayRateResortService 倍率扫描后重排开启了「渠道分组价格倍率重排」的网关组。
type gatewayRateResortService interface {
	ResortRoutesOnRateScan(ctx context.Context)
}

// New 构造调度器。
func New(
	cfg config.SchedulerConfig,
	m *monitor.Service,
	monLogs *storage.MonitorLogs,
	rates *storage.Rates,
	notifies *storage.Notifications,
	announcements *storage.UpstreamAnnouncements,
	captchas *storage.Captchas,
	cipher *crypto.Cipher,
	gatewayResort gatewayRateResortService,
	proxy config.ProxyConfig,
	log *slog.Logger,
) *Scheduler {
	return &Scheduler{
		cfg:           cfg,
		log:           log,
		cron:          cron.New(cron.WithSeconds()),
		monitor:       m,
		monLogs:       monLogs,
		rates:         rates,
		notifies:      notifies,
		announcements: announcements,
		captchas:      captchas,
		cipher:        cipher,
		gatewayResort: gatewayResort,
		proxy:         proxy,
	}
}

// SetIntelligenceRepos 注入主动测活样本与智力监测记录仓储，用于定时清理过期数据。
func (s *Scheduler) SetIntelligenceRepos(liveness *storage.ProbeLivenessSamples, runs *storage.ProbeIntelligenceRuns) {
	s.probeLiveness = liveness
	s.probeRuns = runs
}

// SetSiteOpsStore 注入自有站点运维仓储，用于分块清理过期时序快照。
func (s *Scheduler) SetSiteOpsStore(store *storage.SiteOpsStore) {
	s.siteOps = store
}

// Start 注册 cron 任务并启动。
func (s *Scheduler) Start() error {
	if s.cfg.BalanceCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.BalanceCron, s.runBalance); err != nil {
			return err
		}
	}
	if s.cfg.RateCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.RateCron, s.runRates); err != nil {
			return err
		}
	}
	if s.cfg.Retention.Cron != "" && s.hasRetention() {
		if _, err := s.cron.AddFunc(s.cfg.Retention.Cron, s.runRetention); err != nil {
			return err
		}
	}
	s.cron.Start()
	s.log.Info("scheduler started",
		"balanceCron", s.cfg.BalanceCron,
		"rateCron", s.cfg.RateCron,
		"retentionCron", s.cfg.Retention.Cron,
		"concurrency", s.cfg.Concurrency,
	)
	return nil
}

// Stop 停止调度器。
func (s *Scheduler) Stop() {
	if s.cron != nil {
		<-s.cron.Stop().Done()
	}
}

func (s *Scheduler) runBalance() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.monitor.ScanAllBalances(ctx)
	if s.captchas != nil && s.cipher != nil {
		if _, err := captcha.RefreshAllBalancesWithProxy(ctx, s.captchas, s.cipher, s.log, s.proxy); err != nil {
			s.log.Warn("refresh captcha balances failed", "err", err)
		}
	}
}

func (s *Scheduler) runRates() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if s.monitor != nil {
		s.monitor.ScanAllRates(ctx)
	}
	if s.gatewayResort != nil {
		s.gatewayResort.ResortRoutesOnRateScan(ctx)
	}
}

func (s *Scheduler) hasRetention() bool {
	r := s.cfg.Retention
	return r.MonitorLogsDays > 0 ||
		r.BalanceSnapshotsDays > 0 ||
		r.NotificationLogsDays > 0 ||
		r.AnnouncementsDays > 0 ||
		r.ProbeLivenessDays > 0 ||
		r.ProbeIntelligenceRunsDays > 0 ||
		r.SiteOpsSnapshotsDays > 0 ||
		r.SiteOpsGroupSnapshotsDays > 0 ||
		r.SiteOpsEventsDays > 0 ||
		r.SiteOpsAlertDeliveriesDays > 0 ||
		r.SiteOpsAccountWindowSamplesDays > 0 ||
		r.SiteOpsMailBatchesDays > 0
}

// runRetention 按配置删除过期历史。任一表失败不影响其它，全部错误写日志。
func (s *Scheduler) runRetention() {
	r := s.cfg.Retention
	now := time.Now()

	if r.MonitorLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.MonitorLogsDays)
		n, err := s.monLogs.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention monitor_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention monitor_logs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.BalanceSnapshotsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.BalanceSnapshotsDays)
		n, err := s.rates.DeleteBalanceSnapshotsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention balance_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention balance_snapshots deleted", "rows", n, "before", cutoff)
		}

		n, err = s.rates.DeleteCostSnapshotsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention cost_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention cost_snapshots deleted", "rows", n, "before", cutoff)
		}
	}

	if r.NotificationLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.NotificationLogsDays)
		n, err := s.notifies.DeleteLogsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention notification_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention notification_logs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.AnnouncementsDays > 0 && s.announcements != nil {
		cutoff := now.AddDate(0, 0, -r.AnnouncementsDays)
		n, err := s.announcements.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention announcements failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention announcements deleted", "rows", n, "before", cutoff)
		}
	}

	if r.ProbeLivenessDays > 0 && s.probeLiveness != nil {
		cutoff := now.AddDate(0, 0, -r.ProbeLivenessDays)
		n, err := s.probeLiveness.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention probe_liveness_samples failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention probe_liveness_samples deleted", "rows", n, "before", cutoff)
		}
	}

	if r.ProbeIntelligenceRunsDays > 0 && s.probeRuns != nil {
		cutoff := now.AddDate(0, 0, -r.ProbeIntelligenceRunsDays)
		n, err := s.probeRuns.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention probe_intelligence_runs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention probe_intelligence_runs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.SiteOpsSnapshotsDays > 0 && s.siteOps != nil {
		cutoff := now.UTC().AddDate(0, 0, -r.SiteOpsSnapshotsDays)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n, err := s.siteOps.DeleteSnapshotsBeforeChunked(ctx, cutoff, 500, 10000, 2*time.Second)
		cancel()
		if err != nil {
			s.log.Warn("retention site_ops_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention site_ops_snapshots deleted", "rows", n, "before", cutoff)
		}
	}

	if r.SiteOpsGroupSnapshotsDays > 0 && s.siteOps != nil {
		cutoff := now.UTC().AddDate(0, 0, -r.SiteOpsGroupSnapshotsDays)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n, err := s.siteOps.DeleteGroupSnapshotsBeforeChunked(ctx, cutoff, 500, 10000, 2*time.Second)
		cancel()
		if err != nil {
			s.log.Warn("retention site_ops_group_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention site_ops_group_snapshots deleted", "rows", n, "before", cutoff)
		}
	}

	if r.SiteOpsEventsDays > 0 && s.siteOps != nil {
		cutoff := now.UTC().AddDate(0, 0, -r.SiteOpsEventsDays)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n, err := s.siteOps.DeleteEventsBeforeChunked(ctx, cutoff, 500, 10000, 2*time.Second)
		cancel()
		if err != nil {
			s.log.Warn("retention site_ops_events failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention site_ops_events deleted", "rows", n, "before", cutoff)
		}
	}

	if r.SiteOpsAlertDeliveriesDays > 0 && s.siteOps != nil {
		cutoff := now.UTC().AddDate(0, 0, -r.SiteOpsAlertDeliveriesDays)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n, err := s.siteOps.DeleteDeliveriesBeforeChunked(ctx, cutoff, 500, 10000, 2*time.Second)
		cancel()
		if err != nil {
			s.log.Warn("retention site_ops_alert_deliveries failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention site_ops_alert_deliveries deleted", "rows", n, "before", cutoff)
		}
	}

	if r.SiteOpsAccountWindowSamplesDays > 0 && s.siteOps != nil {
		cutoff := now.UTC().AddDate(0, 0, -r.SiteOpsAccountWindowSamplesDays)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n, err := s.siteOps.DeleteWindowSamplesBeforeChunked(ctx, cutoff, 500, 10000, 2*time.Second)
		cancel()
		if err != nil {
			s.log.Warn("retention site_ops_account_window_samples failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention site_ops_account_window_samples deleted", "rows", n, "before", cutoff)
		}
	}
	if r.SiteOpsMailBatchesDays > 0 && s.siteOps != nil {
		cutoff := now.UTC().AddDate(0, 0, -r.SiteOpsMailBatchesDays)
		n, err := s.siteOps.DeleteFinishedMailBatchesBeforeChunked(context.Background(), cutoff, 200, 10000, 2*time.Second)
		if err != nil {
			s.log.Warn("retention site_ops_mail_batches failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention site_ops_mail_batches deleted", "rows", n, "before", cutoff)
		}
	}
}

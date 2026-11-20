package runtimeconfig

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/channel"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/notify"
	"github.com/bejix/upstream-ops/backend/scheduler"
	"github.com/bejix/upstream-ops/backend/storage"
)

type fakeHTTPConfigConnector struct {
	cfg connector.HTTPConfig
}

func (f *fakeHTTPConfigConnector) SetHTTPConfig(cfg connector.HTTPConfig) {
	f.cfg = cfg
}

func TestApplyFromFileUpdatesUpstreamConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{
			BalanceCron: "",
			RateCron:    "",
			Retention:   config.RetentionConfig{Cron: ""},
		},
		Upstream: config.UpstreamConfig{
			TimeoutSeconds: 45,
			UserAgent:      "custom-agent",
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	channelSvc := channel.NewService(nil, nil, nil, nil, nil, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := New(
		path,
		"",
		log,
		nil,
		channelSvc,
		nil,
		nil,
		nil,
		config.ProxyConfig{},
		config.UpstreamConfig{},
		config.GatewayConfig{},
		func(scfg config.SchedulerConfig, pcfg config.ProxyConfig) *scheduler.Scheduler {
			return scheduler.New(scfg, nil, nil, nil, nil, nil, nil, nil, nil, pcfg, log)
		},
	)

	result, err := mgr.ApplyFromFile()
	if err != nil {
		t.Fatalf("ApplyFromFile: %v", err)
	}
	if len(result.AppliedSections) == 0 {
		t.Fatalf("applied sections empty")
	}
	if got := mgr.CurrentUpstream(); got.TimeoutSeconds != 45 || got.UserAgent != "custom-agent" {
		t.Fatalf("current upstream = %#v", got)
	}

	conn := &fakeHTTPConfigConnector{}
	channelSvc.ApplyHTTPConfig(conn)
	if conn.cfg.Timeout != 45*time.Second {
		t.Fatalf("timeout = %s", conn.cfg.Timeout)
	}
	if conn.cfg.UserAgent != "custom-agent" {
		t.Fatalf("user agent = %q", conn.cfg.UserAgent)
	}
}

func TestApplyFromFileUpdatesIntelligenceAndAlertCooldown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{
			Retention: config.RetentionConfig{
				ProbeLivenessDays:         7,
				ProbeIntelligenceRunsDays: 30,
			},
		},
		Notifications: config.NotificationsConfig{
			IntelligenceAlertCooldownMinutes: 45,
		},
		Intelligence: config.IntelligenceConfig{
			LMFPDDataDir:        filepath.Join(dir, "custom-lmfpd"),
			TickIntervalSeconds: 15,
			Concurrency:         4,
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	db, err := storage.Open(storage.DBConfig{Driver: storage.DBDriverSQLite, Path: filepath.Join(dir, "rt.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_ = storage.AutoMigrate(db)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := notify.NewDispatcher(storage.NewNotifications(db), nil, log, notify.Policy{})
	intelSvc := intelligence.NewService(db, nil, nil, nil, log)
	mgr := New(
		path, "", log, disp, nil, nil, nil, nil,
		config.ProxyConfig{}, config.UpstreamConfig{}, config.GatewayConfig{},
		func(scfg config.SchedulerConfig, pcfg config.ProxyConfig) *scheduler.Scheduler {
			return scheduler.New(scfg, nil, nil, nil, nil, nil, nil, nil, nil, pcfg, log)
		},
	)
	mgr.SetIntelligenceService(intelSvc)

	if _, err := mgr.ApplyFromFile(); err != nil {
		t.Fatalf("ApplyFromFile: %v", err)
	}
	if got := disp.Policy().IntelligenceAlertCooldown; got != 45*time.Minute {
		t.Fatalf("expected IntelligenceAlertCooldown=45m, got %s", got)
	}
}

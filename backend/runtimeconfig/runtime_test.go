package runtimeconfig

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/channel"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/connector"
	embedsvc "github.com/bejix/upstream-ops/backend/embed"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/notify"
	"github.com/bejix/upstream-ops/backend/scheduler"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
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

func TestApplyFromFileReconfiguresIntelligenceRunnerAndEmbedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	ttlZero := 0
	rlDisabled := false
	backoffDisabled := false
	cfg := &config.Config{
		Intelligence: config.IntelligenceConfig{
			LMFPDDataDir:               filepath.Join(dir, "lmfpd"),
			TickIntervalSeconds:        20,
			Concurrency:                6,
			BackoffEnabled:             &backoffDisabled,
			BackoffTransientMaxSeconds: 1800,
			BackoffRateLimitMaxSeconds: 3600,
			BackoffAuthMaxSeconds:      7200,
		},
		Embed: config.EmbedConfig{
			CacheTTLSeconds:  &ttlZero,
			RateLimitEnabled: &rlDisabled,
			RateLimitRPS:     12,
			RateLimitBurst:   60,
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	db, err := storage.Open(storage.DBConfig{Driver: storage.DBDriverSQLite, Path: filepath.Join(dir, "rt2.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_ = storage.AutoMigrate(db)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	intelSvc := intelligence.NewService(db, nil, nil, nil, log)
	intelSvc.Start(10*time.Second, 3)
	defer intelSvc.Stop()

	embedSvc := embedsvc.NewService(db, intelSvc)

	mgr := New(
		path, "", log, nil, nil, nil, nil, nil,
		config.ProxyConfig{}, config.UpstreamConfig{}, config.GatewayConfig{},
		func(scfg config.SchedulerConfig, pcfg config.ProxyConfig) *scheduler.Scheduler {
			return scheduler.New(scfg, nil, nil, nil, nil, nil, nil, nil, nil, pcfg, log)
		},
	)
	mgr.SetIntelligenceService(intelSvc)
	mgr.SetEmbedService(embedSvc)

	res, err := mgr.ApplyFromFile()
	if err != nil {
		t.Fatalf("ApplyFromFile #1: %v", err)
	}
	hasEmbed := false
	for _, s := range res.AppliedSections {
		if s == "embed" {
			hasEmbed = true
		}
	}
	if !hasEmbed {
		t.Fatalf("expected 'embed' in AppliedSections, got %v", res.AppliedSections)
	}

	running, tick, conc, gen := intelSvc.RunnerStats()
	if !running || tick != 20*time.Second || conc != 6 || gen != 2 {
		t.Fatalf("unexpected runner stats after ApplyFromFile: running=%v tick=%s conc=%d gen=%d", running, tick, conc, gen)
	}

	// 再次应用相同配置应保持幂等，不触发多余重建。
	if _, err := mgr.ApplyFromFile(); err != nil {
		t.Fatalf("ApplyFromFile #2: %v", err)
	}
	_, _, _, gen2 := intelSvc.RunnerStats()
	if gen2 != gen {
		t.Fatalf("expected idempotent runner generation %d, got %d", gen, gen2)
	}

	// 验证 embedSvc 已热更新为 cacheTTLSeconds=0（连续 2 次公开状态请求均返回 BYPASS 且查库 2 次）。
	gin.SetMode(gin.TestMode)
	r := gin.New()
	embedSvc.RegisterPublic(r)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/public/status", nil)
		req.RemoteAddr = "198.51.100.10:12345"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status request #%d failed: code=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Embed-Cache"); got != "BYPASS" {
			t.Fatalf("expected X-Embed-Cache=BYPASS after hot-disabling cache, got %q", got)
		}
	}
	if got := embedSvc.SnapshotBuildCount(); got != 2 {
		t.Fatalf("expected SnapshotBuildCount=2 after hot-disabling cache (TTL=0), got %d", got)
	}
}

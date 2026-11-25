package config

import (
	"path/filepath"
	"testing"
)

func TestLoadAppliesUpstreamDefaults(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Upstream.TimeoutSeconds != DefaultUpstreamTimeoutSeconds {
		t.Fatalf("timeout seconds = %d", cfg.Upstream.TimeoutSeconds)
	}
	if cfg.Upstream.UserAgent != DefaultUpstreamUserAgent {
		t.Fatalf("user agent = %q", cfg.Upstream.UserAgent)
	}
	if !cfg.Pricing.Enabled || cfg.Pricing.RemoteURL != DefaultPricingRemoteURL {
		t.Fatalf("pricing = %#v", cfg.Pricing)
	}
	if !cfg.Auth.Enabled || cfg.Auth.Username != DefaultAuthUsername || cfg.Auth.Password != DefaultAuthPassword || !cfg.Auth.IsDefaultPassword() {
		t.Fatalf("expected default auth enabled with admin/admin_ops, got %+v", cfg.Auth)
	}
}

func TestBootstrapEmptyAdminEnvFallsBackToDefaultCredentials(t *testing.T) {
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD", "")

	cfg, _, err := LoadWithPath(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadWithPath: %v", err)
	}
	if !cfg.Auth.Enabled || cfg.Auth.Username != DefaultAuthUsername || cfg.Auth.Password != DefaultAuthPassword {
		t.Fatalf("expected empty env bootstrap fallback to admin/admin_ops, got %+v", cfg.Auth)
	}
}

func TestUpstreamConfigWithDefaultsKeepsCustomUserAgent(t *testing.T) {
	cfg := UpstreamConfig{
		TimeoutSeconds: 0,
		UserAgent:      "custom-agent",
	}.WithDefaults()
	if cfg.TimeoutSeconds != DefaultUpstreamTimeoutSeconds {
		t.Fatalf("timeout seconds = %d", cfg.TimeoutSeconds)
	}
	if cfg.UserAgent != "custom-agent" {
		t.Fatalf("user agent = %q", cfg.UserAgent)
	}
}

func TestGatewayConfigWithDefaults(t *testing.T) {
	cfg := GatewayConfig{}.WithDefaults()
	if cfg.TempPauseSeconds != DefaultGatewayTempPauseSeconds {
		t.Fatalf("temp pause = %d", cfg.TempPauseSeconds)
	}
	if cfg.ForwardTimeoutSeconds != DefaultGatewayForwardTimeoutSeconds {
		t.Fatalf("forward timeout = %d", cfg.ForwardTimeoutSeconds)
	}
	if cfg.RouteBatchConcurrency != DefaultGatewayRouteBatchConcurrency {
		t.Fatalf("batch concurrency = %d", cfg.RouteBatchConcurrency)
	}
	custom := GatewayConfig{RouteBatchConcurrency: 16, ForwardTimeoutSeconds: 120}.WithDefaults()
	if custom.RouteBatchConcurrency != 16 || custom.ForwardTimeoutSeconds != 120 {
		t.Fatalf("custom = %#v", custom)
	}
	if custom.ModelsCacheTTLSeconds != DefaultGatewayModelsCacheTTLSeconds {
		t.Fatalf("models cache ttl = %d", custom.ModelsCacheTTLSeconds)
	}
}

func TestPostgresDatabaseEnvConfigBinding(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "postgres")
	t.Setenv("DATABASE_HOST", "pg.local")
	t.Setenv("DATABASE_PORT", "5432")
	t.Setenv("DATABASE_USER", "uops")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("DATABASE_NAME", "uops_db")
	t.Setenv("DATABASE_SSLMODE", "require")
	t.Setenv("DATABASE_TIMEZONE", "UTC")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sc := cfg.Database.ToStorageConfig()
	if sc.Driver != "postgres" || sc.Host != "pg.local" || sc.Port != 5432 || sc.SSLMode != "require" || sc.TimeZone != "UTC" {
		t.Fatalf("unexpected postgres storage config: %+v", sc)
	}
	if dsn := sc.PostgresDSN(); dsn != "postgres://uops:secret@pg.local:5432/uops_db?TimeZone=UTC&sslmode=require" {
		t.Fatalf("unexpected postgres dsn: %q", dsn)
	}
}

func TestEmbedAndIntelligenceBackoffConfigDefaults(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !cfg.Intelligence.IsBackoffEnabled() ||
		cfg.Intelligence.BackoffTransientMaxSeconds != DefaultBackoffTransientMaxSeconds ||
		cfg.Intelligence.BackoffRateLimitMaxSeconds != DefaultBackoffRateLimitMaxSeconds ||
		cfg.Intelligence.BackoffAuthMaxSeconds != DefaultBackoffAuthMaxSeconds {
		t.Fatalf("unexpected intelligence defaults: %+v", cfg.Intelligence)
	}
	if cfg.Embed.EffectiveCacheTTLSeconds() != DefaultEmbedCacheTTLSeconds ||
		!cfg.Embed.IsRateLimitEnabled() ||
		cfg.Embed.RateLimitRPS != DefaultEmbedRateLimitRPS ||
		cfg.Embed.RateLimitBurst != DefaultEmbedRateLimitBurst {
		t.Fatalf("unexpected embed defaults: %+v", cfg.Embed)
	}

	// 验证显式关闭（false / 0）不会被 WithDefaults 误覆写。
	zero := 0
	off := false
	customEmbed := EmbedConfig{
		CacheTTLSeconds:  &zero,
		RateLimitEnabled: &off,
		RateLimitRPS:     12,
		RateLimitBurst:   50,
	}.WithDefaults()
	if customEmbed.EffectiveCacheTTLSeconds() != 0 || customEmbed.IsRateLimitEnabled() {
		t.Fatalf("explicit disabled embed config should be preserved: %+v", customEmbed)
	}
	customIntel := IntelligenceConfig{BackoffEnabled: &off}.WithDefaults()
	if customIntel.IsBackoffEnabled() {
		t.Fatalf("explicit backoffEnabled=false should be preserved")
	}
}

func TestSaveAtomicReplacesWithoutLeavingTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := Save(path, &Config{App: AppConfig{Title: "First"}}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := Save(path, &Config{App: AppConfig{Title: "Second"}}); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.App.Title != "Second" {
		t.Fatalf("expected updated title Second, got %q", got.App.Title)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("glob dir: %v", err)
	}
	if len(matches) != 1 || matches[0] != path {
		t.Fatalf("expected only %s in dir after atomic save, got %v", path, matches)
	}
}

func TestLayeredConfigBootstrapFromEnvWhenFileMissing(t *testing.T) {
	t.Setenv("APP_SECRET", "bootstrap-app-secret-32bytes-minimum")
	t.Setenv("SERVER_PORT", "9091")
	t.Setenv("SERVER_MODE", "release")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("DATABASE_DRIVER", "sqlite")
	t.Setenv("DATABASE_PATH", "/tmp/boot.db")
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("ADMIN_USERNAME", "boss")
	t.Setenv("ADMIN_PASSWORD", "init-pass")
	t.Setenv("AUTH_TOKEN_SECRET", "init-token-secret")
	t.Setenv("SITEOPS_ALLOW_LOOPBACK", "true")
	t.Setenv("SCHEDULER_CONCURRENCY", "9")

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	cfg, used, err := LoadWithPath(missing)
	if err != nil {
		t.Fatalf("LoadWithPath: %v", err)
	}
	if used != "" {
		t.Fatalf("expected empty used path for missing file, got %q", used)
	}
	if cfg.Security.AppSecret != "bootstrap-app-secret-32bytes-minimum" ||
		cfg.Server.Port != 9091 ||
		cfg.Server.Mode != "release" ||
		cfg.Log.Level != "warn" ||
		cfg.Database.Path != "/tmp/boot.db" {
		t.Fatalf("infra env not applied on bootstrap: %+v %+v %+v %+v", cfg.Security, cfg.Server, cfg.Log, cfg.Database)
	}
	if !cfg.Auth.Enabled ||
		cfg.Auth.Username != "boss" ||
		cfg.Auth.Password != "init-pass" ||
		cfg.Auth.TokenSecret != "init-token-secret" ||
		!cfg.SiteOps.AllowLoopback ||
		cfg.Scheduler.Concurrency != 9 {
		t.Fatalf("dynamic bootstrap env not applied when file missing: auth=%+v siteOps=%+v scheduler=%+v", cfg.Auth, cfg.SiteOps, cfg.Scheduler)
	}
}

func TestLayeredConfigFileWinsForDynamicSectionsWhenFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := &Config{
		App: AppConfig{Title: "SavedOps", NotificationPrefix: "[Saved] "},
		Server: ServerConfig{
			Port: 8418,
			Mode: "debug",
		},
		Database: DatabaseConfig{
			Driver: "sqlite",
			Path:   "./data/old.db",
		},
		Security: SecurityConfig{
			AppSecret: "file-app-secret",
		},
		Auth: AuthConfig{
			Enabled:         true,
			Username:        "owner",
			Password:        "saved-file-password",
			TokenSecret:     "saved-file-token",
			SessionTTLHours: 72,
		},
		Scheduler: SchedulerConfig{
			Concurrency: 8,
		},
		SiteOps: SiteOpsConfig{
			AllowLoopback:  true,
			AllowedOrigins: []string{"http://sub2api:8080"},
		},
		Log: LogConfig{
			Level:  "debug",
			Format: "text",
		},
	}
	if err := Save(path, initial); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 模拟容器重启时 Compose 仍注入旧的环境变量（包括基础设施项 + 冲突的动态业务项）。
	t.Setenv("APP_SECRET", "env-app-secret-overrides-file")
	t.Setenv("SERVER_PORT", "9092")
	t.Setenv("SERVER_MODE", "release")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("DATABASE_DRIVER", "postgres")
	t.Setenv("DATABASE_HOST", "pg.internal")
	t.Setenv("DATABASE_PORT", "5432")
	t.Setenv("DATABASE_NAME", "uops")

	// 这些动态业务环境变量在 config.yaml 已存在后不得覆盖文件中的值。
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD", "env-pass-must-not-override")
	t.Setenv("AUTH_TOKEN_SECRET", "env-token-must-not-override")
	t.Setenv("SCHEDULER_CONCURRENCY", "2")
	t.Setenv("SITEOPS_ALLOW_LOOPBACK", "false")
	t.Setenv("APP_TITLE", "EnvTitleMustNotOverride")

	cold, used, err := LoadWithPath(path)
	if err != nil {
		t.Fatalf("LoadWithPath: %v", err)
	}
	if used != path {
		t.Fatalf("used = %q, want %q", used, path)
	}

	// 1. 基础设施层：环境变量覆盖文件。
	if cold.Security.AppSecret != "env-app-secret-overrides-file" ||
		cold.Server.Port != 9092 ||
		cold.Server.Mode != "release" ||
		cold.Log.Level != "error" ||
		cold.Log.Format != "json" ||
		cold.Database.Driver != "postgres" ||
		cold.Database.Host != "pg.internal" ||
		cold.Database.Port != 5432 ||
		cold.Database.Name != "uops" {
		t.Fatalf("infra env did not override file: sec=%+v srv=%+v log=%+v db=%+v", cold.Security, cold.Server, cold.Log, cold.Database)
	}

	// 2. 动态业务层：文件胜出，重启前后与热重载（LoadFile）完全一致。
	hot, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !cold.Auth.Enabled ||
		cold.Auth.Username != "owner" ||
		cold.Auth.Password != "saved-file-password" ||
		cold.Auth.TokenSecret != "saved-file-token" ||
		cold.Auth.SessionTTLHours != 72 {
		t.Fatalf("file auth was overwritten by env: %+v", cold.Auth)
	}
	if cold.App.Title != "SavedOps" || cold.Scheduler.Concurrency != 8 || !cold.SiteOps.AllowLoopback {
		t.Fatalf("file dynamic sections overwritten by env: app=%+v sched=%+v siteOps=%+v", cold.App, cold.Scheduler, cold.SiteOps)
	}
	if cold.Auth != hot.Auth || cold.App != hot.App || cold.Scheduler != hot.Scheduler || cold.SiteOps.AllowLoopback != hot.SiteOps.AllowLoopback {
		t.Fatalf("cold start and hot reload diverged on dynamic sections:\ncold=%+v\nhot=%+v", cold, hot)
	}
}

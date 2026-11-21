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

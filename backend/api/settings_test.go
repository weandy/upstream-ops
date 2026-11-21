package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/runtimeconfig"
	"github.com/gin-gonic/gin"
)

func TestSaveSettingsKeepsAppVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{
		App: config.AppConfig{
			Title:              "Old",
			NotificationPrefix: "[Old] ",
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	r := gin.New()
	api := r.Group("/api")
	registerSettings(api, &Deps{
		Runtime: runtimeconfig.New(path, "", nil, nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{}, config.GatewayConfig{}, nil),
	})

	body := `{
		"app":{"title":"New","notificationPrefix":"[New] "},
		"auth":{"enabled":false,"username":"admin","password":"","tokenSecret":"","sessionTTLHours":168},
		"scheduler":{"balanceCron":"37 */15 * * * *","rateCron":"13 */30 * * * *","concurrency":4,"retention":{"cron":"0 17 3 * * *","monitorLogsDays":30,"balanceSnapshotsDays":90,"notificationLogsDays":90,"announcementsDays":90}},
		"notifications":{"minChangePct":0,"balanceLowCooldownMinutes":60,"subscriptionDailyRemainingThresholdPct":0,"subscriptionWeeklyRemainingThresholdPct":0,"subscriptionMonthlyRemainingThresholdPct":0,"subscriptionExpiryThresholdHours":0,"subscriptionAlertCooldownMinutes":1440,"sendMaxAttempts":3},
		"proxy":{"enabled":true,"versionCheckEnabled":true,"protocol":"socks5","host":"127.0.0.1","port":1080,"username":"u","password":"p"},
		"upstream":{"timeoutSeconds":45,"userAgent":"custom-agent"},
		"gateway":{"tempPauseSeconds":30,"forwardTimeoutSeconds":600,"modelsCacheTTLSeconds":60,"maxFailoverSwitches":8,"routeBatchConcurrency":8,"usageErrorBodyBytes":32768,"usageErrorMsgRunes":500,"usageErrorHeaderValueRunes":8192,"usageErrorHeadersJSONBytes":65536}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got.App.Title != "New" {
		t.Fatalf("title = %q", got.App.Title)
	}
	if got.App.NotificationPrefix != "[New] " {
		t.Fatalf("notification prefix = %q", got.App.NotificationPrefix)
	}
	if !got.Proxy.Enabled || !got.Proxy.VersionCheckEnabled || got.Proxy.Protocol != "socks5" || got.Proxy.Host != "127.0.0.1" || got.Proxy.Port != 1080 || got.Proxy.Username != "u" || got.Proxy.Password != "p" {
		t.Fatalf("proxy = %#v", got.Proxy)
	}
	if got.Upstream.TimeoutSeconds != 45 || got.Upstream.UserAgent != "custom-agent" {
		t.Fatalf("upstream = %#v", got.Upstream)
	}
	if got.Gateway.RouteBatchConcurrency != 8 || got.Gateway.ForwardTimeoutSeconds != 600 {
		t.Fatalf("gateway = %#v", got.Gateway)
	}
}

func TestSaveAndGetSettingsEmbedAndBackoffFalseValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, &config.Config{}); err != nil {
		t.Fatalf("save init config: %v", err)
	}

	r := gin.New()
	apiGroup := r.Group("/api")
	registerSettings(apiGroup, &Deps{
		Runtime: runtimeconfig.New(path, "", nil, nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{}, config.GatewayConfig{}, nil),
	})

	body := `{
		"app":{"title":"Ops","notificationPrefix":"[Ops] "},
		"auth":{"enabled":false,"username":"admin","password":"","tokenSecret":"","sessionTTLHours":168},
		"scheduler":{"balanceCron":"","rateCron":"","concurrency":4,"retention":{"cron":"","monitorLogsDays":30,"balanceSnapshotsDays":90,"notificationLogsDays":90,"announcementsDays":90}},
		"notifications":{"minChangePct":0,"balanceLowCooldownMinutes":60,"subscriptionDailyRemainingThresholdPct":0,"subscriptionWeeklyRemainingThresholdPct":0,"subscriptionMonthlyRemainingThresholdPct":0,"subscriptionExpiryThresholdHours":0,"subscriptionAlertCooldownMinutes":1440,"sendMaxAttempts":3},
		"proxy":{"enabled":false,"versionCheckEnabled":false,"protocol":"http","host":"","port":0,"username":"","password":""},
		"upstream":{"timeoutSeconds":30,"userAgent":"upstream-ops/0.1"},
		"gateway":{"tempPauseSeconds":30,"forwardTimeoutSeconds":600,"modelsCacheTTLSeconds":60,"maxFailoverSwitches":8,"routeBatchConcurrency":8,"usageErrorBodyBytes":32768,"usageErrorMsgRunes":500,"usageErrorHeaderValueRunes":8192,"usageErrorHeadersJSONBytes":65536},
		"intelligence":{"lmfpdDataDir":"./data/lmfpd","tickIntervalSeconds":15,"concurrency":6,"backoffEnabled":false,"backoffTransientMaxSeconds":1800,"backoffRateLimitMaxSeconds":3600,"backoffAuthMaxSeconds":10800},
		"embed":{"cacheTTLSeconds":0,"rateLimitEnabled":false,"rateLimitRPS":12.5,"rateLimitBurst":60}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if got.Intelligence.TickIntervalSeconds != 15 || got.Intelligence.Concurrency != 6 {
		t.Fatalf("unexpected intelligence runner config: %+v", got.Intelligence)
	}
	if got.Intelligence.IsBackoffEnabled() {
		t.Fatalf("expected backoffEnabled=false preserved, got true")
	}
	if got.Intelligence.BackoffTransientMaxSeconds != 1800 || got.Intelligence.BackoffRateLimitMaxSeconds != 3600 || got.Intelligence.BackoffAuthMaxSeconds != 10800 {
		t.Fatalf("unexpected backoff caps: %+v", got.Intelligence)
	}
	if got.Embed.EffectiveCacheTTLSeconds() != 0 {
		t.Fatalf("expected cacheTTLSeconds=0 preserved, got %d", got.Embed.EffectiveCacheTTLSeconds())
	}
	if got.Embed.IsRateLimitEnabled() {
		t.Fatalf("expected rateLimitEnabled=false preserved, got true")
	}
	if got.Embed.RateLimitRPS != 12.5 || got.Embed.RateLimitBurst != 60 {
		t.Fatalf("unexpected embed rate limit: %+v", got.Embed)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/config", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	respBody := getRec.Body.String()
	for _, want := range []string{`"backoffEnabled":false`, `"cacheTTLSeconds":0`, `"rateLimitEnabled":false`, `"rateLimitBurst":60`} {
		if !strings.Contains(respBody, want) {
			t.Fatalf("expected GET response to contain %s, got %s", want, respBody)
		}
	}
}

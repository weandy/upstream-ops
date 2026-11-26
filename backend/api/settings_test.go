package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/runtimeconfig"
	"github.com/bejix/upstream-ops/backend/storage"
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

func TestGetSettingsRedactsSecretsAndPutEmptyPreservesExisting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	path := filepath.Join(t.TempDir(), "config.yaml")
	initCfg := &config.Config{
		App: config.AppConfig{Title: "Ops", NotificationPrefix: "[Ops] "},
		Auth: config.AuthConfig{
			Enabled:         true,
			Username:        "admin",
			Password:        "super-secret-admin-pass",
			TokenSecret:     "super-secret-token-key",
			SessionTTLHours: 168,
		},
		Proxy: config.ProxyConfig{
			Enabled:  true,
			Protocol: "http",
			Host:     "127.0.0.1",
			Port:     7890,
			Username: "proxyuser",
			Password: "super-secret-proxy-pass",
		},
	}
	if err := config.Save(path, initCfg); err != nil {
		t.Fatalf("save init config: %v", err)
	}

	r := gin.New()
	apiGroup := r.Group("/api")
	registerSettings(apiGroup, &Deps{
		Runtime: runtimeconfig.New(path, "", nil, nil, nil, nil, nil, nil, initCfg.Proxy, config.UpstreamConfig{}, config.GatewayConfig{}, nil),
	})

	// 1. GET 不得返回明文密码/签名密钥，必须返回脱敏状态标记。
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/settings/config", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	respBody := getRec.Body.String()
	for _, secret := range []string{"super-secret-admin-pass", "super-secret-token-key", "super-secret-proxy-pass"} {
		if strings.Contains(respBody, secret) {
			t.Fatalf("GET /api/settings/config leaked plaintext secret %q in response: %s", secret, respBody)
		}
	}
	for _, want := range []string{`"auth_password_set":true`, `"auth_token_secret_set":true`, `"proxy_password_set":true`} {
		if !strings.Contains(respBody, want) {
			t.Fatalf("expected GET response to include %s, got %s", want, respBody)
		}
	}

	// 2. PUT 提交空的密码/密钥字段时，应保留原值，不被误清空。
	putBody := `{
		"app":{"title":"Ops2","notificationPrefix":"[Ops2] "},
		"auth":{"enabled":true,"username":"admin","password":"","tokenSecret":"","sessionTTLHours":168},
		"scheduler":{"balanceCron":"","rateCron":"","concurrency":4,"retention":{"cron":"","monitorLogsDays":30,"balanceSnapshotsDays":90,"notificationLogsDays":90,"announcementsDays":90}},
		"notifications":{"minChangePct":0,"balanceLowCooldownMinutes":60,"sendMaxAttempts":3},
		"proxy":{"enabled":true,"protocol":"http","host":"127.0.0.1","port":7890,"username":"proxyuser","password":""},
		"upstream":{"timeoutSeconds":30,"userAgent":"upstream-ops/0.1"}
	}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	r.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putRec.Code, putRec.Body.String())
	}

	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load config after empty-secret PUT: %v", err)
	}
	if got.Auth.Password != "super-secret-admin-pass" || got.Auth.TokenSecret != "super-secret-token-key" || got.Proxy.Password != "super-secret-proxy-pass" {
		t.Fatalf("expected existing secrets preserved when PUT sends empty strings, got auth=%+v proxy=%+v", got.Auth, got.Proxy)
	}

	// 3. 传入 clear 标记时可显式清除可选密钥。
	clearBody := `{
		"app":{"title":"Ops2","notificationPrefix":"[Ops2] "},
		"auth":{"enabled":true,"username":"admin","password":"","tokenSecret":"","sessionTTLHours":168},
		"scheduler":{"balanceCron":"","rateCron":"","concurrency":4,"retention":{"cron":"","monitorLogsDays":30,"balanceSnapshotsDays":90,"notificationLogsDays":90,"announcementsDays":90}},
		"notifications":{"minChangePct":0,"balanceLowCooldownMinutes":60,"sendMaxAttempts":3},
		"proxy":{"enabled":true,"protocol":"http","host":"127.0.0.1","port":7890,"username":"proxyuser","password":""},
		"clear_auth_token_secret":true,
		"clear_proxy_password":true
	}`
	clearReq := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(clearBody))
	clearReq.Header.Set("Content-Type", "application/json")
	clearRec := httptest.NewRecorder()
	r.ServeHTTP(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear PUT status = %d, body = %s", clearRec.Code, clearRec.Body.String())
	}
	cleared, _ := config.LoadFile(path)
	if cleared.Auth.Password != "super-secret-admin-pass" || cleared.Auth.TokenSecret != "" || cleared.Proxy.Password != "" {
		t.Fatalf("expected explicit clear flags to clear only specified secrets, got auth=%+v proxy=%+v", cleared.Auth, cleared.Proxy)
	}
}

func TestSaveAndApplySettingsBlockedWhenRequireSecureAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	path := filepath.Join(t.TempDir(), "config.yaml")
	initCfg := &config.Config{
		App: config.AppConfig{Title: "Ops", NotificationPrefix: "[Ops] "},
		Auth: config.AuthConfig{
			Enabled:         true,
			Username:        "admin",
			Password:        "StrongInitialPass!2026",
			SessionTTLHours: 168,
		},
	}
	if err := config.Save(path, initCfg); err != nil {
		t.Fatalf("save init config: %v", err)
	}

	secret := "0123456789abcdef0123456789abcdef"
	rt := runtimeconfig.New(path, secret, nil, nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{}, config.GatewayConfig{}, nil)
	rt.SetRequireSecureAuth(true)

	r := gin.New()
	apiGroup := r.Group("/api")
	registerSettings(apiGroup, &Deps{Runtime: rt})

	// 1. PUT 尝试关闭鉴权必须被拒绝且返回 400。
	disableBody := `{
		"app":{"title":"Ops","notificationPrefix":"[Ops] "},
		"auth":{"enabled":false,"username":"admin","password":"","tokenSecret":"","sessionTTLHours":168},
		"scheduler":{"balanceCron":"","rateCron":"","concurrency":4},
		"notifications":{"minChangePct":0,"balanceLowCooldownMinutes":60,"sendMaxAttempts":3}
	}`
	req1 := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(disableBody))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when disabling auth under RequireSecureAuth, got %d body=%s", rec1.Code, rec1.Body.String())
	}

	// 2. PUT 尝试改回默认弱密码必须被拒绝且返回 400。
	weakPassBody := `{
		"app":{"title":"Ops","notificationPrefix":"[Ops] "},
		"auth":{"enabled":true,"username":"admin","password":"admin_ops","tokenSecret":"","sessionTTLHours":168},
		"scheduler":{"balanceCron":"","rateCron":"","concurrency":4},
		"notifications":{"minChangePct":0,"balanceLowCooldownMinutes":60,"sendMaxAttempts":3}
	}`
	req2 := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(weakPassBody))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when setting default password under RequireSecureAuth, got %d body=%s", rec2.Code, rec2.Body.String())
	}

	saved, err := config.LoadFile(path)
	if err != nil || !saved.Auth.Enabled || saved.Auth.Password != "StrongInitialPass!2026" {
		t.Fatalf("config file should remain unchanged after rejected PUTs: err=%v auth=%+v", err, saved.Auth)
	}

	// 3. 磁盘上若被改写为不安全配置，POST /api/settings/apply 返回 400。
	initCfg.Auth.Enabled = false
	if err := config.Save(path, initCfg); err != nil {
		t.Fatalf("save tampered config: %v", err)
	}
	recApply := httptest.NewRecorder()
	r.ServeHTTP(recApply, httptest.NewRequest(http.MethodPost, "/api/settings/apply", nil))
	if recApply.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on apply insecure config, got %d body=%s", recApply.Code, recApply.Body.String())
	}
}

func TestHealthzDoesNotLeakInternalDBError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "healthz.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}

	r := gin.New()
	Register(r, &Deps{DB: db})

	recOK := httptest.NewRecorder()
	r.ServeHTTP(recOK, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recOK.Code != http.StatusOK || !strings.Contains(recOK.Body.String(), `"status":"ok"`) {
		t.Fatalf("expected 200 ok, got %d body=%s", recOK.Code, recOK.Body.String())
	}

	_ = sqlDB.Close()
	recDown := httptest.NewRecorder()
	r.ServeHTTP(recDown, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recDown.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when db closed, got %d body=%s", recDown.Code, recDown.Body.String())
	}
	if strings.Contains(recDown.Body.String(), `"err"`) || strings.Contains(recDown.Body.String(), "database is closed") {
		t.Fatalf("/healthz leaked internal error details: %s", recDown.Body.String())
	}
}

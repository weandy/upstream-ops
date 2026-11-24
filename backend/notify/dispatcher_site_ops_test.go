package notify

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func openNotifyTestDB(t *testing.T) (*gorm.DB, *storage.Notifications, *crypto.Cipher) {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "notify-site-ops.db")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	cipher, err := crypto.NewCipher("test-notify-siteops-secret-key")
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return db, storage.NewNotifications(db), cipher
}

func TestDispatcherSendDirectAndListEnabledChannels(t *testing.T) {
	db, repo, cipher := openNotifyTestDB(t)

	var received struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	var webhookHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHits.Add(1)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		req, err := http.NewRequestWithContext(r.Context(), r.Method, r.RequestURI, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	cfgJSON, _ := json.Marshal(map[string]any{"url": target.URL})
	cfgCipher, err := cipher.Encrypt(string(cfgJSON))
	if err != nil {
		t.Fatalf("encrypt config: %v", err)
	}

	enabledCh := &storage.NotificationChannel{
		Name:                 "siteops-direct",
		Type:                 storage.NotifyWebhook,
		ConfigCipher:         cfgCipher,
		SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["1"],"events":["*"]}`,
		Enabled:              true,
	}
	if err := repo.CreateChannel(enabledCh); err != nil {
		t.Fatalf("create enabled channel: %v", err)
	}

	proxiedCh := &storage.NotificationChannel{
		Name:                 "siteops-proxied",
		Type:                 storage.NotifyWebhook,
		ConfigCipher:         cfgCipher,
		SiteOpsSubscriptions: `{"enabled":true,"instance_ids":["1"],"events":["*"]}`,
		Enabled:              true,
		ProxyEnabled:         true,
	}
	if err := repo.CreateChannel(proxiedCh); err != nil {
		t.Fatalf("create proxied channel: %v", err)
	}

	disabledCh := &storage.NotificationChannel{
		Name:         "siteops-disabled",
		Type:         storage.NotifyWebhook,
		ConfigCipher: cfgCipher,
		Enabled:      true,
	}
	if err := repo.CreateChannel(disabledCh); err != nil {
		t.Fatalf("create disabled channel: %v", err)
	}
	disabledCh.Enabled = false
	if err := repo.UpdateChannel(disabledCh); err != nil {
		t.Fatalf("disable channel: %v", err)
	}

	d := NewDispatcher(repo, cipher, nil, Policy{
		NotificationPrefix: "[SiteOps] ",
		SendMaxAttempts:    3,
	})
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	proxyHost, proxyPortStr, err := net.SplitHostPort(proxyURL.Host)
	if err != nil {
		t.Fatalf("split proxy host: %v", err)
	}
	proxyPort, _ := strconv.Atoi(proxyPortStr)
	d.UpdateProxyConfig(config.ProxyConfig{
		Enabled:  true,
		Protocol: proxyURL.Scheme,
		Host:     proxyHost,
		Port:     proxyPort,
	})

	// ListEnabledChannels returns only enabled channels.
	enabledList, err := d.ListEnabledChannels()
	if err != nil {
		t.Fatalf("ListEnabledChannels error: %v", err)
	}
	if len(enabledList) != 2 {
		t.Fatalf("ListEnabledChannels len = %d, want 2", len(enabledList))
	}

	// Nil repo safety check.
	if _, err := (&Dispatcher{}).ListEnabledChannels(); err == nil {
		t.Fatal("expected error when dispatcher repo is nil")
	}

	// SendDirect succeeds with prefix and without proxy when ProxyEnabled=false.
	if err := d.SendDirect(context.Background(), enabledCh.ID, "站点告警", "实例不可达"); err != nil {
		t.Fatalf("SendDirect failed: %v", err)
	}
	if received.Subject != "[SiteOps] 站点告警" || received.Body != "实例不可达" {
		t.Fatalf("unexpected webhook payload: %+v", received)
	}
	if webhookHits.Load() != 1 || proxyHits.Load() != 0 {
		t.Fatalf("hits mismatch: webhook=%d proxy=%d", webhookHits.Load(), proxyHits.Load())
	}

	// SendDirect routes through proxy when ProxyEnabled=true.
	if err := d.SendDirect(context.Background(), proxiedCh.ID, "代理告警", "经由代理发送"); err != nil {
		t.Fatalf("SendDirect proxied failed: %v", err)
	}
	if webhookHits.Load() != 2 || proxyHits.Load() != 1 {
		t.Fatalf("proxied hits mismatch: webhook=%d proxy=%d", webhookHits.Load(), proxyHits.Load())
	}
	if received.Subject != "[SiteOps] 代理告警" || received.Body != "经由代理发送" {
		t.Fatalf("unexpected proxied payload: %+v", received)
	}

	// Disabled or missing channels return explicit errors.
	if err := d.SendDirect(context.Background(), disabledCh.ID, "s", "b"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled channel error = %v, want disabled error", err)
	}
	if err := d.SendDirect(context.Background(), 99999, "s", "b"); err == nil {
		t.Fatal("expected error for nonexistent channel")
	}

	// SendDirect must never write notification_logs or notification_cooldowns.
	var logCount, cooldownCount int64
	if err := db.Model(&storage.NotificationLog{}).Count(&logCount).Error; err != nil {
		t.Fatalf("count notification_logs: %v", err)
	}
	if err := db.Model(&storage.NotificationCooldown{}).Count(&cooldownCount).Error; err != nil {
		t.Fatalf("count notification_cooldowns: %v", err)
	}
	if logCount != 0 || cooldownCount != 0 {
		t.Fatalf("SendDirect polluted legacy tables: logs=%d cooldowns=%d", logCount, cooldownCount)
	}
}

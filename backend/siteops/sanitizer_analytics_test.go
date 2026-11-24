package siteops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func eraseParser[T any](f func([]byte) (T, error)) func([]byte) (any, error) {
	return func(b []byte) (any, error) { return f(b) }
}
func noAnalyticsCanaries(t *testing.T, b []byte) {
	t.Helper()
	for _, secret := range []string{"alice.chen@portal.example.com", "AliceRealName", "sk-canary-secret-9999", "ProdBillingKey", "canaryhint", "deadbeef1234", "/var/lib/sub2api/backups/prod.sql.gz"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("隐私投影泄漏")
		}
	}
}
func noNonEmailCanaries(t *testing.T, b []byte) {
	t.Helper()
	for _, secret := range []string{"AliceRealName", "sk-canary-secret-9999", "ProdBillingKey", "canaryhint", "deadbeef1234", "/var/lib/sub2api/backups/prod.sql.gz"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("非邮箱隐私投影泄漏")
		}
	}
}
func TestAnalyticsProjectionAndPrivacyCanaries(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		parse     func([]byte) (any, error)
	}{
		{"ParseGroupsAll", `[{"id":1,"name":"alice.chen@portal.example.com"}]`, eraseParser(ParseGroupsAll)},
		{"ParseUsersPage", `{"items":[{"id":1,"email":"alice.chen@portal.example.com","username":"AliceRealName"}]}`, eraseParser(ParseUsersPage)},
		{"ParseUsersUsageBatch", `{"stats":{"1":{"user_id":1,"by_platform":[{"platform":"AliceRealName"}]}}}`, eraseParser(ParseUsersUsageBatch)},
		{"ParseUserConcurrency", `{"user":{"1":{"username":"AliceRealName","user_email":"alice.chen@portal.example.com"}}}`, eraseParser(ParseUserConcurrency)},
		{"ParseSubscriptionsPage", `[{"id":1,"user_id":2,"group_id":3,"user":{"email":"alice.chen@portal.example.com"},"group":{"name":"sk-canary-secret-9999"}}]`, eraseParser(ParseSubscriptionsPage)},
		{"ParseSubscriptionProgress", `{"id":1,"daily":{"source":"AliceRealName","used_usd":2}}`, eraseParser(ParseSubscriptionProgress)},
		{"ParseUsageStats", `{"total_requests":3,"username":"AliceRealName"}`, eraseParser(ParseUsageStats)},
		{"ParseUserAPIKeysPage", `{"items":[{"id":2,"name":"ProdBillingKey","key":"sk-canary-secret-9999","hint":"canaryhint"}]}`, eraseParser(ParseUserAPIKeysPage)},
		{"ParseAPIKeysUsageBatch", `{"stats":{"2":{"api_key_id":2,"name":"ProdBillingKey"}}}`, eraseParser(ParseAPIKeysUsageBatch)},
		{"ParseDashboardModels", `{"models":[{"model":"sk-canary-secret-9999","upstream_model":"alice.chen@portal.example.com"}]}`, eraseParser(ParseDashboardModels)},
		{"ParseDashboardGroups", `{"groups":[{"group_id":2,"group_name":"alice.chen@portal.example.com"}]}`, eraseParser(ParseDashboardGroups)},
		{"ParseRiskControlStatus", `{"enabled":true,"key_hash":"deadbeef1234","flagged_hashes":["deadbeef1234"]}`, eraseParser(ParseRiskControlStatus)},
		{"ParseBackupsList", `{"records":[{"file_name":"/var/lib/sub2api/backups/prod.sql.gz","triggered_by":"AliceRealName"}]}`, eraseParser(ParseBackupsList)},
		{"ParseChannelMonitorAvailability", `{"message":"AliceRealName"}`, eraseParser(ParseChannelMonitorAvailability)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newAnalyticsCache(time.Now)
			defer c.stop()
			for _, raw := range []string{tc.raw, `{"code":0,"data":` + tc.raw + "}"} {
				value, err := tc.parse([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				b, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				allowDisplayEmail := tc.name == "ParseUsersPage" || tc.name == "ParseSubscriptionsPage"
				if allowDisplayEmail {
					noNonEmailCanaries(t, b)
				} else {
					noAnalyticsCanaries(t, b)
				}
				key := newLeafKey(c.scope(1, 1, 1), tc.name, "GET", nil)
				_, _, _, err = loadProjection(context.Background(), c, key, leafOptions{}, func(context.Context) (any, UpstreamFetchMeta, error) { return value, UpstreamFetchMeta{}, nil })
				if err != nil {
					t.Fatal(err)
				}
				c.mu.Lock()
				entry := c.entries[key].Value.(leafEntry)
				c.mu.Unlock()
				if allowDisplayEmail {
					noNonEmailCanaries(t, entry.data)
				} else {
					noAnalyticsCanaries(t, entry.data)
				}
			}
			for _, bad := range []string{`{"code":1,"data":` + tc.raw + "}", "null"} {
				if _, err := tc.parse([]byte(bad)); err == nil {
					t.Fatal("accepted invalid envelope")
				}
			}
		})
	}
	for _, tc := range []struct{ local, want string }{{"a", "***"}, {"ab", "a***"}, {"abc", "a***"}, {"abcd", "a***"}, {"abcde", "ab***e"}, {"一二三四五", "一二***五"}, {"alice.chen", "al***n"}} {
		v := MaskEmailStrict(tc.local + "@portal.example.com")
		if v == nil || *v != tc.want+"@portal.example.com" {
			t.Fatal("email mask")
		}
	}
	for _, email := range []string{"a", "a@@portal.example.com", "a@-portal.example.com", "a@invalid", "a@portal..com", "s k@portal.example.com", "a\x7fb@portal.example.com", "sk-fixture@portal.example.com"} {
		if MaskEmailStrict(email) != nil {
			t.Fatal("accepted invalid email")
		}
	}
	if _, err := ParseUsersPage([]byte(`{"metadata":[]}`)); err == nil {
		t.Fatal("missing container accepted")
	}
	p, err := ParseUsersPage([]byte("[" + strings.Repeat(`{"id":1},`, 100) + `{"id":101}]`))
	if err != nil || !p.Truncated || len(p.Items) != 100 {
		t.Fatal("user directory limit")
	}
	result, err := ParseBackupsList([]byte(`[{"status":"completed","started_at":"2024-07-01T11:00:00Z"},{"status":"failed","started_at":"2024-07-02T11:00:00Z"}]`))
	if err != nil || result.Rows[0].Status != "failed" {
		t.Fatal("backup order")
	}
}

func TestAnalyticsPrivacyAcrossServiceOutputs(t *testing.T) {
	// 使用独立临时库，将相同哨兵同时送入真实 HTTP、白名单投影与序列化链路。
	ctx := context.Background()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case adminPath + "/users":
			io.WriteString(w, `{"items":[{"id":1,"email":"alice.chen@portal.example.com","username":"AliceRealName"}]}`)
		case adminPath + "/users/1/api-keys":
			io.WriteString(w, `[{"id":2,"name":"ProdBillingKey","hint":"canaryhint","key":"sk-canary-secret-9999"}]`)
		case adminPath + "/risk-control/status":
			io.WriteString(w, `{"enabled":false,"key_hash":"deadbeef1234"}`)
		case adminPath + "/backups":
			io.WriteString(w, `[{"status":"failed","started_at":"2024-01-02T12:00:00Z","file_name":"/var/lib/sub2api/backups/prod.sql.gz","triggered_by":"AliceRealName"}]`)
		case adminPath + "/dashboard/users-usage", adminPath + "/dashboard/api-keys-usage":
			io.WriteString(w, `{"stats":{}}`)
		case adminPath + "/ops/user-concurrency":
			io.WriteString(w, `{"user":{}}`)
		case adminPath + "/usage/stats":
			io.WriteString(w, `{"total_requests":2}`)
		default:
			io.WriteString(w, "[]")
		}
	}))
	defer up.Close()
	db, err := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "privacy.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err = storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewCipher("portal-fixture-material")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	svc, _ := NewDefaultService(db, cipher, config.SiteOpsConfig{AllowLoopback: true}, config.ProxyConfig{}, config.UpstreamConfig{}, true, slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer svc.Stop()
	inst, err := svc.CreateInstance(ctx, CreateInstanceInput{Name: "Portal", APIKey: "fixture", BaseURL: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.GetAnalyticsUserDetail(ctx, inst.ID, 1, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.User.Email == nil || *result.User.Email != "alice.chen@portal.example.com" {
		t.Fatal("display email missing")
	}
	if result.User.EmailMasked == nil || *result.User.EmailMasked != "al***n@portal.example.com" {
		t.Fatal("masked email missing")
	}
	lights, err := svc.GetAnalyticsLights(ctx, inst.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	report, err := svc.PreviewDailyReport(ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 用户接口仅在内存与 HTTP 响应直出完整邮箱，严禁泄漏其他敏感字段；风控灯与日报严格零邮箱。
	if b, err := json.Marshal(result); err != nil {
		t.Fatal(err)
	} else {
		noNonEmailCanaries(t, b)
	}
	for _, out := range []any{lights, report} {
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		noAnalyticsCanaries(t, b)
	}
	if log.Len() == 0 {
		t.Fatal("未捕获请求日志")
	}
	noAnalyticsCanaries(t, log.Bytes())
	svc.analytics.mu.Lock()
	for _, e := range svc.analytics.entries {
		noNonEmailCanaries(t, e.Value.(leafEntry).data)
	}
	svc.analytics.mu.Unlock()
	var tables []string
	if err = db.Raw("SELECT name FROM sqlite_master WHERE type='table'").Scan(&tables).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range tables {
		var rows []map[string]any
		if err := db.Table(name).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			for _, v := range row {
				switch x := v.(type) {
				case string:
					noAnalyticsCanaries(t, []byte(x))
				case []byte:
					noAnalyticsCanaries(t, x)
				}
			}
		}
	}
}

func TestAnalyticsDirectoryPageSizeAndCoverage(t *testing.T) {
	for _, tc := range []struct {
		page, n  int
		total    string
		more     *bool
		coverage string
	}{
		{1, 50, `,"total":120`, ptr(true), "page"}, {2, 50, `,"total":120`, ptr(true), "page"},
		{3, 20, `,"total":120`, ptr(false), "page"}, {1, 50, "", nil, "page"},
		{1, 49, "", ptr(false), "complete"},
	} {
		rows := strings.TrimSuffix(strings.Repeat(`{"id":1,"user_id":2,"group_id":3},`, tc.n), ",")
		raw := []byte(fmt.Sprintf(`{"page_size":50%s,"items":[%s]}`, tc.total, rows))
		p, err := parseSubscriptionsPage(raw, tc.page)
		if err != nil || p.PageSize != 50 || p.Page != tc.page || coverage(p) != tc.coverage || (p.HasMore == nil) != (tc.more == nil) || p.HasMore != nil && *p.HasMore != *tc.more {
			t.Fatal(tc, p, err)
		}
	}
}

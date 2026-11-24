package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

func setupNotificationsTestRouter(t *testing.T) (*gin.Engine, *storage.Notifications) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "notify-api.db")})
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
	cipher, err := crypto.NewCipher("test-notify-api-secret")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	notifies := storage.NewNotifications(db)
	r := gin.New()
	registerNotifications(r.Group("/api"), &Deps{
		DB:       db,
		Notifies: notifies,
		Cipher:   cipher,
	})
	return r, notifies
}

func TestNotificationChannelSiteOpsSubscriptionsAPI(t *testing.T) {
	r, notifies := setupNotificationsTestRouter(t)

	// 1. Create with valid site_ops_subscriptions (numeric + duplicate ids get normalized).
	createBody := `{
		"name": "ops-tg",
		"type": "webhook",
		"config": "{\"url\":\"https://hooks.example.com/ops\"}",
		"subscriptions": "[]",
		"site_ops_subscriptions": "{\"enabled\":true,\"instance_ids\":[1, \"01\", 2],\"events\":[\"site_offline\",\"daily_report\"]}",
		"enabled": true
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/channels", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data storage.NotificationChannel `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}
	wantSub := `{"enabled":true,"instance_ids":["1","2"],"events":["site_offline","daily_report"]}`
	if created.Data.SiteOpsSubscriptions != wantSub {
		t.Fatalf("created site_ops_subscriptions = %q, want %q", created.Data.SiteOpsSubscriptions, wantSub)
	}

	// 2. Update with another valid subscription.
	updateBody := `{
		"name": "ops-tg",
		"type": "webhook",
		"subscriptions": "[]",
		"site_ops_subscriptions": "{\"enabled\":true,\"instance_ids\":[\"*\"],\"events\":[\"*\"]}",
		"enabled": true
	}`
	reqUpd := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/notifications/channels/%d", created.Data.ID), bytes.NewBufferString(updateBody))
	reqUpd.Header.Set("Content-Type", "application/json")
	wUpd := httptest.NewRecorder()
	r.ServeHTTP(wUpd, reqUpd)
	if wUpd.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200, body=%s", wUpd.Code, wUpd.Body.String())
	}
	stored, err := notifies.FindChannel(created.Data.ID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if stored.SiteOpsSubscriptions != `{"enabled":true,"instance_ids":["*"],"events":["*"]}` {
		t.Fatalf("updated site_ops_subscriptions = %q", stored.SiteOpsSubscriptions)
	}

	// 3. Invalid JSON or invalid instance_id returns 400 and preserves previous value.
	badInputs := []string{
		`{"name":"bad-1","type":"webhook","config":"{\"url\":\"https://hooks.example.com\"}","site_ops_subscriptions":"{broken"}`,
		`{"name":"bad-2","type":"webhook","config":"{\"url\":\"https://hooks.example.com\"}","site_ops_subscriptions":"{\"enabled\":true,\"instance_ids\":[\"abc\"],\"events\":[\"*\"]}"}`,
		`{"name":"bad-3","type":"webhook","config":"{\"url\":\"https://hooks.example.com\"}","site_ops_subscriptions":"{\"enabled\":true,\"instance_ids\":[\"0\"],\"events\":[\"*\"]}"}`,
	}
	for i, payload := range badInputs {
		reqCreateBad := httptest.NewRequest(http.MethodPost, "/api/notifications/channels", bytes.NewBufferString(payload))
		reqCreateBad.Header.Set("Content-Type", "application/json")
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqCreateBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("case %d create bad status = %d, want 400, body=%s", i, wBad.Code, wBad.Body.String())
		}

		reqUpdateBad := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/notifications/channels/%d", created.Data.ID), bytes.NewBufferString(payload))
		reqUpdateBad.Header.Set("Content-Type", "application/json")
		wUpdBad := httptest.NewRecorder()
		r.ServeHTTP(wUpdBad, reqUpdateBad)
		if wUpdBad.Code != http.StatusBadRequest {
			t.Fatalf("case %d update bad status = %d, want 400, body=%s", i, wUpdBad.Code, wUpdBad.Body.String())
		}
	}

	// 4. Empty string or "null" clears site_ops_subscriptions to "".
	for _, clearVal := range []string{"", "null"} {
		clearPayload, _ := json.Marshal(map[string]any{
			"name":                   "ops-tg",
			"type":                   "webhook",
			"subscriptions":          "[]",
			"site_ops_subscriptions": clearVal,
			"enabled":                true,
		})
		reqClear := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/notifications/channels/%d", created.Data.ID), bytes.NewReader(clearPayload))
		reqClear.Header.Set("Content-Type", "application/json")
		wClear := httptest.NewRecorder()
		r.ServeHTTP(wClear, reqClear)
		if wClear.Code != http.StatusOK {
			t.Fatalf("clear(%q) status = %d, want 200, body=%s", clearVal, wClear.Code, wClear.Body.String())
		}
		afterClear, err := notifies.FindChannel(created.Data.ID)
		if err != nil {
			t.Fatalf("find channel after clear: %v", err)
		}
		if afterClear.SiteOpsSubscriptions != "" {
			t.Fatalf("clear(%q) left site_ops_subscriptions = %q, want empty", clearVal, afterClear.SiteOpsSubscriptions)
		}
	}
}

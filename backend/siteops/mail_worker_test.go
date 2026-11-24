package siteops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const testMailPassword = "synthetic-smtp-password-canary"
const testMailAddress = "synthetic-recipient-canary@example.com"

type testMailTransport struct {
	mu          sync.Mutex
	connections int
	recipients  []string
	data        atomic.Int32
	connectErr  error
	envelope    func(context.Context, string) error
	send        func(context.Context, string) error
}
type testMailSession struct {
	t  *testMailTransport
	to string
}

func (s *testMailTransport) Connect(context.Context, MailSMTPConnectionConfig) (MailSMTPSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections++
	return &testMailSession{t: s}, s.connectErr
}
func (s *testMailSession) Envelope(ctx context.Context, from, to string) error {
	s.to = to
	s.t.mu.Lock()
	s.t.recipients = append(s.t.recipients, to)
	s.t.mu.Unlock()
	if s.t.envelope != nil {
		return s.t.envelope(ctx, to)
	}
	return nil
}
func (s *testMailSession) SendData(ctx context.Context, _ []byte) error {
	s.t.data.Add(1)
	if s.t.send != nil {
		return s.t.send(ctx, s.to)
	}
	return nil
}
func (s *testMailSession) Reset(context.Context) error { return nil }
func (s *testMailSession) Close() error                { return nil }

type mailHarness struct {
	*MailSite
	db       *gorm.DB
	smtp     *testMailTransport
	log      *bytes.Buffer
	notifier *mockDirectNotifier
	template MailTemplateView
	scope    MailScopeInput
}

func newMailHarness(t *testing.T) *mailHarness {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "fixture")
		if r.URL.Path == adminPath+"/users" {
			fmt.Fprintf(w, `{"items":[{"id":1,"status":"active","role":"user","email":"%s"},{"id":2,"status":"active","role":"user","email":"two@example.com"},{"id":3,"status":"active","role":"user","email":"three@example.com"},{"id":4,"status":"active","role":"user","email":"four@example.com"}],"has_more":false,"total":4,"page":1,"page_size":100}`, testMailAddress)
			return
		}
		fmt.Fprint(w, "[]")
	}))
	t.Cleanup(upstream.Close)
	db, e := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "mail.sqlite")})
	if e != nil {
		t.Fatal(e)
	}
	sql, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { sql.Close() })
	if e = storage.AutoMigrate(db); e != nil {
		t.Fatal(e)
	}
	cipher, e := crypto.NewCipher("synthetic-test-key-material")
	if e != nil {
		t.Fatal(e)
	}
	log := &bytes.Buffer{}
	svc, _ := NewDefaultService(db, cipher, config.SiteOpsConfig{AllowLoopback: true, PerInstanceMaxConcurrency: 4}, config.ProxyConfig{}, config.UpstreamConfig{}, true, slog.New(slog.NewJSONHandler(log, nil)))
	tr := &testMailTransport{}
	svc.deps.MailSender = tr
	n := newMockDirectNotifier([]storage.NotificationChannel{{Enabled: true}})
	svc.SetNotifier(n)
	t.Cleanup(svc.Stop)
	inst, e := svc.CreateInstance(context.Background(), CreateInstanceInput{Name: "Portal", APIKey: "synthetic", BaseURL: upstream.URL})
	if e != nil {
		t.Fatal(e)
	}
	m, e := svc.MailSite(context.Background(), inst.ID)
	if e != nil {
		t.Fatal(e)
	}
	scope := MailScopeInput{inst.Generation, inst.CredGen}
	_, e = m.SaveSMTP(SaveMailSMTPInput{MailScopeInput: scope, Host: "smtp.example.com", Port: 465, TLSMode: MailTLSModeImplicit, AuthMode: MailAuthModePlain, Username: ptr("synthetic-account"), Password: ptr(testMailPassword), FromEmail: ptr("sender@example.com"), RatePerMinute: 300})
	if e != nil {
		t.Fatal(e)
	}
	tpl, e := m.SaveTemplate(0, SaveMailTemplateInput{MailScopeInput: scope, Name: "Fixture", Category: MailTemplateCategoryMarketing, Subject: "Message", HTMLBody: "<p>{{.UserID}}</p>"})
	if e != nil {
		t.Fatal(e)
	}
	return &mailHarness{m, db, tr, log, n, tpl, scope}
}
func (h *mailHarness) startWorker(t *testing.T) {
	t.Helper()
	w := newMailWorker(h.s)
	if e := w.RecoverStaleMailWork(h.ctx); e != nil {
		t.Fatal(e)
	}
	h.s.mu.Lock()
	h.s.mailWorker = w
	h.s.mu.Unlock()
	go w.run()
}
func (h *mailHarness) input(ids ...int64) CreateMailBatchInput {
	return CreateMailBatchInput{MailScopeInput: h.scope, RequestID: uuid.NewString(), SMTPRevision: 1, TemplateID: h.template.ID, TemplateRevision: 1, Audience: MailAudienceSpec{Mode: MailAudienceModeSelected, UserIDs: ids}}
}
func (h *mailHarness) wait(t *testing.T, id uint) MailBatchView {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, e := h.Batch(id)
		if e != nil {
			t.Fatal(e)
		}
		if v.FinishedAt != nil {
			return v
		}
		time.Sleep(15 * time.Millisecond)
	}
	b, _ := h.Batch(id)
	t.Fatalf("batch did not finish: %+v", b)
	return b
}

type transientMailCompletion struct {
	MailStore
	calls atomic.Int32
}

func (s *transientMailCompletion) CompleteMailItem(ctx context.Context, p storage.MailCompleteItemParams) error {
	if s.calls.Add(1) == 1 {
		return errors.New("transient")
	}
	return s.MailStore.CompleteMailItem(ctx, p)
}
func TestMailDeliveryFivePrivacySurfacesAndRetry(t *testing.T) {
	h := newMailHarness(t)
	h.smtp.envelope = func(_ context.Context, to string) error {
		if to == "two@example.com" {
			return &MailSMTPFailure{Stage: MailSMTPStageRCPTTo, SMTPCode: 550, Code: ErrCodeMailSMTPRejected}
		}
		return nil
	}
	h.smtp.send = func(_ context.Context, to string) error {
		if to == "three@example.com" {
			return fmt.Errorf("synthetic-peer-error-canary: %w", smtpFailure(MailSMTPStageDataCommit, errors.New("EOF")))
		}
		return nil
	}
	h.s.deps.MailStore = &transientMailCompletion{MailStore: h.store}
	b, _, e := h.CreateBatch(h.input(1, 2, 3))
	if e != nil || b.Status != MailBatchStatusPreparing {
		t.Fatal(b, e)
	}
	h.startWorker(t)
	b = h.wait(t, b.ID)
	if b.SentCount != 1 || b.FailedCount != 1 || b.UncertainCount != 1 || h.smtp.data.Load() != 2 {
		t.Fatal("incorrect evidence", b)
	}
	child, replay, e := h.RetryBatch(b.ID, RetryMailBatchInput{h.scope, uuid.NewString(), 1})
	if e != nil || replay {
		t.Fatal(e)
	}
	child = h.wait(t, child.ID)
	parent, err := h.Batch(b.ID)
	if err != nil || parent.CanRetryFailed {
		t.Fatal("source still allows a second retry", err)
	}
	if child.RecipientCount != 1 || child.FailedCount != 1 || h.smtp.data.Load() != 2 {
		t.Fatal("retry included another outcome", child)
	}
	items, e := h.Items(b.ID, MailListQuery{Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	added, e := h.AddMailSuppressions(AddMailSuppressionInput{MailScopeInput: h.scope, ItemID: items.Items[0].ID})
	if e != nil || added.Added != 1 {
		t.Fatal(added, e)
	}
	v, e := h.Overview(MailSecurityModeAuthenticated)
	if e != nil {
		t.Fatal(e)
	}
	candidates, e := h.Candidates(1)
	if e != nil {
		t.Fatal(e)
	}
	smtp, e := h.SMTP()
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal([]any{v, candidates, smtp, items, child})
	if e != nil {
		t.Fatal(e)
	}
	noLeaks := func(surface string, b []byte) {
		t.Helper()
		for _, secret := range []string{testMailPassword, testMailAddress, "synthetic-account", "synthetic-peer-error-canary"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatalf("canary on %s", surface)
			}
		}
	}
	noLeaks("API", encoded)
	noLeaks("slog", h.log.Bytes())
	var tables []string
	if e = h.db.Raw("SELECT name FROM sqlite_master WHERE type='table'").Scan(&tables).Error; e != nil {
		t.Fatal(e)
	}
	for _, table := range tables {
		var rows []map[string]any
		if e = h.db.Table(table).Find(&rows).Error; e != nil {
			t.Fatal(e)
		}
		for _, row := range rows {
			for _, value := range row {
				switch val := value.(type) {
				case []byte:
					noLeaks(table, val)
				case string:
					noLeaks(table, []byte(val))
				}
			}
		}
	}
	var live int64
	if e = h.db.Model(&storage.SiteOpsMailItem{}).Where("payload_cipher IS NOT NULL").Count(&live).Error; e != nil || live != 0 {
		t.Fatal("terminal payload retained", live, e)
	}
	c := h.s.deps.Client.(*Client)
	c.mu.Lock()
	cacheSize := len(c.cache)
	c.mu.Unlock()
	c.analytics.mu.Lock()
	entries := len(c.analytics.entries)
	c.analytics.mu.Unlock()
	if cacheSize != 0 || entries != 0 {
		t.Fatal("private directory was cached")
	}
	h.notifier.mu.Lock()
	defer h.notifier.mu.Unlock()
	if len(h.notifier.sent) != 0 {
		t.Fatal("mail triggered a notification")
	}
}
func TestMailWorkerCircuitBreaker(t *testing.T) {
	for _, tc := range []struct {
		stage MailSMTPStage
		want  int
	}{{MailSMTPStageAuth, 1}, {MailSMTPStageDial, 3}} {
		t.Run(string(tc.stage), func(t *testing.T) {
			h := newMailHarness(t)
			h.smtp.connectErr = smtpFailure(tc.stage, errors.New("fixture"))
			h.startWorker(t)
			b, _, e := h.CreateBatch(h.input(1, 2, 3, 4))
			if e != nil {
				t.Fatal(e)
			}
			b = h.wait(t, b.ID)
			h.smtp.mu.Lock()
			calls := h.smtp.connections
			h.smtp.mu.Unlock()
			if calls != tc.want || b.FailedCount != 4 || b.AbortReason != ErrCodeMailSMTPConfigBlocked {
				t.Fatal("circuit", calls, b)
			}
		})
	}
}
func TestMailWorkerCancelBeforeDataAndStopAfterPermit(t *testing.T) {
	for _, stage := range []string{"envelope", "data"} {
		t.Run(stage, func(t *testing.T) {
			h := newMailHarness(t)
			hit, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			block := func(ctx context.Context, to string) error {
				close(hit)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if stage == "envelope" {
				h.smtp.envelope = block
			} else {
				h.smtp.send = block
			}
			h.startWorker(t)
			b, _, e := h.CreateBatch(h.input(1))
			if e != nil {
				t.Fatal(e)
			}
			select {
			case <-hit:
			case <-time.After(5 * time.Second):
				t.Fatal("delivery did not start")
			}
			if stage == "envelope" {
				_, e = h.CancelBatch(b.ID, h.scope)
				if e != nil {
					t.Fatal(e)
				}
				unblock()
				b = h.wait(t, b.ID)
				if b.CancelledCount != 1 || h.smtp.data.Load() != 0 {
					t.Fatal(b)
				}
				return
			}
			done := make(chan struct{})
			go func() { h.s.Stop(); close(done) }()
			read := make(chan struct{})
			go func() { h.s.CurrentConfig(); close(read) }()
			select {
			case <-read:
			case <-time.After(time.Second):
				t.Fatal("Service mutex held during shutdown")
			}
			unblock()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown deadlocked")
			}
			if b = h.wait(t, b.ID); b.SentCount != 1 {
				t.Fatal("lost accepted DATA", b)
			}
		})
	}
}

func TestMailWorkerStartupReconcilesStaleData(t *testing.T) {
	h := newMailHarness(t)
	b, _, e := h.CreateBatch(h.input(1))
	if e != nil {
		t.Fatal(e)
	}
	// Model the state left by a previous process that died after obtaining DATA permission.
	old := time.Now().UTC().Add(-MailStaleClaimAge - time.Second)
	if e = h.db.Model(&storage.SiteOpsMailBatch{}).Where("id = ?", b.ID).Updates(map[string]any{"status": "running", "recipient_count": 1, "owner": "old-worker", "lease_until": old}).Error; e != nil {
		t.Fatal(e)
	}
	row := storage.SiteOpsMailItem{InstanceID: h.inst.ID, BatchID: b.ID, UserID: 1, EmailHash: "synthetic-hash", PayloadCipher: ptr("opaque"), Status: "sending", Phase: "data", ClaimToken: "old-claim", ClaimedAt: &old}
	if e = h.db.Create(&row).Error; e != nil {
		t.Fatal(e)
	}
	h.startWorker(t)
	b = h.wait(t, b.ID)
	if b.UncertainCount != 1 || b.CanRetryFailed || h.smtp.connections != 0 {
		t.Fatal("stale recovery sent mail", b)
	}
	if e = h.db.First(&row, row.ID).Error; e != nil || row.PayloadCipher != nil || row.Status != "uncertain" {
		t.Fatal("stale payload retained", e)
	}
}

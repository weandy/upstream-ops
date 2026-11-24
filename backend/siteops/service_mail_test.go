package siteops

import (
	"errors"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/google/uuid"
)

func TestMailBatchCanonicalReplayAndConflict(t *testing.T) {
	h := newMailHarness(t)
	in := h.input(4, 1, 1)
	b, replay, e := h.CreateBatch(in)
	if e != nil || replay {
		t.Fatal(b, e)
	}
	in.Audience.UserIDs = []int64{1, 4}
	again, replay, e := h.CreateBatch(in)
	if e != nil || !replay || again.ID != b.ID {
		t.Fatal("canonical replay", again, e)
	}
	in.Audience.UserIDs = []int64{4}
	if _, _, e = h.CreateBatch(in); !errors.Is(e, ErrMailIdempotencyConflict) {
		t.Fatal(e)
	}
	in.RequestID = uuid.NewString()
	_, _, e = h.CreateBatch(in)
	var detail *MailBatchError
	if !errors.As(e, &detail) || detail.ActiveBatchID != b.ID {
		t.Fatal("active slot", e)
	}
	in.Generation++
	if _, _, e = h.CreateBatch(in); !errors.Is(e, ErrMailGenerationMismatch) {
		t.Fatal(e)
	}
}
func TestMailSMTPTestDurableReplayAndRate(t *testing.T) {
	h := newMailHarness(t)
	h.startWorker(t)
	in := MailTestInput{MailScopeInput: h.scope, ExpectedRevision: 1, RequestID: uuid.NewString()}
	out, done, e := h.TestSMTP(in)
	if e != nil || !done || out.Status != MailBatchStatusCompleted || out.SMTPCode != 250 || out.FromMasked == "sender@example.com" {
		t.Fatal(out, e)
	}
	h.s.Stop()
	again, done, e := h.TestSMTP(in)
	if e != nil || !done || again != out || h.smtp.data.Load() != 1 {
		t.Fatal("persistent replay", again, e)
	}
	h.smtp.mu.Lock()
	defer h.smtp.mu.Unlock()
	if len(h.smtp.recipients) != 1 || h.smtp.recipients[0] != "sender@example.com" {
		t.Fatal("test recipient")
	}
	in.RequestID = uuid.NewString()
	_, _, e = h.TestSMTP(in)
	var detail *MailBatchError
	if !errors.Is(e, ErrMailTestRateLimited) || !errors.As(e, &detail) || detail.RetryAfterSeconds < 1 || detail.RetryAfterSeconds > 60 {
		t.Fatal("test throttle", e)
	}
	page, e := h.Batches(MailListQuery{Limit: 1})
	if e != nil || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
	items, e := h.Items(out.BatchID, MailListQuery{Limit: 20})
	if e != nil || len(items.Items) != 0 {
		t.Fatal(items, e)
	}
}
func TestMailCopyCASAndResealing(t *testing.T) {
	h := newMailHarness(t)
	target, e := h.s.CreateInstance(h.ctx, CreateInstanceInput{Name: "Other", APIKey: "fixture", BaseURL: h.inst.BaseURL + "/other"})
	if e != nil {
		t.Fatal(e)
	}
	m, e := h.s.MailSite(h.ctx, target.ID)
	if e != nil {
		t.Fatal(e)
	}
	in := MailCopyInput{MailScopeInput: MailScopeInput{target.Generation, target.CredGen}, SourceInstanceID: h.inst.ID, SourceRevision: 1, SourceGeneration: h.scope.Generation, SourceCredGen: h.scope.CredGen}
	out, e := m.CopySMTP(in)
	if e != nil || out.Revision != 1 || !out.HasPassword {
		t.Fatal(out, e)
	}
	a, e := h.store.GetMailSMTP(h.ctx, h.key)
	if e != nil {
		t.Fatal(e)
	}
	b, e := h.store.GetMailSMTP(h.ctx, m.key)
	if e != nil {
		t.Fatal(e)
	}
	if a.IdentityCipher == b.IdentityCipher || a.PasswordCipher == b.PasswordCipher {
		t.Fatal("cipher copied across scope")
	}
	ident, password, e := m.smtpSecrets(b)
	if e != nil || password != testMailPassword || ident.FromEmail != "sender@example.com" {
		t.Fatal("secret binding")
	}
	if _, e = m.CopySMTP(in); !errors.Is(e, ErrMailRevisionMismatch) {
		t.Fatal(e)
	}
	in.SourceTemplateID = h.template.ID
	tpl, e := m.CopyTemplate(in)
	if e != nil || tpl.ID == h.template.ID || tpl.HTMLBody != h.template.HTMLBody {
		t.Fatal(tpl, e)
	}
	in.SourceCredGen++
	if _, e = m.CopyTemplate(in); !errors.Is(e, ErrMailGenerationMismatch) {
		t.Fatal(e)
	}
	change := SaveMailSMTPInput{MailScopeInput: h.scope, ExpectedRevision: 1, Host: "another.example.com", Port: 465, TLSMode: MailTLSModeImplicit, AuthMode: MailAuthModePlain}
	if _, e = h.SaveSMTP(change); !errors.Is(e, ErrMailInvalidInput) {
		t.Fatal("changed endpoint retained password", e)
	}
	var rows int64
	if e = h.db.Model(&storage.SiteOpsMailSMTP{}).Count(&rows).Error; e != nil || rows != 2 {
		t.Fatal(e)
	}
}

// A live test batch with no running worker stays durable after the HTTP wait expires.
func TestMailSMTPTestWaitReturnsPending(t *testing.T) {
	h := newMailHarness(t)
	in := MailTestInput{MailScopeInput: h.scope, ExpectedRevision: 1, RequestID: uuid.NewString()}
	start := time.Now()
	result, done, err := h.TestSMTP(in)
	if err != nil || done || result.BatchID == 0 || result.Status != MailBatchStatusPreparing {
		t.Fatal(result, done, err)
	}
	if elapsed := time.Since(start); elapsed < MailTestWait || elapsed > MailTestWait+3*time.Second {
		t.Fatal("unexpected synchronous wait", elapsed)
	}
	h.startWorker(t)
	if b := h.wait(t, result.BatchID); b.SentCount != 1 {
		t.Fatal(b)
	}
}

func TestMailCopySMTPClearsCrossSiteUnsubscribeAndGuardBlocksSubLocalhost(t *testing.T) {
	guard := NewOutboundGuard(false, nil, nil)
	if err := guard.ValidateSMTPHostPort("sub.localhost", 465); !errors.Is(err, ErrMailSMTPConfigBlocked) {
		t.Fatalf("expected sub.localhost blocked when allowLoopback=false, got %v", err)
	}
	h := newMailHarness(t)
	// Source has HTTPS unsubscribe URL on source host; target has different host.
	_, err := h.SaveSMTP(SaveMailSMTPInput{
		MailScopeInput: h.scope, ExpectedRevision: 1, Host: "smtp.example.com", Port: 465,
		TLSMode: MailTLSModeImplicit, AuthMode: MailAuthModePlain,
		UnsubscribeURL: "mailto:unsub@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := h.s.CreateInstance(h.ctx, CreateInstanceInput{Name: "Target", APIKey: "fixture", BaseURL: "https://target.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	tm, err := h.s.MailSite(h.ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := tm.CopySMTP(MailCopyInput{
		MailScopeInput: MailScopeInput{target.Generation, target.CredGen},
		SourceInstanceID: h.inst.ID, SourceGeneration: h.scope.Generation, SourceCredGen: h.scope.CredGen, SourceRevision: 2,
	})
	if err != nil || copied.UnsubscribeURL != "mailto:unsub@example.com" {
		t.Fatalf("expected mailto preserved on copy, got %+v err=%v", copied, err)
	}
	httpsSrc, err := h.s.CreateInstance(h.ctx, CreateInstanceInput{Name: "HTTPSSource", APIKey: "fixture", BaseURL: "https://source.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	sm, err := h.s.MailSite(h.ctx, httpsSrc.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sm.SaveSMTP(SaveMailSMTPInput{
		MailScopeInput: MailScopeInput{httpsSrc.Generation, httpsSrc.CredGen},
		Host: "smtp.example.com", Port: 465, TLSMode: MailTLSModeImplicit, AuthMode: MailAuthModePlain,
		Username: ptr("u"), Password: ptr("p"), FromEmail: ptr("from@source.example.com"),
		UnsubscribeURL: "https://source.example.com/unsub",
	})
	if err != nil {
		t.Fatal(err)
	}
	copied2, err := tm.CopySMTP(MailCopyInput{
		MailScopeInput: MailScopeInput{target.Generation, target.CredGen},
		ExpectedRevision: copied.Revision,
		SourceInstanceID: httpsSrc.ID, SourceGeneration: httpsSrc.Generation, SourceCredGen: httpsSrc.CredGen, SourceRevision: 1,
	})
	if err != nil || copied2.UnsubscribeURL != "" {
		t.Fatalf("expected cross-host https unsubscribe cleared on copy, got %+v err=%v", copied2, err)
	}
}

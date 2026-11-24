package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"
)

type mailFixture struct {
	*SiteOpsStore
	ctx  context.Context
	inst SiteOpsInstance
	p    MailBatchCreateParams
}

func mailKey(inst SiteOpsInstance) MailScopeKey {
	hash := sha256.Sum256([]byte(inst.BaseURL))
	return MailScopeKey{InstanceID: inst.ID, TargetBinding: hex.EncodeToString(hash[:]), Generation: inst.Generation, CredGen: inst.CredGen}
}

func newMailFixture(t *testing.T) *mailFixture {
	t.Helper()
	s := newTestSiteOpsStore(t)
	ctx := context.Background()
	inst := createTestSiteOpsInstance(t, s, "mail", true, 0)
	k := mailKey(inst)
	if _, err := s.EnsureMailScope(ctx, k, "sealed-seed"); err != nil {
		t.Fatal(err)
	}
	smtp, err := s.SaveMailSMTP(ctx, k, 0, SiteOpsMailSMTP{Host: "smtp.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := s.SaveMailTemplate(ctx, k, 0, SiteOpsMailTemplate{Name: "template"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return &mailFixture{s, ctx, inst, MailBatchCreateParams{Scope: k, RequestID: "first", RequestDigest: "digest", Mode: "selected", SMTPRevision: smtp.Revision, TemplateID: tpl.ID, TemplateRevision: tpl.Revision, AudienceJSON: "[1,2]", Now: now, PrepareBy: now.Add(30 * time.Minute)}}
}

func (f *mailFixture) running(t *testing.T, n int) MailWorkKey {
	t.Helper()
	b, err := f.CreateMailBatch(f.ctx, f.p)
	if err != nil {
		t.Fatal(err)
	}
	w := MailWorkKey{Scope: f.p.Scope, BatchID: b.Batch.ID, Owner: "worker", Now: f.p.Now}
	lease := func(status string) {
		b, err := f.LeaseMailBatch(f.ctx, w.Owner, w.Now, w.Now.Add(time.Minute))
		if err != nil || b == nil || b.ID != w.BatchID || b.Status != status || b.Owner != w.Owner {
			t.Fatalf("lease %s: %+v %v", status, b, err)
		}
	}
	lease("preparing")
	rows := make([]MailItemStageRow, n)
	for i := range rows {
		rows[i] = MailItemStageRow{UserID: int64(i + 1), EmailHash: fmt.Sprint(i + 1), PayloadCipher: "sealed"}
	}
	if err = f.StageMailItems(f.ctx, w, rows); err != nil {
		t.Fatal(err)
	}
	if err = f.FinishMailPreparation(f.ctx, w, MailPrepareCounts{Scanned: n}); err != nil {
		t.Fatal(err)
	}
	lease("running")
	return w
}

func (f *mailFixture) claim(t *testing.T, w MailWorkKey, token string) MailAuthorizeDataParams {
	t.Helper()
	p := MailClaimParams{MailWorkKey: w, SMTPRevision: 1, ClaimToken: token}
	item, err := f.ClaimMailItem(f.ctx, p)
	if err != nil || item == nil || item.Phase != "envelope" || item.ClaimToken != token || item.PayloadCipher == nil {
		t.Fatalf("claim: %+v %v", item, err)
	}
	return MailAuthorizeDataParams{MailClaimParams: p, ItemID: item.ID}
}

func (f *mailFixture) checkItems(t *testing.T, bid uint, statuses ...string) {
	t.Helper()
	var items []SiteOpsMailItem
	if err := f.db.Where("batch_id = ?", bid).Order("id ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != len(statuses) {
		t.Fatalf("items %d, want %d", len(items), len(statuses))
	}
	for i, item := range items {
		terminal := item.Status != "sending" && item.Status != "pending"
		if item.Status != statuses[i] || (item.PayloadCipher == nil) != terminal || (item.Phase == "done") != terminal {
			t.Fatalf("item %d: status %s phase %s has cipher %t", i, item.Status, item.Phase, item.PayloadCipher != nil)
		}
	}
}

func TestMailConcurrentSlotsAndReplay(t *testing.T) {
	f := newMailFixture(t)
	var journal string
	if err := f.db.Raw("PRAGMA journal_mode").Scan(&journal).Error; err != nil || journal != "wal" {
		t.Fatalf("journal %q: %v", journal, err)
	}
	type result struct {
		p   MailBatchCreateParams
		r   MailBatchCreateResult
		err error
	}
	ch := make(chan result, 12)
	start := make(chan struct{})
	for i := 0; i < cap(ch); i++ {
		p := f.p
		p.RequestID = fmt.Sprint(i % 3)
		if i%3 == 0 {
			p.Mode = "test"
		}
		go func() { <-start; r, err := f.CreateMailBatch(f.ctx, p); ch <- result{p, r, err} }()
	}
	close(start)
	var winner MailBatchCreateParams
	var id uint
	var conflicts []uint
	created, replayed := 0, 0
	for i := 0; i < cap(ch); i++ {
		v := <-ch
		switch {
		case errors.Is(v.err, ErrMailActiveBatchExists):
			conflicts = append(conflicts, v.r.ActiveBatchID)
		case v.err != nil:
			t.Fatal(v.err)
		case v.r.IdempotentReplay:
			replayed++
		default:
			created++
			winner, id = v.p, v.r.Batch.ID
		}
	}
	if created != 1 || replayed != 3 || len(conflicts) != 8 {
		t.Fatalf("created %d replayed %d blocked %d", created, replayed, len(conflicts))
	}
	for _, blocked := range conflicts {
		if blocked != id {
			t.Fatalf("active id %d, want %d", blocked, id)
		}
	}
	winner.RequestDigest = "different"
	if _, err := f.CreateMailBatch(f.ctx, winner); !errors.Is(err, ErrMailIdempotencyConflict) {
		t.Fatal(err)
	}
}

func TestMailDataPermissionAndCancellation(t *testing.T) {
	f := newMailFixture(t)
	w := f.running(t, 4)
	data := f.claim(t, w, "data")
	envelope := f.claim(t, w, "envelope")
	suppressed := f.claim(t, w, "suppression")
	wrong := data
	wrong.ClaimToken = "other"
	if ok, err := f.AuthorizeMailData(f.ctx, wrong); ok || !errors.Is(err, ErrMailClaimLost) {
		t.Fatalf("wrong token: %t %v", ok, err)
	}
	if ok, err := f.AuthorizeMailData(f.ctx, data); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := f.AddMailSuppressionFromItem(f.ctx, f.p.Scope, suppressed.ItemID, "admin_added"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := f.AuthorizeMailData(f.ctx, suppressed); ok || err != nil {
		t.Fatal(ok, err)
	}
	r, err := f.CancelMailBatch(f.ctx, f.p.Scope, w.BatchID, w.Now)
	if err != nil || r.InFlightData != 1 || r.Batch.ActiveSlot == nil || r.Batch.Status != "cancelling" {
		t.Fatal(r, err)
	}
	if ok, err := f.AuthorizeMailData(f.ctx, envelope); ok || err != nil {
		t.Fatal(ok, err)
	}
	f.checkItems(t, w.BatchID, "sending", "cancelled", "suppressed", "cancelled")
	complete := MailCompleteItemParams{InstanceID: f.inst.ID, BatchID: w.BatchID, ItemID: data.ItemID, ClaimToken: data.ClaimToken, Status: "sent", Now: w.Now}
	if err = f.CompleteMailItem(f.ctx, complete); err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteMailItem(f.ctx, complete); !errors.Is(err, ErrMailClaimLost) {
		t.Fatal(err)
	}
	f.checkItems(t, w.BatchID, "sent", "cancelled", "suppressed", "cancelled")
	b, err := f.GetMailBatch(f.ctx, f.p.Scope, w.BatchID)
	if err != nil || b.Status != "cancelled" || b.ActiveSlot != nil || b.SentCount != 1 || b.SuppressedCount != 1 || b.CancelledCount != 2 || b.AudienceJSON != "" {
		t.Fatal(b, err)
	}
}

func TestMailAddressChangeRecoveryAndCascade(t *testing.T) {
	f := newMailFixture(t)
	w := f.running(t, 2)
	data := f.claim(t, w, "data")
	if ok, err := f.AuthorizeMailData(f.ctx, data); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err := f.AddMailSuppressions(f.ctx, w.Scope, []MailSuppressionEntry{{EmailHash: "old"}}); err != nil {
		t.Fatal(err)
	}
	oldURL := f.inst.BaseURL
	f.inst.BaseURL = "https://other.example.com"
	f.inst.Generation++
	if err := f.UpdateInstance(f.ctx, &f.inst); err != nil {
		t.Fatal(err)
	}
	f.checkItems(t, w.BatchID, "sending", "cancelled")
	k := mailKey(f.inst)
	if _, err := f.GetMailSMTP(f.ctx, k); !errors.Is(err, ErrMailNotFound) {
		t.Fatal(err)
	}
	if _, err := f.GetMailTemplate(f.ctx, k, f.p.TemplateID); !errors.Is(err, ErrMailNotFound) {
		t.Fatal(err)
	}
	if found, err := f.FindSuppressedMailHashes(f.ctx, k, []string{"old"}); err != nil || len(found) != 0 {
		t.Fatal(found, err)
	}
	if _, err := f.SaveMailSMTP(f.ctx, k, 0, SiteOpsMailSMTP{}); err != nil {
		t.Fatal(err)
	}
	test := f.p
	test.Mode, test.Scope, test.RequestID = "test", k, "another"
	if out, err := f.CreateMailBatch(f.ctx, test); !errors.Is(err, ErrMailActiveBatchExists) || out.ActiveBatchID != w.BatchID {
		t.Fatal(out, err)
	}
	for _, tc := range []struct {
		age  time.Duration
		want int64
	}{{10*time.Minute - time.Millisecond, 0}, {10 * time.Minute, 1}} {
		now := w.Now.Add(tc.age)
		r, err := f.RecoverStaleMailItems(f.ctx, now, now)
		if err != nil || r.UncertainItems != tc.want || r.FinishedBatches != tc.want {
			t.Fatal(r, err)
		}
	}
	f.checkItems(t, w.BatchID, "uncertain", "cancelled")
	if _, err := f.CreateMailBatch(f.ctx, test); err != nil {
		t.Fatal(err)
	}
	f.inst.BaseURL = oldURL
	f.inst.Generation++
	if err := f.UpdateInstance(f.ctx, &f.inst); err != nil {
		t.Fatal(err)
	}
	k = mailKey(f.inst)
	if found, err := f.FindSuppressedMailHashes(f.ctx, k, []string{"old"}); err != nil || !found["old"] {
		t.Fatal(found, err)
	}
	if row, err := f.EnsureMailScope(f.ctx, k, "different"); err != nil || row.SeedCipher != "sealed-seed" {
		t.Fatal(row, err)
	}
	if _, err := f.SaveMailSMTP(f.ctx, k, 0, SiteOpsMailSMTP{}); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteInstance(f.ctx, k.InstanceID); err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&SiteOpsMailScope{}, &SiteOpsMailSMTP{}, &SiteOpsMailTemplate{}, &SiteOpsMailSuppression{}, &SiteOpsMailBatch{}, &SiteOpsMailItem{}} {
		var n int64
		if err := f.db.Model(model).Where("instance_id = ?", k.InstanceID).Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("remaining %T: %d, %v", model, n, err)
		}
	}
}

func TestMailChunkedRetentionHidesPartialBatch(t *testing.T) {
	f := newMailFixture(t)
	w := f.running(t, 203)
	if err := f.AbortMailBatch(f.ctx, w, "MAIL_SMTP_CONFIG_BLOCKED"); err != nil {
		t.Fatal(err)
	}
	cutoff := w.Now.Add(time.Second)
	if n, err := f.DeleteFinishedMailBatchesBeforeChunked(f.ctx, w.Now, 200, 10000, time.Second); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := f.DeleteFinishedMailBatchesBeforeChunked(f.ctx, cutoff, 200, 200, time.Second); err != nil || n != 200 {
		t.Fatal(n, err)
	}
	if rows, err := f.ListMailBatches(f.ctx, MailListParams{Scope: w.Scope}); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	retry := f.p
	retry.RequestID, retry.Mode, retry.ParentBatchID = "retry", "retry_failed", &w.BatchID
	if _, err := f.CreateMailBatch(f.ctx, retry); !errors.Is(err, ErrMailNotFound) {
		t.Fatal(err)
	}
	if n, err := f.DeleteFinishedMailBatchesBeforeChunked(f.ctx, cutoff, 200, 10000, time.Second); err != nil || n != 4 {
		t.Fatal(n, err)
	}
}

func TestMailSMTPRevisionChangesBeforeData(t *testing.T) {
	f := newMailFixture(t)
	w := f.running(t, 2)
	p := f.claim(t, w, "claimed")
	original, err := f.GetMailSMTP(f.ctx, w.Scope)
	if err != nil {
		t.Fatal(err)
	}
	original.HasPassword = true
	original.PasswordCipher = "sealed"
	updated, err := f.SaveMailSMTP(f.ctx, w.Scope, original.Revision, original)
	if err != nil || updated.Revision != 2 {
		t.Fatal(updated.Revision, err)
	}
	if _, err = f.SaveMailSMTP(f.ctx, w.Scope, 1, original); !errors.Is(err, ErrMailRevisionMismatch) {
		t.Fatal(err)
	}
	if ok, err := f.AuthorizeMailData(f.ctx, p); ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err = f.ClaimMailItem(f.ctx, p.MailClaimParams); !errors.Is(err, ErrMailRevisionMismatch) {
		t.Fatal(err)
	}
	f.checkItems(t, w.BatchID, "cancelled", "pending")
	updated.HasPassword = false
	updated.PasswordCipher = ""
	if _, err = f.SaveMailSMTP(f.ctx, w.Scope, 2, updated); err != nil {
		t.Fatal(err)
	}
	smtp, err := f.GetMailSMTP(f.ctx, w.Scope)
	if err != nil || smtp.Revision != 3 || smtp.HasPassword || smtp.PasswordCipher != "" {
		t.Fatal(smtp.Revision, err)
	}
}

func TestMailPreparationLeaseAndDeadline(t *testing.T) {
	f := newMailFixture(t)
	b, err := f.CreateMailBatch(f.ctx, f.p)
	if err != nil {
		t.Fatal(err)
	}
	w := MailWorkKey{Scope: f.p.Scope, BatchID: b.Batch.ID, Owner: "first", Now: f.p.Now}
	if _, err = f.LeaseMailBatch(f.ctx, w.Owner, w.Now, w.Now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = f.StageMailItems(f.ctx, w, []MailItemStageRow{{EmailHash: "one", PayloadCipher: "sealed"}}); err != nil {
		t.Fatal(err)
	}
	w.Now = w.Now.Add(time.Minute)
	winner, err := f.LeaseMailBatch(f.ctx, "replacement", w.Now, w.Now.Add(time.Minute))
	if err != nil || winner == nil || !winner.PrepareBy.Equal(f.p.PrepareBy) {
		t.Fatal(winner, err)
	}
	f.checkItems(t, w.BatchID)
	if err = f.RenewMailLease(f.ctx, w, w.Now.Add(time.Hour)); !errors.Is(err, ErrMailLeaseLost) {
		t.Fatal(err)
	}
	w.Owner = winner.Owner
	if err = f.StageMailItems(f.ctx, w, []MailItemStageRow{{EmailHash: "one", PayloadCipher: "resealed"}}); err != nil {
		t.Fatal(err)
	}
	r, err := f.RecoverStaleMailItems(f.ctx, w.Now.Add(-10*time.Minute), w.Now.Add(time.Minute))
	if err != nil || r.FinishedBatches != 1 {
		t.Fatal(r, err)
	}
	f.checkItems(t, w.BatchID, "cancelled")
	row, err := f.GetMailBatch(f.ctx, w.Scope, w.BatchID)
	if err != nil || row.Status != "failed" || row.ActiveSlot != nil || row.FailedCount != 0 || row.AudienceJSON != "" {
		t.Fatal(row, err)
	}
}

func TestMailTestBatchRateLimit(t *testing.T) {
	f := newMailFixture(t)
	p := f.p
	p.Mode = "test"
	r, err := f.CreateMailBatch(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.CancelMailBatch(f.ctx, p.Scope, r.Batch.ID, p.Now); err != nil {
		t.Fatal(err)
	}
	if replay, err := f.CreateMailBatch(f.ctx, p); err != nil || !replay.IdempotentReplay {
		t.Fatal(replay, err)
	}
	p.RequestID = "second"
	p.Now = p.Now.Add(59 * time.Second)
	if limited, err := f.CreateMailBatch(f.ctx, p); !errors.Is(err, ErrMailTestRateLimited) || limited.RetryAfterSeconds != 1 {
		t.Fatal(limited, err)
	}
	p.Now = p.Now.Add(time.Second)
	if _, err = f.CreateMailBatch(f.ctx, p); err != nil {
		t.Fatal(err)
	}
}

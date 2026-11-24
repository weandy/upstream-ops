package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errMailPersistence = errors.New("mail persistence operation failed")
var errMailSize = errors.New("mail content exceeds storage limit")

// Driver errors (including constraint details) may contain bound values.
func mailError(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{
		context.Canceled, context.DeadlineExceeded, ErrMailNotFound, ErrMailActiveBatchExists,
		ErrMailIdempotencyConflict, ErrMailRevisionMismatch, ErrMailRetryAlreadyExists,
		ErrMailNoRetryableItems, ErrMailTemplateLimitReached, ErrMailTemplateBuiltinProtected,
		ErrMailTestRateLimited, ErrMailBatchNotCancellable, ErrMailInstanceDisabled,
		ErrMailClaimLost, ErrMailLeaseLost, ErrMailGenerationMismatch, errMailSize,
	} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return errMailPersistence
}

func (s *SiteOpsStore) mailTx(ctx context.Context, fn func(*gorm.DB) error) error {
	return mailError(s.db.WithContext(ctx).Transaction(fn))
}

// A parent lock serializes all mail decisions for one instance, including changes
// to settings and suppressions. Never escape the transaction to the root session.
func lockMailParent(tx *gorm.DB, id uint) (*SiteOpsInstance, error) {
	inst, err := findSiteOpsInstance(tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "base_url", "enabled", "generation", "cred_gen"), id)
	if errors.Is(err, ErrSiteOpsNotFound) {
		return nil, ErrMailNotFound
	}
	return inst, err
}

func checkMailScope(inst *SiteOpsInstance, k MailScopeKey) error {
	hash := sha256.Sum256([]byte(inst.BaseURL))
	if inst.Generation != k.Generation || inst.CredGen != k.CredGen || hex.EncodeToString(hash[:]) != k.TargetBinding {
		return ErrMailGenerationMismatch
	}
	return nil
}

func (s *SiteOpsStore) mailScopeTx(ctx context.Context, k MailScopeKey, fn func(*gorm.DB) error) error {
	return s.mailTx(ctx, func(tx *gorm.DB) error {
		inst, err := lockMailParent(tx, k.InstanceID)
		if err != nil {
			return err
		}
		if err = checkMailScope(inst, k); err != nil {
			return err
		}
		return fn(tx)
	})
}

func mailScopeRows(tx *gorm.DB, k MailScopeKey) *gorm.DB {
	return tx.Where("instance_id = ? AND target_binding = ?", k.InstanceID, k.TargetBinding)
}

func mailOne(q *gorm.DB, dest any) error {
	err := q.Take(dest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrMailNotFound
	}
	return err
}

func mailCAS(r *gorm.DB, conflict error) error {
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return conflict
	}
	return nil
}

func mailPage(q *gorm.DB, p MailListParams) *gorm.DB {
	if p.BeforeID != 0 {
		q = q.Where("id < ?", p.BeforeID)
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	return q.Order("id DESC").Limit(min(p.Limit, 101))
}

func (s *SiteOpsStore) GetMailOverview(ctx context.Context, k MailScopeKey) (out MailOverviewRows, err error) {
	err = s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		var smtp SiteOpsMailSMTP
		err := mailOne(mailScopeRows(tx, k), &smtp)
		if err == nil {
			out.SMTP = &smtp
		} else if !errors.Is(err, ErrMailNotFound) {
			return err
		}
		if err = mailScopeRows(tx, k).Model(&SiteOpsMailTemplate{}).Count(&out.TemplateCount).Error; err != nil {
			return err
		}
		if err = mailScopeRows(tx, k).Model(&SiteOpsMailSuppression{}).Count(&out.SuppressionCount).Error; err != nil {
			return err
		}
		var batch SiteOpsMailBatch
		err = mailOne(mailScopeRows(tx, k).Where("active_slot = ?", k.InstanceID), &batch)
		if errors.Is(err, ErrMailNotFound) {
			return nil
		}
		if err == nil {
			out.ActiveBatch = &batch
		}
		return err
	})
	return
}

func (s *SiteOpsStore) EnsureMailScope(ctx context.Context, k MailScopeKey, seedCipher string) (row SiteOpsMailScope, err error) {
	err = s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		err := mailOne(mailScopeRows(tx, k), &row)
		if !errors.Is(err, ErrMailNotFound) {
			return err
		}
		if seedCipher == "" {
			return ErrMailNotFound
		}
		row = SiteOpsMailScope{InstanceID: k.InstanceID, TargetBinding: k.TargetBinding, SeedCipher: seedCipher}
		return tx.Create(&row).Error
	})
	return
}

func (s *SiteOpsStore) GetMailSMTP(ctx context.Context, k MailScopeKey) (row SiteOpsMailSMTP, err error) {
	err = s.mailScopeTx(ctx, k, func(tx *gorm.DB) error { return mailOne(mailScopeRows(tx, k), &row) })
	return
}

func saveMailSMTP(tx *gorm.DB, k MailScopeKey, revision int64, row *SiteOpsMailSMTP) error {
	var old SiteOpsMailSMTP
	err := mailOne(mailScopeRows(tx, k), &old)
	row.InstanceID, row.TargetBinding, row.Revision = k.InstanceID, k.TargetBinding, revision+1
	if errors.Is(err, ErrMailNotFound) {
		if revision != 0 {
			return ErrMailRevisionMismatch
		}
		return tx.Create(row).Error
	}
	if err != nil {
		return err
	}
	if revision != old.Revision {
		return ErrMailRevisionMismatch
	}
	row.CreatedAt = old.CreatedAt
	return mailCAS(mailScopeRows(tx, k).Model(&old).Where("revision = ?", revision).
		Select("*").Omit("instance_id", "target_binding", "created_at").Updates(row), ErrMailRevisionMismatch)
}

func (s *SiteOpsStore) SaveMailSMTP(ctx context.Context, k MailScopeKey, revision int64, row SiteOpsMailSMTP) (SiteOpsMailSMTP, error) {
	err := s.mailScopeTx(ctx, k, func(tx *gorm.DB) error { return saveMailSMTP(tx, k, revision, &row) })
	return row, err
}

func (s *SiteOpsStore) mailCopyTx(ctx context.Context, p MailCopyParams, fn func(*gorm.DB) error) error {
	first, second := p.Source, p.Target
	if first.InstanceID == second.InstanceID {
		return ErrMailGenerationMismatch
	}
	if first.InstanceID > second.InstanceID {
		first, second = second, first
	}
	return s.mailTx(ctx, func(tx *gorm.DB) error {
		for _, k := range []MailScopeKey{first, second} {
			inst, err := lockMailParent(tx, k.InstanceID)
			if err != nil {
				return err
			}
			if err = checkMailScope(inst, k); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func (s *SiteOpsStore) CopyMailSMTP(ctx context.Context, p MailCopySMTPParams) (SiteOpsMailSMTP, error) {
	err := s.mailCopyTx(ctx, p.MailCopyParams, func(tx *gorm.DB) error {
		var source SiteOpsMailSMTP
		if err := mailOne(mailScopeRows(tx, p.Source).Select("revision"), &source); err != nil {
			return err
		}
		if source.Revision != p.SourceRevision {
			return ErrMailRevisionMismatch
		}
		return saveMailSMTP(tx, p.Target, p.ExpectedRevision, &p.TargetSMTP)
	})
	return p.TargetSMTP, err
}

func (s *SiteOpsStore) EnsureMailBuiltinTemplates(ctx context.Context, k MailScopeKey, rows []SiteOpsMailTemplate) error {
	return s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		for _, row := range rows {
			var n int64
			if err := mailScopeRows(tx, k).Model(&SiteOpsMailTemplate{}).Where("template_key = ?", row.TemplateKey).Count(&n).Error; err != nil {
				return err
			}
			if n != 0 {
				continue
			}
			row.ID, row.InstanceID, row.TargetBinding, row.Revision, row.IsBuiltIn = 0, k.InstanceID, k.TargetBinding, 1, true
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SiteOpsStore) ListMailTemplates(ctx context.Context, p MailListParams) (rows []SiteOpsMailTemplate, err error) {
	err = s.mailScopeTx(ctx, p.Scope, func(tx *gorm.DB) error {
		return mailPage(mailScopeRows(tx, p.Scope), p).Omit("html_body", "text_body").Find(&rows).Error
	})
	return
}

func (s *SiteOpsStore) GetMailTemplate(ctx context.Context, k MailScopeKey, id uint) (row SiteOpsMailTemplate, err error) {
	err = s.mailScopeTx(ctx, k, func(tx *gorm.DB) error { return mailOne(mailScopeRows(tx, k).Where("id = ?", id), &row) })
	return
}

func saveMailTemplate(tx *gorm.DB, k MailScopeKey, revision int64, row *SiteOpsMailTemplate) error {
	if len(row.HTMLBody) > 24<<10 || len(row.TextBody) > 12<<10 {
		return errMailSize
	}
	row.InstanceID, row.TargetBinding, row.Revision = k.InstanceID, k.TargetBinding, revision+1
	if row.ID == 0 {
		if revision != 0 {
			return ErrMailRevisionMismatch
		}
		var n int64
		if err := mailScopeRows(tx, k).Model(&SiteOpsMailTemplate{}).Where("is_built_in = ?", false).Count(&n).Error; err != nil {
			return err
		}
		if n >= 50 {
			return ErrMailTemplateLimitReached
		}
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		row.IsBuiltIn, row.TemplateKey = false, "custom:"+hex.EncodeToString(nonce[:])
		return tx.Create(row).Error
	}
	var old SiteOpsMailTemplate
	if err := mailOne(mailScopeRows(tx, k).Where("id = ?", row.ID), &old); err != nil {
		return err
	}
	row.TemplateKey, row.IsBuiltIn, row.CreatedAt = old.TemplateKey, old.IsBuiltIn, old.CreatedAt
	return mailCAS(tx.Model(&old).Where("revision = ?", revision).Select("revision", "name", "category", "subject", "html_body", "text_body", "updated_at").Updates(row), ErrMailRevisionMismatch)
}

func (s *SiteOpsStore) SaveMailTemplate(ctx context.Context, k MailScopeKey, revision int64, row SiteOpsMailTemplate) (SiteOpsMailTemplate, error) {
	err := s.mailScopeTx(ctx, k, func(tx *gorm.DB) error { return saveMailTemplate(tx, k, revision, &row) })
	return row, err
}

func (s *SiteOpsStore) CopyMailTemplate(ctx context.Context, p MailCopyTemplateParams) (SiteOpsMailTemplate, error) {
	err := s.mailCopyTx(ctx, p.MailCopyParams, func(tx *gorm.DB) error {
		var source SiteOpsMailTemplate
		if err := mailOne(mailScopeRows(tx, p.Source).Select("revision").Where("id = ?", p.SourceTemplateID), &source); err != nil {
			return err
		}
		if source.Revision != p.SourceRevision {
			return ErrMailRevisionMismatch
		}
		p.TargetTemplate.ID = 0
		return saveMailTemplate(tx, p.Target, p.ExpectedRevision, &p.TargetTemplate)
	})
	return p.TargetTemplate, err
}

func (s *SiteOpsStore) DeleteMailTemplate(ctx context.Context, k MailScopeKey, id uint, revision int64) error {
	return s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		var row SiteOpsMailTemplate
		if err := mailOne(mailScopeRows(tx, k).Where("id = ?", id), &row); err != nil {
			return err
		}
		if row.IsBuiltIn {
			return ErrMailTemplateBuiltinProtected
		}
		return mailCAS(tx.Where("id = ? AND revision = ?", id, revision).Delete(&row), ErrMailRevisionMismatch)
	})
}

func (s *SiteOpsStore) ListMailSuppressions(ctx context.Context, p MailListParams) (rows []SiteOpsMailSuppression, err error) {
	err = s.mailScopeTx(ctx, p.Scope, func(tx *gorm.DB) error { return mailPage(mailScopeRows(tx, p.Scope), p).Find(&rows).Error })
	return
}

func (s *SiteOpsStore) FindSuppressedMailHashes(ctx context.Context, k MailScopeKey, hashes []string) (map[string]bool, error) {
	found := make(map[string]bool)
	err := s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		for len(hashes) > 0 {
			n := min(len(hashes), 200)
			var part []string
			if err := mailScopeRows(tx, k).Model(&SiteOpsMailSuppression{}).Where("email_hash IN ?", hashes[:n]).Pluck("email_hash", &part).Error; err != nil {
				return err
			}
			for _, hash := range part {
				found[hash] = true
			}
			hashes = hashes[n:]
		}
		return nil
	})
	return found, err
}

func (s *SiteOpsStore) DeleteMailSuppression(ctx context.Context, k MailScopeKey, id uint) error {
	return s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		return mailCAS(mailScopeRows(tx, k).Where("id = ?", id).Delete(&SiteOpsMailSuppression{}), ErrMailNotFound)
	})
}

const mailDeleting = "deleting" // Private marker while chunked retention is in progress.

func mailBatch(tx *gorm.DB, k MailScopeKey, id uint) (b SiteOpsMailBatch, err error) {
	err = mailOne(mailScopeRows(tx, k).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status <> ?", id, mailDeleting), &b)
	return
}

func (s *SiteOpsStore) GetMailBatch(ctx context.Context, k MailScopeKey, id uint) (b SiteOpsMailBatch, err error) {
	err = s.mailScopeTx(ctx, k, func(tx *gorm.DB) error { var e error; b, e = mailBatch(tx, k, id); return e })
	return
}

func (s *SiteOpsStore) ListMailBatches(ctx context.Context, p MailListParams) (rows []SiteOpsMailBatch, err error) {
	err = s.mailScopeTx(ctx, p.Scope, func(tx *gorm.DB) error {
		q := mailScopeRows(tx, p.Scope).Where("status <> ? AND mode <> ?", mailDeleting, "test")
		if p.Status != "" {
			q = q.Where("status = ?", p.Status)
		}
		return mailPage(q, p).Omit("snapshot_json", "audience_json").Find(&rows).Error
	})
	return
}

func (s *SiteOpsStore) ListMailItems(ctx context.Context, batchID uint, p MailListParams) (rows []SiteOpsMailItem, err error) {
	err = s.mailScopeTx(ctx, p.Scope, func(tx *gorm.DB) error {
		b, err := mailBatch(tx, p.Scope, batchID)
		if err != nil {
			return err
		}
		if b.Mode == "test" {
			return nil
		}
		q := tx.Where("instance_id = ? AND batch_id = ?", p.Scope.InstanceID, batchID)
		if p.Status != "" {
			q = q.Where("status = ?", p.Status)
		}
		return mailPage(q, p).Omit("payload_cipher", "claim_token").Find(&rows).Error
	})
	return
}

func existingMailRequest(tx *gorm.DB, p MailBatchCreateParams, out *MailBatchCreateResult) (bool, error) {
	var b SiteOpsMailBatch
	err := mailOne(mailScopeRows(tx, p.Scope).Where("request_id = ?", p.RequestID), &b)
	if errors.Is(err, ErrMailNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if b.RequestDigest != p.RequestDigest {
		return true, ErrMailIdempotencyConflict
	}
	if b.Status == mailDeleting {
		return true, ErrMailNotFound
	}
	out.Batch, out.IdempotentReplay = b, true
	return true, nil
}

func checkMailActive(tx *gorm.DB, id uint, out *MailBatchCreateResult) error {
	var b SiteOpsMailBatch
	err := mailOne(tx.Select("id").Where("active_slot = ?", id), &b)
	if errors.Is(err, ErrMailNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	out.ActiveBatchID = b.ID
	return ErrMailActiveBatchExists
}

func checkMailRetry(tx *gorm.DB, p MailBatchCreateParams) error {
	if p.ParentBatchID == nil {
		if p.Mode == "retry_failed" {
			return ErrMailNoRetryableItems
		}
		return nil
	}
	b, err := mailBatch(tx, p.Scope, *p.ParentBatchID)
	if err != nil {
		return err
	}
	if p.Mode != "retry_failed" || b.Mode == "test" || b.ActiveSlot != nil || b.FinishedAt == nil || b.FailedCount == 0 {
		return ErrMailNoRetryableItems
	}
	var n int64
	if err = tx.Model(&SiteOpsMailBatch{}).Where("parent_batch_id = ?", b.ID).Count(&n).Error; err != nil {
		return err
	}
	if n != 0 {
		return ErrMailRetryAlreadyExists
	}
	return nil
}

func (s *SiteOpsStore) CreateMailBatch(ctx context.Context, p MailBatchCreateParams) (out MailBatchCreateResult, err error) {
	if len(p.SnapshotJSON) > 60<<10 {
		return out, errMailSize
	}
	var insertErr error
	err = s.mailTx(ctx, func(tx *gorm.DB) error {
		inst, err := lockMailParent(tx, p.Scope.InstanceID)
		if err != nil {
			return err
		}
		if done, err := existingMailRequest(tx, p, &out); done || err != nil {
			return err
		}
		if !inst.Enabled {
			return ErrMailInstanceDisabled
		}
		if err = checkMailScope(inst, p.Scope); err != nil {
			return err
		}
		var smtp SiteOpsMailSMTP
		if err = mailOne(mailScopeRows(tx, p.Scope).Select("revision"), &smtp); err != nil {
			return err
		}
		if smtp.Revision != p.SMTPRevision {
			return ErrMailRevisionMismatch
		}
		if p.Mode != "test" && p.Mode != "retry_failed" {
			var tpl SiteOpsMailTemplate
			if err = mailOne(mailScopeRows(tx, p.Scope).Select("revision").Where("id = ?", p.TemplateID), &tpl); err != nil {
				return err
			}
			if tpl.Revision != p.TemplateRevision {
				return ErrMailRevisionMismatch
			}
		}
		if err = checkMailRetry(tx, p); err != nil {
			return err
		}
		if err = checkMailActive(tx, inst.ID, &out); err != nil {
			return err
		}
		now := p.Now.UTC()
		if p.Mode == "test" {
			var recent SiteOpsMailBatch
			err = mailOne(tx.Select("created_at").Where("instance_id = ? AND mode = ? AND created_at > ?", inst.ID, "test", now.Add(-time.Minute)).Order("created_at DESC"), &recent)
			if err == nil {
				out.RetryAfterSeconds = int(math.Ceil(recent.CreatedAt.Add(time.Minute).Sub(now).Seconds()))
				return ErrMailTestRateLimited
			}
			if !errors.Is(err, ErrMailNotFound) {
				return err
			}
		}
		out.Batch = SiteOpsMailBatch{
			InstanceID: inst.ID, TargetBinding: p.Scope.TargetBinding, ActiveSlot: &inst.ID,
			RequestID: p.RequestID, RequestDigest: p.RequestDigest, ParentBatchID: p.ParentBatchID,
			Generation: inst.Generation, CredGen: inst.CredGen, SMTPRevision: p.SMTPRevision,
			TemplateID: p.TemplateID, TemplateRevision: p.TemplateRevision, TemplateName: p.TemplateName,
			Mode: p.Mode, SnapshotJSON: p.SnapshotJSON, AudienceJSON: p.AudienceJSON,
			Status: "preparing", NextWorkAt: now, PrepareBy: p.PrepareBy.UTC(), CreatedAt: now,
		}
		insertErr = tx.Create(&out.Batch).Error
		return insertErr
	})
	if insertErr != nil {
		// A failed INSERT aborts a PostgreSQL transaction. Classify the winner in a fresh one.
		out = MailBatchCreateResult{}
		err = s.mailScopeTx(ctx, p.Scope, func(tx *gorm.DB) error {
			if done, err := existingMailRequest(tx, p, &out); done || err != nil {
				return err
			}
			if err := checkMailActive(tx, p.Scope.InstanceID, &out); err != nil {
				return err
			}
			if err := checkMailRetry(tx, p); err != nil {
				return err
			}
			return errors.New("mail batch insert failed")
		})
	}
	return
}

func batchScope(b *SiteOpsMailBatch) MailScopeKey {
	return MailScopeKey{InstanceID: b.InstanceID, TargetBinding: b.TargetBinding, Generation: b.Generation, CredGen: b.CredGen}
}

func mailItems(tx *gorm.DB, b *SiteOpsMailBatch) *gorm.DB {
	return tx.Model(&SiteOpsMailItem{}).Where("instance_id = ? AND batch_id = ?", b.InstanceID, b.ID)
}

func mailDone(status string, now time.Time) map[string]any {
	return map[string]any{"status": status, "phase": "done", "payload_cipher": nil, "finished_at": now.UTC()}
}

// All bulk transitions prelock the affected rows in primary-key order. The parent
// lock also prevents concurrent inserts into the batch while its counts are rebuilt.
func lockMailItemIDs(q *gorm.DB) (ids []uint, err error) {
	err = q.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id ASC").Pluck("id", &ids).Error
	return
}

func updateMailItems(q *gorm.DB, fields map[string]any) (int, error) {
	ids, err := lockMailItemIDs(q.Session(&gorm.Session{}))
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	r := q.Updates(fields)
	return int(r.RowsAffected), r.Error
}

// SuppressedCount also includes recipients filtered out before staging, for
// which there is deliberately no item. Record only newly suppressed rows there.
func recountMail(tx *gorm.DB, b *SiteOpsMailBatch, now time.Time, newSuppressed int) (int, error) {
	var groups []struct {
		Status string
		N      int
	}
	if err := mailItems(tx, b).Select("status, count(*) AS n").Group("status").Scan(&groups).Error; err != nil {
		return 0, err
	}
	count := make(map[string]int, len(groups))
	for _, g := range groups {
		count[g.Status] = g.N
	}
	b.SuppressedCount += newSuppressed
	b.SentCount, b.FailedCount, b.UncertainCount, b.CancelledCount = count["sent"], count["failed"], count["uncertain"], count["cancelled"]
	fields := map[string]any{"sent_count": b.SentCount, "failed_count": b.FailedCount, "uncertain_count": b.UncertainCount, "cancelled_count": b.CancelledCount, "suppressed_count": b.SuppressedCount}
	if count["pending"]+count["sending"] == 0 && b.Status != "preparing" {
		switch {
		case b.CancelledAt != nil:
			b.Status = "cancelled"
		case b.FailedCount+b.UncertainCount == 0 && b.AbortReason == "":
			b.Status = "completed"
		case b.SentCount > 0:
			b.Status = "completed_with_errors"
		default:
			b.Status = "failed"
		}
		b.FinishedAt, b.ActiveSlot, b.LeaseUntil, b.Owner = &now, nil, nil, ""
		fields["status"], fields["finished_at"], fields["active_slot"], fields["lease_until"], fields["owner"] = b.Status, now.UTC(), nil, nil, ""
		if b.Mode == "selected" {
			b.AudienceJSON = ""
			fields["audience_json"] = ""
		}
	}
	return count["sending"], tx.Model(b).Updates(fields).Error
}

func stopMailBatch(tx *gorm.DB, b *SiteOpsMailBatch, now time.Time, abortReason string) (int, error) {
	if b.ActiveSlot == nil {
		if abortReason == "" && b.Status == "cancelled" {
			return 0, nil
		}
		return 0, ErrMailBatchNotCancellable
	}
	fields := map[string]any{"status": "cancelling"}
	status := "cancelled"
	if abortReason != "" {
		// An incomplete directory scan is never a source of retryable deliveries.
		if b.Status != "preparing" {
			status = "failed"
		}
		b.AbortReason = abortReason
		fields["abort_reason"] = abortReason
	} else if b.CancelledAt == nil {
		b.CancelledAt = &now
		fields["cancelled_at"] = now.UTC()
	}
	b.Status = "cancelling"
	if err := tx.Model(b).Updates(fields).Error; err != nil {
		return 0, err
	}
	itemFields := mailDone(status, now)
	if abortReason != "" {
		itemFields["error_code"] = abortReason
	}
	_, err := updateMailItems(mailItems(tx, b).Where("status = ? OR (status = ? AND phase = ?)", "pending", "sending", "envelope"), itemFields)
	if err != nil {
		return 0, err
	}
	return recountMail(tx, b, now, 0)
}

func (s *SiteOpsStore) CancelMailBatch(ctx context.Context, k MailScopeKey, id uint, now time.Time) (out MailCancelResult, err error) {
	err = s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		b, err := mailBatch(tx, k, id)
		if err != nil {
			return err
		}
		out.InFlightData, err = stopMailBatch(tx, &b, now.UTC(), "")
		out.Batch = b
		return err
	})
	return
}

func activeMailBatch(tx *gorm.DB, id uint) (*SiteOpsMailBatch, error) {
	var b SiteOpsMailBatch
	err := mailOne(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("active_slot = ?", id), &b)
	if errors.Is(err, ErrMailNotFound) {
		return nil, nil
	}
	return &b, err
}

func addMailSuppressions(tx *gorm.DB, k MailScopeKey, rows []MailSuppressionEntry) (int, error) {
	var added int
	hashes := make([]string, 0, len(rows))
	for _, entry := range rows {
		var n int64
		if err := mailScopeRows(tx, k).Model(&SiteOpsMailSuppression{}).Where("email_hash = ?", entry.EmailHash).Count(&n).Error; err != nil {
			return 0, err
		}
		if n == 0 {
			row := SiteOpsMailSuppression{InstanceID: k.InstanceID, TargetBinding: k.TargetBinding, EmailHash: entry.EmailHash, EmailMasked: entry.EmailMasked, Reason: entry.Reason}
			if err := tx.Create(&row).Error; err != nil {
				return 0, err
			}
			added++
		}
		hashes = append(hashes, entry.EmailHash)
	}
	b, err := activeMailBatch(tx, k.InstanceID)
	if err != nil || b == nil || b.TargetBinding != k.TargetBinding {
		return added, err
	}
	now := time.Now().UTC()
	n, err := updateMailItems(mailItems(tx, b).Where("email_hash IN ?", hashes).Where("status = ? OR (status = ? AND phase = ?)", "pending", "sending", "envelope"), mailDone("suppressed", now))
	if err == nil && n > 0 {
		_, err = recountMail(tx, b, now, n)
	}
	return added, err
}

func (s *SiteOpsStore) AddMailSuppressions(ctx context.Context, k MailScopeKey, rows []MailSuppressionEntry) (added int, err error) {
	err = s.mailScopeTx(ctx, k, func(tx *gorm.DB) error { var e error; added, e = addMailSuppressions(tx, k, rows); return e })
	return
}

func (s *SiteOpsStore) AddMailSuppressionFromItem(ctx context.Context, k MailScopeKey, id uint, reason string) (bool, error) {
	var n int
	err := s.mailScopeTx(ctx, k, func(tx *gorm.DB) error {
		var item SiteOpsMailItem
		if err := mailOne(tx.Where("instance_id = ? AND id = ?", k.InstanceID, id), &item); err != nil {
			return err
		}
		b, err := mailBatch(tx, k, item.BatchID)
		if err != nil {
			return err
		}
		if b.Mode == "test" {
			return ErrMailNotFound
		}
		n, err = addMailSuppressions(tx, k, []MailSuppressionEntry{{item.EmailHash, item.EmailMasked, reason}})
		return err
	})
	return n != 0, err
}

func dueMail(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("active_slot IS NOT NULL AND status IN ? AND next_work_at <= ? AND (lease_until IS NULL OR lease_until <= ?)", []string{"preparing", "queued", "running"}, now, now)
}

func (s *SiteOpsStore) LeaseMailBatch(ctx context.Context, owner string, now, until time.Time) (*SiteOpsMailBatch, error) {
	if owner == "" || !until.After(now) {
		return nil, ErrMailLeaseLost
	}
	db := s.db.WithContext(ctx)
	for {
		var candidate SiteOpsMailBatch
		err := mailOne(dueMail(db, now.UTC()).Select("id", "instance_id").Order("next_work_at ASC, id ASC"), &candidate)
		if errors.Is(err, ErrMailNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var leased *SiteOpsMailBatch
		err = s.mailTx(ctx, func(tx *gorm.DB) error {
			inst, err := lockMailParent(tx, candidate.InstanceID)
			if errors.Is(err, ErrMailNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			var b SiteOpsMailBatch
			err = mailOne(dueMail(tx, now.UTC()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", candidate.ID), &b)
			if errors.Is(err, ErrMailNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if !inst.Enabled || checkMailScope(inst, batchScope(&b)) != nil {
				_, err = stopMailBatch(tx, &b, now, "")
				return err
			}
			fields := map[string]any{"owner": owner, "lease_until": until.UTC()}
			if b.Status == "preparing" {
				if !b.PrepareBy.After(now) {
					_, err = stopMailBatch(tx, &b, now, "MAIL_PREP_DEADLINE_EXCEEDED")
					return err
				}
				if b.LeaseUntil != nil {
					if _, err = lockMailItemIDs(mailItems(tx, &b)); err != nil {
						return err
					}
					if err = mailItems(tx, &b).Delete(&SiteOpsMailItem{}).Error; err != nil {
						return err
					}
					fields["suppressed_count"] = 0
				}
			} else if b.Status == "queued" {
				fields["status"], fields["started_at"] = "running", now.UTC()
			}
			if err = mailCAS(dueMail(tx.Model(&b), now.UTC()).Updates(fields), ErrMailLeaseLost); err != nil {
				return err
			}
			leased = &b
			return nil
		})
		if err != nil || leased != nil {
			return leased, err
		}
	}
}

func validMailLease(b *SiteOpsMailBatch, w MailWorkKey) bool {
	return b.ActiveSlot != nil && w.Owner != "" && b.Owner == w.Owner && b.LeaseUntil != nil && b.LeaseUntil.After(w.Now)
}

func (s *SiteOpsStore) mailWorkTx(ctx context.Context, w MailWorkKey, fn func(*gorm.DB, *SiteOpsMailBatch) error) error {
	return s.mailScopeTx(ctx, w.Scope, func(tx *gorm.DB) error {
		b, err := mailBatch(tx, w.Scope, w.BatchID)
		if err != nil {
			return err
		}
		if !validMailLease(&b, w) {
			return ErrMailLeaseLost
		}
		return fn(tx, &b)
	})
}

func (s *SiteOpsStore) RenewMailLease(ctx context.Context, w MailWorkKey, until time.Time) error {
	return s.mailWorkTx(ctx, w, func(tx *gorm.DB, b *SiteOpsMailBatch) error {
		if !until.After(w.Now) || b.Status == "cancelling" {
			return ErrMailLeaseLost
		}
		return tx.Model(b).Updates(map[string]any{"lease_until": until.UTC()}).Error
	})
}

func (s *SiteOpsStore) StageMailItems(ctx context.Context, w MailWorkKey, rows []MailItemStageRow) error {
	return s.mailWorkTx(ctx, w, func(tx *gorm.DB, b *SiteOpsMailBatch) error {
		if b.Status != "preparing" || !b.PrepareBy.After(w.Now) {
			return ErrMailLeaseLost
		}
		items := make([]SiteOpsMailItem, 0, len(rows))
		for i := range rows {
			r := &rows[i]
			items = append(items, SiteOpsMailItem{InstanceID: b.InstanceID, BatchID: b.ID, UserID: r.UserID, EmailHash: r.EmailHash, EmailMasked: r.EmailMasked, PayloadCipher: &r.PayloadCipher, Status: "pending"})
		}
		if len(items) == 0 {
			return nil
		}
		return tx.CreateInBatches(&items, 40).Error
	})
}

func (s *SiteOpsStore) FinishMailPreparation(ctx context.Context, w MailWorkKey, c MailPrepareCounts) error {
	return s.mailWorkTx(ctx, w, func(tx *gorm.DB, b *SiteOpsMailBatch) error {
		if b.Status != "preparing" || !b.PrepareBy.After(w.Now) {
			return ErrMailLeaseLost
		}
		var n int64
		if err := mailItems(tx, b).Count(&n).Error; err != nil {
			return err
		}
		fields := map[string]any{"pages": c.Pages, "scanned": c.Scanned, "excluded_count": c.Excluded, "duplicate_count": c.Duplicate, "recipient_count": n, "status": "queued", "next_work_at": w.Now.UTC(), "owner": "", "lease_until": nil}
		if err := tx.Model(b).Updates(fields).Error; err != nil {
			return err
		}
		b.Status = "queued"
		_, err := recountMail(tx, b, w.Now.UTC(), c.Suppressed)
		return err
	})
}

func (s *SiteOpsStore) AbortMailBatch(ctx context.Context, w MailWorkKey, code string) error {
	return s.mailWorkTx(ctx, w, func(tx *gorm.DB, b *SiteOpsMailBatch) error {
		if b.Status == "cancelling" || code == "" {
			return ErrMailBatchNotCancellable
		}
		_, err := stopMailBatch(tx, b, w.Now.UTC(), code)
		return err
	})
}

// Load SMTP before locking the batch. Complete deliberately uses neither the
// current scope nor the lease, as the remote peer may already have accepted DATA.
func mailSendContext(tx *gorm.DB, p MailClaimParams) (SiteOpsMailBatch, error, error) {
	inst, err := lockMailParent(tx, p.Scope.InstanceID)
	if err != nil {
		return SiteOpsMailBatch{}, nil, err
	}
	state := checkMailScope(inst, p.Scope)
	if !inst.Enabled {
		state = ErrMailInstanceDisabled
	}
	var smtp SiteOpsMailSMTP
	err = mailOne(mailScopeRows(tx, p.Scope).Select("revision"), &smtp)
	if err != nil && !errors.Is(err, ErrMailNotFound) {
		return SiteOpsMailBatch{}, nil, err
	}
	if state == nil && (err != nil || smtp.Revision != p.SMTPRevision) {
		state = ErrMailRevisionMismatch
	}
	b, err := mailBatch(tx, p.Scope, p.BatchID)
	if err == nil && state == nil {
		switch {
		case b.Generation != p.Scope.Generation || b.CredGen != p.Scope.CredGen:
			state = ErrMailGenerationMismatch
		case b.SMTPRevision != p.SMTPRevision:
			state = ErrMailRevisionMismatch
		case b.Status != "running" || !validMailLease(&b, p.MailWorkKey):
			state = ErrMailLeaseLost
		}
	}
	return b, state, err
}

func mailSuppressed(tx *gorm.DB, k MailScopeKey, hash string) (bool, error) {
	var n int64
	err := mailScopeRows(tx, k).Model(&SiteOpsMailSuppression{}).Where("email_hash = ?", hash).Count(&n).Error
	return n != 0, err
}

func (s *SiteOpsStore) ClaimMailItem(ctx context.Context, p MailClaimParams) (item *SiteOpsMailItem, err error) {
	if p.ClaimToken == "" {
		return nil, ErrMailClaimLost
	}
	err = s.mailTx(ctx, func(tx *gorm.DB) error {
		b, state, err := mailSendContext(tx, p)
		if err != nil {
			return err
		}
		if state != nil {
			return state
		}
		for {
			var row SiteOpsMailItem
			err = mailOne(mailItems(tx, &b).Clauses(clause.Locking{Strength: "UPDATE"}).Where("status = ?", "pending").Order("id ASC"), &row)
			if errors.Is(err, ErrMailNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			suppressed, err := mailSuppressed(tx, p.Scope, row.EmailHash)
			if err != nil {
				return err
			}
			q := tx.Model(&row).Where("status = ?", "pending")
			if suppressed {
				if err = mailCAS(q.Updates(mailDone("suppressed", p.Now)), ErrMailClaimLost); err != nil {
					return err
				}
				if _, err = recountMail(tx, &b, p.Now.UTC(), 1); err != nil {
					return err
				}
				continue
			}
			err = mailCAS(q.Updates(map[string]any{"status": "sending", "phase": "envelope", "claim_token": p.ClaimToken, "claimed_at": p.Now.UTC()}), ErrMailClaimLost)
			if err == nil {
				item = &row
			}
			return err
		}
	})
	if err != nil {
		return nil, err
	}
	return
}

func (s *SiteOpsStore) AuthorizeMailData(ctx context.Context, p MailAuthorizeDataParams) (bool, error) {
	var authorized bool
	err := s.mailTx(ctx, func(tx *gorm.DB) error {
		b, state, err := mailSendContext(tx, p.MailClaimParams)
		if err != nil {
			return err
		}
		var row SiteOpsMailItem
		if err = mailOne(mailItems(tx, &b).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", p.ItemID), &row); err != nil {
			return err
		}
		if p.ClaimToken == "" || row.ClaimToken != p.ClaimToken {
			return ErrMailClaimLost
		}
		if row.Status == "cancelled" || row.Status == "suppressed" {
			return nil
		}
		if row.Status != "sending" || row.Phase != "envelope" {
			return ErrMailClaimLost
		}
		suppressed, err := mailSuppressed(tx, p.Scope, row.EmailHash)
		if err != nil {
			return err
		}
		q := tx.Model(&row).Where("status = ? AND phase = ? AND claim_token = ?", "sending", "envelope", p.ClaimToken)
		if state != nil || suppressed {
			status, delta := "cancelled", 0
			if suppressed {
				status, delta = "suppressed", 1
			}
			if err = mailCAS(q.Updates(mailDone(status, p.Now)), ErrMailClaimLost); err != nil {
				return err
			}
			_, err = recountMail(tx, &b, p.Now.UTC(), delta)
			return err
		}
		authorized = true
		return mailCAS(q.Updates(map[string]any{"phase": "data"}), ErrMailClaimLost)
	})
	return authorized && err == nil, err
}

func (s *SiteOpsStore) CompleteMailItem(ctx context.Context, p MailCompleteItemParams) error {
	if p.ClaimToken == "" || (p.Status != "sent" && p.Status != "failed" && p.Status != "uncertain") {
		return ErrMailClaimLost
	}
	return s.mailTx(ctx, func(tx *gorm.DB) error {
		if _, err := lockMailParent(tx, p.InstanceID); err != nil {
			return err
		}
		var b SiteOpsMailBatch
		if err := mailOne(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ? AND id = ?", p.InstanceID, p.BatchID), &b); err != nil {
			return err
		}
		fields := mailDone(p.Status, p.Now)
		fields["smtp_stage"], fields["smtp_code"], fields["error_code"] = p.SMTPStage, p.SMTPCode, p.ErrorCode
		q := mailItems(tx, &b).Where("id = ? AND status = ? AND claim_token = ?", p.ItemID, "sending", p.ClaimToken)
		if p.Status == "sent" || p.Status == "uncertain" {
			q = q.Where("phase = ?", "data")
		}
		if p.Status == "sent" {
			fields["sent_at"] = p.Now.UTC()
		}
		if err := mailCAS(q.Updates(fields), ErrMailClaimLost); err != nil {
			return err
		}
		_, err := recountMail(tx, &b, p.Now.UTC(), 0)
		return err
	})
}

func (s *SiteOpsStore) RecoverStaleMailItems(ctx context.Context, before, now time.Time) (out MailRecoverResult, err error) {
	db := s.db.WithContext(ctx)
	if threshold := now.Add(-10 * time.Minute); before.After(threshold) {
		before = threshold
	}
	var candidates []SiteOpsMailBatch
	err = db.Select("id", "instance_id").Where("active_slot IS NOT NULL").Order("instance_id ASC, id ASC").Find(&candidates).Error
	if err != nil {
		return
	}
	for _, candidate := range candidates {
		var n int
		var finished bool
		err = s.mailTx(ctx, func(tx *gorm.DB) error {
			if _, err := lockMailParent(tx, candidate.InstanceID); err != nil {
				if errors.Is(err, ErrMailNotFound) {
					return nil
				}
				return err
			}
			b, err := activeMailBatch(tx, candidate.InstanceID)
			if err != nil || b == nil || b.ID != candidate.ID {
				return err
			}
			if b.Status == "preparing" && (!b.PrepareBy.After(now) || (b.LeaseUntil != nil && !b.LeaseUntil.After(now))) {
				_, err = stopMailBatch(tx, b, now.UTC(), "MAIL_PREP_DEADLINE_EXCEEDED")
			} else {
				fields := mailDone("uncertain", now)
				fields["error_code"] = "MAIL_DELIVERY_UNCERTAIN"
				n, err = updateMailItems(mailItems(tx, b).Where("status = ? AND claimed_at <= ?", "sending", before.UTC()), fields)
				if err == nil && n > 0 {
					_, err = recountMail(tx, b, now.UTC(), 0)
				}
			}
			finished = b.ActiveSlot == nil
			return err
		})
		if err != nil {
			return
		}
		out.UncertainItems += int64(n)
		if finished {
			out.FinishedBatches++
		}
	}
	return
}

func (s *SiteOpsStore) DeleteFinishedMailBatchesBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	if batchSize <= 0 || batchSize > 200 {
		batchSize = 200
	}
	if maxRows <= 0 || maxRows > 10000 {
		maxRows = 10000
	}
	if maxDuration <= 0 || maxDuration > 2*time.Second {
		maxDuration = 2 * time.Second
	}
	budget, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()
	db := s.db.WithContext(budget)
	const eligible = "active_slot IS NULL AND finished_at IS NOT NULL AND finished_at < ?"
	var deleted int64
	for deleted < int64(maxRows) {
		remaining := maxRows - int(deleted)
		var b SiteOpsMailBatch
		// A preparing retry may still need the source's failed user/hash pairs.
		retrying := db.Model(&SiteOpsMailBatch{}).Select("parent_batch_id").Where("parent_batch_id IS NOT NULL AND status = ?", "preparing")
		err := mailOne(db.Select("instance_id", "id").Where(eligible, cutoff.UTC()).Where("id NOT IN (?)", retrying).Order("id ASC"), &b)
		if errors.Is(err, ErrMailNotFound) {
			return deleted, nil
		}
		var n int64
		if err == nil {
			err = s.mailTx(budget, func(tx *gorm.DB) error {
				if _, err := lockMailParent(tx, b.InstanceID); err != nil {
					if errors.Is(err, ErrMailNotFound) {
						return nil
					}
					return err
				}
				var current SiteOpsMailBatch
				err := mailOne(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", b.ID).Where(eligible, cutoff.UTC()), &current)
				if errors.Is(err, ErrMailNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				var pendingRetry int64
				if err = tx.Model(&SiteOpsMailBatch{}).Where("parent_batch_id = ? AND status = ?", b.ID, "preparing").Count(&pendingRetry).Error; err != nil || pendingRetry != 0 {
					return err
				}
				if current.Status != mailDeleting {
					if err = tx.Model(&current).Updates(map[string]any{"status": mailDeleting}).Error; err != nil {
						return err
					}
				}
				limit := min(batchSize, remaining)
				ids, err := lockMailItemIDs(mailItems(tx, &current).Limit(limit))
				if err != nil {
					return err
				}
				if len(ids) > 0 {
					r := mailItems(tx, &current).Where("id IN ?", ids).Delete(&SiteOpsMailItem{})
					if r.Error != nil {
						return r.Error
					}
					n = r.RowsAffected
				}
				if len(ids) < limit && n < int64(remaining) {
					r := tx.Where("id = ?", b.ID).Where(eligible, cutoff.UTC()).Delete(&SiteOpsMailBatch{})
					n += r.RowsAffected
					return r.Error
				}
				return nil
			})
		}
		if err != nil {
			if budget.Err() != nil && ctx.Err() == nil {
				return deleted, nil
			}
			return deleted, mailError(err)
		}
		deleted += n
	}
	return deleted, nil
}

// Lock all mail descendants in the same order used by mail writers, before
// deleting in dependency order. No concurrent creator can pass the parent lock.
func deleteInstanceMail(tx *gorm.DB, id uint) error {
	for _, entry := range []struct {
		model any
		key   string
	}{
		{&SiteOpsMailScope{}, "target_binding"}, {&SiteOpsMailSMTP{}, "instance_id"},
		{&SiteOpsMailTemplate{}, "id"}, {&SiteOpsMailSuppression{}, "id"},
		{&SiteOpsMailBatch{}, "id"}, {&SiteOpsMailItem{}, "id"},
	} {
		var keys []string
		if err := tx.Model(entry.model).Where("instance_id = ?", id).Clauses(clause.Locking{Strength: "UPDATE"}).Order(entry.key+" ASC").Pluck(entry.key, &keys).Error; err != nil {
			return err
		}
	}
	for _, model := range []any{&SiteOpsMailItem{}, &SiteOpsMailBatch{}, &SiteOpsMailSuppression{}, &SiteOpsMailTemplate{}, &SiteOpsMailSMTP{}, &SiteOpsMailScope{}} {
		if err := tx.Where("instance_id = ?", id).Delete(model).Error; err != nil {
			return err
		}
	}
	return nil
}

package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/google/uuid"
)

const mailLeaseTime = time.Minute

type mailWorker struct {
	service *Service
	ctx     context.Context
	stop    context.CancelFunc
	wake    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	jobs    map[*mailJob]bool
	rates   map[uint]*MailRateLimiter
	wg      sync.WaitGroup
}
type mailJob struct {
	w             *mailWorker
	batch         storage.SiteOpsMailBatch
	ctx, delivery context.Context
	cancel, force context.CancelFunc
}

func newMailWorker(s *Service) *mailWorker {
	ctx, stop := context.WithCancel(context.Background())
	return &mailWorker{service: s, ctx: ctx, stop: stop, wake: make(chan struct{}, 1), done: make(chan struct{}), jobs: make(map[*mailJob]bool), rates: make(map[uint]*MailRateLimiter)}
}
func (w *mailWorker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (s *Service) wakeMail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mailWorker != nil {
		s.mailWorker.signal()
	}
}
func (s *Service) interruptMail(id uint) { s.interruptMailBatch(id, 0) }
func (s *Service) interruptMailBatch(id, batch uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mailWorker != nil {
		s.mailWorker.interrupt(id, batch, false)
	}
}
func (w *mailWorker) interrupt(id, batch uint, force bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for j := range w.jobs {
		if (id == 0 || j.batch.InstanceID == id) && (batch == 0 || j.batch.ID == batch) {
			j.cancel()
			if force {
				j.force()
			}
		}
	}
	w.signal()
}
func (w *mailWorker) Stop(grace time.Duration) {
	w.stop()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-w.done:
		return
	case <-timer.C:
		w.interrupt(0, 0, true)
		<-w.done
	}
}
func (w *mailWorker) RecoverStaleMailWork(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := w.service.deps.MailStore.RecoverStaleMailItems(ctx, now.Add(-MailStaleClaimAge), now)
	return err
}
func (w *mailWorker) run() {
	defer close(w.done)
	defer w.wg.Wait()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for w.ctx.Err() == nil {
		for w.startNext() {
		}
		select {
		case <-w.ctx.Done():
			return
		case <-w.wake:
		case <-tick.C:
			w.RecoverStaleMailWork(w.ctx)
		}
	}
}
func (w *mailWorker) startNext() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return false
	}
	now := time.Now().UTC()
	b, e := w.service.deps.MailStore.LeaseMailBatch(w.ctx, uuid.NewString(), now, now.Add(mailLeaseTime))
	if e != nil || b == nil {
		return false
	}
	ctx, cancel := context.WithCancel(w.ctx)
	delivery, force := context.WithCancel(context.Background())
	j := &mailJob{w: w, batch: *b, ctx: ctx, cancel: cancel, delivery: delivery, force: force}
	w.jobs[j] = true
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() {
			cancel()
			force()
			w.mu.Lock()
			delete(w.jobs, j)
			w.mu.Unlock()
			w.signal()
		}()
		j.run()
	}()
	return true
}
func (j *mailJob) key() storage.MailWorkKey {
	b := j.batch
	return storage.MailWorkKey{Scope: storage.MailScopeKey{InstanceID: b.InstanceID, TargetBinding: b.TargetBinding, Generation: b.Generation, CredGen: b.CredGen}, BatchID: b.ID, Owner: b.Owner, Now: time.Now().UTC()}
}
func (j *mailJob) run() {
	s := j.w.service
	ctx, stop := context.WithCancel(j.ctx)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		ticker := time.NewTicker(mailLeaseTime / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				k := j.key()
				if e := s.deps.MailStore.RenewMailLease(ctx, k, k.Now.Add(mailLeaseTime)); e != nil {
					j.cancel()
					return
				}
			}
		}
	}()
	defer func() { stop(); <-joined }()
	m, e := s.MailSite(j.ctx, j.batch.InstanceID)
	if e != nil || m.key != j.key().Scope {
		return
	}
	err := j.process(m)
	if err != nil && j.ctx.Err() == nil {
		code := MailPublicErrorCode(err)
		m.store.AbortMailBatch(j.ctx, j.key(), string(code))
	}
}
func (j *mailJob) process(m *MailSite) error {
	var snap mailSnapshot
	if json.Unmarshal([]byte(j.batch.SnapshotJSON), &snap) != nil {
		return ErrMailTemplateInvalid
	}
	seed, e := m.seed(true)
	if e != nil {
		return e
	}
	smtp, e := m.store.GetMailSMTP(j.ctx, m.key)
	if e != nil {
		return e
	}
	if smtp.Revision != j.batch.SMTPRevision {
		return ErrMailRevisionMismatch
	}
	ident, password, e := m.smtpSecrets(smtp)
	if e != nil {
		return e
	}
	if j.batch.Status == "preparing" {
		return j.prepare(m, snap, seed, ident)
	}
	if m.s.deps.MailSender == nil {
		return ErrMailSMTPConfigBlocked
	}
	cfg := MailSMTPConnectionConfig{Host: smtp.Host, Port: smtp.Port, TLSMode: MailTLSMode(smtp.TLSMode), AuthMode: MailAuthMode(smtp.AuthMode), Username: ident.Username, Password: password}
	return j.send(m, snap, seed, smtp, ident, cfg)
}
func (s mailSnapshot) template() MailTemplatePreviewInput {
	return MailTemplatePreviewInput{Subject: s.Subject, HTMLBody: s.HTML, TextBody: s.Text}
}
func (j *mailJob) prepare(m *MailSite, snap mailSnapshot, seed []byte, id MailSMTPIdentity) error {
	var result MailAudienceResult
	if j.batch.Mode == "test" {
		a, e := ProjectMailItem(m.s.deps.Cipher, m.key, seed, j.batch.ID, 0, id.FromEmail, MailPersonalVars{})
		if e != nil {
			return e
		}
		result.Rows = []storage.MailItemStageRow{{UserID: 0, EmailHash: a.EmailHash, EmailMasked: a.EmailMasked, PayloadCipher: a.PayloadCipher}}
	} else {
		target, e := m.directory()
		if e != nil {
			return e
		}
		_, used, e := ValidateMailTemplate(snap.template())
		if e != nil {
			return e
		}
		o := MailAudienceOptions{Target: target, Scope: m.key, BatchID: j.batch.ID, AsOf: j.batch.CreatedAt, PrepareBy: j.batch.PrepareBy, UsedVars: used, Seed: seed, Cipher: m.s.deps.Cipher, FindSuppressed: m.store.FindSuppressedMailHashes}
		if json.Unmarshal([]byte(j.batch.AudienceJSON), &o.Spec) != nil {
			return ErrMailInvalidInput
		}
		if j.batch.Mode == "retry_failed" {
			if j.batch.ParentBatchID == nil {
				return ErrMailNoRetryableItems
			}
			o.RetryFailed, e = LoadMailRetryRecipients(j.ctx, m.store, m.key, *j.batch.ParentBatchID)
			if e != nil {
				return e
			}
		}
		result, e = ResolveMailAudience(j.ctx, m.s.deps.MailClient, o)
		if e != nil {
			return e
		}
	}
	for start := 0; start < len(result.Rows); start += 100 {
		if e := m.store.StageMailItems(j.ctx, j.key(), result.Rows[start:min(start+100, len(result.Rows))]); e != nil {
			return e
		}
	}
	return m.store.FinishMailPreparation(j.ctx, j.key(), result.Counts)
}
func (j *mailJob) send(m *MailSite, snap mailSnapshot, seed []byte, smtp storage.SiteOpsMailSMTP, ident MailSMTPIdentity, cfg MailSMTPConnectionConfig) error {
	if smtp.MaxConcurrency < 1 || smtp.MaxConcurrency > MailMaxConcurrency || smtp.RatePerMinute < 1 || smtp.RatePerMinute > MailMaxRatePerMinute {
		return ErrMailSMTPConfigBlocked
	}
	var wg sync.WaitGroup
	j.w.mu.Lock()
	rate := j.w.rates[m.inst.ID]
	if rate == nil {
		rate = &MailRateLimiter{}
		j.w.rates[m.inst.ID] = rate
	}
	j.w.mu.Unlock()
	var mu sync.Mutex
	network := 0
	stopped := false
	ctx, cancel := context.WithCancel(j.ctx)
	defer cancel()
	for i := 0; i < smtp.MaxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lane := NewMailDeliveryLane(m.s.deps.MailSender, cfg, smtp.ReconnectEvery)
			unhook := context.AfterFunc(j.delivery, lane.Close)
			defer func() { unhook(); lane.Close() }()
			for rate.Wait(ctx, smtp.RatePerMinute) == nil {
				claim := storage.MailClaimParams{MailWorkKey: j.key(), SMTPRevision: smtp.Revision, ClaimToken: uuid.NewString()}
				item, e := m.store.ClaimMailItem(ctx, claim)
				if e != nil || item == nil {
					return
				}
				status, f := j.deliver(ctx, m, lane, claim, *item, seed, snap, smtp, ident)
				if status == "" {
					continue
				}

				mu.Lock()
				if f.Code == ErrCodeMailSMTPUnavailable && f.SMTPCode == 0 {
					network++
				} else {
					network = 0
				}
				if f.Code == ErrCodeMailSMTPConfigBlocked || network >= MailNetworkFailureLimit {
					stopped = true
					cancel()
				}
				mu.Unlock()
				// Only the evidence is retried, never the envelope or DATA.
				j.settle(m, storage.MailCompleteItemParams{InstanceID: m.inst.ID, BatchID: j.batch.ID, ItemID: item.ID, ClaimToken: claim.ClaimToken, Status: string(status), SMTPStage: string(f.Stage), SMTPCode: f.SMTPCode, ErrorCode: string(f.Code), Now: time.Now().UTC()})
			}
		}()
	}
	wg.Wait()
	if stopped {
		return ErrMailSMTPConfigBlocked
	}
	return nil
}
func (j *mailJob) deliver(ctx context.Context, m *MailSite, lane *MailDeliveryLane, claim storage.MailClaimParams, item storage.SiteOpsMailItem, seed []byte, snap mailSnapshot, smtp storage.SiteOpsMailSMTP, ident MailSMTPIdentity) (MailItemStatus, MailSMTPFailure) {
	payload, e := OpenMailItem(m.s.deps.Cipher, m.key, seed, item)
	if e != nil {
		return MailItemStatusFailed, MailSMTPFailure{Stage: MailSMTPStageConfig, Code: ErrCodeMailCryptoUnavailable}
	}
	v := MailTemplateVars{SiteName: snap.SiteName, SiteURL: snap.SiteURL, TodayDate: snap.Date, UserID: fmt.Sprintf("U#%d", item.UserID), EmailMasked: item.EmailMasked, BalanceUSD: payload.Vars.BalanceUSD, PlanName: payload.Vars.PlanName, ExpiresDate: payload.Vars.ExpiresDate, ExpiresInDays: payload.Vars.ExpiresInDays}
	msg, e := RenderMailTemplate(snap.template(), v)
	var body []byte
	if e == nil {
		body, e = BuildMailMIME(MailMessageHeaders{FromEmail: ident.FromEmail, FromName: smtp.FromName, To: payload.Email, ReplyTo: smtp.ReplyTo, UnsubscribeURL: smtp.UnsubscribeURL}, msg)
	}
	if e != nil {
		return MailItemStatusFailed, MailSMTPFailure{Stage: MailSMTPStageRender, Code: ErrCodeMailTemplateInvalid}
	}
	var permitErr error
	authorized, e := lane.Deliver(j.delivery, ident.FromEmail, payload.Email, body, func(delivery context.Context) (bool, error) {
		if ctx.Err() != nil {
			permitErr = ctx.Err()
			return false, permitErr
		}
		claim.Now = time.Now().UTC()
		ok, err := m.store.AuthorizeMailData(delivery, storage.MailAuthorizeDataParams{MailClaimParams: claim, ItemID: item.ID})
		permitErr = err
		return ok, err
	})
	if !authorized && e == nil {
		return "", MailSMTPFailure{}
	}
	if permitErr != nil {
		return MailItemStatusFailed, MailSMTPFailure{Stage: MailSMTPStageConfig, Code: ErrCodeMailClaimLost}
	}
	return ClassifyMailSMTPFailure(e, authorized)
}
func (j *mailJob) settle(m *MailSite, p storage.MailCompleteItemParams) {
	ctx, cancel := context.WithTimeout(context.Background(), MailStaleClaimAge)
	defer cancel()
	for {
		attempt, stop := context.WithTimeout(ctx, 3*time.Second)
		e := m.store.CompleteMailItem(attempt, p)
		stop()
		if e == nil || errors.Is(e, ErrMailClaimLost) || errors.Is(e, ErrMailResourceNotFound) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-j.delivery.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

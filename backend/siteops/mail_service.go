package siteops

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/google/uuid"
)

// MailSite binds a request to the instance epochs used by every subsequent transaction.
type MailSite struct {
	s     *Service
	ctx   context.Context
	inst  storage.SiteOpsInstance
	key   storage.MailScopeKey
	store MailStore
}

func (s *Service) MailSite(ctx context.Context, id uint) (*MailSite, error) {
	if id == 0 || uint64(id) > MailMaxSafeID {
		return nil, ErrMailInvalidInput
	}
	if s.deps.MailStore == nil {
		return nil, ErrMailCryptoUnavailable
	}
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(inst.BaseURL))
	return &MailSite{s, ctx, *inst, storage.MailScopeKey{InstanceID: id, TargetBinding: hex.EncodeToString(hash[:]), Generation: inst.Generation, CredGen: inst.CredGen}, s.deps.MailStore}, nil
}
func (m *MailSite) check(in MailScopeInput) error {
	if in.Generation <= 0 || in.CredGen <= 0 || in.Generation > MailMaxSafeID || in.CredGen > MailMaxSafeID {
		return ErrMailInvalidInput
	}
	if in.Generation != m.key.Generation || in.CredGen != m.key.CredGen {
		return ErrMailGenerationMismatch
	}
	return nil
}

func (m *MailSite) seed(create bool) ([]byte, error) {
	row, err := m.store.EnsureMailScope(m.ctx, m.key, "")
	if errors.Is(err, ErrMailResourceNotFound) {
		cipher, e := NewMailScopeSeed(m.s.deps.Cipher, m.key)
		if e != nil {
			return nil, e
		}
		if !create {
			return OpenMailScopeSeed(m.s.deps.Cipher, m.key, cipher)
		}
		row, err = m.store.EnsureMailScope(m.ctx, m.key, cipher)
	}
	if err != nil {
		return nil, err
	}
	return OpenMailScopeSeed(m.s.deps.Cipher, m.key, row.SeedCipher)
}
func (m *MailSite) ensureTemplates() error {
	return m.store.EnsureMailBuiltinTemplates(m.ctx, m.key, BuiltinMailTemplates(m.key))
}
func (m *MailSite) Overview(mode MailSecurityMode) (out MailOverview, err error) {
	if err = m.ensureTemplates(); err != nil {
		return
	}
	rows, err := m.store.GetMailOverview(m.ctx, m.key)
	if err != nil {
		return
	}
	out = MailOverview{SecurityMode: mode, Generation: m.key.Generation, CredGen: m.key.CredGen, TemplateCount: int(rows.TemplateCount), SuppressionCount: int(rows.SuppressionCount), CopyableSources: []MailCopySource{}}
	if rows.SMTP != nil {
		out.SMTPReady = true
		out.SMTPFromMasked = rows.SMTP.FromMasked
	}
	if rows.ActiveBatch != nil {
		v := batchView(*rows.ActiveBatch)
		out.ActiveBatch = &v
	}
	instances, err := m.s.deps.Store.ListInstances(m.ctx)
	if err != nil {
		return
	}
	for _, inst := range instances {
		if inst.ID == m.inst.ID || !inst.Enabled {
			continue
		}
		source, e := m.s.MailSite(m.ctx, inst.ID)
		if e != nil {
			return out, e
		}
		row, e := source.SMTP()
		if e != nil {
			return out, e
		}
		out.CopyableSources = append(out.CopyableSources, MailCopySource{InstanceID: inst.ID, Name: inst.Name, Generation: source.key.Generation, CredGen: source.key.CredGen, SMTPRevision: row.Revision})
	}
	return
}

func smtpView(r storage.SiteOpsMailSMTP) MailSMTPView {
	return MailSMTPView{Revision: r.Revision, Host: r.Host, Port: r.Port, TLSMode: MailTLSMode(r.TLSMode), AuthMode: MailAuthMode(r.AuthMode), FromName: r.FromName, FromMasked: r.FromMasked, HasUsername: r.HasUsername, HasPassword: r.HasPassword, ReplyTo: r.ReplyTo, UnsubscribeURL: r.UnsubscribeURL, RatePerMinute: r.RatePerMinute, MaxConcurrency: r.MaxConcurrency, ReconnectEvery: r.ReconnectEvery}
}
func (m *MailSite) SMTP() (MailSMTPView, error) {
	r, e := m.store.GetMailSMTP(m.ctx, m.key)
	if errors.Is(e, ErrMailResourceNotFound) {
		return MailSMTPView{Port: 465, TLSMode: MailTLSModeImplicit, AuthMode: MailAuthModeAuto, RatePerMinute: 30, MaxConcurrency: 1, ReconnectEvery: 50}, nil
	}
	return smtpView(r), e
}
func (m *MailSite) smtpSecrets(r storage.SiteOpsMailSMTP) (MailSMTPIdentity, string, error) {
	ident, e := OpenMailSMTPIdentity(m.s.deps.Cipher, r)
	if e != nil {
		return ident, "", e
	}
	if !r.HasPassword {
		return ident, "", nil
	}
	pw, e := OpenMailSMTPPassword(m.s.deps.Cipher, r, ident.Username)
	return ident, pw, e
}
func (m *MailSite) sealSMTP(r *storage.SiteOpsMailSMTP, ident MailSMTPIdentity, password string) error {
	guard, ok := m.s.deps.Validator.(SMTPOutboundValidator)
	if !ok || guard.ValidateSMTPHostPort(r.Host, r.Port) != nil {
		return ErrMailSMTPConfigBlocked
	}
	if r.UnsubscribeURL != "" {
		if !validMailLink(r.UnsubscribeURL) || len(r.UnsubscribeURL) > 512 {
			return ErrMailInvalidInput
		}
		dest, _ := url.Parse(r.UnsubscribeURL)
		base, e := url.Parse(m.inst.BaseURL)
		if dest.Scheme == "https" && (e != nil || !strings.EqualFold(dest.Host, base.Host)) {
			return ErrMailInvalidInput
		}
	}
	if r.ReplyTo != "" {
		if _, e := NormalizeBareEmail(r.ReplyTo); e != nil {
			return e
		}
	}
	if !mailHeaderSafe(r.FromName) || len(r.FromName) > 120 || !mailHeaderSafe(ident.Username) || strings.ContainsRune(password, 0) {
		return ErrMailInvalidInput
	}
	addr, e := NormalizeBareEmail(ident.FromEmail)
	if e != nil {
		return e
	}
	ident.FromEmail = addr
	if r.AuthMode == "none" {
		ident.Username = ""
		password = ""
	} else if ident.Username == "" || password == "" {
		return ErrMailInvalidInput
	}
	r.InstanceID, r.TargetBinding = m.key.InstanceID, m.key.TargetBinding
	r.FromMasked = *MaskEmailStrict(addr)
	r.HasUsername, r.HasPassword = ident.Username != "", password != ""
	r.IdentityCipher, e = SealMailSMTPIdentity(m.s.deps.Cipher, *r, ident)
	if e != nil {
		return e
	}
	r.PasswordCipher = ""
	if r.HasPassword {
		r.PasswordCipher, e = SealMailSMTPPassword(m.s.deps.Cipher, *r, ident.Username, password)
	}
	return e
}
func mailDefault(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}
func (m *MailSite) SaveSMTP(in SaveMailSMTPInput) (out MailSMTPView, err error) {
	if err = m.check(in.MailScopeInput); err != nil {
		return
	}
	if in.ExpectedRevision < 0 || in.ExpectedRevision >= MailMaxSafeID || !slices.Contains([]MailTLSMode{MailTLSModeImplicit, MailTLSModeStarttls}, in.TLSMode) || !slices.Contains([]MailAuthMode{MailAuthModeAuto, MailAuthModePlain, MailAuthModeLogin, MailAuthModeNone}, in.AuthMode) {
		return out, ErrMailInvalidInput
	}
	r := storage.SiteOpsMailSMTP{Revision: in.ExpectedRevision + 1, Host: strings.ToLower(in.Host), Port: in.Port, TLSMode: string(in.TLSMode), AuthMode: string(in.AuthMode), FromName: in.FromName, ReplyTo: in.ReplyTo, UnsubscribeURL: in.UnsubscribeURL, RatePerMinute: mailDefault(in.RatePerMinute, 30), MaxConcurrency: mailDefault(in.MaxConcurrency, 1), ReconnectEvery: mailDefault(in.ReconnectEvery, 50)}
	if r.RatePerMinute < 1 || r.RatePerMinute > 300 || r.MaxConcurrency < 1 || r.MaxConcurrency > 4 || r.ReconnectEvery < 1 || r.ReconnectEvery > 200 {
		return out, ErrMailInvalidInput
	}
	var ident MailSMTPIdentity
	var password string
	old, e := m.store.GetMailSMTP(m.ctx, m.key)
	if e != nil && !errors.Is(e, ErrMailResourceNotFound) {
		return out, e
	}
	if old.Revision != in.ExpectedRevision {
		return out, ErrMailRevisionMismatch
	}
	if old.Revision != 0 {
		ident, password, e = m.smtpSecrets(old)
		if e != nil {
			return out, e
		}
		if old.Host != r.Host || old.Port != r.Port || in.Username != nil && *in.Username != ident.Username {
			password = ""
		}
	}
	assignInput(&ident.Username, in.Username)
	assignInput(&ident.FromEmail, in.FromEmail)
	assignInput(&password, in.Password)
	if err = m.sealSMTP(&r, ident, password); err != nil {
		return
	}
	r, err = m.store.SaveMailSMTP(m.ctx, m.key, in.ExpectedRevision, r)
	if err == nil {
		m.s.interruptMail(m.inst.ID)
	}
	return smtpView(r), err
}
func (m *MailSite) copySource(in MailCopyInput) (*MailSite, storage.MailCopyParams, error) {
	var p storage.MailCopyParams
	if e := m.check(in.MailScopeInput); e != nil {
		return nil, p, e
	}
	if in.SourceInstanceID == m.key.InstanceID || in.SourceRevision < 1 || in.SourceRevision > MailMaxSafeID || in.ExpectedRevision < 0 || in.ExpectedRevision >= MailMaxSafeID {
		return nil, p, ErrMailInvalidInput
	}
	src, e := m.s.MailSite(m.ctx, in.SourceInstanceID)
	if e != nil {
		return nil, p, e
	}
	if e = src.check(MailScopeInput{in.SourceGeneration, in.SourceCredGen}); e != nil {
		return nil, p, e
	}
	p = storage.MailCopyParams{Source: src.key, Target: m.key, SourceRevision: in.SourceRevision, ExpectedRevision: in.ExpectedRevision}
	return src, p, nil
}
func (m *MailSite) CopySMTP(in MailCopyInput) (out MailSMTPView, err error) {
	if in.SourceTemplateID != 0 {
		return out, ErrMailInvalidInput
	}
	src, p, err := m.copySource(in)
	if err != nil {
		return
	}
	r, err := m.store.GetMailSMTP(m.ctx, src.key)
	if err != nil {
		return
	}
	if r.Revision != in.SourceRevision {
		return out, ErrMailRevisionMismatch
	}
	id, pw, err := src.smtpSecrets(r)
	if err != nil {
		return
	}
	r.Revision = in.ExpectedRevision + 1
	if err = m.sealSMTP(&r, id, pw); err != nil {
		return
	}
	r, err = m.store.CopyMailSMTP(m.ctx, storage.MailCopySMTPParams{MailCopyParams: p, TargetSMTP: r})
	if err == nil {
		m.s.interruptMail(m.inst.ID)
	}
	return smtpView(r), err
}

func templateView(r storage.SiteOpsMailTemplate) MailTemplateView {
	return MailTemplateView{MailTemplateSummary: MailTemplateSummary{ID: r.ID, Name: r.Name, Category: MailTemplateCategory(r.Category), Subject: r.Subject, IsBuiltIn: r.IsBuiltIn, Revision: r.Revision, UpdatedAt: r.UpdatedAt}, HTMLBody: r.HTMLBody, TextBody: r.TextBody}
}

type MailListQuery struct {
	Cursor uint
	Limit  int
	Status string
}

func (q MailListQuery) params(key storage.MailScopeKey) storage.MailListParams {
	return storage.MailListParams{Scope: key, BeforeID: q.Cursor, Limit: q.Limit + 1, Status: q.Status}
}
func mailPageView[R, V any](rows []R, limit int, id func(R) uint, view func(R) V) MailCursorPage[V] {
	out := MailCursorPage[V]{Items: make([]V, 0, min(len(rows), limit))}
	if len(rows) > limit {
		next := strconv.FormatUint(uint64(id(rows[limit-1])), 10)
		out.NextCursor = &next
		rows = rows[:limit]
	}
	for _, r := range rows {
		out.Items = append(out.Items, view(r))
	}
	return out
}
func (m *MailSite) Templates(q MailListQuery) (out MailCursorPage[MailTemplateSummary], err error) {
	if q.Limit <= 0 || q.Limit > MailMaxListLimit || uint64(q.Cursor) > MailMaxSafeID {
		return out, ErrMailInvalidInput
	}
	if err = m.ensureTemplates(); err != nil {
		return
	}
	rows, err := m.store.ListMailTemplates(m.ctx, q.params(m.key))
	if err != nil {
		return
	}
	return mailPageView(rows, q.Limit, func(r storage.SiteOpsMailTemplate) uint { return r.ID }, func(r storage.SiteOpsMailTemplate) MailTemplateSummary { return templateView(r).MailTemplateSummary }), nil
}
func (m *MailSite) Template(id uint) (out MailTemplateView, err error) {
	if err = m.ensureTemplates(); err != nil {
		return
	}
	r, err := m.store.GetMailTemplate(m.ctx, m.key, id)
	return templateView(r), err
}
func (m *MailSite) SaveTemplate(id uint, in SaveMailTemplateInput) (out MailTemplateView, err error) {
	if err = m.check(in.MailScopeInput); err != nil {
		return
	}
	if in.ExpectedRevision < 0 || in.ExpectedRevision >= MailMaxSafeID || in.Name == "" || len(in.Name) > 96 || !mailHeaderSafe(in.Name) || !slices.Contains([]MailTemplateCategory{MailTemplateCategoryMarketing, MailTemplateCategoryRenewal, MailTemplateCategoryReactivation, MailTemplateCategoryAnnouncement}, in.Category) {
		return out, ErrMailInvalidInput
	}
	clean, _, err := ValidateMailTemplate(MailTemplatePreviewInput{Subject: in.Subject, HTMLBody: in.HTMLBody, TextBody: in.TextBody})
	if err != nil {
		return
	}
	r, err := m.store.SaveMailTemplate(m.ctx, m.key, in.ExpectedRevision, storage.SiteOpsMailTemplate{ID: id, Name: in.Name, Category: string(in.Category), Subject: clean.Subject, HTMLBody: clean.HTMLBody, TextBody: clean.TextBody})
	return templateView(r), err
}
func (m *MailSite) DeleteTemplate(id uint, in MailScopeInput, revision int64) (MailDeletedView, error) {
	if e := m.check(in); e != nil {
		return MailDeletedView{}, e
	}
	e := m.store.DeleteMailTemplate(m.ctx, m.key, id, revision)
	return MailDeletedView{Deleted: e == nil}, e
}
func (m *MailSite) staticVars(now time.Time) MailTemplateVars {
	base := m.inst.BaseURL
	if !strings.HasPrefix(base, "https://") || !validMailLink(base) {
		base = ""
	}
	return MailTemplateVars{SiteName: m.inst.Name, SiteURL: base, TodayDate: now.In(time.FixedZone("UTC+8", 8*3600)).Format("2006-01-02")}
}
func (m *MailSite) PreviewTemplate(in MailTemplatePreviewInput) (MailTemplatePreview, error) {
	if e := m.check(in.MailScopeInput); e != nil {
		return MailTemplatePreview{}, e
	}
	vars := m.staticVars(time.Now())
	vars.UserID = "U#1042"
	vars.EmailMasked = "d***@example.com"
	vars.BalanceUSD = "$28.50"
	vars.PlanName = "示例套餐"
	vars.ExpiresDate = vars.TodayDate
	vars.ExpiresInDays = "0"
	r, e := RenderMailTemplate(in, vars)
	return r.MailTemplatePreview, e
}
func (m *MailSite) CopyTemplate(in MailCopyInput) (out MailTemplateView, err error) {
	src, p, err := m.copySource(in)
	if err != nil {
		return
	}
	if in.SourceTemplateID == 0 || uint64(in.SourceTemplateID) > MailMaxSafeID || in.ExpectedRevision != 0 {
		return out, ErrMailInvalidInput
	}
	r, err := m.store.GetMailTemplate(m.ctx, src.key, in.SourceTemplateID)
	if err != nil {
		return
	}
	if r.Revision != in.SourceRevision {
		return out, ErrMailRevisionMismatch
	}
	clean, _, err := ValidateMailTemplate(MailTemplatePreviewInput{Subject: r.Subject, HTMLBody: r.HTMLBody, TextBody: r.TextBody})
	if err != nil {
		return
	}
	if _, err = RenderMailTemplate(clean, m.staticVars(time.Now())); err != nil {
		return
	}
	r.HTMLBody = clean.HTMLBody
	r, err = m.store.CopyMailTemplate(m.ctx, storage.MailCopyTemplateParams{MailCopyParams: p, SourceTemplateID: r.ID, TargetTemplate: r})
	return templateView(r), err
}

func (m *MailSite) directory() (TargetEndpoint, error) {
	if !m.inst.Enabled {
		return TargetEndpoint{}, ErrMailInstanceDisabled
	}
	if m.s.deps.MailClient == nil {
		return TargetEndpoint{}, ErrMailPrepCoverageIncomplete
	}
	return m.s.targetForInstance(m.inst)
}
func (m *MailSite) Candidates(page int) (out MailCandidatePage, err error) {
	if page < 1 || page > 100 {
		return out, ErrMailInvalidInput
	}
	target, err := m.directory()
	if err != nil {
		return
	}
	r, _, err := m.s.deps.MailClient.FetchMailUsersPage(m.ctx, target, page, func(_ MailDirectoryUser, email string) (MailAddressProjection, error) {
		masked := MaskEmailStrict(email)
		if masked == nil {
			return MailAddressProjection{}, nil
		}
		return MailAddressProjection{EmailMasked: *masked}, nil
	})
	if err != nil {
		return
	}
	out = MailCandidatePage{Items: []MailCandidate{}, Page: page, PageSize: 100, Total: r.Total, HasMore: r.HasMore}
	if r.PageSize != nil {
		out.PageSize = *r.PageSize
	}
	for _, u := range r.Items {
		if u.ID > 0 && u.ID <= MailMaxSafeID && u.Address.EmailMasked != "" {
			out.Items = append(out.Items, MailCandidate{u.ID, u.Address.EmailMasked})
		}
	}
	return
}
func (m *MailSite) Groups() ([]MailGroupOption, error) {
	target, e := m.directory()
	if e != nil {
		return nil, e
	}
	rows, _, e := m.s.deps.MailClient.FetchAnalyticsGroups(context.WithValue(m.ctx, mailPrivateRequestKey{}, true), target)
	if e != nil {
		return nil, e
	}
	out := []MailGroupOption{}
	for _, r := range rows {
		if r.ID > 0 && r.ID <= MailMaxSafeID {
			out = append(out, MailGroupOption{r.ID, r.Name})
		}
	}
	return out, nil
}
func (m *MailSite) PreviewAudience(in MailAudiencePreviewInput) (out MailAudiencePreview, err error) {
	if err = m.check(in.MailScopeInput); err != nil {
		return
	}
	if err = ValidateMailAudience(in.Audience); err != nil {
		return
	}
	target, err := m.directory()
	if err != nil {
		return
	}
	seed, err := m.seed(false)
	if err != nil {
		return
	}
	r, err := ResolveMailAudience(m.ctx, m.s.deps.MailClient, MailAudienceOptions{Scope: m.key, Target: target, Seed: seed, Spec: in.Audience, AsOf: time.Now(), FindSuppressed: m.store.FindSuppressedMailHashes})
	r.Preview.Samples = emptySlice(r.Preview.Samples)
	return r.Preview, err
}

type mailSnapshot struct {
	Subject    string `json:"subject"`
	HTML       string `json:"html"`
	Text       string `json:"text"`
	SiteName   string `json:"site_name"`
	SiteURL    string `json:"site_url"`
	Date       string `json:"send_date"`
	FromMasked string `json:"from_masked,omitempty"`
}

func (m *MailSite) snapshot(subject, html, text, mask string, now time.Time) string {
	v := m.staticVars(now)
	b, _ := json.Marshal(mailSnapshot{subject, html, text, v.SiteName, v.SiteURL, v.TodayDate, mask})
	return string(b)
}
func sortedUnique[T cmp.Ordered](in []T) []T {
	v := slices.Clone(in)
	slices.Sort(v)
	return slices.Compact(v)
}
func canonicalAudience(a MailAudienceSpec) MailAudienceSpec {
	a.UserIDs = sortedUnique(a.UserIDs)
	a.GroupIDs = sortedUnique(a.GroupIDs)
	a.Tags = sortedUnique(a.Tags)
	a.ActivityStates = sortedUnique(a.ActivityStates)
	return a
}
func batchView(b storage.SiteOpsMailBatch) MailBatchView {
	return MailBatchView{ID: b.ID, ParentBatchID: b.ParentBatchID, Mode: MailBatchMode(b.Mode), Status: MailBatchStatus(b.Status), TemplateID: b.TemplateID, TemplateRevision: b.TemplateRevision, TemplateName: b.TemplateName, SMTPRevision: b.SMTPRevision, Pages: b.Pages, Scanned: b.Scanned, RecipientCount: b.RecipientCount, ExcludedCount: b.ExcludedCount, DuplicateCount: b.DuplicateCount, SuppressedCount: b.SuppressedCount, SentCount: b.SentCount, FailedCount: b.FailedCount, UncertainCount: b.UncertainCount, CancelledCount: b.CancelledCount, AbortReason: ErrorCode(b.AbortReason), CreatedAt: b.CreatedAt, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt, CancelledAt: b.CancelledAt, CanCancel: b.ActiveSlot != nil && b.Status != "cancelling", CanRetryFailed: b.ActiveSlot == nil && b.FailedCount > 0 && b.Mode != "test"}
}

type MailBatchError struct {
	Err               error
	ActiveBatchID     uint
	RetryAfterSeconds int
}

func (e *MailBatchError) Error() string { return e.Err.Error() }
func (e *MailBatchError) Unwrap() error { return e.Err }
func (m *MailSite) batchParams(scope MailScopeInput, request string, smtp int64, mode MailBatchMode, input any) (p storage.MailBatchCreateParams, err error) {
	if err = m.check(scope); err != nil {
		return
	}
	parsed, e := uuid.Parse(request)
	if e != nil || parsed.String() != request || smtp < 1 || smtp > MailMaxSafeID {
		return p, ErrMailInvalidInput
	}
	data, err := json.Marshal(input)
	if err != nil {
		return
	}
	hash := sha256.Sum256(append([]byte(string(mode)+"\x00"), data...))
	now := time.Now().UTC()
	return storage.MailBatchCreateParams{Scope: m.key, RequestID: request, RequestDigest: hex.EncodeToString(hash[:]), Mode: string(mode), SMTPRevision: smtp, Now: now, PrepareBy: now.Add(MailPrepareTimeout)}, nil
}
func (m *MailSite) submit(p storage.MailBatchCreateParams) (storage.MailBatchCreateResult, error) {
	if len(p.SnapshotJSON) > MailMaxSnapshotBytes {
		return storage.MailBatchCreateResult{}, ErrMailTemplateInvalid
	}
	r, e := m.store.CreateMailBatch(m.ctx, p)
	if e != nil {
		return r, &MailBatchError{e, r.ActiveBatchID, r.RetryAfterSeconds}
	}
	m.s.wakeMail()
	return r, nil
}
func (m *MailSite) CreateBatch(in CreateMailBatchInput) (out MailBatchView, replay bool, err error) {
	if err = ValidateMailAudience(in.Audience); err != nil {
		return
	}
	if in.TemplateID == 0 || uint64(in.TemplateID) > MailMaxSafeID || in.TemplateRevision < 1 || in.TemplateRevision > MailMaxSafeID {
		return out, false, ErrMailInvalidInput
	}
	in.Audience = canonicalAudience(in.Audience)
	p, err := m.batchParams(in.MailScopeInput, in.RequestID, in.SMTPRevision, MailBatchMode(in.Audience.Mode), in)
	if err != nil {
		return
	}
	t, err := m.store.GetMailTemplate(m.ctx, m.key, in.TemplateID)
	// A deleted template may still have a replay. Revision -1 prevents a fresh insert.
	if err != nil && !errors.Is(err, ErrMailResourceNotFound) {
		return
	}
	p.TemplateID, p.TemplateRevision, p.TemplateName = in.TemplateID, in.TemplateRevision, t.Name
	if t.Revision != in.TemplateRevision {
		p.TemplateRevision = -1
	}
	p.SnapshotJSON = m.snapshot(t.Subject, t.HTMLBody, t.TextBody, "", p.Now)
	a, _ := json.Marshal(in.Audience)
	p.AudienceJSON = string(a)
	r, err := m.submit(p)
	return batchView(r.Batch), r.IdempotentReplay, err
}
func (m *MailSite) RetryBatch(id uint, in RetryMailBatchInput) (out MailBatchView, replay bool, err error) {
	p, err := m.batchParams(in.MailScopeInput, in.RequestID, in.SMTPRevision, MailBatchModeRetryFailed, struct {
		Parent uint
		RetryMailBatchInput
	}{id, in})
	if err != nil {
		return
	}
	parent, err := m.store.GetMailBatch(m.ctx, m.key, id)
	if err != nil {
		return
	}
	p.ParentBatchID = &id
	p.SnapshotJSON = parent.SnapshotJSON
	p.TemplateID, p.TemplateRevision, p.TemplateName = parent.TemplateID, parent.TemplateRevision, parent.TemplateName
	p.AudienceJSON = `{"mode":"retry_failed"}`
	r, err := m.submit(p)
	return batchView(r.Batch), r.IdempotentReplay, err
}
func (m *MailSite) TestSMTP(in MailTestInput) (out MailTestResult, done bool, err error) {
	p, err := m.batchParams(in.MailScopeInput, in.RequestID, in.ExpectedRevision, MailBatchModeTest, in)
	if err != nil {
		return
	}
	smtp, err := m.SMTP()
	if err != nil {
		return
	}
	if smtp.Revision == 0 {
		return out, false, ErrMailSMTPNotReady
	}
	p.SnapshotJSON = m.snapshot("SMTP 测试", "<p>测试邮件已送达。</p>", "", smtp.FromMasked, p.Now)
	r, err := m.submit(p)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, MailTestWait)
	defer cancel()
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	b := r.Batch
	var snap mailSnapshot
	if json.Unmarshal([]byte(b.SnapshotJSON), &snap) != nil {
		return out, false, ErrMailTemplateInvalid
	}
	for {
		out = MailTestResult{BatchID: b.ID, Status: MailBatchStatus(b.Status), FromMasked: snap.FromMasked, ErrorCode: ErrorCode(b.AbortReason)}
		if b.ActiveSlot == nil {
			if m.s.deps.mailEvidence != nil {
				err = m.s.deps.mailEvidence(m.ctx, m.key, b.ID, &out)
			}
			return out, true, err
		}
		select {
		case <-ctx.Done():
			return out, false, m.ctx.Err()
		case <-tick.C:
		}
		b, err = m.store.GetMailBatch(ctx, m.key, b.ID)
		if err != nil {
			if ctx.Err() != nil {
				return out, false, m.ctx.Err()
			}
			return
		}
	}
}
func (m *MailSite) Batch(id uint) (MailBatchView, error) {
	b, err := m.store.GetMailBatch(m.ctx, m.key, id)
	if err != nil {
		return MailBatchView{}, err
	}
	views := []MailBatchView{batchView(b)}
	err = m.checkRetryChildren(views)
	return views[0], err
}

// A retry child has a larger ID than its parent, even when it is outside this page.
func (m *MailSite) checkRetryChildren(views []MailBatchView) error {
	candidates := map[uint]int{}
	var oldest uint
	for i, v := range views {
		if v.CanRetryFailed {
			candidates[v.ID] = i
			if oldest == 0 || v.ID < oldest {
				oldest = v.ID
			}
		}
	}
	p := storage.MailListParams{Scope: m.key, Limit: MailMaxListLimit}
	for len(candidates) > 0 {
		rows, e := m.store.ListMailBatches(m.ctx, p)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.ID <= oldest {
				return nil
			}
			if r.ParentBatchID != nil {
				if i, ok := candidates[*r.ParentBatchID]; ok {
					views[i].CanRetryFailed = false
					delete(candidates, *r.ParentBatchID)
				}
			}
		}
		if len(rows) < p.Limit {
			return nil
		}
		p.BeforeID = rows[len(rows)-1].ID
	}
	return nil
}
func (m *MailSite) Batches(q MailListQuery) (out MailCursorPage[MailBatchView], err error) {
	if q.Limit <= 0 || q.Limit > MailMaxBatchListLimit || uint64(q.Cursor) > MailMaxSafeID {
		return out, ErrMailInvalidInput
	}
	p := q.params(m.key)
	var filtered []storage.SiteOpsMailBatch
	for len(filtered) < q.Limit+1 {
		rows, e := m.store.ListMailBatches(m.ctx, p)
		if e != nil {
			return out, e
		}
		for _, r := range rows {
			if r.Mode != "test" {
				filtered = append(filtered, r)
			}
		}
		if len(rows) < p.Limit {
			break
		}
		p.BeforeID = rows[len(rows)-1].ID
	}
	out = mailPageView(filtered, q.Limit, func(b storage.SiteOpsMailBatch) uint { return b.ID }, batchView)
	return out, m.checkRetryChildren(out.Items)
}
func (m *MailSite) Items(id uint, q MailListQuery) (out MailCursorPage[MailItemView], err error) {
	if q.Limit <= 0 || q.Limit > MailMaxListLimit || uint64(q.Cursor) > MailMaxSafeID {
		return out, ErrMailInvalidInput
	}
	rows, err := m.store.ListMailItems(m.ctx, id, q.params(m.key))
	if err != nil {
		return
	}
	return mailPageView(rows, q.Limit, func(i storage.SiteOpsMailItem) uint { return i.ID }, func(i storage.SiteOpsMailItem) MailItemView {
		return MailItemView{ID: i.ID, UserID: i.UserID, EmailMasked: i.EmailMasked, Status: MailItemStatus(i.Status), Phase: MailItemPhase(i.Phase), SMTPStage: MailSMTPStage(i.SMTPStage), SMTPCode: i.SMTPCode, ErrorCode: ErrorCode(i.ErrorCode), HardBounceHint: i.SMTPStage == "rcpt_to" && i.SMTPCode >= 550 && i.SMTPCode <= 554}
	}), nil
}
func (m *MailSite) CancelBatch(id uint, in MailScopeInput) (out MailBatchView, err error) {
	if err = m.check(in); err != nil {
		return
	}
	r, err := m.store.CancelMailBatch(m.ctx, m.key, id, time.Now())
	if err == nil {
		m.s.interruptMailBatch(m.inst.ID, id)
	}
	return batchView(r.Batch), err
}
func (m *MailSite) Suppressions(q MailListQuery) (out MailCursorPage[MailSuppressionView], err error) {
	if q.Limit <= 0 || q.Limit > MailMaxListLimit || uint64(q.Cursor) > MailMaxSafeID {
		return out, ErrMailInvalidInput
	}
	rows, err := m.store.ListMailSuppressions(m.ctx, q.params(m.key))
	if err != nil {
		return
	}
	return mailPageView(rows, q.Limit, func(r storage.SiteOpsMailSuppression) uint { return r.ID }, func(r storage.SiteOpsMailSuppression) MailSuppressionView {
		return MailSuppressionView{r.ID, r.EmailMasked, MailSuppressionReason(r.Reason), r.CreatedAt}
	}), nil
}
func (m *MailSite) AddMailSuppressions(in AddMailSuppressionInput) (out MailSuppressionAddResult, err error) {
	if err = m.check(in.MailScopeInput); err != nil {
		return
	}
	if in.Reason == "" {
		in.Reason = MailSuppressionReasonAdminAdded
	}
	if !slices.Contains([]MailSuppressionReason{MailSuppressionReasonAdminAdded, MailSuppressionReasonUserUnsubscribed, MailSuppressionReasonHardBounce}, in.Reason) || (in.ItemID != 0) == (len(in.Emails) > 0) || len(in.Emails) > 50 || uint64(in.ItemID) > MailMaxSafeID {
		return out, ErrMailInvalidInput
	}
	if in.ItemID != 0 {
		added, e := m.store.AddMailSuppressionFromItem(m.ctx, m.key, in.ItemID, string(in.Reason))
		if added {
			out.Added = 1
		} else {
			out.Existing = 1
		}
		return out, e
	}
	for _, email := range in.Emails {
		if _, err = NormalizeBareEmail(email); err != nil {
			return
		}
	}
	seed, err := m.seed(true)
	if err != nil {
		return
	}
	rows := make([]storage.MailSuppressionEntry, 0, len(in.Emails))
	for _, email := range in.Emails {
		addr, e := ProjectMailItem(nil, m.key, seed, 0, 0, email, MailPersonalVars{})
		if e != nil {
			return out, e
		}
		rows = append(rows, storage.MailSuppressionEntry{EmailHash: addr.EmailHash, EmailMasked: addr.EmailMasked, Reason: string(in.Reason)})
	}
	out.Added, err = m.store.AddMailSuppressions(m.ctx, m.key, rows)
	out.Existing = len(rows) - out.Added
	return
}
func (m *MailSite) DeleteSuppression(id uint, in MailScopeInput) (MailDeletedView, error) {
	if e := m.check(in); e != nil {
		return MailDeletedView{}, e
	}
	e := m.store.DeleteMailSuppression(m.ctx, m.key, id)
	return MailDeletedView{Deleted: e == nil}, e
}

// Only allow known domain codes across the persistence and HTTP boundaries.
func MailPublicErrorCode(err error) ErrorCode {
	for _, entry := range mailErrorCodes {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}
	return ErrCodeMailInternal
}

var mailErrorCodes = []struct {
	err  error
	code ErrorCode
}{
	{ErrMailSMTPNotReady, ErrCodeMailSMTPNotReady},
	{ErrMailResourceNotFound, ErrCodeMailResourceNotFound},
	{ErrMailTemplateInvalid, ErrCodeMailTemplateInvalid},
	{ErrMailCryptoUnavailable, ErrCodeMailCryptoUnavailable},
	{ErrMailInvalidInput, ErrCodeMailInvalidInput},
	{ErrMailPrepCoverageIncomplete, ErrCodeMailPrepCoverageIncomplete},
	{ErrMailPrepLimitExceeded, ErrCodeMailPrepLimitExceeded},
	{ErrMailPrepPageDrift, ErrCodeMailPrepPageDrift},
	{ErrMailPrepDeadlineExceeded, ErrCodeMailPrepDeadlineExceeded},
	{ErrMailSMTPConfigBlocked, ErrCodeMailSMTPConfigBlocked},
	{ErrMailSMTPRejected, ErrCodeMailSMTPRejected},
	{ErrMailSMTPUnavailable, ErrCodeMailSMTPUnavailable},
	{ErrMailDeliveryUncertain, ErrCodeMailDeliveryUncertain},
	{ErrMailActiveBatchExists, ErrCodeMailActiveBatchExists},
	{ErrMailIdempotencyConflict, ErrCodeMailIdempotencyConflict},
	{ErrMailRevisionMismatch, ErrCodeMailRevisionMismatch},
	{ErrMailRetryAlreadyExists, ErrCodeMailRetryAlreadyExists},
	{ErrMailNoRetryableItems, ErrCodeMailNoRetryableItems},
	{ErrMailTemplateLimitReached, ErrCodeMailTemplateLimitReached},
	{ErrMailTemplateBuiltinProtected, ErrCodeMailTemplateBuiltinProtected},
	{ErrMailTestRateLimited, ErrCodeMailTestRateLimited},
	{ErrMailBatchNotCancellable, ErrCodeMailBatchNotCancellable},
	{ErrMailInstanceDisabled, ErrCodeMailInstanceDisabled},
	{ErrMailClaimLost, ErrCodeMailClaimLost},
	{ErrMailLeaseLost, ErrCodeMailLeaseLost},
	{ErrMailGenerationMismatch, ErrCodeMailGenerationMismatch},
	{ErrMailAuthRequired, ErrCodeMailAuthRequired},
	{ErrBudgetExceeded, ErrCodeMailBudgetExceeded}, {ErrRateLimited, ErrCodeMailRateLimited}, {ErrInstanceNotFound, ErrCodeMailResourceNotFound},
}

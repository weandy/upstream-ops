package siteops

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	authRetryInterval       = 15 * time.Minute
	capabilityRetryWindow   = 6 * time.Hour
	liveGroupsTTL           = 60 * time.Second
	liveAccountsTTL         = 10 * time.Minute
	metadataRefreshPeriod   = 5 * time.Minute
	l2RefreshPeriod         = 5 * time.Minute
	l3AccountsRefreshPeriod = 30 * time.Minute
	deliveryRetryTick       = 30 * time.Second
	dailyReportCheckTick    = time.Minute
)

type PollerDeps struct {
	Store         Store
	EventStore    EventStore
	Notifier      DirectNotifier
	Cipher        *crypto.Cipher
	Validator     OutboundValidator
	Client        UpstreamClient
	Prober        Prober
	Log           *slog.Logger
	InitialConfig config.SiteOpsConfig
	Now           func() time.Time
	Jitter        func(time.Duration) time.Duration
	SanitizeError func(string, int) string
}

type Poller struct {
	deps      PollerDeps
	lifecycle sync.Mutex
	mu        sync.Mutex
	wg        sync.WaitGroup

	cfg       config.SiteOpsConfig
	running   bool
	allowed   bool
	bgCancel  context.CancelFunc
	instances map[uint]storage.SiteOpsInstance
	workers   map[uint]*instanceWorker
	tails     map[uint]chan struct{}
	security  map[uint]pollSecurity
	live      map[uint]liveGroupSample
	accounts  map[uint]liveAccountsSample
	evaluator *EventEvaluator
	alerter   *AlertDispatcher
	dailySent map[uint]string
}

type liveGroupSample struct {
	response GroupsResponse
	cachedAt time.Time
}

type liveAccountsSample struct {
	response AccountsStatusResponse
	cachedAt time.Time
}

type pollSecurity struct {
	unauthorized int
	authRetryAt  time.Time
	backoff      time.Duration
}

type pollAction uint8

const (
	actionRefresh pollAction = iota
	actionProbe
)

type pollRequest struct {
	done      chan struct{}
	cancel    context.CancelFunc
	waiters   int
	automatic bool
	report    *ProbeReport
	err       error
}

type instanceWorker struct {
	poller   *Poller
	instance storage.SiteOpsInstance
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	requests map[pollAction]*pollRequest // Protected by Poller.mu.

	initialized   bool
	caps          CapabilitiesMap
	security      pollSecurity
	last          *storage.SiteOpsSnapshot
	trusted       *storage.SiteOpsSnapshot
	trustedGroups bool
	lastArchive     time.Time
	groupMetadata   []SanitizedGroupInfo
	lastL2At        time.Time
	lastAccountsAt  time.Time
	authCacheHealth *SanitizedAuthCacheHealth
	ingressHealth   *SanitizedIngressRejectHealth
	accountsDir     []SanitizedAccountSummary
}

func NewPoller(deps PollerDeps) *Poller {
	if deps.EventStore == nil && deps.Store != nil {
		if es, ok := deps.Store.(EventStore); ok {
			deps.EventStore = es
		}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Jitter == nil {
		deps.Jitter = func(interval time.Duration) time.Duration {
			return rand.N(min(interval/4, 15*time.Second))
		}
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	var evaluator *EventEvaluator
	var alerter *AlertDispatcher
	if deps.EventStore != nil {
		evaluator = NewEventEvaluator()
		if deps.Notifier != nil {
			alerter = NewAlertDispatcher(deps.EventStore, deps.Notifier)
			alerter.Now = func() time.Time { return deps.Now().UTC() }
		}
	}
	return &Poller{
		deps: deps, cfg: deps.InitialConfig.WithDefaults(), allowed: true,
		instances: make(map[uint]storage.SiteOpsInstance), workers: make(map[uint]*instanceWorker),
		tails: make(map[uint]chan struct{}), security: make(map[uint]pollSecurity),
		live: make(map[uint]liveGroupSample), accounts: make(map[uint]liveAccountsSample),
		evaluator: evaluator, alerter: alerter, dailySent: make(map[uint]string),
	}
}

// SetNotifier 运行时绑定或更新专用直投通知器。
func (p *Poller) SetNotifier(notifier DirectNotifier) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deps.Notifier = notifier
	if p.deps.EventStore != nil && notifier != nil {
		ad := NewAlertDispatcher(p.deps.EventStore, notifier)
		ad.Now = p.now
		p.alerter = ad
	} else {
		p.alerter = nil
	}
}

func (p *Poller) Start() {
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return
	}
	p.running = true
	if p.deps.EventStore != nil {
		bgCtx, bgCancel := context.WithCancel(context.Background())
		p.bgCancel = bgCancel
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.runMaintenance(bgCtx)
		}()
	}
	for _, inst := range p.instances {
		p.startWorkerLocked(inst)
	}
}

func (p *Poller) Stop() {
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	p.running = false
	if p.bgCancel != nil {
		p.bgCancel()
		p.bgCancel = nil
	}
	for id := range p.workers {
		p.stopWorkerLocked(id)
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Poller) SetPollingAllowed(allowed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setPollingAllowedLocked(allowed)
}

func (p *Poller) setPollingAllowedLocked(allowed bool) {
	if p.allowed == allowed {
		return
	}
	p.allowed = allowed
	if allowed {
		for _, inst := range p.instances {
			p.startWorkerLocked(inst)
		}
		return
	}
	for id := range p.workers {
		p.stopWorkerLocked(id)
	}
}

func (p *Poller) applyRuntimeConfig(allowed bool, cfg config.SiteOpsConfig, proxy config.ProxyConfig, upstream config.UpstreamConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !allowed {
		p.setPollingAllowedLocked(false)
	}
	p.cfg = cfg
	if p.deps.Validator != nil {
		p.deps.Validator.UpdatePolicy(cfg.AllowLoopback, cfg.AllowedOrigins, cfg.AllowedCIDRs)
	}
	if p.deps.Client != nil {
		p.deps.Client.UpdateRuntimeConfig(cfg, proxy, upstream)
	}
	p.setPollingAllowedLocked(allowed)
}

func (p *Poller) SyncInstances(instances []storage.SiteOpsInstance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[uint]bool, len(instances))
	for _, inst := range instances {
		seen[inst.ID] = true
		p.reconcileLocked(inst)
	}
	for id := range p.instances {
		if !seen[id] {
			p.removeLocked(id)
		}
	}
}

func (p *Poller) ReconcileInstance(inst storage.SiteOpsInstance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reconcileLocked(inst)
}

func (p *Poller) reconcileLocked(inst storage.SiteOpsInstance) {
	previous, exists := p.instances[inst.ID]
	if exists && inst.Generation <= previous.Generation {
		return
	}
	if exists {
		p.stopWorkerLocked(inst.ID)
		if credentialTargetChanged(previous, inst) {
			delete(p.security, inst.ID)
			if p.evaluator != nil {
				p.evaluator.ResetInstance(inst.ID)
			}
		}
	}
	p.instances[inst.ID] = inst
	p.startWorkerLocked(inst)
}

func credentialTargetChanged(previous, current storage.SiteOpsInstance) bool {
	return previous.CredGen != current.CredGen || previous.BaseURL != current.BaseURL ||
		previous.KeyKind != current.KeyKind || previous.ProxyEnabled != current.ProxyEnabled
}

func (p *Poller) RemoveInstance(id uint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removeLocked(id)
}

func (p *Poller) removeLocked(id uint) {
	p.stopWorkerLocked(id)
	delete(p.instances, id)
	delete(p.security, id)
	delete(p.dailySent, id)
	if p.evaluator != nil {
		p.evaluator.ResetInstance(id)
	}
}

func (p *Poller) stopWorkerLocked(id uint) {
	if worker := p.workers[id]; worker != nil {
		worker.cancel()
		delete(p.workers, id)
	}
	delete(p.live, id)
	delete(p.accounts, id)
	if p.deps.Client != nil {
		p.deps.Client.ResetInstanceState(id)
	}
}

func (p *Poller) startWorkerLocked(inst storage.SiteOpsInstance) {
	if !p.running || !p.allowed || !inst.Enabled || p.deps.Client == nil || p.workers[inst.ID] != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &instanceWorker{poller: p, instance: inst, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), requests: make(map[pollAction]*pollRequest), security: p.security[inst.ID]}
	previous, done := p.tails[inst.ID], make(chan struct{})
	p.tails[inst.ID], p.workers[inst.ID] = done, w
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		// A cancelled generation may still be unwinding a request. Keep the
		// chain intact even if this generation is itself replaced while waiting.
		if previous != nil {
			<-previous
		}
		if w.ctx.Err() == nil {
			w.run()
		}
		p.mu.Lock()
		if p.tails[inst.ID] == done {
			delete(p.tails, inst.ID)
		}
		close(done)
		p.mu.Unlock()
	}()
}

func (p *Poller) TriggerRefresh(ctx context.Context, id uint) error {
	_, err := p.request(ctx, id, actionRefresh)
	return err
}

func (p *Poller) request(ctx context.Context, id uint, action pollAction) (*ProbeReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if !p.allowed {
		p.mu.Unlock()
		return nil, ErrAuthRequired
	}
	if _, exists := p.instances[id]; !exists {
		p.mu.Unlock()
		return nil, ErrInstanceNotFound
	}
	w := p.workers[id]
	if w == nil {
		p.mu.Unlock()
		return nil, ErrInvalidInput
	}
	req := w.requests[action]
	if req == nil {
		req = &pollRequest{done: make(chan struct{})}
		w.requests[action] = req
		w.signal()
	}
	req.waiters++
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		p.mu.Lock()
		req.waiters--
		if !req.automatic && req.waiters == 0 && w.requests[action] == req {
			delete(w.requests, action)
			if req.cancel != nil {
				req.cancel()
			}
		}
		p.mu.Unlock()
		return nil, ctx.Err()
	case <-w.ctx.Done():
		return nil, ErrGenerationMismatch
	case <-req.done:
		if req.report == nil {
			return nil, req.err
		}
		report := *req.report
		report.Capabilities = maps.Clone(report.Capabilities)
		return &report, req.err
	}
}

func (w *instanceWorker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *instanceWorker) run() {
	p := w.poller
	p.mu.Lock()
	interval := w.instance.PollIntervalSeconds
	if interval < config.MinSiteOpsPollIntervalSeconds {
		interval = p.cfg.DefaultPollIntervalSeconds
	}
	p.mu.Unlock()
	period := time.Duration(interval) * time.Second
	timer := time.NewTimer(p.deps.Jitter(period))
	defer timer.Stop()
	for w.ctx.Err() == nil {
		select {
		case <-w.ctx.Done():
			return
		case <-timer.C:
			w.execute(actionRefresh, true)
			timer.Reset(period)
		case <-w.wake:
			w.execute(actionProbe, false)
			w.execute(actionRefresh, false)
		}
	}
}

func (w *instanceWorker) execute(action pollAction, automatic bool) {
	p := w.poller
	p.mu.Lock()
	if w.ctx.Err() != nil {
		p.mu.Unlock()
		return
	}
	req := w.requests[action]
	if req == nil {
		if !automatic {
			p.mu.Unlock()
			return
		}
		req = &pollRequest{done: make(chan struct{})}
		w.requests[action] = req
	}
	ctx, cancel := context.WithCancel(w.ctx)
	req.cancel, req.automatic = cancel, automatic
	cfg := p.cfg
	p.mu.Unlock()

	batch, endBatch := context.WithTimeout(ctx, time.Duration(cfg.BatchTimeoutSeconds)*time.Second)
	err := w.initialize(batch)
	var report *ProbeReport
	if err == nil {
		if action == actionProbe {
			report, err = w.probe(ctx, batch)
		} else {
			err = w.collect(ctx, batch, time.Duration(cfg.GroupArchiveIntervalSeconds)*time.Second)
		}
	}
	endBatch()
	if automatic && err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrGenerationMismatch) && !errors.Is(err, ErrInstanceNotFound) {
		p.deps.Log.Warn("站点运维采集未能保存", "instance_id", w.instance.ID, "code", siteOpsErrorCode(err))
	}

	p.mu.Lock()
	req.report, req.err = report, err
	if w.requests[action] == req {
		delete(w.requests, action)
	}
	close(req.done)
	p.mu.Unlock()
	cancel()
}

func (w *instanceWorker) initialize(ctx context.Context) error {
	if w.initialized {
		return nil
	}
	store := w.poller.deps.Store
	inst, err := store.GetInstance(ctx, w.instance.ID)
	if err != nil {
		return err
	}
	if inst.Generation != w.instance.Generation || !inst.Enabled {
		return ErrGenerationMismatch
	}
	if inst.LastStatus == string(StatusAuthError) && w.security.unauthorized < 2 {
		w.security.unauthorized = 2
		w.security.authRetryAt = w.poller.now().Add(authRetryInterval)
	}
	if inst.LastObservedAt != nil {
		w.last, err = store.GetLatestSnapshot(ctx, inst.ID)
		if err != nil {
			return err
		}
		groups, err := store.GetLatestGroupSnapshots(ctx, inst.ID)
		if err != nil {
			return err
		}
		if len(groups) > 0 {
			w.lastArchive = groups[0].ObservedAt
		}
		for _, group := range groups {
			w.trustedGroups = w.trustedGroups || (group.TrustState == string(QualityTrusted) && group.AccountsTotal > 0)
		}
		if w.last != nil && w.last.ConcTrustState == string(QualityTrusted) {
			w.trusted = w.last
		} else if w.last != nil {
			rows, err := store.ListSnapshotsSince(ctx, inst.ID, w.poller.now().Add(-14*24*time.Hour), 0)
			if err != nil {
				return err
			}
			for i := len(rows) - 1; i >= 0; i-- {
				if rows[i].ConcTrustState == string(QualityTrusted) {
					w.trusted = &rows[i]
					break
				}
			}
		}
	}
	w.instance, w.caps, w.initialized = *inst, decodeCapabilities(inst.CapabilitiesJSON), true
	return nil
}

func (p *Poller) now() time.Time { return p.deps.Now().UTC() }

func (w *instanceWorker) target() (TargetEndpoint, error) {
	inst, deps := w.instance, w.poller.deps
	target := TargetEndpoint{InstanceID: inst.ID, BaseURL: inst.BaseURL, KeyKind: inst.KeyKind,
		CredGen: inst.CredGen, Generation: inst.Generation, ProxyEnabled: inst.ProxyEnabled, AuditBudgetPerDay: inst.AuditBudgetPerDay}
	if deps.Validator == nil {
		return target, ErrOriginNotAllowed
	}
	url, err := deps.Validator.ValidateBaseURL(inst.BaseURL)
	if err != nil || url == "" {
		return target, ErrOriginNotAllowed
	}
	target.BaseURL = url
	return target, nil
}

func (w *instanceWorker) unlockTarget(target *TargetEndpoint) error {
	cipher := w.poller.deps.Cipher
	if cipher == nil {
		return ErrUnauthorized
	}
	key, err := cipher.Decrypt(w.instance.APIKeyCipher)
	if err != nil || key == "" {
		return ErrUnauthorized
	}
	target.APIKey = key
	return nil
}

type collectionRound struct {
	w             *instanceWorker
	now           time.Time
	patch         InstanceRuntimePatch
	caps          CapabilitiesMap
	security      pollSecurity
	backoff       *time.Time
	adminErr      error
	haltAdmin     bool
	authenticated bool
}

func (w *instanceWorker) newRound() *collectionRound {
	return &collectionRound{w: w, now: w.poller.now(), caps: maps.Clone(w.caps), security: w.security, backoff: w.instance.BackoffUntil}
}

func (r *collectionRound) noteAdmin(meta UpstreamFetchMeta, err error) {
	if err == nil || errors.Is(err, ErrForbidden) || errors.Is(err, ErrOpsDisabled) || errors.Is(err, ErrUnsupportedRoute) {
		r.security.unauthorized, r.security.authRetryAt = 0, time.Time{}
		r.authenticated = true
		return
	}
	if r.adminErr == nil {
		r.adminErr = err
	}
	switch {
	case errors.Is(err, ErrUnauthorized):
		r.security.unauthorized = min(r.security.unauthorized+1, 2)
		if r.security.unauthorized == 2 {
			r.security.authRetryAt = r.now.Add(authRetryInterval)
		}
		r.adminErr, r.haltAdmin = err, true
	case errors.Is(err, ErrRateLimited):
		var delay time.Duration
		if meta.RetryAfter != nil && *meta.RetryAfter >= 0 {
			delay = *meta.RetryAfter
		} else {
			r.security.backoff = max(30*time.Second, min(r.security.backoff*2, 10*time.Minute))
			delay = r.security.backoff
		}
		r.backoff = ptr(r.w.poller.now().Add(delay))
		r.adminErr, r.haltAdmin = err, true
	case errors.Is(err, ErrBudgetExceeded), errors.Is(err, ErrQueueTimeout), errors.Is(err, context.DeadlineExceeded):
		r.haltAdmin = true
	}
}

func (r *collectionRound) capability(endpoint string, meta UpstreamFetchMeta, err error) {
	state := CapabilitySupported
	switch {
	case errors.Is(err, ErrForbidden):
		state = CapabilityForbidden
	case errors.Is(err, ErrOpsDisabled):
		state = CapabilityOpsDisabled
	case errors.Is(err, ErrUnsupportedRoute):
		state = CapabilityUnsupported
	case err != nil:
		state = CapabilityError
	}
	r.caps[endpoint] = EndpointCapability{State: state, HTTPStatus: meta.HTTPStatus, CheckedAt: r.now,
		FallbackTarget: approvedFallback(meta.FallbackUsed), Reason: r.w.poller.diagnostic(err, meta.SanitizedError, meta.HTTPStatus)}
}

func (r *collectionRound) skip(endpoint string) bool {
	cap := r.caps[endpoint]
	switch cap.State {
	case CapabilityForbidden, CapabilityOpsDisabled, CapabilityUnsupported:
		return r.now.Sub(cap.CheckedAt) < capabilityRetryWindow
	default:
		return false
	}
}

func qualityFor(err error) FieldQualityState {
	switch {
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrUnauthorized):
		return QualityUnauthorized
	case errors.Is(err, ErrOpsDisabled):
		return QualityDisabled
	default:
		return QualityUnavailable
	}
}

func capabilityQuality(cap EndpointCapability) FieldQualityState {
	switch cap.State {
	case CapabilityForbidden:
		return QualityUnauthorized
	case CapabilityOpsDisabled:
		return QualityDisabled
	default:
		return QualityUnavailable
	}
}

// roundFetch keeps capability updates and admin throttling at the per-call boundary.
func roundFetch[T any](r *collectionRound, ctx context.Context, endpoint, domain string, quality FieldQualityMap, fetch func() (T, UpstreamFetchMeta, error)) (T, UpstreamFetchMeta) {
	var empty T
	if r.haltAdmin || ctx.Err() != nil {
		quality[domain] = qualityFor(r.adminErr)
		return empty, UpstreamFetchMeta{}
	}
	if r.skip(endpoint) {
		quality[domain] = capabilityQuality(r.caps[endpoint])
		return empty, UpstreamFetchMeta{}
	}
	value, meta, err := fetch()
	if err == nil {
		switch meta.HTTPStatus {
		case 401:
			err = ErrUnauthorized
		case 403:
			err = ErrForbidden
		case 429:
			err = ErrRateLimited
		}
	}
	r.noteAdmin(meta, err)
	r.capability(endpoint, meta, err)
	if err != nil {
		quality[domain] = qualityFor(err)
		return empty, meta
	}
	return value, meta
}

func (w *instanceWorker) collect(commitCtx, batch context.Context, archiveInterval time.Duration) error {
	r := w.newRound()
	client := w.poller.deps.Client
	in := AnalyzeInput{InstanceID: w.instance.ID, Now: r.now, FieldQuality: make(FieldQualityMap),
		PreviousTrusted: w.trusted, PreviousLiveGroups: w.trustedGroups}
	target, err := w.target()
	if err == nil {
		in.Health, err = client.ProbeHealth(batch, target)
		if in.Health != nil {
			r.capability(EndpointHealth, UpstreamFetchMeta{HTTPStatus: in.Health.HTTPStatus, SanitizedError: in.Health.SanitizedError}, err)
		}
	}
	if err != nil {
		r.recordError(err)
	}
	status := StatusOffline
	if in.Health != nil && in.Health.Reachable {
		status = StatusOnline
	}
	var overviewMeta UpstreamFetchMeta
	if status == StatusOnline && batch.Err() == nil {
		overviewMeta = r.collectAdmin(batch, target, &in)
	}
	if status == StatusOnline && (in.Overview == nil || r.adminErr != nil ||
		(in.Overview.OpsEnabled != nil && !*in.Overview.OpsEnabled) ||
		(in.Overview.DBOK != nil && !*in.Overview.DBOK) || (in.Overview.RedisOK != nil && !*in.Overview.RedisOK)) {
		status = StatusDegraded
	}
	if r.security.unauthorized >= 2 {
		status = StatusAuthError
	}
	if in.Version != nil && in.Version.Version != "" {
		r.patch.LastVersion = &in.Version.Version
	} else if in.FieldQuality[DomainSystemVersion] == "" && in.Health != nil && in.Health.Reachable && r.adminErr == nil {
		version := r.caps[EndpointSystemVersion]
		in.FieldQuality[DomainSystemVersion] = capabilityQuality(version)
		if version.State == CapabilitySupported && w.instance.LastVersion != "" {
			in.FieldQuality[DomainSystemVersion] = QualityTrusted
		}
	}
	if r.adminErr != nil {
		r.recordError(r.adminErr)
	}
	r.setStatus(status, in.Health)
	if notModified(overviewMeta) && (w.last != nil || in.Overview == nil || overviewMeta.ObservedAt.IsZero()) {
		verified := overviewMeta.VerifiedAt
		if verified.IsZero() {
			verified = r.now
		}
		if w.last != nil && decodeFieldQuality(w.last.FieldQualityJSON)[DomainOpsOverview] == QualityTrusted &&
			(overviewMeta.ObservedAt.IsZero() || w.last.ObservedAt.Equal(overviewMeta.ObservedAt)) {
			if (w.last.DBOK == nil || *w.last.DBOK) && (w.last.RedisOK == nil || *w.last.RedisOK) && r.adminErr == nil &&
				w.last.ConcTrustState != string(QualityUnverifiedZero) && decodeFieldQuality(w.last.FieldQualityJSON)[DomainInfraMetrics] != QualityStale {
				r.patch.LastStatus = ptr(string(StatusOnline))
			}
			return r.persist(commitCtx, nil, nil, &verified, nil, nil)
		}
		return r.persist(commitCtx, nil, nil, nil, nil, nil)
	}
	if in.Overview != nil && !overviewMeta.ObservedAt.IsZero() {
		in.ObservedAt = overviewMeta.ObservedAt
	}
	if !overviewMeta.VerifiedAt.IsZero() {
		in.VerifiedAt = overviewMeta.VerifiedAt
	}
	out := AnalyzeCollection(in)
	snap := &out.Snapshot
	if status == StatusOnline && (out.FieldQuality[DomainConcurrency] == QualityUnverifiedZero || out.FieldQuality[DomainInfraMetrics] == QualityStale) {
		r.patch.LastStatus = ptr(string(StatusDegraded))
	}
	if w.last != nil && snap.ObservedAt.Before(w.last.ObservedAt) {
		return r.persist(commitCtx, nil, nil, nil, nil, nil)
	}
	r.patch.LastObservedAt, r.patch.LastVerifiedAt, r.patch.LastHealthScore = &snap.ObservedAt, &snap.VerifiedAt, &snap.HealthScore
	live := groupsView(w.instance.ID, &snap.ObservedAt, out.FieldQuality[DomainGroupCapacity], out.Detail.ConcTrustReason, out.Detail.PlatformBreakdown, out.Groups)
	var liveAccounts *AccountsStatusResponse
	if in.Availability != nil || in.Concurrency != nil || len(w.accountsDir) > 0 {
		fused := buildLiveAccountsStatus(w.instance.ID, snap.ObservedAt, out.FieldQuality[DomainAccountAvailability], in.Availability, in.Concurrency, w.accountsDir)
		liveAccounts = &fused
	}
	var archive []storage.SiteOpsGroupSnapshot
	if len(out.Groups) > 0 && (w.lastArchive.IsZero() || snap.ObservedAt.Sub(w.lastArchive) >= archiveInterval) {
		archive = archiveGroups(w.instance.ID, out.Groups)
	}
	if err := r.persist(commitCtx, snap, archive, nil, &live, liveAccounts); err != nil {
		return err
	}
	w.evaluateAfterCollect(commitCtx, r, snap, out.Detail, out.FieldQuality, out.Groups, in.Availability)
	return nil
}

func (r *collectionRound) collectAdmin(ctx context.Context, target TargetEndpoint, in *AnalyzeInput) UpstreamFetchMeta {
	w := r.w
	if r.backoff != nil && r.now.Before(*r.backoff) {
		r.adminErr = ErrRateLimited
		return UpstreamFetchMeta{}
	}
	authRetry := r.security.unauthorized >= 2
	if authRetry && r.now.Before(r.security.authRetryAt) {
		for _, domain := range []string{DomainOpsOverview, DomainInfraMetrics, DomainConcurrency, DomainAccountAvailability, DomainSystemVersion} {
			in.FieldQuality[domain] = QualityUnauthorized
		}
		r.adminErr = ErrUnauthorized
		return UpstreamFetchMeta{}
	}
	if err := w.unlockTarget(&target); err != nil {
		r.noteAdmin(UpstreamFetchMeta{}, err)
		return UpstreamFetchMeta{}
	}
	if authRetry {
		r.security.authRetryAt = r.now.Add(authRetryInterval)
	}
	client, quality := w.poller.deps.Client, in.FieldQuality
	endpoint := EndpointOpsSnapshotV2
	if r.skip(endpoint) && r.caps[endpoint].FallbackTarget != "" {
		endpoint = EndpointOpsOverview
	} else if !r.skip(endpoint) && r.caps[endpoint].State == CapabilityUnsupported {
		delete(r.caps, endpoint)
	}
	if authRetry {
		// A failed credential may have no currently eligible overview endpoint.
		delete(r.caps, endpoint)
	}
	var meta UpstreamFetchMeta
	in.Overview, meta = roundFetch(r, ctx, endpoint, DomainOpsOverview, quality, func() (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
		data, meta, err := client.FetchOpsSnapshot(ctx, target, maps.Clone(r.caps))
		if err == nil && data != nil && data.OpsEnabled != nil && !*data.OpsEnabled {
			err = ErrOpsDisabled
		}
		return data, meta, err
	})
	if endpoint == EndpointOpsSnapshotV2 && approvedFallback(meta.FallbackUsed) != "" {
		r.caps[EndpointOpsOverview] = r.caps[endpoint]
		r.caps[endpoint] = EndpointCapability{State: CapabilityUnsupported, HTTPStatus: 404, FallbackTarget: EndpointOpsOverview, CheckedAt: r.now}
	}
	if authRetry && r.security.unauthorized >= 2 {
		r.haltAdmin = true
	}
	if quality[DomainOpsOverview] != "" {
		quality[DomainInfraMetrics] = quality[DomainOpsOverview]
	}
	in.FallbackUsed = approvedFallback(meta.FallbackUsed)
	if notModified(meta) && w.last != nil {
		return meta
	}
	var concurrencyMeta, availabilityMeta UpstreamFetchMeta
	in.Concurrency, concurrencyMeta = roundFetch(r, ctx, EndpointOpsConcurrency, DomainConcurrency, quality, func() (*SanitizedConcurrencyPayload, UpstreamFetchMeta, error) {
		value, meta, err := client.FetchConcurrency(ctx, target)
		if errors.Is(err, ErrOpsDisabled) && value != nil && value.RawEmpty && value.Enabled == nil {
			// Keep the sanitized empty signal for the analyzer to assess against history.
			err = nil
		}
		return value, meta, err
	})
	if in.Concurrency != nil && ((in.Concurrency.Enabled != nil && !*in.Concurrency.Enabled) || (in.Concurrency.RawEmpty && in.Concurrency.Enabled == nil)) {
		r.capability(EndpointOpsConcurrency, UpstreamFetchMeta{HTTPStatus: 200}, ErrOpsDisabled)
	}
	in.Availability, availabilityMeta = roundFetch(r, ctx, EndpointOpsAccountAvail, DomainAccountAvailability, quality, func() (*SanitizedAvailabilityPayload, UpstreamFetchMeta, error) {
		value, meta, err := client.FetchAccountAvailability(ctx, target)
		if err == nil && value != nil && ((value.Enabled != nil && !*value.Enabled) || (value.RawEmpty && value.Enabled == nil)) {
			err = ErrOpsDisabled
		}
		return value, meta, err
	})
	if w.last != nil && notModified(concurrencyMeta) && (in.Concurrency == nil || concurrencyMeta.ObservedAt.IsZero() || r.now.Sub(concurrencyMeta.ObservedAt) > 180*time.Second) {
		in.Concurrency, quality[DomainConcurrency] = nil, QualityStale
	}
	if w.last != nil && notModified(availabilityMeta) && (in.Availability == nil || availabilityMeta.ObservedAt.IsZero() || r.now.Sub(availabilityMeta.ObservedAt) > 180*time.Second) {
		in.Availability, quality[DomainAccountAvailability] = nil, QualityStale
	}
	if (len(w.groupMetadata) == 0 || r.now.Sub(r.caps[EndpointGroupsAll].CheckedAt) >= metadataRefreshPeriod) &&
		(in.Concurrency != nil || in.Availability != nil) {
		groups, _ := roundFetch(r, ctx, EndpointGroupsAll, "", make(FieldQualityMap), func() ([]SanitizedGroupInfo, UpstreamFetchMeta, error) {
			return client.FetchGroupsAll(ctx, target)
		})
		if groups != nil {
			w.groupMetadata = groups
		}
	}
	in.Groups = w.groupMetadata
	if r.now.Sub(r.caps[EndpointSystemVersion].CheckedAt) >= capabilityRetryWindow {
		in.Version, _ = roundFetch(r, ctx, EndpointSystemVersion, DomainSystemVersion, quality, func() (*SanitizedVersionInfo, UpstreamFetchMeta, error) {
			return client.FetchSystemVersion(ctx, target)
		})
	}
	if p1a, ok := client.(P1AClient); ok && !r.haltAdmin && ctx.Err() == nil {
		if w.lastL2At.IsZero() || r.now.Sub(w.lastL2At) >= l2RefreshPeriod {
			authCache, _ := roundFetch(r, ctx, EndpointOpsAuthCacheHealth, DomainAuthCacheHealth, quality, func() (*SanitizedAuthCacheHealth, UpstreamFetchMeta, error) {
				return p1a.FetchAuthCacheHealth(ctx, target)
			})
			if authCache != nil {
				w.authCacheHealth = authCache
				quality[DomainAuthCacheHealth] = QualityTrusted
			}
			ingress, _ := roundFetch(r, ctx, EndpointOpsIngressHealth, DomainIngressHealth, quality, func() (*SanitizedIngressRejectHealth, UpstreamFetchMeta, error) {
				return p1a.FetchIngressRejectHealth(ctx, target)
			})
			if ingress != nil {
				w.ingressHealth = ingress
				quality[DomainIngressHealth] = QualityTrusted
			}
			_, _ = roundFetch(r, ctx, EndpointOpsAlertEvents, "", make(FieldQualityMap), func() ([]SanitizedOpsAlertEvent, UpstreamFetchMeta, error) {
				return p1a.FetchOpsAlertEvents(ctx, target)
			})
			w.lastL2At = r.now
		}
		if w.lastAccountsAt.IsZero() || r.now.Sub(w.lastAccountsAt) >= l3AccountsRefreshPeriod {
			dir, _ := roundFetch(r, ctx, EndpointAccounts, "", make(FieldQualityMap), func() ([]SanitizedAccountSummary, UpstreamFetchMeta, error) {
				return p1a.FetchAccountsDirectory(ctx, target)
			})
			if dir != nil {
				w.accountsDir = dir
				w.lastAccountsAt = r.now
			}
		}
	}
	return meta
}

func notModified(meta UpstreamFetchMeta) bool { return meta.NotModified || meta.HTTPStatus == 304 }

func (r *collectionRound) setStatus(status InstanceStatus, health *HealthProbeResult) {
	reachability := ReachabilityUnknown
	if health != nil {
		reachability = ReachabilityUnreachable
		if health.Reachable {
			reachability = ReachabilityReachable
		}
	}
	r.patch.LastStatus, r.patch.LastReachability = ptr(string(status)), ptr(string(reachability))
	if r.patch.LastErrorCode == nil {
		r.patch.LastErrorCode, r.patch.LastErrorSummary = ptr(""), ptr("")
	}
}

func (r *collectionRound) recordError(err error) {
	if err != nil {
		r.patch.LastErrorCode = ptr(siteOpsErrorCode(err))
		r.patch.LastErrorSummary = ptr(r.w.poller.diagnostic(err, "", 0))
	}
}

func (r *collectionRound) persist(ctx context.Context, snap *storage.SiteOpsSnapshot, archive []storage.SiteOpsGroupSnapshot, verified *time.Time, live *GroupsResponse, liveAccounts *AccountsStatusResponse) error {
	w, p := r.w, r.w.poller
	if r.authenticated && !errors.Is(r.adminErr, ErrRateLimited) {
		r.security.backoff, r.backoff = 0, nil
	}
	r.patch.CapabilitiesJSON = ptr(jsonString(r.caps))
	r.patch.BackoffUntil = &r.backoff
	p.mu.Lock()
	defer p.mu.Unlock()
	id, generation := w.instance.ID, w.instance.Generation
	if p.workers[id] != w || w.ctx.Err() != nil {
		return ErrGenerationMismatch
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	if verified != nil {
		err = p.deps.Store.TouchSnapshotVerified(ctx, id, generation, *verified, r.patch)
	} else {
		err = p.deps.Store.CommitCollection(ctx, id, generation, r.patch, snap, archive)
	}
	if err != nil {
		return err
	}
	p.security[id], w.security, w.caps = r.security, r.security, r.caps
	w.instance.BackoffUntil = r.backoff
	if r.patch.LastVersion != nil {
		w.instance.LastVersion = *r.patch.LastVersion
	}
	if snap != nil {
		w.last = snap
		if snap.ConcTrustState == string(QualityTrusted) {
			w.trusted = snap
		}
	}
	if len(archive) > 0 {
		w.lastArchive = archive[0].ObservedAt
	}
	if live != nil {
		p.live[id] = liveGroupSample{response: *live, cachedAt: p.now()}
		if live.TrustState == QualityTrusted {
			w.trustedGroups = false
			for _, group := range live.Groups {
				w.trustedGroups = w.trustedGroups || (group.TrustState == QualityTrusted && group.AccountsTotal > 0)
			}
		}
	}
	if liveAccounts != nil {
		p.accounts[id] = liveAccountsSample{response: *liveAccounts, cachedAt: p.now()}
	}
	return nil
}

func (w *instanceWorker) probe(commitCtx, batch context.Context) (*ProbeReport, error) {
	r := w.newRound()
	if r.backoff != nil && r.now.Before(*r.backoff) {
		return nil, ErrRateLimited
	}
	p := w.poller
	if p.deps.Prober == nil {
		return nil, ErrInvalidInput
	}
	target, err := w.target()
	if err != nil {
		return nil, err
	}
	if err := w.unlockTarget(&target); err != nil {
		return nil, err
	}
	report, err := p.deps.Prober.ProbeInstance(batch, target, maps.Clone(w.caps))
	if report == nil {
		switch {
		case errors.Is(err, ErrUnauthorized):
			r.noteAdmin(UpstreamFetchMeta{}, ErrUnauthorized)
			if r.security.unauthorized == 2 {
				r.patch.LastStatus = ptr(string(StatusAuthError))
			}
		case errors.Is(err, ErrRateLimited):
			r.noteAdmin(UpstreamFetchMeta{}, ErrRateLimited)
		default:
			return nil, safeProbeError(err)
		}
		r.recordError(err)
		if err := r.persist(commitCtx, nil, nil, nil, nil, nil); err != nil {
			return nil, err
		}
		return nil, safeProbeError(err)
	}

	clean := *report
	clean.InstanceID = w.instance.ID
	clean.Health.SanitizedError = p.diagnostic(nil, report.Health.SanitizedError, report.Health.HTTPStatus)
	clean.Health.ErrorCode = approvedErrorCode(report.Health.ErrorCode)
	clean.Capabilities = make(CapabilitiesMap)
	var authorized, unauthorized, rateLimited bool
	for endpoint, cap := range report.Capabilities {
		switch endpoint {
		case EndpointHealth, EndpointOpsSnapshotV2, EndpointOpsOverview, EndpointOpsConcurrency, EndpointOpsAccountAvail,
			EndpointGroupsAll, EndpointSystemVersion, EndpointOpsAlertEvents, EndpointOpsIngressHealth, EndpointOpsAuthCacheHealth, EndpointAccounts:
		default:
			continue
		}
		original := cap
		cap.FallbackTarget = approvedFallback(cap.FallbackTarget)
		cap.Reason = p.diagnostic(nil, cap.Reason, cap.HTTPStatus)
		clean.Capabilities[endpoint], r.caps[endpoint] = cap, cap
		if endpoint == EndpointHealth || (original == w.caps[endpoint] && cap.CheckedAt.Before(r.now)) {
			continue
		}
		unauthorized = unauthorized || cap.HTTPStatus == 401
		rateLimited = rateLimited || cap.HTTPStatus == 429
		authorized = authorized || (cap.HTTPStatus != 401 && cap.HTTPStatus != 429 &&
			(cap.State == CapabilitySupported || cap.State == CapabilityForbidden || cap.State == CapabilityOpsDisabled))
	}
	switch {
	case rateLimited || errors.Is(err, ErrRateLimited):
		r.noteAdmin(UpstreamFetchMeta{}, ErrRateLimited)
	case unauthorized || errors.Is(err, ErrUnauthorized):
		r.noteAdmin(UpstreamFetchMeta{}, ErrUnauthorized)
	case authorized:
		r.noteAdmin(UpstreamFetchMeta{}, nil)
	}
	status := StatusOffline
	if clean.Health.Reachable {
		status = StatusDegraded
		if r.caps[EndpointOpsSnapshotV2].State == CapabilitySupported || r.caps[EndpointOpsOverview].State == CapabilitySupported {
			status = StatusOnline
		}
	}
	if r.security.unauthorized >= 2 {
		status = StatusAuthError
	}
	if clean.Version != "" {
		r.patch.LastVersion = &clean.Version
	}
	if r.adminErr != nil {
		r.recordError(r.adminErr)
	}
	r.setStatus(status, &clean.Health)
	if err := r.persist(commitCtx, nil, nil, nil, nil, nil); err != nil {
		return nil, err
	}
	return &clean, nil
}

func safeProbeError(err error) error {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, ErrUnauthorized, ErrForbidden, ErrRateLimited,
		ErrBudgetExceeded, ErrQueueTimeout, ErrOriginNotAllowed, ErrRedirectBlocked, ErrOpsDisabled, ErrUnsupportedRoute} {
		if errors.Is(err, known) {
			return known
		}
	}
	return errors.New("site-ops upstream probe failed")
}

func approvedFallback(value string) string {
	switch value {
	case EndpointOpsOverview, "/api/v1/admin/ops/dashboard/overview", "/ops/dashboard/overview", "overview":
		return EndpointOpsOverview
	default:
		return ""
	}
}

func siteOpsErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrAuthRequired):
		return ErrCodeAuthRequired
	case errors.Is(err, ErrOriginNotAllowed):
		return ErrCodeOriginNotAllowed
	case errors.Is(err, ErrRedirectBlocked):
		return ErrCodeRedirectBlocked
	case errors.Is(err, ErrBudgetExceeded):
		return ErrCodeBudgetExceeded
	case errors.Is(err, ErrQueueTimeout):
		return ErrCodeQueueTimeout
	case errors.Is(err, ErrRateLimited):
		return ErrCodeRateLimited
	case errors.Is(err, ErrUnauthorized):
		return ErrCodeUnauthorized
	case errors.Is(err, ErrForbidden):
		return ErrCodeForbidden
	case errors.Is(err, ErrOpsDisabled):
		return ErrCodeOpsDisabled
	case errors.Is(err, ErrUnsupportedRoute):
		return ErrCodeUnsupportedRoute
	default:
		return ErrCodeUpstreamError
	}
}

func approvedErrorCode(code string) string {
	switch code {
	case ErrCodeAuthRequired, ErrCodeOriginNotAllowed, ErrCodeRedirectBlocked, ErrCodeBudgetExceeded, ErrCodeQueueTimeout,
		ErrCodeRateLimited, ErrCodeUnauthorized, ErrCodeForbidden, ErrCodeOpsDisabled, ErrCodeUnsupportedRoute, ErrCodeUpstreamError:
		return code
	default:
		return ""
	}
}

func (p *Poller) diagnostic(err error, upstreamSummary string, status int) string {
	if upstreamSummary != "" && p.deps.SanitizeError != nil {
		return p.deps.SanitizeError(upstreamSummary, 200)
	}
	switch {
	case errors.Is(err, ErrUnauthorized) || status == 401:
		return "上游凭据未通过认证"
	case errors.Is(err, ErrForbidden) || status == 403:
		return "上游凭据缺少此端点权限"
	case errors.Is(err, ErrRateLimited) || status == 429:
		return "上游请求受限，已安排退避"
	case errors.Is(err, ErrOriginNotAllowed):
		return "站点地址未获出站策略允许"
	case errors.Is(err, ErrOpsDisabled):
		return "目标站点未启用此 Ops 功能"
	case errors.Is(err, ErrUnsupportedRoute):
		return "目标站点未提供此端点"
	case err != nil || upstreamSummary != "" || status >= 400:
		return "上游请求失败"
	default:
		return ""
	}
}

func (p *Poller) LatestLiveGroups(instanceID uint) (*GroupsResponse, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sample, found := p.live[instanceID]
	if !found || p.now().Sub(sample.cachedAt) > liveGroupsTTL {
		return nil, false
	}
	// Return a detached view, including optional metric pointers.
	return cloneGroupsView(sample.response), true
}

func (p *Poller) LatestAccountsStatus(instanceID uint) (*AccountsStatusResponse, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sample, found := p.accounts[instanceID]
	if !found || p.now().Sub(sample.cachedAt) > liveAccountsTTL {
		return nil, false
	}
	return filterAccountsStatus(&sample.response, "", ""), true
}

func (w *instanceWorker) evaluateAfterCollect(
	ctx context.Context,
	r *collectionRound,
	snap *storage.SiteOpsSnapshot,
	detail SnapshotDetailPayload,
	quality FieldQualityMap,
	groups []GroupCapacityItem,
	avail *SanitizedAvailabilityPayload,
) {
	p := w.poller
	if p.deps.EventStore == nil || p.evaluator == nil || ctx.Err() != nil {
		return
	}
	var accounts []SanitizedAccountAvailability
	if avail != nil && len(avail.Accounts) > 0 {
		accounts = make([]SanitizedAccountAvailability, 0, len(avail.Accounts))
		for _, a := range avail.Accounts {
			accounts = append(accounts, a)
		}
	}
	cfg := ParseAlertConfig(w.instance.AlertConfigJSON)
	in := EvalInput{
		Instance:           w.instance,
		Config:             cfg,
		Now:                r.now,
		Snapshot:           snap,
		Detail:             detail,
		FieldQuality:       maps.Clone(quality),
		Groups:             groups,
		Accounts:           accounts,
		AuthCacheHealth:    w.authCacheHealth,
		Auth401Consecutive: r.security.unauthorized,
		Auth401CredGen:     w.instance.CredGen,
		AuthVerified:       r.authenticated,
	}
	evalCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transitions, err := p.evaluator.Evaluate(evalCtx, p.deps.EventStore, in)
	if err != nil {
		p.deps.Log.Warn("站点运维告警评估失败", "instance_id", w.instance.ID, "err", err)
		return
	}
	if len(transitions) == 0 {
		return
	}
	p.mu.Lock()
	alerter := p.alerter
	p.mu.Unlock()
	if alerter == nil {
		return
	}
	if _, err := alerter.DispatchTransitions(evalCtx, w.instance, cfg, transitions); err != nil {
		p.deps.Log.Warn("站点运维告警直投失败", "instance_id", w.instance.ID, "err", err)
	}
}

func (p *Poller) runMaintenance(ctx context.Context) {
	retryTicker := time.NewTicker(deliveryRetryTick)
	reportTicker := time.NewTicker(dailyReportCheckTick)
	defer retryTicker.Stop()
	defer reportTicker.Stop()

	// 启动后先检查一次重启前遗留的待重试投递。
	p.processRetries(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-retryTicker.C:
			p.processRetries(ctx)
		case <-reportTicker.C:
			p.checkScheduledDailyReports(ctx)
		}
	}
}

func (p *Poller) processRetries(ctx context.Context) {
	p.mu.Lock()
	allowed, alerter := p.allowed, p.alerter
	p.mu.Unlock()
	if !allowed || alerter == nil {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := alerter.ProcessDueDeliveries(runCtx, 50); err != nil && !errors.Is(err, context.Canceled) {
		p.deps.Log.Warn("站点运维告警补发失败", "err", err)
	}
}

func (p *Poller) checkScheduledDailyReports(ctx context.Context) {
	p.mu.Lock()
	if !p.allowed || p.alerter == nil || p.deps.EventStore == nil || p.deps.Store == nil {
		p.mu.Unlock()
		return
	}
	alerter := p.alerter
	now := p.now()
	nowUTC8 := now.In(locUTC8)
	startUTC, _, dateLabel := YesterdayWindowUTC8(now)
	var due []storage.SiteOpsInstance
	for _, inst := range p.instances {
		if !inst.Enabled {
			continue
		}
		cfg := ParseAlertConfig(inst.AlertConfigJSON)
		if !cfg.DailyReportEnabled || nowUTC8.Hour() != cfg.DailyReportHourUTC8 {
			continue
		}
		if p.dailySent[inst.ID] == dateLabel {
			continue
		}
		due = append(due, inst)
	}
	p.mu.Unlock()

	for _, inst := range due {
		if ctx.Err() != nil {
			return
		}
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		snapshots, err := p.deps.Store.ListSnapshotsSince(runCtx, inst.ID, startUTC.Add(-2*time.Hour), 0)
		if err != nil {
			cancel()
			continue
		}
		active, err := p.deps.EventStore.ListActiveEvents(runCtx, inst.ID)
		if err != nil {
			cancel()
			continue
		}
		cfg := ParseAlertConfig(inst.AlertConfigJSON)
		preview := BuildDailyReport(DailyReportInput{
			Instance:             inst,
			Now:                  now,
			Snapshots:            snapshots,
			ActiveEvents:         active,
			IncludeMaskedSummary: cfg.IncludeMaskedSummary,
		})
		_, _, err = alerter.DispatchDailyReport(runCtx, inst, preview)
		cancel()
		if err != nil {
			p.deps.Log.Warn("站点运维定时日报直投失败", "instance_id", inst.ID, "err", err)
			continue
		}
		p.mu.Lock()
		p.dailySent[inst.ID] = dateLabel
		p.mu.Unlock()
	}
}

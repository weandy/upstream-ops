package siteops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/bejix/upstream-ops/backend/storage"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
)

func (s *Service) invalidateAnalytics(id uint, reason string) {
	if c, ok := s.deps.Client.(BackoffAwareClient); ok {
		c.InvalidateAnalyticsScope(id, reason)
		if _, shared := c.(*Client); shared {
			return
		}
	}
	s.analytics.invalidate(id, ErrGenerationMismatch)
}
func (s *Service) mergeAnalyticsCapabilities(inst storage.SiteOpsInstance, caps CapabilitiesMap) *time.Time {
	until := inst.BackoffUntil
	if until != nil && !until.After(s.poller.now()) {
		until = nil
	}
	if c, ok := s.deps.Client.(BackoffAwareClient); ok {
		if until != nil {
			c.SeedAdminBackoff(inst.ID, inst.CredGen, *until)
		}
		if t, active := c.AdminBackoff(inst.ID, inst.CredGen); active && (until == nil || t.After(*until)) {
			until = &t
		}
		for k, v := range c.ObservedP2Capabilities(inst.ID, inst.Generation, inst.CredGen) {
			caps[k] = v
		}
	}
	return until
}

type analyticsRequest struct {
	s                  *Service
	ctx                context.Context
	inst               storage.SiteOpsInstance
	target             TargetEndpoint
	c                  P2AnalyticsClient
	scope              analyticsScope
	meta               AnalyticsMeta
	force, only        bool
	timeout            time.Duration
	fatal, upstreamErr error
	denied             map[string]DomainMeta
	caps               CapabilitiesMap
	lastLeaf           DomainMeta
	success            bool
}

func validAnalyticsID(id int64) bool   { return id > 0 && id <= 9007199254740991 }
func validAnalyticsPage(page int) bool { return page >= 1 && page <= 10000 }

func (s *Service) analyticsRequest(ctx context.Context, id uint, force, only bool) (*analyticsRequest, error) {
	if !validAnalyticsID(int64(id)) {
		return nil, ErrInvalidInput
	}
	scope := s.analytics.scope(id, 0, 0)
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	scope.gen, scope.cred = inst.Generation, inst.CredGen
	if e := s.analytics.scopeError(scope); e != nil {
		return nil, e
	}
	c, _ := s.deps.Client.(P2AnalyticsClient)
	r := &analyticsRequest{s: s, ctx: ctx, inst: *inst, c: c, scope: scope, force: force, only: only, timeout: time.Duration(s.CurrentConfig().BatchTimeoutSeconds) * time.Second, meta: AnalyticsMeta{InstanceID: id, Generation: inst.Generation, AsOf: s.poller.now(), FromCache: true, Domains: map[string]DomainMeta{}}}
	r.caps = CapabilitiesMap{}
	r.meta.BackoffUntil = s.mergeAnalyticsCapabilities(*inst, r.caps)
	if inst.Enabled && c != nil && !only {
		r.target, err = s.targetForInstance(*inst)
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}
func (r *analyticsRequest) finish() error {
	if errors.Is(r.fatal, ErrUnauthorized) {
		return r.fatal
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if err := r.s.analytics.scopeError(r.scope); err != nil {
		return err
	}
	if r.fatal != nil {
		return r.fatal
	}
	if !r.success && r.upstreamErr != nil {
		return r.upstreamErr
	}
	return nil
}
func leaf[T any](r *analyticsRequest, domain, ep string, params any, o leafOptions, fetch func(context.Context) (T, UpstreamFetchMeta, error)) T {
	var zero T
	if !r.inst.Enabled {
		r.meta.Domains[domain] = DomainMeta{Quality: QualityDisabled}
		return zero
	}
	if r.fatal != nil {
		return zero
	}
	if m, ok := r.denied[domain]; ok {
		r.noteDomain(domain, m)
		return zero
	}
	if cap := r.caps[ep]; cap.State == CapabilityForbidden {
		r.noteDomain(domain, DomainMeta{Quality: QualityUnauthorized, Reason: errorCode(ErrForbidden)})
		return zero
	}
	method := "GET"
	if ep == EndpointUsersUsage || ep == EndpointAPIKeysUsage {
		method = "POST"
	}
	o.only = o.only || r.only || r.c == nil
	o.force = r.force && r.meta.BackoffUntil == nil
	o.timeout = r.timeout
	_, o.clientFences = r.s.deps.Client.(*Client)
	v, m, hit, err := loadProjection(r.ctx, r.s.analytics, newLeafKey(r.scope, ep, method, params), o, fetch)
	if err != nil {
		m = DomainMeta{Quality: qualityFor(err), Reason: errorCode(err)}
		switch {
		case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrGenerationMismatch), errors.Is(err, ErrOriginNotAllowed):
			r.fatal = err
		case errors.Is(err, ErrForbidden) && !r.s.analytics.valid(r.scope):
			r.fatal = ErrForbidden
			if err == errForbiddenFence {
				return zero
			} // 其他端点触发的围栏，当前域尚未确认无权限。
		case errors.Is(err, ErrRateLimited):
			r.upstreamErr = err
			if c, ok := r.s.deps.Client.(BackoffAwareClient); ok {
				if t, active := c.AdminBackoff(r.inst.ID, r.inst.CredGen); active {
					r.meta.BackoffUntil = &t
					if store := r.s.deps.AnalyticsStore; store != nil {
						if e := store.ExtendAdminBackoff(r.ctx, r.inst.ID, r.inst.Generation, t); e != nil {
							r.fatal = e
						}
					}
				}
			}
		case errors.Is(err, ErrBudgetExceeded), errors.Is(err, ErrQueueTimeout):
			r.upstreamErr = err
		case errors.Is(err, errProjectionMiss):
			m.Reason = "cache_miss"
		}
	} else {
		r.success = true
		if !hit {
			r.meta.FromCache = false
		}
	}
	// 同域多叶子取最早到期，保留部分失败与缺数原因。
	r.lastLeaf = m
	r.noteDomain(domain, m)
	return v
}
func (r *analyticsRequest) noteDomain(domain string, m DomainMeta) {
	old, exists := r.meta.Domains[domain]
	if exists && domain == "user_stats" {
		if m.Reason == "cache_miss" && old.Reason != "cache_miss" {
			return
		}
		if old.Reason == "cache_miss" && m.Reason != "cache_miss" {
			exists = false
		}
	}
	if exists && old.ExpiresAt != nil && (m.ExpiresAt == nil || old.ExpiresAt.Before(*m.ExpiresAt)) {
		m.ExpiresAt = old.ExpiresAt
	}
	if exists && old.Quality != QualityTrusted && m.Quality == QualityTrusted {
		m.Quality, m.Reason = old.Quality, old.Reason
	}
	r.meta.Domains[domain] = m
}
func runAnalysis[T any](s *Service, ctx context.Context, id uint, force, only bool, build func(*analyticsRequest) *T) (*T, error) {
	r, err := s.analyticsRequest(ctx, id, force, only)
	if err != nil {
		return nil, err
	}
	original := r.scope
	denied := map[string]DomainMeta{}
	// 一次构建最多涉及 15 个 P2 端点。只在 403 更新令牌后重建。
	for attempt := 0; ; attempt++ {
		v := build(r)
		err = r.finish()
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, ErrForbidden) || attempt >= 15 {
			return nil, err
		}
		for domain, m := range r.meta.Domains {
			if m.Quality == QualityUnauthorized {
				denied[domain] = m
			}
		}
		retry, e := s.analyticsRequest(ctx, id, false, only)
		if e != nil {
			return nil, e
		}
		if retry.scope.epoch != original.epoch || retry.scope.gen != original.gen || retry.scope.cred != original.cred {
			return nil, ErrGenerationMismatch
		}
		if e = s.analytics.scopeError(original); e != nil && !errors.Is(e, ErrForbidden) {
			return nil, e
		}
		retry.denied = denied
		r = retry
	}
}
func (r *analyticsRequest) users(page int) AnalyticsPage[SanitizedAnalyticsUser] {
	v := leaf(r, "users", EndpointUsersPage, page, leafOptions{}, func(ctx context.Context) (AnalyticsPage[SanitizedAnalyticsUser], UpstreamFetchMeta, error) {
		return r.c.FetchUsersPage(ctx, r.target, page)
	})
	v.Page = page
	if v.PageSize <= 0 {
		v.PageSize = 100
	}
	return v
}
func (r *analyticsRequest) subs(page int) AnalyticsPage[SanitizedSubscription] {
	v := leaf(r, "subscriptions", EndpointSubscriptionsPage, page, leafOptions{}, func(ctx context.Context) (AnalyticsPage[SanitizedSubscription], UpstreamFetchMeta, error) {
		return r.c.FetchSubscriptionsPage(ctx, r.target, page)
	})
	v.Page = page
	if v.PageSize <= 0 {
		v.PageSize = 100
	}
	return v
}
func (r *analyticsRequest) groups() map[int64]SanitizedGroupInfo {
	g, at, fresh := r.s.poller.LatestGroupMetadataFresh(r.inst.ID, r.meta.AsOf)
	if fresh && r.inst.Enabled {
		r.meta.Domains["groups"] = DomainMeta{Quality: QualityTrusted, ObservedAt: &at, VerifiedAt: &at, ExpiresAt: ptr(at.Add(metadataRefreshPeriod))}
	} else {
		g = leaf(r, "groups", EndpointAnalyticsGroups, nil, leafOptions{}, func(ctx context.Context) ([]SanitizedGroupInfo, UpstreamFetchMeta, error) {
			return r.c.FetchAnalyticsGroups(ctx, r.target)
		})
	}
	m := map[int64]SanitizedGroupInfo{}
	for _, v := range g {
		m[v.ID] = v
	}
	return m
}
func coverage[T any](p AnalyticsPage[T]) string {
	if p.Truncated {
		return "truncated"
	}
	if p.Page == 1 && (p.HasMore == nil || !*p.HasMore) && (p.Total != nil && *p.Total == int64(len(p.Items)) || p.Total == nil && p.HasMore != nil && !*p.HasMore) {
		return "complete"
	}
	return "page"
}
func mappedPage[A, B any](p AnalyticsPage[A]) AnalyticsPage[B] {
	return AnalyticsPage[B]{Items: []B{}, Page: p.Page, PageSize: p.PageSize, Total: p.Total, HasMore: p.HasMore, Truncated: p.Truncated}
}
func (r *analyticsRequest) quick(ids []int64) map[int64]SanitizedUserQuickUsage {
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return leaf(r, "users_usage", EndpointUsersUsage, ids, leafOptions{today: true}, func(ctx context.Context) (map[int64]SanitizedUserQuickUsage, UpstreamFetchMeta, error) {
		return r.c.FetchUsersUsageBatch(ctx, r.target, ids)
	})
}
func (r *analyticsRequest) concurrency() SanitizedUserConcurrencyMap {
	return leaf(r, "user_concurrency", EndpointUserConcurrency, nil, leafOptions{}, func(ctx context.Context) (SanitizedUserConcurrencyMap, UpstreamFetchMeta, error) {
		return r.c.FetchUserConcurrency(ctx, r.target)
	})
}
func buildUserConcurrencyPeakSamples(con SanitizedUserConcurrencyMap, highPct float64) []storage.SiteOpsUserConcurrencyPeak {
	if len(con) == 0 {
		return nil
	}
	if highPct <= 0 {
		highPct = 85
	}
	out := make([]storage.SiteOpsUserConcurrencyPeak, 0, len(con))
	for uid, c := range con {
		inUse := max(int64(0), val(c.CurrentInUse))
		maxCap := max(int64(0), val(c.MaxCapacity))
		waiting := max(int64(0), val(c.WaitingInQueue))
		if uid <= 0 || (inUse == 0 && waiting == 0) {
			continue
		}
		var hit int64
		if waiting > 0 || (maxCap > 0 && float64(inUse)*100 >= float64(maxCap)*highPct) {
			hit = 1
		}
		out = append(out, storage.SiteOpsUserConcurrencyPeak{
			UserID:       uid,
			PeakInUse:    inUse,
			MaxCapacity:  maxCap,
			PeakWaiting:  waiting,
			FullLoadHits: hit,
		})
	}
	return out
}

func (s *Service) peakStore() UserConcurrencyPeakStore {
	if ps, ok := s.deps.AnalyticsStore.(UserConcurrencyPeakStore); ok {
		return ps
	}
	if ps, ok := s.deps.Store.(UserConcurrencyPeakStore); ok {
		return ps
	}
	return nil
}

func mergeConcurrencyPeak(v *AnalyticsUserRow, peak storage.SiteOpsUserConcurrencyPeak, hasPeak bool) {
	if !hasPeak {
		return
	}
	if v.LiveConcurrency == nil {
		zero := int64(0)
		var maxCap *int64
		if peak.MaxCapacity > 0 {
			mc := peak.MaxCapacity
			maxCap = &mc
		} else if v.ConcurrencyLimit != nil {
			mc := int64(*v.ConcurrencyLimit)
			maxCap = &mc
		}
		v.LiveConcurrency = &UserConcurrencyView{
			CurrentInUse:   &zero,
			MaxCapacity:    maxCap,
			WaitingInQueue: &zero,
		}
	}
	pInUse := peak.PeakInUse
	if v.LiveConcurrency.CurrentInUse != nil && *v.LiveConcurrency.CurrentInUse > pInUse {
		pInUse = *v.LiveConcurrency.CurrentInUse
	}
	pWait := peak.PeakWaiting
	if v.LiveConcurrency.WaitingInQueue != nil && *v.LiveConcurrency.WaitingInQueue > pWait {
		pWait = *v.LiveConcurrency.WaitingInQueue
	}
	hits := peak.FullLoadHits
	peakAt := peak.PeakAt
	v.LiveConcurrency.PeakInUse = &pInUse
	v.LiveConcurrency.PeakWaiting = &pWait
	v.LiveConcurrency.FullLoadHits = &hits
	if !peakAt.IsZero() {
		v.LiveConcurrency.PeakAt = &peakAt
	}
	v.LiveConcurrency.PeakWindowDays = 7
}

func baseUser(u SanitizedAnalyticsUser, q SanitizedUserQuickUsage, con SanitizedUserConcurrencyMap, subs []SanitizedSubscription, n time.Time) AnalyticsUserRow {
	v := AnalyticsUserRow{UserID: u.ID, Email: u.Email, EmailMasked: u.EmailMasked, Role: u.Role, Status: u.Status, CreatedAt: u.CreatedAt, BalanceUSD: u.BalanceUSD, ConcurrencyLimit: u.ConcurrencyLimit, TodayActualUSD: q.Today, RecentAggregateActualUSD: q.Recent, ByPlatform: q.ByPlatform, EnrichmentState: "not_loaded"}
	if c, ok := con[u.ID]; ok {
		v.LiveConcurrency = &c
	}
	for _, sub := range subs {
		if sub.UserID == u.ID && activeSub(sub, n) {
			v.ActiveSubCount++
			d := sub.ExpiresAt.Sub(n).Hours() / 24
			if v.NearestSubExpiresDays == nil || d < *v.NearestSubExpiresDays {
				v.NearestSubExpiresDays = &d
			}
		}
	}
	return v
}
func (r *analyticsRequest) stats(domain string, q AnalyticsStatsQuery) *UsageWindowView {
	var observed time.Time
	v := leaf(r, domain, EndpointUsageStats, q, leafOptions{today: q.EndDate == r.meta.AsOf.In(locUTC8).Format(time.DateOnly)}, func(ctx context.Context) (*SanitizedUsageStats, UpstreamFetchMeta, error) {
		return r.c.FetchUsageStats(ctx, r.target, q)
	})
	if v == nil {
		return nil
	}
	if r.lastLeaf.ObservedAt != nil {
		observed = *r.lastLeaf.ObservedAt
	}
	return &UsageWindowView{q.StartDate, q.EndDate, q.Timezone, &observed, QualityTrusted, v.Requests, v.Standard, v.Actual, v.Account, costSpread(v.Actual, v.Account), v.DurationMS, v.Tokens}
}
func (r *analyticsRequest) userWindow(u AnalyticsUserRow, days int) *UsageWindowView {
	today := daySH(r.meta.AsOf)
	start := today.AddDate(0, 0, 1-days)
	if u.CreatedAt != nil && !u.CreatedAt.IsZero() && !u.CreatedAt.After(r.meta.AsOf) {
		first := daySH(*u.CreatedAt).AddDate(0, 0, -1)
		floor := today.AddDate(-10, 0, 0)
		if first.Before(floor) {
			first = floor
		}
		if days == 0 || first.After(start) {
			start = first
		}
	} else if days == 0 {
		return &UsageWindowView{Quality: QualityUnavailable, Timezone: "Asia/Shanghai", Cache: cacheMetrics(nil, nil, nil, nil, nil)}
	}
	return r.stats("user_stats", AnalyticsStatsQuery{AnalyticsDateRange: dateRange(start, today), UserID: u.UserID})
}
func (s *Service) GetAnalyticsLights(ctx context.Context, id uint, force bool) (*AnalyticsLightsResponse, error) {
	return runAnalysis(s, ctx, id, force, false, func(r *analyticsRequest) *AnalyticsLightsResponse {
		w := leaf(r, "risk_control", EndpointRiskControlStatus, nil, leafOptions{}, func(c context.Context) (*SanitizedRiskControlStatus, UpstreamFetchMeta, error) {
			return r.c.FetchRiskControlStatus(c, r.target)
		})
		b := leaf(r, "backups", EndpointBackups, nil, leafOptions{}, func(c context.Context) (SanitizedBackupsResult, UpstreamFetchMeta, error) {
			return r.c.FetchBackupsList(c, r.target)
		})
		caps := CapabilitiesMap{}
		s.mergeAnalyticsCapabilities(r.inst, caps)
		cap := caps[EndpointChannelMonitorV2]
		if cap.State == "" {
			cap.State = CapabilityUnknown
		}
		return &AnalyticsLightsResponse{r.meta, riskLamp(w, r.meta.Domains["risk_control"]), backupLamp(b, r.meta.Domains["backups"], r.meta.AsOf), cap}
	})
}
func (s *Service) GetAnalyticsChannelStatus(ctx context.Context, id uint, force bool) (*AnalyticsChannelResponse, error) {
	return runAnalysis(s, ctx, id, force, false, func(r *analyticsRequest) *AnalyticsChannelResponse {
		leaf(r, "channel_monitor", EndpointChannelMonitorV2, nil, leafOptions{}, func(c context.Context) (*SanitizedChannelAvailability, UpstreamFetchMeta, error) {
			return r.c.ProbeChannelMonitorV2(c, r.target)
		})
		caps := CapabilitiesMap{}
		s.mergeAnalyticsCapabilities(r.inst, caps)
		cap := caps[EndpointChannelMonitorV2]
		note := ""
		if cap.State == CapabilityOpsDisabled {
			note = "通道监控未开启（可在 Sub2API 后台开启）"
		}
		if cap.State == "" {
			cap.State = CapabilityUnknown
		}
		return &AnalyticsChannelResponse{r.meta, cap, note}
	})
}
func (s *Service) GetAnalyticsUsers(ctx context.Context, id uint, q AnalyticsUsersQuery) (*AnalyticsUsersResponse, error) {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.TopN == 0 {
		q.TopN = 5
	}
	if q.Page < 1 || q.Page > 10000 || q.TopN < 1 || q.TopN > 20 {
		return nil, ErrInvalidInput
	}
	return runAnalysis(s, ctx, id, q.Force, q.CacheOnly, func(r *analyticsRequest) *AnalyticsUsersResponse {
		groups := r.groups()
		p := r.users(q.Page)
		ids := make([]int64, 0, len(p.Items))
		for _, u := range p.Items {
			ids = append(ids, u.ID)
		}
		quick := r.quick(ids)
		con := r.concurrency()
		subs := r.subs(1)
		cfg := analyticsConfig(r.inst)
		prices := r.prices()
		var peaks map[int64]storage.SiteOpsUserConcurrencyPeak
		if ps := s.peakStore(); ps != nil {
			todayStr := r.meta.AsOf.In(locUTC8).Format(time.DateOnly)
			if samples := buildUserConcurrencyPeakSamples(con, cfg.HighConcurrencyPct); len(samples) > 0 {
				_ = ps.RecordUserConcurrencyPeaks(r.ctx, id, todayStr, r.meta.AsOf, samples)
			}
			sinceStr := r.meta.AsOf.In(locUTC8).AddDate(0, 0, -6).Format(time.DateOnly)
			if queried, err := ps.QueryUserConcurrencyPeaks(r.ctx, id, sinceStr, ids); err == nil {
				peaks = queried
			}
		}
		activeSubsByUser := make(map[int64][]AnalyticsSubscriptionRow)
		for _, sub := range subs.Items {
			if activeSub(sub, r.meta.AsOf) {
				row := r.subRow(sub, subs, groups, prices)
				activeSubsByUser[sub.UserID] = append(activeSubsByUser[sub.UserID], row)
			}
		}
		rows := mappedPage[SanitizedAnalyticsUser, AnalyticsUserRow](p)
		summary := AnalyticsUsersSummary{UsersSeen: len(p.Items), UsersCoverage: coverage(p), SubsCoverage: coverage(subs), TagCounts: map[string]int{}}
		var candidates []int
		for i, u := range p.Items {
			v := baseUser(u, quick[u.ID], con, subs.Items, r.meta.AsOf)
			if peak, ok := peaks[u.ID]; ok {
				mergeConcurrencyPeak(&v, peak, true)
			}
			if list := activeSubsByUser[u.ID]; len(list) > 0 {
				v.ActiveSubscriptions = list
			}
			if v.ActiveSubCount > 0 || val(v.TodayActualUSD) > 0 || val(v.RecentAggregateActualUSD) > 0 || val(v.BalanceUSD) >= 20 {
				candidates = append(candidates, i)
			}
			if val(v.TodayActualUSD) > 0 {
				summary.ActiveTodayUsers++
			}
			if v.TodayActualUSD != nil {
				if summary.TodayActualUSD == nil {
					summary.TodayActualUSD = ptr(0.0)
				}
				summary.TodayActualUSD = safeFinitePtr(*summary.TodayActualUSD + *v.TodayActualUSD)
			}
			rows.Items = append(rows.Items, v)
		}
		sort.Slice(candidates, func(i, j int) bool {
			a, b := rows.Items[candidates[i]], rows.Items[candidates[j]]
			if val(a.TodayActualUSD) != val(b.TodayActualUSD) {
				return val(a.TodayActualUSD) > val(b.TodayActualUSD)
			}
			if val(a.RecentAggregateActualUSD) != val(b.RecentAggregateActualUSD) {
				return val(a.RecentAggregateActualUSD) > val(b.RecentAggregateActualUSD)
			}
			return a.UserID < b.UserID
		})
		budget := q.TopN
		if c, ok := s.deps.Client.(BackoffAwareClient); ok && !q.CacheOnly {
			n, _, _ := c.BudgetHeadroomForAnalytics(id, r.inst.AuditBudgetPerDay)
			budget = min(budget, n/2)
		}
		selected := candidates[:min(len(candidates), q.TopN)]
		batches := make([]analyticsRequest, len(selected))
		var workers sync.WaitGroup
		// 每个 worker 只操作自己的行和域元数据。网络叶子在客户端统一占槽。
		for worker := 0; worker < min(3, len(selected)); worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for rank := worker; rank < len(selected); rank += 3 {
					child := *r
					child.upstreamErr = nil
					child.only = r.only || rank >= budget
					child.meta.Domains = map[string]DomainMeta{}
					v := &rows.Items[selected[rank]]
					v.AllTime = child.userWindow(*v, 0)
					v.Last7d = child.userWindow(*v, 7)
					switch {
					case v.Last7d != nil:
						v.EnrichmentState = "loaded"
					case !r.only && (rank >= budget || errors.Is(child.upstreamErr, ErrBudgetExceeded)):
						v.EnrichmentState = "deferred_budget"
					case child.only && child.meta.Domains["user_stats"].Reason == "cache_miss":
						v.EnrichmentState = "not_loaded"
					default:
						v.EnrichmentState = "error"
					}
					batches[rank] = child
				}
			}()
		}
		workers.Wait()
		for rank, child := range batches {
			if child.fatal != nil && !errors.Is(r.fatal, ErrUnauthorized) {
				r.fatal = child.fatal
			}
			if child.upstreamErr != nil {
				r.upstreamErr = child.upstreamErr
			}
			r.success = r.success || child.success
			r.meta.FromCache = r.meta.FromCache && child.meta.FromCache
			if t := child.meta.BackoffUntil; t != nil && (r.meta.BackoffUntil == nil || t.After(*r.meta.BackoffUntil)) {
				r.meta.BackoffUntil = t
			}
			for domain, m := range child.meta.Domains {
				r.noteDomain(domain, m)
			}
			switch rows.Items[selected[rank]].EnrichmentState {
			case "loaded":
				summary.EvaluatedUsers++
			case "deferred_budget":
				summary.BudgetDeferredUsers++
			}
		}
		for i := range rows.Items {
			v := &rows.Items[i]
			v.Tags = userTags(*v, subs.Items, coverage(subs) == "complete", r.meta.AsOf)
			v.ActivityState = userActivityState(*v, v.Tags)
			for _, tag := range v.Tags {
				summary.TagCounts[tag.Key]++
				if tag.Key == "concurrency_queued" {
					summary.QueueingUsersSeen++
				}
			}
		}
		for _, sub := range subs.Items {
			if activeSub(sub, r.meta.AsOf) {
				summary.ActiveSubsSeen++
				if sub.ExpiresAt.Sub(r.meta.AsOf) <= 7*24*time.Hour {
					summary.ExpiringSoonSubsSeen++
				}
			}
		}
		if summary.BudgetDeferredUsers > 0 {
			summary.BudgetNote = "部分用户补查已延后"
		}
		return &AnalyticsUsersResponse{r.meta, cfg, q.TopN, summary, rows}
	})
}
func (s *Service) GetAnalyticsUserDetail(ctx context.Context, id uint, userID int64, page, keyPage int, force bool) (*AnalyticsUserDetailResponse, error) {
	if !validAnalyticsID(userID) || !validAnalyticsPage(page) || !validAnalyticsPage(keyPage) {
		return nil, ErrInvalidInput
	}
	return runAnalysis(s, ctx, id, force, false, func(r *analyticsRequest) *AnalyticsUserDetailResponse {
		p := r.users(page)
		out := &AnalyticsUserDetailResponse{Subscriptions: []UserSubscriptionBrief{}}
		defer func() { out.AnalyticsMeta = r.meta }()
		var u *SanitizedAnalyticsUser
		for i := range p.Items {
			if p.Items[i].ID == userID {
				u = &p.Items[i]
				break
			}
		}
		if u == nil {
			r.missing("users")
			return out
		}
		subs := r.subs(1)
		ids := make([]int64, 0, len(p.Items))
		for _, user := range p.Items {
			ids = append(ids, user.ID)
		}
		quick := r.quick(ids)
		v := baseUser(*u, quick[u.ID], r.concurrency(), subs.Items, r.meta.AsOf)
		v.AllTime = r.userWindow(v, 0)
		v.Last7d = r.userWindow(v, 7)
		out.Last30d = r.userWindow(v, 30)
		v.Tags = userTags(v, subs.Items, coverage(subs) == "complete", r.meta.AsOf)
		if v.Last7d != nil {
			v.EnrichmentState = "loaded"
		}
		out.User = v
		for _, sub := range subs.Items {
			if sub.UserID == u.ID {
				var days *float64
				if sub.ExpiresAt != nil {
					days = safeFinitePtr(sub.ExpiresAt.Sub(r.meta.AsOf).Hours() / 24)
				}
				out.Subscriptions = append(out.Subscriptions, UserSubscriptionBrief{sub.ID, sub.GroupID, sub.Group.Name, sub.Group.Platform, sub.Status, sub.StartsAt, sub.ExpiresAt, days})
			}
		}
		keys := leaf(r, "user_keys", EndpointUserAPIKeys, [2]int64{userID, int64(keyPage)}, leafOptions{}, func(c context.Context) (AnalyticsPage[SanitizedUserAPIKey], UpstreamFetchMeta, error) {
			return r.c.FetchUserAPIKeysPage(c, r.target, userID, keyPage)
		})
		keys.Page = keyPage
		if keys.PageSize <= 0 {
			keys.PageSize = 20
		}
		ids = nil
		for _, k := range keys.Items {
			ids = append(ids, k.APIKeyID)
		}
		if len(ids) > 0 {
			slices.Sort(ids)
			cost := leaf(r, "user_keys_usage", EndpointAPIKeysUsage, ids, leafOptions{today: true}, func(c context.Context) (map[int64]SanitizedAPIKeyUsage, UpstreamFetchMeta, error) {
				return r.c.FetchAPIKeysUsageBatch(c, r.target, ids)
			})
			for i := range keys.Items {
				k := &keys.Items[i]
				k.Quality = QualityUnavailable
				if x, ok := cost[k.APIKeyID]; ok {
					k.TodayActualUSD, k.UpstreamTotalActualUSD, k.Quality = x.Today, x.Total, QualityTrusted
				}
			}
		}
		out.APIKeys = keys
		return out
	})
}
func (r *analyticsRequest) missing(domain string) {
	if r.fatal == nil && r.meta.Domains[domain].Quality == QualityTrusted {
		r.fatal = ErrAnalyticsResourceNotFound
	}
}

func (s *Service) binding(inst storage.SiteOpsInstance) (string, error) {
	if s.deps.Validator == nil {
		return "", ErrOriginNotAllowed
	}
	base, err := s.deps.Validator.ValidateBaseURL(inst.BaseURL)
	if err != nil {
		return "", ErrOriginNotAllowed
	}
	digest := sha256.Sum256([]byte(base))
	return hex.EncodeToString(digest[:]), nil
}
func analyticsConfig(inst storage.SiteOpsInstance) AnalyticsConfig {
	v := DefaultAnalyticsConfig()
	if inst.AnalyticsConfigJSON != "" {
		if json.Unmarshal([]byte(inst.AnalyticsConfigJSON), &v) != nil {
			return DefaultAnalyticsConfig()
		}
		v = v.withForecastDefaults()
		if !validConfig(v) {
			return DefaultAnalyticsConfig()
		}
	}
	return v
}
func validConfig(v AnalyticsConfig) bool {
	v = v.withForecastDefaults()
	return v.CNYPerUSD > 0 && v.CNYPerUSD <= 100 &&
		v.LeverageWarnThreshold >= 0 && v.LeverageWarnThreshold <= 1000 &&
		v.LeverageDangerThreshold >= 0 && v.LeverageDangerThreshold <= 1000 &&
		(v.LeverageDangerThreshold == 0 || v.LeverageDangerThreshold >= v.LeverageWarnThreshold) &&
		v.NearFullPct >= 50 && v.NearFullPct <= 99 &&
		v.UnderUsePct >= 5 && v.UnderUsePct <= 70 &&
		v.UnderUsePct < v.NearFullPct &&
		v.HighConcurrencyPct >= 50 && v.HighConcurrencyPct <= 100 &&
		v.UnlimitedTargetUSD >= 10 && v.UnlimitedTargetUSD <= 100000
}
func priceView(v storage.SiteOpsReferencePrice) ReferencePriceView {
	return ReferencePriceView{v.InstanceID, v.Scope, v.ScopeID, v.Currency, v.MonthlyMinor, v.CNYPerUSDMicros, v.UpdatedAt}
}

type localPrices struct{ groups, subs map[int64]*ReferencePriceView }

func (r *analyticsRequest) prices() localPrices {
	out := localPrices{map[int64]*ReferencePriceView{}, map[int64]*ReferencePriceView{}}
	if r.s.deps.AnalyticsStore == nil {
		r.meta.Domains["prices"] = DomainMeta{Quality: QualityUnavailable}
		return out
	}
	binding, err := r.s.binding(r.inst)
	if err != nil {
		r.fatal = err
		return out
	}
	rows, err := r.s.deps.AnalyticsStore.ListReferencePrices(r.ctx, r.inst.ID, binding)
	if err != nil {
		r.fatal = err
		return out
	}
	for _, row := range rows {
		v := priceView(row)
		if v.Scope == "subscription" {
			out.subs[v.ScopeID] = &v
		} else {
			out.groups[v.ScopeID] = &v
		}
	}
	r.meta.Domains["prices"] = DomainMeta{Quality: QualityTrusted}
	return out
}
func (r *analyticsRequest) subRow(sub SanitizedSubscription, page AnalyticsPage[SanitizedSubscription], groups map[int64]SanitizedGroupInfo, prices localPrices) AnalyticsSubscriptionRow {
	if g, ok := groups[sub.GroupID]; ok {
		sub.Group = g
		if g.DailyLimitUSD != nil {
			sub.Daily.LimitUSD = g.DailyLimitUSD
		}
		if g.WeeklyLimitUSD != nil {
			sub.Weekly.LimitUSD = g.WeeklyLimitUSD
		}
		if g.MonthlyLimitUSD != nil {
			sub.Monthly.LimitUSD = g.MonthlyLimitUSD
		}
	}
	v := subscriptionCalendar(sub, page.Items, coverage(page) == "complete", r.meta.AsOf)
	p := prices.subs[sub.ID]
	if p == nil {
		p = prices.groups[sub.GroupID]
	}
	var d, total time.Duration
	if v.SubCycleEnd != nil {
		d = v.SubCycleEnd.Sub(*v.SubCycleStart)
		total = sub.ExpiresAt.Sub(*sub.StartsAt)
	}
	cfg := analyticsConfig(r.inst)
	v.Forecast = subscriptionForecastSummary(v, cfg, r.meta.AsOf)
	v.PricingCompare = priceCompare(p, cfg, d, total, v.Attribution == "unique_group_term", nil, nil)
	return v
}
func (s *Service) GetAnalyticsSubscriptions(ctx context.Context, id uint, q AnalyticsSubscriptionsQuery) (*AnalyticsSubscriptionsResponse, error) {
	if !validAnalyticsPage(q.Page) || !slices.Contains([]string{"active", "expired", "all"}, q.Status) || (q.GroupID != nil && !validAnalyticsID(*q.GroupID)) {
		return nil, ErrInvalidInput
	}
	return runAnalysis(s, ctx, id, q.Force, q.CacheOnly, func(r *analyticsRequest) *AnalyticsSubscriptionsResponse {
		groups := r.groups()
		p := r.subs(q.Page)
		prices := r.prices()
		out := &AnalyticsSubscriptionsResponse{AnalyticsMeta: r.meta, AnalyticsConfig: analyticsConfig(r.inst), Groups: []AnalyticsGroupOption{}, Subscriptions: mappedPage[SanitizedSubscription, AnalyticsSubscriptionRow](p)}
		for _, g := range groups {
			out.Groups = append(out.Groups, AnalyticsGroupOption{g.ID, g.Name, g.Platform, prices.groups[g.ID]})
		}
		sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].GroupID < out.Groups[j].GroupID })
		for _, sub := range p.Items {
			row := r.subRow(sub, p, groups, prices)
			if q.GroupID != nil && *q.GroupID != sub.GroupID || q.Status == "active" && row.CycleState != "active" || q.Status == "expired" && row.CycleState != "expired" && row.CycleState != "revoked" {
				continue
			}
			out.Subscriptions.Items = append(out.Subscriptions.Items, row)
		}
		return out
	})
}
func (s *Service) GetAnalyticsSubscriptionDetail(ctx context.Context, id uint, subID int64, page int, force, only bool) (*AnalyticsSubscriptionDetailResponse, error) {
	if !validAnalyticsID(subID) || !validAnalyticsPage(page) {
		return nil, ErrInvalidInput
	}
	return runAnalysis(s, ctx, id, force, only, func(r *analyticsRequest) *AnalyticsSubscriptionDetailResponse {
		out := &AnalyticsSubscriptionDetailResponse{AnalyticsConfig: analyticsConfig(r.inst)}
		defer func() { out.AnalyticsMeta = r.meta }()
		p := r.subs(page)
		var sub *SanitizedSubscription
		for i := range p.Items {
			if p.Items[i].ID == subID {
				sub = &p.Items[i]
				break
			}
		}
		if sub == nil {
			r.missing("subscriptions")
			return out
		}
		groups := r.groups()
		row := r.subRow(*sub, p, groups, r.prices())
		defer func() { out.Subscription = row }()
		if row.SubCycleStart == nil {
			return out
		}
		var progressOptions leafOptions
		if row.ExpiresAt != nil && row.ExpiresAt.After(r.meta.AsOf) {
			progressOptions.deadline = *row.ExpiresAt
		}
		progress := leaf(r, "subscription_progress", EndpointSubscriptionProgress, subID, progressOptions, func(c context.Context) (*SanitizedSubscriptionProgress, UpstreamFetchMeta, error) {
			return r.c.FetchSubscriptionProgress(c, r.target, subID)
		})
		at := time.Time{}
		if progress != nil {
			if m := r.meta.Domains["subscription_progress"]; m.ObservedAt != nil {
				at = *m.ObservedAt
			}
			for _, w := range []struct {
				dst *RollingWindowView
				src *RollingWindowView
			}{{&row.DailyWindow, progress.Daily}, {&row.WeeklyWindow, progress.Weekly}, {&row.MonthlyWindow, progress.Monthly}} {
				if w.src != nil {
					*w.dst = *w.src
					if w.dst.ResetsAt == nil {
						if reset := windowReset(w.dst, at); !reset.IsZero() {
							w.dst.ResetsAt = &reset
						}
					}
				}
			}
		}
		known := progress != nil || row.DailyWindow.LimitUSD != nil && row.WeeklyWindow.LimitUSD != nil && row.MonthlyWindow.LimitUSD != nil
		layer := func(a, z time.Time, renew bool, domain string) *SubscriptionTermLayerView {
			var usage *UsageWindowView
			end := r.meta.AsOf
			if row.CycleState == "expired" {
				end = z.Add(-time.Nanosecond)
			}
			if row.CycleState == "active" && a.Equal(end) {
				dr := dateRange(a, end)
				usage = &UsageWindowView{StartDate: dr.StartDate, EndDate: dr.EndDate, Timezone: dr.Timezone, Cache: cacheMetrics(nil, nil, nil, nil, nil), ObservedAt: &end, Quality: QualityTrusted, ActualUSD: ptr(0.0), Requests: ptr(int64(0))}
				r.noteDomain(domain, DomainMeta{Quality: QualityTrusted, ObservedAt: &end})
			} else if (row.CycleState == "active" || row.CycleState == "expired") && !a.After(end) {
				usage = r.stats(domain, AnalyticsStatsQuery{dateRange(a, end), sub.UserID, &sub.GroupID, ptr(1)})
			}
			v := subscriptionLayer(row, a, z, r.meta.AsOf, at, usage, renew, known)
			return &v
		}
		out.CurrentSubCycle = layer(*row.SubCycleStart, *row.SubCycleEnd, row.CycleIndex > 1, "subscription_cycle")
		if row.SameAsTerm {
			v := *out.CurrentSubCycle
			out.ContinuousTerm = &v
			r.meta.Domains["subscription_term"] = r.meta.Domains["subscription_cycle"]
		} else if row.Attribution == "unique_group_term" {
			out.ContinuousTerm = layer(*row.StartsAt, *row.ExpiresAt, false, "subscription_term")
		} else {
			v := subscriptionLayer(row, *row.StartsAt, *row.ExpiresAt, r.meta.AsOf, at, nil, false, known)
			out.ContinuousTerm = &v
		}
		var actual *float64
		if out.CurrentSubCycle.Usage != nil {
			actual = out.CurrentSubCycle.Usage.ActualUSD
		}
		row.PricingCompare = priceCompare(row.PricingCompare.ReferencePrice, out.AnalyticsConfig, row.SubCycleEnd.Sub(*row.SubCycleStart), row.ExpiresAt.Sub(*row.StartsAt), row.Attribution == "unique_group_term", actual, out.CurrentSubCycle.ProjectedEndUSD)
		return out
	})
}
func (s *Service) GetAnalyticsMarket(ctx context.Context, id uint, span string, force, only bool) (*AnalyticsMarketResponse, error) {
	if span != "7d" && span != "30d" {
		return nil, ErrInvalidInput
	}
	return runAnalysis(s, ctx, id, force, only, func(r *analyticsRequest) *AnalyticsMarketResponse {
		days := 30
		if span == "7d" {
			days = 7
		}
		dr := dateRange(r.meta.AsOf.AddDate(0, 0, 1-days), r.meta.AsOf)
		o := leafOptions{today: true}
		models := leaf(r, "models", EndpointDashboardModels, dr, o, func(c context.Context) (SanitizedModelRows, UpstreamFetchMeta, error) {
			return r.c.FetchDashboardModels(c, r.target, dr, false)
		})
		mismatches := leaf(r, "model_mismatches", EndpointModelMismatch, dr, o, func(c context.Context) (SanitizedModelRows, UpstreamFetchMeta, error) {
			return r.c.FetchDashboardModels(c, r.target, dr, true)
		})
		gs := leaf(r, "market_groups", EndpointDashboardGroups, dr, o, func(c context.Context) (SanitizedGroupMarketRows, UpstreamFetchMeta, error) {
			return r.c.FetchDashboardGroups(c, r.target, dr)
		})
		r.only = true
		meta := r.groups()
		subs := r.subs(1)
		prices := r.prices()
		cfg := analyticsConfig(r.inst)
		out := &AnalyticsMarketResponse{AnalyticsMeta: r.meta, Range: span, DateRange: dr, AnalyticsConfig: cfg, Models: []AnalyticsModelRowView{}, Groups: gs.Rows, Mismatches: ModelMismatchSummaryView{Quality: r.meta.Domains["model_mismatches"].Quality, ModelRows: len(mismatches.Rows), Rows: []ModelMismatchRowView{}}}
		counts := map[string]int64{}
		unknown := map[string]bool{}
		if out.Mismatches.Quality == QualityTrusted && !mismatches.Truncated {
			out.Mismatches.TotalRequests = ptr(int64(0))
		}
		for _, m := range mismatches.Rows {
			n := m.Stats.Requests
			out.Mismatches.Rows = append(out.Mismatches.Rows, ModelMismatchRowView{m.Model, m.UpstreamModel, n, m.Stats.Actual})
			if n == nil || *n < 0 || *n > math.MaxInt64-counts[m.Model] {
				unknown[m.Model] = true
			} else {
				counts[m.Model] += *n
			}
			out.Mismatches.TotalRequests = sumCount(out.Mismatches.TotalRequests, n)
		}
		if r.meta.Domains["models"].Quality == QualityTrusted && !models.Truncated {
			out.TotalRequests = ptr(int64(0))
			out.TotalActualUSD = ptr(0.0)
			out.TotalAccountUSD = ptr(0.0)
		}
		for _, m := range models.Rows {
			x := m.Stats
			v := AnalyticsModelRowView{Model: m.Model, Requests: x.Requests, StandardUSD: x.Standard, ActualUSD: x.Actual, AccountUSD: x.Account, HasCostSpread: costSpread(x.Actual, x.Account), Cache: x.Tokens, Quality: QualityTrusted}
			n, ok := counts[m.Model]
			v.HasMismatch = ok || unknown[m.Model]
			if !unknown[m.Model] && out.Mismatches.Quality == QualityTrusted && !mismatches.Truncated {
				v.MismatchRequests = &n
			}
			out.Models = append(out.Models, v)
			out.TotalRequests = sumCount(out.TotalRequests, x.Requests)
			out.TotalActualUSD = sumMoney(out.TotalActualUSD, x.Actual)
			out.TotalAccountUSD = sumMoney(out.TotalAccountUSD, x.Account)
		}
		out.HasCostSpread = costSpread(out.TotalActualUSD, out.TotalAccountUSD)
		for i := range out.Groups {
			g := &out.Groups[i]
			info := meta[g.GroupID]
			g.Platform, g.RateMultiplier = info.Platform, info.RateMultiplier
			if info.SubscriptionType != nil {
				g.SubscriptionType = *info.SubscriptionType
			}
			if r.meta.Domains["subscriptions"].Quality == QualityTrusted {
				n := 0
				for _, s := range subs.Items {
					if s.GroupID == g.GroupID && activeSub(s, r.meta.AsOf) {
						n++
					}
				}
				g.ActiveSubCount = &n
			}
			g.ReferencePrice = prices.groups[g.GroupID]
			g.RefMonthlyUSD = priceCompare(g.ReferencePrice, cfg, cycleDuration, cycleDuration, false, nil, nil).CycleRefUSD
		}
		return out
	})
}
func sumCount(a, b *int64) *int64 {
	if a == nil || b == nil || *b < 0 || *b > math.MaxInt64-*a {
		return nil
	}
	return ptr(*a + *b)
}
func sumMoney(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	return safeFinitePtr(*a + *b)
}
func (s *Service) GetAnalyticsPricing(ctx context.Context, id uint) (*AnalyticsPricingResponse, error) {
	r, err := s.analyticsRequest(ctx, id, false, true)
	if err != nil {
		return nil, err
	}
	prices := r.prices()
	out := &AnalyticsPricingResponse{id, r.inst.Generation, analyticsConfig(r.inst), []ReferencePriceView{}}
	for _, m := range []map[int64]*ReferencePriceView{prices.groups, prices.subs} {
		for _, p := range m {
			out.Prices = append(out.Prices, *p)
		}
	}
	sort.Slice(out.Prices, func(i, j int) bool {
		a, b := out.Prices[i], out.Prices[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.ScopeID < b.ScopeID
	})
	if err := r.finish(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Service) UpdateAnalyticsConfig(ctx context.Context, id uint, in AnalyticsConfig) (*AnalyticsConfig, error) {
	in = in.withForecastDefaults()
	if !validConfig(in) {
		return nil, ErrInvalidInput
	}
	if s.deps.AnalyticsStore == nil {
		return nil, errProjectionMiss
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, ErrInvalidInput
	}
	if err := s.deps.AnalyticsStore.UpdateAnalyticsConfig(ctx, id, string(data)); err != nil {
		return nil, err
	}
	return &in, nil
}
func validPriceScope(scope string, id int64) bool {
	return (scope == "subscription" || scope == "group") && validAnalyticsID(id)
}
func (s *Service) priceBinding(ctx context.Context, id uint, generation int64) (string, error) {
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return "", err
	}
	if inst.Generation != generation {
		return "", ErrGenerationMismatch
	}
	if s.deps.AnalyticsStore == nil {
		return "", errProjectionMiss
	}
	return s.binding(*inst)
}
func (s *Service) PutReferencePrice(ctx context.Context, id uint, scope string, scopeID int64, in PutReferencePriceInput) (*ReferencePriceView, error) {
	if !validPriceScope(scope, scopeID) || in.Generation <= 0 || in.Currency != "CNY" && in.Currency != "USD" || val(in.MonthlyMinor) < 0 || *in.MonthlyMinor > 1e9 || in.CNYPerUSDMicros != nil && (in.Currency == "USD" || *in.CNYPerUSDMicros < 1 || *in.CNYPerUSDMicros > 1e9) {
		return nil, ErrInvalidInput
	}
	b, err := s.priceBinding(ctx, id, in.Generation)
	if err != nil {
		return nil, err
	}
	row := storage.SiteOpsReferencePrice{InstanceID: id, TargetBinding: b, Scope: scope, ScopeID: scopeID, Currency: in.Currency, MonthlyMinor: *in.MonthlyMinor, CNYPerUSDMicros: in.CNYPerUSDMicros}
	if err = s.deps.AnalyticsStore.PutReferencePrice(ctx, in.Generation, &row); err != nil {
		return nil, err
	}
	v := priceView(row)
	return &v, nil
}
func (s *Service) ClearReferencePrice(ctx context.Context, id uint, scope string, scopeID, generation int64) error {
	if !validPriceScope(scope, scopeID) || generation <= 0 {
		return ErrInvalidInput
	}
	b, err := s.priceBinding(ctx, id, generation)
	if err != nil {
		return err
	}
	return s.deps.AnalyticsStore.ClearReferencePrice(ctx, id, generation, b, scope, scopeID)
}

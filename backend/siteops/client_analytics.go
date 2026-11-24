package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var _ P2AnalyticsClient = (*Client)(nil)
var _ BackoffAwareClient = (*Client)(nil)

type adminBackoffState struct {
	baseURL string
	credGen int64
	until   time.Time
	streak  int
}
type backoffError struct{ until time.Time }

func (e *backoffError) Error() string { return ErrRateLimited.Error() }
func (e *backoffError) Unwrap() error { return ErrRateLimited }

type p2CapOverlay struct {
	generation, credGen int64
	caps                CapabilitiesMap
}

func (c *Client) headroomLocked(id uint, limit int) (int, int, int) {
	now := time.Now()
	if limit <= 0 {
		limit = c.runtime.site.DefaultAuditBudgetPerDay
	}
	ml := max(12, limit/240)
	mr := min(max(5, ml/4), ml/2)
	dr := min(max(500, limit/10), limit/2)
	w := c.budgets[id]
	if w == nil {
		w = &auditWindow{}
	}
	w.advance(now)
	g := &c.analyticsGlobalBudget
	g.advance(now)
	gl := c.runtime.site.DefaultAuditBudgetPerDay
	return max(0, min(ml-mr-len(w.recent)-w.pending, limit-dr-w.used-w.pending, max(12, gl/240)-len(g.recent)-g.pending, gl-g.used-g.pending)), mr, dr
}
func (c *Client) BudgetHeadroomForAnalytics(id uint, limit int) (int, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.headroomLocked(id, limit)
}
func (c *Client) AdminBackoff(id uint, cred int64) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.backoffs[id]
	return s.until, s.credGen == cred && time.Now().Before(s.until)
}
func (c *Client) backoffLocked(inst TargetEndpoint) (time.Time, bool) {
	s := c.backoffs[inst.InstanceID]
	return s.until, s.credGen == inst.CredGen && (s.baseURL == "" || s.baseURL == inst.BaseURL) && time.Now().Before(s.until)
}
func (c *Client) SeedAdminBackoff(id uint, cred int64, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.backoffs[id]
	if until.After(time.Now()) && (s.credGen != cred || until.After(s.until)) {
		if s.credGen != cred {
			s = adminBackoffState{credGen: cred}
		}
		s.until = until
		c.backoffs[id] = s
	}
}
func (c *Client) InvalidateAnalyticsScope(id uint, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reason == "target" || reason == "delete" {
		delete(c.backoffs, id)
	}
	c.invalidateP2Locked(id, ErrGenerationMismatch)
}
func (c *Client) invalidateP2Locked(id uint, cause error) {
	if id == 0 {
		clear(c.p2Caps)
	} else {
		delete(c.p2Caps, id)
	}
	c.analytics.invalidate(id, cause)
	for k, f := range c.flights {
		if (id == 0 || k.instanceID == id) && k.policy == cachePolicyAnalyticsLRU {
			f.cancel()
			delete(c.flights, k)
		}
	}
}
func capError(s CapabilityState) error {
	switch s {
	case CapabilityForbidden:
		return ErrForbidden
	case CapabilityOpsDisabled:
		return ErrOpsDisabled
	case CapabilityUnsupported:
		return ErrUnsupportedRoute
	}
	return nil
}
func capValid(ep string, v EndpointCapability, now time.Time) bool {
	if capError(v.State) == nil {
		return true
	}
	ttl := 5 * time.Minute
	if ep == EndpointChannelMonitorV2 && v.State == CapabilityOpsDisabled {
		ttl = 6 * time.Hour
	}
	return now.Before(v.CheckedAt.Add(ttl))
}
func (c *Client) negativeCapLocked(inst TargetEndpoint, ep string) (EndpointCapability, bool) {
	o := c.p2Caps[inst.InstanceID]
	v := o.caps[ep]
	return v, o.generation == inst.Generation && o.credGen == inst.CredGen && capError(v.State) != nil && capValid(ep, v, time.Now())
}
func (c *Client) ObservedP2Capabilities(id uint, gen, cred int64) CapabilitiesMap {
	c.mu.Lock()
	defer c.mu.Unlock()
	o := c.p2Caps[id]
	out := CapabilitiesMap{}
	if o.generation == gen && o.credGen == cred {
		for ep, v := range o.caps {
			if capValid(ep, v, time.Now()) {
				out[ep] = v
			}
		}
	}
	return out
}
func (c *Client) recordAdminLocked(inst TargetEndpoint, key flightKey, start time.Time, r *fetchResult) {
	now := time.Now().UTC()
	s := c.backoffs[inst.InstanceID]
	if s.credGen != inst.CredGen || s.baseURL != "" && s.baseURL != inst.BaseURL {
		s = adminBackoffState{}
	}
	if r.meta.HTTPStatus == 429 {
		s.streak = min(6, s.streak+1)
		d := min(10*time.Minute, 30*time.Second<<uint(s.streak-1))
		if r.meta.RetryAfter != nil {
			d = *r.meta.RetryAfter
		}
		until := now.Add(d)
		if until.After(s.until) {
			s.until = until
		}
		s.baseURL, s.credGen = inst.BaseURL, inst.CredGen
		c.backoffs[inst.InstanceID] = s
		r.meta.RetryAfter = ptr(s.until.Sub(now))
	} else if !start.Before(s.until) && (r.err == nil || r.meta.HTTPStatus == 403 || r.meta.HTTPStatus == 404) {
		delete(c.backoffs, inst.InstanceID)
	}
	if key.policy != cachePolicyAnalyticsLRU {
		return
	}
	if errors.Is(r.err, ErrUnauthorized) || errors.Is(r.err, ErrForbidden) {
		before := c.p2Caps[inst.InstanceID]
		c.invalidateP2Locked(inst.InstanceID, r.err)
		if errors.Is(r.err, ErrForbidden) && before.generation == inst.Generation && before.credGen == inst.CredGen {
			c.p2Caps[inst.InstanceID] = before
		}
		if errors.Is(r.err, ErrForbidden) {
			c.analytics.dropEndpoint(inst.InstanceID, key.endpoint)
		}
		if errors.Is(r.err, ErrUnauthorized) {
			return
		}
	}
	if r.meta.HTTPStatus == 0 || errors.Is(r.err, context.Canceled) || r.meta.HTTPStatus == 404 && (key.endpoint == EndpointUserAPIKeys || key.endpoint == EndpointSubscriptionProgress) {
		return
	}
	o := c.p2Caps[inst.InstanceID]
	if o.generation != inst.Generation || o.credGen != inst.CredGen || o.caps == nil {
		o = p2CapOverlay{inst.Generation, inst.CredGen, CapabilitiesMap{}}
	}
	o.caps[key.endpoint] = probeCapability(r.meta.HTTPStatus, r.err, now)
	c.p2Caps[inst.InstanceID] = o
}
func channelDisabled(r io.Reader) bool {
	b, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil {
		return false
	}
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, "{") {
		var w struct{ Message, Error, Reason string }
		if json.Unmarshal(b, &w) != nil {
			return false
		}
		s = w.Message + " " + w.Error + " " + w.Reason
	}
	return strings.Contains(strings.ToLower(s), "channel monitor feature is disabled")
}
func fetchAnalytics[T any](c *Client, ctx context.Context, inst TargetEndpoint, ep, path string, params url.Values, body []byte, parse func([]byte) (T, error)) (T, UpstreamFetchMeta, error) {
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	return fetchSpec(c, ctx, inst, requestSpec{endpoint: ep, method: method, path: adminPath + path, body: body, parserTag: ep, policy: cachePolicyAnalyticsLRU}, parse)
}
func pageParams(page, size int) url.Values {
	return url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
}
func dateParams(r AnalyticsDateRange) url.Values {
	return url.Values{"start_date": {r.StartDate}, "end_date": {r.EndDate}, "timezone": {"Asia/Shanghai"}}
}
func batchIDs(ids []int64, limit int) ([]int64, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > limit || len(ids) > 0 && ids[0] <= 0 {
		return nil, ErrInvalidInput
	}
	return ids, nil
}
func (c *Client) FetchAnalyticsGroups(ctx context.Context, inst TargetEndpoint) ([]SanitizedGroupInfo, UpstreamFetchMeta, error) {
	ep := EndpointAnalyticsGroups
	return fetchAnalytics(c, ctx, inst, ep, "/groups/all", nil, nil, ParseGroupsAll)
}
func (c *Client) FetchUsersPage(ctx context.Context, inst TargetEndpoint, page int) (AnalyticsPage[SanitizedAnalyticsUser], UpstreamFetchMeta, error) {
	ep := EndpointUsersPage
	return fetchAnalytics(c, ctx, inst, ep, "/users", pageParams(page, 100), nil, func(raw []byte) (AnalyticsPage[SanitizedAnalyticsUser], error) { return parseUsersPage(raw, page) })
}
func (c *Client) FetchUserConcurrency(ctx context.Context, inst TargetEndpoint) (SanitizedUserConcurrencyMap, UpstreamFetchMeta, error) {
	ep := EndpointUserConcurrency
	return fetchAnalytics(c, ctx, inst, ep, "/ops/user-concurrency", nil, nil, ParseUserConcurrency)
}
func (c *Client) FetchSubscriptionsPage(ctx context.Context, inst TargetEndpoint, page int) (AnalyticsPage[SanitizedSubscription], UpstreamFetchMeta, error) {
	ep := EndpointSubscriptionsPage
	return fetchAnalytics(c, ctx, inst, ep, "/subscriptions", pageParams(page, 100), nil, func(raw []byte) (AnalyticsPage[SanitizedSubscription], error) {
		return parseSubscriptionsPage(raw, page)
	})
}
func (c *Client) FetchSubscriptionProgress(ctx context.Context, inst TargetEndpoint, id int64) (*SanitizedSubscriptionProgress, UpstreamFetchMeta, error) {
	ep := EndpointSubscriptionProgress
	return fetchAnalytics(c, ctx, inst, ep, fmt.Sprintf("/subscriptions/%d/progress", id), nil, nil, ParseSubscriptionProgress)
}
func (c *Client) FetchUsageStats(ctx context.Context, inst TargetEndpoint, q AnalyticsStatsQuery) (*SanitizedUsageStats, UpstreamFetchMeta, error) {
	ep := EndpointUsageStats
	v := dateParams(q.AnalyticsDateRange)
	v.Set("user_id", strconv.FormatInt(q.UserID, 10))
	if q.GroupID != nil {
		v.Set("group_id", strconv.FormatInt(*q.GroupID, 10))
	}
	if q.BillingType != nil {
		v.Set("billing_type", strconv.Itoa(*q.BillingType))
	}
	return fetchAnalytics(c, ctx, inst, ep, "/usage/stats", v, nil, ParseUsageStats)
}
func (c *Client) FetchUserAPIKeysPage(ctx context.Context, inst TargetEndpoint, id int64, page int) (AnalyticsPage[SanitizedUserAPIKey], UpstreamFetchMeta, error) {
	ep := EndpointUserAPIKeys
	return fetchAnalytics(c, ctx, inst, ep, fmt.Sprintf("/users/%d/api-keys", id), pageParams(page, 20), nil, func(raw []byte) (AnalyticsPage[SanitizedUserAPIKey], error) { return parseUserAPIKeysPage(raw, page) })
}
func (c *Client) FetchDashboardModels(ctx context.Context, inst TargetEndpoint, r AnalyticsDateRange, mismatch bool) (SanitizedModelRows, UpstreamFetchMeta, error) {
	ep := EndpointDashboardModels
	v := dateParams(r)
	if mismatch {
		ep = EndpointModelMismatch
		v.Set("upstream_model_mismatch", "true")
	}
	return fetchAnalytics(c, ctx, inst, ep, "/dashboard/models", v, nil, ParseDashboardModels)
}
func (c *Client) FetchDashboardGroups(ctx context.Context, inst TargetEndpoint, r AnalyticsDateRange) (SanitizedGroupMarketRows, UpstreamFetchMeta, error) {
	ep := EndpointDashboardGroups
	return fetchAnalytics(c, ctx, inst, ep, "/dashboard/groups", dateParams(r), nil, ParseDashboardGroups)
}
func (c *Client) FetchRiskControlStatus(ctx context.Context, inst TargetEndpoint) (*SanitizedRiskControlStatus, UpstreamFetchMeta, error) {
	ep := EndpointRiskControlStatus
	return fetchAnalytics(c, ctx, inst, ep, "/risk-control/status", nil, nil, ParseRiskControlStatus)
}
func (c *Client) FetchBackupsList(ctx context.Context, inst TargetEndpoint) (SanitizedBackupsResult, UpstreamFetchMeta, error) {
	ep := EndpointBackups
	return fetchAnalytics(c, ctx, inst, ep, "/backups", nil, nil, ParseBackupsList)
}
func (c *Client) ProbeChannelMonitorV2(ctx context.Context, inst TargetEndpoint) (*SanitizedChannelAvailability, UpstreamFetchMeta, error) {
	ep := EndpointChannelMonitorV2
	return fetchAnalytics(c, ctx, inst, ep, "/channel-monitor-v2/snapshot", nil, nil, ParseChannelMonitorAvailability)
}
func (c *Client) FetchUsersUsageBatch(ctx context.Context, inst TargetEndpoint, ids []int64) (map[int64]SanitizedUserQuickUsage, UpstreamFetchMeta, error) {
	ids, err := batchIDs(ids, 100)
	meta := UpstreamFetchMeta{Endpoint: EndpointUsersUsage}
	if err != nil {
		return nil, meta, err
	}
	if len(ids) == 0 {
		return map[int64]SanitizedUserQuickUsage{}, meta, nil
	}
	b, _ := json.Marshal(map[string][]int64{"user_ids": ids})
	return fetchAnalytics(c, ctx, inst, EndpointUsersUsage, "/dashboard/users-usage", nil, b, ParseUsersUsageBatch)
}
func (c *Client) FetchAPIKeysUsageBatch(ctx context.Context, inst TargetEndpoint, ids []int64) (map[int64]SanitizedAPIKeyUsage, UpstreamFetchMeta, error) {
	ids, err := batchIDs(ids, 20)
	meta := UpstreamFetchMeta{Endpoint: EndpointAPIKeysUsage}
	if err != nil {
		return nil, meta, err
	}
	if len(ids) == 0 {
		return map[int64]SanitizedAPIKeyUsage{}, meta, nil
	}
	b, _ := json.Marshal(map[string][]int64{"api_key_ids": ids})
	return fetchAnalytics(c, ctx, inst, EndpointAPIKeysUsage, "/dashboard/api-keys-usage", nil, b, ParseAPIKeysUsageBatch)
}

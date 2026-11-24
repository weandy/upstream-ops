package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/bejix/upstream-ops/backend/config"
	"sync/atomic"
	"testing"
	"time"
)

type countedAnalytics struct {
	siteOpsTestClient
	P2AnalyticsClient
	calls     atomic.Int32
	available int
	now       func() time.Time
}

func (c *countedAnalytics) meta() UpstreamFetchMeta {
	c.calls.Add(1)
	return UpstreamFetchMeta{HTTPStatus: 200, ObservedAt: c.now(), VerifiedAt: c.now()}
}
func (c *countedAnalytics) FetchUsersPage(context.Context, TargetEndpoint, int) (AnalyticsPage[SanitizedAnalyticsUser], UpstreamFetchMeta, error) {
	v := AnalyticsPage[SanitizedAnalyticsUser]{Total: ptr(int64(5))}
	for id := int64(1); id <= 5; id++ {
		v.Items = append(v.Items, SanitizedAnalyticsUser{ID: id, BalanceUSD: ptr(20.), CreatedAt: ptr(c.now().AddDate(-1, 0, 0))})
	}
	return v, c.meta(), nil
}
func (c *countedAnalytics) FetchSubscriptionsPage(context.Context, TargetEndpoint, int) (AnalyticsPage[SanitizedSubscription], UpstreamFetchMeta, error) {
	p := AnalyticsPage[SanitizedSubscription]{Total: ptr(int64(2))}
	for i, days := range []int{30, 90} {
		start := c.now().AddDate(0, 0, -days+10)
		p.Items = append(p.Items, SanitizedSubscription{ID: int64(i + 1), UserID: int64(i + 1), GroupID: 10, Status: "active", StartsAt: &start, ExpiresAt: ptr(start.AddDate(0, 0, days))})
	}
	return p, c.meta(), nil
}
func (c *countedAnalytics) FetchSubscriptionProgress(context.Context, TargetEndpoint, int64) (*SanitizedSubscriptionProgress, UpstreamFetchMeta, error) {
	return &SanitizedSubscriptionProgress{}, c.meta(), nil
}
func (c *countedAnalytics) FetchUsageStats(context.Context, TargetEndpoint, AnalyticsStatsQuery) (*SanitizedUsageStats, UpstreamFetchMeta, error) {
	return &SanitizedUsageStats{Actual: ptr(20.), Requests: ptr(int64(50))}, c.meta(), nil
}
func (c *countedAnalytics) FetchRiskControlStatus(context.Context, TargetEndpoint) (*SanitizedRiskControlStatus, UpstreamFetchMeta, error) {
	return &SanitizedRiskControlStatus{Enabled: ptr(false)}, c.meta(), nil
}
func (c *countedAnalytics) FetchBackupsList(context.Context, TargetEndpoint) (SanitizedBackupsResult, UpstreamFetchMeta, error) {
	return SanitizedBackupsResult{}, c.meta(), nil
}
func (c *countedAnalytics) FetchUserConcurrency(context.Context, TargetEndpoint) (SanitizedUserConcurrencyMap, UpstreamFetchMeta, error) {
	return SanitizedUserConcurrencyMap{}, c.meta(), nil
}
func (c *countedAnalytics) FetchUsersUsageBatch(context.Context, TargetEndpoint, []int64) (map[int64]SanitizedUserQuickUsage, UpstreamFetchMeta, error) {
	return map[int64]SanitizedUserQuickUsage{}, c.meta(), nil
}
func (c *countedAnalytics) FetchDashboardModels(context.Context, TargetEndpoint, AnalyticsDateRange, bool) (SanitizedModelRows, UpstreamFetchMeta, error) {
	return SanitizedModelRows{}, c.meta(), nil
}
func (c *countedAnalytics) FetchDashboardGroups(context.Context, TargetEndpoint, AnalyticsDateRange) (SanitizedGroupMarketRows, UpstreamFetchMeta, error) {
	return SanitizedGroupMarketRows{}, c.meta(), nil
}
func (c *countedAnalytics) FetchAnalyticsGroups(context.Context, TargetEndpoint) ([]SanitizedGroupInfo, UpstreamFetchMeta, error) {
	return []SanitizedGroupInfo{{ID: 10, Name: "Portal"}}, c.meta(), nil
}
func (c *countedAnalytics) AdminBackoff(uint, int64) (time.Time, bool) { return time.Time{}, false }
func (c *countedAnalytics) SeedAdminBackoff(uint, int64, time.Time)    {}
func (c *countedAnalytics) BudgetHeadroomForAnalytics(uint, int) (int, int, int) {
	return c.available, 8, 800
}
func (c *countedAnalytics) ObservedP2Capabilities(uint, int64, int64) CapabilitiesMap {
	return CapabilitiesMap{EndpointUsersPage: {State: CapabilitySupported}}
}
func (c *countedAnalytics) InvalidateAnalyticsScope(uint, string) {}
func TestAnalyticsBudgetLedgerAndOrchestration(t *testing.T) {
	ctx := context.Background()
	c := &countedAnalytics{available: 25}
	svc, _, clock := newTestSiteOpsService(t, c, siteOpsTestProber(func(context.Context, TargetEndpoint, CapabilitiesMap) (*ProbeReport, error) {
		return &ProbeReport{Capabilities: CapabilitiesMap{}}, nil
	}))
	c.now = clock.now
	id := addTestSiteOpsInstance(t, svc, ctx).ID
	refreshTestSiteOps(t, ctx, svc, id)
	clock.add(time.Minute)
	refreshTestSiteOps(t, ctx, svc, id)
	if c.calls.Load() != 0 {
		t.Fatal("P2 出现在后台采集")
	}
	check := func(want int32, fn func() error) {
		t.Helper()
		before := c.calls.Load()
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		if n := c.calls.Load() - before; n != want {
			t.Fatalf("上游增量 %d，期望 %d", n, want)
		}
	}
	users := func() error {
		_, err := svc.GetAnalyticsUsers(ctx, id, AnalyticsUsersQuery{Page: 1, TopN: 5})
		return err
	}
	lights := func() error { _, err := svc.GetAnalyticsLights(ctx, id, false); return err }
	check(2, lights)
	check(14, users)
	check(0, lights)
	check(0, users)
	clock.add(3 * time.Minute)
	check(2, lights)
	check(0, users)
	check(0, func() error {
		v, err := svc.GetAnalyticsSubscriptions(ctx, id, AnalyticsSubscriptionsQuery{Page: 1, Status: "all"})
		if err == nil && len(v.Subscriptions.Items) != 2 {
			t.Fatal("subscriptions")
		}
		return err
	})
	for i, n := range []int32{2, 3} {
		check(n, func() error {
			v, err := svc.GetAnalyticsSubscriptionDetail(ctx, id, int64(i+1), 1, false, false)
			if err == nil && v.ContinuousTerm.Usage == nil {
				t.Fatal("term missing")
			}
			return err
		})
	}
	check(3, func() error { _, err := svc.GetAnalyticsMarket(ctx, id, "30d", false, false); return err })
	check(0, func() error {
		_, err := svc.PutReferencePrice(ctx, id, "group", 10, PutReferencePriceInput{1, "CNY", ptr(int64(7200)), nil})
		if err != nil {
			return err
		}
		_, err = svc.UpdateAnalyticsConfig(ctx, id, DefaultAnalyticsConfig())
		if err != nil {
			return err
		}
		v, err := svc.GetAnalyticsSubscriptionDetail(ctx, id, 1, 1, false, true)
		if err == nil && val(v.Subscription.PricingCompare.CycleRefUSD) != 10 {
			t.Fatal("本地配置重算")
		}
		return err
	})
	check(0, func() error {
		r, e := svc.ProbeInstance(ctx, id)
		if e == nil && r.Capabilities[EndpointUsersPage].State != CapabilitySupported {
			t.Fatal("missing overlay")
		}
		return e
	})
	c.available = 0
	clock.add(12 * time.Minute)
	v, e := svc.GetAnalyticsUsers(ctx, id, AnalyticsUsersQuery{Page: 1, TopN: 5})
	if e != nil || v.Summary.BudgetDeferredUsers != 5 || len(v.Users.Items) != 5 {
		t.Fatal("deferred budget", e)
	}
}

type subscriptionWindowClient struct {
	countedAnalytics
	start, end time.Time
}

func (c *subscriptionWindowClient) FetchSubscriptionsPage(context.Context, TargetEndpoint, int) (AnalyticsPage[SanitizedSubscription], UpstreamFetchMeta, error) {
	return AnalyticsPage[SanitizedSubscription]{Total: ptr(int64(1)), Items: []SanitizedSubscription{{ID: 1, UserID: 2, GroupID: 3, Status: "active", StartsAt: &c.start, ExpiresAt: &c.end}}}, c.meta(), nil
}
func (c *subscriptionWindowClient) FetchSubscriptionProgress(context.Context, TargetEndpoint, int64) (*SanitizedSubscriptionProgress, UpstreamFetchMeta, error) {
	return &SanitizedSubscriptionProgress{ExpiresAt: &c.end, Daily: &RollingWindowView{ResetsInSeconds: ptr(int64(120))}}, c.meta(), nil
}
func TestAnalyticsSubscriptionDetailExpiryAndRollover(t *testing.T) {
	ctx := context.Background()
	for _, expired := range []bool{true, false} {
		c := &subscriptionWindowClient{}
		svc, _, clock := newTestSiteOpsService(t, c, nil)
		c.now = clock.now
		id := addTestSiteOpsInstance(t, svc, ctx).ID
		c.start = clock.now().Add(-cycleDuration)
		c.end = c.start.Add(2 * cycleDuration)
		if expired {
			c.end = daySH(clock.now())
			c.start = c.end.Add(-cycleDuration)
		}
		var first int32
		for i := 0; i < 2; i++ {
			v, err := svc.GetAnalyticsSubscriptionDetail(ctx, id, 1, 1, false, false)
			if err != nil {
				t.Fatal(err)
			}
			progress := v.Domains["subscription_progress"]
			reset := v.Subscription.DailyWindow.ResetsAt
			if progress.Quality != QualityTrusted || reset == nil || !reset.Equal(clock.now().Add(2*time.Minute)) || !progress.ExpiresAt.Equal(*reset) {
				t.Fatal(progress, reset)
			}
			u := v.CurrentSubCycle.Usage
			if expired {
				if u == nil || u.EndDate != c.end.Add(-1).In(locUTC8).Format(time.DateOnly) {
					t.Fatal("午夜到期查询越界", u)
				}
			} else if u == nil || val(u.ActualUSD) != 0 || val(u.Requests) != 0 || u.StartDate != u.EndDate || u.EndDate != clock.now().In(locUTC8).Format(time.DateOnly) || u.Timezone != "Asia/Shanghai" || u.Cache.InputMode != "unverified" || u.Cache.TokenQuality != QualityUnavailable {
				t.Fatal("切期零窗", u)
			}
			if i == 0 {
				first = c.calls.Load()
			} else if c.calls.Load() != first {
				t.Fatal("二次读取发起上游请求")
			}
		}
	}
}

type barrierAnalytics struct {
	countedAnalytics
	started, release chan struct{}
	reads            atomic.Int32
	riskErr          error
}

func (c *barrierAnalytics) FetchSubscriptionsPage(context.Context, TargetEndpoint, int) (AnalyticsPage[SanitizedSubscription], UpstreamFetchMeta, error) {
	id := int64(22)
	if c.reads.Add(1) == 1 {
		close(c.started)
		<-c.release
		id = 11
	}
	return AnalyticsPage[SanitizedSubscription]{Total: ptr(int64(1)), Items: []SanitizedSubscription{{ID: id}}}, c.meta(), nil
}
func (c *barrierAnalytics) FetchRiskControlStatus(context.Context, TargetEndpoint) (*SanitizedRiskControlStatus, UpstreamFetchMeta, error) {
	return nil, UpstreamFetchMeta{}, c.riskErr
}
func TestAnalyticsServiceInFlightFences(t *testing.T) {
	for _, action := range []string{"rename", "disable", "key", "delete", "reload", "401", "403", "403-twice", "403-then-401"} {
		t.Run(action, func(t *testing.T) {
			c := &barrierAnalytics{started: make(chan struct{}), release: make(chan struct{})}
			svc, _, clock := newTestSiteOpsService(t, c, nil)
			c.now = clock.now
			ctx := context.Background()
			id := addTestSiteOpsInstance(t, svc, ctx).ID
			query := AnalyticsSubscriptionsQuery{Page: 1, Status: "all"}
			var result *AnalyticsSubscriptionsResponse
			done := make(chan error, 1)
			go func() { var err error; result, err = svc.GetAnalyticsSubscriptions(ctx, id, query); done <- err }()
			<-c.started
			var err error
			want := ErrGenerationMismatch
			switch action {
			case "rename":
				_, err = svc.UpdateInstance(ctx, id, UpdateInstanceInput{Name: ptr("Portal Two")})
			case "disable":
				_, err = svc.UpdateInstance(ctx, id, UpdateInstanceInput{Enabled: ptr(false)})
			case "key":
				_, err = svc.UpdateInstance(ctx, id, UpdateInstanceInput{APIKey: ptr("replacement-fixture")})
			case "delete":
				err = svc.DeleteInstance(ctx, id)
			case "reload":
				svc.ApplyRuntimeConfig(true, svc.CurrentConfig(), config.ProxyConfig{}, config.UpstreamConfig{})
			default:
				c.riskErr = ErrForbidden
				if action == "401" {
					c.riskErr = ErrUnauthorized
				}
				_, err = svc.GetAnalyticsLights(ctx, id, false)
				if action == "403-twice" || action == "403-then-401" {
					if err != nil {
						t.Fatal(err)
					}
					if action == "403-then-401" {
						c.riskErr = ErrUnauthorized
					}
					_, err = svc.GetAnalyticsLights(ctx, id, false)
				}
				if c.riskErr == ErrUnauthorized {
					if !errors.Is(err, ErrUnauthorized) {
						t.Fatal("401 未上报", err)
					}
					want = ErrUnauthorized
					err = nil
				} else {
					want = nil
				}
			}
			close(c.release)
			if err != nil {
				t.Fatal(err)
			}
			if got := <-done; !errors.Is(got, want) {
				t.Fatalf("err = %v, want %v", got, want)
			}
			if want != nil && result != nil {
				t.Fatal("旧请求返回了目录")
			}
			if want == nil && (result == nil || len(result.Subscriptions.Items) != 1 || result.Subscriptions.Items[0].SubscriptionID != 22) {
				t.Fatal("403 后未重读有效目录", result)
			}
			svc.analytics.mu.Lock()
			defer svc.analytics.mu.Unlock()
			for key, elem := range svc.analytics.entries {
				if key.endpoint != EndpointSubscriptionsPage {
					continue
				}
				var page AnalyticsPage[SanitizedSubscription]
				if e := json.Unmarshal(elem.Value.(leafEntry).data, &page); e != nil || len(page.Items) != 1 || page.Items[0].ID != 22 || !svc.analytics.validLocked(key.analyticsScope) {
					t.Fatal("在途旧投影写回", page, e)
				}
			}
		})
	}
}

func TestAnalyticsServiceQueryValidation(t *testing.T) {
	svc := new(Service)
	ctx := context.Background()
	for _, check := range []func() error{
		func() error { _, e := svc.GetAnalyticsMarket(ctx, 1, "2d", false, false); return e },
		func() error {
			_, e := svc.GetAnalyticsSubscriptions(ctx, 1, AnalyticsSubscriptionsQuery{Page: 1, Status: "bad"})
			return e
		},
		func() error {
			_, e := svc.GetAnalyticsSubscriptions(ctx, 1, AnalyticsSubscriptionsQuery{Page: 10001, Status: "active"})
			return e
		},
		func() error {
			_, e := svc.GetAnalyticsSubscriptions(ctx, 1, AnalyticsSubscriptionsQuery{Page: 1, Status: "all", GroupID: ptr(int64(0))})
			return e
		},
		func() error { _, e := svc.GetAnalyticsUserDetail(ctx, 1, 0, 1, 1, false); return e },
		func() error { _, e := svc.GetAnalyticsUserDetail(ctx, 1, 1, 0, 1, false); return e },
		func() error { _, e := svc.GetAnalyticsUserDetail(ctx, 1, 1, 1, 10001, false); return e },
		func() error {
			_, e := svc.GetAnalyticsSubscriptionDetail(ctx, 1, 9007199254740992, 1, false, false)
			return e
		},
		func() error { _, e := svc.GetAnalyticsSubscriptionDetail(ctx, 1, 1, 0, false, false); return e },
		func() error { _, e := svc.GetAnalyticsMarket(ctx, 0, "7d", false, false); return e },
	} {
		if err := check(); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
}

type enrichmentClient struct{ countedAnalytics }

func (c *enrichmentClient) FetchUsersPage(ctx context.Context, inst TargetEndpoint, page int) (AnalyticsPage[SanitizedAnalyticsUser], UpstreamFetchMeta, error) {
	p, m, e := c.countedAnalytics.FetchUsersPage(ctx, inst, page)
	p.Items[0].CreatedAt = nil
	p.Items[1].CreatedAt = ptr(c.now().Add(time.Hour))
	return p, m, e
}
func TestAnalyticsUserEnrichmentStates(t *testing.T) {
	c := &enrichmentClient{}
	svc, _, clock := newTestSiteOpsService(t, c, nil)
	c.now = clock.now
	c.available = 4
	ctx := context.Background()
	id := addTestSiteOpsInstance(t, svc, ctx).ID
	for _, only := range []bool{false, true} {
		before := c.calls.Load()
		v, err := svc.GetAnalyticsUsers(ctx, id, AnalyticsUsersQuery{Page: 1, TopN: 5, CacheOnly: only})
		if err != nil {
			t.Fatal(err)
		}
		if v.Domains["user_stats"].Quality != QualityTrusted || v.Summary.EvaluatedUsers != 2 || len(v.Users.Items) != 5 {
			t.Fatal(v.Domains, v.Summary)
		}
		deferred := "deferred_budget"
		if only {
			deferred = "not_loaded"
			if c.calls.Load() != before {
				t.Fatal("cache_only 触发上游")
			}
		} else if c.calls.Load()-before != 7 {
			t.Fatalf("五个目录/基础请求和两个七日窗，实际 %d", c.calls.Load()-before)
		}
		for i, u := range v.Users.Items {
			if i < 2 {
				if u.AllTime == nil || u.AllTime.Quality != QualityUnavailable || u.Last7d == nil || u.EnrichmentState != "loaded" {
					t.Fatal(u)
				}
			} else if u.EnrichmentState != deferred {
				t.Fatal(i, u.EnrichmentState, deferred)
			}
		}
	}
}

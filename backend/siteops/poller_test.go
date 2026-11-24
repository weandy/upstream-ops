package siteops

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
)

type siteOpsTestClient struct {
	health      func(context.Context, TargetEndpoint) (*HealthProbeResult, error)
	overview    func(context.Context, TargetEndpoint) (*SanitizedOpsOverview, UpstreamFetchMeta, error)
	concurrency func(context.Context, TargetEndpoint) (*SanitizedConcurrencyPayload, UpstreamFetchMeta, error)
	resets      atomic.Int32
}

func (c *siteOpsTestClient) ProbeHealth(ctx context.Context, target TargetEndpoint) (*HealthProbeResult, error) {
	if c.health != nil {
		return c.health(ctx, target)
	}
	return &HealthProbeResult{Reachable: true, HTTPStatus: 200}, nil
}

func (c *siteOpsTestClient) FetchOpsSnapshot(ctx context.Context, target TargetEndpoint, _ CapabilitiesMap) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
	if c.overview != nil {
		return c.overview(ctx, target)
	}
	return &SanitizedOpsOverview{HealthScore: ptr(96), RedisOK: ptr(true)}, UpstreamFetchMeta{HTTPStatus: 200}, nil
}

func (c *siteOpsTestClient) FetchConcurrency(ctx context.Context, target TargetEndpoint) (*SanitizedConcurrencyPayload, UpstreamFetchMeta, error) {
	if c.concurrency != nil {
		return c.concurrency(ctx, target)
	}
	return &SanitizedConcurrencyPayload{Enabled: ptr(true),
		Accounts: map[int64]SanitizedAccountConcurrency{101: {Platform: "openai", CurrentInUse: 3, MaxCapacity: 5}},
		Groups:   map[int64]SanitizedGroupConcurrency{10: {CurrentInUse: 3, MaxCapacity: 5}},
	}, UpstreamFetchMeta{HTTPStatus: 200}, nil
}

func (*siteOpsTestClient) FetchAccountAvailability(context.Context, TargetEndpoint) (*SanitizedAvailabilityPayload, UpstreamFetchMeta, error) {
	return &SanitizedAvailabilityPayload{Enabled: ptr(true),
		Accounts: map[int64]SanitizedAccountAvailability{101: {Platform: "openai", Available: true}},
		Groups:   map[int64]SanitizedGroupAvailability{10: {TotalCount: 1, AvailableCount: 1}},
	}, UpstreamFetchMeta{HTTPStatus: 200}, nil
}

func (*siteOpsTestClient) FetchGroupsAll(context.Context, TargetEndpoint) ([]SanitizedGroupInfo, UpstreamFetchMeta, error) {
	return []SanitizedGroupInfo{{ID: 10, Name: "portal-group", Platform: "openai"}}, UpstreamFetchMeta{HTTPStatus: 200}, nil
}

func (*siteOpsTestClient) FetchSystemVersion(context.Context, TargetEndpoint) (*SanitizedVersionInfo, UpstreamFetchMeta, error) {
	return &SanitizedVersionInfo{Version: "v0.2.24"}, UpstreamFetchMeta{HTTPStatus: 200}, nil
}

func (*siteOpsTestClient) BudgetUsage(_ uint, limit int) AuditBudgetUsage {
	return AuditBudgetUsage{DailyLimit: limit}
}

func (c *siteOpsTestClient) ResetInstanceState(uint) { c.resets.Add(1) }

func (*siteOpsTestClient) UpdateRuntimeConfig(config.SiteOpsConfig, config.ProxyConfig, config.UpstreamConfig) {
}

func TestPollerRejectsLateResponsesAfterReconciliation(t *testing.T) {
	for _, change := range []string{"key", "url", "delete"} {
		t.Run(change, func(t *testing.T) {
			started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var oldInFlight atomic.Bool
			var overlapped atomic.Bool
			client := &siteOpsTestClient{overview: func(ctx context.Context, target TargetEndpoint) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
				if target.Generation == 1 {
					oldInFlight.Store(true)
					defer oldInFlight.Store(false)
					close(started)
					<-ctx.Done()
					close(canceled)
					<-release
					if change == "key" {
						return nil, UpstreamFetchMeta{HTTPStatus: 401}, ErrUnauthorized
					}
					return &SanitizedOpsOverview{HealthScore: ptr(1)}, UpstreamFetchMeta{HTTPStatus: 200}, nil
				}
				overlapped.Store(oldInFlight.Load())
				if change == "key" {
					return nil, UpstreamFetchMeta{HTTPStatus: 401}, ErrUnauthorized
				}
				return &SanitizedOpsOverview{HealthScore: ptr(90)}, UpstreamFetchMeta{HTTPStatus: 200}, nil
			}}
			svc, store, clock := newTestSiteOpsService(t, client, nil)
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			inst := addTestSiteOpsInstance(t, svc, ctx)
			result := make(chan error, 1)
			go func() { result <- svc.poller.TriggerRefresh(ctx, inst.ID) }()
			waitSiteOpsSignal(t, ctx, started)
			switch change {
			case "key":
				updated, err := svc.UpdateInstance(ctx, inst.ID, UpdateInstanceInput{APIKey: ptr("test-replacement")})
				if err != nil || updated.CredGen != 2 || updated.Generation != 2 {
					t.Fatalf("replace key: %+v, %v", updated, err)
				}
			case "url":
				updated, err := svc.UpdateInstance(ctx, inst.ID, UpdateInstanceInput{BaseURL: ptr("https://next.portal.example.com")})
				if err != nil || updated.CredGen != 1 || updated.Generation != 2 {
					t.Fatalf("replace URL: %+v, %v", updated, err)
				}
			case "delete":
				if err := svc.DeleteInstance(ctx, inst.ID); err != nil {
					t.Fatal(err)
				}
			}
			waitSiteOpsSignal(t, ctx, canceled)
			if client.resets.Load() != 1 {
				t.Fatalf("client state resets: %d", client.resets.Load())
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrGenerationMismatch) {
					t.Fatalf("cancelled generation: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if change == "delete" {
				once.Do(func() { close(release) })
				svc.Stop()
				if snap, err := store.GetLatestSnapshot(ctx, inst.ID); err != nil || snap != nil {
					t.Fatalf("late response resurrected deleted snapshot: %+v, %v", snap, err)
				}
				return
			}
			clock.add(time.Minute)
			// Queue the new refresh before releasing the previous in-flight request.
			go func() { result <- svc.poller.TriggerRefresh(ctx, inst.ID) }()
			once.Do(func() { close(release) })
			select {
			case err := <-result:
				if err != nil || overlapped.Load() {
					t.Fatalf("new generation overlapped old request: %t, %v", overlapped.Load(), err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			rows, err := store.ListSnapshotsSince(ctx, inst.ID, clock.now().Add(-time.Hour), 0)
			if err != nil || len(rows) != 1 || !rows[0].ObservedAt.Equal(clock.now()) {
				t.Fatalf("old response affected trend: %+v, %v", rows, err)
			}
			if change == "key" {
				current, err := store.GetInstance(ctx, inst.ID)
				if err != nil || current.LastStatus == string(StatusAuthError) {
					t.Fatalf("old cred_gen counted against new key: %+v, %v", current, err)
				}
				clock.add(time.Minute)
				if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusAuthError {
					t.Fatalf("two current-generation 401s failed to pause: %+v", got)
				}
			}
		})
	}
}

func TestPollerUnauthorizedPausesOnlyAdminAndCanRecover(t *testing.T) {
	var healthCalls, adminCalls atomic.Int32
	var authorized atomic.Bool
	client := &siteOpsTestClient{
		health: func(_ context.Context, target TargetEndpoint) (*HealthProbeResult, error) {
			healthCalls.Add(1)
			if target.APIKey != "" {
				return nil, errors.New("health received a credential")
			}
			return &HealthProbeResult{Reachable: true, HTTPStatus: 200}, nil
		},
		overview: func(context.Context, TargetEndpoint) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
			adminCalls.Add(1)
			if !authorized.Load() {
				return nil, UpstreamFetchMeta{HTTPStatus: 401}, ErrUnauthorized
			}
			return &SanitizedOpsOverview{HealthScore: ptr(90)}, UpstreamFetchMeta{HTTPStatus: 200}, nil
		},
	}
	svc, _, clock := newTestSiteOpsService(t, client, nil)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusDegraded {
		t.Fatalf("first 401 should only degrade: %+v", got)
	}
	clock.add(time.Minute)
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusAuthError {
		t.Fatalf("second 401 should pause: %+v", got)
	}
	clock.add(time.Minute)
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusAuthError || !got.Snapshot.Reachable || adminCalls.Load() != 2 || healthCalls.Load() != 3 {
		t.Fatalf("paused instance should probe health only: %+v, admin=%d health=%d", got, adminCalls.Load(), healthCalls.Load())
	}
	authorized.Store(true)
	clock.add(14 * time.Minute)
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusOnline || adminCalls.Load() != 3 {
		t.Fatalf("low-frequency auth retry failed to recover: %+v, admin=%d", got, adminCalls.Load())
	}
}

func TestPollerForbiddenIsEndpointLocal(t *testing.T) {
	var calls atomic.Int32
	client := &siteOpsTestClient{concurrency: func(context.Context, TargetEndpoint) (*SanitizedConcurrencyPayload, UpstreamFetchMeta, error) {
		calls.Add(1)
		return nil, UpstreamFetchMeta{HTTPStatus: 403}, ErrForbidden
	}}
	svc, store, clock := newTestSiteOpsService(t, client, nil)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	for range 2 {
		got := refreshTestSiteOps(t, ctx, svc, inst.ID)
		if got.Status != StatusOnline || got.Snapshot.FieldQuality[DomainConcurrency] != QualityUnauthorized ||
			got.Snapshot.FieldQuality[DomainAccountAvailability] != QualityTrusted || got.Snapshot.ConcUsed != nil {
			t.Fatalf("403 contaminated another domain: %+v", got.Snapshot)
		}
		clock.add(time.Minute)
	}
	current, err := store.GetInstance(ctx, inst.ID)
	if err != nil || calls.Load() != 1 || decodeCapabilities(current.CapabilitiesJSON)[EndpointOpsConcurrency].State != CapabilityForbidden {
		t.Fatalf("403 endpoint was not skipped: %+v, calls=%d, %v", current, calls.Load(), err)
	}
}

func TestPollerNotModifiedPreservesObservedAt(t *testing.T) {
	var calls atomic.Int32
	observed := testSiteOpsStartTime
	verified := observed.Add(time.Minute)
	client := &siteOpsTestClient{overview: func(context.Context, TargetEndpoint) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
		if calls.Add(1) == 1 {
			return &SanitizedOpsOverview{HealthScore: ptr(96)}, UpstreamFetchMeta{HTTPStatus: 200, ObservedAt: observed, VerifiedAt: observed}, nil
		}
		return nil, UpstreamFetchMeta{HTTPStatus: 304, NotModified: true, ObservedAt: observed, VerifiedAt: verified}, nil
	}}
	svc, _, clock := newTestSiteOpsService(t, client, nil)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	clock.add(time.Minute)
	got := refreshTestSiteOps(t, ctx, svc, inst.ID)
	if got.Snapshot == nil || !got.Snapshot.ObservedAt.Equal(observed) || !got.Snapshot.VerifiedAt.Equal(verified) || *got.Snapshot.HealthScore != 96 {
		t.Fatalf("304 changed the sample: %+v", got.Snapshot)
	}
	trends, err := svc.GetTrends(ctx, inst.ID, "1h")
	if err != nil || len(trends.Points) != 1 || !trends.Points[0].ObservedAt.Equal(observed) {
		t.Fatalf("304 added a trend point: %+v, %v", trends, err)
	}
}

func TestPollerFirstNotModifiedCanUseClientObservation(t *testing.T) {
	observed := testSiteOpsStartTime.Add(-time.Minute)
	client := &siteOpsTestClient{overview: func(context.Context, TargetEndpoint) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
		return &SanitizedOpsOverview{HealthScore: ptr(96)}, UpstreamFetchMeta{
			HTTPStatus: 304, NotModified: true, ObservedAt: observed, VerifiedAt: testSiteOpsStartTime,
		}, nil
	}}
	svc, _, _ := newTestSiteOpsService(t, client, nil)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	got := refreshTestSiteOps(t, ctx, svc, inst.ID)
	if got.Snapshot == nil || !got.Snapshot.ObservedAt.Equal(observed) || !got.Snapshot.VerifiedAt.Equal(testSiteOpsStartTime) {
		t.Fatalf("already observed client data was lost or retimed: %+v", got.Snapshot)
	}
}

func TestPollerRateLimitUsesRetryAfterAndExponentialBackoff(t *testing.T) {
	var calls atomic.Int32
	client := &siteOpsTestClient{concurrency: func(context.Context, TargetEndpoint) (*SanitizedConcurrencyPayload, UpstreamFetchMeta, error) {
		meta := UpstreamFetchMeta{HTTPStatus: 429}
		if calls.Add(1) == 1 {
			meta.RetryAfter = ptr(10 * time.Second)
		}
		return nil, meta, ErrRateLimited
	}}
	svc, store, clock := newTestSiteOpsService(t, client, nil)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	checkBackoff := func(duration time.Duration) {
		t.Helper()
		current, err := store.GetInstance(ctx, inst.ID)
		if err != nil || current.BackoffUntil == nil || !current.BackoffUntil.Equal(clock.now().Add(duration)) {
			t.Fatalf("backoff should be %s: %+v %v", duration, current, err)
		}
	}
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	checkBackoff(10 * time.Second)
	clock.add(5 * time.Second)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	if calls.Load() != 1 {
		t.Fatalf("retried during server backoff: %d", calls.Load())
	}
	clock.add(5 * time.Second)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	checkBackoff(30 * time.Second)
	clock.add(30 * time.Second)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	checkBackoff(time.Minute)
	clock.add(time.Minute)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	checkBackoff(120 * time.Second)
}

func waitSiteOpsSignal(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

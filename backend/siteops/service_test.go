package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

var testSiteOpsStartTime = time.Date(2026, 11, 24, 1, 30, 0, 0, time.UTC)

type siteOpsTestClock struct{ nanos atomic.Int64 }

func (c *siteOpsTestClock) now() time.Time             { return time.Unix(0, c.nanos.Load()).UTC() }
func (c *siteOpsTestClock) add(duration time.Duration) { c.nanos.Add(int64(duration)) }

type siteOpsTestValidator struct{}

func (siteOpsTestValidator) ValidateBaseURL(candidate string) (string, error) {
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
		(parsed.Hostname() != "portal.example.com" && !strings.HasSuffix(parsed.Hostname(), ".portal.example.com")) {
		return "", ErrOriginNotAllowed
	}
	return strings.TrimRight(candidate, "/"), nil
}

func (siteOpsTestValidator) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, ErrOriginNotAllowed
}

func (siteOpsTestValidator) UpdatePolicy(bool, []string, []string) {}

type siteOpsTestProber func(context.Context, TargetEndpoint, CapabilitiesMap) (*ProbeReport, error)

func (f siteOpsTestProber) ProbeInstance(ctx context.Context, target TargetEndpoint, caps CapabilitiesMap) (*ProbeReport, error) {
	return f(ctx, target, caps)
}

func newTestSiteOpsService(t *testing.T, client UpstreamClient, prober Prober) (*Service, Store, *siteOpsTestClock) {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{Path: filepath.Join(t.TempDir(), "site-ops.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewCipher("test-only-encryption-material")
	if err != nil {
		t.Fatal(err)
	}
	clock := &siteOpsTestClock{}
	clock.nanos.Store(testSiteOpsStartTime.UnixNano())
	store := NewStorageStore(storage.NewSiteOpsStore(db))
	svc := NewService(ServiceDeps{Store: store, Cipher: cipher, Validator: siteOpsTestValidator{}, Client: client,
		Prober: prober, AuthActive: true, Now: clock.now, Jitter: func(time.Duration) time.Duration { return time.Hour }})
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)
	return svc, store, clock
}

func addTestSiteOpsInstance(t *testing.T, svc *Service, ctx context.Context) *InstanceView {
	t.Helper()
	inst, err := svc.CreateInstance(ctx, CreateInstanceInput{Name: "Portal", BaseURL: "https://portal.example.com/", APIKey: "test-fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

func refreshTestSiteOps(t *testing.T, ctx context.Context, svc *Service, id uint) *RefreshResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := svc.RefreshInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestServiceValidatesStoresAndProjectsInstances(t *testing.T) {
	svc, store, _ := newTestSiteOpsService(t, &siteOpsTestClient{}, nil)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	stored, err := store.GetInstance(ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.APIKeyCipher == "" || stored.APIKeyCipher == "test-fixture-key" || inst.BaseURL != "https://portal.example.com" || !inst.APIKeySet {
		t.Fatal("instance validation or persistence failed")
	}
	view, err := json.Marshal(inst)
	if err != nil || strings.Contains(string(view), "test-fixture-key") || strings.Contains(string(view), stored.APIKeyCipher) {
		t.Fatal("instance view exposed credential material")
	}
	if _, err := svc.CreateInstance(ctx, CreateInstanceInput{Name: "Blocked", BaseURL: "https://blocked.example.com", APIKey: "test-fixture-key"}); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("unapproved origin: %v", err)
	}
	second, err := svc.CreateInstance(ctx, CreateInstanceInput{Name: "Portal!", BaseURL: inst.BaseURL, APIKey: "test-fixture-key", Enabled: ptr(false), SortOrder: -1})
	if err != nil || second.Slug != "portal-2" || second.Enabled {
		t.Fatalf("slug collision or disabled create: %+v, %v", second, err)
	}
	updated, err := svc.UpdateInstance(ctx, inst.ID, UpdateInstanceInput{APIKey: ptr(""), SortOrder: ptr(3)})
	if err != nil || updated.CredGen != 1 || updated.Generation != 2 {
		t.Fatalf("blank key should retain old credential: %+v, %v", updated, err)
	}
	current, err := store.GetInstance(ctx, inst.ID)
	if err != nil || current.APIKeyCipher != stored.APIKeyCipher {
		t.Fatal("empty key overwrote the ciphertext")
	}
	instances, err := svc.ListInstances(ctx)
	if err != nil || len(instances) != 2 || instances[0].ID != second.ID {
		t.Fatalf("instance list: %+v, %v", instances, err)
	}
}

func TestServiceWorksWithoutGlobalAuth(t *testing.T) {
	svc, _, clock := newTestSiteOpsService(t, &siteOpsTestClient{}, nil)
	ctx := context.Background()
	svc.ApplyRuntimeConfig(false, config.SiteOpsConfig{}, config.ProxyConfig{}, config.UpstreamConfig{})

	summary, err := svc.GetSummary(ctx)
	if err != nil || summary.AuthEnabled || !summary.PollingActive {
		t.Fatalf("expected active polling when global auth disabled: %+v, %v", summary, err)
	}
	inst := addTestSiteOpsInstance(t, svc, ctx)
	if inst.LastStatus == StatusPausedNoAuth {
		t.Fatalf("new instance should not be paused_no_auth: %+v", inst)
	}
	updated, err := svc.UpdateInstance(ctx, inst.ID, UpdateInstanceInput{SortOrder: ptr(2)})
	if err != nil || updated.SortOrder != 2 || updated.LastStatus == StatusPausedNoAuth {
		t.Fatalf("update without global auth failed: %+v, %v", updated, err)
	}
	clock.add(time.Minute)
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusOnline || got.Snapshot == nil {
		t.Fatalf("refresh without global auth failed: %+v", got)
	}
	if err := svc.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("delete without global auth failed: %v", err)
	}
}

func TestServiceRuntimeConfigHotReloadAndPollerPauseLifecycle(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	client := &siteOpsTestClient{overview: func(ctx context.Context, _ TargetEndpoint) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
		}
		return &SanitizedOpsOverview{HealthScore: ptr(95)}, UpstreamFetchMeta{HTTPStatus: 200}, nil
	}}
	svc, store, clock := newTestSiteOpsService(t, client, nil)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	// Applying runtime config hot-reloads flags without stopping active polling.
	svc.ApplyRuntimeConfig(false, config.SiteOpsConfig{AllowLoopback: true}, config.ProxyConfig{}, config.UpstreamConfig{})
	if !svc.CurrentConfig().AllowLoopback {
		t.Fatal("ApplyRuntimeConfig did not update AllowLoopback")
	}
	result := make(chan error, 1)
	go func() { result <- svc.poller.TriggerRefresh(ctx, inst.ID) }()
	waitSiteOpsSignal(t, ctx, started)
	// Explicitly pausing the poller cancels the in-flight worker and prevents stale writes.
	svc.poller.SetPollingAllowed(false)
	waitSiteOpsSignal(t, ctx, canceled)
	if err := svc.poller.TriggerRefresh(ctx, inst.ID); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("refresh during poller pause: %v", err)
	}
	if _, err := svc.ProbeInstance(ctx, inst.ID); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("probe during poller pause: %v", err)
	}
	if snap, err := store.GetLatestSnapshot(ctx, inst.ID); err != nil || snap != nil {
		t.Fatalf("snapshot from paused worker: %+v, %v", snap, err)
	}
	svc.poller.SetPollingAllowed(true)
	clock.add(time.Minute)
	once.Do(func() { close(release) })
	select {
	case err := <-result:
		if !errors.Is(err, ErrGenerationMismatch) {
			t.Fatalf("paused worker result: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusOnline || !got.Snapshot.ObservedAt.Equal(clock.now()) || calls.Load() != 2 {
		t.Fatalf("polling did not resume with one worker: %+v, calls=%d", got, calls.Load())
	}
	rows, err := store.ListSnapshotsSince(ctx, inst.ID, clock.now().Add(-time.Hour), 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("paused worker wrote after resumption: %d, %v", len(rows), err)
	}
}

func TestServiceProbeRecoversAuthenticationAndPersistsCapabilities(t *testing.T) {
	var authorized atomic.Bool
	client := &siteOpsTestClient{overview: func(context.Context, TargetEndpoint) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
		if !authorized.Load() {
			return nil, UpstreamFetchMeta{HTTPStatus: 401}, ErrUnauthorized
		}
		return &SanitizedOpsOverview{HealthScore: ptr(95)}, UpstreamFetchMeta{HTTPStatus: 200}, nil
	}}
	prober := siteOpsTestProber(func(context.Context, TargetEndpoint, CapabilitiesMap) (*ProbeReport, error) {
		return &ProbeReport{Reachability: ReachabilityReachable, Health: HealthProbeResult{Reachable: true},
			Capabilities: CapabilitiesMap{
				EndpointOpsSnapshotV2: {State: CapabilitySupported, HTTPStatus: 200},
				EndpointSystemVersion: {State: CapabilityForbidden, HTTPStatus: 403, Reason: "unsanitized-test-detail"},
			}}, nil
	})
	svc, store, clock := newTestSiteOpsService(t, client, prober)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	clock.add(time.Minute)
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusAuthError {
		t.Fatalf("expected authentication pause: %+v", got)
	}
	authorized.Store(true)
	report, err := svc.ProbeInstance(ctx, inst.ID)
	if err != nil || report.Capabilities[EndpointSystemVersion].State != CapabilityForbidden {
		t.Fatalf("manual recovery report: %+v, %v", report, err)
	}
	current, err := store.GetInstance(ctx, inst.ID)
	if err != nil || current.LastStatus == string(StatusAuthError) || strings.Contains(current.CapabilitiesJSON, "unsanitized-test-detail") ||
		decodeCapabilities(current.CapabilitiesJSON)[EndpointOpsSnapshotV2].State != CapabilitySupported {
		t.Fatalf("manual probe did not update safe capabilities and status: %+v, %v", current, err)
	}
	clock.add(time.Minute)
	if got := refreshTestSiteOps(t, ctx, svc, inst.ID); got.Status != StatusOnline {
		t.Fatalf("manual probe did not clear polling pause: %+v", got)
	}
}

func TestServiceGroupArchiveIsThrottledAndFallsBackAfterTTL(t *testing.T) {
	svc, store, clock := newTestSiteOpsService(t, &siteOpsTestClient{}, nil)
	ctx := context.Background()
	inst := addTestSiteOpsInstance(t, svc, ctx)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	clock.add(time.Minute)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	live, err := svc.GetGroups(ctx, inst.ID)
	if err != nil || live.ObservedAt == nil || !live.ObservedAt.Equal(clock.now()) || len(live.Groups) != 1 || !live.Groups[0].SinglePointRisk {
		t.Fatalf("missing live group sample: %+v, %v", live, err)
	}
	rows, err := store.GetLatestGroupSnapshots(ctx, inst.ID)
	if err != nil || len(rows) != 1 || !rows[0].ObservedAt.Equal(testSiteOpsStartTime) {
		t.Fatalf("group archive was not throttled: %+v, %v", rows, err)
	}
	clock.add(61 * time.Second)
	archived, err := svc.GetGroups(ctx, inst.ID)
	if err != nil || archived.ObservedAt == nil || !archived.ObservedAt.Equal(testSiteOpsStartTime) || archived.TrustState != QualityStale {
		t.Fatalf("missing stale archive fallback: %+v, %v", archived, err)
	}
	clock.add(3 * time.Minute)
	refreshTestSiteOps(t, ctx, svc, inst.ID)
	rows, err = store.GetLatestGroupSnapshots(ctx, inst.ID)
	if err != nil || len(rows) != 1 || !rows[0].ObservedAt.Equal(clock.now()) {
		t.Fatalf("archive not renewed after interval: %+v, %v", rows, err)
	}
}

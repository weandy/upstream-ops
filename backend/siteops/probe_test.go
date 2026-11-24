package siteops

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
)

func TestProbeFallbackForbiddenAndRecovery(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	recovered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case healthPath:
			io.WriteString(w, `{"status":"ok"}`)
		case snapshotPath:
			if !recovered {
				http.NotFound(w, r)
				return
			}
			io.WriteString(w, `{"overview":{"health_score":93}}`)
		case overviewPath:
			io.WriteString(w, `{"health_score":90}`)
		case concurrencyPath:
			if !recovered {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			io.WriteString(w, `{"enabled":true}`)
		case availabilityPath:
			if !recovered {
				io.WriteString(w, `{"enabled":false}`)
				return
			}
			io.WriteString(w, `{"enabled":true}`)
		case groupsPath:
			io.WriteString(w, `[]`)
		case versionPath:
			io.WriteString(w, `{"version":"v0.2.24"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	prober := NewProber(client)
	ctx := context.Background()
	first, err := prober.ProbeInstance(ctx, inst, nil)
	if err != nil || first.Reachability != ReachabilityReachable || first.Version != "v0.2.24" {
		t.Fatalf("first report = %+v, %v", first, err)
	}
	if primary := first.Capabilities[EndpointOpsSnapshotV2]; primary.State != CapabilityUnsupported ||
		primary.HTTPStatus != 404 || primary.FallbackTarget != EndpointOpsOverview {
		t.Fatalf("snapshot capability = %+v", primary)
	}
	for endpoint, state := range map[string]CapabilityState{
		EndpointHealth: CapabilitySupported, EndpointOpsOverview: CapabilitySupported,
		EndpointOpsConcurrency: CapabilityForbidden, EndpointOpsAccountAvail: CapabilityOpsDisabled,
		EndpointGroupsAll: CapabilitySupported, EndpointSystemVersion: CapabilitySupported,
	} {
		if actual := first.Capabilities[endpoint]; actual.State != state || actual.CheckedAt.IsZero() {
			t.Errorf("%s = %+v, want %s", endpoint, actual, state)
		}
	}
	mu.Lock()
	firstRequests := slices.Clone(requests)
	requests = nil
	recovered = true
	mu.Unlock()
	if !slices.Equal(firstRequests, []string{healthPath, snapshotPath, overviewPath, concurrencyPath, availabilityPath, groupsPath, versionPath}) {
		t.Fatalf("initial probe order = %v", firstRequests)
	}
	// Polling can use the known fallback, while a reprobe always rechecks the preferred route.
	overview, meta, err := client.FetchOpsSnapshot(ctx, inst, first.Capabilities)
	if err != nil || meta.FallbackUsed != EndpointOpsOverview || overview.HealthScore == nil || *overview.HealthScore != 90 {
		t.Fatalf("known fallback = %+v, %+v, %v", overview, meta, err)
	}
	waitUntil(t, func() bool { return time.Now().After(first.Capabilities[EndpointSystemVersion].CheckedAt) })
	second, err := prober.ProbeInstance(ctx, inst, first.Capabilities)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{EndpointOpsSnapshotV2, EndpointOpsConcurrency, EndpointOpsAccountAvail} {
		current, old := second.Capabilities[endpoint], first.Capabilities[endpoint]
		if current.State != CapabilitySupported || current.FallbackTarget != "" || !current.CheckedAt.After(old.CheckedAt) {
			t.Errorf("reprobe of %s = %+v, previously %+v", endpoint, current, old)
		}
	}
	mu.Lock()
	secondRequests := slices.Clone(requests)
	mu.Unlock()
	if !slices.Equal(secondRequests, []string{overviewPath, healthPath, snapshotPath, concurrencyPath, availabilityPath, groupsPath, versionPath}) {
		t.Fatalf("poll and reprobe order = %v", secondRequests)
	}
}

func TestSnapshotSemantic404AndDisabledResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
		want   error
	}{
		{"disabled 404", 404, fixture(t, "ops_disabled_404"), ErrOpsDisabled},
		{"disabled 200", 200, fixture(t, "ops_realtime_disabled_200"), ErrOpsDisabled},
		{"business 404", 404, []byte(`{"code":404,"message":"group not found","reason":"GROUP_NOT_FOUND"}`), errUpstreamStatus},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != snapshotPath {
					t.Errorf("unexpected fallback to %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				w.Write(tc.body)
			}))
			defer server.Close()
			client, inst := testClient(server.URL, config.SiteOpsConfig{})
			_, meta, err := client.FetchOpsSnapshot(context.Background(), inst, nil)
			if !errors.Is(err, tc.want) || meta.HTTPStatus != tc.status || meta.FallbackUsed != "" {
				t.Fatalf("snapshot = %+v, %v; wanted %v", meta, err, tc.want)
			}
		})
	}
}

func TestProbeUnauthorizedStopsAdminRequests(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case healthPath:
			w.WriteHeader(http.StatusOK)
		case snapshotPath:
			w.WriteHeader(http.StatusForbidden)
		case concurrencyPath:
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("unexpected request after the 401: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, inst := testClient(server.URL, config.SiteOpsConfig{})
	old := EndpointCapability{State: CapabilitySupported, CheckedAt: time.Unix(10, 0)}
	existing := CapabilitiesMap{EndpointOpsAccountAvail: old}
	report, err := NewProber(client).ProbeInstance(context.Background(), inst, existing)
	if !errors.Is(err, ErrUnauthorized) || report.Capabilities[EndpointOpsSnapshotV2].State != CapabilityForbidden ||
		report.Capabilities[EndpointOpsConcurrency].HTTPStatus != 401 ||
		report.Capabilities[EndpointOpsAccountAvail] != old || len(existing) != 1 {
		t.Fatalf("probe = %+v, %v", report, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(requests, []string{healthPath, snapshotPath, concurrencyPath}) {
		t.Fatalf("probe requests = %v", requests)
	}
}

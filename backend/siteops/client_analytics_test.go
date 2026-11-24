package siteops

import (
	"context"
	"errors"
	"fmt"
	"github.com/bejix/upstream-ops/backend/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnalyticsAdmissionBudgetAndBackoff(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case adminPath + "/dashboard/users-usage":
			b, _ := io.ReadAll(r.Body)
			if string(b) != `{"user_ids":[1,2]}` || r.Header.Get("If-None-Match") != "" {
				t.Error("POST canonicalization")
			}
			started <- struct{}{}
			<-release
			w.Header().Set("ETag", "sample")
			fmt.Fprint(w, `{"stats":{}}`)
		case adminPath + "/channel-monitor-v2/snapshot":
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":" Channel monitor feature is disabled "}`)
		case adminPath + "/backups":
			w.WriteHeader(403)
			fmt.Fprint(w, "channel monitor feature is disabled")
		case adminPath + "/risk-control/status":
			w.WriteHeader(429)
		case healthPath:
			fmt.Fprint(w, "ok")
		default:
			fmt.Fprint(w, "[]")
		}
	}))
	defer srv.Close()
	c, inst := testClient(srv.URL, config.SiteOpsConfig{})
	done := make(chan error, 2)
	go func() { _, _, e := c.FetchUsersUsageBatch(ctx, inst, []int64{2, 1, 1}); done <- e }()
	<-started
	go func() { _, _, e := c.FetchUsersUsageBatch(ctx, inst, []int64{1, 2}); done <- e }()
	waitAnalytics(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, f := range c.flights {
			if f.waiters == 2 {
				return true
			}
		}
		return false
	})
	close(release)
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 || len(c.cache) != 0 {
		t.Fatal("P2 leaked to ETag / duplicated")
	}
	if _, _, e := c.ProbeChannelMonitorV2(ctx, inst); !errors.Is(e, ErrOpsDisabled) {
		t.Fatal(e)
	}
	if _, _, e := c.FetchBackupsList(ctx, inst); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	for _, body := range []string{`{"other":"channel monitor feature is disabled"}`, strings.Repeat(" ", 4096) + "channel monitor feature is disabled"} {
		if channelDisabled(strings.NewReader(body)) {
			t.Fatal("false disabled")
		}
	}
	_, m, e := c.FetchRiskControlStatus(ctx, inst)
	if !errors.Is(e, ErrRateLimited) || m.RetryAfter == nil || *m.RetryAfter != 30*time.Second {
		t.Fatal(m, e)
	}
	before := calls.Load()
	if _, _, e := c.FetchGroupsAll(ctx, inst); !errors.Is(e, ErrRateLimited) || calls.Load() != before {
		t.Fatal("background bypassed backoff")
	}
	if _, e := c.ProbeHealth(ctx, inst); e != nil {
		t.Fatal(e)
	}
	if n, m, d := c.BudgetHeadroomForAnalytics(2, 8000); m != 8 || d != 800 || n != 25 {
		t.Fatalf("headroom %d %d %d", n, m, d)
	}
	budget, _ := testClient(srv.URL, config.SiteOpsConfig{})
	for i := 0; i < 33; i++ {
		inst.InstanceID = uint(1 + i/25)
		charge, release, e := budget.reserveAudit(inst, ctx, true)
		if e != nil {
			t.Fatal(i, e)
		}
		charge()
		release()
	}
	inst.InstanceID = 3
	if _, _, e := budget.reserveAudit(inst, ctx, true); !errors.Is(e, ErrBudgetExceeded) {
		t.Fatal("global budget")
	}
	budget.ResetInstanceState(1)
	if budget.BudgetUsage(1, 8000).UsedToday != 25 {
		t.Fatal("reset budget")
	}
	charge, releaseBudget, e := budget.reserveAudit(inst, ctx)
	if e != nil {
		t.Fatal("L1 reserved budget", e)
	}
	charge()
	releaseBudget()
	for i := 0; i < 3; i++ {
		if e := budget.acquire(ctx, 1, time.Second, true); e != nil {
			t.Fatal(e)
		}
	}
	if e := budget.acquire(ctx, 1, time.Millisecond, true); !errors.Is(e, ErrQueueTimeout) {
		t.Fatal("P2 slot")
	}
	if e := budget.acquire(ctx, 1, time.Second); e != nil {
		t.Fatal("L1 slot")
	}
	budget.release(1)
	for i := 0; i < 3; i++ {
		budget.release(1, true)
	}
	for _, x := range []struct {
		header string
		d      time.Duration
	}{{"12", 12 * time.Second}, {time.Now().UTC().Truncate(time.Second).Add(time.Minute).Format(http.TimeFormat), time.Minute}} {
		now := time.Now().UTC().Truncate(time.Second)
		d := parseRetryAfter(x.header, now)
		if d == nil || (*d-x.d).Abs() > time.Second {
			t.Fatal("Retry-After")
		}
	}
}

func TestAnalyticsBackoffStreakAndLateSuccess(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/backups") {
			close(started)
			<-release
			fmt.Fprint(w, "[]")
			return
		}
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c, inst := testClient(srv.URL, config.SiteOpsConfig{})
	done := make(chan error, 1)
	go func() { _, _, e := c.FetchBackupsList(ctx, inst); done <- e }()
	<-started
	for i, d := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute} {
		_, m, e := c.FetchRiskControlStatus(ctx, inst)
		if i == 0 {
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if !errors.Is(e, ErrRateLimited) || m.RetryAfter == nil || *m.RetryAfter != d {
			t.Fatal(i, m, e)
		}
		if until, active := c.AdminBackoff(inst.InstanceID, inst.CredGen); !active || time.Until(until) > d {
			t.Fatal("退避丢失", until)
		}
		// 将当前等待期移到过去，保留连续 429 次数以测试下一次真实请求。
		c.mu.Lock()
		state := c.backoffs[inst.InstanceID]
		state.until = time.Now().Add(-time.Second)
		c.backoffs[inst.InstanceID] = state
		c.mu.Unlock()
	}
}

func TestAnalyticsResponseLimitsAndFlightPanic(t *testing.T) {
	var redirects atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case adminPath + "/users":
			http.Redirect(w, r, "/redirected", http.StatusFound)
		case "/redirected":
			redirects.Add(1)
		case adminPath + "/backups":
			io.WriteString(w, strings.Repeat(" ", (8<<20)+1))
		default:
			io.WriteString(w, `{"enabled":false}`)
		}
	}))
	defer srv.Close()
	c, inst := testClient(srv.URL, config.SiteOpsConfig{MaxResponseBytes: 16 << 20})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, e := c.FetchUsersPage(ctx, inst, 1); !errors.Is(e, ErrRedirectBlocked) || redirects.Load() != 0 {
		t.Fatal("302", e)
	}
	if _, _, e := c.FetchBackupsList(ctx, inst); !errors.Is(e, errResponseLimit) {
		t.Fatal("8 MiB", e)
	}
	spec := requestSpec{endpoint: EndpointRiskControlStatus, path: adminPath + "/risk-control/status", method: http.MethodGet, policy: cachePolicyAnalyticsLRU}
	if r := c.fetchShared(ctx, inst, spec, func([]byte) ([]byte, error) { panic("parser failure") }); !errors.Is(r.err, errUpstreamRequest) {
		t.Fatal(r.err)
	}
	if r := c.fetchShared(ctx, inst, spec, func(b []byte) ([]byte, error) { return b, nil }); r.err != nil {
		t.Fatal("panic 后单飞没有释放", r.err)
	}
	cache := newAnalyticsCache(time.Now)
	defer cache.stop()
	key := newLeafKey(cache.scope(1, 1, 1), EndpointUsersPage, "GET", 1)
	if _, _, _, e := loadProjection(ctx, cache, key, leafOptions{}, func(context.Context) (string, UpstreamFetchMeta, error) { panic("projection failure") }); !errors.Is(e, errUpstreamRequest) {
		t.Fatal(e)
	}
	if v, _, _, e := loadProjection(ctx, cache, key, leafOptions{}, func(context.Context) (string, UpstreamFetchMeta, error) { return "new", UpstreamFetchMeta{}, nil }); v != "new" || e != nil {
		t.Fatal(v, e)
	}
}

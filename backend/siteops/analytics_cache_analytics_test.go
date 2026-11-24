package siteops

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitAnalytics(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("等待并发条件超时")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestAnalyticsCacheAndSingleflightAndTokens(t *testing.T) {
	clock := &siteOpsTestClock{}
	clock.nanos.Store(time.Date(2024, 2, 28, 23, 59, 59, 0, locUTC8).UnixNano())
	c := newAnalyticsCache(clock.now)
	defer c.stop()
	ctx := context.Background()
	scope := c.scope(1, 1, 1)
	k := newLeafKey(scope, EndpointUsersUsage, "POST", 1)
	counter := atomic.Int32{}
	fetch := func(context.Context) (string, UpstreamFetchMeta, error) {
		counter.Add(1)
		return "masked", UpstreamFetchMeta{}, nil
	}
	_, m, hit, err := loadProjection(ctx, c, k, leafOptions{today: true}, fetch)
	if err != nil || hit || m.ExpiresAt.Sub(clock.now()) != time.Second {
		t.Fatal(m, err)
	}
	clock.add(time.Second)
	if _, _, _, err := loadProjection(ctx, c, k, leafOptions{only: true}, fetch); !errors.Is(err, errProjectionMiss) {
		t.Fatal("returned expired projection")
	}
	for _, tc := range []struct {
		ep  string
		ttl time.Duration
	}{{EndpointBackups, 3 * time.Minute}, {EndpointUsersPage, 5 * time.Minute}, {EndpointDashboardModels, 10 * time.Minute}} {
		key := newLeafKey(scope, tc.ep, "GET", nil)
		_, m, _, err := loadProjection(ctx, c, key, leafOptions{}, fetch)
		if err != nil || m.ExpiresAt.Sub(clock.now()) != tc.ttl {
			t.Fatal("TTL")
		}
		clock.add(29 * time.Second)
		_, m2, hit, _ := loadProjection(ctx, c, key, leafOptions{force: true}, fetch)
		if !hit || !m2.ExpiresAt.Equal(*m.ExpiresAt) {
			t.Fatal("force extended TTL")
		}
		clock.add(time.Second)
		_, _, hit, _ = loadProjection(ctx, c, key, leafOptions{force: true}, fetch)
		if hit {
			t.Fatal("force throttled after 30s")
		}
	}
	reset := clock.now().Add(time.Minute)
	_, m, _, err = loadProjection(ctx, c, newLeafKey(scope, EndpointSubscriptionProgress, "GET", 3), leafOptions{}, func(context.Context) (*SanitizedSubscriptionProgress, UpstreamFetchMeta, error) {
		return &SanitizedSubscriptionProgress{Daily: &RollingWindowView{ResetsAt: &reset}}, UpstreamFetchMeta{}, nil
	})
	if err != nil || !m.ExpiresAt.Equal(reset) {
		t.Fatal("reset TTL")
	}
	c.invalidate(0, ErrGenerationMismatch)
	scope = c.scope(1, 1, 1)
	key := newLeafKey(scope, EndpointUsersPage, "GET", 22)
	release := make(chan struct{})
	calls := atomic.Int32{}
	done := make(chan error, 20)
	first, cancel := context.WithCancel(ctx)
	defer cancel()
	for i := 0; i < 20; i++ {
		wait := ctx
		if i == 0 {
			wait = first
		}
		go func() {
			_, _, _, e := loadProjection(wait, c, key, leafOptions{}, func(context.Context) (string, UpstreamFetchMeta, error) {
				calls.Add(1)
				<-release
				return "ok", UpstreamFetchMeta{}, nil
			})
			done <- e
		}()
	}
	waitAnalytics(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); f := c.flights[key]; return f != nil && f.waiters == 20 })
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(release)
	for i := 0; i < 19; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate flight")
	}
	key = newLeafKey(scope, EndpointUsersPage, "GET", 23)
	started := make(chan struct{})
	stopped := make(chan struct{})
	last, stop := context.WithCancel(ctx)
	go func() {
		_, _, _, e := loadProjection(last, c, key, leafOptions{}, func(work context.Context) (string, UpstreamFetchMeta, error) {
			close(started)
			<-work.Done()
			close(stopped)
			return "", UpstreamFetchMeta{}, work.Err()
		})
		done <- e
	}()
	<-started
	stop()
	<-done
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("last waiter did not cancel")
	}
	for _, cause := range []error{ErrGenerationMismatch, ErrForbidden, ErrUnauthorized} {
		scope = c.scope(1, 1, 1)
		key = newLeafKey(scope, EndpointUsersPage, "GET", 24)
		enter := make(chan struct{})
		resume := make(chan struct{})
		go func() {
			v, _, _, e := loadProjection(ctx, c, key, leafOptions{}, func(context.Context) (string, UpstreamFetchMeta, error) {
				close(enter)
				<-resume
				return "old", UpstreamFetchMeta{}, nil
			})
			if v != "" {
				t.Error("stale response")
			}
			done <- e
		}()
		<-enter
		c.invalidate(1, cause)
		close(resume)
		if e := <-done; !errors.Is(e, cause) {
			t.Fatal(e)
		}
	}
	c.invalidate(0, ErrGenerationMismatch)
	expiry := clock.now().Add(time.Hour)
	c.mu.Lock()
	insert := func(id uint, i, n int) {
		k := newLeafKey(analyticsScope{id: id}, "test", "GET", i)
		c.insertLocked(leafEntry{k, []byte(strings.Repeat("x", n)), DomainMeta{ExpiresAt: &expiry}})
	}
	for i := 0; i < 201; i++ {
		insert(1, i, 128)
	}
	if len(c.entries) != 200 {
		t.Fatal("local count")
	}
	for i := 0; i < 20; i++ {
		insert(1, 300+i, 128<<10)
	}
	if len(c.entries) != 16 {
		t.Fatal("local bytes", len(c.entries))
	}
	for i := 0; i < 1026; i++ {
		insert(2+uint(i/100), i, 128)
	}
	if len(c.entries) != 1024 {
		t.Fatal("global count")
	}
	for i := 0; i < 131; i++ {
		insert(30+uint(i/10), i, 128<<10)
	}
	if len(c.entries) != 128 {
		t.Fatal("global bytes", len(c.entries))
	}
	c.mu.Unlock()
	huge := newLeafKey(c.scope(100, 1, 1), "huge", "GET", nil)
	_, m, _, err = loadProjection(ctx, c, huge, leafOptions{}, func(context.Context) (string, UpstreamFetchMeta, error) {
		return strings.Repeat("a", 128<<10), UpstreamFetchMeta{}, nil
	})
	if err != nil || m.Reason != "truncated" {
		t.Fatal("oversize projection")
	}
	if _, _, _, e := loadProjection(ctx, c, huge, leafOptions{only: true}, fetch); !errors.Is(e, errProjectionMiss) {
		t.Fatal(e)
	}
	c.stop()
	if c.timer != nil || len(c.entries) != 0 {
		t.Fatal("stop cleanup")
	}
}

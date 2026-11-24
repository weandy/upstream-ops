package siteops

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var errProjectionMiss = errors.New("analytics projection unavailable")

// A 403 invalidates other in-flight leaves too. Only the endpoint that returned
// the denial may be skipped when rebuilding the response.
var errForbiddenFence = fmt.Errorf("analytics projections invalidated: %w", ErrForbidden)

type hardFence struct {
	token uint64
	cause error
}

type analyticsTokenKey struct{}
type analyticsScope struct {
	id           uint
	gen, cred    int64
	epoch, token uint64
}
type leafKey struct {
	analyticsScope
	endpoint, method string
	params           [32]byte
}
type leafEntry struct {
	key  leafKey
	data []byte
	meta DomainMeta
}
type leafFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  leafEntry
	err     error
}
type AnalyticsCache struct {
	mu         sync.Mutex
	now        func() time.Time
	epoch      uint64
	tokens     map[uint]uint64
	hardFences map[uint]hardFence
	entries    map[leafKey]*list.Element
	lru        list.List
	flights    map[leafKey]*leafFlight
	timer      *time.Timer
	stopped    bool
}

func newAnalyticsCache(now func() time.Time) *AnalyticsCache {
	if now == nil {
		now = time.Now
	}
	return &AnalyticsCache{now: now, tokens: map[uint]uint64{}, hardFences: map[uint]hardFence{}, entries: map[leafKey]*list.Element{}, flights: map[leafKey]*leafFlight{}}
}
func (c *AnalyticsCache) scope(id uint, gen, cred int64) analyticsScope {
	c.mu.Lock()
	defer c.mu.Unlock()
	return analyticsScope{id, gen, cred, c.epoch, c.tokens[id]}
}
func (c *AnalyticsCache) validLocked(k analyticsScope) bool {
	return !c.stopped && c.epoch == k.epoch && c.tokens[k.id] == k.token
}
func (c *AnalyticsCache) valid(k analyticsScope) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validLocked(k)
}
func (c *AnalyticsCache) failureLocked(k analyticsScope) error {
	if c.stopped || k.epoch != c.epoch {
		return ErrGenerationMismatch
	}
	if fence := c.hardFences[k.id]; fence.token > k.token {
		return fence.cause
	}
	return errForbiddenFence
}
func (c *AnalyticsCache) scopeError(k analyticsScope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.validLocked(k) {
		return nil
	}
	return c.failureLocked(k)
}
func (c *AnalyticsCache) remove(e *list.Element) {
	delete(c.entries, e.Value.(leafEntry).key)
	c.lru.Remove(e)
}
func (c *AnalyticsCache) invalidate(id uint, cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidateLocked(id, cause)
}
func (c *AnalyticsCache) invalidateLocked(id uint, cause error) {
	if id == 0 {
		c.epoch++
		clear(c.tokens)
		clear(c.hardFences)
	} else {
		c.tokens[id]++
		if cause != ErrForbidden {
			c.hardFences[id] = hardFence{c.tokens[id], cause}
		}
	}
	var retained []leafEntry
	for k, e := range c.entries {
		if id != 0 && k.id != id {
			continue
		}
		if cause == ErrForbidden {
			// 撤销单端点权限后仍可组合已验证的非个人投影。
			switch k.endpoint {
			case EndpointRiskControlStatus, EndpointBackups, EndpointChannelMonitorV2, EndpointAnalyticsGroups, EndpointDashboardModels, EndpointModelMismatch, EndpointDashboardGroups:
				v := e.Value.(leafEntry)
				v.key.token = c.tokens[id]
				retained = append(retained, v)
			}
		}
		c.remove(e)
	}
	for _, v := range retained {
		c.entries[v.key] = c.lru.PushBack(v)
	}
	for k, f := range c.flights {
		if id == 0 || k.id == id {
			f.cancel()
			delete(c.flights, k)
		}
	}
	c.scheduleLocked()
}
func (c *AnalyticsCache) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	c.invalidateLocked(0, ErrGenerationMismatch)
}
func (c *AnalyticsCache) scheduleLocked() {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	now := c.now()
	var first time.Time
	for _, e := range c.entries {
		end := *e.Value.(leafEntry).meta.ExpiresAt
		if !end.After(now) {
			c.remove(e)
		} else if first.IsZero() || end.Before(first) {
			first = end
		}
	}
	if !first.IsZero() && !c.stopped {
		c.timer = time.AfterFunc(first.Sub(now), func() { c.mu.Lock(); defer c.mu.Unlock(); c.scheduleLocked() })
	}
}
func (c *AnalyticsCache) insertLocked(entry leafEntry) {
	if old := c.entries[entry.key]; old != nil {
		c.remove(old)
	}
	if len(entry.data) > 128<<10 {
		return
	}
	c.entries[entry.key] = c.lru.PushFront(entry)
	var globalBytes, localBytes, localCount int
	// 从新到旧扫描，先保留最近使用且仍在本站与全局预算内的投影。
	globalCount := 0
	for elem := c.lru.Front(); elem != nil; {
		next := elem.Next()
		v := elem.Value.(leafEntry)
		n := len(v.data)
		if v.key.id == entry.key.id {
			localBytes += n
			localCount++
			if localCount > 200 || localBytes > 2<<20 {
				c.remove(elem)
				elem = next
				continue
			}
		}
		globalCount++
		globalBytes += n
		if globalCount > 1024 || globalBytes > 16<<20 {
			c.remove(elem)
		}
		elem = next
	}
	c.scheduleLocked()
}
func ttlFor(endpoint string) time.Duration {
	switch endpoint {
	case EndpointBackups, EndpointRiskControlStatus, EndpointChannelMonitorV2:
		return 3 * time.Minute
	case EndpointUsageStats, EndpointDashboardModels, EndpointModelMismatch, EndpointDashboardGroups:
		return 10 * time.Minute
	}
	return 5 * time.Minute
}

type leafOptions struct {
	force, only, today bool
	deadline           time.Time
	timeout            time.Duration
	clientFences       bool
}

func nextMidnight(t time.Time) time.Time {
	d := t.In(locUTC8)
	return time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, locUTC8)
}
func loadProjection[T any](ctx context.Context, c *AnalyticsCache, k leafKey, o leafOptions, fetch func(context.Context) (T, UpstreamFetchMeta, error)) (T, DomainMeta, bool, error) {
	var zero T
	c.mu.Lock()
	if !c.validLocked(k.analyticsScope) {
		err := c.failureLocked(k.analyticsScope)
		c.mu.Unlock()
		return zero, DomainMeta{}, false, err
	}
	now := c.now()
	if elem := c.entries[k]; elem != nil {
		v := elem.Value.(leafEntry)
		if !v.meta.ExpiresAt.After(now) {
			c.remove(elem)
		} else if o.only || !o.force || now.Sub(*v.meta.ObservedAt) < 30*time.Second {
			c.lru.MoveToFront(elem)
			c.mu.Unlock()
			var dst T
			err := json.Unmarshal(v.data, &dst)
			return dst, cloneDomainMeta(v.meta), true, err
		}
	}
	if o.only {
		c.mu.Unlock()
		return zero, DomainMeta{}, false, errProjectionMiss
	}
	f := c.flights[k]
	if f == nil {
		timeout := o.timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		work, cancel := context.WithTimeout(context.WithValue(context.Background(), analyticsTokenKey{}, k.analyticsScope), timeout)
		f = &leafFlight{done: make(chan struct{}), cancel: cancel}
		c.flights[k] = f
		go func() {
			defer cancel()
			defer func() {
				if recover() != nil {
					c.mu.Lock()
					defer c.mu.Unlock()
					if c.flights[k] == f {
						delete(c.flights, k)
					}
					f.err = errUpstreamRequest
					close(f.done)
				}
			}()
			var value T
			var meta UpstreamFetchMeta
			err := work.Err()
			if err == nil && c.valid(k.analyticsScope) {
				value, meta, err = fetch(work)
			}
			var data []byte
			if err == nil {
				data, err = json.Marshal(value)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if !c.validLocked(k.analyticsScope) {
				fence := c.failureLocked(k.analyticsScope)
				if fence != errForbiddenFence || !errors.Is(err, ErrForbidden) {
					err = fence
				}
			} else if c.flights[k] != f || work.Err() != nil {
				err = work.Err()
				if err == nil {
					err = context.Canceled
				}
			}
			if c.flights[k] == f {
				delete(c.flights, k)
			}
			if (errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden)) && !o.clientFences && c.validLocked(k.analyticsScope) {
				c.invalidateLocked(k.id, err)
			}
			if err == nil {
				at := meta.ObservedAt
				if at.IsZero() {
					at = c.now()
				}
				ver := meta.VerifiedAt
				if ver.IsZero() {
					ver = at
				}
				end := at.Add(ttlFor(k.endpoint))
				shorten := func(t time.Time) {
					if t.After(at) && t.Before(end) {
						end = t
					}
				}
				if o.today {
					shorten(nextMidnight(at))
				}
				shorten(o.deadline)
				if p, ok := any(value).(*SanitizedSubscriptionProgress); ok && p != nil {
					if p.ExpiresAt != nil {
						shorten(*p.ExpiresAt)
					}
					for _, w := range []*RollingWindowView{p.Daily, p.Weekly, p.Monthly} {
						if w != nil {
							r := windowReset(w, at)
							if r.After(at) {
								shorten(r)
							}
						}
					}
				}
				dm := DomainMeta{Quality: QualityTrusted, ObservedAt: &at, VerifiedAt: &ver, ExpiresAt: &end}
				if len(data) > 128<<10 {
					dm.Reason = "truncated"
				}
				f.result = leafEntry{k, data, dm}
				if end.After(c.now()) {
					c.insertLocked(f.result)
				} else {
					err = errProjectionMiss
				}
			}
			f.err = err
			close(f.done)
		}()
	}
	f.waiters++
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		f.waiters--
		if f.waiters == 0 && c.flights[k] == f {
			delete(c.flights, k)
			f.cancel()
		}
		c.mu.Unlock()
		return zero, DomainMeta{}, false, ctx.Err()
	case <-f.done:
		if f.err != nil {
			return zero, DomainMeta{}, false, f.err
		}
		var dst T
		err := json.Unmarshal(f.result.data, &dst)
		return dst, cloneDomainMeta(f.result.meta), false, err
	}
}
func newLeafKey(s analyticsScope, ep, method string, params any) leafKey {
	b, _ := json.Marshal(params)
	return leafKey{s, ep, method, sha256.Sum256(b)}
}

func cloneDomainMeta(v DomainMeta) DomainMeta {
	v.ObservedAt = cloneTimePtr(v.ObservedAt)
	v.VerifiedAt = cloneTimePtr(v.VerifiedAt)
	v.ExpiresAt = cloneTimePtr(v.ExpiresAt)
	return v
}

func (c *AnalyticsCache) dropEndpoint(id uint, endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if k.id == id && k.endpoint == endpoint {
			c.remove(e)
		}
	}
	c.scheduleLocked()
}

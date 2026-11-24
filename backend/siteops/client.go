package siteops

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
)

const (
	adminPath          = "/api/v1/admin"
	healthPath         = "/health"
	snapshotPath       = adminPath + "/ops/dashboard/snapshot-v2"
	overviewPath       = adminPath + "/ops/dashboard/overview"
	concurrencyPath    = adminPath + "/ops/concurrency"
	availabilityPath   = adminPath + "/ops/account-availability"
	groupsPath         = adminPath + "/groups/all"
	versionPath        = adminPath + "/system/version"
	alertEventsPath    = adminPath + "/ops/alert-events?limit=50"
	ingressHealthPath  = adminPath + "/ops/ingress-rejections/health"
	authCachePath      = adminPath + "/ops/auth-cache-invalidation/health"
	accountsPathPrefix = adminPath + "/accounts"
	accountsPageSize   = 100
	accountsMaxPages   = 10
)

var (
	errUpstreamRequest = errors.New("site-ops upstream request failed")
	errUpstreamStatus  = errors.New("site-ops upstream returned an unexpected HTTP status")
	errResponseLimit   = errors.New("site-ops upstream response exceeds the configured size limit")
	errMissingCache    = errors.New("site-ops upstream returned 304 without a matching cached observation")
	errProxyConfig     = errors.New("invalid site-ops outbound proxy configuration")
	etagFormat         = regexp.MustCompile(`^(?:(?:W/)?"[!#-~]*"|[a-zA-Z0-9._-]+)$`)
)

type clientRuntime struct {
	site      config.SiteOpsConfig
	proxy     config.ProxyConfig
	userAgent string
}

type endpointKey struct {
	instanceID uint
	endpoint   string
	path       string
}

type targetIdentity struct {
	baseURL    string
	generation int64
	credGen    int64
	proxy      bool
	keyDigest  [sha256.Size]byte
}

type flightKey struct {
	endpointKey
	targetIdentity
}

type observation struct {
	target     targetIdentity
	etag       string
	projected  []byte
	observedAt time.Time
}

type fetchResult struct {
	projected []byte
	meta      UpstreamFetchMeta
	err       error
}

type requestFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  fetchResult
}

type requestSlots struct {
	active int
	queued int
}

type auditWindow struct {
	date    string
	used    int
	recent  []time.Time
	pending int
}

type Client struct {
	mu      sync.Mutex
	runtime clientRuntime
	guard   OutboundValidator
	log     *slog.Logger
	flights map[flightKey]*requestFlight
	cache   map[endpointKey]observation
	budgets map[uint]*auditWindow
	slots   map[uint]*requestSlots
	active  int
	changed chan struct{}
}

var (
	_ UpstreamClient = (*Client)(nil)
	_ P1AClient      = (*Client)(nil)
)

func NewClient(siteCfg config.SiteOpsConfig, proxyCfg config.ProxyConfig, upstreamCfg config.UpstreamConfig, guard OutboundValidator, log *slog.Logger) *Client {
	if guard == nil {
		guard = NewOutboundGuard(siteCfg.AllowLoopback, siteCfg.AllowedOrigins, siteCfg.AllowedCIDRs)
	}
	return &Client{
		runtime: clientRuntime{siteCfg.WithDefaults(), proxyCfg, upstreamCfg.WithDefaults().UserAgent},
		guard:   guard, log: log, flights: make(map[flightKey]*requestFlight), cache: make(map[endpointKey]observation),
		budgets: make(map[uint]*auditWindow), slots: make(map[uint]*requestSlots), changed: make(chan struct{}),
	}
}

func (c *Client) UpdateRuntimeConfig(siteCfg config.SiteOpsConfig, proxyCfg config.ProxyConfig, upstreamCfg config.UpstreamConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runtime = clientRuntime{siteCfg.WithDefaults(), proxyCfg, upstreamCfg.WithDefaults().UserAgent}
	c.guard.UpdatePolicy(siteCfg.AllowLoopback, siteCfg.AllowedOrigins, siteCfg.AllowedCIDRs)
	for key, flight := range c.flights {
		flight.cancel()
		delete(c.flights, key)
	}
	c.signalSlots()
}

func (c *Client) ResetInstanceState(instanceID uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.cache {
		if key.instanceID == instanceID {
			delete(c.cache, key)
		}
	}
	for key, flight := range c.flights {
		if key.instanceID == instanceID {
			flight.cancel()
			delete(c.flights, key)
		}
	}
}

func (w *auditWindow) advance(now time.Time) {
	if date := now.UTC().Format(time.DateOnly); date != w.date {
		w.date = date
		w.used = 0
	}
	cutoff := now.Add(-time.Minute)
	first := 0
	for first < len(w.recent) && !w.recent[first].After(cutoff) {
		first++
	}
	w.recent = w.recent[first:]
}

func (c *Client) BudgetUsage(instanceID uint, dailyLimit int) AuditBudgetUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dailyLimit <= 0 {
		dailyLimit = c.runtime.site.DefaultAuditBudgetPerDay
	}
	now := time.Now().UTC()
	reset := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	usage := AuditBudgetUsage{
		DailyLimit: dailyLimit, PerMinLimit: max(12, dailyLimit/240),
		WindowDateUTC: now.Format(time.DateOnly), ResetAt: &reset,
	}
	if window := c.budgets[instanceID]; window != nil {
		window.advance(now)
		usage.UsedToday = window.used
		usage.UsedLastMin = len(window.recent)
	}
	return usage
}

// reserveAudit reserves room before dialing and charges the budget when the Admin request is sent.
func (c *Client) reserveAudit(inst TargetEndpoint, ctx context.Context) (charge func(), release func(), err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	limit := inst.AuditBudgetPerDay
	if limit <= 0 {
		limit = c.runtime.site.DefaultAuditBudgetPerDay
	}
	w := c.budgets[inst.InstanceID]
	if w == nil {
		w = &auditWindow{}
		c.budgets[inst.InstanceID] = w
	}
	w.advance(time.Now())
	if w.used+w.pending >= limit || len(w.recent)+w.pending >= max(12, limit/240) {
		return nil, nil, ErrBudgetExceeded
	}
	w.pending++
	var once sync.Once
	finish := func(sent bool) {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			w.pending--
			if sent {
				now := time.Now()
				w.advance(now)
				w.used++
				w.recent = append(w.recent, now)
			}
		})
	}
	return func() { finish(true) }, func() { finish(false) }, nil
}

func (c *Client) signalSlots() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *Client) tryAcquire(slots *requestSlots) bool {
	if c.active >= c.runtime.site.GlobalMaxConcurrency || slots.active >= c.runtime.site.PerInstanceMaxConcurrency {
		return false
	}
	c.active++
	slots.active++
	return true
}

func (c *Client) acquire(ctx context.Context, instanceID uint, wait time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	slots := c.slots[instanceID]
	if slots == nil {
		slots = &requestSlots{}
		c.slots[instanceID] = slots
	}
	if c.tryAcquire(slots) {
		return nil
	}
	if slots.queued >= c.runtime.site.PerInstanceQueueLimit {
		return ErrQueueTimeout
	}
	slots.queued++
	defer func() {
		slots.queued--
		c.removeEmptySlots(instanceID, slots)
	}()
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-waitCtx.Done():
		}
		c.mu.Lock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if waitCtx.Err() != nil {
			return ErrQueueTimeout
		}
		if c.tryAcquire(slots) {
			return nil
		}
	}
}

func (c *Client) removeEmptySlots(instanceID uint, slots *requestSlots) {
	if slots.active == 0 && slots.queued == 0 {
		delete(c.slots, instanceID)
	}
}

func (c *Client) release(instanceID uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	slots := c.slots[instanceID]
	c.active--
	slots.active--
	c.removeEmptySlots(instanceID, slots)
	c.signalSlots()
}

type projection func([]byte) ([]byte, error)

func fetch[T any](c *Client, ctx context.Context, inst TargetEndpoint, endpoint, path string, parse func([]byte) (T, error)) (T, UpstreamFetchMeta, error) {
	result := c.fetchShared(ctx, inst, endpoint, path, func(body []byte) ([]byte, error) {
		value, err := parse(body)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	})
	var value T
	if result.err != nil {
		return value, result.meta, result.err
	}
	// Each caller owns its copy, so it cannot mutate a cached observation or another caller's result.
	if err := json.Unmarshal(result.projected, &value); err != nil {
		return value, failedFetch(result.meta, errInvalidPayload).meta, errInvalidPayload
	}
	return value, result.meta, nil
}

func (c *Client) fetchShared(ctx context.Context, inst TargetEndpoint, endpoint, path string, project projection) fetchResult {
	meta := UpstreamFetchMeta{Endpoint: endpoint}
	if err := ctx.Err(); err != nil {
		return failedFetch(meta, err)
	}
	c.mu.Lock()
	baseURL, err := c.guard.ValidateBaseURL(inst.BaseURL)
	if err != nil {
		c.mu.Unlock()
		return failedFetch(meta, ErrOriginNotAllowed)
	}
	key := flightKey{
		endpointKey{inst.InstanceID, endpoint, path},
		targetIdentity{baseURL, inst.Generation, inst.CredGen, inst.ProxyEnabled, sha256.Sum256([]byte(inst.APIKey))},
	}
	flight := c.flights[key]
	if flight == nil {
		runtime := c.runtime
		limit := time.Duration(runtime.site.BatchTimeoutSeconds) * time.Second
		flightCtx, cancel := context.WithTimeout(context.Background(), limit)
		flight = &requestFlight{done: make(chan struct{}), cancel: cancel}
		c.flights[key] = flight
		cached := c.cache[key.endpointKey]
		if cached.target != key.targetIdentity {
			cached = observation{}
		}
		go c.runFlight(flightCtx, runtime, key, flight, inst, cached, project)
	}
	flight.waiters++
	c.mu.Unlock()

	select {
	case <-flight.done:
		result := flight.result
		if result.meta.RetryAfter != nil {
			delay := *result.meta.RetryAfter
			result.meta.RetryAfter = &delay
		}
		return result
	case <-ctx.Done():
		c.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 && c.flights[key] == flight {
			delete(c.flights, key)
			flight.cancel()
		}
		c.mu.Unlock()
		return failedFetch(meta, ctx.Err())
	}
}

func (c *Client) runFlight(ctx context.Context, runtime clientRuntime, key flightKey, flight *requestFlight, inst TargetEndpoint, cached observation, project projection) {
	defer flight.cancel()
	result := c.execute(ctx, runtime, key, inst, cached, project)
	if c.log != nil {
		c.log.Debug("site-ops upstream request", "instance_id", inst.InstanceID, "endpoint", key.endpoint,
			"http_status", result.meta.HTTPStatus, "latency_ms", result.meta.LatencyMS, "error_code", result.meta.ErrorCode)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[key] == flight {
		delete(c.flights, key)
		if project != nil && result.err == nil && !result.meta.NotModified {
			delete(c.cache, key.endpointKey)
			if result.meta.ETag != "" {
				c.cache[key.endpointKey] = observation{key.targetIdentity, result.meta.ETag, result.projected, result.meta.ObservedAt}
			}
		}
		if errors.Is(result.err, ErrUnauthorized) || errors.Is(result.err, ErrForbidden) ||
			errors.Is(result.err, ErrOpsDisabled) || errors.Is(result.err, ErrUnsupportedRoute) {
			if c.cache[key.endpointKey].target == key.targetIdentity {
				delete(c.cache, key.endpointKey)
			}
		}
	}
	flight.result = result
	close(flight.done)
}

func errorCode(err error) string {
	switch {
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

func failedFetch(meta UpstreamFetchMeta, err error) fetchResult {
	meta.ErrorCode = errorCode(err)
	meta.SanitizedError = err.Error()
	return fetchResult{meta: meta, err: err}
}

func (c *Client) execute(ctx context.Context, runtime clientRuntime, key flightKey, inst TargetEndpoint, cached observation, project projection) fetchResult {
	meta := UpstreamFetchMeta{Endpoint: key.endpoint}
	if err := c.acquire(ctx, inst.InstanceID, time.Duration(runtime.site.QueueWaitTimeoutSeconds)*time.Second); err != nil {
		return failedFetch(meta, err)
	}
	defer c.release(inst.InstanceID)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(runtime.site.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, key.baseURL+key.path, nil)
	if err != nil {
		return failedFetch(meta, errUpstreamRequest)
	}
	if key.endpoint == EndpointHealth {
		req.URL.Path, req.URL.RawPath = healthPath, ""
	}
	req.Header.Set("User-Agent", runtime.userAgent)
	if project != nil {
		if strings.TrimSpace(inst.APIKey) == "" {
			return failedFetch(meta, ErrUnauthorized)
		}
		req.Header.Set("x-api-key", inst.APIKey)
		if cached.etag != "" {
			req.Header.Set("If-None-Match", cached.etag)
		}
	}
	start := time.Now()
	transport, err := c.transport(req, inst.ProxyEnabled, runtime.proxy)
	if err != nil {
		return failedFetch(meta, safeRequestError(ctx, err))
	}
	defer transport.CloseIdleConnections()
	var charge func()
	if project != nil {
		markSent, release, err := c.reserveAudit(inst, ctx)
		if err != nil {
			return failedFetch(meta, err)
		}
		defer release()
		charge = markSent
		req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				markSent()
			}
		}}))
	}
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return ErrRedirectBlocked
	}}
	resp, err := client.Do(req)
	meta.LatencyMS = time.Since(start).Milliseconds()
	if resp != nil {
		meta.HTTPStatus = resp.StatusCode
		if charge != nil {
			charge()
		}
	}
	if err != nil {
		return failedFetch(meta, safeRequestError(ctx, err))
	}
	defer resp.Body.Close()
	if project == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		meta.VerifiedAt = time.Now().UTC()
		return fetchResult{meta: meta}
	}
	return readAdminResponse(ctx, resp, meta, cached, project, inst.APIKey, runtime.site.MaxResponseBytes, start)
}

func safeRequestError(ctx context.Context, err error) error {
	for _, allowed := range []error{ErrOriginNotAllowed, ErrRedirectBlocked, errProxyConfig} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errUpstreamRequest
}

func (c *Client) transport(req *http.Request, enabled bool, proxyCfg config.ProxyConfig) (*http.Transport, error) {
	// A fresh connection enforces the current address policy and prevents automatic replay on stale connections.
	// net/http may detach its dial context for connection reuse. Keep the deadline for these dials.
	dialWith := func(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
		return func(_ context.Context, network, addr string) (net.Conn, error) {
			return dial(req.Context(), network, addr)
		}
	}
	t := &http.Transport{DialContext: dialWith(c.guard.DialContext), DisableKeepAlives: true}
	if !enabled || !proxyCfg.Enabled {
		return t, nil
	}
	rawProxy, err := proxyCfg.ActiveURL()
	if err != nil {
		return nil, errProxyConfig
	}
	proxy, err := url.Parse(rawProxy)
	if err != nil {
		return nil, errProxyConfig
	}
	_, target, err := parseBaseURL(req.URL.String())
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(target.host); err == nil {
		t.DialContext, t.Proxy = dialWith((&net.Dialer{}).DialContext), http.ProxyURL(proxy)
		return t, nil
	}
	resolver, ok := c.guard.(interface {
		resolveTarget(context.Context, string, string) ([]string, error)
	})
	if !ok {
		return nil, ErrOriginNotAllowed
	}
	targets, err := resolver.resolveTarget(req.Context(), "tcp", net.JoinHostPort(target.host, target.port))
	if err != nil {
		return nil, err
	}
	if proxy.Scheme == "socks5" {
		// SOCKS uses the URL authority for the destination and the request Host for HTTP.
		req.URL.Host = targets[0]
		t.DialContext, t.Proxy = dialWith((&net.Dialer{}).DialContext), http.ProxyURL(proxy)
		t.TLSClientConfig = &tls.Config{ServerName: target.host}
		return t, nil
	}
	// Tunnel to an approved IP while leaving the target HTTP Host and TLS identity intact.
	t.DialContext = dialWith(func(ctx context.Context, network, _ string) (net.Conn, error) {
		var conn net.Conn
		var err error
		for _, addr := range targets {
			conn, err = dialProxyTunnel(ctx, network, proxy, addr)
			if err == nil {
				return conn, nil
			}
		}
		return nil, err
	})
	return t, nil
}

func dialProxyTunnel(ctx context.Context, network string, proxy *url.URL, addr string) (net.Conn, error) {
	var dialer net.Dialer
	dial := dialer.DialContext
	if proxy.Scheme == "https" {
		dial = (&tls.Dialer{NetDialer: &dialer}).DialContext
	}
	conn, err := dial(ctx, network, proxy.Host)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	req := http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		credentials := proxy.User.Username() + ":" + password
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if err = req.Write(conn); err == nil {
		reader := bufio.NewReader(io.LimitReader(conn, 64<<10))
		var resp *http.Response
		resp, err = http.ReadResponse(reader, &req)
		if err == nil && (resp.StatusCode != http.StatusOK || reader.Buffered() != 0) {
			err = errUpstreamRequest
		}
	}
	if !stop() || err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return conn, nil
}

func readAdminResponse(ctx context.Context, resp *http.Response, meta UpstreamFetchMeta, cached observation, project projection, key string, limit int64, start time.Time) fetchResult {
	now := time.Now().UTC()
	switch resp.StatusCode {
	case http.StatusNotModified:
		if cached.etag == "" {
			return failedFetch(meta, errMissingCache)
		}
		meta.ETag, meta.NotModified = cached.etag, true
		meta.ObservedAt, meta.VerifiedAt = cached.observedAt, now
		return fetchResult{projected: cached.projected, meta: meta}
	case http.StatusTooManyRequests:
		meta.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), now)
		return failedFetch(meta, ErrRateLimited)
	case http.StatusUnauthorized:
		return failedFetch(meta, ErrUnauthorized)
	case http.StatusForbidden:
		return failedFetch(meta, ErrForbidden)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return failedFetch(meta, ErrRedirectBlocked)
	}
	if project == nil || (resp.StatusCode != http.StatusNotFound && (resp.StatusCode < 200 || resp.StatusCode >= 300)) {
		return failedFetch(meta, errUpstreamStatus)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err == nil && int64(len(body)) == limit {
		var extra [1]byte
		if _, err = io.ReadFull(resp.Body, extra[:]); err == nil {
			return failedFetch(meta, errResponseLimit)
		}
		if err == io.EOF {
			err = nil
		}
	}
	meta.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		return failedFetch(meta, safeRequestError(ctx, err))
	}
	if strings.HasPrefix(meta.Endpoint, "ops.") {
		if disabled, _ := DetectOpsDisabled(resp.StatusCode, body); disabled {
			return failedFetch(meta, ErrOpsDisabled)
		}
	}
	if resp.StatusCode == http.StatusNotFound {
		if routeNotFound(body) {
			return failedFetch(meta, ErrUnsupportedRoute)
		}
		return failedFetch(meta, errUpstreamStatus)
	}
	projected, err := project(body)
	if err != nil {
		return failedFetch(meta, errInvalidPayload)
	}
	if err := ctx.Err(); err != nil {
		return failedFetch(meta, err)
	}
	if tag := resp.Header.Get("ETag"); len(tag) <= 200 && etagFormat.MatchString(tag) &&
		!strings.Contains(tag, key) && SanitizeErrorText(tag, 200) == tag {
		meta.ETag = tag
	}
	meta.ObservedAt = time.Now().UTC()
	meta.VerifiedAt = meta.ObservedAt
	return fetchResult{projected: projected, meta: meta}
}

func routeNotFound(body []byte) bool {
	body = bytes.TrimSpace(body)
	plain := strings.ToLower(string(body))
	switch plain {
	case "", "404 page not found", "404 not found", "not found", "route not found":
		return true
	}
	var data struct {
		Code    *int   `json:"code"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if len(body) == 0 || body[0] != '{' || json.Unmarshal(body, &data) != nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(data.Reason)) {
	case "ROUTE_NOT_FOUND", "UNSUPPORTED_ROUTE":
		return true
	case "", "NOT_FOUND":
	default:
		return false
	}
	message := strings.ToLower(strings.TrimSpace(data.Message + " " + data.Error))
	switch message {
	case "not found", "404 not found", "404 page not found", "route not found", "endpoint not found":
		return true
	case "":
		return data.Code != nil && *data.Code == http.StatusNotFound
	}
	return false
}

func parseRetryAfter(value string, now time.Time) *time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil && seconds <= uint64((1<<63-1)/time.Second) {
		delay := time.Duration(seconds) * time.Second
		return &delay
	}
	if at, err := http.ParseTime(value); err == nil {
		delay := max(at.Sub(now), 0)
		return &delay
	}
	return nil
}

func (c *Client) ProbeHealth(ctx context.Context, inst TargetEndpoint) (*HealthProbeResult, error) {
	inst.APIKey = ""
	result := c.fetchShared(ctx, inst, EndpointHealth, healthPath, nil)
	return &HealthProbeResult{
		Reachable: result.err == nil, HTTPStatus: result.meta.HTTPStatus, LatencyMS: result.meta.LatencyMS,
		CheckedAt: time.Now().UTC(), ErrorCode: result.meta.ErrorCode, SanitizedError: result.meta.SanitizedError,
	}, result.err
}

func (c *Client) FetchOpsSnapshot(ctx context.Context, inst TargetEndpoint, caps CapabilitiesMap) (*SanitizedOpsOverview, UpstreamFetchMeta, error) {
	c.mu.Lock()
	timeout := time.Duration(c.runtime.site.BatchTimeoutSeconds) * time.Second
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	capability := caps[EndpointOpsSnapshotV2]
	if capability.State != CapabilityUnsupported || capability.FallbackTarget != EndpointOpsOverview {
		value, meta, err := fetch(c, ctx, inst, EndpointOpsSnapshotV2, snapshotPath, ParseOpsOverview)
		if !errors.Is(err, ErrUnsupportedRoute) {
			return value, meta, err
		}
	}
	value, meta, err := fetch(c, ctx, inst, EndpointOpsOverview, overviewPath, ParseOpsOverview)
	meta.FallbackUsed = EndpointOpsOverview
	return value, meta, err
}

func (c *Client) FetchConcurrency(ctx context.Context, inst TargetEndpoint) (*SanitizedConcurrencyPayload, UpstreamFetchMeta, error) {
	return fetch(c, ctx, inst, EndpointOpsConcurrency, concurrencyPath, ParseConcurrency)
}

func (c *Client) FetchAccountAvailability(ctx context.Context, inst TargetEndpoint) (*SanitizedAvailabilityPayload, UpstreamFetchMeta, error) {
	return fetch(c, ctx, inst, EndpointOpsAccountAvail, availabilityPath, ParseAccountAvailability)
}

func (c *Client) FetchGroupsAll(ctx context.Context, inst TargetEndpoint) ([]SanitizedGroupInfo, UpstreamFetchMeta, error) {
	return fetch(c, ctx, inst, EndpointGroupsAll, groupsPath, ParseGroupsAll)
}

func (c *Client) FetchSystemVersion(ctx context.Context, inst TargetEndpoint) (*SanitizedVersionInfo, UpstreamFetchMeta, error) {
	return fetch(c, ctx, inst, EndpointSystemVersion, versionPath, ParseSystemVersion)
}

func (c *Client) FetchOpsAlertEvents(ctx context.Context, inst TargetEndpoint) ([]SanitizedOpsAlertEvent, UpstreamFetchMeta, error) {
	return fetch(c, ctx, inst, EndpointOpsAlertEvents, alertEventsPath, ParseOpsAlertEvents)
}

func (c *Client) FetchIngressRejectHealth(ctx context.Context, inst TargetEndpoint) (*SanitizedIngressRejectHealth, UpstreamFetchMeta, error) {
	return fetch(c, ctx, inst, EndpointOpsIngressHealth, ingressHealthPath, ParseIngressRejectHealth)
}

func (c *Client) FetchAuthCacheHealth(ctx context.Context, inst TargetEndpoint) (*SanitizedAuthCacheHealth, UpstreamFetchMeta, error) {
	return fetch(c, ctx, inst, EndpointOpsAuthCacheHealth, authCachePath, ParseAuthCacheHealth)
}

func (c *Client) FetchAccountsDirectory(ctx context.Context, inst TargetEndpoint) ([]SanitizedAccountSummary, UpstreamFetchMeta, error) {
	c.mu.Lock()
	timeout := time.Duration(c.runtime.site.BatchTimeoutSeconds) * time.Second
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	all := make([]SanitizedAccountSummary, 0)
	var aggregateMeta UpstreamFetchMeta
	aggregateMeta.Endpoint = EndpointAccounts
	allNotModified := true
	var totalLatency int64

	for page := 1; page <= accountsMaxPages; page++ {
		path := fmt.Sprintf("%s?page=%d&page_size=%d", accountsPathPrefix, page, accountsPageSize)
		pageData, meta, err := fetch(c, ctx, inst, EndpointAccounts, path, ParseAccountsPage)
		totalLatency += meta.LatencyMS
		if err != nil {
			meta.LatencyMS = totalLatency
			return nil, meta, err
		}
		if !meta.NotModified {
			allNotModified = false
		}
		aggregateMeta.HTTPStatus = meta.HTTPStatus
		if meta.ObservedAt.After(aggregateMeta.ObservedAt) {
			aggregateMeta.ObservedAt = meta.ObservedAt
		}
		if meta.VerifiedAt.After(aggregateMeta.VerifiedAt) {
			aggregateMeta.VerifiedAt = meta.VerifiedAt
		}
		if pageData == nil || len(pageData.Items) == 0 {
			break
		}
		all = append(all, pageData.Items...)
		pageLimit := accountsPageSize
		if pageData.PageSize > 0 {
			pageLimit = pageData.PageSize
		}
		if len(pageData.Items) < pageLimit {
			break
		}
		if pageData.Total > 0 && int64(len(all)) >= pageData.Total {
			break
		}
	}
	aggregateMeta.LatencyMS = totalLatency
	aggregateMeta.NotModified = allNotModified
	return all, aggregateMeta, nil
}

package siteops

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	urlCredentials    = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/<>"']+@`)
	secretHeader      = regexp.MustCompile(`(?i)\b(?:set[-_]cookie|cookie|(?:proxy[-_])?authorization)["']?[ \t]*[:=][ \t]*[^\r\n]*`)
	secretField       = regexp.MustCompile(`(?i)\b(?:x[-_]api[-_]key|api[-_]key|password|passwd|(?:access[-_]|refresh[-_]|id[-_])?token|client[-_]secret)["']?[ \t]*[:=][ \t]*(?:(?:Bearer|Basic)[ \t]+)?(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;<>"'\]}]+)`)
	bearerToken       = regexp.MustCompile(`(?i)\bBearer\s+(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;<>"'\]}]+)`)
	credentialToken   = regexp.MustCompile(`(?i)\b(?:sk-|adm_|admin-)[a-z0-9._~+/=-]+`)
	keyHintChars      = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	errInvalidPayload = errors.New("invalid site-ops upstream response")
)

func SanitizeErrorText(raw string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 200
	}
	raw = urlCredentials.ReplaceAllString(raw, "${1}[redacted]@")
	for _, pattern := range []*regexp.Regexp{secretHeader, secretField, bearerToken, credentialToken} {
		raw = pattern.ReplaceAllString(raw, "[redacted]")
	}
	text := strings.Join(strings.Fields(raw), " ")
	for i := range text {
		if maxRunes == 0 {
			return text[:i]
		}
		maxRunes--
	}
	return text
}

func MaskAPIKeyHint(rawKey string) string {
	key := strings.TrimSpace(rawKey)
	prefixLen := 4
	for _, prefix := range []string{"adm_sc_", "adm_", "admin-", "sk-"} {
		if strings.HasPrefix(key, prefix) {
			prefixLen = len(prefix) + 2
			break
		}
	}
	if len(key) < prefixLen+6 || !keyHintChars.MatchString(key) {
		return "***"
	}
	return key[:prefixLen] + "..." + key[len(key)-2:]
}

type upstreamEnvelope struct {
	Code   *int            `json:"code"`
	Reason string          `json:"reason"`
	Data   json.RawMessage `json:"data"`
}

func DetectOpsDisabled(httpStatus int, body []byte) (bool, string) {
	if httpStatus != http.StatusNotFound && httpStatus != http.StatusOK {
		return false, ""
	}
	var envelope upstreamEnvelope
	if json.Unmarshal(body, &envelope) != nil {
		return false, ""
	}
	if httpStatus == http.StatusNotFound {
		var data upstreamEnvelope
		_ = json.Unmarshal(envelope.Data, &data)
		if strings.EqualFold(envelope.Reason, "OPS_DISABLED") || strings.EqualFold(data.Reason, "OPS_DISABLED") {
			return true, "OPS_DISABLED"
		}
		return false, ""
	}
	if envelope.Code != nil && *envelope.Code != 0 {
		return false, ""
	}
	if len(envelope.Data) > 0 {
		body = envelope.Data
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) == nil && object != nil &&
		(len(object) == 0 || bytes.Equal(bytes.TrimSpace(object["enabled"]), []byte("false"))) {
		return true, "OPS_DISABLED"
	}
	return false, ""
}

func decodeProjection(body []byte, dst any) error {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '{' {
		var envelope upstreamEnvelope
		if json.Unmarshal(body, &envelope) != nil || (envelope.Code != nil && *envelope.Code != 0) {
			return errInvalidPayload
		}
		if len(envelope.Data) > 0 {
			body = bytes.TrimSpace(envelope.Data)
		}
	}
	if len(body) == 0 || bytes.Equal(body, []byte("null")) || json.Unmarshal(body, dst) != nil {
		return errInvalidPayload
	}
	return nil
}

type overviewMetrics struct {
	CreatedAt             *time.Time `json:"created_at"`
	CollectedAt           *time.Time `json:"collected_at"`
	CPUUsagePercent       *float64   `json:"cpu_usage_percent"`
	CPUPercent            *float64   `json:"cpu_percent"`
	MemoryUsagePercent    *float64   `json:"memory_usage_percent"`
	MemoryPercent         *float64   `json:"memory_percent"`
	MemoryUsedMB          *int64     `json:"memory_used_mb"`
	DBOK                  *bool      `json:"db_ok"`
	DBConnActive          *int       `json:"db_conn_active"`
	DBConnWaiting         *int       `json:"db_conn_waiting"`
	DBMaxOpenConns        *int       `json:"db_max_open_conns"`
	RedisOK               *bool      `json:"redis_ok"`
	RedisConnTotal        *int       `json:"redis_conn_total"`
	RedisConnIdle         *int       `json:"redis_conn_idle"`
	RedisPoolSize         *int       `json:"redis_pool_size"`
	GoroutineCount        *int       `json:"goroutine_count"`
	ConcurrencyQueueDepth *int       `json:"concurrency_queue_depth"`
	QueueDepth            *int       `json:"queue_depth"`
	AccountSwitchCount    *int64     `json:"account_switch_count"`
}

type overviewWire struct {
	SanitizedOpsOverview
	SLA                   *float64 `json:"sla"`
	ErrorRate             *float64 `json:"error_rate"`
	UpstreamErrorRate     *float64 `json:"upstream_error_rate"`
	RequestCountTotal     *int64   `json:"request_count_total"`
	ErrorCountTotal       *int64   `json:"error_count_total"`
	Upstream429Count      *int64   `json:"upstream_429_count"`
	Upstream529Count      *int64   `json:"upstream_529_count"`
	UpstreamErrExcl429529 *int64   `json:"upstream_error_count_excl_429_529"`
	QPS                   struct {
		Current *float64 `json:"current"`
		Peak    *float64 `json:"peak"`
	} `json:"qps"`
	TPS struct {
		Current *float64 `json:"current"`
	} `json:"tps"`
	TTFT struct {
		P50MS *int `json:"p50_ms"`
		P95MS *int `json:"p95_ms"`
	} `json:"ttft"`
	Duration struct {
		P95MS *int `json:"p95_ms"`
	} `json:"duration"`
	OutputTPS struct {
		P50 *float64 `json:"p50"`
	} `json:"output_tps"`
	SystemMetrics overviewMetrics `json:"system_metrics"`
	JobHeartbeats []struct {
		JobName       string     `json:"job_name"`
		Status        string     `json:"status"`
		LastSuccessAt *time.Time `json:"last_success_at"`
		LastRunAt     *time.Time `json:"last_run_at"`
		LastErrorAt   *time.Time `json:"last_error_at"`
		LastError     string     `json:"last_error"`
	} `json:"job_heartbeats"`
}

func preferValue[T any](preferred, fallback *T) *T {
	if preferred != nil {
		return preferred
	}
	return fallback
}

// normalizeRatesToPercent 将 Sub2API v0.2.24 返回的 0..1 比例值统一转为 0..100 百分数；
// 若上游已按百分数返回（任一比率字段 > 1），则保留原值。
func normalizeRatesToPercent(sla, errRate, upErrRate *float64) (*float64, *float64, *float64) {
	alreadyPercent := (sla != nil && *sla > 1) || (errRate != nil && *errRate > 1) || (upErrRate != nil && *upErrRate > 1)
	scale := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		out := *v
		if !alreadyPercent && out >= 0 && out <= 1 {
			out = math.Round(out*10000) / 100
		}
		return &out
	}
	return scale(sla), scale(errRate), scale(upErrRate)
}

func ParseOpsOverview(body []byte) (*SanitizedOpsOverview, error) {
	var wire struct {
		overviewWire
		Overview *overviewWire `json:"overview"`
	}
	if err := decodeProjection(body, &wire); err != nil {
		return nil, err
	}
	raw := &wire.overviewWire
	if wire.Overview != nil {
		raw = wire.Overview
	}
	out := raw.SanitizedOpsOverview
	m := raw.SystemMetrics
	out.SystemMetricsAt = preferValue(m.CreatedAt, preferValue(m.CollectedAt, out.SystemMetricsAt))
	out.CPUPercent = preferValue(m.CPUUsagePercent, preferValue(m.CPUPercent, out.CPUPercent))
	out.MemPercent = preferValue(m.MemoryUsagePercent, preferValue(m.MemoryPercent, out.MemPercent))
	out.MemUsedMB = preferValue(m.MemoryUsedMB, out.MemUsedMB)
	out.DBOK = preferValue(m.DBOK, out.DBOK)
	out.DBConnActive = preferValue(m.DBConnActive, out.DBConnActive)
	out.DBConnWaiting = preferValue(m.DBConnWaiting, out.DBConnWaiting)
	out.DBMaxOpenConns = preferValue(m.DBMaxOpenConns, out.DBMaxOpenConns)
	out.RedisOK = preferValue(m.RedisOK, out.RedisOK)
	out.RedisConnTotal = preferValue(m.RedisConnTotal, out.RedisConnTotal)
	out.RedisConnIdle = preferValue(m.RedisConnIdle, out.RedisConnIdle)
	out.RedisPoolSize = preferValue(m.RedisPoolSize, out.RedisPoolSize)
	out.GoroutineCount = preferValue(m.GoroutineCount, out.GoroutineCount)
	out.QueueDepth = preferValue(m.ConcurrencyQueueDepth, preferValue(m.QueueDepth, out.QueueDepth))
	out.AccountSwitchCount1m = preferValue(m.AccountSwitchCount, out.AccountSwitchCount1m)
	slaPct, errPct, upErrPct := normalizeRatesToPercent(raw.SLA, raw.ErrorRate, raw.UpstreamErrorRate)
	out.SLAPercent1h = preferValue(slaPct, out.SLAPercent1h)
	out.ErrorRatePercent1h = preferValue(errPct, out.ErrorRatePercent1h)
	out.UpstreamErrorRatePct1h = preferValue(upErrPct, out.UpstreamErrorRatePct1h)
	out.ReqTotal1h = preferValue(raw.RequestCountTotal, out.ReqTotal1h)
	out.ErrTotal1h = preferValue(raw.ErrorCountTotal, out.ErrTotal1h)
	out.Upstream4291h = preferValue(raw.Upstream429Count, out.Upstream4291h)
	out.Upstream5291h = preferValue(raw.Upstream529Count, out.Upstream5291h)
	out.UpstreamErrExcl4295291h = preferValue(raw.UpstreamErrExcl429529, out.UpstreamErrExcl4295291h)
	out.QPSCurrent = preferValue(raw.QPS.Current, out.QPSCurrent)
	out.QPSPeak = preferValue(raw.QPS.Peak, out.QPSPeak)
	out.TPSCurrent = preferValue(raw.TPS.Current, out.TPSCurrent)
	out.TTFTP50MS = preferValue(raw.TTFT.P50MS, out.TTFTP50MS)
	out.TTFTP95MS = preferValue(raw.TTFT.P95MS, out.TTFTP95MS)
	out.DurationP95MS = preferValue(raw.Duration.P95MS, out.DurationP95MS)
	out.OutputTPSP50 = preferValue(raw.OutputTPS.P50, out.OutputTPSP50)
	for _, heartbeat := range raw.JobHeartbeats {
		rawErr := strings.TrimSpace(heartbeat.LastError)
		recovered := rawErr != "" && heartbeat.LastErrorAt != nil && heartbeat.LastSuccessAt != nil && heartbeat.LastSuccessAt.After(*heartbeat.LastErrorAt)
		hasError := rawErr != "" && !recovered
		status := strings.ToLower(SanitizeErrorText(heartbeat.Status, 200))
		if status == "" {
			if hasError {
				status = "error"
			} else if heartbeat.LastSuccessAt != nil {
				status = "ok"
			}
		}
		if status == "error" {
			hasError = true
		}
		errSummary := ""
		if hasError && rawErr != "" {
			errSummary = SanitizeErrorText(rawErr, 0)
		}
		out.JobHeartbeats = append(out.JobHeartbeats, SanitizedJobHeartbeat{
			JobName: SanitizeErrorText(heartbeat.JobName, 200), Status: status,
			LastSuccessAt: heartbeat.LastSuccessAt, LastRunAt: heartbeat.LastRunAt,
			HasError: hasError, ErrorSummary: errSummary,
		})
	}
	return &out, nil
}

type realtimeWire[A, G any] struct {
	Enabled   *bool       `json:"enabled"`
	Timestamp *time.Time  `json:"timestamp"`
	Account   map[int64]A `json:"account"`
	Accounts  map[int64]A `json:"accounts"`
	Group     map[int64]G `json:"group"`
	Groups    map[int64]G `json:"groups"`
}

func (w *realtimeWire[A, G]) normalize() {
	if w.Accounts == nil {
		w.Accounts = w.Account
	}
	if w.Accounts == nil {
		w.Accounts = make(map[int64]A)
	}
	if w.Groups == nil {
		w.Groups = w.Group
	}
	if w.Groups == nil {
		w.Groups = make(map[int64]G)
	}
}

func ParseConcurrency(body []byte) (*SanitizedConcurrencyPayload, error) {
	var wire realtimeWire[SanitizedAccountConcurrency, SanitizedGroupConcurrency]
	if err := decodeProjection(body, &wire); err != nil {
		return nil, err
	}
	wire.normalize()
	for id, account := range wire.Accounts {
		if account.AccountID == 0 {
			account.AccountID = id
		}
		account.Platform = SanitizeErrorText(account.Platform, 200)
		wire.Accounts[id] = account
	}
	for id, group := range wire.Groups {
		if group.GroupID == 0 {
			group.GroupID = id
		}
		group.GroupName = SanitizeErrorText(group.GroupName, 200)
		group.Platform = SanitizeErrorText(group.Platform, 200)
		wire.Groups[id] = group
	}
	return &SanitizedConcurrencyPayload{
		Enabled: wire.Enabled, Timestamp: wire.Timestamp, Accounts: wire.Accounts, Groups: wire.Groups,
		RawEmpty: len(wire.Accounts) == 0 && len(wire.Groups) == 0,
	}, nil
}

type accountAvailabilityWire struct {
	AccountID     int64  `json:"account_id"`
	Platform      string `json:"platform"`
	Available     bool   `json:"available"`
	RateLimited   bool   `json:"rate_limited"`
	Overloaded    bool   `json:"overloaded"`
	IsAvailable   *bool  `json:"is_available"`
	IsRateLimited *bool  `json:"is_rate_limited"`
	IsOverloaded  *bool  `json:"is_overloaded"`
	HasError      bool   `json:"has_error"`
	ErrorMessage  string `json:"error_message"`
}

type groupAvailabilityWire struct {
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name"`
	Platform       string `json:"platform"`
	TotalAccounts  *int64 `json:"total_accounts"`
	TotalCount     *int64 `json:"total_count"`
	AvailableCount int64  `json:"available_count"`
	RateLimitCount int64  `json:"rate_limit_count"`
	OverloadCount  int64  `json:"overload_count"`
	ErrorCount     int64  `json:"error_count"`
}

func ParseAccountAvailability(body []byte) (*SanitizedAvailabilityPayload, error) {
	var wire realtimeWire[accountAvailabilityWire, groupAvailabilityWire]
	if err := decodeProjection(body, &wire); err != nil {
		return nil, err
	}
	wire.normalize()
	accounts := make(map[int64]SanitizedAccountAvailability, len(wire.Accounts))
	for id, account := range wire.Accounts {
		if account.AccountID == 0 {
			account.AccountID = id
		}
		accounts[id] = SanitizedAccountAvailability{
			AccountID: account.AccountID, Platform: SanitizeErrorText(account.Platform, 200),
			Available:    *preferValue(account.IsAvailable, &account.Available),
			RateLimited:  *preferValue(account.IsRateLimited, &account.RateLimited),
			Overloaded:   *preferValue(account.IsOverloaded, &account.Overloaded),
			HasError:     account.HasError || strings.TrimSpace(account.ErrorMessage) != "",
			ErrorSummary: SanitizeErrorText(account.ErrorMessage, 0),
		}
	}
	groups := make(map[int64]SanitizedGroupAvailability, len(wire.Groups))
	for id, group := range wire.Groups {
		gid := group.GroupID
		if gid == 0 {
			gid = id
		}
		var total int64
		if picked := preferValue(group.TotalAccounts, group.TotalCount); picked != nil {
			total = *picked
		}
		groups[id] = SanitizedGroupAvailability{
			GroupID:        gid,
			GroupName:      SanitizeErrorText(group.GroupName, 200),
			Platform:       SanitizeErrorText(group.Platform, 200),
			TotalCount:     total,
			AvailableCount: group.AvailableCount,
			RateLimitCount: group.RateLimitCount,
			OverloadCount:  group.OverloadCount,
			ErrorCount:     group.ErrorCount,
		}
	}
	return &SanitizedAvailabilityPayload{
		Enabled: wire.Enabled, Timestamp: wire.Timestamp, Accounts: accounts, Groups: groups,
		RawEmpty: len(accounts) == 0 && len(groups) == 0,
	}, nil
}

func ParseGroupsAll(body []byte) ([]SanitizedGroupInfo, error) {
	var groups []SanitizedGroupInfo
	if err := decodeProjection(body, &groups); err != nil {
		return nil, err
	}
	for i := range groups {
		groups[i].Name = SanitizeErrorText(groups[i].Name, 200)
		groups[i].Platform = SanitizeErrorText(groups[i].Platform, 200)
		groups[i].Status = SanitizeErrorText(groups[i].Status, 200)
	}
	return groups, nil
}

func ParseSystemVersion(body []byte) (*SanitizedVersionInfo, error) {
	var out SanitizedVersionInfo
	if err := decodeProjection(body, &out); err != nil {
		return nil, err
	}
	out.Version = SanitizeErrorText(out.Version, 200)
	if out.Version == "" {
		return nil, errInvalidPayload
	}
	return &out, nil
}

func decodeEnvelopeData(body []byte) ([]byte, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '{' {
		var envelope upstreamEnvelope
		if json.Unmarshal(body, &envelope) != nil || (envelope.Code != nil && *envelope.Code != 0) {
			return nil, errInvalidPayload
		}
		if len(envelope.Data) > 0 {
			body = bytes.TrimSpace(envelope.Data)
		}
	}
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return nil, errInvalidPayload
	}
	return body, nil
}

func parseDurationMS(raw json.RawMessage) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, false
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		str = strings.TrimSpace(str)
		if str == "" {
			return 0, false
		}
		if d, err := time.ParseDuration(str); err == nil {
			return d.Milliseconds(), true
		}
		if n, err := strconv.ParseInt(str, 10, 64); err == nil {
			return n, true
		}
		return 0, false
	}
	var num float64
	if json.Unmarshal(raw, &num) == nil && !math.IsNaN(num) && !math.IsInf(num, 0) {
		return int64(math.Round(num)), true
	}
	return 0, false
}

type opsAlertEventWire struct {
	ID             int64      `json:"id"`
	RuleID         int64      `json:"rule_id"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Message        string     `json:"message"`
	MetricValue    *float64   `json:"metric_value"`
	ThresholdValue *float64   `json:"threshold_value"`
	FiredAt        time.Time  `json:"fired_at"`
	CreatedAt      *time.Time `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
}

func ParseOpsAlertEvents(raw []byte) ([]SanitizedOpsAlertEvent, error) {
	payload, err := decodeEnvelopeData(raw)
	if err != nil {
		return nil, err
	}
	var wireItems []opsAlertEventWire
	switch payload[0] {
	case '[':
		if err := json.Unmarshal(payload, &wireItems); err != nil {
			return nil, errInvalidPayload
		}
	case '{':
		var wrapped struct {
			Items  []opsAlertEventWire `json:"items"`
			Events []opsAlertEventWire `json:"events"`
		}
		if err := json.Unmarshal(payload, &wrapped); err != nil {
			return nil, errInvalidPayload
		}
		if wrapped.Items != nil {
			wireItems = wrapped.Items
		} else {
			wireItems = wrapped.Events
		}
	default:
		return nil, errInvalidPayload
	}
	out := make([]SanitizedOpsAlertEvent, 0, len(wireItems))
	for _, item := range wireItems {
		desc := item.Description
		if strings.TrimSpace(desc) == "" {
			desc = item.Message
		}
		firedAt := item.FiredAt
		if firedAt.IsZero() && item.CreatedAt != nil {
			firedAt = *item.CreatedAt
		}
		out = append(out, SanitizedOpsAlertEvent{
			ID:             item.ID,
			RuleID:         item.RuleID,
			Severity:       strings.ToLower(SanitizeErrorText(item.Severity, 32)),
			Status:         strings.ToLower(SanitizeErrorText(item.Status, 32)),
			Title:          SanitizeErrorText(item.Title, 200),
			Description:    SanitizeErrorText(desc, 200),
			MetricValue:    item.MetricValue,
			ThresholdValue: item.ThresholdValue,
			FiredAt:        firedAt,
			ResolvedAt:     item.ResolvedAt,
		})
	}
	return out, nil
}

func ParseIngressRejectHealth(raw []byte) (*SanitizedIngressRejectHealth, error) {
	payload, err := decodeEnvelopeData(raw)
	if err != nil {
		return nil, err
	}
	if payload[0] != '{' {
		return nil, errInvalidPayload
	}
	var out SanitizedIngressRejectHealth
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, errInvalidPayload
	}
	out.LastError = SanitizeErrorText(out.LastError, 200)
	return &out, nil
}

type authCacheOutboxWire struct {
	Running      bool            `json:"running"`
	Pending      int64           `json:"pending"`
	Failures     uint64          `json:"failures"`
	OldestLag    json.RawMessage `json:"oldest_lag"`
	OldestLagMS  *int64          `json:"oldest_lag_ms"`
	HealthySLA   json.RawMessage `json:"healthy_sla"`
	HealthySLAMS *int64          `json:"healthy_sla_ms"`
	LastError    string          `json:"last_error"`
	StatsError   string          `json:"stats_error"`
}

type authCacheSubscriberWire struct {
	Healthy   *bool  `json:"healthy"`
	Running   *bool  `json:"running"`
	Connected *bool  `json:"connected"`
	LastError string `json:"last_error"`
	Error     string `json:"error"`
}

func ParseAuthCacheHealth(raw []byte) (*SanitizedAuthCacheHealth, error) {
	payload, err := decodeEnvelopeData(raw)
	if err != nil {
		return nil, err
	}
	if payload[0] != '{' {
		return nil, errInvalidPayload
	}
	var wire struct {
		Outbox     *authCacheOutboxWire     `json:"outbox"`
		Subscriber *authCacheSubscriberWire `json:"subscriber"`
		Healthy    *bool                    `json:"healthy"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, errInvalidPayload
	}
	if wire.Outbox == nil && wire.Subscriber == nil && wire.Healthy == nil {
		return nil, errInvalidPayload
	}
	var out SanitizedAuthCacheHealth
	if wire.Outbox != nil {
		out.OutboxRunning = wire.Outbox.Running
		out.OutboxPending = wire.Outbox.Pending
		out.OutboxFailures = wire.Outbox.Failures
		if wire.Outbox.OldestLagMS != nil {
			out.OutboxOldestMS = *wire.Outbox.OldestLagMS
		} else if ms, ok := parseDurationMS(wire.Outbox.OldestLag); ok {
			out.OutboxOldestMS = ms
		}
		if wire.Outbox.HealthySLAMS != nil {
			out.OutboxHealthySLA = *wire.Outbox.HealthySLAMS
		} else if ms, ok := parseDurationMS(wire.Outbox.HealthySLA); ok {
			out.OutboxHealthySLA = ms
		}
		errParts := make([]string, 0, 2)
		if s := strings.TrimSpace(wire.Outbox.LastError); s != "" {
			errParts = append(errParts, s)
		}
		if s := strings.TrimSpace(wire.Outbox.StatsError); s != "" {
			errParts = append(errParts, s)
		}
		if len(errParts) > 0 {
			out.OutboxLastError = SanitizeErrorText(strings.Join(errParts, "; "), 200)
		}
	}
	if wire.Subscriber != nil {
		rawSubErr := strings.TrimSpace(wire.Subscriber.LastError)
		if rawSubErr == "" {
			rawSubErr = strings.TrimSpace(wire.Subscriber.Error)
		}
		if rawSubErr != "" {
			out.SubscriberError = SanitizeErrorText(rawSubErr, 200)
		}
		switch {
		case wire.Subscriber.Healthy != nil:
			out.SubscriberOK = *wire.Subscriber.Healthy
		case wire.Subscriber.Running != nil && wire.Subscriber.Connected != nil:
			out.SubscriberOK = *wire.Subscriber.Running && *wire.Subscriber.Connected
		case wire.Subscriber.Connected != nil:
			out.SubscriberOK = *wire.Subscriber.Connected
		case wire.Subscriber.Running != nil:
			out.SubscriberOK = *wire.Subscriber.Running
		}
		if wire.Subscriber.Healthy == nil && out.SubscriberError != "" {
			out.SubscriberOK = false
		}
	}
	outboxHealthy := out.OutboxRunning && out.OutboxLastError == "" &&
		(out.OutboxHealthySLA <= 0 || out.OutboxOldestMS <= out.OutboxHealthySLA)
	out.Healthy = outboxHealthy && out.SubscriberOK
	if wire.Healthy != nil && !*wire.Healthy {
		out.Healthy = false
	}
	return &out, nil
}

type accountSummaryWire struct {
	ID                     int64      `json:"id"`
	AccountID              int64      `json:"account_id"`
	Name                   string     `json:"name"`
	Platform               string     `json:"platform"`
	Type                   string     `json:"type"`
	Status                 string     `json:"status"`
	Schedulable            bool       `json:"schedulable"`
	Concurrency            int        `json:"concurrency"`
	Priority               int        `json:"priority"`
	GroupIDs               []int64    `json:"group_ids"`
	ExpiresAt              *time.Time `json:"expires_at"`
	RateLimitResetAt       *time.Time `json:"rate_limit_reset_at"`
	OverloadUntil          *time.Time `json:"overload_until"`
	TempUnschedulableUntil *time.Time `json:"temp_unschedulable_until"`
	ErrorMessage           string     `json:"error_message"`
}

func ParseAccountsPage(raw []byte) (*AccountsPagePayload, error) {
	payload, err := decodeEnvelopeData(raw)
	if err != nil {
		return nil, err
	}
	var wireItems []accountSummaryWire
	var total int64
	var page, pageSize int
	switch payload[0] {
	case '[':
		if err := json.Unmarshal(payload, &wireItems); err != nil {
			return nil, errInvalidPayload
		}
		total = int64(len(wireItems))
		page = 1
		pageSize = len(wireItems)
	case '{':
		var wrapped struct {
			Items    []accountSummaryWire `json:"items"`
			Accounts []accountSummaryWire `json:"accounts"`
			Total    int64                `json:"total"`
			Page     int                  `json:"page"`
			PageSize int                  `json:"page_size"`
		}
		if err := json.Unmarshal(payload, &wrapped); err != nil {
			return nil, errInvalidPayload
		}
		if wrapped.Items != nil {
			wireItems = wrapped.Items
		} else {
			wireItems = wrapped.Accounts
		}
		total = wrapped.Total
		page = wrapped.Page
		pageSize = wrapped.PageSize
	default:
		return nil, errInvalidPayload
	}
	items := make([]SanitizedAccountSummary, 0, len(wireItems))
	for _, item := range wireItems {
		accountID := item.ID
		if accountID == 0 {
			accountID = item.AccountID
		}
		items = append(items, SanitizedAccountSummary{
			AccountID:              accountID,
			Name:                   SanitizeErrorText(item.Name, 200),
			Platform:               SanitizeErrorText(item.Platform, 64),
			Type:                   SanitizeErrorText(item.Type, 64),
			Status:                 strings.ToLower(SanitizeErrorText(item.Status, 64)),
			Schedulable:            item.Schedulable,
			Concurrency:            item.Concurrency,
			Priority:               item.Priority,
			GroupIDs:               slices.Clone(item.GroupIDs),
			ExpiresAt:              item.ExpiresAt,
			RateLimitResetAt:       item.RateLimitResetAt,
			OverloadUntil:          item.OverloadUntil,
			TempUnschedulableUntil: item.TempUnschedulableUntil,
			ErrorSummary:           SanitizeErrorText(item.ErrorMessage, 200),
		})
	}
	return &AccountsPagePayload{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

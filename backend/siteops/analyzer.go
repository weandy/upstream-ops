package siteops

import (
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

type AnalyzeInput struct {
	InstanceID         uint
	Now                time.Time
	ObservedAt         time.Time
	VerifiedAt         time.Time
	Health             *HealthProbeResult
	Overview           *SanitizedOpsOverview
	Concurrency        *SanitizedConcurrencyPayload
	Availability       *SanitizedAvailabilityPayload
	Groups             []SanitizedGroupInfo
	Version            *SanitizedVersionInfo
	FieldQuality       FieldQualityMap
	FallbackUsed       string
	PreviousTrusted    *storage.SiteOpsSnapshot
	PreviousLiveGroups bool
}

type AnalyzeOutput struct {
	Snapshot        storage.SiteOpsSnapshot
	Detail          SnapshotDetailPayload
	FieldQuality    FieldQualityMap
	ConcLoadPercent *float64
	Groups          []GroupCapacityItem
}

func AnalyzeCollection(in AnalyzeInput) AnalyzeOutput {
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	in.Now = now
	observed, verified := in.ObservedAt.UTC(), in.VerifiedAt.UTC()
	if observed.IsZero() {
		observed = now
	}
	if verified.IsZero() {
		verified = now
	}
	quality := completeFieldQuality(in.FieldQuality)
	out := AnalyzeOutput{
		Snapshot: storage.SiteOpsSnapshot{InstanceID: in.InstanceID, ObservedAt: observed, VerifiedAt: verified},
		Detail:   SnapshotDetailPayload{FallbackUsed: in.FallbackUsed}, FieldQuality: quality,
	}
	snap := &out.Snapshot
	if in.Health != nil {
		quality[DomainReachability] = QualityTrusted
		snap.Reachable, snap.ProbeLatencyMS = in.Health.Reachable, in.Health.LatencyMS
	}
	if in.Version != nil && in.Version.Version != "" {
		quality[DomainSystemVersion] = QualityTrusted
	}
	projectOverview(in.Overview, &out, now)
	conc, avail := in.Concurrency, in.Availability
	if conc != nil && conc.Enabled != nil && !*conc.Enabled {
		quality[DomainConcurrency] = QualityDisabled
		conc = nil
	}
	if avail != nil && ((avail.Enabled != nil && !*avail.Enabled) || (avail.RawEmpty && avail.Enabled == nil)) {
		quality[DomainAccountAvailability] = QualityDisabled
		avail = nil
	}
	if conc != nil && conc.RawEmpty && conc.Enabled == nil {
		quality[DomainConcurrency] = QualityDisabled
	}
	var total poolMetrics
	pools := make(map[string]*poolMetrics)
	pool := func(platform string) *poolMetrics {
		if platform == "" {
			platform = "unknown"
		}
		if pools[platform] == nil {
			pools[platform] = &poolMetrics{}
		}
		return pools[platform]
	}
	var accounts map[int64]SanitizedAccountConcurrency
	if conc != nil && conc.Accounts != nil && quality[DomainConcurrency] != QualityDisabled {
		accounts = distinctAccounts(conc.Accounts, func(a SanitizedAccountConcurrency) int64 { return a.AccountID })
		quality[DomainConcurrency] = QualityTrusted
	}
	var availability map[int64]SanitizedAccountAvailability
	if avail != nil && avail.Accounts != nil {
		availability = distinctAccounts(avail.Accounts, func(a SanitizedAccountAvailability) int64 { return a.AccountID })
		quality[DomainAccountAvailability] = QualityTrusted
	}
	for id, account := range accounts {
		platform := account.Platform
		if platform == "" {
			platform = availability[id].Platform
		}
		total.addConcurrency(account)
		pool(platform).addConcurrency(account)
	}
	for id, account := range availability {
		platform := accounts[id].Platform
		if platform == "" {
			platform = account.Platform
		}
		total.addAvailability(account)
		pool(platform).addAvailability(account)
	}
	if availability != nil {
		snap.AccountsTotal, snap.AccountsOpsAvailable = &total.accounts, &total.available
		snap.AccountsRateLimited, snap.AccountsOverloaded, snap.AccountsError = &total.rateLimited, &total.overloaded, &total.failed
	}
	accountCount := len(accounts)
	if availability != nil {
		accountCount = len(availability)
	}
	trustReason := concurrencyTrustReason(in, conc, accounts != nil, total.used, int64(accountCount))
	if trustReason != "" {
		quality[DomainConcurrency] = QualityUnverifiedZero
		out.Detail.ConcTrustReason = trustReason
	}
	if accounts != nil {
		snap.ConcUsed, snap.ConcMax, snap.ConcWaiting = total.concurrency(trustReason != "")
		out.ConcLoadPercent = loadPercent(snap.ConcUsed, snap.ConcMax)
	}
	for _, name := range slices.Sorted(maps.Keys(pools)) {
		p := pools[name]
		// The wire contract has required availability counters for each platform.
		if p.accounts == 0 {
			continue
		}
		item := PlatformPoolSummary{Platform: name, AccountsTotal: p.accounts, AccountsOpsAvailable: p.available,
			AccountsRateLimited: p.rateLimited, AccountsOverloaded: p.overloaded, AccountsError: p.failed}
		if p.hasConcurrency {
			item.ConcUsed, item.ConcMax, item.ConcWaiting = p.concurrency(trustReason != "")
			item.LoadPercent = loadPercent(item.ConcUsed, item.ConcMax)
		}
		out.Detail.PlatformBreakdown = append(out.Detail.PlatformBreakdown, item)
	}
	out.Groups, quality[DomainGroupCapacity] = analyzeGroups(in.Groups, conc, avail, quality, observed, trustReason)
	snap.ConcTrustState = string(quality[DomainConcurrency])
	snap.FieldQualityJSON = jsonString(quality)
	snap.DetailJSON = jsonString(out.Detail)
	return out
}

func completeFieldQuality(existing FieldQualityMap) FieldQualityMap {
	quality := make(FieldQualityMap, 7)
	for _, domain := range []string{DomainReachability, DomainOpsOverview, DomainInfraMetrics, DomainConcurrency,
		DomainAccountAvailability, DomainGroupCapacity, DomainSystemVersion} {
		quality[domain] = QualityUnavailable
		if existing[domain] != "" {
			quality[domain] = existing[domain]
		}
	}
	return quality
}

func projectOverview(data *SanitizedOpsOverview, out *AnalyzeOutput, now time.Time) {
	if data == nil {
		return
	}
	quality, snap := out.FieldQuality, &out.Snapshot
	if data.OpsEnabled != nil && !*data.OpsEnabled {
		quality[DomainOpsOverview], quality[DomainInfraMetrics] = QualityDisabled, QualityDisabled
		return
	}
	quality[DomainOpsOverview] = QualityTrusted
	snap.HealthScore, snap.UptimeSeconds = data.HealthScore, data.UptimeSeconds
	snap.SLAPercent1h, snap.ErrorRatePercent1h, snap.UpstreamErrorRatePct1h = data.SLAPercent1h, data.ErrorRatePercent1h, data.UpstreamErrorRatePct1h
	snap.ReqTotal1h, snap.ErrTotal1h = data.ReqTotal1h, data.ErrTotal1h
	snap.Upstream4291h, snap.Upstream5291h, snap.UpstreamErrExcl4295291h = data.Upstream4291h, data.Upstream5291h, data.UpstreamErrExcl4295291h
	snap.QPSCurrent, snap.QPSPeak, snap.TPSCurrent = data.QPSCurrent, data.QPSPeak, data.TPSCurrent
	snap.RPM5m, snap.TPM5m = data.RPM5m, data.TPM5m
	snap.TTFTP50MS, snap.TTFTP95MS, snap.DurationP95MS, snap.OutputTPSP50 = data.TTFTP50MS, data.TTFTP95MS, data.DurationP95MS, data.OutputTPSP50
	out.Detail.SystemMetricsAt = data.SystemMetricsAt
	out.Detail.JobHeartbeats = data.JobHeartbeats
	snap.AccountSwitchCount1m = data.AccountSwitchCount1m
	if data.SystemMetricsAt != nil && now.Sub(*data.SystemMetricsAt) > 180*time.Second {
		quality[DomainInfraMetrics] = QualityStale
		return
	}
	if hasInfraMetrics(data) {
		quality[DomainInfraMetrics] = QualityTrusted
	}
	snap.CPUPercent, snap.MemPercent, snap.MemUsedMB = data.CPUPercent, data.MemPercent, data.MemUsedMB
	snap.DBOK, snap.DBConnActive, snap.DBConnWaiting, snap.DBMaxOpenConns = data.DBOK, data.DBConnActive, data.DBConnWaiting, data.DBMaxOpenConns
	snap.RedisOK, snap.RedisConnTotal, snap.RedisConnIdle, snap.RedisPoolSize = data.RedisOK, data.RedisConnTotal, data.RedisConnIdle, data.RedisPoolSize
	snap.GoroutineCount, snap.QueueDepth = data.GoroutineCount, data.QueueDepth
	if data.AccountSwitchCount1m != nil && data.QPSCurrent != nil && *data.QPSCurrent*60 >= 20 {
		out.Detail.EstimatedSwitchPct1m = ptr(float64(*data.AccountSwitchCount1m) / (*data.QPSCurrent * 60) * 100)
	}
}

func hasInfraMetrics(data *SanitizedOpsOverview) bool {
	return data.CPUPercent != nil || data.MemPercent != nil || data.MemUsedMB != nil || data.DBOK != nil ||
		data.DBConnActive != nil || data.DBConnWaiting != nil || data.DBMaxOpenConns != nil || data.RedisOK != nil ||
		data.RedisConnTotal != nil || data.RedisConnIdle != nil || data.RedisPoolSize != nil || data.GoroutineCount != nil ||
		data.QueueDepth != nil || data.AccountSwitchCount1m != nil || len(data.JobHeartbeats) != 0
}

func distinctAccounts[T any](rows map[int64]T, accountID func(T) int64) map[int64]T {
	unique := make(map[int64]T, len(rows))
	for _, key := range slices.Sorted(maps.Keys(rows)) {
		row := rows[key]
		id := accountID(row)
		if id <= 0 {
			id = key
		}
		if id <= 0 {
			continue
		}
		if _, seen := unique[id]; !seen || key == id {
			unique[id] = row
		}
	}
	return unique
}

type poolMetrics struct {
	used, capacity, waiting                              int64
	accounts, available, rateLimited, overloaded, failed int64
	hasConcurrency                                       bool
}

func (p *poolMetrics) addConcurrency(a SanitizedAccountConcurrency) {
	p.hasConcurrency = true
	p.used += a.CurrentInUse
	p.capacity += a.MaxCapacity
	p.waiting += a.WaitingInQueue
}

func (p *poolMetrics) addAvailability(a SanitizedAccountAvailability) {
	p.accounts++
	if a.Available {
		p.available++
	}
	if a.RateLimited {
		p.rateLimited++
	}
	if a.Overloaded {
		p.overloaded++
	}
	if a.HasError {
		p.failed++
	}
}

func (p *poolMetrics) concurrency(unverified bool) (used, capacity, waiting *int64) {
	used, capacity, waiting = &p.used, &p.capacity, &p.waiting
	if unverified {
		if p.used == 0 {
			used = nil
		}
		if p.capacity == 0 {
			capacity = nil
		}
		if p.waiting == 0 {
			waiting = nil
		}
	}
	return
}

func concurrencyTrustReason(in AnalyzeInput, conc *SanitizedConcurrencyPayload, accountData bool, used, currentCount int64) string {
	if conc == nil {
		return ""
	}
	prev := in.PreviousTrusted
	prevActive := prev != nil && prev.ConcUsed != nil && *prev.ConcUsed > 0
	if conc.RawEmpty && conc.Enabled == nil && (prevActive || in.PreviousLiveGroups) {
		return "实时接口返回空载荷，上一份可信样本仍有活跃分组或并发"
	}
	if !accountData && len(conc.Groups) == 0 && !conc.RawEmpty {
		return ""
	}
	data := in.Overview
	if data == nil || (data.OpsEnabled != nil && !*data.OpsEnabled) {
		return ""
	}
	if data.RedisOK != nil && !*data.RedisOK {
		return "基础设施指标显示 Redis 异常"
	}
	if data.SystemMetricsAt != nil && in.Now.Sub(*data.SystemMetricsAt) > 180*time.Second {
		return "基础设施指标已超过 180 秒"
	}
	if prevActive && accountData && used == 0 && data.QPSCurrent != nil && *data.QPSCurrent > 0.1 {
		previousCount := prev.AccountsTotal
		if previousCount != nil && *previousCount > 0 {
			delta := currentCount - *previousCount
			if delta < 0 {
				delta = -delta
			}
			// A shift of at most 20% preserves the reference pool size.
			if delta <= *previousCount/5 {
				return "账号池规模稳定且仍有请求，并发从非零降至零"
			}
		}
	}
	return ""
}

func analyzeGroups(metadata []SanitizedGroupInfo, conc *SanitizedConcurrencyPayload, avail *SanitizedAvailabilityPayload, quality FieldQualityMap, observed time.Time, reason string) ([]GroupCapacityItem, FieldQualityState) {
	state := quality[DomainConcurrency]
	if state != QualityUnverifiedZero && (avail == nil || avail.Groups == nil) {
		state = quality[DomainAccountAvailability]
		if state == QualityTrusted {
			state = QualityUnavailable
		}
	}
	if avail == nil || avail.Groups == nil {
		return nil, state
	}
	if state == QualityUnavailable && conc != nil && conc.Groups != nil {
		state = QualityTrusted
	}
	if state == QualityTrusted && (conc == nil || conc.Groups == nil) {
		state = QualityUnavailable
	}
	info := make(map[int64]SanitizedGroupInfo, len(metadata))
	for _, item := range metadata {
		info[item.ID] = item
	}
	groups := make([]GroupCapacityItem, 0, len(avail.Groups))
	for _, id := range slices.Sorted(maps.Keys(avail.Groups)) {
		a := avail.Groups[id]
		item := GroupCapacityItem{GroupID: id, GroupName: a.GroupName, Platform: a.Platform, ObservedAt: observed,
			TrustState: state, TrustReason: reason, AccountsTotal: a.TotalCount, AccountsOpsAvailable: a.AvailableCount,
			AccountsRateLimited: a.RateLimitCount, AccountsOverloaded: a.OverloadCount, AccountsError: a.ErrorCount,
			SinglePointRisk: a.TotalCount > 0 && a.AvailableCount <= 1}
		if conc != nil && state != QualityDisabled {
			if c, exists := conc.Groups[id]; exists {
				p := poolMetrics{used: c.CurrentInUse, capacity: c.MaxCapacity, waiting: c.WaitingInQueue}
				item.ConcUsed, item.ConcMax, item.ConcWaiting = p.concurrency(state == QualityUnverifiedZero)
				item.LoadPercent = loadPercent(item.ConcUsed, item.ConcMax)
				if item.GroupName == "" {
					item.GroupName = c.GroupName
				}
				if item.Platform == "" {
					item.Platform = c.Platform
				}
			} else if item.TrustState == QualityTrusted {
				item.TrustState = QualityUnavailable
			}
		}
		if item.GroupName == "" {
			item.GroupName = info[id].Name
		}
		if item.Platform == "" {
			item.Platform = info[id].Platform
		}
		groups = append(groups, item)
	}
	return groups, state
}

func loadPercent(used, capacity *int64) *float64 {
	if used == nil || capacity == nil || *capacity <= 0 {
		return nil
	}
	return ptr(float64(*used) / float64(*capacity) * 100)
}

func ptr[T any](value T) *T { return &value }

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

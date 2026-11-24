package siteops

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

const rateLimitWaveMinDuration = 5 * time.Minute

// ParseAlertConfig 解析单实例告警 JSON 配置，缺失、空串或非法 JSON 时回退 DefaultAlertConfig。
func ParseAlertConfig(raw string) AlertConfig {
	def := DefaultAlertConfig()
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return def
	}
	cfg := def
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return def
	}
	return normalizeAlertConfig(cfg, def)
}

func normalizeAlertConfig(cfg, def AlertConfig) AlertConfig {
	if cfg.SLAThresholdPct <= 0 || cfg.SLAThresholdPct > 100 {
		cfg.SLAThresholdPct = def.SLAThresholdPct
	}
	if cfg.UpstreamErrorRateThresholdPct <= 0 || cfg.UpstreamErrorRateThresholdPct > 100 {
		cfg.UpstreamErrorRateThresholdPct = def.UpstreamErrorRateThresholdPct
	}
	if cfg.MinSamplesForSLA <= 0 {
		cfg.MinSamplesForSLA = def.MinSamplesForSLA
	}
	if cfg.ConcurrencyThresholdPct <= 0 || cfg.ConcurrencyThresholdPct > 100 {
		cfg.ConcurrencyThresholdPct = def.ConcurrencyThresholdPct
	}
	if cfg.SwitchCount1mThreshold <= 0 {
		cfg.SwitchCount1mThreshold = def.SwitchCount1mThreshold
	}
	if cfg.SwitchRate1mThresholdPct <= 0 || cfg.SwitchRate1mThresholdPct > 100 {
		cfg.SwitchRate1mThresholdPct = def.SwitchRate1mThresholdPct
	}
	if cfg.ResourcePressurePct <= 0 || cfg.ResourcePressurePct > 100 {
		cfg.ResourcePressurePct = def.ResourcePressurePct
	}
	if cfg.RateLimitWaveRatioPct <= 0 || cfg.RateLimitWaveRatioPct > 100 {
		cfg.RateLimitWaveRatioPct = def.RateLimitWaveRatioPct
	}
	if cfg.CooldownMinutes < 0 {
		cfg.CooldownMinutes = def.CooldownMinutes
	}
	if cfg.DailyReportHourUTC8 < 0 || cfg.DailyReportHourUTC8 > 23 {
		cfg.DailyReportHourUTC8 = def.DailyReportHourUTC8
	}
	return cfg
}

func resolveEvalConfig(in EvalInput) AlertConfig {
	if in.Config == (AlertConfig{}) {
		return ParseAlertConfig(in.Instance.AlertConfigJSON)
	}
	return normalizeAlertConfig(in.Config, DefaultAlertConfig())
}

// EvalInput 单轮告警状态机评估输入。
type EvalInput struct {
	Instance           storage.SiteOpsInstance
	Config             AlertConfig
	Now                time.Time
	Snapshot           *storage.SiteOpsSnapshot
	Detail             SnapshotDetailPayload
	FieldQuality       FieldQualityMap
	Groups             []GroupCapacityItem
	Accounts           []SanitizedAccountAvailability
	AuthCacheHealth    *SanitizedAuthCacheHealth
	Auth401Consecutive int
	Auth401CredGen     int64
	AuthVerified       bool
}

// EventTransition 状态机单次评估产出的状态跃迁（触发告警或自动恢复）。
type EventTransition struct {
	Event             storage.SiteOpsEvent
	Kind              string
	NewlyTransitioned bool
}

type ruleOutcome int

const (
	outcomeSkip ruleOutcome = iota
	outcomeOK
	outcomeHit
)

type ruleKeyPair struct {
	ruleKey     string
	resourceKey string
}

type instanceEvalState struct {
	lastCredGen       int64
	consecutiveHits   map[ruleKeyPair]int
	rateLimitWaveAt   time.Time
	rateLimitWaveHits int
}

func newInstanceEvalState() *instanceEvalState {
	return &instanceEvalState{
		consecutiveHits: make(map[ruleKeyPair]int),
	}
}

// EventEvaluator 维护多实例规则防抖状态，并驱动持久化 SiteOpsEvent 的触发与同域自动恢复。
type EventEvaluator struct {
	mu    sync.Mutex
	state map[uint]*instanceEvalState
}

func NewEventEvaluator() *EventEvaluator {
	return &EventEvaluator{
		state: make(map[uint]*instanceEvalState),
	}
}

// ResetInstance 清理指定实例在内存中的防抖与持续计时状态（如换 Key、改地址或删除实例时）。
func (e *EventEvaluator) ResetInstance(instanceID uint) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != nil {
		delete(e.state, instanceID)
	}
}

func (e *EventEvaluator) instanceStateLocked(instanceID uint) *instanceEvalState {
	if e.state == nil {
		e.state = make(map[uint]*instanceEvalState)
	}
	st, ok := e.state[instanceID]
	if !ok {
		st = newInstanceEvalState()
		e.state[instanceID] = st
	}
	return st
}

// Evaluate 基于本轮采集快照与域可信度评估全部 11 条 P1-A 告警规则并持久化状态跃迁。
func (e *EventEvaluator) Evaluate(ctx context.Context, store EventStore, in EvalInput) ([]EventTransition, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: event store is nil", ErrInvalidInput)
	}
	cfg := resolveEvalConfig(in)
	if !cfg.Enabled {
		return nil, nil
	}
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	activeEvents, err := store.ListActiveEvents(ctx, in.Instance.ID)
	if err != nil {
		return nil, err
	}
	activeByKey := make(map[ruleKeyPair]storage.SiteOpsEvent, len(activeEvents))
	for _, evt := range activeEvents {
		if evt.State != EventStateFiring {
			continue
		}
		activeByKey[ruleKeyPair{ruleKey: evt.RuleKey, resourceKey: evt.ResourceKey}] = evt
	}

	fq := resolveFieldQuality(in.FieldQuality, in.Snapshot)
	detail := resolveSnapshotDetail(in.Detail, in.Snapshot)

	e.mu.Lock()
	defer e.mu.Unlock()

	st := e.instanceStateLocked(in.Instance.ID)
	if st.lastCredGen != 0 && st.lastCredGen != in.Instance.CredGen {
		delete(st.consecutiveHits, ruleKeyPair{ruleKey: RuleAuthInvalid, resourceKey: "site"})
	}
	st.lastCredGen = in.Instance.CredGen

	var transitions []EventTransition
	apply := func(spec ruleEvaluation) error {
		tr, err := e.applyRuleOutcome(ctx, store, st, activeByKey, in.Instance, now, spec)
		if err != nil {
			return err
		}
		if tr != nil {
			transitions = append(transitions, *tr)
		}
		return nil
	}

	// 1. site_offline (critical, 连续 2 次, [reachability])
	if err := apply(evalSiteOffline(in, fq)); err != nil {
		return nil, err
	}
	// 2. auth_invalid (critical, 连续 2 次同 cred_gen, [auth_probe])
	if err := apply(evalAuthInvalid(st, in, fq)); err != nil {
		return nil, err
	}
	// 3. infra_down (critical, 连续 2 次, [infra_metrics])
	if err := apply(evalInfraDown(in, fq)); err != nil {
		return nil, err
	}
	// 4. group_pool_exhausted (critical, 连续 2 次, group:<id>, [account_availability, infra_metrics])
	for _, spec := range evalGroupPoolExhausted(in, fq) {
		if err := apply(spec); err != nil {
			return nil, err
		}
	}
	// 5. sla_degraded (warning, 连续 2 次, [ops_overview])
	if err := apply(evalSLADegraded(st, in, cfg, fq)); err != nil {
		return nil, err
	}
	// 6. concurrency_saturated (warning, 连续 3 次, site + group:<id>, [concurrency, infra_metrics])
	for _, spec := range evalConcurrencySaturated(in, cfg, fq) {
		if err := apply(spec); err != nil {
			return nil, err
		}
	}
	// 7. high_switch_spike (warning, 连续 2 次, [infra_metrics, ops_overview])
	if err := apply(evalHighSwitchSpike(in, detail, cfg, fq)); err != nil {
		return nil, err
	}
	// 8. resource_pressure (warning, 连续 3 次, [infra_metrics])
	if err := apply(evalResourcePressure(in, cfg, fq)); err != nil {
		return nil, err
	}
	// 9. job_or_cache_unhealthy (warning, 连续 1 次, job:<name> / auth_cache)
	for _, spec := range evalJobAndCacheHealth(in, detail, fq) {
		if err := apply(spec); err != nil {
			return nil, err
		}
	}
	// 10. account_error_new (warning, Diff 新增, account:<id>, [account_availability])
	for _, spec := range evalAccountErrors(in, fq, activeByKey) {
		if err := apply(spec); err != nil {
			return nil, err
		}
	}
	// 11. account_rate_limit_wave (warning, 持续 >= 5 分钟, site, [account_availability])
	if err := apply(evalAccountRateLimitWave(st, in, cfg, fq, now)); err != nil {
		return nil, err
	}

	return transitions, nil
}

type ruleEvaluation struct {
	ruleKey          string
	resourceKey      string
	severity         string
	requiredDomains  []string
	thresholdHits    int
	observedConsec   int
	outcome          ruleOutcome
	title            string
	summary          string
	detail           map[string]any
	resetDebounceOff bool
}

func (e *EventEvaluator) applyRuleOutcome(
	ctx context.Context,
	store EventStore,
	st *instanceEvalState,
	activeByKey map[ruleKeyPair]storage.SiteOpsEvent,
	inst storage.SiteOpsInstance,
	now time.Time,
	spec ruleEvaluation,
) (*EventTransition, error) {
	key := ruleKeyPair{ruleKey: spec.ruleKey, resourceKey: spec.resourceKey}
	existing, hasActive := activeByKey[key]

	switch spec.outcome {
	case outcomeSkip:
		if spec.resetDebounceOff {
			delete(st.consecutiveHits, key)
		}
		return nil, nil

	case outcomeOK:
		delete(st.consecutiveHits, key)
		if !hasActive {
			return nil, nil
		}
		resolved := existing
		resolved.State = EventStateResolved
		resolvedAt := now
		resolved.ResolvedAt = &resolvedAt
		resolved.UpdatedAt = now
		if spec.summary != "" {
			resolved.Summary = SanitizeErrorText(spec.summary, 480)
		}
		if len(spec.detail) > 0 {
			resolved.DetailJSON = marshalDetailMap(spec.detail)
		}
		if err := store.UpsertEvent(ctx, &resolved); err != nil {
			return nil, err
		}
		delete(activeByKey, key)
		return &EventTransition{
			Event:             resolved,
			Kind:              DeliveryKindRecovery,
			NewlyTransitioned: true,
		}, nil

	case outcomeHit:
		hits := st.consecutiveHits[key] + 1
		if spec.observedConsec > hits {
			hits = spec.observedConsec
		}
		st.consecutiveHits[key] = hits
		need := spec.thresholdHits
		if need <= 0 {
			need = 1
		}
		if hits < need && !hasActive {
			return nil, nil
		}
		if hasActive {
			updated := existing
			if hits > updated.ConsecutiveHits {
				updated.ConsecutiveHits = hits
			} else {
				updated.ConsecutiveHits++
			}
			updated.Severity = spec.severity
			updated.RequiredDomainsJSON = marshalDomains(spec.requiredDomains)
			if spec.title != "" {
				updated.Title = SanitizeErrorText(spec.title, 240)
			}
			if spec.summary != "" {
				updated.Summary = SanitizeErrorText(spec.summary, 480)
			}
			if len(spec.detail) > 0 {
				updated.DetailJSON = marshalDetailMap(spec.detail)
			}
			updated.UpdatedAt = now
			if err := store.UpsertEvent(ctx, &updated); err != nil {
				return nil, err
			}
			activeByKey[key] = updated
			return nil, nil
		}
		evt := storage.SiteOpsEvent{
			InstanceID:          inst.ID,
			Severity:            spec.severity,
			RuleKey:             spec.ruleKey,
			ResourceKey:         spec.resourceKey,
			RequiredDomainsJSON: marshalDomains(spec.requiredDomains),
			State:               EventStateFiring,
			ConsecutiveHits:     hits,
			Title:               SanitizeErrorText(spec.title, 240),
			Summary:             SanitizeErrorText(spec.summary, 480),
			DetailJSON:          marshalDetailMap(spec.detail),
			FiredAt:             now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		if err := store.UpsertEvent(ctx, &evt); err != nil {
			return nil, err
		}
		activeByKey[key] = evt
		return &EventTransition{
			Event:             evt,
			Kind:              DeliveryKindAlert,
			NewlyTransitioned: true,
		}, nil
	}
	return nil, nil
}

func resolveFieldQuality(fq FieldQualityMap, snap *storage.SiteOpsSnapshot) FieldQualityMap {
	out := make(FieldQualityMap, len(fq)+8)
	if snap != nil && strings.TrimSpace(snap.FieldQualityJSON) != "" {
		var parsed FieldQualityMap
		if err := json.Unmarshal([]byte(snap.FieldQualityJSON), &parsed); err == nil {
			for k, v := range parsed {
				out[k] = v
			}
		}
	}
	for k, v := range fq {
		out[k] = v
	}
	return out
}

func resolveSnapshotDetail(detail SnapshotDetailPayload, snap *storage.SiteOpsSnapshot) SnapshotDetailPayload {
	if len(detail.JobHeartbeats) > 0 || detail.EstimatedSwitchPct1m != nil || detail.SystemMetricsAt != nil {
		return detail
	}
	if snap == nil || strings.TrimSpace(snap.DetailJSON) == "" {
		return detail
	}
	var parsed SnapshotDetailPayload
	if err := json.Unmarshal([]byte(snap.DetailJSON), &parsed); err != nil {
		return detail
	}
	return parsed
}

func domainsTrusted(fq FieldQualityMap, domains ...string) bool {
	for _, d := range domains {
		if fq[d] != QualityTrusted {
			return false
		}
	}
	return true
}

func marshalDomains(domains []string) string {
	if len(domains) == 0 {
		return "[]"
	}
	b, err := json.Marshal(domains)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func marshalDetailMap(detail map[string]any) string {
	if len(detail) == 0 {
		return "{}"
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func evalSiteOffline(in EvalInput, fq FieldQualityMap) ruleEvaluation {
	spec := ruleEvaluation{
		ruleKey:         RuleSiteOffline,
		resourceKey:     "site",
		severity:        SeverityCritical,
		requiredDomains: []string{DomainReachability},
		thresholdHits:   2,
		outcome:         outcomeSkip,
	}
	if !domainsTrusted(fq, DomainReachability) || in.Snapshot == nil {
		return spec
	}
	if !in.Snapshot.Reachable {
		spec.outcome = outcomeHit
		spec.title = "站点 HTTP 探活不可达"
		spec.summary = fmt.Sprintf("L0 /health 连续不可达（延迟 %dms）", in.Snapshot.ProbeLatencyMS)
		spec.detail = map[string]any{
			"reachable":        false,
			"probe_latency_ms": in.Snapshot.ProbeLatencyMS,
		}
		return spec
	}
	spec.outcome = outcomeOK
	spec.summary = fmt.Sprintf("L0 /health 已恢复可达（延迟 %dms）", in.Snapshot.ProbeLatencyMS)
	spec.detail = map[string]any{
		"reachable":        true,
		"probe_latency_ms": in.Snapshot.ProbeLatencyMS,
	}
	return spec
}

func evalAuthInvalid(st *instanceEvalState, in EvalInput, fq FieldQualityMap) ruleEvaluation {
	spec := ruleEvaluation{
		ruleKey:         RuleAuthInvalid,
		resourceKey:     "site",
		severity:        SeverityCritical,
		requiredDomains: []string{DomainAuthProbe},
		thresholdHits:   2,
		outcome:         outcomeSkip,
	}
	if state, ok := fq[DomainAuthProbe]; ok && state != "" && state != QualityTrusted {
		return spec
	}
	// 旧 cred_gen 的迟到 401 绝不触发，并立即清理防抖状态。
	if in.Auth401CredGen != 0 && in.Auth401CredGen != in.Instance.CredGen {
		delete(st.consecutiveHits, ruleKeyPair{ruleKey: RuleAuthInvalid, resourceKey: "site"})
		if !in.AuthVerified {
			return spec
		}
	}
	if in.AuthVerified {
		spec.outcome = outcomeOK
		spec.summary = fmt.Sprintf("当前凭据代际 cred_gen=%d 鉴权探测已恢复通过", in.Instance.CredGen)
		spec.detail = map[string]any{
			"cred_gen":      in.Instance.CredGen,
			"auth_verified": true,
		}
		return spec
	}
	if in.Auth401CredGen == in.Instance.CredGen && in.Auth401Consecutive > 0 {
		spec.observedConsec = in.Auth401Consecutive
		if in.Auth401Consecutive >= 2 {
			spec.outcome = outcomeHit
			spec.title = "站点 Admin API 鉴权失效（401）"
			spec.summary = fmt.Sprintf("同一凭据代际 cred_gen=%d 连续 %d 次返回 401", in.Instance.CredGen, in.Auth401Consecutive)
			spec.detail = map[string]any{
				"cred_gen":              in.Instance.CredGen,
				"auth_401_consecutive":  in.Auth401Consecutive,
			}
			return spec
		}
		// 仅 1 次 401，记录防抖但不触发告警也不恢复已有事件。
		st.consecutiveHits[ruleKeyPair{ruleKey: RuleAuthInvalid, resourceKey: "site"}] = in.Auth401Consecutive
	}
	return spec
}

func evalInfraDown(in EvalInput, fq FieldQualityMap) ruleEvaluation {
	spec := ruleEvaluation{
		ruleKey:         RuleInfraDown,
		resourceKey:     "site",
		severity:        SeverityCritical,
		requiredDomains: []string{DomainInfraMetrics},
		thresholdHits:   2,
		outcome:         outcomeSkip,
	}
	if !domainsTrusted(fq, DomainInfraMetrics) || in.Snapshot == nil {
		return spec
	}
	dbOK := in.Snapshot.DBOK
	redisOK := in.Snapshot.RedisOK
	if dbOK == nil && redisOK == nil {
		return spec
	}
	var bad []string
	if dbOK != nil && !*dbOK {
		bad = append(bad, "DB")
	}
	if redisOK != nil && !*redisOK {
		bad = append(bad, "Redis")
	}
	if len(bad) > 0 {
		spec.outcome = outcomeHit
		spec.title = fmt.Sprintf("核心基础设施异常（%s）", strings.Join(bad, " / "))
		spec.summary = fmt.Sprintf("%s 健康检查失败", strings.Join(bad, "、"))
		spec.detail = map[string]any{
			"db_ok":    dbOK,
			"redis_ok": redisOK,
		}
		return spec
	}
	spec.outcome = outcomeOK
	spec.summary = "DB / Redis 基础设施健康检查已恢复正常"
	spec.detail = map[string]any{
		"db_ok":    dbOK,
		"redis_ok": redisOK,
	}
	return spec
}

func evalGroupPoolExhausted(in EvalInput, fq FieldQualityMap) []ruleEvaluation {
	if !domainsTrusted(fq, DomainAccountAvailability, DomainInfraMetrics) ||
		in.Snapshot == nil || in.Snapshot.RedisOK == nil || !*in.Snapshot.RedisOK {
		return nil
	}
	out := make([]ruleEvaluation, 0, len(in.Groups))
	for _, g := range in.Groups {
		if g.GroupID <= 0 {
			continue
		}
		spec := ruleEvaluation{
			ruleKey:         RuleGroupPoolExhausted,
			resourceKey:     fmt.Sprintf("group:%d", g.GroupID),
			severity:        SeverityCritical,
			requiredDomains: []string{DomainAccountAvailability, DomainInfraMetrics},
			thresholdHits:   2,
			outcome:         outcomeSkip,
		}
		// 严禁 unverified_zero / stale 等非 trusted 分组样本触发或恢复。
		if g.TrustState != QualityTrusted {
			out = append(out, spec)
			continue
		}
		groupLabel := SanitizeErrorText(g.GroupName, 80)
		if groupLabel == "" {
			groupLabel = fmt.Sprintf("group:%d", g.GroupID)
		}
		if g.AccountsTotal > 0 && g.AccountsOpsAvailable == 0 {
			spec.outcome = outcomeHit
			spec.title = fmt.Sprintf("分组号池耗尽（%s）", groupLabel)
			spec.summary = fmt.Sprintf("分组 %s（ID %d）Ops 可用账号为 0/%d（限流 %d，过载 %d，异常 %d）",
				groupLabel, g.GroupID, g.AccountsTotal, g.AccountsRateLimited, g.AccountsOverloaded, g.AccountsError)
			spec.detail = map[string]any{
				"group_id":               g.GroupID,
				"group_name":             groupLabel,
				"accounts_total":         g.AccountsTotal,
				"accounts_ops_available": g.AccountsOpsAvailable,
				"accounts_rate_limited":  g.AccountsRateLimited,
				"accounts_overloaded":    g.AccountsOverloaded,
				"accounts_error":         g.AccountsError,
			}
		} else {
			spec.outcome = outcomeOK
			spec.summary = fmt.Sprintf("分组 %s（ID %d）号池已恢复可用（%d/%d）",
				groupLabel, g.GroupID, g.AccountsOpsAvailable, g.AccountsTotal)
			spec.detail = map[string]any{
				"group_id":               g.GroupID,
				"group_name":             groupLabel,
				"accounts_total":         g.AccountsTotal,
				"accounts_ops_available": g.AccountsOpsAvailable,
			}
		}
		out = append(out, spec)
	}
	return out
}

func evalSLADegraded(st *instanceEvalState, in EvalInput, cfg AlertConfig, fq FieldQualityMap) ruleEvaluation {
	spec := ruleEvaluation{
		ruleKey:         RuleSLADegraded,
		resourceKey:     "site",
		severity:        SeverityWarning,
		requiredDomains: []string{DomainOpsOverview},
		thresholdHits:   2,
		outcome:         outcomeSkip,
	}
	if !domainsTrusted(fq, DomainOpsOverview) || in.Snapshot == nil || in.Snapshot.ReqTotal1h == nil {
		return spec
	}
	reqTotal := *in.Snapshot.ReqTotal1h
	if reqTotal < cfg.MinSamplesForSLA {
		// 样本量不足时不触发，并清理未触发时的防抖计数。
		spec.resetDebounceOff = true
		return spec
	}
	sla := in.Snapshot.SLAPercent1h
	upErr := in.Snapshot.UpstreamErrorRatePct1h
	if sla == nil && upErr == nil {
		return spec
	}
	slaBad := sla != nil && *sla < cfg.SLAThresholdPct
	upErrBad := upErr != nil && *upErr > cfg.UpstreamErrorRateThresholdPct
	if slaBad || upErrBad {
		var parts []string
		if sla != nil {
			parts = append(parts, fmt.Sprintf("1h SLA=%.2f%%（阈值 %.2f%%）", *sla, cfg.SLAThresholdPct))
		}
		if upErr != nil {
			parts = append(parts, fmt.Sprintf("上游错误率=%.2f%%（阈值 %.2f%%）", *upErr, cfg.UpstreamErrorRateThresholdPct))
		}
		spec.outcome = outcomeHit
		spec.title = "站点 1h SLA 降级或上游错误率偏高"
		spec.summary = fmt.Sprintf("%s，1h 有效样本 %d", strings.Join(parts, "，"), reqTotal)
		spec.detail = map[string]any{
			"req_total_1h":               reqTotal,
			"sla_percent_1h":             sla,
			"upstream_error_rate_pct_1h": upErr,
		}
		return spec
	}
	spec.outcome = outcomeOK
	spec.summary = fmt.Sprintf("1h SLA 与上游错误率已恢复正常（1h 样本 %d）", reqTotal)
	spec.detail = map[string]any{
		"req_total_1h":               reqTotal,
		"sla_percent_1h":             sla,
		"upstream_error_rate_pct_1h": upErr,
	}
	return spec
}

func evalConcurrencySaturated(in EvalInput, cfg AlertConfig, fq FieldQualityMap) []ruleEvaluation {
	if !domainsTrusted(fq, DomainConcurrency, DomainInfraMetrics) || in.Snapshot == nil {
		return nil
	}
	out := make([]ruleEvaluation, 0, 1+len(in.Groups))

	// 全站并发评估
	siteSpec := ruleEvaluation{
		ruleKey:         RuleConcurrencySaturated,
		resourceKey:     "site",
		severity:        SeverityWarning,
		requiredDomains: []string{DomainConcurrency, DomainInfraMetrics},
		thresholdHits:   3,
		outcome:         outcomeSkip,
	}
	siteLoad := loadPercent(in.Snapshot.ConcUsed, in.Snapshot.ConcMax)
	queueDepth := in.Snapshot.QueueDepth
	if siteLoad != nil || queueDepth != nil {
		loadHigh := siteLoad != nil && *siteLoad >= cfg.ConcurrencyThresholdPct
		queueBusy := queueDepth != nil && *queueDepth > 0
		if loadHigh || queueBusy {
			siteSpec.outcome = outcomeHit
			siteSpec.title = "全站并发负载饱和或出现排队"
			var parts []string
			if siteLoad != nil {
				parts = append(parts, fmt.Sprintf("全站并发负载 %.1f%%（%d/%d）", *siteLoad, *in.Snapshot.ConcUsed, *in.Snapshot.ConcMax))
			}
			if queueBusy {
				parts = append(parts, fmt.Sprintf("等待队列深度 %d", *queueDepth))
			}
			siteSpec.summary = strings.Join(parts, "，")
			siteSpec.detail = map[string]any{
				"conc_load_percent": siteLoad,
				"conc_used":         in.Snapshot.ConcUsed,
				"conc_max":          in.Snapshot.ConcMax,
				"queue_depth":       queueDepth,
			}
		} else {
			siteSpec.outcome = outcomeOK
			siteSpec.summary = "全站并发负载与排队已回落至阈值以下"
			siteSpec.detail = map[string]any{
				"conc_load_percent": siteLoad,
				"queue_depth":       queueDepth,
			}
		}
	}
	out = append(out, siteSpec)

	// 单组并发评估
	for _, g := range in.Groups {
		if g.GroupID <= 0 {
			continue
		}
		grpSpec := ruleEvaluation{
			ruleKey:         RuleConcurrencySaturated,
			resourceKey:     fmt.Sprintf("group:%d", g.GroupID),
			severity:        SeverityWarning,
			requiredDomains: []string{DomainConcurrency, DomainInfraMetrics},
			thresholdHits:   3,
			outcome:         outcomeSkip,
		}
		if g.TrustState != QualityTrusted {
			out = append(out, grpSpec)
			continue
		}
		grpLoad := g.LoadPercent
		if grpLoad == nil {
			grpLoad = loadPercent(g.ConcUsed, g.ConcMax)
		}
		if grpLoad == nil {
			out = append(out, grpSpec)
			continue
		}
		groupLabel := SanitizeErrorText(g.GroupName, 80)
		if groupLabel == "" {
			groupLabel = fmt.Sprintf("group:%d", g.GroupID)
		}
		if *grpLoad >= cfg.ConcurrencyThresholdPct {
			grpSpec.outcome = outcomeHit
			grpSpec.title = fmt.Sprintf("分组并发负载饱和（%s）", groupLabel)
			grpSpec.summary = fmt.Sprintf("分组 %s（ID %d）并发负载 %.1f%%（阈值 %.1f%%）",
				groupLabel, g.GroupID, *grpLoad, cfg.ConcurrencyThresholdPct)
			grpSpec.detail = map[string]any{
				"group_id":     g.GroupID,
				"group_name":   groupLabel,
				"load_percent": *grpLoad,
				"conc_used":    g.ConcUsed,
				"conc_max":     g.ConcMax,
			}
		} else {
			grpSpec.outcome = outcomeOK
			grpSpec.summary = fmt.Sprintf("分组 %s（ID %d）并发负载已回落至 %.1f%%", groupLabel, g.GroupID, *grpLoad)
			grpSpec.detail = map[string]any{
				"group_id":     g.GroupID,
				"group_name":   groupLabel,
				"load_percent": *grpLoad,
			}
		}
		out = append(out, grpSpec)
	}
	return out
}

func evalHighSwitchSpike(in EvalInput, detail SnapshotDetailPayload, cfg AlertConfig, fq FieldQualityMap) ruleEvaluation {
	spec := ruleEvaluation{
		ruleKey:         RuleHighSwitchSpike,
		resourceKey:     "site",
		severity:        SeverityWarning,
		requiredDomains: []string{DomainInfraMetrics, DomainOpsOverview},
		thresholdHits:   2,
		outcome:         outcomeSkip,
	}
	if !domainsTrusted(fq, DomainInfraMetrics, DomainOpsOverview) || in.Snapshot == nil || in.Snapshot.AccountSwitchCount1m == nil {
		return spec
	}
	switches := *in.Snapshot.AccountSwitchCount1m
	var estReq1m float64
	if in.Snapshot.QPSCurrent != nil && *in.Snapshot.QPSCurrent > 0 {
		estReq1m = *in.Snapshot.QPSCurrent * 60
	}
	estPct := detail.EstimatedSwitchPct1m
	if estPct == nil && estReq1m >= 20 {
		v := float64(switches) / estReq1m * 100
		estPct = &v
	}

	countHit := switches >= cfg.SwitchCount1mThreshold
	rateHit := estReq1m >= 20 && estPct != nil && *estPct > cfg.SwitchRate1mThresholdPct
	if countHit || rateHit {
		spec.outcome = outcomeHit
		spec.title = "近 1 分钟账号切换次数突增"
		if estPct != nil && estReq1m >= 20 {
			spec.summary = fmt.Sprintf("近 1m 切号 %d 次（估算切号率 %.1f%%，阈值 %d 次 / %.1f%%）",
				switches, *estPct, cfg.SwitchCount1mThreshold, cfg.SwitchRate1mThresholdPct)
		} else {
			spec.summary = fmt.Sprintf("近 1m 切号 %d 次（阈值 %d 次）", switches, cfg.SwitchCount1mThreshold)
		}
		spec.detail = map[string]any{
			"account_switch_count_1m": switches,
			"estimated_switch_pct_1m": estPct,
			"estimated_req_1m":        math.Round(estReq1m*100) / 100,
		}
		return spec
	}
	spec.outcome = outcomeOK
	spec.summary = fmt.Sprintf("近 1m 切号次数已恢复正常（%d 次）", switches)
	spec.detail = map[string]any{
		"account_switch_count_1m": switches,
		"estimated_switch_pct_1m": estPct,
	}
	return spec
}

func evalResourcePressure(in EvalInput, cfg AlertConfig, fq FieldQualityMap) ruleEvaluation {
	spec := ruleEvaluation{
		ruleKey:         RuleResourcePressure,
		resourceKey:     "site",
		severity:        SeverityWarning,
		requiredDomains: []string{DomainInfraMetrics},
		thresholdHits:   3,
		outcome:         outcomeSkip,
	}
	if !domainsTrusted(fq, DomainInfraMetrics) || in.Snapshot == nil {
		return spec
	}
	s := in.Snapshot
	var dbPoolPct *float64
	if s.DBConnActive != nil && s.DBMaxOpenConns != nil && *s.DBMaxOpenConns > 0 {
		v := float64(*s.DBConnActive) / float64(*s.DBMaxOpenConns) * 100
		dbPoolPct = &v
	}
	if s.CPUPercent == nil && s.MemPercent == nil && dbPoolPct == nil && s.DBConnWaiting == nil {
		return spec
	}
	threshold := cfg.ResourcePressurePct
	var reasons []string
	if s.CPUPercent != nil && *s.CPUPercent >= threshold {
		reasons = append(reasons, fmt.Sprintf("CPU %.1f%%", *s.CPUPercent))
	}
	if s.MemPercent != nil && *s.MemPercent >= threshold {
		reasons = append(reasons, fmt.Sprintf("内存 %.1f%%", *s.MemPercent))
	}
	if dbPoolPct != nil && *dbPoolPct >= threshold {
		reasons = append(reasons, fmt.Sprintf("DB 连接池 %.1f%%", *dbPoolPct))
	}
	if s.DBConnWaiting != nil && *s.DBConnWaiting > 0 {
		reasons = append(reasons, fmt.Sprintf("DB 等待连接 %d", *s.DBConnWaiting))
	}
	if len(reasons) > 0 {
		spec.outcome = outcomeHit
		spec.title = "站点主机或数据库连接池资源承压"
		spec.summary = fmt.Sprintf("资源指标超阈值：%s", strings.Join(reasons, "，"))
		spec.detail = map[string]any{
			"cpu_percent":     s.CPUPercent,
			"mem_percent":     s.MemPercent,
			"db_pool_percent": dbPoolPct,
			"db_conn_waiting": s.DBConnWaiting,
		}
		return spec
	}
	spec.outcome = outcomeOK
	spec.summary = "站点 CPU、内存与 DB 连接池压力已恢复正常"
	spec.detail = map[string]any{
		"cpu_percent":     s.CPUPercent,
		"mem_percent":     s.MemPercent,
		"db_pool_percent": dbPoolPct,
		"db_conn_waiting": s.DBConnWaiting,
	}
	return spec
}

func evalJobAndCacheHealth(in EvalInput, detail SnapshotDetailPayload, fq FieldQualityMap) []ruleEvaluation {
	var out []ruleEvaluation

	if domainsTrusted(fq, DomainOpsOverview) {
		for _, hb := range detail.JobHeartbeats {
			name := strings.TrimSpace(hb.JobName)
			if name == "" {
				continue
			}
			st := strings.ToLower(strings.TrimSpace(hb.Status))
			unhealthy := hb.HasError || st == "failed" || st == "stale" || st == "unhealthy" || st == "error"
			spec := ruleEvaluation{
				ruleKey:         RuleJobOrCacheUnhealthy,
				resourceKey:     "job:" + name,
				severity:        SeverityWarning,
				requiredDomains: []string{DomainOpsOverview},
				thresholdHits:   1,
			}
			if unhealthy {
				spec.outcome = outcomeHit
				spec.title = fmt.Sprintf("后台任务心跳异常（%s）", SanitizeErrorText(name, 64))
				msg := fmt.Sprintf("任务 %s 状态=%s", SanitizeErrorText(name, 64), st)
				if hb.ErrorSummary != "" {
					msg += "，摘要：" + SanitizeErrorText(hb.ErrorSummary, 160)
				}
				spec.summary = msg
				spec.detail = map[string]any{
					"job_name":      SanitizeErrorText(name, 64),
					"status":        st,
					"has_error":     hb.HasError,
					"error_summary": SanitizeErrorText(hb.ErrorSummary, 160),
				}
			} else {
				spec.outcome = outcomeOK
				spec.summary = fmt.Sprintf("后台任务 %s 心跳已恢复正常（status=%s）", SanitizeErrorText(name, 64), st)
				spec.detail = map[string]any{
					"job_name": SanitizeErrorText(name, 64),
					"status":   st,
				}
			}
			out = append(out, spec)
		}
	}

	if domainsTrusted(fq, DomainAuthCacheHealth) && in.AuthCacheHealth != nil {
		ach := in.AuthCacheHealth
		spec := ruleEvaluation{
			ruleKey:         RuleJobOrCacheUnhealthy,
			resourceKey:     "auth_cache",
			severity:        SeverityWarning,
			requiredDomains: []string{DomainAuthCacheHealth},
			thresholdHits:   1,
		}
		if !ach.Healthy {
			spec.outcome = outcomeHit
			spec.title = "鉴权缓存失效通道异常（auth_cache）"
			var parts []string
			if !ach.OutboxRunning {
				parts = append(parts, "outbox 未运行")
			}
			if !ach.SubscriberOK {
				parts = append(parts, "subscriber 异常")
			}
			if ach.OutboxPending > 0 {
				parts = append(parts, fmt.Sprintf("pending=%d", ach.OutboxPending))
			}
			if ach.OutboxLastError != "" {
				parts = append(parts, SanitizeErrorText(ach.OutboxLastError, 120))
			}
			if ach.SubscriberError != "" {
				parts = append(parts, SanitizeErrorText(ach.SubscriberError, 120))
			}
			if len(parts) == 0 {
				parts = append(parts, "健康检查返回 unhealthy")
			}
			spec.summary = strings.Join(parts, "；")
			spec.detail = map[string]any{
				"healthy":         ach.Healthy,
				"outbox_running":  ach.OutboxRunning,
				"outbox_pending":  ach.OutboxPending,
				"subscriber_ok":   ach.SubscriberOK,
				"outbox_failures": ach.OutboxFailures,
			}
		} else {
			spec.outcome = outcomeOK
			spec.summary = "鉴权缓存失效通道（auth_cache）已恢复健康"
			spec.detail = map[string]any{
				"healthy":        true,
				"outbox_running": ach.OutboxRunning,
				"subscriber_ok":  ach.SubscriberOK,
			}
		}
		out = append(out, spec)
	}

	return out
}

func isPureRateLimitError(acc SanitizedAccountAvailability) bool {
	if !acc.HasError {
		return true
	}
	if !acc.RateLimited {
		return false
	}
	summary := strings.ToLower(strings.TrimSpace(acc.ErrorSummary))
	if summary == "" {
		return true
	}
	// 若明确包含鉴权/封禁/凭证等硬错误词，不视为纯限流。
	for _, hard := range []string{"401", "403", "unauthorized", "forbidden", "invalid_api_key", "invalid api key", "banned", "suspended", "disabled", "revoked", "expired"} {
		if strings.Contains(summary, hard) {
			return false
		}
	}
	for _, rl := range []string{"429", "rate_limit", "rate limit", "too many requests", "ratelimit", "throttl", "限流"} {
		if strings.Contains(summary, rl) {
			return true
		}
	}
	return false
}

func isAccountRealError(acc SanitizedAccountAvailability) bool {
	return acc.HasError && !isPureRateLimitError(acc)
}

func evalAccountErrors(in EvalInput, fq FieldQualityMap, activeByKey map[ruleKeyPair]storage.SiteOpsEvent) []ruleEvaluation {
	if !domainsTrusted(fq, DomainAccountAvailability) {
		return nil
	}

	// 按 AccountID 去重并有序处理，保证事件顺序稳定。
	byID := make(map[int64]SanitizedAccountAvailability, len(in.Accounts))
	ids := make([]int64, 0, len(in.Accounts))
	for _, acc := range in.Accounts {
		if acc.AccountID <= 0 {
			continue
		}
		if _, exists := byID[acc.AccountID]; !exists {
			ids = append(ids, acc.AccountID)
		}
		byID[acc.AccountID] = acc
	}
	slices.Sort(ids)

	var out []ruleEvaluation
	seenResource := make(map[string]bool, len(ids))
	for _, id := range ids {
		acc := byID[id]
		resKey := fmt.Sprintf("account:%d", id)
		seenResource[resKey] = true
		spec := ruleEvaluation{
			ruleKey:         RuleAccountErrorNew,
			resourceKey:     resKey,
			severity:        SeverityWarning,
			requiredDomains: []string{DomainAccountAvailability},
			thresholdHits:   1,
		}
		if isAccountRealError(acc) {
			spec.outcome = outcomeHit
			spec.title = fmt.Sprintf("账号出现错误状态（account:%d）", id)
			summary := fmt.Sprintf("账号 #%d", id)
			if acc.Platform != "" {
				summary += fmt.Sprintf("（%s）", SanitizeErrorText(acc.Platform, 32))
			}
			summary += " 进入错误态"
			if acc.ErrorSummary != "" {
				summary += "：" + SanitizeErrorText(acc.ErrorSummary, 160)
			}
			spec.summary = summary
			spec.detail = map[string]any{
				"account_id":    id,
				"platform":      SanitizeErrorText(acc.Platform, 32),
				"has_error":     acc.HasError,
				"rate_limited":  acc.RateLimited,
				"error_summary": SanitizeErrorText(acc.ErrorSummary, 160),
			}
		} else {
			spec.outcome = outcomeOK
			spec.summary = fmt.Sprintf("账号 #%d 错误态已清除", id)
			spec.detail = map[string]any{
				"account_id": id,
				"has_error":  acc.HasError,
			}
		}
		out = append(out, spec)
	}

	// 若某个 firing 中的 account:<id> 本轮快照明确证实全站 AccountsError == 0，则自动恢复。
	if in.Snapshot != nil && in.Snapshot.AccountsError != nil && *in.Snapshot.AccountsError == 0 {
		var missingKeys []string
		for k := range activeByKey {
			if k.ruleKey == RuleAccountErrorNew && !seenResource[k.resourceKey] {
				missingKeys = append(missingKeys, k.resourceKey)
			}
		}
		slices.Sort(missingKeys)
		for _, resKey := range missingKeys {
			out = append(out, ruleEvaluation{
				ruleKey:         RuleAccountErrorNew,
				resourceKey:     resKey,
				severity:        SeverityWarning,
				requiredDomains: []string{DomainAccountAvailability},
				thresholdHits:   1,
				outcome:         outcomeOK,
				summary:         fmt.Sprintf("账号 %s 已恢复正常（当前全站异常账号数为 0）", resKey),
			})
		}
	}

	return out
}

func evalAccountRateLimitWave(st *instanceEvalState, in EvalInput, cfg AlertConfig, fq FieldQualityMap, now time.Time) ruleEvaluation {
	spec := ruleEvaluation{
		ruleKey:         RuleAccountRateLimitWave,
		resourceKey:     "site",
		severity:        SeverityWarning,
		requiredDomains: []string{DomainAccountAvailability},
		thresholdHits:   1,
		outcome:         outcomeSkip,
	}
	if !domainsTrusted(fq, DomainAccountAvailability) {
		return spec
	}

	var total, rateLimited int64
	if len(in.Accounts) > 0 {
		seen := make(map[int64]struct{}, len(in.Accounts))
		for _, acc := range in.Accounts {
			if acc.AccountID <= 0 {
				continue
			}
			if _, ok := seen[acc.AccountID]; ok {
				continue
			}
			seen[acc.AccountID] = struct{}{}
			total++
			if acc.RateLimited {
				rateLimited++
			}
		}
	} else if in.Snapshot != nil && in.Snapshot.AccountsTotal != nil && in.Snapshot.AccountsRateLimited != nil {
		total = *in.Snapshot.AccountsTotal
		rateLimited = *in.Snapshot.AccountsRateLimited
	} else {
		return spec
	}

	if total <= 0 {
		st.rateLimitWaveAt = time.Time{}
		st.rateLimitWaveHits = 0
		spec.outcome = outcomeOK
		spec.summary = "全站限流比例已回落"
		return spec
	}

	ratio := float64(rateLimited) / float64(total) * 100
	if ratio >= cfg.RateLimitWaveRatioPct {
		if st.rateLimitWaveAt.IsZero() {
			st.rateLimitWaveAt = now
		}
		st.rateLimitWaveHits++
		elapsed := now.Sub(st.rateLimitWaveAt)
		if elapsed < rateLimitWaveMinDuration {
			return spec
		}
		spec.outcome = outcomeHit
		spec.observedConsec = st.rateLimitWaveHits
		spec.title = "全站账号大面积触发 429 限流波"
		spec.summary = fmt.Sprintf("限流账号 %d/%d（%.1f%% ≥ 阈值 %.1f%%），已持续 %.0f 分钟",
			rateLimited, total, ratio, cfg.RateLimitWaveRatioPct, elapsed.Minutes())
		spec.detail = map[string]any{
			"accounts_total":        total,
			"accounts_rate_limited": rateLimited,
			"rate_limit_ratio_pct":  math.Round(ratio*100) / 100,
			"sustained_seconds":     int64(elapsed.Seconds()),
		}
		return spec
	}

	st.rateLimitWaveAt = time.Time{}
	st.rateLimitWaveHits = 0
	spec.outcome = outcomeOK
	spec.summary = fmt.Sprintf("全站账号限流比例已回落至 %.1f%%（%d/%d）", ratio, rateLimited, total)
	spec.detail = map[string]any{
		"accounts_total":        total,
		"accounts_rate_limited": rateLimited,
		"rate_limit_ratio_pct":  math.Round(ratio*100) / 100,
	}
	return spec
}

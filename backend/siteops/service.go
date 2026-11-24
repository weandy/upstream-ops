package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

type ServiceDeps struct {
	Store          Store
	Cipher         *crypto.Cipher
	Validator      OutboundValidator
	Client         UpstreamClient
	Prober         Prober
	Log            *slog.Logger
	InitialConfig  config.SiteOpsConfig
	AuthActive     bool
	Now            func() time.Time
	Jitter         func(time.Duration) time.Duration
	MaskAPIKeyHint func(string) string
	SanitizeError  func(string, int) string
}

type Service struct {
	deps   ServiceDeps
	poller *Poller
	mu     sync.Mutex
	cfg    config.SiteOpsConfig
	auth   bool
	active bool
}

func NewService(deps ServiceDeps) *Service {
	cfg := copySiteOpsConfig(deps.InitialConfig)
	poller := NewPoller(PollerDeps{
		Store: deps.Store, Cipher: deps.Cipher, Validator: deps.Validator, Client: deps.Client, Prober: deps.Prober,
		Log: deps.Log, InitialConfig: cfg, Now: deps.Now, Jitter: deps.Jitter, SanitizeError: deps.SanitizeError,
	})
	if deps.Validator != nil {
		deps.Validator.UpdatePolicy(cfg.AllowedOrigins, cfg.AllowedCIDRs)
	}
	poller.SetPollingAllowed(IsPollingAllowed(deps.AuthActive, cfg))
	return &Service{deps: deps, poller: poller, cfg: cfg, auth: deps.AuthActive}
}

func copySiteOpsConfig(cfg config.SiteOpsConfig) config.SiteOpsConfig {
	cfg.AllowedOrigins = slices.Clone(cfg.AllowedOrigins)
	cfg.AllowedCIDRs = slices.Clone(cfg.AllowedCIDRs)
	return cfg.WithDefaults()
}

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.BatchTimeoutSeconds)*time.Second)
	defer cancel()
	instances, err := s.deps.Store.ListInstances(ctx)
	if err != nil {
		return err
	}
	s.poller.SyncInstances(instances)
	s.poller.Start()
	s.active = true
	return nil
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.poller.Stop()
	s.active = false
}

func (s *Service) ApplyRuntimeConfig(authActive bool, siteCfg config.SiteOpsConfig, proxyCfg config.ProxyConfig, upstreamCfg config.UpstreamConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	siteCfg = copySiteOpsConfig(siteCfg)
	s.poller.applyRuntimeConfig(IsPollingAllowed(authActive, siteCfg), siteCfg, proxyCfg, upstreamCfg)
	s.cfg, s.auth = siteCfg, authActive
}

// AccessPolicy supplies the current auth policy to the HTTP access guard.
func (s *Service) AccessPolicy() (authActive, allowUnauthenticatedLocalOnly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auth, s.cfg.AllowUnauthenticatedLocalOnly
}

// CurrentConfig 返回当前生效的 SiteOps 配置副本（供 HTTP 鉴权中间件读取）。
func (s *Service) CurrentConfig() config.SiteOpsConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copySiteOpsConfig(s.cfg)
}

func (s *Service) ListInstances(ctx context.Context) ([]InstanceView, error) {
	instances, err := s.deps.Store.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	auth, _ := s.AccessPolicy()
	pollingAllowed := IsPollingAllowed(auth, s.CurrentConfig())
	views := make([]InstanceView, 0, len(instances))
	for _, inst := range instances {
		views = append(views, s.instanceView(inst, pollingAllowed))
	}
	return views, nil
}

func (s *Service) CreateInstance(ctx context.Context, input CreateInstanceInput) (*InstanceView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.auth {
		return nil, ErrAuthRequired
	}
	inst := storage.SiteOpsInstance{Name: input.Name, Slug: input.Slug, BaseURL: input.BaseURL, KeyKind: input.KeyKind,
		Enabled: true, ProxyEnabled: input.ProxyEnabled, SortOrder: input.SortOrder, PollIntervalSeconds: input.PollIntervalSeconds,
		AuditBudgetPerDay: input.AuditBudgetPerDay, CredGen: 1, Generation: 1}
	assignInput(&inst.Enabled, input.Enabled)
	if err := s.validateInstance(&inst); err != nil {
		return nil, err
	}
	if err := s.setKey(&inst, strings.TrimSpace(input.APIKey)); err != nil {
		return nil, err
	}
	if err := s.setUniqueSlug(ctx, &inst, input.Slug == ""); err != nil {
		return nil, err
	}
	if err := s.deps.Store.CreateInstance(ctx, &inst); err != nil {
		return nil, err
	}
	s.poller.ReconcileInstance(inst)
	view := s.instanceView(inst, s.auth)
	return &view, nil
}

func (s *Service) UpdateInstance(ctx context.Context, id uint, input UpdateInstanceInput) (*InstanceView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.auth {
		return nil, ErrAuthRequired
	}
	previous, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	inst := *previous
	assignInput(&inst.Name, input.Name)
	assignInput(&inst.Slug, input.Slug)
	assignInput(&inst.BaseURL, input.BaseURL)
	assignInput(&inst.KeyKind, input.KeyKind)
	assignInput(&inst.ProxyEnabled, input.ProxyEnabled)
	assignInput(&inst.Enabled, input.Enabled)
	assignInput(&inst.SortOrder, input.SortOrder)
	assignInput(&inst.PollIntervalSeconds, input.PollIntervalSeconds)
	assignInput(&inst.AuditBudgetPerDay, input.AuditBudgetPerDay)
	if err := s.validateInstance(&inst); err != nil {
		return nil, err
	}
	if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" {
		if err := s.setKey(&inst, strings.TrimSpace(*input.APIKey)); err != nil {
			return nil, err
		}
		inst.CredGen++
	}
	if inst.Name != previous.Name || inst.Slug != previous.Slug {
		if err := s.setUniqueSlug(ctx, &inst, inst.Slug == ""); err != nil {
			return nil, err
		}
	}
	if inst == *previous {
		view := s.instanceView(inst, s.auth)
		return &view, nil
	}
	inst.Generation++
	if credentialTargetChanged(*previous, inst) {
		inst.CapabilitiesJSON, inst.LastVersion, inst.LastErrorCode, inst.LastErrorSummary = "", "", "", ""
		inst.LastReachability, inst.LastStatus = string(ReachabilityUnknown), string(StatusPending)
		inst.LastHealthScore, inst.LastObservedAt, inst.LastVerifiedAt, inst.BackoffUntil = nil, nil, nil, nil
	}
	if err := s.deps.Store.UpdateInstance(ctx, &inst); err != nil {
		return nil, err
	}
	s.poller.ReconcileInstance(inst)
	updated, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	view := s.instanceView(*updated, s.auth)
	return &view, nil
}

func assignInput[T any](field *T, update *T) {
	if update != nil {
		*field = *update
	}
}

func (s *Service) validateInstance(inst *storage.SiteOpsInstance) error {
	inst.Name = strings.TrimSpace(inst.Name)
	if inst.Name == "" || utf8.RuneCountInString(inst.Name) > 128 {
		return fmt.Errorf("%w: name", ErrInvalidInput)
	}
	if s.deps.Validator == nil {
		return ErrOriginNotAllowed
	}
	url, err := s.deps.Validator.ValidateBaseURL(inst.BaseURL)
	if err != nil {
		return ErrOriginNotAllowed
	}
	if url == "" || len(url) > 512 {
		return fmt.Errorf("%w: base_url", ErrInvalidInput)
	}
	inst.BaseURL = url
	if inst.KeyKind == "" {
		inst.KeyKind = KeyKindScoped
	}
	if inst.KeyKind != KeyKindScoped && inst.KeyKind != KeyKindFull {
		return fmt.Errorf("%w: key_kind", ErrInvalidInput)
	}
	if inst.PollIntervalSeconds == 0 {
		inst.PollIntervalSeconds = s.cfg.DefaultPollIntervalSeconds
	}
	if inst.PollIntervalSeconds < config.MinSiteOpsPollIntervalSeconds || int64(inst.PollIntervalSeconds) > int64((1<<63-1)/time.Second) {
		return fmt.Errorf("%w: poll_interval_seconds", ErrInvalidInput)
	}
	if inst.AuditBudgetPerDay == 0 {
		inst.AuditBudgetPerDay = s.cfg.DefaultAuditBudgetPerDay
	}
	if inst.AuditBudgetPerDay < 0 {
		return fmt.Errorf("%w: audit_budget_per_day", ErrInvalidInput)
	}
	return nil
}

func (s *Service) setKey(inst *storage.SiteOpsInstance, key string) error {
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("%w: api_key", ErrInvalidInput)
	}
	if s.deps.Cipher == nil {
		return errors.New("site-ops credential encryption unavailable")
	}
	encrypted, err := s.deps.Cipher.Encrypt(key)
	if err != nil {
		return err
	}
	hint := "••••"
	if s.deps.MaskAPIKeyHint != nil {
		candidate := s.deps.MaskAPIKeyHint(key)
		if candidate != "" && !strings.Contains(candidate, key) && len(candidate) <= 64 {
			hint = candidate
		}
	}
	inst.APIKeyCipher, inst.APIKeyHint = encrypted, hint
	return nil
}

func (s *Service) setUniqueSlug(ctx context.Context, inst *storage.SiteOpsInstance, generate bool) error {
	instances, err := s.deps.Store.ListInstances(ctx)
	if err != nil {
		return err
	}
	used := make(map[string]bool, len(instances))
	for _, other := range instances {
		if other.ID == inst.ID {
			continue
		}
		if other.Name == inst.Name {
			return fmt.Errorf("%w: name already exists", ErrInvalidInput)
		}
		used[other.Slug] = true
	}
	if !generate {
		if inst.Slug == "" || inst.Slug != slugify(inst.Slug) || used[inst.Slug] {
			return fmt.Errorf("%w: slug", ErrInvalidInput)
		}
		return nil
	}
	base := slugify(inst.Name)
	if base == "" {
		base = "site"
	}
	inst.Slug = base
	for n := 2; used[inst.Slug]; n++ {
		suffix := "-" + strconv.Itoa(n)
		inst.Slug = strings.TrimRight(base[:min(len(base), 64-len(suffix))], "-") + suffix
	}
	return nil
}

func slugify(name string) string {
	var slug strings.Builder
	separator := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if separator && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			slug.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
		if slug.Len() >= 64 {
			break
		}
	}
	return strings.TrimRight(slug.String()[:min(slug.Len(), 64)], "-")
}

func (s *Service) DeleteInstance(ctx context.Context, id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.auth {
		return ErrAuthRequired
	}
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	s.poller.RemoveInstance(id)
	if err := s.deps.Store.DeleteInstance(ctx, id); err != nil {
		if !errors.Is(err, ErrInstanceNotFound) {
			s.poller.ReconcileInstance(*inst)
		}
		return err
	}
	return nil
}

func (s *Service) ProbeInstance(ctx context.Context, id uint) (*ProbeReport, error) {
	return s.poller.request(ctx, id, actionProbe)
}

func (s *Service) RefreshInstance(ctx context.Context, id uint) (*RefreshResult, error) {
	if err := s.poller.TriggerRefresh(ctx, id); err != nil {
		return nil, err
	}
	overview, err := s.GetOverview(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RefreshResult{InstanceID: id, Snapshot: overview.Latest, Status: overview.Instance.LastStatus,
		Message: overview.Instance.LastErrorSummary}, nil
}

func (s *Service) GetSummary(ctx context.Context) (*SummaryResponse, error) {
	instances, err := s.deps.Store.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	latest, err := s.deps.Store.GetLatestSnapshotsByInstances(ctx, ids)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	pollingAllowed := IsPollingAllowed(s.auth, s.cfg)
	out := SummaryResponse{AuthEnabled: s.auth, PollingActive: pollingAllowed && s.active, TotalCount: len(instances),
		Instances: make([]InstanceSummaryItem, 0, len(instances))}
	s.mu.Unlock()
	for _, row := range instances {
		inst := s.instanceView(row, pollingAllowed)
		item := InstanceSummaryItem{ID: inst.ID, Name: inst.Name, Slug: inst.Slug, Enabled: inst.Enabled,
			LastReachability: inst.LastReachability, LastStatus: inst.LastStatus, HealthScore: inst.LastHealthScore,
			ConcTrustState: QualityUnavailable, LastVersion: inst.LastVersion, LastObservedAt: inst.LastObservedAt,
			LastVerifiedAt: inst.LastVerifiedAt, AuditBudget: inst.AuditBudget,
			LastErrorCode: inst.LastErrorCode, LastErrorSummary: inst.LastErrorSummary}
		if snap, exists := latest[inst.ID]; exists && inst.LastObservedAt != nil {
			item.SLAPercent1h, item.QPSCurrent = snap.SLAPercent1h, snap.QPSCurrent
			item.ConcUsed, item.ConcMax, item.ConcTrustState = snap.ConcUsed, snap.ConcMax, FieldQualityState(snap.ConcTrustState)
			item.AccountsTotal, item.AccountsOpsAvailable, item.AccountsError = snap.AccountsTotal, snap.AccountsOpsAvailable, snap.AccountsError
		}
		switch item.LastStatus {
		case StatusOnline:
			out.OnlineCount++
		case StatusDegraded, StatusAuthError:
			out.DegradedCount++
		case StatusOffline:
			out.OfflineCount++
		}
		out.Instances = append(out.Instances, item)
	}
	return &out, nil
}

func (s *Service) GetOverview(ctx context.Context, id uint) (*OverviewResponse, error) {
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	auth, _ := s.AccessPolicy()
	out := OverviewResponse{Instance: s.instanceView(*inst, IsPollingAllowed(auth, s.CurrentConfig())), SystemMetricsResolution: SystemMetricsResolutionNote,
		SLADenominatorNote: SLADenominatorNote, OpsAvailabilityLabel: OpsAvailabilityLabel, ConfiguredCapacityLabel: ConfiguredCapacityLabel}
	if inst.LastObservedAt != nil {
		snapshot, err := s.deps.Store.GetLatestSnapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Latest = snapshotView(snapshot)
	}
	return &out, nil
}

func (s *Service) GetTrends(ctx context.Context, id uint, rangeStr string) (*TrendsResponse, error) {
	if rangeStr == "" {
		rangeStr = "24h"
	}
	duration, ok := map[string]time.Duration{
		"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "14d": 14 * 24 * time.Hour,
	}[rangeStr]
	if !ok {
		return nil, fmt.Errorf("%w: range", ErrInvalidInput)
	}
	if _, err := s.deps.Store.GetInstance(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.deps.Store.ListSnapshotsSince(ctx, id, s.poller.now().Add(-duration), 0)
	if err != nil {
		return nil, err
	}
	out := TrendsResponse{InstanceID: id, Range: rangeStr, Points: make([]TrendPoint, 0, len(rows))}
	for _, row := range rows {
		out.Points = append(out.Points, trendPoint(row))
	}
	return &out, nil
}

func (s *Service) GetGroups(ctx context.Context, id uint) (*GroupsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if live, ok := s.poller.LatestLiveGroups(id); ok {
		return live, nil
	}
	out := groupsView(id, nil, QualityUnavailable, "", nil, nil)
	if inst.LastObservedAt == nil {
		return &out, nil
	}
	rows, err := s.deps.Store.GetLatestGroupSnapshots(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &out, nil
	}
	observed := rows[0].ObservedAt
	out.ObservedAt, out.TrustState = &observed, QualityTrusted
	stale := s.poller.now().Sub(observed) > liveGroupsTTL
	if stale {
		out.TrustState = QualityStale
		out.TrustReason = "实时分组样本已过期，当前显示最近一次归档"
	}
	for _, row := range rows {
		item := GroupCapacityItem{GroupID: row.GroupID, GroupName: row.GroupName, Platform: row.Platform,
			ObservedAt: row.ObservedAt, TrustState: FieldQualityState(row.TrustState), AccountsTotal: row.AccountsTotal,
			AccountsOpsAvailable: row.AccountsOpsAvailable, AccountsRateLimited: row.AccountsRateLimited,
			AccountsOverloaded: row.AccountsOverloaded, AccountsError: row.AccountsError,
			SinglePointRisk: row.AccountsTotal > 0 && row.AccountsOpsAvailable <= 1,
			ConcUsed:        row.ConcUsed, ConcMax: row.ConcMax, ConcWaiting: row.ConcWaiting, LoadPercent: loadPercent(row.ConcUsed, row.ConcMax)}
		switch item.TrustState {
		case QualityUnverifiedZero:
			item.TrustReason = "归档中的实时计数未证实"
			out.TrustState, out.TrustReason = item.TrustState, item.TrustReason
		case QualityTrusted:
			if stale {
				item.TrustState, item.TrustReason = QualityStale, "实时分组样本已过期"
			}
		default:
			if out.TrustState == QualityTrusted {
				out.TrustState = item.TrustState
			}
		}
		out.Groups = append(out.Groups, item)
	}
	snapshot, err := s.deps.Store.GetLatestSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if snapshot != nil {
		detail := decodeSnapshotDetail(snapshot.DetailJSON)
		out.PlatformBreakdown = emptySlice(detail.PlatformBreakdown)
	}
	return &out, nil
}

func (s *Service) instanceView(inst storage.SiteOpsInstance, auth bool) InstanceView {
	budget := AuditBudgetUsage{DailyLimit: inst.AuditBudgetPerDay}
	if s.deps.Client != nil {
		budget = s.deps.Client.BudgetUsage(inst.ID, inst.AuditBudgetPerDay)
	}
	status := InstanceStatus(inst.LastStatus)
	if !auth && inst.Enabled {
		status = StatusPausedNoAuth
	}
	return InstanceView{ID: inst.ID, Name: inst.Name, Slug: inst.Slug, BaseURL: inst.BaseURL,
		APIKeySet: inst.APIKeyCipher != "", APIKeyHint: inst.APIKeyHint, KeyKind: inst.KeyKind, CredGen: inst.CredGen,
		Generation: inst.Generation, ProxyEnabled: inst.ProxyEnabled, Enabled: inst.Enabled, SortOrder: inst.SortOrder,
		PollIntervalSeconds: inst.PollIntervalSeconds, AuditBudgetPerDay: inst.AuditBudgetPerDay, AuditBudget: budget,
		Capabilities: decodeCapabilities(inst.CapabilitiesJSON), LastReachability: ReachabilityState(inst.LastReachability),
		LastStatus: status, LastHealthScore: inst.LastHealthScore, LastVersion: inst.LastVersion,
		LastObservedAt: inst.LastObservedAt, LastVerifiedAt: inst.LastVerifiedAt, BackoffUntil: inst.BackoffUntil,
		LastErrorCode: inst.LastErrorCode, LastErrorSummary: inst.LastErrorSummary, CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt}
}

func decodeCapabilities(raw string) CapabilitiesMap {
	var caps CapabilitiesMap
	if json.Unmarshal([]byte(raw), &caps) != nil || caps == nil {
		return make(CapabilitiesMap)
	}
	return caps
}

func decodeFieldQuality(raw string) FieldQualityMap {
	var quality FieldQualityMap
	_ = json.Unmarshal([]byte(raw), &quality)
	return completeFieldQuality(quality)
}

func decodeSnapshotDetail(raw string) SnapshotDetailPayload {
	var detail SnapshotDetailPayload
	_ = json.Unmarshal([]byte(raw), &detail)
	return detail
}

func archiveGroups(id uint, groups []GroupCapacityItem) []storage.SiteOpsGroupSnapshot {
	rows := make([]storage.SiteOpsGroupSnapshot, 0, len(groups))
	for _, group := range groups {
		rows = append(rows, storage.SiteOpsGroupSnapshot{InstanceID: id, GroupID: group.GroupID, GroupName: group.GroupName,
			Platform: group.Platform, ObservedAt: group.ObservedAt, TrustState: string(group.TrustState),
			AccountsTotal: group.AccountsTotal, AccountsOpsAvailable: group.AccountsOpsAvailable, AccountsRateLimited: group.AccountsRateLimited,
			AccountsOverloaded: group.AccountsOverloaded, AccountsError: group.AccountsError,
			ConcUsed: group.ConcUsed, ConcMax: group.ConcMax, ConcWaiting: group.ConcWaiting})
	}
	return rows
}

func groupsView(id uint, observed *time.Time, trust FieldQualityState, reason string, platforms []PlatformPoolSummary, groups []GroupCapacityItem) GroupsResponse {
	return GroupsResponse{InstanceID: id, ObservedAt: observed, TrustState: trust, TrustReason: reason,
		OpsAvailabilityLabel: OpsAvailabilityLabel, ConfiguredCapacityLabel: ConfiguredCapacityLabel,
		PlatformBreakdown: emptySlice(platforms), Groups: emptySlice(groups)}
}

func emptySlice[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func cloneGroupsView(response GroupsResponse) *GroupsResponse {
	response.ObservedAt = cloneSiteOpsValue(response.ObservedAt)
	response.Groups = slices.Clone(response.Groups)
	for i := range response.Groups {
		group := &response.Groups[i]
		group.ConcUsed, group.ConcMax = cloneSiteOpsValue(group.ConcUsed), cloneSiteOpsValue(group.ConcMax)
		group.ConcWaiting, group.LoadPercent = cloneSiteOpsValue(group.ConcWaiting), cloneSiteOpsValue(group.LoadPercent)
	}
	response.PlatformBreakdown = slices.Clone(response.PlatformBreakdown)
	for i := range response.PlatformBreakdown {
		platform := &response.PlatformBreakdown[i]
		platform.ConcUsed, platform.ConcMax = cloneSiteOpsValue(platform.ConcUsed), cloneSiteOpsValue(platform.ConcMax)
		platform.ConcWaiting, platform.LoadPercent = cloneSiteOpsValue(platform.ConcWaiting), cloneSiteOpsValue(platform.LoadPercent)
	}
	return &response
}

func cloneSiteOpsValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	return ptr(*value)
}

type storageStore struct{ *storage.SiteOpsStore }

func NewStorageStore(s *storage.SiteOpsStore) Store { return storageStore{s} }

func (s storageStore) GetInstance(ctx context.Context, id uint) (*storage.SiteOpsInstance, error) {
	inst, err := s.SiteOpsStore.GetInstance(ctx, id)
	return inst, siteOpsStoreError(err)
}

func (s storageStore) UpdateInstance(ctx context.Context, inst *storage.SiteOpsInstance) error {
	return siteOpsStoreError(s.SiteOpsStore.UpdateInstance(ctx, inst))
}

func (s storageStore) DeleteInstance(ctx context.Context, id uint) error {
	return siteOpsStoreError(s.SiteOpsStore.DeleteInstance(ctx, id))
}

func (s storageStore) CommitCollection(ctx context.Context, id uint, generation int64, patch InstanceRuntimePatch, snap *storage.SiteOpsSnapshot, groups []storage.SiteOpsGroupSnapshot) error {
	return siteOpsStoreError(s.SiteOpsStore.CommitCollection(ctx, id, generation, storage.SiteOpsRuntimePatch(patch), snap, groups))
}

func (s storageStore) TouchSnapshotVerified(ctx context.Context, id uint, generation int64, verified time.Time, patch InstanceRuntimePatch) error {
	return siteOpsStoreError(s.SiteOpsStore.TouchSnapshotVerified(ctx, id, generation, verified, storage.SiteOpsRuntimePatch(patch)))
}

func siteOpsStoreError(err error) error {
	switch {
	case errors.Is(err, storage.ErrSiteOpsNotFound):
		return ErrInstanceNotFound
	case errors.Is(err, storage.ErrSiteOpsGenerationMismatch):
		return ErrGenerationMismatch
	default:
		return err
	}
}

func snapshotView(row *storage.SiteOpsSnapshot) *SnapshotView {
	if row == nil {
		return nil
	}
	detail := decodeSnapshotDetail(row.DetailJSON)
	return &SnapshotView{
		ObservedAt:              row.ObservedAt,
		VerifiedAt:              row.VerifiedAt,
		Reachable:               row.Reachable,
		ProbeLatencyMS:          row.ProbeLatencyMS,
		FieldQuality:            decodeFieldQuality(row.FieldQualityJSON),
		HealthScore:             row.HealthScore,
		UptimeSeconds:           row.UptimeSeconds,
		CPUPercent:              row.CPUPercent,
		MemPercent:              row.MemPercent,
		MemUsedMB:               row.MemUsedMB,
		DBOK:                    row.DBOK,
		DBConnActive:            row.DBConnActive,
		DBConnWaiting:           row.DBConnWaiting,
		DBMaxOpenConns:          row.DBMaxOpenConns,
		RedisOK:                 row.RedisOK,
		RedisConnTotal:          row.RedisConnTotal,
		RedisConnIdle:           row.RedisConnIdle,
		RedisPoolSize:           row.RedisPoolSize,
		GoroutineCount:          row.GoroutineCount,
		QueueDepth:              row.QueueDepth,
		AccountSwitchCount1m:    row.AccountSwitchCount1m,
		EstimatedSwitchPct1m:    detail.EstimatedSwitchPct1m,
		SLAPercent1h:            row.SLAPercent1h,
		ErrorRatePercent1h:      row.ErrorRatePercent1h,
		UpstreamErrorRatePct1h:  row.UpstreamErrorRatePct1h,
		ReqTotal1h:              row.ReqTotal1h,
		ErrTotal1h:              row.ErrTotal1h,
		Upstream4291h:           row.Upstream4291h,
		Upstream5291h:           row.Upstream5291h,
		UpstreamErrExcl4295291h: row.UpstreamErrExcl4295291h,
		QPSCurrent:              row.QPSCurrent,
		QPSPeak:                 row.QPSPeak,
		TPSCurrent:              row.TPSCurrent,
		RPM5m:                   row.RPM5m,
		TPM5m:                   row.TPM5m,
		TTFTP50MS:               row.TTFTP50MS,
		TTFTP95MS:               row.TTFTP95MS,
		DurationP95MS:           row.DurationP95MS,
		OutputTPSP50:            row.OutputTPSP50,
		ConcUsed:                row.ConcUsed,
		ConcMax:                 row.ConcMax,
		ConcWaiting:             row.ConcWaiting,
		ConcLoadPercent:         loadPercent(row.ConcUsed, row.ConcMax),
		ConcTrustState:          FieldQualityState(row.ConcTrustState),
		ConcTrustReason:         detail.ConcTrustReason,
		AccountsTotal:           row.AccountsTotal,
		AccountsOpsAvailable:    row.AccountsOpsAvailable,
		AccountsRateLimited:     row.AccountsRateLimited,
		AccountsOverloaded:      row.AccountsOverloaded,
		AccountsError:           row.AccountsError,
		SystemMetricsAt:         detail.SystemMetricsAt,
		JobHeartbeats:           detail.JobHeartbeats,
		PlatformBreakdown:       detail.PlatformBreakdown,
	}
}

func trendPoint(row storage.SiteOpsSnapshot) TrendPoint {
	return TrendPoint{
		ObservedAt:             row.ObservedAt,
		VerifiedAt:             row.VerifiedAt,
		Reachable:              row.Reachable,
		ProbeLatencyMS:         row.ProbeLatencyMS,
		HealthScore:            row.HealthScore,
		SLAPercent1h:           row.SLAPercent1h,
		ErrorRatePercent1h:     row.ErrorRatePercent1h,
		UpstreamErrorRatePct1h: row.UpstreamErrorRatePct1h,
		AccountSwitchCount1m:   row.AccountSwitchCount1m,
		QPSCurrent:             row.QPSCurrent,
		TTFTP95MS:              row.TTFTP95MS,
		DurationP95MS:          row.DurationP95MS,
		CPUPercent:             row.CPUPercent,
		MemPercent:             row.MemPercent,
		ConcUsed:               row.ConcUsed,
		ConcMax:                row.ConcMax,
		ConcTrustState:         FieldQualityState(row.ConcTrustState),
		AccountsTotal:          row.AccountsTotal,
		AccountsOpsAvailable:   row.AccountsOpsAvailable,
	}
}

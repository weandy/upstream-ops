package siteops

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	accountStatsCacheTTL       = 10 * time.Minute
	accountStatsMaxFetchCount  = 50
	accountStatsFetchConcLimit = 3
)

type cachedAccountStats struct {
	generation int64
	credGen    int64
	fetchedAt  time.Time
	stats      map[int64]*SanitizedAccountStats
	trustState FieldQualityState
}

func (s *Service) p1bStore() P1BStore {
	if s.deps.P1BStore != nil {
		return s.deps.P1BStore
	}
	if ps, ok := s.deps.Store.(P1BStore); ok {
		return ps
	}
	return nil
}

func (s *Service) invalidateStatsCacheLocked(id uint) {
	if s.statsCache != nil {
		delete(s.statsCache, id)
	}
}

func (s *Service) targetForInstance(inst storage.SiteOpsInstance) (TargetEndpoint, error) {
	target := TargetEndpoint{
		InstanceID:        inst.ID,
		BaseURL:           inst.BaseURL,
		KeyKind:           inst.KeyKind,
		CredGen:           inst.CredGen,
		Generation:        inst.Generation,
		ProxyEnabled:      inst.ProxyEnabled,
		AuditBudgetPerDay: inst.AuditBudgetPerDay,
	}
	if s.deps.Validator == nil {
		return target, ErrOriginNotAllowed
	}
	normURL, err := s.deps.Validator.ValidateBaseURL(inst.BaseURL)
	if err != nil || normURL == "" {
		return target, ErrOriginNotAllowed
	}
	target.BaseURL = normURL
	if s.deps.Cipher == nil {
		return target, ErrUnauthorized
	}
	key, err := s.deps.Cipher.Decrypt(inst.APIKeyCipher)
	if err != nil || key == "" {
		return target, ErrUnauthorized
	}
	target.APIKey = key
	return target, nil
}

// GetPoolForecast 返回指定实例的 M4 被动号池周期消耗预测视图。
func (s *Service) GetPoolForecast(ctx context.Context, id uint) (*PoolForecastResponse, error) {
	if _, err := s.deps.Store.GetInstance(ctx, id); err != nil {
		return nil, err
	}
	if live, ok := s.poller.LatestPoolForecast(id); ok {
		return live, nil
	}
	accounts := s.poller.LatestAccountsDirectory(id)
	groups := s.poller.LatestGroupMetadata(id)
	now := s.poller.now()
	windowMap := make(map[int64][]WindowForecastView)
	var observedAt *time.Time
	trust := QualityUnavailable

	if ps := s.p1bStore(); ps != nil {
		rows, err := ps.ListLatestWindowSamplesByInstance(ctx, id, now.Add(-7*24*time.Hour))
		if err != nil {
			return nil, err
		}
		type winKey struct {
			accID  int64
			window string
			source string
		}
		latest := make(map[winKey]storage.SiteOpsAccountWindowSample)
		for _, row := range rows {
			k := winKey{accID: row.AccountID, window: row.WindowKey, source: row.Source}
			if prev, exists := latest[k]; !exists || !row.ObservedAt.Before(prev.ObservedAt) {
				latest[k] = row
			}
			if observedAt == nil || row.ObservedAt.After(*observedAt) {
				t := row.ObservedAt
				observedAt = &t
			}
		}
		for _, row := range latest {
			windowMap[row.AccountID] = append(windowMap[row.AccountID], WindowForecastView{
				WindowKey:          row.WindowKey,
				Source:             row.Source,
				WindowDurationSec:  row.WindowDurationSec,
				Utilization:        row.Utilization,
				ResetsAt:           cloneSiteOpsValue(row.ResetsAt),
				RemainingSeconds:   row.RemainingSeconds,
				UpstreamObservedAt: row.UpstreamObservedAt,
				BurnRatePerHour:    cloneSiteOpsValue(row.BurnRatePerHour),
				ETASeconds:         cloneSiteOpsValue(row.ETASeconds),
				PredictedEndUtil:   cloneSiteOpsValue(row.PredictedEndUtil),
				MarginHours:        cloneSiteOpsValue(row.MarginHours),
				ForecastStatus:     ForecastStatus(row.ForecastStatus),
				Confidence:         ForecastConfidence(row.Confidence),
			})
		}
		if len(latest) > 0 {
			trust = QualityStale
		}
	}
	if len(accounts) > 0 && trust == QualityUnavailable {
		trust = QualityTrusted
		observedAt = &now
	}
	resp := BuildPoolForecast(id, observedAt, trust, accounts, groups, windowMap)
	return &resp, nil
}

// GetAccountsEfficiency 返回指定实例的 M5 单号成本效率评估（默认规避 today-stats/batch，走逐账号鉴权 stats 接口 + 10m 缓存）。
func (s *Service) GetAccountsEfficiency(ctx context.Context, id uint) (*AccountsEfficiencyResponse, error) {
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}

	accounts := s.poller.LatestAccountsDirectory(id)
	if len(accounts) == 0 {
		if p1a, ok := s.deps.Client.(P1AClient); ok {
			if target, targetErr := s.targetForInstance(*inst); targetErr == nil {
				if dir, _, fetchErr := p1a.FetchAccountsDirectory(ctx, target); fetchErr == nil && len(dir) > 0 {
					accounts = dir
					s.poller.CacheAccountsDirectory(id, dir)
				}
			}
		}
	}

	now := s.poller.now()
	var statsMap map[int64]*SanitizedAccountStats
	trustState := QualityUnavailable
	var observedAt *time.Time

	s.mu.Lock()
	cached, hasCache := s.statsCache[id]
	s.mu.Unlock()

	if hasCache &&
		cached.generation == inst.Generation &&
		cached.credGen == inst.CredGen &&
		now.Sub(cached.fetchedAt) <= accountStatsCacheTTL {
		statsMap = cached.stats
		trustState = cached.trustState
		ts := cached.fetchedAt
		observedAt = &ts
	} else if len(accounts) > 0 {
		statsMap, trustState = s.fetchAccountStatsBounded(ctx, *inst, accounts)
		ts := s.poller.now()
		observedAt = &ts
		if trustState == QualityTrusted || trustState == QualityUnauthorized {
			s.mu.Lock()
			if s.statsCache == nil {
				s.statsCache = make(map[uint]cachedAccountStats)
			}
			s.statsCache[id] = cachedAccountStats{
				generation: inst.Generation,
				credGen:    inst.CredGen,
				fetchedAt:  ts,
				stats:      statsMap,
				trustState: trustState,
			}
			s.mu.Unlock()
		}
	}

	costsMap := make(map[int64]storage.SiteOpsAccountCost)
	if ps := s.p1bStore(); ps != nil {
		costs, err := ps.ListAccountCosts(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, c := range costs {
			costsMap[c.AccountID] = c
		}
	}

	latestWindows := s.poller.LatestUsageWindows(id)
	resp := BuildAccountsEfficiency(EfficiencyInput{
		InstanceID:           id,
		ObservedAt:           observedAt,
		TrustState:           trustState,
		Accounts:             accounts,
		Stats:                statsMap,
		LatestWindows:        latestWindows,
		Costs:                costsMap,
		TodayStatsBatchGated: true,
	})
	return &resp, nil
}

func (s *Service) fetchAccountStatsBounded(
	ctx context.Context,
	inst storage.SiteOpsInstance,
	accounts []SanitizedAccountSummary,
) (map[int64]*SanitizedAccountStats, FieldQualityState) {
	result := make(map[int64]*SanitizedAccountStats, len(accounts))
	p1b, ok := s.deps.Client.(P1BClient)
	if !ok {
		return result, QualityUnavailable
	}
	target, err := s.targetForInstance(inst)
	if err != nil {
		return result, qualityFor(err)
	}

	limit := min(len(accounts), accountStatsMaxFetchCount)
	ids := make([]int64, 0, limit)
	for i := 0; i < limit; i++ {
		if accounts[i].AccountID > 0 {
			ids = append(ids, accounts[i].AccountID)
		}
	}
	if len(ids) == 0 {
		return result, QualityUnavailable
	}

	var mu sync.Mutex
	var succeeded int
	var unauthorizedCount int
	var halted bool
	sem := make(chan struct{}, accountStatsFetchConcLimit)
	var wg sync.WaitGroup

	for _, accID := range ids {
		mu.Lock()
		stop := halted
		mu.Unlock()
		if stop || ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break
		}
		wg.Add(1)
		go func(accountID int64) {
			defer wg.Done()
			defer func() { <-sem }()
			st, _, fetchErr := p1b.FetchAccountStats(ctx, target, accountID, 30)
			mu.Lock()
			defer mu.Unlock()
			if fetchErr == nil && st != nil {
				result[accountID] = st
				succeeded++
				return
			}
			switch {
			case errors.Is(fetchErr, ErrUnauthorized), errors.Is(fetchErr, ErrForbidden):
				unauthorizedCount++
				if errors.Is(fetchErr, ErrUnauthorized) {
					halted = true
				}
			case errors.Is(fetchErr, ErrRateLimited), errors.Is(fetchErr, ErrBudgetExceeded):
				halted = true
			}
		}(accID)
	}
	wg.Wait()

	if succeeded > 0 {
		return result, QualityTrusted
	}
	if unauthorizedCount > 0 {
		return result, QualityUnauthorized
	}
	return result, QualityUnavailable
}

// UpdateAccountCost 保存或更新单账号本地参考月租成本（monthly_cost_usd >= 0，0 表示清除月租杠杆）。
func (s *Service) UpdateAccountCost(ctx context.Context, id uint, accountID int64, input UpdateAccountCostInput) (*AccountCostView, error) {
	if accountID <= 0 {
		return nil, fmt.Errorf("%w: account_id", ErrInvalidInput)
	}
	if math.IsNaN(input.MonthlyCostUSD) || math.IsInf(input.MonthlyCostUSD, 0) || input.MonthlyCostUSD < 0 {
		return nil, fmt.Errorf("%w: monthly_cost_usd", ErrInvalidInput)
	}
	if _, err := s.deps.Store.GetInstance(ctx, id); err != nil {
		return nil, err
	}
	ps := s.p1bStore()
	if ps == nil {
		return nil, fmt.Errorf("%w: account cost store unavailable", ErrInvalidInput)
	}
	now := s.poller.now()
	row := &storage.SiteOpsAccountCost{
		InstanceID:     id,
		AccountID:      accountID,
		MonthlyCostUSD: round4(input.MonthlyCostUSD),
		Note:           SanitizeErrorText(input.Note, 256),
		UpdatedAt:      now,
	}
	if err := ps.UpsertAccountCost(ctx, row); err != nil {
		return nil, err
	}
	return &AccountCostView{
		InstanceID:     row.InstanceID,
		AccountID:      row.AccountID,
		MonthlyCostUSD: row.MonthlyCostUSD,
		Note:           row.Note,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

func clonePoolForecastResponse(src PoolForecastResponse) *PoolForecastResponse {
	cp := src
	cp.ObservedAt = cloneSiteOpsValue(src.ObservedAt)
	cp.Groups = slices.Clone(src.Groups)
	for i := range cp.Groups {
		cp.Groups[i].HighRiskRatioPct = cloneSiteOpsValue(cp.Groups[i].HighRiskRatioPct)
	}
	cp.Accounts = slices.Clone(src.Accounts)
	for i := range cp.Accounts {
		a := &cp.Accounts[i]
		a.GroupIDs = slices.Clone(a.GroupIDs)
		a.ExpiresAt = cloneSiteOpsValue(a.ExpiresAt)
		a.EarliestETASeconds = cloneSiteOpsValue(a.EarliestETASeconds)
		a.EarliestMarginHours = cloneSiteOpsValue(a.EarliestMarginHours)
		a.Windows = slices.Clone(a.Windows)
		for j := range a.Windows {
			w := &a.Windows[j]
			w.ResetsAt = cloneSiteOpsValue(w.ResetsAt)
			w.BurnRatePerHour = cloneSiteOpsValue(w.BurnRatePerHour)
			w.ETASeconds = cloneSiteOpsValue(w.ETASeconds)
			w.PredictedEndUtil = cloneSiteOpsValue(w.PredictedEndUtil)
			w.MarginHours = cloneSiteOpsValue(w.MarginHours)
		}
	}
	return &cp
}

func cloneUsageWindowsMap(src map[int64][]SanitizedUsageWindow) map[int64][]SanitizedUsageWindow {
	if len(src) == 0 {
		return nil
	}
	out := make(map[int64][]SanitizedUsageWindow, len(src))
	for k, wins := range src {
		cloned := make([]SanitizedUsageWindow, len(wins))
		for i, w := range wins {
			w.ResetsAt = cloneSiteOpsValue(w.ResetsAt)
			w.WindowStats = cloneSiteOpsValue(w.WindowStats)
			cloned[i] = w
		}
		out[k] = cloned
	}
	return out
}

func cloneAccountsDir(src []SanitizedAccountSummary) []SanitizedAccountSummary {
	if len(src) == 0 {
		return nil
	}
	out := make([]SanitizedAccountSummary, len(src))
	for i, a := range src {
		a.GroupIDs = slices.Clone(a.GroupIDs)
		a.ExpiresAt = cloneSiteOpsValue(a.ExpiresAt)
		a.RateLimitResetAt = cloneSiteOpsValue(a.RateLimitResetAt)
		a.OverloadUntil = cloneSiteOpsValue(a.OverloadUntil)
		a.TempUnschedulableUntil = cloneSiteOpsValue(a.TempUnschedulableUntil)
		out[i] = a
	}
	return out
}

func cloneGroupCapMap(src map[int64]SanitizedGroupCapacitySummary) map[int64]SanitizedGroupCapacitySummary {
	if len(src) == 0 {
		return nil
	}
	return maps.Clone(src)
}

package siteops

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

func (s *Service) eventStore() EventStore {
	if s.deps.EventStore != nil {
		return s.deps.EventStore
	}
	if es, ok := s.deps.Store.(EventStore); ok {
		return es
	}
	return nil
}

func (s *Service) currentNotifier() DirectNotifier {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deps.Notifier
}

// ListEvents 返回指定实例的状态跃迁事件流与各渠道投递明细。
func (s *Service) ListEvents(ctx context.Context, id uint, state, severity string, limit int) (*EventsResponse, error) {
	if _, err := s.deps.Store.GetInstance(ctx, id); err != nil {
		return nil, err
	}
	out := &EventsResponse{
		InstanceID: id,
		Items:      []EventItemView{},
	}
	es := s.eventStore()
	if es == nil {
		return out, nil
	}
	if state != "" && state != EventStateFiring && state != EventStateResolved {
		return nil, fmt.Errorf("%w: state", ErrInvalidInput)
	}
	if severity != "" && severity != SeverityCritical && severity != SeverityWarning && severity != SeverityInfo {
		return nil, fmt.Errorf("%w: severity", ErrInvalidInput)
	}

	active, err := es.ListActiveEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	out.FiringCount = len(active)

	rows, err := es.ListEvents(ctx, id, state, severity, limit)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return out, nil
	}

	eventIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		eventIDs = append(eventIDs, row.ID)
	}
	deliveryMap, err := es.ListDeliveriesByEventIDs(ctx, eventIDs)
	if err != nil {
		return nil, err
	}
	chMeta := s.channelMetaMap()

	items := make([]EventItemView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toEventItemView(row, deliveryMap[row.ID], chMeta))
	}
	out.Items = items
	return out, nil
}

// GetAccountsStatus 返回融合 /ops/account-availability、/ops/concurrency 与 /accounts 目录后的脱敏账号状态列表。
func (s *Service) GetAccountsStatus(ctx context.Context, id uint, statusFilter, platformFilter string) (*AccountsStatusResponse, error) {
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	statusFilter = strings.ToLower(strings.TrimSpace(statusFilter))
	switch statusFilter {
	case "", "all", "abnormal", "error", "rate_limited", "overloaded", "available":
	default:
		return nil, fmt.Errorf("%w: status", ErrInvalidInput)
	}
	platformFilter = strings.ToLower(strings.TrimSpace(platformFilter))

	if live, ok := s.poller.LatestAccountsStatus(id); ok {
		return filterAccountsStatus(live, statusFilter, platformFilter), nil
	}

	out := &AccountsStatusResponse{
		InstanceID: id,
		TrustState: QualityUnavailable,
		Items:      []AccountStatusItem{},
	}
	if inst.LastObservedAt == nil {
		return out, nil
	}
	snap, err := s.deps.Store.GetLatestSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if snap != nil {
		obs := snap.ObservedAt
		out.ObservedAt = &obs
		fq := decodeFieldQuality(snap.FieldQualityJSON)
		if q, exists := fq[DomainAccountAvailability]; exists && q != "" {
			out.TrustState = q
		}
		if snap.AccountsTotal != nil {
			out.TotalCount = int(*snap.AccountsTotal)
		}
		if snap.AccountsOpsAvailable != nil {
			out.AvailableCount = int(*snap.AccountsOpsAvailable)
		}
		if snap.AccountsRateLimited != nil {
			out.RateLimitCount = int(*snap.AccountsRateLimited)
		}
		if snap.AccountsOverloaded != nil {
			out.OverloadCount = int(*snap.AccountsOverloaded)
		}
		if snap.AccountsError != nil {
			out.ErrorCount = int(*snap.AccountsError)
		}
	}
	return out, nil
}

// PreviewDailyReport 生成指定实例昨日 24h 同窗聚合运维日报预览（默认零 PII）。
func (s *Service) PreviewDailyReport(ctx context.Context, id uint) (*DailyReportPreview, error) {
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.poller.now()
	startUTC, _, _ := YesterdayWindowUTC8(now)
	snapshots, err := s.deps.Store.ListSnapshotsSince(ctx, id, startUTC.Add(-2*time.Hour), 0)
	if err != nil {
		return nil, err
	}
	var activeEvents []storage.SiteOpsEvent
	if es := s.eventStore(); es != nil {
		activeEvents, err = es.ListActiveEvents(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	cfg := ParseAlertConfig(inst.AlertConfigJSON)
	poolForecast, _ := s.poller.LatestPoolForecast(id)
	preview := BuildDailyReport(DailyReportInput{
		Instance:             *inst,
		Now:                  now,
		Snapshots:            snapshots,
		ActiveEvents:         activeEvents,
		PoolForecast:         poolForecast,
		IncludeMaskedSummary: cfg.IncludeMaskedSummary,
	})
	return &preview, nil
}

// SendDailyReport 生成昨日运维日报并直投至显式订阅 daily_report 的通知渠道（零广播）。
func (s *Service) SendDailyReport(ctx context.Context, id uint) (*DailyReportSendResult, error) {
	inst, err := s.deps.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	preview, err := s.PreviewDailyReport(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &DailyReportSendResult{
		Preview:    *preview,
		Deliveries: []EventDeliveryView{},
	}
	es := s.eventStore()
	notifier := s.currentNotifier()
	if es == nil || notifier == nil {
		return out, nil
	}
	dispatcher := NewAlertDispatcher(es, notifier)
	dispatcher.Now = s.poller.now
	matched, deliveries, err := dispatcher.DispatchDailyReport(ctx, *inst, *preview)
	if err != nil {
		return nil, err
	}
	chMeta := s.channelMetaMap()
	out.MatchedTargets = matched
	out.Deliveries = toDeliveryViews(deliveries, chMeta)
	return out, nil
}

type channelMetaInfo struct {
	name string
	kind string
}

func (s *Service) channelMetaMap() map[uint]channelMetaInfo {
	notifier := s.currentNotifier()
	if notifier == nil {
		return nil
	}
	channels, err := notifier.ListEnabledChannels()
	if err != nil || len(channels) == 0 {
		return nil
	}
	meta := make(map[uint]channelMetaInfo, len(channels))
	for _, ch := range channels {
		meta[ch.ID] = channelMetaInfo{name: ch.Name, kind: string(ch.Type)}
	}
	return meta
}

func toEventItemView(row storage.SiteOpsEvent, deliveries []storage.SiteOpsAlertDelivery, chMeta map[uint]channelMetaInfo) EventItemView {
	var domains []string
	_ = json.Unmarshal([]byte(row.RequiredDomainsJSON), &domains)
	if domains == nil {
		domains = []string{}
	}
	var detail map[string]any
	if strings.TrimSpace(row.DetailJSON) != "" && strings.TrimSpace(row.DetailJSON) != "{}" {
		_ = json.Unmarshal([]byte(row.DetailJSON), &detail)
	}
	return EventItemView{
		ID:              row.ID,
		InstanceID:      row.InstanceID,
		Severity:        row.Severity,
		RuleKey:         row.RuleKey,
		ResourceKey:     row.ResourceKey,
		RequiredDomains: domains,
		State:           row.State,
		ConsecutiveHits: row.ConsecutiveHits,
		Title:           row.Title,
		Summary:         row.Summary,
		Detail:          detail,
		FiredAt:         row.FiredAt,
		ResolvedAt:      row.ResolvedAt,
		Deliveries:      toDeliveryViews(deliveries, chMeta),
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func toDeliveryViews(rows []storage.SiteOpsAlertDelivery, chMeta map[uint]channelMetaInfo) []EventDeliveryView {
	views := make([]EventDeliveryView, 0, len(rows))
	for _, d := range rows {
		meta := chMeta[d.NotifyChannelID]
		views = append(views, EventDeliveryView{
			ID:              d.ID,
			EventID:         d.EventID,
			NotifyChannelID: d.NotifyChannelID,
			ChannelName:     meta.name,
			ChannelType:     meta.kind,
			DeliveryKind:    d.DeliveryKind,
			Status:          d.Status,
			Attempts:        d.Attempts,
			NextRetryAt:     d.NextRetryAt,
			LastError:       d.LastError,
			SentAt:          d.SentAt,
			CreatedAt:       d.CreatedAt,
		})
	}
	return views
}

func filterAccountsStatus(src *AccountsStatusResponse, statusFilter, platformFilter string) *AccountsStatusResponse {
	cp := *src
	cp.ObservedAt = cloneSiteOpsValue(src.ObservedAt)
	if statusFilter == "" || statusFilter == "all" {
		if platformFilter == "" {
			cp.Items = cloneAccountStatusItems(src.Items)
			return &cp
		}
	}
	filtered := make([]AccountStatusItem, 0, len(src.Items))
	for _, item := range src.Items {
		if platformFilter != "" && !strings.EqualFold(item.Platform, platformFilter) {
			continue
		}
		if !matchAccountStatusFilter(item, statusFilter) {
			continue
		}
		filtered = append(filtered, cloneAccountStatusItem(item))
	}
	cp.Items = filtered
	return &cp
}

func matchAccountStatusFilter(item AccountStatusItem, filter string) bool {
	switch filter {
	case "", "all":
		return true
	case "abnormal":
		return item.HasError || item.RateLimited || item.Overloaded || !item.OpsAvailable
	case "error":
		return item.HasError
	case "rate_limited":
		return item.RateLimited
	case "overloaded":
		return item.Overloaded
	case "available":
		return item.OpsAvailable && !item.HasError && !item.RateLimited && !item.Overloaded
	default:
		return true
	}
}

func cloneAccountStatusItems(items []AccountStatusItem) []AccountStatusItem {
	out := make([]AccountStatusItem, 0, len(items))
	for _, item := range items {
		out = append(out, cloneAccountStatusItem(item))
	}
	return out
}

func cloneAccountStatusItem(item AccountStatusItem) AccountStatusItem {
	item.Schedulable = cloneSiteOpsValue(item.Schedulable)
	item.ConcUsed = cloneSiteOpsValue(item.ConcUsed)
	item.ConcMax = cloneSiteOpsValue(item.ConcMax)
	item.ConcWaiting = cloneSiteOpsValue(item.ConcWaiting)
	item.GroupIDs = slices.Clone(item.GroupIDs)
	item.ExpiresAt = cloneSiteOpsValue(item.ExpiresAt)
	item.RateLimitResetAt = cloneSiteOpsValue(item.RateLimitResetAt)
	item.OverloadUntil = cloneSiteOpsValue(item.OverloadUntil)
	item.TempUnschedulableUntil = cloneSiteOpsValue(item.TempUnschedulableUntil)
	return item
}

// buildLiveAccountsStatus 融合 /ops/account-availability、/ops/concurrency 与 /accounts 目录为脱敏账号状态面板数据。
func buildLiveAccountsStatus(
	instanceID uint,
	observedAt time.Time,
	trustState FieldQualityState,
	avail *SanitizedAvailabilityPayload,
	conc *SanitizedConcurrencyPayload,
	dir []SanitizedAccountSummary,
) AccountsStatusResponse {
	if trustState == "" {
		trustState = QualityUnavailable
	}
	dirMap := make(map[int64]SanitizedAccountSummary, len(dir))
	for _, a := range dir {
		dirMap[a.AccountID] = a
	}

	idSet := make(map[int64]struct{})
	if avail != nil {
		for accID := range avail.Accounts {
			idSet[accID] = struct{}{}
		}
	}
	if conc != nil {
		for accID := range conc.Accounts {
			idSet[accID] = struct{}{}
		}
	}
	for accID := range dirMap {
		idSet[accID] = struct{}{}
	}

	items := make([]AccountStatusItem, 0, len(idSet))
	resp := AccountsStatusResponse{
		InstanceID: instanceID,
		ObservedAt: ptr(observedAt),
		TrustState: trustState,
	}

	for accID := range idSet {
		item := AccountStatusItem{AccountID: accID}
		if d, ok := dirMap[accID]; ok {
			item.Name = d.Name
			item.Platform = d.Platform
			item.Type = d.Type
			item.Status = d.Status
			sched := d.Schedulable
			item.Schedulable = &sched
			item.GroupIDs = slices.Clone(d.GroupIDs)
			item.ExpiresAt = cloneSiteOpsValue(d.ExpiresAt)
			item.RateLimitResetAt = cloneSiteOpsValue(d.RateLimitResetAt)
			item.OverloadUntil = cloneSiteOpsValue(d.OverloadUntil)
			item.TempUnschedulableUntil = cloneSiteOpsValue(d.TempUnschedulableUntil)
			if d.ErrorSummary != "" {
				item.ErrorSummary = d.ErrorSummary
			}
			if d.Concurrency > 0 {
				v := int64(d.Concurrency)
				item.ConcMax = &v
			}
		}
		if avail != nil {
			if a, ok := avail.Accounts[accID]; ok {
				if item.Platform == "" {
					item.Platform = a.Platform
				}
				item.OpsAvailable = a.Available
				item.RateLimited = a.RateLimited
				item.Overloaded = a.Overloaded
				item.HasError = a.HasError
				if a.ErrorSummary != "" {
					item.ErrorSummary = a.ErrorSummary
				}
			}
		}
		if conc != nil {
			if c, ok := conc.Accounts[accID]; ok {
				if item.Platform == "" {
					item.Platform = c.Platform
				}
				used, maxCap, wait := c.CurrentInUse, c.MaxCapacity, c.WaitingInQueue
				item.ConcUsed = &used
				item.ConcMax = &maxCap
				item.ConcWaiting = &wait
			}
		}
		if item.Name == "" {
			item.Name = fmt.Sprintf("account#%d", accID)
		}
		resp.TotalCount++
		if item.OpsAvailable {
			resp.AvailableCount++
		}
		if item.RateLimited {
			resp.RateLimitCount++
		}
		if item.Overloaded {
			resp.OverloadCount++
		}
		if item.HasError {
			resp.ErrorCount++
		}
		items = append(items, item)
	}

	// 异常账号优先排序：报错 > 过载 > 限流 > 不可用 > 正常，组内按 ID 升序。
	sort.Slice(items, func(i, j int) bool {
		ri, rj := accountSeverityRank(items[i]), accountSeverityRank(items[j])
		if ri != rj {
			return ri < rj
		}
		return items[i].AccountID < items[j].AccountID
	})
	resp.Items = items
	return resp
}

func accountSeverityRank(item AccountStatusItem) int {
	switch {
	case item.HasError:
		return 0
	case item.Overloaded:
		return 1
	case item.RateLimited:
		return 2
	case !item.OpsAvailable:
		return 3
	default:
		return 4
	}
}

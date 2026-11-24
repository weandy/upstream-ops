package siteops

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

type MailAudienceOptions struct {
	Target          TargetEndpoint
	Scope           storage.MailScopeKey
	BatchID         uint
	AsOf, PrepareBy time.Time
	Spec            MailAudienceSpec
	// Set only by the retry endpoint, using failed items from its verified source batch.
	RetryFailed    map[int64]string
	UsedVars       []MailTemplateVar
	Seed           []byte
	Cipher         *crypto.Cipher
	FindSuppressed func(context.Context, storage.MailScopeKey, []string) (map[string]bool, error)
}

// LoadMailRetryRecipients reads the immutable source of a retry after its worker has
// acquired the new batch lease. The worker passes this map to ResolveMailAudience.
func LoadMailRetryRecipients(ctx context.Context, store MailBatchStore, scope storage.MailScopeKey, batchID uint) (map[int64]string, error) {
	batch, err := store.GetMailBatch(ctx, scope, batchID)
	if err != nil {
		return nil, err
	}
	if batch.ID != batchID || batch.InstanceID != scope.InstanceID || batch.TargetBinding != scope.TargetBinding || batch.Mode == string(MailBatchModeTest) {
		return nil, ErrMailNoRetryableItems
	}
	switch MailBatchStatus(batch.Status) {
	case MailBatchStatusCancelled, MailBatchStatusCompletedWithErrors, MailBatchStatusFailed, MailBatchStatusCompleted:
	default:
		return nil, ErrMailNoRetryableItems
	}
	params := storage.MailListParams{Scope: scope, Status: string(MailItemStatusFailed), Limit: MailMaxListLimit}
	result := map[int64]string{}
	for {
		items, err := store.ListMailItems(ctx, batchID, params)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.ID == 0 || params.BeforeID != 0 && item.ID >= params.BeforeID || item.BatchID != batchID || item.InstanceID != scope.InstanceID || item.Status != params.Status || item.UserID < 1 || item.UserID > MailMaxSafeID || len(item.EmailHash) != 64 || result[item.UserID] != "" {
				return nil, ErrMailPrepCoverageIncomplete
			}
			result[item.UserID] = item.EmailHash
			params.BeforeID = item.ID
		}
		if len(result) > MailMaxRecipients {
			return nil, ErrMailPrepLimitExceeded
		}
		if len(items) < params.Limit {
			break
		}
	}
	if len(result) == 0 {
		return nil, ErrMailNoRetryableItems
	}
	return result, nil
}

type MailAudienceResult struct {
	Preview MailAudiencePreview
	Counts  storage.MailPrepareCounts
	Rows    []storage.MailItemStageRow
}

func ValidateMailAudience(spec MailAudienceSpec) error {
	validIDs := func(ids []int64, limit int) bool {
		set := map[int64]bool{}
		for _, id := range ids {
			if id < 1 || id > MailMaxSafeID {
				return false
			}
			set[id] = true
		}
		return len(set) > 0 && len(set) <= limit
	}
	switch spec.Mode {
	case MailAudienceModeSelected:
		if !validIDs(spec.UserIDs, MailMaxSelectedUsers) {
			return ErrMailInvalidInput
		}
	case MailAudienceModeGroups:
		if !validIDs(spec.GroupIDs, MailMaxGroups) {
			return ErrMailInvalidInput
		}
	case MailAudienceModeSegment:
		if len(spec.Tags)+len(spec.ActivityStates) == 0 {
			return ErrMailInvalidInput
		}
		for _, t := range spec.Tags {
			switch t {
			case MailSegmentTagHighValueAtRisk, MailSegmentTagExpiringSoon, MailSegmentTagExpiredUnrenewed, MailSegmentTagCacheAnomaly, MailSegmentTagConcurrencyQueued, MailSegmentTagDormantHighBalance:
			default:
				return ErrMailInvalidInput
			}
		}
		for _, s := range spec.ActivityStates {
			switch s {
			case MailActivityStateDrop, MailActivityStateIdleBalance, MailActivityStateActive, MailActivityStateLow:
			default:
				return ErrMailInvalidInput
			}
		}
	case MailAudienceModeAll:
	default:
		return ErrMailInvalidInput
	}
	if spec.Mode != MailAudienceModeSelected && len(spec.UserIDs) > 0 || spec.Mode != MailAudienceModeGroups && len(spec.GroupIDs) > 0 || spec.Mode != MailAudienceModeSegment && len(spec.Tags)+len(spec.ActivityStates) > 0 {
		return ErrMailInvalidInput
	}
	return nil
}

type mailScan struct {
	preview        bool
	pages, scanned int
	estimate       bool
}

func scanMailPages[T any](ctx context.Context, scan *mailScan, fetch func(context.Context, int) (MailDirectoryPage[T], UpstreamFetchMeta, error), id func(T) int64, visit func([]T) (bool, error)) error {
	seen := map[int64]bool{}
	var previous MailDirectoryPage[T]
	for n := 1; n <= MailMaxDirectoryPages; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if scan.preview && scan.pages >= MailPreviewMaxPages {
			scan.estimate = true
			return nil
		}
		page, meta, err := fetch(ctx, n)
		if err != nil {
			if errors.Is(err, ErrRateLimited) && meta.RetryAfter != nil {
				return &backoffError{time.Now().Add(*meta.RetryAfter)}
			}
			return err
		}
		scan.pages++
		scan.scanned += len(page.Items)
		size := 100
		if page.PageSize != nil {
			size = *page.PageSize
		}
		if page.Truncated || size < 1 || size > 100 || len(page.Items) > size || page.Page != nil && *page.Page != n {
			return ErrMailPrepPageDrift
		}
		if n > 1 && (!mailMetadataEqual(previous.Total, page.Total) || !mailMetadataEqual(previous.PageSize, page.PageSize) || (previous.Page == nil) != (page.Page == nil) || (previous.HasMore == nil) != (page.HasMore == nil)) {
			return ErrMailPrepPageDrift
		}
		for _, v := range page.Items {
			key := id(v)
			if key <= 0 || key > MailMaxSafeID || seen[key] {
				return ErrMailPrepPageDrift
			}
			seen[key] = true
		}
		more := len(page.Items) == size
		if page.Total != nil {
			if *page.Total < int64(len(seen)) {
				return ErrMailPrepPageDrift
			}
			more = int64(len(seen)) < *page.Total
		}
		if page.HasMore != nil {
			if page.Total != nil && *page.HasMore != more {
				return ErrMailPrepPageDrift
			}
			more = *page.HasMore
		}
		if more && len(page.Items) != size {
			return ErrMailPrepPageDrift
		}
		stop, err := visit(page.Items)
		if err != nil {
			return err
		}
		if stop || !more {
			return nil
		}
		if n == MailMaxDirectoryPages || len(seen) >= MailMaxDirectoryRows {
			if page.Total == nil {
				return ErrMailPrepPageDrift
			}
			return ErrMailPrepLimitExceeded
		}
		previous = page
	}
	return ErrMailPrepLimitExceeded
}
func mailMetadataEqual[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

type mailSegmentNeeds struct{ risk, cache, dormant, expired, expiring, concurrency, activity bool }

func segmentNeeds(s MailAudienceSpec) mailSegmentNeeds {
	activity := len(s.ActivityStates) > 0
	return mailSegmentNeeds{activity || slices.Contains(s.Tags, MailSegmentTagHighValueAtRisk), slices.Contains(s.Tags, MailSegmentTagCacheAnomaly), activity || slices.Contains(s.Tags, MailSegmentTagDormantHighBalance), slices.Contains(s.Tags, MailSegmentTagExpiredUnrenewed), slices.Contains(s.Tags, MailSegmentTagExpiringSoon), slices.Contains(s.Tags, MailSegmentTagConcurrencyQueued), activity}
}
func (n mailSegmentNeeds) subscriptions() bool { return n.risk || n.expired || n.expiring || n.dormant }
func mailSub(sub MailDirectorySubscription) SanitizedSubscription {
	return SanitizedSubscription{ID: sub.ID, UserID: sub.UserID, GroupID: sub.GroupID, Status: sub.Status, StartsAt: sub.StartsAt, ExpiresAt: sub.ExpiresAt, RevokedAt: sub.RevokedAt, Group: SanitizedGroupInfo{ID: sub.GroupID, Name: sub.GroupName}}
}
func mailSubscription(s []SanitizedSubscription, groups []int64, now time.Time) *SanitizedSubscription {
	var best *SanitizedSubscription
	for i := range s {
		row := &s[i]
		if !activeSub(*row, now) || len(groups) > 0 && !slices.Contains(groups, row.GroupID) {
			continue
		}
		if best == nil || row.ID < best.ID {
			best = row
		}
	}
	return best
}
func mailPersonalVars(u MailDirectoryUser, sub *SanitizedSubscription, now time.Time, used []MailTemplateVar) MailPersonalVars {
	var v MailPersonalVars
	for _, key := range used {
		switch key {
		case MailTemplateVarBalanceUSD:
			v.BalanceUSD = "—"
			if u.BalanceUSD != nil {
				v.BalanceUSD = fmt.Sprintf("$%.2f", *u.BalanceUSD)
			}
		case MailTemplateVarPlanName:
			v.PlanName = "按量余额"
			if sub != nil {
				v.PlanName = sub.Group.Name
			}
		case MailTemplateVarExpiresDate:
			v.ExpiresDate = "—"
			if sub != nil {
				v.ExpiresDate = sub.ExpiresAt.In(locUTC8).Format(time.DateOnly)
			}
		case MailTemplateVarExpiresInDays:
			v.ExpiresInDays = "—"
			if sub != nil {
				v.ExpiresInDays = strconv.Itoa(int(math.Ceil(sub.ExpiresAt.Sub(now).Hours() / 24)))
			}
		}
	}
	return v
}

type mailSegmentData struct {
	needs       mailSegmentNeeds
	quick       map[int64]SanitizedUserQuickUsage
	concurrency SanitizedUserConcurrencyMap
}

func mailDomainError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrRateLimited) {
		return err
	}
	return ErrMailPrepCoverageIncomplete
}
func (d *mailSegmentData) load(ctx context.Context, c MailAudienceClient, o MailAudienceOptions, users []MailDirectoryUser) error {
	n := d.needs
	quick := n.expired || n.dormant
	calls := 0
	if quick {
		calls += (len(users) + 99) / 100
	}
	if n.concurrency {
		calls++
	}
	windows := 0
	if n.risk || n.cache {
		windows = 2
	} else if n.dormant {
		windows = 1
	}
	calls += len(users) * windows
	available, _, _ := c.BudgetHeadroomForAnalytics(o.Target.InstanceID, o.Target.AuditBudgetPerDay)
	if calls > available {
		return ErrMailPrepCoverageIncomplete
	}
	if n.concurrency {
		v, _, err := c.FetchUserConcurrency(ctx, o.Target)
		if err != nil {
			return mailDomainError(err)
		}
		d.concurrency = v
	}
	d.quick = map[int64]SanitizedUserQuickUsage{}
	if quick {
		for start := 0; start < len(users); start += 100 {
			var ids []int64
			for _, u := range users[start:min(len(users), start+100)] {
				ids = append(ids, u.ID)
			}
			v, _, err := c.FetchUsersUsageBatch(ctx, o.Target, ids)
			if err != nil {
				return mailDomainError(err)
			}
			for _, id := range ids {
				row, ok := v[id]
				if !ok || n.dormant && val(row.Today) < 0 || n.expired && val(row.Recent) < 0 {
					return ErrMailPrepCoverageIncomplete
				}
				d.quick[id] = row
			}
		}
	}
	return nil
}
func (d *mailSegmentData) evaluate(ctx context.Context, c MailAudienceClient, o MailAudienceOptions, u MailDirectoryUser, subs []SanitizedSubscription) (MailAudienceSample, bool, error) {
	n := d.needs
	q := d.quick[u.ID]
	row := AnalyticsUserRow{UserID: u.ID, Status: u.Status, Role: u.Role, CreatedAt: u.CreatedAt, BalanceUSD: u.BalanceUSD, TodayActualUSD: q.Today, RecentAggregateActualUSD: q.Recent}
	if n.concurrency {
		v, ok := d.concurrency[u.ID]
		if !ok || val(v.WaitingInQueue) < 0 || val(v.MaxCapacity) < 0 || val(v.CurrentInUse) < 0 {
			return MailAudienceSample{}, false, ErrMailPrepCoverageIncomplete
		}
		row.LiveConcurrency = &v
	}
	for _, s := range subs {
		if activeSub(s, o.AsOf) {
			row.ActiveSubCount++
		}
	}
	if n.risk || n.dormant || n.cache {
		if u.CreatedAt == nil || u.CreatedAt.IsZero() || u.CreatedAt.After(o.AsOf) || n.dormant && u.BalanceUSD == nil {
			return MailAudienceSample{}, false, ErrMailPrepCoverageIncomplete
		}
		today := daySH(o.AsOf)
		first := daySH(*u.CreatedAt).AddDate(0, 0, -1)
		if floor := today.AddDate(-10, 0, 0); first.Before(floor) {
			first = floor
		}
		week := today.AddDate(0, 0, -6)
		if week.Before(first) {
			week = first
		}
		for idx, start := range []time.Time{first, week} {
			if idx == 0 && !(n.risk || n.cache) {
				continue
			}
			r := dateRange(start, today)
			stats, _, err := c.FetchUsageStats(ctx, o.Target, AnalyticsStatsQuery{AnalyticsDateRange: r, UserID: u.ID})
			if err != nil {
				return MailAudienceSample{}, false, mailDomainError(err)
			}
			if stats == nil || val(stats.Requests) < 0 || (n.risk || n.dormant) && val(stats.Actual) < 0 || n.risk && val(stats.Requests) >= 20 && val(stats.DurationMS) < 0 || n.cache && val(stats.Requests) >= 20 && (stats.Tokens.TokenQuality != QualityTrusted || stats.Tokens.EffectiveInput == nil || val(stats.Tokens.EffectiveInput) >= 100000 && stats.Tokens.CacheReadHitPct == nil) {
				return MailAudienceSample{}, false, ErrMailPrepCoverageIncomplete
			}
			w := &UsageWindowView{StartDate: r.StartDate, Requests: stats.Requests, ActualUSD: stats.Actual, AverageDurationMS: stats.DurationMS, Cache: stats.Tokens}
			if idx == 0 {
				row.AllTime = w
			} else {
				row.Last7d = w
			}
		}
	}
	tags := userTags(row, subs, n.subscriptions(), o.AsOf)
	sample := MailAudienceSample{}
	tagMatch := len(o.Spec.Tags) == 0
	for _, t := range tags {
		tag := MailSegmentTag(t.Key)
		sample.Tags = append(sample.Tags, tag)
		tagMatch = tagMatch || slices.Contains(o.Spec.Tags, tag)
	}
	if n.activity {
		sample.ActivityState = MailActivityState(userActivityState(row, tags))
	}
	return sample, tagMatch && (!n.activity || slices.Contains(o.Spec.ActivityStates, sample.ActivityState)), nil
}

// ResolveMailAudience runs in the preparing worker. BatchID zero selects a bounded,
// read-only preview and produces only masked samples. No row is returned on failure.
func ResolveMailAudience(ctx context.Context, c MailAudienceClient, o MailAudienceOptions) (MailAudienceResult, error) {
	var result MailAudienceResult
	preview := o.BatchID == 0
	if c == nil || o.FindSuppressed == nil || len(o.Seed) != 32 || o.AsOf.IsZero() {
		return result, ErrMailInvalidInput
	}
	if o.RetryFailed == nil {
		if err := ValidateMailAudience(o.Spec); err != nil {
			return result, err
		}
	} else if preview || len(o.RetryFailed) == 0 || len(o.RetryFailed) > MailMaxRecipients || o.Spec.Mode != MailAudienceMode(MailBatchModeRetryFailed) {
		return result, ErrMailInvalidInput
	}
	end := o.AsOf.Add(MailPrepareTimeout)
	if !o.PrepareBy.IsZero() && o.PrepareBy.Before(end) {
		end = o.PrepareBy
	}
	if preview {
		end = time.Now().Add(MailPreviewTimeout)
	}
	ctx, cancel := context.WithDeadline(context.WithValue(ctx, mailPrivateRequestKey{}, true), end)
	defer cancel()
	scan := mailScan{preview: preview}
	result, err := resolveMail(ctx, c, o, &scan)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			if o.Spec.Mode == MailAudienceModeSegment {
				return MailAudienceResult{}, ErrMailPrepCoverageIncomplete
			}
			return MailAudienceResult{}, ErrMailPrepDeadlineExceeded
		}
		return MailAudienceResult{}, err
	}
	result.Counts.Pages, result.Counts.Scanned = scan.pages, scan.scanned
	result.Preview.IsEstimate = scan.estimate
	return result, nil
}
func resolveMail(ctx context.Context, c MailAudienceClient, o MailAudienceOptions, scan *mailScan) (MailAudienceResult, error) {
	var result MailAudienceResult
	needs := segmentNeeds(o.Spec)
	subs := map[int64][]SanitizedSubscription{}
	wantSubs := needs.subscriptions() || o.Spec.Mode == MailAudienceModeGroups
	for _, v := range o.UsedVars {
		wantSubs = wantSubs || v == MailTemplateVarPlanName || v == MailTemplateVarExpiresDate || v == MailTemplateVarExpiresInDays
	}
	if wantSubs {
		err := scanMailPages(ctx, scan, func(ctx context.Context, n int) (MailDirectoryPage[MailDirectorySubscription], UpstreamFetchMeta, error) {
			return c.FetchMailSubscriptionsPage(ctx, o.Target, n)
		}, func(s MailDirectorySubscription) int64 { return s.ID }, func(page []MailDirectorySubscription) (bool, error) {
			for _, r := range page {
				if r.UserID < 1 || r.GroupID < 1 || r.Status == "" || r.Status == "active" && (r.StartsAt == nil || r.ExpiresAt == nil) || needs.expired && ((r.Status == "cancelled" || r.Status == "revoked") && r.RevokedAt == nil || (r.Status == "expired" && r.ExpiresAt == nil)) {
					return false, ErrMailPrepCoverageIncomplete
				}
				subs[r.UserID] = append(subs[r.UserID], mailSub(r))
			}
			return false, nil
		})
		if err != nil {
			return result, err
		}
	}
	remaining := map[int64]bool{}
	for _, id := range o.Spec.UserIDs {
		remaining[id] = true
	}
	for id := range o.RetryFailed {
		remaining[id] = true
	}
	targeted := o.Spec.Mode == MailAudienceModeSelected || o.RetryFailed != nil
	project := func(u MailDirectoryUser, email string) (MailAddressProjection, error) {
		if targeted && !remaining[u.ID] {
			return MailAddressProjection{}, nil
		}
		sub := mailSubscription(subs[u.ID], o.Spec.GroupIDs, o.AsOf)
		vars := mailPersonalVars(u, sub, o.AsOf, o.UsedVars)
		return ProjectMailItem(o.Cipher, o.Scope, o.Seed, o.BatchID, u.ID, email, vars)
	}
	var users []MailDirectoryUser
	err := scanMailPages(ctx, scan, func(ctx context.Context, n int) (MailDirectoryPage[MailDirectoryUser], UpstreamFetchMeta, error) {
		return c.FetchMailUsersPage(ctx, o.Target, n, project)
	}, func(u MailDirectoryUser) int64 { return u.ID }, func(page []MailDirectoryUser) (bool, error) {
		for _, u := range page {
			if targeted && !remaining[u.ID] {
				continue
			}
			delete(remaining, u.ID)
			if u.Status != "active" || u.Role != "user" && u.Role != "member" || u.Address.EmailHash == "" || o.RetryFailed != nil && o.RetryFailed[u.ID] != u.Address.EmailHash || o.Spec.Mode == MailAudienceModeGroups && mailSubscription(subs[u.ID], o.Spec.GroupIDs, o.AsOf) == nil {
				result.Counts.Excluded++
				continue
			}
			users = append(users, u)
		}
		return targeted && len(remaining) == 0, nil
	})
	if err != nil {
		return result, err
	}
	if !scan.estimate {
		result.Counts.Excluded += len(remaining)
	}
	slices.SortFunc(users, func(a, b MailDirectoryUser) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	data := mailSegmentData{needs: needs}
	if o.Spec.Mode == MailAudienceModeSegment && len(users) > 0 {
		if err := data.load(ctx, c, o, users); err != nil {
			return result, err
		}
	}
	seen := map[string]bool{}
	for start := 0; start < len(users); start += 100 {
		chunk := users[start:min(len(users), start+100)]
		hashes := make([]string, 0, len(chunk))
		for _, u := range chunk {
			hashes = append(hashes, u.Address.EmailHash)
		}
		suppressed, err := o.FindSuppressed(ctx, o.Scope, hashes)
		if err != nil {
			return result, err
		}
		for _, u := range chunk {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			sample := MailAudienceSample{}
			if o.Spec.Mode == MailAudienceModeSegment {
				var ok bool
				sample, ok, err = data.evaluate(ctx, c, o, u, subs[u.ID])
				if err != nil {
					return result, err
				}
				if !ok {
					result.Counts.Excluded++
					continue
				}
			}
			a := u.Address
			if suppressed[a.EmailHash] {
				result.Counts.Suppressed++
				continue
			}
			if seen[a.EmailHash] {
				result.Counts.Duplicate++
				continue
			}
			seen[a.EmailHash] = true
			if len(seen) > MailMaxRecipients {
				return result, ErrMailPrepLimitExceeded
			}
			result.Preview.RecipientCount++
			if !scan.preview {
				result.Rows = append(result.Rows, storage.MailItemStageRow{UserID: u.ID, EmailHash: a.EmailHash, EmailMasked: a.EmailMasked, PayloadCipher: a.PayloadCipher})
				continue
			}
			if len(result.Preview.Samples) < MailPreviewMaxSamples {
				sample.MailCandidate = MailCandidate{UserID: u.ID, EmailMasked: a.EmailMasked}
				if sub := mailSubscription(subs[u.ID], o.Spec.GroupIDs, o.AsOf); sub != nil {
					sample.PlanName = sub.Group.Name
				}
				result.Preview.Samples = append(result.Preview.Samples, sample)
			}
		}
	}
	result.Preview.ExcludedCount, result.Preview.DuplicateCount, result.Preview.SuppressedCount = result.Counts.Excluded, result.Counts.Duplicate, result.Counts.Suppressed
	return result, ctx.Err()
}

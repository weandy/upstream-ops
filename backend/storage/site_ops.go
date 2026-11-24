package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var (
	ErrSiteOpsNotFound           = errors.New("site-ops instance not found")
	ErrSiteOpsGenerationMismatch = errors.New("site-ops instance generation mismatch")
)

type SiteOpsRuntimePatch struct {
	CapabilitiesJSON *string
	LastReachability *string
	LastStatus       *string
	LastHealthScore  **int
	LastVersion      *string
	LastObservedAt   *time.Time
	LastVerifiedAt   *time.Time
	BackoffUntil     **time.Time
	LastErrorCode    *string
	LastErrorSummary *string
}

func (p SiteOpsRuntimePatch) columns() map[string]any {
	values := make(map[string]any)
	setSiteOpsColumn(values, "capabilities_json", p.CapabilitiesJSON)
	setSiteOpsColumn(values, "last_reachability", p.LastReachability)
	setSiteOpsColumn(values, "last_status", p.LastStatus)
	setSiteOpsColumn(values, "last_health_score", p.LastHealthScore)
	setSiteOpsColumn(values, "last_version", p.LastVersion)
	setSiteOpsColumn(values, "last_observed_at", p.LastObservedAt)
	setSiteOpsColumn(values, "last_verified_at", p.LastVerifiedAt)
	setSiteOpsColumn(values, "backoff_until", p.BackoffUntil)
	setSiteOpsColumn(values, "last_error_code", p.LastErrorCode)
	setSiteOpsColumn(values, "last_error_summary", p.LastErrorSummary)
	return values
}

func setSiteOpsColumn[T any](values map[string]any, column string, value *T) {
	if value != nil {
		values[column] = *value
	}
}

type SiteOpsStore struct {
	db *gorm.DB
}

func NewSiteOpsStore(db *gorm.DB) *SiteOpsStore {
	// GORM's shared logger can interpolate bound data into slow or failed SQL.
	return &SiteOpsStore{db: db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})}
}

func (s *SiteOpsStore) ListInstances(ctx context.Context) ([]SiteOpsInstance, error) {
	var instances []SiteOpsInstance
	err := s.db.WithContext(ctx).Order("sort_order ASC, id ASC").Find(&instances).Error
	return instances, err
}

func (s *SiteOpsStore) GetInstance(ctx context.Context, id uint) (*SiteOpsInstance, error) {
	return findSiteOpsInstance(s.db.WithContext(ctx), id)
}

func findSiteOpsInstance(db *gorm.DB, id uint) (*SiteOpsInstance, error) {
	var instance SiteOpsInstance
	err := db.Where("id = ?", id).Take(&instance).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSiteOpsNotFound
	}
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

func (s *SiteOpsStore) CreateInstance(ctx context.Context, inst *SiteOpsInstance) error {
	created := *inst
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		// GORM applies the model's true default to a zero-value Enabled field.
		if !inst.Enabled {
			created.Enabled = false
			return tx.Model(&created).UpdateColumn("enabled", false).Error
		}
		return nil
	})
	if err == nil {
		*inst = created
	}
	return err
}

func (s *SiteOpsStore) UpdateInstance(ctx context.Context, inst *SiteOpsInstance) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := findSiteOpsInstance(tx.Clauses(clause.Locking{Strength: "UPDATE"}), inst.ID)
		if err != nil {
			return err
		}
		if inst.Generation != current.Generation+1 {
			return ErrSiteOpsGenerationMismatch
		}
		fields := []string{"name", "slug", "base_url", "api_key_cipher", "api_key_hint", "key_kind", "cred_gen", "generation", "proxy_enabled", "enabled", "sort_order", "poll_interval_seconds", "audit_budget_per_day"}
		if inst.BaseURL != current.BaseURL || inst.CredGen != current.CredGen || inst.KeyKind != current.KeyKind || inst.ProxyEnabled != current.ProxyEnabled {
			fields = append(fields, "capabilities_json", "last_reachability", "last_status", "last_health_score", "last_version", "last_observed_at", "last_verified_at", "backoff_until", "last_error_code", "last_error_summary")
		}
		return tx.Model(&SiteOpsInstance{}).Where("id = ?", inst.ID).Select(fields).Updates(inst).Error
	})
}

func (s *SiteOpsStore) DeleteInstance(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&SiteOpsInstance{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrSiteOpsNotFound
		}
		if err := tx.Where("instance_id = ?", id).Delete(&SiteOpsSnapshot{}).Error; err != nil {
			return err
		}
		if err := tx.Where("instance_id = ?", id).Delete(&SiteOpsGroupSnapshot{}).Error; err != nil {
			return err
		}
		if err := tx.Where("instance_id = ?", id).Delete(&SiteOpsAlertDelivery{}).Error; err != nil {
			return err
		}
		if err := tx.Where("instance_id = ?", id).Delete(&SiteOpsEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("instance_id = ?", id).Delete(&SiteOpsAccountWindowSample{}).Error; err != nil {
			return err
		}
		if err := tx.Where("instance_id = ?", id).Delete(&SiteOpsAccountCost{}).Error; err != nil {
			return err
		}
		return tx.Where("instance_id = ?", id).Delete(&SiteOpsReferencePrice{}).Error
	})
}

func (s *SiteOpsStore) collectionTx(ctx context.Context, id uint, generation int64, patch SiteOpsRuntimePatch, columns map[string]any, work func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inst SiteOpsInstance
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("generation", "enabled", "backoff_until").Where("id = ?", id).Take(&inst).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrSiteOpsNotFound
		}
		if err != nil {
			return err
		}
		if inst.Generation != generation || !inst.Enabled {
			return ErrSiteOpsGenerationMismatch
		}
		if patch.BackoffUntil != nil && inst.BackoffUntil != nil {
			proposed := *patch.BackoffUntil
			if (proposed == nil && inst.BackoffUntil.After(time.Now())) || (proposed != nil && proposed.Before(*inst.BackoffUntil)) {
				delete(columns, "backoff_until")
			}
		}
		return work(tx.Session(&gorm.Session{SkipDefaultTransaction: true}))
	})
}

func (s *SiteOpsStore) CommitCollection(ctx context.Context, id uint, generation int64, patch SiteOpsRuntimePatch, snap *SiteOpsSnapshot, groups []SiteOpsGroupSnapshot) error {
	var snapshot SiteOpsSnapshot
	if snap != nil {
		snapshot = *snap
		snapshot.ID, snapshot.InstanceID = 0, id
		snapshot.ObservedAt, snapshot.VerifiedAt = snap.ObservedAt.UTC(), snap.VerifiedAt.UTC()
	}
	rows := append([]SiteOpsGroupSnapshot(nil), groups...)
	for i := range rows {
		rows[i].ID, rows[i].InstanceID = 0, id
		rows[i].ObservedAt = rows[i].ObservedAt.UTC()
	}
	columns := patch.columns()
	return s.collectionTx(ctx, id, generation, patch, columns, func(tx *gorm.DB) error {
		if len(columns) > 0 {
			if err := tx.Model(&SiteOpsInstance{}).Where("id = ?", id).Updates(columns).Error; err != nil {
				return err
			}
		}
		if snap != nil {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "instance_id"}, {Name: "observed_at"}}, UpdateAll: true,
			}).Create(&snapshot).Error; err != nil {
				return err
			}
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "instance_id"}, {Name: "group_id"}, {Name: "observed_at"}}, UpdateAll: true,
		}).CreateInBatches(&rows, 50).Error
	})
}

func (s *SiteOpsStore) TouchSnapshotVerified(ctx context.Context, id uint, generation int64, verifiedAt time.Time, patch SiteOpsRuntimePatch) error {
	verifiedAt = verifiedAt.UTC()
	columns := patch.columns()
	delete(columns, "last_observed_at")
	columns["last_verified_at"] = verifiedAt
	return s.collectionTx(ctx, id, generation, patch, columns, func(tx *gorm.DB) error {
		latest := tx.Model(&SiteOpsSnapshot{}).Select("id").Where("instance_id = ?", id).Order("observed_at DESC").Limit(1)
		// Fetch the ID first so this also works with MySQL's same-table update restriction.
		var ids []uint
		if err := latest.Pluck("id", &ids).Error; err != nil || len(ids) == 0 {
			return err
		}
		if err := tx.Model(&SiteOpsSnapshot{}).Where("id = ?", ids[0]).Update("verified_at", verifiedAt).Error; err != nil {
			return err
		}
		return tx.Model(&SiteOpsInstance{}).Where("id = ?", id).Updates(columns).Error
	})
}

func (s *SiteOpsStore) GetLatestSnapshot(ctx context.Context, id uint) (*SiteOpsSnapshot, error) {
	var snapshot SiteOpsSnapshot
	err := s.db.WithContext(ctx).Where("instance_id = ?", id).Order("observed_at DESC").Take(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *SiteOpsStore) GetLatestSnapshotsByInstances(ctx context.Context, ids []uint) (map[uint]SiteOpsSnapshot, error) {
	out := make(map[uint]SiteOpsSnapshot, len(ids))
	for start := 0; start < len(ids); start += 500 {
		db := s.db.WithContext(ctx)
		latest := db.Model(&SiteOpsSnapshot{}).Select("instance_id, MAX(observed_at) AS observed_at").
			Where("instance_id IN ?", ids[start:min(start+500, len(ids))]).Group("instance_id")
		var snapshots []SiteOpsSnapshot
		err := db.Model(&SiteOpsSnapshot{}).Select("site_ops_snapshots.*").
			Joins("JOIN (?) AS latest ON latest.instance_id = site_ops_snapshots.instance_id AND latest.observed_at = site_ops_snapshots.observed_at", latest).
			Find(&snapshots).Error
		if err != nil {
			return nil, err
		}
		for _, snap := range snapshots {
			out[snap.InstanceID] = snap
		}
	}
	return out, nil
}

func (s *SiteOpsStore) ListSnapshotsSince(ctx context.Context, id uint, since time.Time, limit int) ([]SiteOpsSnapshot, error) {
	var snapshots []SiteOpsSnapshot
	query := s.db.WithContext(ctx).Where("instance_id = ? AND observed_at >= ?", id, since.UTC()).Order("observed_at ASC").Omit("detail_json")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&snapshots).Error
	return snapshots, err
}

func (s *SiteOpsStore) GetLatestGroupSnapshots(ctx context.Context, id uint) ([]SiteOpsGroupSnapshot, error) {
	db := s.db.WithContext(ctx)
	latest := db.Model(&SiteOpsGroupSnapshot{}).Select("MAX(observed_at)").Where("instance_id = ?", id)
	var groups []SiteOpsGroupSnapshot
	err := db.Where("instance_id = ? AND observed_at = (?)", id, latest).Order("group_id ASC").Find(&groups).Error
	return groups, err
}

func (s *SiteOpsStore) DeleteSnapshotsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	return s.deleteBeforeChunkedByColumn(ctx, &SiteOpsSnapshot{}, "observed_at", cutoff, batchSize, maxRows, maxDuration)
}

func (s *SiteOpsStore) DeleteGroupSnapshotsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	return s.deleteBeforeChunkedByColumn(ctx, &SiteOpsGroupSnapshot{}, "observed_at", cutoff, batchSize, maxRows, maxDuration)
}

func (s *SiteOpsStore) ListActiveEvents(ctx context.Context, instanceID uint) ([]SiteOpsEvent, error) {
	var events []SiteOpsEvent
	err := s.db.WithContext(ctx).
		Where("instance_id = ? AND state = ?", instanceID, "firing").
		Order("fired_at DESC, id DESC").
		Find(&events).Error
	return events, err
}

func (s *SiteOpsStore) UpsertEvent(ctx context.Context, evt *SiteOpsEvent) error {
	if evt == nil {
		return errors.New("site-ops event is nil")
	}
	row := *evt
	if !row.FiredAt.IsZero() {
		row.FiredAt = row.FiredAt.UTC()
	}
	if row.ResolvedAt != nil {
		resolved := row.ResolvedAt.UTC()
		row.ResolvedAt = &resolved
	}
	if row.ID == 0 {
		if !row.CreatedAt.IsZero() {
			row.CreatedAt = row.CreatedAt.UTC()
		}
		if !row.UpdatedAt.IsZero() {
			row.UpdatedAt = row.UpdatedAt.UTC()
		}
		if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
			return err
		}
		*evt = row
		return nil
	}
	now := time.Now().UTC()
	row.UpdatedAt = now
	err := s.db.WithContext(ctx).Model(&SiteOpsEvent{}).
		Where("id = ?", row.ID).
		Select("instance_id", "severity", "rule_key", "resource_key", "required_domains_json", "state", "consecutive_hits", "title", "summary", "detail_json", "fired_at", "resolved_at", "updated_at").
		Updates(&row).Error
	if err == nil {
		evt.FiredAt = row.FiredAt
		evt.ResolvedAt = row.ResolvedAt
		evt.UpdatedAt = row.UpdatedAt
	}
	return err
}

func (s *SiteOpsStore) ListEvents(ctx context.Context, instanceID uint, state, severity string, limit int) ([]SiteOpsEvent, error) {
	if limit <= 0 {
		limit = 100
	} else if limit > 500 {
		limit = 500
	}
	query := s.db.WithContext(ctx).Where("instance_id = ?", instanceID)
	if state = strings.TrimSpace(state); state != "" {
		query = query.Where("state = ?", state)
	}
	if severity = strings.TrimSpace(severity); severity != "" {
		query = query.Where("severity = ?", severity)
	}
	var events []SiteOpsEvent
	err := query.Order("fired_at DESC, id DESC").Limit(limit).Find(&events).Error
	return events, err
}

func normalizeDeliveryTimes(d *SiteOpsAlertDelivery) SiteOpsAlertDelivery {
	row := *d
	if row.CooldownUntil != nil {
		t := row.CooldownUntil.UTC()
		row.CooldownUntil = &t
	}
	if row.NextRetryAt != nil {
		t := row.NextRetryAt.UTC()
		row.NextRetryAt = &t
	}
	if row.SentAt != nil {
		t := row.SentAt.UTC()
		row.SentAt = &t
	}
	if !row.CreatedAt.IsZero() {
		row.CreatedAt = row.CreatedAt.UTC()
	}
	if !row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.UpdatedAt.UTC()
	}
	return row
}

func (s *SiteOpsStore) CreateDelivery(ctx context.Context, d *SiteOpsAlertDelivery) error {
	if d == nil {
		return errors.New("site-ops alert delivery is nil")
	}
	row := normalizeDeliveryTimes(d)
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	*d = row
	return nil
}

func (s *SiteOpsStore) HasActiveCooldown(ctx context.Context, instanceID, notifyChannelID uint, cooldownKey string, now time.Time) (bool, error) {
	cooldownKey = strings.TrimSpace(cooldownKey)
	if cooldownKey == "" {
		return false, nil
	}
	var count int64
	err := s.db.WithContext(ctx).Model(&SiteOpsAlertDelivery{}).
		Where("instance_id = ? AND notify_channel_id = ? AND cooldown_key = ? AND cooldown_until IS NOT NULL AND cooldown_until > ? AND status IN ?",
			instanceID, notifyChannelID, cooldownKey, now.UTC(), []string{"pending", "sent", "failed"}).
		Limit(1).
		Count(&count).Error
	return count > 0, err
}

func (s *SiteOpsStore) ListDeliveriesByEventIDs(ctx context.Context, eventIDs []uint) (map[uint][]SiteOpsAlertDelivery, error) {
	out := make(map[uint][]SiteOpsAlertDelivery, len(eventIDs))
	if len(eventIDs) == 0 {
		return out, nil
	}
	for start := 0; start < len(eventIDs); start += 500 {
		var rows []SiteOpsAlertDelivery
		err := s.db.WithContext(ctx).
			Where("event_id IN ?", eventIDs[start:min(start+500, len(eventIDs))]).
			Order("id ASC").
			Find(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.EventID] = append(out[row.EventID], row)
		}
	}
	return out, nil
}

func (s *SiteOpsStore) ListDueDeliveries(ctx context.Context, now time.Time, limit int) ([]SiteOpsAlertDelivery, error) {
	if limit <= 0 {
		limit = 100
	} else if limit > 500 {
		limit = 500
	}
	var rows []SiteOpsAlertDelivery
	err := s.db.WithContext(ctx).
		Where("status IN ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", []string{"pending", "failed"}, now.UTC()).
		Order("next_retry_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (s *SiteOpsStore) UpdateDelivery(ctx context.Context, d *SiteOpsAlertDelivery) error {
	if d == nil || d.ID == 0 {
		return errors.New("site-ops alert delivery id is required")
	}
	row := normalizeDeliveryTimes(d)
	row.UpdatedAt = time.Now().UTC()
	err := s.db.WithContext(ctx).Model(&SiteOpsAlertDelivery{}).
		Where("id = ?", row.ID).
		Select("status", "attempts", "next_retry_at", "last_error", "sent_at", "cooldown_until", "updated_at").
		Updates(&row).Error
	if err == nil {
		d.CooldownUntil = row.CooldownUntil
		d.NextRetryAt = row.NextRetryAt
		d.SentAt = row.SentAt
		d.UpdatedAt = row.UpdatedAt
	}
	return err
}

func (s *SiteOpsStore) DeleteEventsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	return s.deleteBeforeChunkedByColumn(ctx, &SiteOpsEvent{}, "created_at", cutoff, batchSize, maxRows, maxDuration)
}

func (s *SiteOpsStore) DeleteDeliveriesBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	return s.deleteBeforeChunkedByColumn(ctx, &SiteOpsAlertDelivery{}, "created_at", cutoff, batchSize, maxRows, maxDuration)
}

type windowSampleKey struct {
	instanceID         uint
	accountID          int64
	source             string
	windowKey          string
	upstreamObservedAt int64
}

func (s *SiteOpsStore) UpsertWindowSamples(ctx context.Context, samples []SiteOpsAccountWindowSample) error {
	if len(samples) == 0 {
		return nil
	}
	now := time.Now().UTC()
	deduped := make([]SiteOpsAccountWindowSample, 0, len(samples))
	indexByKey := make(map[windowSampleKey]int, len(samples))
	for _, sample := range samples {
		row := sample
		row.ID = 0
		row.Source = strings.TrimSpace(row.Source)
		if row.Source == "" {
			row.Source = "passive_cache"
		}
		row.WindowKey = strings.TrimSpace(row.WindowKey)
		row.UpstreamObservedAt = row.UpstreamObservedAt.UTC()
		if row.ObservedAt.IsZero() {
			row.ObservedAt = now
		} else {
			row.ObservedAt = row.ObservedAt.UTC()
		}
		if row.ResetsAt != nil {
			t := row.ResetsAt.UTC()
			row.ResetsAt = &t
		}
		key := windowSampleKey{
			instanceID:         row.InstanceID,
			accountID:          row.AccountID,
			source:             row.Source,
			windowKey:          row.WindowKey,
			upstreamObservedAt: row.UpstreamObservedAt.UnixNano(),
		}
		if idx, exists := indexByKey[key]; exists {
			deduped[idx] = row
			continue
		}
		indexByKey[key] = len(deduped)
		deduped = append(deduped, row)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "instance_id"},
				{Name: "account_id"},
				{Name: "source"},
				{Name: "window_key"},
				{Name: "upstream_observed_at"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"burn_rate_per_hour",
				"eta_seconds",
				"predicted_end_util",
				"margin_hours",
				"forecast_status",
				"confidence",
				"observed_at",
			}),
		}).CreateInBatches(&deduped, 100).Error
	})
}

func (s *SiteOpsStore) ListWindowSamples(ctx context.Context, instanceID uint, accountID int64, windowKey, source string, since time.Time, limit int) ([]SiteOpsAccountWindowSample, error) {
	if limit <= 0 {
		limit = 500
	} else if limit > 2000 {
		limit = 2000
	}
	query := s.db.WithContext(ctx).Where("instance_id = ? AND account_id = ? AND observed_at >= ?", instanceID, accountID, since.UTC())
	if windowKey = strings.TrimSpace(windowKey); windowKey != "" {
		query = query.Where("window_key = ?", windowKey)
	}
	if source = strings.TrimSpace(source); source != "" {
		query = query.Where("source = ?", source)
	}
	var rows []SiteOpsAccountWindowSample
	err := query.Order("observed_at ASC, upstream_observed_at ASC, id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (s *SiteOpsStore) ListLatestWindowSamplesByInstance(ctx context.Context, instanceID uint, since time.Time) ([]SiteOpsAccountWindowSample, error) {
	var rows []SiteOpsAccountWindowSample
	err := s.db.WithContext(ctx).
		Where("instance_id = ? AND observed_at >= ?", instanceID, since.UTC()).
		Order("observed_at ASC, upstream_observed_at ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func (s *SiteOpsStore) DeleteWindowSamplesBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	return s.deleteBeforeChunkedByColumn(ctx, &SiteOpsAccountWindowSample{}, "observed_at", cutoff, batchSize, maxRows, maxDuration)
}

func (s *SiteOpsStore) UpsertAccountCost(ctx context.Context, cost *SiteOpsAccountCost) error {
	if cost == nil {
		return errors.New("site-ops account cost is nil")
	}
	row := *cost
	row.ID = 0
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = time.Now().UTC()
	} else {
		row.UpdatedAt = row.UpdatedAt.UTC()
	}
	var stored SiteOpsAccountCost
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "instance_id"}, {Name: "account_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"monthly_cost_usd", "note", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
		return tx.Where("instance_id = ? AND account_id = ?", row.InstanceID, row.AccountID).Take(&stored).Error
	})
	if err == nil {
		*cost = stored
	}
	return err
}

func (s *SiteOpsStore) ListAccountCosts(ctx context.Context, instanceID uint) ([]SiteOpsAccountCost, error) {
	var rows []SiteOpsAccountCost
	err := s.db.WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Order("account_id ASC").
		Find(&rows).Error
	return rows, err
}

func (s *SiteOpsStore) deleteBeforeChunkedByColumn(ctx context.Context, model any, column string, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	if batchSize <= 0 || batchSize > 500 {
		batchSize = 500
	}
	if maxRows <= 0 {
		maxRows = 10000
	}
	if maxDuration <= 0 {
		maxDuration = 2 * time.Second
	}
	deadline, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()
	batchDB := s.db.WithContext(deadline)
	var total int64
	for total < int64(maxRows) && deadline.Err() == nil {
		var ids []uint
		var deleted int64
		err := batchDB.Transaction(func(tx *gorm.DB) error {
			err := tx.Model(model).Where(column+" < ?", cutoff.UTC()).Order(column+" ASC").
				Limit(min(batchSize, maxRows-int(total))).Pluck("id", &ids).Error
			if err != nil || len(ids) == 0 {
				return err
			}
			result := tx.Where("id IN ?", ids).Delete(model)
			deleted = result.RowsAffected
			return result.Error
		})
		if err != nil {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			if deadline.Err() != nil {
				break
			}
			return total, err
		}
		total += deleted
		if len(ids) == 0 {
			break
		}
	}
	return total, ctx.Err()
}

func (s *SiteOpsStore) ListReferencePrices(ctx context.Context, id uint, binding string) ([]SiteOpsReferencePrice, error) {
	rows := []SiteOpsReferencePrice{}
	err := s.db.WithContext(ctx).Where("instance_id = ? AND target_binding = ?", id, binding).Order("scope, scope_id").Find(&rows).Error
	return rows, err
}
func (s *SiteOpsStore) ListReferencePricesByIDs(ctx context.Context, id uint, binding, scope string, ids []int64) ([]SiteOpsReferencePrice, error) {
	seen := make(map[int64]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, v := range ids {
		if !seen[v] {
			unique = append(unique, v)
			seen[v] = true
		}
	}
	rows := []SiteOpsReferencePrice{}
	for len(unique) > 0 {
		n := min(100, len(unique))
		var part []SiteOpsReferencePrice
		if err := s.db.WithContext(ctx).Where("instance_id = ? AND target_binding = ? AND scope = ? AND scope_id IN ?", id, binding, scope, unique[:n]).Find(&part).Error; err != nil {
			return nil, err
		}
		rows = append(rows, part...)
		unique = unique[n:]
	}
	return rows, nil
}
func (s *SiteOpsStore) withAnalyticsGeneration(ctx context.Context, id uint, generation int64, fn func(*gorm.DB, *SiteOpsInstance) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		inst, err := findSiteOpsInstance(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "generation", "backoff_until"), id)
		if err != nil {
			return err
		}
		if inst.Generation != generation {
			return ErrSiteOpsGenerationMismatch
		}
		return fn(tx, inst)
	})
}
func (s *SiteOpsStore) PutReferencePrice(ctx context.Context, generation int64, row *SiteOpsReferencePrice) error {
	if row == nil {
		return errors.New("missing reference price")
	}
	value := *row
	value.ID = 0
	value.UpdatedAt = time.Now().UTC()
	err := s.withAnalyticsGeneration(ctx, value.InstanceID, generation, func(tx *gorm.DB, _ *SiteOpsInstance) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "instance_id"}, {Name: "target_binding"}, {Name: "scope"}, {Name: "scope_id"}}, DoUpdates: clause.AssignmentColumns([]string{"currency", "monthly_minor", "cny_per_usd_micros", "updated_at"})}).Create(&value).Error
	})
	if err == nil {
		*row = value
	}
	return err
}
func (s *SiteOpsStore) ClearReferencePrice(ctx context.Context, id uint, generation int64, binding, scope string, scopeID int64) error {
	return s.withAnalyticsGeneration(ctx, id, generation, func(tx *gorm.DB, _ *SiteOpsInstance) error {
		return tx.Where("instance_id = ? AND target_binding = ? AND scope = ? AND scope_id = ?", id, binding, scope, scopeID).Delete(&SiteOpsReferencePrice{}).Error
	})
}
func (s *SiteOpsStore) ExtendAdminBackoff(ctx context.Context, id uint, generation int64, until time.Time) error {
	return s.withAnalyticsGeneration(ctx, id, generation, func(tx *gorm.DB, inst *SiteOpsInstance) error {
		if inst.BackoffUntil != nil && !until.After(*inst.BackoffUntil) {
			return nil
		}
		return tx.Model(inst).Update("backoff_until", until.UTC()).Error
	})
}
func (s *SiteOpsStore) UpdateAnalyticsConfig(ctx context.Context, id uint, configJSON string) error {
	res := s.db.WithContext(ctx).Model(&SiteOpsInstance{}).Where("id = ?", id).Update("analytics_config_json", configJSON)
	if res.Error == nil && res.RowsAffected == 0 {
		_, err := s.GetInstance(ctx, id)
		return err
	}
	return res.Error
}

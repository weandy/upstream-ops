package storage

import (
	"context"
	"errors"
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
		return tx.Where("instance_id = ?", id).Delete(&SiteOpsGroupSnapshot{}).Error
	})
}

func (s *SiteOpsStore) collectionTx(ctx context.Context, id uint, generation int64, work func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inst SiteOpsInstance
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("generation", "enabled").Where("id = ?", id).Take(&inst).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrSiteOpsNotFound
		}
		if err != nil {
			return err
		}
		if inst.Generation != generation || !inst.Enabled {
			return ErrSiteOpsGenerationMismatch
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
	return s.collectionTx(ctx, id, generation, func(tx *gorm.DB) error {
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
	return s.collectionTx(ctx, id, generation, func(tx *gorm.DB) error {
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
	return s.deleteBeforeChunked(ctx, &SiteOpsSnapshot{}, cutoff, batchSize, maxRows, maxDuration)
}

func (s *SiteOpsStore) DeleteGroupSnapshotsBeforeChunked(ctx context.Context, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
	return s.deleteBeforeChunked(ctx, &SiteOpsGroupSnapshot{}, cutoff, batchSize, maxRows, maxDuration)
}

func (s *SiteOpsStore) deleteBeforeChunked(ctx context.Context, model any, cutoff time.Time, batchSize, maxRows int, maxDuration time.Duration) (int64, error) {
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
			err := tx.Model(model).Where("observed_at < ?", cutoff.UTC()).Order("observed_at ASC").
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

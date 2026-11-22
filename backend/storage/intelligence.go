package storage

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProbeTargets 监测目标仓储。
type ProbeTargets struct{ db *gorm.DB }

func NewProbeTargets(db *gorm.DB) *ProbeTargets { return &ProbeTargets{db: db} }

func (r *ProbeTargets) Create(t *ProbeTarget) error { return r.db.Create(t).Error }
func (r *ProbeTargets) Update(t *ProbeTarget) error { return r.db.Save(t).Error }

func (r *ProbeTargets) FindByID(id uint) (*ProbeTarget, error) {
	var t ProbeTarget
	if err := r.db.First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *ProbeTargets) FindBySlug(slug string) (*ProbeTarget, error) {
	var t ProbeTarget
	if err := r.db.Where("slug = ?", strings.TrimSpace(slug)).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *ProbeTargets) List() ([]ProbeTarget, error) {
	var list []ProbeTarget
	if err := r.db.Order("sort_order ASC").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ProbeTargets) ListEmbedVisible() ([]ProbeTarget, error) {
	var list []ProbeTarget
	if err := r.db.Where("enabled = ? AND embed_visible = ?", true, true).
		Order("sort_order ASC").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ProbeTargets) UpdateKey(id uint, keyID int64, keyName, cipherText, hint string) error {
	return r.db.Model(&ProbeTarget{}).Where("id = ?", id).Updates(map[string]any{
		"api_key_id":     keyID,
		"api_key_name":   keyName,
		"api_key_cipher": cipherText,
		"api_key_hint":   hint,
		"updated_at":     time.Now(),
	}).Error
}

// Delete 删除目标并级联清理其检测配置、测活样本与智力检测历史。
func (r *ProbeTargets) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{
			&ProbeDetectorConfig{},
			&ProbeLivenessSample{},
			&ProbeIntelligenceRun{},
		} {
			if err := tx.Where("target_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&ProbeTarget{}, id).Error
	})
}

// ProbeDetectorConfigs 目标 × 插件配置仓储。
type ProbeDetectorConfigs struct{ db *gorm.DB }

func NewProbeDetectorConfigs(db *gorm.DB) *ProbeDetectorConfigs {
	return &ProbeDetectorConfigs{db: db}
}

func (r *ProbeDetectorConfigs) ListByTarget(targetID uint) ([]ProbeDetectorConfig, error) {
	var list []ProbeDetectorConfig
	if err := r.db.Where("target_id = ?", targetID).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListByTargets 批量查询多个目标的全部检测插件配置（按 target_id 分组）。
func (r *ProbeDetectorConfigs) ListByTargets(targetIDs []uint) (map[uint][]ProbeDetectorConfig, error) {
	out := make(map[uint][]ProbeDetectorConfig, len(targetIDs))
	if len(targetIDs) == 0 {
		return out, nil
	}
	var list []ProbeDetectorConfig
	if err := r.db.Where("target_id IN ?", targetIDs).Order("target_id ASC").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	for _, cfg := range list {
		out[cfg.TargetID] = append(out[cfg.TargetID], cfg)
	}
	return out, nil
}

func (r *ProbeDetectorConfigs) Find(targetID uint, detectorKey string) (*ProbeDetectorConfig, error) {
	var c ProbeDetectorConfig
	if err := r.db.Where("target_id = ? AND detector_key = ?", targetID, detectorKey).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// Upsert 按 (target_id, detector_key) 跨方言原子更新或插入。
func (r *ProbeDetectorConfigs) Upsert(cfg *ProbeDetectorConfig) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "target_id"}, {Name: "detector_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"enabled",
			"model_name",
			"reasoning_effort",
			"interval_seconds",
			"timeout_seconds",
			"alert_enabled",
			"alert_threshold",
			"params_json",
			"embed_visible",
			"consecutive_errors",
			"last_error_class",
			"backoff_until",
			"next_run_at",
			"updated_at",
		}),
	}).Create(cfg).Error
}

// ListDue 返回已启用且到期的检测配置（仅包含其所属 ProbeTarget 也已启用的配置）。
func (r *ProbeDetectorConfigs) ListDue(now time.Time) ([]ProbeDetectorConfig, error) {
	var list []ProbeDetectorConfig
	err := r.db.Model(&ProbeDetectorConfig{}).
		Joins("JOIN probe_targets ON probe_targets.id = probe_detector_configs.target_id").
		Where("probe_targets.enabled = ? AND probe_detector_configs.enabled = ?", true, true).
		Where("probe_detector_configs.next_run_at IS NULL OR probe_detector_configs.next_run_at <= ?", now).
		Order("probe_detector_configs.id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ProbeDetectorConfigs) UpdateSchedule(id uint, lastRunAt, nextRunAt time.Time) error {
	return r.db.Model(&ProbeDetectorConfig{}).Where("id = ?", id).Updates(map[string]any{
		"last_run_at": lastRunAt,
		"next_run_at": nextRunAt,
		"updated_at":  time.Now(),
	}).Error
}

// UpdateScheduleWithBackoff 原子更新最近执行时间、下次计划时间与异常指数退避状态。
func (r *ProbeDetectorConfigs) UpdateScheduleWithBackoff(
	id uint,
	lastRunAt, nextRunAt time.Time,
	consecutiveErrors int,
	lastErrorClass string,
	backoffUntil *time.Time,
) error {
	return r.db.Model(&ProbeDetectorConfig{}).Where("id = ?", id).Updates(map[string]any{
		"last_run_at":        lastRunAt,
		"next_run_at":        nextRunAt,
		"consecutive_errors": consecutiveErrors,
		"last_error_class":   strings.TrimSpace(lastErrorClass),
		"backoff_until":      backoffUntil,
		"updated_at":         time.Now(),
	}).Error
}

// ResetTargetBackoff 清零某目标下所有插件的退避状态，并将已启用插件立刻安排下一轮探测。
func (r *ProbeDetectorConfigs) ResetTargetBackoff(targetID uint, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ProbeDetectorConfig{}).Where("target_id = ?", targetID).Updates(map[string]any{
			"consecutive_errors": 0,
			"last_error_class":   "",
			"backoff_until":      nil,
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&ProbeDetectorConfig{}).
			Where("target_id = ? AND enabled = ?", targetID, true).
			Updates(map[string]any{
				"next_run_at": now,
				"updated_at":  now,
			}).Error
	})
}

// ResetConfigBackoff 清零单条插件配置的退避状态（不改动其下次计划时间）。
func (r *ProbeDetectorConfigs) ResetConfigBackoff(targetID uint, detectorKey string) error {
	return r.db.Model(&ProbeDetectorConfig{}).
		Where("target_id = ? AND detector_key = ?", targetID, strings.TrimSpace(detectorKey)).
		Updates(map[string]any{
			"consecutive_errors": 0,
			"last_error_class":   "",
			"backoff_until":      nil,
			"updated_at":         time.Now(),
		}).Error
}

// ProbeLivenessSamples 主动测活采样仓储。
type ProbeLivenessSamples struct{ db *gorm.DB }

func NewProbeLivenessSamples(db *gorm.DB) *ProbeLivenessSamples {
	return &ProbeLivenessSamples{db: db}
}

func (r *ProbeLivenessSamples) Append(s *ProbeLivenessSample) error {
	if s.ObservedAt.IsZero() {
		s.ObservedAt = time.Now().UTC()
	}
	return r.db.Create(s).Error
}

func (r *ProbeLivenessSamples) ListByTargetSince(targetID uint, since time.Time) ([]ProbeLivenessSample, error) {
	var list []ProbeLivenessSample
	if err := r.db.Where("target_id = ? AND observed_at >= ?", targetID, since).
		Order("observed_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListByTargetsSince 批量查询多个目标在指定时间之后的测活样本（按 target_id 分组、observed_at 升序）。
func (r *ProbeLivenessSamples) ListByTargetsSince(targetIDs []uint, since time.Time) (map[uint][]ProbeLivenessSample, error) {
	out := make(map[uint][]ProbeLivenessSample, len(targetIDs))
	if len(targetIDs) == 0 {
		return out, nil
	}
	var list []ProbeLivenessSample
	if err := r.db.Where("target_id IN ? AND observed_at >= ?", targetIDs, since).
		Order("target_id ASC").Order("observed_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	for _, sample := range list {
		out[sample.TargetID] = append(out[sample.TargetID], sample)
	}
	return out, nil
}

func (r *ProbeLivenessSamples) ListRecentByTarget(targetID uint, limit int) ([]ProbeLivenessSample, error) {
	if limit <= 0 {
		limit = 20
	}
	var list []ProbeLivenessSample
	if err := r.db.Where("target_id = ?", targetID).
		Order("observed_at DESC").Limit(limit).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ProbeLivenessSamples) DeleteBefore(cutoff time.Time) (int64, error) {
	res := r.db.Where("observed_at < ?", cutoff).Delete(&ProbeLivenessSample{})
	return res.RowsAffected, res.Error
}

// ProbeIntelligenceRuns 智力检测记录仓储。
type ProbeIntelligenceRuns struct{ db *gorm.DB }

func NewProbeIntelligenceRuns(db *gorm.DB) *ProbeIntelligenceRuns {
	return &ProbeIntelligenceRuns{db: db}
}

func (r *ProbeIntelligenceRuns) Append(run *ProbeIntelligenceRun) error {
	if strings.TrimSpace(run.RunUUID) == "" {
		run.RunUUID = uuid.NewString()
	}
	if run.ObservedAt.IsZero() {
		run.ObservedAt = time.Now().UTC()
	}
	return r.db.Create(run).Error
}

// ListByTargetAndDetector 列出某目标某检测器的历史记录（倒序），跳过大字段 artifact_svg。
func (r *ProbeIntelligenceRuns) ListByTargetAndDetector(targetID uint, detectorKey string, limit int) ([]ProbeIntelligenceRun, error) {
	if limit <= 0 {
		limit = 48
	}
	var list []ProbeIntelligenceRun
	q := r.db.Omit("artifact_svg").Where("target_id = ?", targetID)
	if strings.TrimSpace(detectorKey) != "" {
		q = q.Where("detector_key = ?", detectorKey)
	}
	if err := q.Order("observed_at DESC").Order("id DESC").Limit(limit).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListByTargetAndDetectorSince 按时间窗口列出历史记录（跳过 artifact_svg）。
func (r *ProbeIntelligenceRuns) ListByTargetAndDetectorSince(targetID uint, detectorKey string, since time.Time) ([]ProbeIntelligenceRun, error) {
	var list []ProbeIntelligenceRun
	if err := r.db.Omit("artifact_svg").
		Where("target_id = ? AND detector_key = ? AND observed_at >= ?", targetID, detectorKey, since).
		Order("observed_at DESC").Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListByTargetsSince 批量按时间窗口查询多个目标指定插件的历史记录（跳过 artifact_svg，按 target_id 分组）。
func (r *ProbeIntelligenceRuns) ListByTargetsSince(targetIDs []uint, detectorKey string, since time.Time) (map[uint][]ProbeIntelligenceRun, error) {
	out := make(map[uint][]ProbeIntelligenceRun, len(targetIDs))
	if len(targetIDs) == 0 {
		return out, nil
	}
	var list []ProbeIntelligenceRun
	q := r.db.Omit("artifact_svg").Where("target_id IN ? AND observed_at >= ?", targetIDs, since)
	if strings.TrimSpace(detectorKey) != "" {
		q = q.Where("detector_key = ?", detectorKey)
	}
	if err := q.Order("target_id ASC").Order("observed_at DESC").Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	for _, run := range list {
		out[run.TargetID] = append(out[run.TargetID], run)
	}
	return out, nil
}

// ListLatestByTargets 使用标准窗口函数一次性批量查询多个目标 × 多个插件的最近 N 条记录（跳过 artifact_svg）。
func (r *ProbeIntelligenceRuns) ListLatestByTargets(targetIDs []uint, detectorKeys []string, perGroupLimit int) (map[uint]map[string][]ProbeIntelligenceRun, error) {
	out := make(map[uint]map[string][]ProbeIntelligenceRun, len(targetIDs))
	if len(targetIDs) == 0 || len(detectorKeys) == 0 {
		return out, nil
	}
	if perGroupLimit <= 0 {
		perGroupLimit = 1
	}
	const cols = "id, run_uuid, target_id, detector_key, model_name, reasoning_effort, status, summary, claimed_rank, top_candidate, latency_ms, first_token_ms, output_tokens, detail_json, error_message, shared_cache_key, reused_from_run_id, observed_at"
	query := "SELECT " + cols + " FROM (" +
		"SELECT " + cols + ", ROW_NUMBER() OVER (PARTITION BY target_id, detector_key ORDER BY observed_at DESC, id DESC) AS rn " +
		"FROM probe_intelligence_runs WHERE target_id IN ? AND detector_key IN ?" +
		") ranked WHERE rn <= ? ORDER BY target_id ASC, detector_key ASC, rn ASC"

	var rows []ProbeIntelligenceRun
	if err := r.db.Raw(query, targetIDs, detectorKeys, perGroupLimit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, run := range rows {
		byDet, ok := out[run.TargetID]
		if !ok {
			byDet = make(map[string][]ProbeIntelligenceRun)
			out[run.TargetID] = byDet
		}
		byDet[run.DetectorKey] = append(byDet[run.DetectorKey], run)
	}
	return out, nil
}

// FindByUUID 按对外 UUID 查完整记录（含 artifact_svg）。
func (r *ProbeIntelligenceRuns) FindByUUID(runUUID string) (*ProbeIntelligenceRun, error) {
	var run ProbeIntelligenceRun
	if err := r.db.Where("run_uuid = ?", strings.TrimSpace(runUUID)).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

// FindLatestFreshBySharedKey 按同源缓存键查最近一条可复用的新鲜结果。
// 只匹配其他目标的原始采样：本目标自己的上一轮、以及由缓存复制出来的记录都不能再当缓存源，
// 否则慢检测（画鹈鹕约 100 秒）会在下一个整点仍落在 TTL 内，把同一张图无限复制下去。
func (r *ProbeIntelligenceRuns) FindLatestFreshBySharedKey(sharedKey string, since time.Time, excludeTargetID uint) (*ProbeIntelligenceRun, error) {
	if strings.TrimSpace(sharedKey) == "" {
		return nil, nil
	}
	q := r.db.Where(
		"shared_cache_key = ? AND observed_at >= ? AND status <> ? AND reused_from_run_id IS NULL",
		sharedKey, since, IntelligenceStatusError,
	)
	if excludeTargetID != 0 {
		q = q.Where("target_id <> ?", excludeTargetID)
	}
	var run ProbeIntelligenceRun
	err := q.Order("observed_at DESC").Order("id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *ProbeIntelligenceRuns) DeleteBefore(cutoff time.Time) (int64, error) {
	res := r.db.Where("observed_at < ?", cutoff).Delete(&ProbeIntelligenceRun{})
	return res.RowsAffected, res.Error
}

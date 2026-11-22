package embed

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

// PublicCandySummary 对外展示的 Candy 24h 检测摘要。
type PublicCandySummary struct {
	Enabled       bool       `json:"enabled"`
	ModelName     string     `json:"model_name,omitempty"`
	Passed24h     int        `json:"passed_24h"`
	Checked24h    int        `json:"checked_24h"`
	LatestStatus  string     `json:"latest_status,omitempty"`
	LatestSummary string     `json:"latest_summary,omitempty"`
	LatestAt      *time.Time `json:"latest_at,omitempty"`
}

// PublicFingerprintSummary 对外展示的 lmfpd 指纹排名摘要（仅供参考）。
type PublicFingerprintSummary struct {
	Enabled       bool       `json:"enabled"`
	ModelName     string     `json:"model_name,omitempty"`
	ClaimedRank   int        `json:"claimed_rank,omitempty"`
	TopCandidate  string     `json:"top_candidate,omitempty"`
	LatestStatus  string     `json:"latest_status,omitempty"`
	LatestSummary string     `json:"latest_summary,omitempty"`
	LatestAt      *time.Time `json:"latest_at,omitempty"`
}

// PublicPelicanSummary 对外展示的最新画鹈鹕缩略摘要。
type PublicPelicanSummary struct {
	Enabled       bool       `json:"enabled"`
	ModelName     string     `json:"model_name,omitempty"`
	LatestRunUUID string     `json:"latest_run_uuid,omitempty"`
	LatestStatus  string     `json:"latest_status,omitempty"`
	LatestAt      *time.Time `json:"latest_at,omitempty"`
}

// PublicTargetStatus 对外公开的单行渠道性能与智力检测视图（绝对不含密钥、BaseURL、内部笔记）。
type PublicTargetStatus struct {
	Slug               string                            `json:"slug"`
	Name               string                            `json:"name"`
	Family             string                            `json:"family"`
	Performance        intelligence.PerformanceSummary   `json:"performance"`
	Trend              []intelligence.AvailabilityBucket `json:"trend"`
	TrendWindowMinutes int                               `json:"trend_window_minutes"`
	Candy              *PublicCandySummary               `json:"candy,omitempty"`
	Fingerprint        *PublicFingerprintSummary         `json:"fingerprint,omitempty"`
	Pelican            *PublicPelicanSummary             `json:"pelican,omitempty"`
	LastUpdatedAt      *time.Time                        `json:"last_updated_at,omitempty"`
}

// SiteSnapshot 站点完整对外快照（供 /embed SSR 与 /api/public/status JSON 共用）。
type SiteSnapshot struct {
	Site        SitePublicMeta       `json:"site"`
	GeneratedAt time.Time            `json:"generated_at"`
	LatestData  *time.Time           `json:"latest_data_at,omitempty"`
	Targets     []PublicTargetStatus `json:"targets"`
}

// SitePublicMeta 站点对外公开元信息（不含允许域名列表等管理侧配置）。
type SitePublicMeta struct {
	Slug               string `json:"slug"`
	Title              string `json:"title"`
	Eyebrow            string `json:"eyebrow"`
	Theme              string `json:"theme"`
	RefreshSeconds     int    `json:"refresh_seconds"`
	ShowIdentityColumn bool   `json:"show_identity_column"`
}

// CandyHistoryView Candy 钻取页数据模型。
type CandyHistoryView struct {
	Site        SitePublicMeta
	TargetName  string
	TargetSlug  string
	Family      string
	ModelName   string
	GeneratedAt time.Time
	PassedCount int
	TotalCount  int
	Runs        []CandyRunItem
}

// CandyRunItem 单次 Candy 历史记录（仅脱敏展示项）。
type CandyRunItem struct {
	RunUUID         string
	ModelName       string
	ReasoningEffort string
	Status          string
	Summary         string
	Answer          string
	LatencyMS       int64
	FirstTokenMS    int64
	OutputTokens    int
	ErrorMessage    string
	ObservedAt      time.Time
}

// FingerprintCandidate 单个指纹候选排名项。
type FingerprintCandidate struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name"`
	Family      string  `json:"family"`
	Probability float64 `json:"probability"`
	Rank        int     `json:"rank"`
}

// FingerprintRunItem 单次 lmfpd 指纹历史记录。
type FingerprintRunItem struct {
	RunUUID          string
	ModelName        string
	ReasoningEffort  string
	ClaimedModel     string
	ClaimedRank      int
	ClaimedInBank    bool
	TopCandidate     string
	Decision         string
	Probability      float64
	FamilyPrediction string
	Status           string
	Summary          string
	LatencyMS        int64
	ErrorMessage     string
	TopCandidates    []FingerprintCandidate
	ObservedAt       time.Time
}

// FingerprintHistoryView lmfpd 指纹钻取页数据模型。
type FingerprintHistoryView struct {
	Site        SitePublicMeta
	TargetName  string
	TargetSlug  string
	Family      string
	ModelName   string
	GeneratedAt time.Time
	Runs        []FingerprintRunItem
}

// PelicanGalleryItem Pelican 画廊单张卡片。
type PelicanGalleryItem struct {
	RunUUID         string
	ModelName       string
	ReasoningEffort string
	Status          string
	Summary         string
	LatencyMS       int64
	OutputTokens    int
	ObservedAt      time.Time
}

// PelicanGalleryView Pelican 画廊页数据模型。
type PelicanGalleryView struct {
	Site        SitePublicMeta
	TargetName  string
	TargetSlug  string
	Family      string
	ModelName   string
	GeneratedAt time.Time
	Items       []PelicanGalleryItem
}

// BuildSiteSnapshot 为指定嵌入站点聚合只读公开快照。
func (s *Service) BuildSiteSnapshot(site *SiteDTO) (*SiteSnapshot, error) {
	now := s.nowUTC()
	targets, err := s.resolveSiteTargets(site)
	if err != nil {
		return nil, err
	}

	rows := make([]PublicTargetStatus, 0, len(targets))
	var latestData *time.Time

	for i := range targets {
		t := &targets[i]
		row, err := s.buildTargetRow(t, site.ShowIdentityColumn, now)
		if err != nil {
			return nil, err
		}
		if row.LastUpdatedAt != nil && (latestData == nil || row.LastUpdatedAt.After(*latestData)) {
			ts := *row.LastUpdatedAt
			latestData = &ts
		}
		rows = append(rows, row)
	}

	return &SiteSnapshot{
		Site:        sitePublicMeta(site),
		GeneratedAt: now,
		LatestData:  latestData,
		Targets:     rows,
	}, nil
}

// BuildCandyHistory 构建某公开目标的最近 Candy 钻取历史。
func (s *Service) BuildCandyHistory(site *SiteDTO, targetSlug string, limit int) (*CandyHistoryView, error) {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	target, cfg, err := s.resolveVisibleTargetDetector(site, targetSlug, intelligence.DetectorKeyCandy)
	if err != nil {
		return nil, err
	}
	runs, err := s.Runs.ListByTargetAndDetector(target.ID, intelligence.DetectorKeyCandy, limit)
	if err != nil {
		return nil, err
	}

	items := make([]CandyRunItem, 0, len(runs))
	passed, total := 0, 0
	for _, r := range runs {
		if r.Status != storage.IntelligenceStatusError {
			total++
			if r.Status == storage.IntelligenceStatusNormal {
				passed++
			}
		}
		var detail struct {
			Answer string `json:"answer"`
		}
		if strings.TrimSpace(r.DetailJSON) != "" {
			_ = json.Unmarshal([]byte(r.DetailJSON), &detail)
		}
		items = append(items, CandyRunItem{
			RunUUID:         r.RunUUID,
			ModelName:       r.ModelName,
			ReasoningEffort: r.ReasoningEffort,
			Status:          r.Status,
			Summary:         r.Summary,
			Answer:          strings.TrimSpace(detail.Answer),
			LatencyMS:       r.LatencyMS,
			FirstTokenMS:    r.FirstTokenMS,
			OutputTokens:    r.OutputTokens,
			ErrorMessage:    r.ErrorMessage,
			ObservedAt:      r.ObservedAt.UTC(),
		})
	}

	modelName := ""
	if cfg != nil {
		modelName = cfg.ModelName
	}
	if modelName == "" && len(items) > 0 {
		modelName = items[0].ModelName
	}

	return &CandyHistoryView{
		Site:        sitePublicMeta(site),
		TargetName:  target.Name,
		TargetSlug:  target.Slug,
		Family:      target.Family,
		ModelName:   modelName,
		GeneratedAt: s.nowUTC(),
		PassedCount: passed,
		TotalCount:  total,
		Runs:        items,
	}, nil
}

// BuildFingerprintHistory 构建某公开目标的最近 lmfpd 指纹排名历史。
func (s *Service) BuildFingerprintHistory(site *SiteDTO, targetSlug string, limit int) (*FingerprintHistoryView, error) {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	target, cfg, err := s.resolveVisibleTargetDetector(site, targetSlug, intelligence.DetectorKeyLMFPD)
	if err != nil {
		return nil, err
	}
	runs, err := s.Runs.ListByTargetAndDetector(target.ID, intelligence.DetectorKeyLMFPD, limit)
	if err != nil {
		return nil, err
	}

	items := make([]FingerprintRunItem, 0, len(runs))
	for _, r := range runs {
		var detail struct {
			ClaimedModel     string                 `json:"claimed_model"`
			ClaimedInBank    bool                   `json:"claimed_in_bank"`
			Decision         string                 `json:"decision"`
			Probability      float64                `json:"probability"`
			FamilyPrediction string                 `json:"family_prediction"`
			TopCandidates    []FingerprintCandidate `json:"top_candidates"`
		}
		if strings.TrimSpace(r.DetailJSON) != "" {
			_ = json.Unmarshal([]byte(r.DetailJSON), &detail)
		}
		for i := range detail.TopCandidates {
			if detail.TopCandidates[i].Rank <= 0 {
				detail.TopCandidates[i].Rank = i + 1
			}
			if detail.TopCandidates[i].DisplayName == "" {
				detail.TopCandidates[i].DisplayName = detail.TopCandidates[i].ID
			}
		}
		claimedModel := detail.ClaimedModel
		if claimedModel == "" {
			claimedModel = r.ModelName
		}
		items = append(items, FingerprintRunItem{
			RunUUID:          r.RunUUID,
			ModelName:        r.ModelName,
			ReasoningEffort:  r.ReasoningEffort,
			ClaimedModel:     claimedModel,
			ClaimedRank:      r.ClaimedRank,
			ClaimedInBank:    detail.ClaimedInBank,
			TopCandidate:     r.TopCandidate,
			Decision:         detail.Decision,
			Probability:      detail.Probability,
			FamilyPrediction: detail.FamilyPrediction,
			Status:           r.Status,
			Summary:          r.Summary,
			LatencyMS:        r.LatencyMS,
			ErrorMessage:     r.ErrorMessage,
			TopCandidates:    detail.TopCandidates,
			ObservedAt:       r.ObservedAt.UTC(),
		})
	}

	modelName := ""
	if cfg != nil {
		modelName = cfg.ModelName
	}
	if modelName == "" && len(items) > 0 {
		modelName = items[0].ModelName
	}

	return &FingerprintHistoryView{
		Site:        sitePublicMeta(site),
		TargetName:  target.Name,
		TargetSlug:  target.Slug,
		Family:      target.Family,
		ModelName:   modelName,
		GeneratedAt: s.nowUTC(),
		Runs:        items,
	}, nil
}

// BuildPelicanGallery 构建某公开目标的最近 Pelican 沙箱画廊列表。
func (s *Service) BuildPelicanGallery(site *SiteDTO, targetSlug string, limit int) (*PelicanGalleryView, error) {
	if limit <= 0 {
		limit = DefaultPelicanLimit
	}
	target, cfg, err := s.resolveVisibleTargetDetector(site, targetSlug, intelligence.DetectorKeyPelican)
	if err != nil {
		return nil, err
	}
	// 多拉取一些记录以跳过失败运行，仅保留成功生成画面的 normal 记录。
	fetchLimit := limit * 3
	if fetchLimit < 36 {
		fetchLimit = 36
	}
	runs, err := s.Runs.ListByTargetAndDetector(target.ID, intelligence.DetectorKeyPelican, fetchLimit)
	if err != nil {
		return nil, err
	}

	items := make([]PelicanGalleryItem, 0, limit)
	for _, r := range runs {
		if r.Status != storage.IntelligenceStatusNormal {
			continue
		}
		// 缓存复用只是上一张原图的副本，画廊按「一次真实作画」计数。
		if r.ReusedFromRunID != nil {
			continue
		}
		items = append(items, PelicanGalleryItem{
			RunUUID:         r.RunUUID,
			ModelName:       r.ModelName,
			ReasoningEffort: r.ReasoningEffort,
			Status:          r.Status,
			Summary:         r.Summary,
			LatencyMS:       r.LatencyMS,
			OutputTokens:    r.OutputTokens,
			ObservedAt:      r.ObservedAt.UTC(),
		})
		if len(items) >= limit {
			break
		}
	}

	modelName := ""
	if cfg != nil {
		modelName = cfg.ModelName
	}
	if modelName == "" && len(items) > 0 {
		modelName = items[0].ModelName
	}

	return &PelicanGalleryView{
		Site:        sitePublicMeta(site),
		TargetName:  target.Name,
		TargetSlug:  target.Slug,
		Family:      target.Family,
		ModelName:   modelName,
		GeneratedAt: s.nowUTC(),
		Items:       items,
	}, nil
}

// ResolvePelicanArtifact 校验访问边界并返回单张 Pelican HTML/SVG 文档。
// 防止越权访问未对外公开、被禁用或不属于该 site 的检测产物。
func (s *Service) ResolvePelicanArtifact(site *SiteDTO, runUUID string) (string, error) {
	if !site.ShowIdentityColumn {
		return "", ErrArtifactNotFound
	}
	run, err := s.Runs.FindByUUID(runUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrArtifactNotFound
		}
		return "", err
	}
	if run.DetectorKey != intelligence.DetectorKeyPelican || strings.TrimSpace(run.ArtifactSVG) == "" {
		return "", ErrArtifactNotFound
	}

	visibleTargets, err := s.resolveSiteTargets(site)
	if err != nil {
		return "", err
	}
	allowedTarget := false
	for _, t := range visibleTargets {
		if t.ID == run.TargetID {
			allowedTarget = true
			break
		}
	}
	if !allowedTarget {
		return "", ErrArtifactNotFound
	}

	cfg, err := s.Configs.Find(run.TargetID, intelligence.DetectorKeyPelican)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrArtifactNotFound
		}
		return "", err
	}
	if !cfg.EmbedVisible {
		return "", ErrArtifactNotFound
	}
	return run.ArtifactSVG, nil
}

func (s *Service) resolveSiteTargets(site *SiteDTO) ([]storage.ProbeTarget, error) {
	visible, err := s.Targets.ListEmbedVisible()
	if err != nil {
		return nil, err
	}
	if len(site.TargetIDs) == 0 {
		return visible, nil
	}
	byID := make(map[uint]storage.ProbeTarget, len(visible))
	for _, t := range visible {
		byID[t.ID] = t
	}
	ordered := make([]storage.ProbeTarget, 0, len(site.TargetIDs))
	for _, id := range site.TargetIDs {
		if t, ok := byID[id]; ok {
			ordered = append(ordered, t)
		}
	}
	return ordered, nil
}

func (s *Service) resolveVisibleTargetDetector(site *SiteDTO, targetSlug, detectorKey string) (*storage.ProbeTarget, *storage.ProbeDetectorConfig, error) {
	if !site.ShowIdentityColumn {
		return nil, nil, ErrTargetNotVisible
	}
	slug := normalizeSlug(targetSlug)
	if slug == "" {
		return nil, nil, ErrTargetNotVisible
	}
	visible, err := s.resolveSiteTargets(site)
	if err != nil {
		return nil, nil, err
	}
	var target *storage.ProbeTarget
	for i := range visible {
		if visible[i].Slug == slug {
			t := visible[i]
			target = &t
			break
		}
	}
	if target == nil {
		return nil, nil, ErrTargetNotVisible
	}
	cfg, err := s.Configs.Find(target.ID, detectorKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrTargetNotVisible
		}
		return nil, nil, err
	}
	if !cfg.EmbedVisible {
		return nil, nil, ErrTargetNotVisible
	}
	return target, cfg, nil
}

func (s *Service) buildTargetRow(target *storage.ProbeTarget, includeIdentity bool, now time.Time) (PublicTargetStatus, error) {
	intervalSec := 0
	if cfg, err := s.Configs.Find(target.ID, intelligence.DetectorKeyLiveness); err == nil && cfg.IntervalSeconds > 0 {
		intervalSec = cfg.IntervalSeconds
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return PublicTargetStatus{}, err
	}
	since := now.Add(-24 * time.Hour)
	if lookback := intelligence.TrendLookback(intervalSec); lookback > 24*time.Hour {
		since = now.Add(-lookback)
	}
	samples, err := s.Liveness.ListByTargetSince(target.ID, since)
	if err != nil {
		return PublicTargetStatus{}, err
	}
	perf, trend := intelligence.AggregateLiveness(samples, now, intelligence.LivenessBucketDuration(intervalSec))

	var lastUpdated *time.Time
	for _, sample := range samples {
		ts := sample.ObservedAt.UTC()
		if lastUpdated == nil || ts.After(*lastUpdated) {
			v := ts
			lastUpdated = &v
		}
	}

	row := PublicTargetStatus{
		Slug:               target.Slug,
		Name:               target.Name,
		Family:             target.Family,
		Performance:        perf,
		Trend:              trend,
		TrendWindowMinutes: intelligence.TrendWindowMinutes(intervalSec),
	}

	if !includeIdentity {
		row.LastUpdatedAt = lastUpdated
		return row, nil
	}

	cfgs, err := s.Configs.ListByTarget(target.ID)
	if err != nil {
		return PublicTargetStatus{}, err
	}
	cfgMap := make(map[string]storage.ProbeDetectorConfig, len(cfgs))
	for _, c := range cfgs {
		cfgMap[c.DetectorKey] = c
	}

	if cfg, ok := cfgMap[intelligence.DetectorKeyCandy]; ok && cfg.EmbedVisible {
		candy24h, err := s.Runs.ListByTargetAndDetectorSince(target.ID, intelligence.DetectorKeyCandy, now.Add(-24*time.Hour))
		if err != nil {
			return PublicTargetStatus{}, err
		}
		latestRuns, err := s.Runs.ListByTargetAndDetector(target.ID, intelligence.DetectorKeyCandy, 1)
		if err != nil {
			return PublicTargetStatus{}, err
		}
		if cfg.Enabled || len(latestRuns) > 0 {
			passed, checked := 0, 0
			for _, r := range candy24h {
				if r.Status == storage.IntelligenceStatusError {
					continue
				}
				checked++
				if r.Status == storage.IntelligenceStatusNormal {
					passed++
				}
			}
			sum := &PublicCandySummary{
				Enabled:    cfg.Enabled,
				ModelName:  cfg.ModelName,
				Passed24h:  passed,
				Checked24h: checked,
			}
			if len(latestRuns) > 0 {
				lr := latestRuns[0]
				ts := lr.ObservedAt.UTC()
				sum.LatestStatus = lr.Status
				sum.LatestSummary = lr.Summary
				sum.LatestAt = &ts
				if lastUpdated == nil || ts.After(*lastUpdated) {
					v := ts
					lastUpdated = &v
				}
			}
			row.Candy = sum
		}
	}

	if cfg, ok := cfgMap[intelligence.DetectorKeyLMFPD]; ok && cfg.EmbedVisible {
		latestRuns, err := s.Runs.ListByTargetAndDetector(target.ID, intelligence.DetectorKeyLMFPD, 1)
		if err != nil {
			return PublicTargetStatus{}, err
		}
		if cfg.Enabled || len(latestRuns) > 0 {
			sum := &PublicFingerprintSummary{
				Enabled:   cfg.Enabled,
				ModelName: cfg.ModelName,
			}
			if len(latestRuns) > 0 {
				lr := latestRuns[0]
				ts := lr.ObservedAt.UTC()
				sum.ClaimedRank = lr.ClaimedRank
				sum.TopCandidate = lr.TopCandidate
				sum.LatestStatus = lr.Status
				sum.LatestSummary = lr.Summary
				sum.LatestAt = &ts
				if lastUpdated == nil || ts.After(*lastUpdated) {
					v := ts
					lastUpdated = &v
				}
			}
			row.Fingerprint = sum
		}
	}

	if cfg, ok := cfgMap[intelligence.DetectorKeyPelican]; ok && cfg.EmbedVisible {
		recentPelicans, err := s.Runs.ListByTargetAndDetector(target.ID, intelligence.DetectorKeyPelican, 10)
		if err != nil {
			return PublicTargetStatus{}, err
		}
		if cfg.Enabled || len(recentPelicans) > 0 {
			sum := &PublicPelicanSummary{
				Enabled:   cfg.Enabled,
				ModelName: cfg.ModelName,
			}
			for _, pr := range recentPelicans {
				if pr.Status == storage.IntelligenceStatusNormal {
					ts := pr.ObservedAt.UTC()
					sum.LatestRunUUID = pr.RunUUID
					sum.LatestStatus = pr.Status
					sum.LatestAt = &ts
					if lastUpdated == nil || ts.After(*lastUpdated) {
						v := ts
						lastUpdated = &v
					}
					break
				}
			}
			if sum.LatestRunUUID == "" && len(recentPelicans) > 0 {
				ts := recentPelicans[0].ObservedAt.UTC()
				sum.LatestStatus = recentPelicans[0].Status
				sum.LatestAt = &ts
			}
			row.Pelican = sum
		}
	}

	row.LastUpdatedAt = lastUpdated
	return row, nil
}

func sitePublicMeta(site *SiteDTO) SitePublicMeta {
	return SitePublicMeta{
		Slug:               site.Slug,
		Title:              site.Title,
		Eyebrow:            site.Eyebrow,
		Theme:              site.Theme,
		RefreshSeconds:     site.RefreshSeconds,
		ShowIdentityColumn: site.ShowIdentityColumn,
	}
}

// SortCandidatesDesc 确保指纹候选列表按概率降序稳定排序。
func SortCandidatesDesc(list []FingerprintCandidate) {
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].Probability > list[j].Probability
	})
}

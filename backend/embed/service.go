package embed

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

const (
	ThemePaper = "paper"
	ThemeLight = "light"
	ThemeDark  = "dark"

	DefaultSiteSlug      = "default"
	DefaultSiteTitle     = "渠道性能"
	DefaultSiteEyebrow   = "CHANNEL PERFORMANCE"
	DefaultRefreshSec    = 15
	MaxRefreshSec        = 3600
	DefaultHistoryLimit  = 48
	DefaultPelicanLimit  = 12
)

var (
	// ErrSiteNotFound 嵌入站点不存在或未启用。
	ErrSiteNotFound = errors.New("embed site not found")
	// ErrTargetNotVisible 目标不存在或未对当前站点公开。
	ErrTargetNotVisible = errors.New("target not visible in embed site")
	// ErrArtifactNotFound Pelican 画面不存在或无权在当前站点访问。
	ErrArtifactNotFound = errors.New("pelican artifact not found")

	nonSlugCharsRe = regexp.MustCompile(`[^a-z0-9-]+`)
)

// SiteInput 管理端创建/更新嵌入站点的输入模型。
type SiteInput struct {
	Slug               string   `json:"slug"`
	Title              string   `json:"title"`
	Eyebrow            string   `json:"eyebrow"`
	Enabled            bool     `json:"enabled"`
	RefreshSeconds     int      `json:"refresh_seconds"`
	AllowedOrigins     []string `json:"allowed_origins"`
	CORSOrigins        []string `json:"cors_origins"`
	Theme              string   `json:"theme"`
	TargetIDs          []uint   `json:"target_ids"`
	ShowIdentityColumn bool     `json:"show_identity_column"`
}

// SiteDTO 对管理端输出的结构化嵌入站点视图（同时附带解析后的数组字段，方便前端编辑）。
type SiteDTO struct {
	ID                 uint      `json:"id"`
	Slug               string    `json:"slug"`
	Title              string    `json:"title"`
	Eyebrow            string    `json:"eyebrow"`
	Enabled            bool      `json:"enabled"`
	RefreshSeconds     int       `json:"refresh_seconds"`
	AllowedOrigins     []string  `json:"allowed_origins"`
	CORSOrigins        []string  `json:"cors_origins"`
	Theme              string    `json:"theme"`
	TargetIDs          []uint    `json:"target_ids"`
	ShowIdentityColumn bool      `json:"show_identity_column"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Service 外部嵌入组件与公开状态服务。
type Service struct {
	Sites    *storage.EmbedSites
	Targets  *storage.ProbeTargets
	Configs  *storage.ProbeDetectorConfigs
	Liveness *storage.ProbeLivenessSamples
	Runs     *storage.ProbeIntelligenceRuns
	NowFunc  func() time.Time
}

// NewService 构造嵌入服务。可复用已有 intelligence.Service 的仓储实例。
func NewService(db *gorm.DB, intel *intelligence.Service) *Service {
	svc := &Service{
		Sites:   storage.NewEmbedSites(db),
		NowFunc: func() time.Time { return time.Now().UTC() },
	}
	if intel != nil {
		svc.Targets = intel.Targets
		svc.Configs = intel.Configs
		svc.Liveness = intel.Liveness
		svc.Runs = intel.Runs
	} else {
		svc.Targets = storage.NewProbeTargets(db)
		svc.Configs = storage.NewProbeDetectorConfigs(db)
		svc.Liveness = storage.NewProbeLivenessSamples(db)
		svc.Runs = storage.NewProbeIntelligenceRuns(db)
	}
	return svc
}

func (s *Service) nowUTC() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc().UTC()
	}
	return time.Now().UTC()
}

// ListSites 列出所有外部嵌入站点配置。
func (s *Service) ListSites() ([]SiteDTO, error) {
	rows, err := s.Sites.List()
	if err != nil {
		return nil, err
	}
	out := make([]SiteDTO, 0, len(rows))
	for i := range rows {
		out = append(out, toSiteDTO(&rows[i]))
	}
	return out, nil
}

// GetSite 按 ID 查询单个站点配置。
func (s *Service) GetSite(id uint) (*SiteDTO, error) {
	row, err := s.Sites.FindByID(id)
	if err != nil {
		return nil, err
	}
	dto := toSiteDTO(row)
	return &dto, nil
}

// CreateSite 创建新的外部嵌入站点。
func (s *Service) CreateSite(in SiteInput) (*SiteDTO, error) {
	row, err := normalizeSiteInput(nil, in)
	if err != nil {
		return nil, err
	}
	if err := s.Sites.Create(row); err != nil {
		return nil, err
	}
	dto := toSiteDTO(row)
	return &dto, nil
}

// UpdateSite 更新指定 ID 的外部嵌入站点。
func (s *Service) UpdateSite(id uint, in SiteInput) (*SiteDTO, error) {
	existing, err := s.Sites.FindByID(id)
	if err != nil {
		return nil, err
	}
	row, err := normalizeSiteInput(existing, in)
	if err != nil {
		return nil, err
	}
	if err := s.Sites.Update(row); err != nil {
		return nil, err
	}
	dto := toSiteDTO(row)
	return &dto, nil
}

// DeleteSite 删除外部嵌入站点。
func (s *Service) DeleteSite(id uint) error {
	if _, err := s.Sites.FindByID(id); err != nil {
		return err
	}
	return s.Sites.Delete(id)
}

// ResolveSite 解析外部请求指定的嵌入站点（仅返回启用站点；未配置任何站点时为 default 提供仅限 'self' 的只读视图）。
func (s *Service) ResolveSite(slug string) (*SiteDTO, error) {
	clean := normalizeSlug(slug)
	if clean != "" {
		row, err := s.Sites.FindBySlug(clean)
		if err == nil {
			if !row.Enabled {
				return nil, ErrSiteNotFound
			}
			dto := toSiteDTO(row)
			return &dto, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if clean != DefaultSiteSlug {
			return nil, ErrSiteNotFound
		}
	}

	// slug 为空或请求 default 时：优先精确匹配 default，再回退首个启用站点；数据库完全为空时提供安全默认只读视图。
	if row, err := s.Sites.FindBySlug(DefaultSiteSlug); err == nil {
		if !row.Enabled {
			return nil, ErrSiteNotFound
		}
		dto := toSiteDTO(row)
		return &dto, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	all, err := s.Sites.List()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return &SiteDTO{
			Slug:               DefaultSiteSlug,
			Title:              DefaultSiteTitle,
			Eyebrow:            DefaultSiteEyebrow,
			Enabled:            true,
			RefreshSeconds:     DefaultRefreshSec,
			AllowedOrigins:     []string{},
			CORSOrigins:        []string{},
			Theme:              ThemePaper,
			TargetIDs:          []uint{},
			ShowIdentityColumn: true,
		}, nil
	}
	if clean == "" {
		for i := range all {
			if all[i].Enabled {
				dto := toSiteDTO(&all[i])
				return &dto, nil
			}
		}
	}
	return nil, ErrSiteNotFound
}

func normalizeSiteInput(existing *storage.EmbedSite, in SiteInput) (*storage.EmbedSite, error) {
	slug := normalizeSlug(in.Slug)
	if slug == "" {
		return nil, errors.New("slug is required (use letters, digits and hyphens)")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = DefaultSiteTitle
	}
	if len([]rune(title)) > 120 {
		return nil, errors.New("title too long (max 120 chars)")
	}
	eyebrow := strings.TrimSpace(in.Eyebrow)
	if eyebrow == "" {
		eyebrow = DefaultSiteEyebrow
	}
	if len([]rune(eyebrow)) > 120 {
		return nil, errors.New("eyebrow too long (max 120 chars)")
	}

	refresh := in.RefreshSeconds
	if refresh < 0 || refresh > MaxRefreshSec {
		return nil, fmt.Errorf("refresh_seconds must be between 0 and %d", MaxRefreshSec)
	}

	allowed, err := SanitizeOriginList(in.AllowedOrigins, true)
	if err != nil {
		return nil, fmt.Errorf("allowed_origins: %w", err)
	}
	corsOrigins, err := SanitizeOriginList(in.CORSOrigins, true)
	if err != nil {
		return nil, fmt.Errorf("cors_origins: %w", err)
	}

	theme := normalizeTheme(in.Theme)
	targetIDs := dedupPositiveUints(in.TargetIDs)

	allowedJSON, _ := json.Marshal(allowed)
	corsJSON, _ := json.Marshal(corsOrigins)
	targetIDsJSON, _ := json.Marshal(targetIDs)

	var row storage.EmbedSite
	if existing != nil {
		row = *existing
	}
	row.Slug = slug
	row.Title = title
	row.Eyebrow = eyebrow
	row.Enabled = in.Enabled
	row.RefreshSeconds = refresh
	row.AllowedOriginsJSON = string(allowedJSON)
	row.CORSOriginsJSON = string(corsJSON)
	row.Theme = theme
	row.TargetIDsJSON = string(targetIDsJSON)
	row.ShowIdentityColumn = in.ShowIdentityColumn
	return &row, nil
}

func toSiteDTO(row *storage.EmbedSite) SiteDTO {
	var allowed []string
	var corsOrigins []string
	var targetIDs []uint
	_ = json.Unmarshal([]byte(row.AllowedOriginsJSON), &allowed)
	_ = json.Unmarshal([]byte(row.CORSOriginsJSON), &corsOrigins)
	_ = json.Unmarshal([]byte(row.TargetIDsJSON), &targetIDs)
	if allowed == nil {
		allowed = []string{}
	}
	if corsOrigins == nil {
		corsOrigins = []string{}
	}
	if targetIDs == nil {
		targetIDs = []uint{}
	}
	return SiteDTO{
		ID:                 row.ID,
		Slug:               row.Slug,
		Title:              row.Title,
		Eyebrow:            row.Eyebrow,
		Enabled:            row.Enabled,
		RefreshSeconds:     row.RefreshSeconds,
		AllowedOrigins:     allowed,
		CORSOrigins:        corsOrigins,
		Theme:              normalizeTheme(row.Theme),
		TargetIDs:          targetIDs,
		ShowIdentityColumn: row.ShowIdentityColumn,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

func normalizeSlug(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = nonSlugCharsRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = strings.TrimRight(s[:64], "-")
	}
	return s
}

func normalizeTheme(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ThemeLight:
		return ThemeLight
	case ThemeDark:
		return ThemeDark
	default:
		return ThemePaper
	}
}

func dedupPositiveUints(in []uint) []uint {
	out := make([]uint, 0, len(in))
	seen := make(map[uint]struct{}, len(in))
	for _, id := range in {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

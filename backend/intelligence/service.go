package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

// ChannelKeyManager 上游渠道 API Key 管理子集（由 *channel.Service 原生实现）。
type ChannelKeyManager interface {
	ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error)
	CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error)
	UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error)
	RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error)
}

// TargetInput 创建/更新监测目标的输入载荷。
type TargetInput struct {
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	Family           string `json:"family"`
	SourceKind       string `json:"source_kind"`
	ChannelID        uint   `json:"channel_id"`
	SourceGroupID    *int64 `json:"source_group_id,omitempty"`
	SourceGroupName  string `json:"source_group_name"`
	BaseURL          string `json:"base_url"`
	APIKeySource     string `json:"api_key_source"`
	APIKey           string `json:"api_key"`
	Protocol         string `json:"protocol"`
	AuthStyle        string `json:"auth_style"`
	ProxyEnabled     bool   `json:"proxy_enabled"`
	UserAgent        string `json:"user_agent"`
	ExtraHeadersJSON string `json:"extra_headers_json"`
	SortOrder        int    `json:"sort_order"`
	Enabled          bool   `json:"enabled"`
	EmbedVisible     bool   `json:"embed_visible"`
	Notes            string `json:"notes"`
}

// TargetOverview 单目标实时总览（24h 性能 + 90 分钟趋势 + 最近采样 + 检测器配置）。
type TargetOverview struct {
	Target         *storage.ProbeTarget          `json:"target"`
	Performance    PerformanceSummary            `json:"performance"`
	Trend          []AvailabilityBucket          `json:"trend"`
	RecentSamples  []storage.ProbeLivenessSample `json:"recent_samples"`
	DetectorConfig []storage.ProbeDetectorConfig `json:"detector_configs"`
}

// Service 外挂式监测核心领域服务。
type Service struct {
	Targets    *storage.ProbeTargets
	Configs    *storage.ProbeDetectorConfigs
	Liveness   *storage.ProbeLivenessSamples
	Runs       *storage.ProbeIntelligenceRuns
	Channels   *storage.Channels
	ChannelAPI ChannelKeyManager
	Cipher     *crypto.Cipher
	Client     LLMClient
	Log        *slog.Logger

	mu             sync.RWMutex
	proxyConfig    config.ProxyConfig
	upstreamConfig config.UpstreamConfig
	keyLocks       sync.Map
}

// NewService 构造监测核心服务。
func NewService(
	db *gorm.DB,
	cipher *crypto.Cipher,
	channelAPI ChannelKeyManager,
	client LLMClient,
	log *slog.Logger,
) *Service {
	if client == nil {
		client = llmclient.New()
	}
	return &Service{
		Targets:        storage.NewProbeTargets(db),
		Configs:        storage.NewProbeDetectorConfigs(db),
		Liveness:       storage.NewProbeLivenessSamples(db),
		Runs:           storage.NewProbeIntelligenceRuns(db),
		Channels:       storage.NewChannels(db),
		ChannelAPI:     channelAPI,
		Cipher:         cipher,
		Client:         client,
		Log:            log,
		upstreamConfig: config.UpstreamConfig{}.WithDefaults(),
	}
}

func (s *Service) UpdateProxyConfig(cfg config.ProxyConfig) {
	s.mu.Lock()
	s.proxyConfig = cfg
	s.mu.Unlock()
}

func (s *Service) UpdateUpstreamConfig(cfg config.UpstreamConfig) {
	s.mu.Lock()
	s.upstreamConfig = cfg.WithDefaults()
	s.mu.Unlock()
}

func (s *Service) currentProxyURL(enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	s.mu.RLock()
	cfg := s.proxyConfig
	s.mu.RUnlock()
	return cfg.ActiveURL()
}

func (s *Service) currentUpstreamConfig() config.UpstreamConfig {
	s.mu.RLock()
	cfg := s.upstreamConfig
	s.mu.RUnlock()
	return cfg.WithDefaults()
}

// CreateTarget 新建监测目标。按既定决策，新建目标不自动开启任何定时监测，由用户显式勾选开启。
func (s *Service) CreateTarget(in TargetInput) (*storage.ProbeTarget, error) {
	target, err := s.normalizeTargetInput(nil, in)
	if err != nil {
		return nil, err
	}
	if err := s.Targets.Create(target); err != nil {
		return nil, err
	}
	return target, nil
}

// UpdateTarget 更新监测目标。APIKey 留空时保留原有加密密钥。
func (s *Service) UpdateTarget(id uint, in TargetInput) (*storage.ProbeTarget, error) {
	existing, err := s.Targets.FindByID(id)
	if err != nil {
		return nil, err
	}
	updated, err := s.normalizeTargetInput(existing, in)
	if err != nil {
		return nil, err
	}
	if err := s.Targets.Update(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteTarget(id uint) error {
	return s.Targets.Delete(id)
}

func (s *Service) ListTargets() ([]storage.ProbeTarget, error) {
	return s.Targets.List()
}

func (s *Service) GetTarget(id uint) (*storage.ProbeTarget, error) {
	return s.Targets.FindByID(id)
}

func (s *Service) normalizeTargetInput(existing *storage.ProbeTarget, in TargetInput) (*storage.ProbeTarget, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	slug := normalizeSlug(in.Slug)
	if slug == "" {
		slug = normalizeSlug(name)
	}
	if slug == "" {
		slug = fmt.Sprintf("target-%d", time.Now().Unix())
	}

	sourceKind := normalizeSourceKind(in.SourceKind)
	baseURL := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if sourceKind == storage.ProbeSourceChannel {
		if in.ChannelID == 0 {
			return nil, errors.New("channel_id is required when source_kind is channel")
		}
		if s.Channels != nil {
			if _, err := s.Channels.FindByID(in.ChannelID); err != nil {
				return nil, fmt.Errorf("channel %d not found: %w", in.ChannelID, err)
			}
		}
	} else {
		if baseURL == "" {
			return nil, errors.New("base_url is required when source_kind is direct")
		}
	}

	if extra := strings.TrimSpace(in.ExtraHeadersJSON); extra != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(extra), &m); err != nil {
			return nil, fmt.Errorf("invalid extra_headers_json: %w", err)
		}
	}

	var target storage.ProbeTarget
	if existing != nil {
		target = *existing
	}
	target.Name = name
	target.Slug = slug
	target.Family = normalizeFamily(in.Family)
	target.SourceKind = sourceKind
	target.ChannelID = in.ChannelID
	target.SourceGroupID = in.SourceGroupID
	target.SourceGroupName = strings.TrimSpace(in.SourceGroupName)
	target.BaseURL = baseURL
	target.APIKeySource = normalizeAPIKeySource(in.APIKeySource, sourceKind)
	target.Protocol = normalizeTargetProtocol(in.Protocol)
	target.AuthStyle = normalizeAuthStyle(in.AuthStyle, target.Protocol)
	target.ProxyEnabled = in.ProxyEnabled
	target.UserAgent = strings.TrimSpace(in.UserAgent)
	target.ExtraHeadersJSON = strings.TrimSpace(in.ExtraHeadersJSON)
	target.SortOrder = in.SortOrder
	target.Enabled = in.Enabled
	target.EmbedVisible = in.EmbedVisible
	target.Notes = strings.TrimSpace(in.Notes)

	if plainKey := strings.TrimSpace(in.APIKey); plainKey != "" {
		if s.Cipher == nil {
			return nil, errors.New("cipher not initialized")
		}
		enc, err := s.Cipher.Encrypt(plainKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt api key: %w", err)
		}
		target.APIKeyCipher = enc
		target.APIKeyHint = maskSecret(plainKey)
	}
	return &target, nil
}

// EnsureChannelKey 为绑定监控渠道的目标自动创建或复用专用上游 API Key 并加密落库。
func (s *Service) EnsureChannelKey(ctx context.Context, targetID uint) (*storage.ProbeTarget, error) {
	if s.ChannelAPI == nil || s.Channels == nil {
		return nil, errors.New("channel key manager not configured")
	}
	target, err := s.Targets.FindByID(targetID)
	if err != nil {
		return nil, err
	}
	if target.SourceKind != storage.ProbeSourceChannel || target.ChannelID == 0 {
		return nil, errors.New("target is not bound to a monitored channel")
	}
	ch, err := s.Channels.FindByID(target.ChannelID)
	if err != nil {
		return nil, err
	}

	keyName := stableProbeKeyName(target.ChannelID, target.SourceGroupID, target.SourceGroupName)
	unlock := s.lockKeyName(keyName)
	defer unlock()

	page, err := s.ChannelAPI.ListAPIKeys(ctx, target.ChannelID, connector.APIKeyQuery{
		Page:     1,
		PageSize: 100,
		Search:   keyName,
	})
	if err != nil {
		return nil, err
	}
	var existingKey *connector.APIKey
	for i := range page.Items {
		if strings.EqualFold(strings.TrimSpace(page.Items[i].Name), keyName) {
			k := page.Items[i]
			existingKey = &k
			break
		}
	}
	if existingKey == nil && target.APIKeyID > 0 {
		fullPage, err := s.ChannelAPI.ListAPIKeys(ctx, target.ChannelID, connector.APIKeyQuery{Page: 1, PageSize: 100})
		if err == nil && fullPage != nil {
			for i := range fullPage.Items {
				if fullPage.Items[i].ID == target.APIKeyID || strings.EqualFold(strings.TrimSpace(fullPage.Items[i].Name), keyName) {
					k := fullPage.Items[i]
					existingKey = &k
					break
				}
			}
		}
	}

	var unlimitedQuota *bool
	var neverExpire *int64
	if ch.Type == storage.ChannelTypeNewAPI {
		t := true
		neg := int64(-1)
		unlimitedQuota = &t
		neverExpire = &neg
	}
	groupName := strings.TrimSpace(target.SourceGroupName)

	var finalKey *connector.APIKey
	if existingKey != nil {
		name := keyName
		var grpPtr *string
		if groupName != "" {
			grpPtr = &groupName
		}
		finalKey, err = s.ChannelAPI.UpdateAPIKey(ctx, target.ChannelID, existingKey.ID, connector.APIKeyUpdateRequest{
			Name:           &name,
			Group:          grpPtr,
			GroupID:        target.SourceGroupID,
			UnlimitedQuota: unlimitedQuota,
			ExpiredTime:    neverExpire,
		})
		if err != nil {
			return nil, err
		}
	} else {
		finalKey, err = s.ChannelAPI.CreateAPIKey(ctx, target.ChannelID, connector.APIKeyCreateRequest{
			Name:           keyName,
			Group:          groupName,
			GroupID:        target.SourceGroupID,
			UnlimitedQuota: unlimitedQuota,
			ExpiredTime:    neverExpire,
		})
		if err != nil {
			return nil, err
		}
	}

	secret, err := s.ChannelAPI.RevealAPIKey(ctx, target.ChannelID, finalKey.ID)
	if err != nil {
		return nil, err
	}
	cipherText, err := s.Cipher.Encrypt(secret)
	if err != nil {
		return nil, err
	}
	hint := maskSecret(secret)
	if err := s.Targets.UpdateKey(target.ID, finalKey.ID, keyName, cipherText, hint); err != nil {
		return nil, err
	}
	return s.Targets.FindByID(target.ID)
}

type resolvedEndpoint struct {
	BaseURL      string
	APIKey       string
	Protocol     string
	AuthStyle    string
	UserAgent    string
	ExtraHeaders map[string]string
	ProxyURL     string
}

func (s *Service) resolveEndpoint(target *storage.ProbeTarget) (*resolvedEndpoint, error) {
	if target == nil {
		return nil, errors.New("nil probe target")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(target.BaseURL), "/")
	if baseURL == "" && target.SourceKind == storage.ProbeSourceChannel && target.ChannelID > 0 && s.Channels != nil {
		ch, err := s.Channels.FindByID(target.ChannelID)
		if err != nil {
			return nil, fmt.Errorf("load channel %d: %w", target.ChannelID, err)
		}
		baseURL = strings.TrimRight(strings.TrimSpace(ch.SiteURL), "/")
	}
	if baseURL == "" {
		return nil, errors.New("target base_url is empty")
	}

	var apiKey string
	if strings.TrimSpace(target.APIKeyCipher) != "" {
		if s.Cipher == nil {
			return nil, errors.New("cipher not initialized")
		}
		plain, err := s.Cipher.Decrypt(target.APIKeyCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypt target api key: %w", err)
		}
		apiKey = strings.TrimSpace(plain)
	}
	if apiKey == "" {
		return nil, errors.New("target api_key is empty (fill manually or run ensure-key)")
	}

	proxyURL, err := s.currentProxyURL(target.ProxyEnabled)
	if err != nil {
		return nil, err
	}
	ua := strings.TrimSpace(target.UserAgent)
	if ua == "" {
		ua = s.currentUpstreamConfig().UserAgent
	}
	var extra map[string]string
	if strings.TrimSpace(target.ExtraHeadersJSON) != "" {
		_ = json.Unmarshal([]byte(target.ExtraHeadersJSON), &extra)
	}

	return &resolvedEndpoint{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Protocol:     normalizeTargetProtocol(target.Protocol),
		AuthStyle:    normalizeAuthStyle(target.AuthStyle, target.Protocol),
		UserAgent:    ua,
		ExtraHeaders: extra,
		ProxyURL:     proxyURL,
	}, nil
}

// FetchTargetModels 拉取该监测目标的上游可用模型列表。
func (s *Service) FetchTargetModels(ctx context.Context, targetID uint) ([]string, error) {
	target, err := s.Targets.FindByID(targetID)
	if err != nil {
		return nil, err
	}
	ep, err := s.resolveEndpoint(target)
	if err != nil {
		return nil, err
	}
	return s.Client.ListModels(ctx, llmclient.Endpoint{
		BaseURL:      ep.BaseURL,
		APIKey:       ep.APIKey,
		AuthStyle:    ep.AuthStyle,
		UserAgent:    ep.UserAgent,
		ExtraHeaders: ep.ExtraHeaders,
		ProxyURL:     ep.ProxyURL,
		Timeout:      time.Duration(s.currentUpstreamConfig().TimeoutSeconds) * time.Second,
	})
}

// RunLivenessNow 立即对指定目标执行一次主动测活并将结果落库。
func (s *Service) RunLivenessNow(ctx context.Context, targetID uint, modelOverride string) (*storage.ProbeLivenessSample, error) {
	target, err := s.Targets.FindByID(targetID)
	if err != nil {
		return nil, err
	}
	ep, err := s.resolveEndpoint(target)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(modelOverride)
	timeout := DefaultLivenessTimeoutSec * time.Second
	if cfg, err := s.Configs.Find(target.ID, DetectorKeyLiveness); err == nil && cfg != nil {
		if model == "" {
			model = strings.TrimSpace(cfg.ModelName)
		}
		if cfg.TimeoutSeconds > 0 {
			timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
		}
	}
	if model == "" {
		return nil, errors.New("model_name is required for liveness probe")
	}

	sample := RunLivenessProbe(ctx, s.Client, llmclient.Request{
		BaseURL:      ep.BaseURL,
		APIKey:       ep.APIKey,
		Protocol:     ep.Protocol,
		AuthStyle:    ep.AuthStyle,
		Model:        model,
		UserAgent:    ep.UserAgent,
		ExtraHeaders: ep.ExtraHeaders,
		ProxyURL:     ep.ProxyURL,
		Timeout:      timeout,
	}, target.ID)

	if err := s.Liveness.Append(sample); err != nil {
		return sample, err
	}
	return sample, nil
}

// GetTargetOverview 获取单目标的 24h 性能汇总、90 分钟趋势、最近采样及检测器配置。
func (s *Service) GetTargetOverview(targetID uint) (*TargetOverview, error) {
	target, err := s.Targets.FindByID(targetID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	samples24h, err := s.Liveness.ListByTargetSince(target.ID, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	perf, trend := AggregateLiveness(samples24h, now)
	recent, err := s.Liveness.ListRecentByTarget(target.ID, 20)
	if err != nil {
		return nil, err
	}
	cfgs, err := s.Configs.ListByTarget(target.ID)
	if err != nil {
		return nil, err
	}
	return &TargetOverview{
		Target:         target,
		Performance:    perf,
		Trend:          trend,
		RecentSamples:  recent,
		DetectorConfig: cfgs,
	}, nil
}

func (s *Service) lockKeyName(name string) func() {
	v, _ := s.keyLocks.LoadOrStore(name, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

var nonSlugCharsRe = regexp.MustCompile(`[^a-z0-9-]+`)

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

func stableProbeKeyName(channelID uint, groupID *int64, groupName string) string {
	if groupID != nil && *groupID > 0 {
		return fmt.Sprintf("uops-probe-ch%d-sg%d", channelID, *groupID)
	}
	if slug := normalizeSlug(groupName); slug != "" {
		return fmt.Sprintf("uops-probe-ch%d-sgn-%s", channelID, slug)
	}
	return fmt.Sprintf("uops-probe-ch%d-default", channelID)
}

func normalizeSourceKind(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), storage.ProbeSourceChannel) {
		return storage.ProbeSourceChannel
	}
	return storage.ProbeSourceDirect
}

func normalizeAPIKeySource(raw, sourceKind string) string {
	if sourceKind == storage.ProbeSourceChannel && strings.EqualFold(strings.TrimSpace(raw), storage.ProbeAPIKeyAutoChannelKey) {
		return storage.ProbeAPIKeyAutoChannelKey
	}
	return storage.ProbeAPIKeyManual
}

func normalizeFamily(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case storage.ProbeFamilyClaude, storage.ProbeFamilyCodex, storage.ProbeFamilyGrok, storage.ProbeFamilyDomestic:
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return storage.ProbeFamilyOther
	}
}

func normalizeTargetProtocol(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case storage.ProbeProtocolOpenAIResponses, "responses":
		return storage.ProbeProtocolOpenAIResponses
	case storage.ProbeProtocolAnthropic, "claude":
		return storage.ProbeProtocolAnthropic
	default:
		return storage.ProbeProtocolOpenAIChat
	}
}

func normalizeAuthStyle(raw, proto string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case storage.GatewayProviderAuthBearer, storage.GatewayProviderAuthXAPIKey, storage.GatewayProviderAuthBoth:
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		if normalizeTargetProtocol(proto) == storage.ProbeProtocolAnthropic {
			return storage.GatewayProviderAuthXAPIKey
		}
		return storage.GatewayProviderAuthBearer
	}
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 8 {
		if len(s) <= 2 {
			return "***"
		}
		return s[:2] + "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

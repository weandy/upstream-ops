package intelligence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

const (
	DefaultRunnerTickInterval = 10 * time.Second
	DefaultRunnerConcurrency  = 3
	MinDetectorIntervalSec    = 60
)

// DetectorConfigInput 目标单个监测插件的保存输入。
type DetectorConfigInput struct {
	DetectorKey     string `json:"detector_key"`
	Enabled         bool   `json:"enabled"`
	ModelName       string `json:"model_name"`
	ReasoningEffort string `json:"reasoning_effort"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	AlertEnabled    bool   `json:"alert_enabled"`
	AlertThreshold  int    `json:"alert_threshold"`
	ParamsJSON      string `json:"params_json"`
	EmbedVisible    bool   `json:"embed_visible"`
}

// RunOptions 执行单次检测的可选控制参数。
type RunOptions struct {
	Manual            bool
	ModelOverride     string
	ReasoningOverride string
	ParamsOverride    string
}

// SetResultHook 注册检测完成后的回调钩子（如告警状态机）。
func (s *Service) SetResultHook(hook ResultHook) {
	s.mu.Lock()
	s.resultHook = hook
	s.mu.Unlock()
}

func (s *Service) currentResultHook() ResultHook {
	s.mu.RLock()
	h := s.resultHook
	s.mu.RUnlock()
	return h
}

// RegisterDetector 向服务动态注册外部或扩展检测插件。
func (s *Service) RegisterDetector(d Detector) error {
	return s.Registry.Register(d)
}

// ListDetectorMetas 返回后台已注册的全部插件元信息列表（供前端动态渲染开关矩阵）。
func (s *Service) ListDetectorMetas() []DetectorMeta {
	return s.Registry.ListMeta()
}

// GetTargetDetectors 查询某目标已保存的全部插件配置。
func (s *Service) GetTargetDetectors(targetID uint) ([]storage.ProbeDetectorConfig, error) {
	if _, err := s.Targets.FindByID(targetID); err != nil {
		return nil, err
	}
	return s.Configs.ListByTarget(targetID)
}

// SaveTargetDetectors 校验并批量保存某目标的插拔式插件配置。
func (s *Service) SaveTargetDetectors(targetID uint, items []DetectorConfigInput) ([]storage.ProbeDetectorConfig, error) {
	if _, err := s.Targets.FindByID(targetID); err != nil {
		return nil, err
	}
	existingList, err := s.Configs.ListByTarget(targetID)
	if err != nil {
		return nil, err
	}
	existingMap := make(map[string]storage.ProbeDetectorConfig, len(existingList))
	for _, c := range existingList {
		existingMap[c.DetectorKey] = c
	}

	now := time.Now().UTC()
	for _, in := range items {
		key := strings.TrimSpace(in.DetectorKey)
		det, ok := s.Registry.Get(key)
		if !ok {
			return nil, fmt.Errorf("unknown detector_key %q", key)
		}
		meta := det.Meta()

		modelName := strings.TrimSpace(in.ModelName)
		if in.Enabled && modelName == "" {
			return nil, fmt.Errorf("detector %q requires model_name when enabled", key)
		}
		if in.Enabled {
			if err := det.ValidateParams(in.ParamsJSON); err != nil {
				return nil, fmt.Errorf("detector %q invalid params: %w", key, err)
			}
		}

		interval := in.IntervalSeconds
		if interval <= 0 {
			interval = meta.DefaultIntervalSec
		}
		if interval < MinDetectorIntervalSec {
			interval = MinDetectorIntervalSec
		}
		timeout := in.TimeoutSeconds
		if timeout <= 0 {
			timeout = meta.DefaultTimeoutSec
		}
		threshold := in.AlertThreshold
		if threshold <= 0 {
			threshold = meta.DefaultAlertThresh
		}
		effort := strings.ToLower(strings.TrimSpace(in.ReasoningEffort))
		if effort == "" {
			effort = "omit"
		}

		var nextRunAt *time.Time
		if prev, hasPrev := existingMap[key]; hasPrev {
			nextRunAt = prev.NextRunAt
			// 从禁用切换为启用、或缩短间隔时，立即安排下一轮执行。
			if in.Enabled && (!prev.Enabled || nextRunAt == nil) {
				t := now
				nextRunAt = &t
			}
		} else if in.Enabled {
			t := now
			nextRunAt = &t
		}
		if !in.Enabled {
			nextRunAt = nil
		}

		cfg := &storage.ProbeDetectorConfig{
			TargetID:          targetID,
			DetectorKey:       key,
			Enabled:           in.Enabled,
			ModelName:         modelName,
			ReasoningEffort:   effort,
			IntervalSeconds:   interval,
			TimeoutSeconds:    timeout,
			AlertEnabled:      in.AlertEnabled,
			AlertThreshold:    threshold,
			ParamsJSON:        strings.TrimSpace(in.ParamsJSON),
			EmbedVisible:      in.EmbedVisible,
			ConsecutiveErrors: 0,
			LastErrorClass:    "",
			BackoffUntil:      nil,
			NextRunAt:         nextRunAt,
		}
		if err := s.Configs.Upsert(cfg); err != nil {
			return nil, err
		}
	}
	s.notifyPublicDataChanged()
	return s.Configs.ListByTarget(targetID)
}

// ExecuteDetector 对指定目标运行指定监测插件（含单目标互斥、同源共享去重、落库与告警钩子）。
func (s *Service) ExecuteDetector(ctx context.Context, targetID uint, detectorKey string, opts RunOptions) (*DetectorRunResult, error) {
	key := strings.TrimSpace(detectorKey)
	det, ok := s.Registry.Get(key)
	if !ok {
		return nil, fmt.Errorf("unknown detector_key %q", key)
	}
	target, err := s.Targets.FindByID(targetID)
	if err != nil {
		return nil, err
	}
	ep, err := s.resolveEndpoint(target)
	if err != nil {
		return nil, err
	}

	cfg, cfgErr := s.Configs.Find(target.ID, key)
	if cfgErr != nil && !errors.Is(cfgErr, gorm.ErrRecordNotFound) {
		return nil, cfgErr
	}
	meta := det.Meta()
	if cfg == nil {
		cfg = &storage.ProbeDetectorConfig{
			TargetID:        target.ID,
			DetectorKey:     key,
			IntervalSeconds: meta.DefaultIntervalSec,
			TimeoutSeconds:  meta.DefaultTimeoutSec,
			AlertThreshold:  meta.DefaultAlertThresh,
			ReasoningEffort: "omit",
		}
	}

	model := strings.TrimSpace(opts.ModelOverride)
	if model == "" {
		model = strings.TrimSpace(cfg.ModelName)
	}
	if model == "" {
		return nil, fmt.Errorf("model_name is required for detector %q", key)
	}
	effort := strings.TrimSpace(opts.ReasoningOverride)
	if effort == "" {
		effort = cfg.ReasoningEffort
	}
	params := strings.TrimSpace(opts.ParamsOverride)
	if params == "" {
		params = strings.TrimSpace(cfg.ParamsJSON)
	}
	if err := det.ValidateParams(params); err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(meta.DefaultTimeoutSec) * time.Second
	}
	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Duration(meta.DefaultIntervalSec) * time.Second
	}

	// 同目标同插件串行互斥，防止慢请求叠加重入。
	unlock := s.lockTargetDetector(target.ID, key)
	defer unlock()

	// 主动测活不做跨目标缓存复用，需要如实采集本目标实时延迟。
	if meta.Category == DetectorCategoryLiveness {
		res, runErr := det.Run(ctx, s.Client, DetectorRunRequest{
			TargetID:   target.ID,
			ParamsJSON: params,
			Request: llmclient.Request{
				BaseURL:      ep.BaseURL,
				APIKey:       ep.APIKey,
				Protocol:     ep.Protocol,
				AuthStyle:    ep.AuthStyle,
				Model:        model,
				UserAgent:    ep.UserAgent,
				ExtraHeaders: ep.ExtraHeaders,
				ProxyURL:     ep.ProxyURL,
				Timeout:      timeout,
			},
		})
		if runErr != nil {
			return nil, runErr
		}
		if res != nil && res.LivenessSample != nil {
			if err := s.Liveness.Append(res.LivenessSample); err != nil {
				return res, err
			}
			if opts.Manual && res.LivenessSample.Success {
				_ = s.Configs.ResetConfigBackoff(target.ID, key)
			}
		}
		if opts.Manual {
			s.notifyPublicDataChanged()
		}
		if hook := s.currentResultHook(); hook != nil && res != nil {
			hook(ctx, target, cfg, res)
		}
		return res, nil
	}

	// 智力检测：支持同源 SHA-256 共享去重（数据库 TTL 复用 + singleflight 并发合并）。
	sharedKey := computeSharedCacheKey(ep, key, model, effort, params)
	now := time.Now().UTC()

	if !opts.Manual {
		cached, err := s.Runs.FindLatestFreshBySharedKey(sharedKey, now.Add(-interval))
		if err != nil {
			return nil, err
		}
		if cached != nil {
			reusedID := cached.ID
			run := &storage.ProbeIntelligenceRun{
				TargetID:        target.ID,
				DetectorKey:     key,
				ModelName:       model,
				ReasoningEffort: effort,
				Status:          cached.Status,
				Summary:         cached.Summary,
				ClaimedRank:     cached.ClaimedRank,
				TopCandidate:    cached.TopCandidate,
				LatencyMS:       cached.LatencyMS,
				FirstTokenMS:    cached.FirstTokenMS,
				OutputTokens:    cached.OutputTokens,
				DetailJSON:      cached.DetailJSON,
				ArtifactSVG:     cached.ArtifactSVG,
				ErrorMessage:    cached.ErrorMessage,
				SharedCacheKey:  sharedKey,
				ReusedFromRunID: &reusedID,
				ObservedAt:      now,
			}
			if err := s.Runs.Append(run); err != nil {
				return nil, err
			}
			res := runToResult(run)
			if hook := s.currentResultHook(); hook != nil {
				hook(ctx, target, cfg, res)
			}
			return res, nil
		}
	}

	res, shared, runErr := s.inflight.Do(sharedKey, func() (*DetectorRunResult, error) {
		return det.Run(ctx, s.Client, DetectorRunRequest{
			TargetID:   target.ID,
			ParamsJSON: params,
			Request: llmclient.Request{
				BaseURL:         ep.BaseURL,
				APIKey:          ep.APIKey,
				Protocol:        ep.Protocol,
				AuthStyle:       ep.AuthStyle,
				Model:           model,
				ReasoningEffort: effort,
				UserAgent:       ep.UserAgent,
				ExtraHeaders:    ep.ExtraHeaders,
				ProxyURL:        ep.ProxyURL,
				Timeout:         timeout,
			},
		})
	})
	if runErr != nil {
		return nil, runErr
	}
	if res == nil {
		return nil, errors.New("detector returned empty result")
	}

	run := &storage.ProbeIntelligenceRun{
		TargetID:        target.ID,
		DetectorKey:     key,
		ModelName:       model,
		ReasoningEffort: effort,
		Status:          res.Status,
		Summary:         res.Summary,
		ClaimedRank:     res.ClaimedRank,
		TopCandidate:    res.TopCandidate,
		LatencyMS:       res.LatencyMS,
		FirstTokenMS:    res.FirstTokenMS,
		OutputTokens:    res.OutputTokens,
		DetailJSON:      res.DetailJSON,
		ArtifactSVG:     res.ArtifactSVG,
		ErrorMessage:    res.ErrorMessage,
		SharedCacheKey:  sharedKey,
		ObservedAt:      time.Now().UTC(),
	}
	if shared && s.Log != nil {
		s.Log.Debug("intelligence detector coalesced via singleflight", "target_id", target.ID, "detector", key)
	}
	if err := s.Runs.Append(run); err != nil {
		return res, err
	}
	if opts.Manual {
		if res.Status != storage.IntelligenceStatusError {
			_ = s.Configs.ResetConfigBackoff(target.ID, key)
		}
		s.notifyPublicDataChanged()
	}
	if hook := s.currentResultHook(); hook != nil {
		hook(ctx, target, cfg, res)
	}
	return res, nil
}

// RunEnabledDetectors 手动依次触发某目标下所有已启用的检测插件。
func (s *Service) RunEnabledDetectors(ctx context.Context, targetID uint) (map[string]*DetectorRunResult, error) {
	cfgs, err := s.GetTargetDetectors(targetID)
	if err != nil {
		return nil, err
	}
	results := make(map[string]*DetectorRunResult)
	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}
		res, err := s.ExecuteDetector(ctx, targetID, cfg.DetectorKey, RunOptions{Manual: true})
		if err != nil {
			results[cfg.DetectorKey] = &DetectorRunResult{
				Status:       storage.IntelligenceStatusError,
				Summary:      "执行失败",
				ErrorMessage: err.Error(),
			}
			continue
		}
		results[cfg.DetectorKey] = res
	}
	return results, nil
}

// ListTargetRuns 查询某目标某插件的历史检测记录（不含 artifact_svg）。
func (s *Service) ListTargetRuns(targetID uint, detectorKey string, limit int) ([]storage.ProbeIntelligenceRun, error) {
	if _, err := s.Targets.FindByID(targetID); err != nil {
		return nil, err
	}
	return s.Runs.ListByTargetAndDetector(targetID, detectorKey, limit)
}

// GetRunArtifact 按 UUID 获取单条检测记录的完整内容（含 Pelican SVG）。
func (s *Service) GetRunArtifact(runUUID string) (*storage.ProbeIntelligenceRun, error) {
	return s.Runs.FindByUUID(runUUID)
}

func normalizeRunnerParams(tickInterval time.Duration, concurrency int) (time.Duration, int) {
	if tickInterval <= 0 {
		tickInterval = DefaultRunnerTickInterval
	}
	if concurrency <= 0 {
		concurrency = DefaultRunnerConcurrency
	}
	if concurrency > 32 {
		concurrency = 32
	}
	return tickInterval, concurrency
}

// Start 启动独立后台 Tick 调度器，定期扫描到期任务并以有界并发执行。
func (s *Service) Start(tickInterval time.Duration, concurrency int) {
	tickInterval, concurrency = normalizeRunnerParams(tickInterval, concurrency)

	s.runnerMu.Lock()
	defer s.runnerMu.Unlock()
	if s.runnerRunning {
		return
	}

	s.shutdownCtx, s.shutdownCancel = context.WithCancel(context.Background())
	s.runnerRunning = true
	s.startTickerLocked(tickInterval, concurrency)
}

// Reconfigure 平滑热重建后台调度器与并发 Worker 池，不阻塞等待已派发的长耗时在途任务。
// 若调度器未启动或参数未变化，返回 false 且不做任何重启。
func (s *Service) Reconfigure(tickInterval time.Duration, concurrency int) bool {
	tickInterval, concurrency = normalizeRunnerParams(tickInterval, concurrency)

	s.runnerMu.Lock()
	if !s.runnerRunning {
		s.runnerMu.Unlock()
		return false
	}
	if s.runnerTick == tickInterval && s.runnerConcurrency == concurrency {
		s.runnerMu.Unlock()
		return false
	}

	oldCancel := s.tickCancel
	s.tickCancel = nil
	s.runnerMu.Unlock()

	// 毫秒级停止旧代 Ticker 循环（不再拾取新任务），已在途的慢任务仍持 shutdownCtx 优雅完成。
	if oldCancel != nil {
		oldCancel()
	}
	s.tickWG.Wait()

	s.runnerMu.Lock()
	defer s.runnerMu.Unlock()
	if !s.runnerRunning {
		return false
	}
	s.startTickerLocked(tickInterval, concurrency)
	return true
}

func (s *Service) startTickerLocked(tickInterval time.Duration, concurrency int) {
	tickCtx, tickCancel := context.WithCancel(s.shutdownCtx)
	s.tickCancel = tickCancel
	s.runnerTick = tickInterval
	s.runnerConcurrency = concurrency
	s.runnerGen++
	gen := s.runnerGen
	sem := make(chan struct{}, concurrency)
	jobCtx := s.shutdownCtx

	s.tickWG.Add(1)
	go func() {
		defer s.tickWG.Done()
		s.dispatchDueJobs(tickCtx, jobCtx, sem)
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-tickCtx.Done():
				return
			case <-ticker.C:
				s.dispatchDueJobs(tickCtx, jobCtx, sem)
			}
		}
	}()
	if s.Log != nil {
		s.Log.Info("intelligence runner generation started",
			"generation", gen,
			"tick_interval", tickInterval.String(),
			"concurrency", concurrency,
		)
	}
}

// RunnerStats 返回调度器当前运行状态（供测试与诊断）。
func (s *Service) RunnerStats() (running bool, tick time.Duration, concurrency int, gen uint64) {
	s.runnerMu.Lock()
	defer s.runnerMu.Unlock()
	return s.runnerRunning, s.runnerTick, s.runnerConcurrency, s.runnerGen
}

// Stop 优雅停止后台调度器并等待所有在途 Worker 结束。
func (s *Service) Stop() {
	s.runnerMu.Lock()
	tickCancel := s.tickCancel
	shutdownCancel := s.shutdownCancel
	s.tickCancel = nil
	s.shutdownCancel = nil
	s.runnerRunning = false
	s.runnerMu.Unlock()

	if tickCancel != nil {
		tickCancel()
	}
	s.tickWG.Wait()

	if shutdownCancel != nil {
		shutdownCancel()
	}
	s.jobWG.Wait()
}

func (s *Service) dispatchDueJobs(tickCtx, jobCtx context.Context, sem chan struct{}) {
	now := time.Now().UTC()
	dueList, err := s.Configs.ListDue(now)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("list due probe detector configs failed", "err", err)
		}
		return
	}
	for _, cfg := range dueList {
		if tickCtx.Err() != nil {
			return
		}
		cfg := cfg
		interval := time.Duration(cfg.IntervalSeconds) * time.Second
		if interval < MinDetectorIntervalSec*time.Second {
			interval = MinDetectorIntervalSec * time.Second
		}
		// 抢先推进 next_run_at，避免下个 Tick 重复拾取仍在执行中的慢任务。
		if err := s.Configs.UpdateSchedule(cfg.ID, now, now.Add(interval)); err != nil {
			if s.Log != nil {
				s.Log.Warn("advance probe schedule failed", "config_id", cfg.ID, "err", err)
			}
			continue
		}

		select {
		case sem <- struct{}{}:
		case <-tickCtx.Done():
			return
		}

		s.jobWG.Add(1)
		go func() {
			defer s.jobWG.Done()
			defer func() { <-sem }()
			res, execErr := s.ExecuteDetector(jobCtx, cfg.TargetID, cfg.DetectorKey, RunOptions{Manual: false})
			finished := time.Now().UTC()
			if jobCtx.Err() != nil {
				return
			}
			decision := ComputeNextSchedule(s.currentBackoffPolicy(), &cfg, res, execErr, finished, nil)
			_ = s.Configs.UpdateScheduleWithBackoff(
				cfg.ID,
				finished,
				decision.NextRunAt,
				decision.ConsecutiveErrors,
				decision.LastErrorClass,
				decision.BackoffUntil,
			)
			if execErr != nil && s.Log != nil {
				s.Log.Warn("scheduled probe execution failed",
					"target_id", cfg.TargetID,
					"detector", cfg.DetectorKey,
					"error_class", decision.LastErrorClass,
					"consecutive_errors", decision.ConsecutiveErrors,
					"next_run_at", decision.NextRunAt.Format(time.RFC3339),
					"err", execErr,
				)
			}
		}()
	}
}

func (s *Service) lockTargetDetector(targetID uint, key string) func() {
	lockKey := fmt.Sprintf("%d:%s", targetID, key)
	v, _ := s.targetDetectorLocks.LoadOrStore(lockKey, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func computeSharedCacheKey(ep *resolvedEndpoint, detectorKey, model, effort, params string) string {
	keyHash := sha256.Sum256([]byte(ep.APIKey))
	canonicalParams := canonicalizeJSON(params)
	headerKeys := make([]string, 0, len(ep.ExtraHeaders))
	for k := range ep.ExtraHeaders {
		headerKeys = append(headerKeys, strings.ToLower(strings.TrimSpace(k)))
	}
	sort.Strings(headerKeys)
	var headerPart strings.Builder
	for _, k := range headerKeys {
		headerPart.WriteString(k)
		headerPart.WriteByte('=')
		headerPart.WriteString(ep.ExtraHeaders[k])
		headerPart.WriteByte(';')
	}
	raw := strings.Join([]string{
		strings.ToLower(strings.TrimRight(ep.BaseURL, "/")),
		hex.EncodeToString(keyHash[:]),
		ep.Protocol,
		ep.AuthStyle,
		strings.TrimSpace(model),
		strings.ToLower(strings.TrimSpace(effort)),
		strings.TrimSpace(detectorKey),
		canonicalParams,
		strings.TrimSpace(ep.UserAgent),
		strings.TrimSpace(ep.ProxyURL),
		headerPart.String(),
	}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func canonicalizeJSON(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

func runToResult(r *storage.ProbeIntelligenceRun) *DetectorRunResult {
	return &DetectorRunResult{
		Status:       r.Status,
		Summary:      r.Summary,
		ClaimedRank:  r.ClaimedRank,
		TopCandidate: r.TopCandidate,
		FirstTokenMS: r.FirstTokenMS,
		LatencyMS:    r.LatencyMS,
		OutputTokens: r.OutputTokens,
		DetailJSON:   r.DetailJSON,
		ArtifactSVG:  r.ArtifactSVG,
		ErrorMessage: r.ErrorMessage,
	}
}

// inflightGroup 轻量级泛型 singleflight，合并同一时刻的同源探测请求，零外部依赖。
type inflightCall struct {
	wg  sync.WaitGroup
	val *DetectorRunResult
	err error
}

type inflightGroup struct {
	mu sync.Mutex
	m  map[string]*inflightCall
}

func (g *inflightGroup) Do(key string, fn func() (*DetectorRunResult, error)) (*DetectorRunResult, bool, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*inflightCall)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		if c.val == nil {
			return nil, true, c.err
		}
		cp := *c.val
		return &cp, true, c.err
	}
	c := &inflightCall{}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	if c.val == nil {
		return nil, false, c.err
	}
	cp := *c.val
	return &cp, false, c.err
}

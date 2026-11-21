package intelligence

import (
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

// BackoffPolicy 异常目标分级指数退避策略。
type BackoffPolicy struct {
	Enabled      bool
	TransientMax time.Duration
	RateLimitMax time.Duration
	AuthMax      time.Duration
}

// DefaultBackoffPolicy 返回推荐的生产默认退避策略（1h 瞬时 / 2h 限流 / 6h 鉴权配置）。
func DefaultBackoffPolicy() BackoffPolicy {
	return BackoffPolicyFromConfig(config.IntelligenceConfig{}.WithDefaults())
}

// BackoffPolicyFromConfig 从全局 IntelligenceConfig 构造退避策略。
func BackoffPolicyFromConfig(cfg config.IntelligenceConfig) BackoffPolicy {
	c := cfg.WithDefaults()
	return BackoffPolicy{
		Enabled:      c.IsBackoffEnabled(),
		TransientMax: time.Duration(c.BackoffTransientMaxSeconds) * time.Second,
		RateLimitMax: time.Duration(c.BackoffRateLimitMaxSeconds) * time.Second,
		AuthMax:      time.Duration(c.BackoffAuthMaxSeconds) * time.Second,
	}
}

// BackoffDecision 一次定时探测完成后的下次调度决策与持久化退避态。
type BackoffDecision struct {
	NextRunAt         time.Time
	ConsecutiveErrors int
	LastErrorClass    string
	BackoffUntil      *time.Time
}

// defaultJitterFactor 返回 [0.9, 1.1] 区间的均匀抖动倍率，防多目标同秒惊群。
func defaultJitterFactor() float64 {
	return 0.9 + rand.Float64()*0.2
}

// ComputeNextSchedule 根据探测结果与错误类型计算下一次调度时间及退避状态。
//
// 核心约束：
//  1. normal 与 suspected（疑似降智）永不退避，且清零连续错误计数；
//  2. rate_limit（HTTP 429）首次即启动指数退避，并优先尊重上游 Retry-After；
//  3. auth/config 及瞬时错误（timeout/network/upstream/truncated）在连续错误未达 alert_threshold 前不退避，
//     确保首次掉线告警准点送达；达阈值后启动分级指数退避。
func ComputeNextSchedule(
	policy BackoffPolicy,
	cfg *storage.ProbeDetectorConfig,
	res *DetectorRunResult,
	execErr error,
	now time.Time,
	jitterFn func() float64,
) BackoffDecision {
	nowUTC := now.UTC()
	base := time.Duration(MinDetectorIntervalSec) * time.Second
	threshold := 3
	prevErrors := 0
	if cfg != nil {
		if cfg.IntervalSeconds > MinDetectorIntervalSec {
			base = time.Duration(cfg.IntervalSeconds) * time.Second
		}
		if cfg.AlertThreshold > 0 {
			threshold = cfg.AlertThreshold
		}
		if cfg.ConsecutiveErrors > 0 {
			prevErrors = cfg.ConsecutiveErrors
		}
	}

	// 成功或 suspected（模型正常返回但智力异常）均清零退避，维持原周期监测。
	if execErr == nil && res != nil && res.Status != storage.IntelligenceStatusError {
		return BackoffDecision{
			NextRunAt:         nowUTC.Add(base),
			ConsecutiveErrors: 0,
			LastErrorClass:    "",
			BackoffUntil:      nil,
		}
	}

	nextErrors := prevErrors + 1
	errClass := resolveFailureErrorClass(res, execErr)

	if !policy.Enabled {
		return BackoffDecision{
			NextRunAt:         nowUTC.Add(base),
			ConsecutiveErrors: nextErrors,
			LastErrorClass:    errClass,
			BackoffUntil:      nil,
		}
	}

	retryAfter := time.Duration(0)
	if res != nil && res.RetryAfterSec > 0 {
		retryAfter = time.Duration(res.RetryAfterSec) * time.Second
	}

	var (
		wait         = base
		shouldBackoff bool
		step         int
		capDur       time.Duration
	)

	switch errClass {
	case llmclient.ErrorClassRateLimit:
		shouldBackoff = true
		step = nextErrors
		capDur = policy.RateLimitMax
		if capDur < base {
			capDur = base
		}
	case llmclient.ErrorClassAuth, llmclient.ErrorClassConfig:
		if nextErrors >= threshold {
			shouldBackoff = true
			step = nextErrors - threshold + 1
			capDur = policy.AuthMax
			if capDur < base {
				capDur = base
			}
		}
	default:
		if nextErrors >= threshold {
			shouldBackoff = true
			step = nextErrors - threshold + 1
			capDur = policy.TransientMax
			if capDur < base {
				capDur = base
			}
		}
	}

	if shouldBackoff {
		mult := math.Pow(2, float64(minInt(step, 16)))
		rawWait := time.Duration(float64(base) * mult)
		if rawWait <= 0 || rawWait > capDur {
			rawWait = capDur
		}
		if retryAfter > rawWait {
			rawWait = retryAfter
			if rawWait > capDur {
				rawWait = capDur
			}
		}
		if jitterFn == nil {
			jitterFn = defaultJitterFactor
		}
		j := jitterFn()
		if j <= 0 {
			j = 1.0
		}
		wait = time.Duration(float64(rawWait) * j)
		if wait < base {
			wait = base
		}
		if wait > capDur {
			wait = capDur
		}
	}

	nextAt := nowUTC.Add(wait)
	var backoffUntil *time.Time
	if shouldBackoff && wait > base {
		ts := nextAt
		backoffUntil = &ts
	}
	return BackoffDecision{
		NextRunAt:         nextAt,
		ConsecutiveErrors: nextErrors,
		LastErrorClass:    errClass,
		BackoffUntil:      backoffUntil,
	}
}

func resolveFailureErrorClass(res *DetectorRunResult, execErr error) string {
	if res != nil {
		if c := strings.ToLower(strings.TrimSpace(res.ErrorClass)); c != "" && c != llmclient.ErrorClassOK {
			return c
		}
		if res.LivenessSample != nil {
			if c := strings.ToLower(strings.TrimSpace(res.LivenessSample.ErrorClass)); c != "" && c != llmclient.ErrorClassOK {
				return c
			}
		}
	}
	if execErr != nil {
		msg := strings.ToLower(execErr.Error())
		switch {
		case strings.Contains(msg, "api_key") || strings.Contains(msg, "base_url") || strings.Contains(msg, "model_name"):
			return llmclient.ErrorClassConfig
		case strings.Contains(msg, "401") || strings.Contains(msg, "403"):
			return llmclient.ErrorClassAuth
		case strings.Contains(msg, "429"):
			return llmclient.ErrorClassRateLimit
		case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
			return llmclient.ErrorClassTimeout
		default:
			return llmclient.ErrorClassNetwork
		}
	}
	return llmclient.ErrorClassUpstream
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

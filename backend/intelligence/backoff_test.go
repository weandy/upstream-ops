package intelligence

import (
	"errors"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

func noJitter() float64 { return 1.0 }

func TestComputeNextScheduleNormalAndSuspectedNeverBackoff(t *testing.T) {
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	policy := DefaultBackoffPolicy()
	cfg := &storage.ProbeDetectorConfig{
		IntervalSeconds:   180,
		AlertThreshold:    3,
		ConsecutiveErrors: 5,
		LastErrorClass:    llmclient.ErrorClassAuth,
	}

	// normal 必须立刻清零退避。
	dNormal := ComputeNextSchedule(policy, cfg, &DetectorRunResult{Status: storage.IntelligenceStatusNormal}, nil, now, noJitter)
	if dNormal.ConsecutiveErrors != 0 || dNormal.BackoffUntil != nil || !dNormal.NextRunAt.Equal(now.Add(180*time.Second)) {
		t.Fatalf("normal should reset backoff, got %+v", dNormal)
	}

	// suspected（模型降智/答错）同样绝不退避并清零错误计数。
	dSusp := ComputeNextSchedule(policy, cfg, &DetectorRunResult{Status: storage.IntelligenceStatusSuspected}, nil, now, noJitter)
	if dSusp.ConsecutiveErrors != 0 || dSusp.BackoffUntil != nil || !dSusp.NextRunAt.Equal(now.Add(180*time.Second)) {
		t.Fatalf("suspected must never backoff, got %+v", dSusp)
	}
}

func TestComputeNextScheduleTransientAlertThresholdAndCap(t *testing.T) {
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	policy := DefaultBackoffPolicy()
	errRes := &DetectorRunResult{
		Status:     storage.IntelligenceStatusError,
		ErrorClass: llmclient.ErrorClassUpstream,
	}

	// 第 1、2 次失败（< alert_threshold=3）不退避，确保准点触发连续 3 次掉线告警。
	cfg := &storage.ProbeDetectorConfig{IntervalSeconds: 180, AlertThreshold: 3, ConsecutiveErrors: 0}
	d1 := ComputeNextSchedule(policy, cfg, errRes, nil, now, noJitter)
	if d1.ConsecutiveErrors != 1 || d1.BackoffUntil != nil || !d1.NextRunAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("1st transient error should keep base interval, got %+v", d1)
	}
	cfg.ConsecutiveErrors = d1.ConsecutiveErrors
	d2 := ComputeNextSchedule(policy, cfg, errRes, nil, now, noJitter)
	if d2.ConsecutiveErrors != 2 || d2.BackoffUntil != nil || !d2.NextRunAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("2nd transient error should keep base interval, got %+v", d2)
	}

	// 第 3 次失败（达到 alert_threshold）开始指数退避：step=1 -> 6m；第 4 次 -> 12m；多次后封顶 1h。
	cfg.ConsecutiveErrors = d2.ConsecutiveErrors
	d3 := ComputeNextSchedule(policy, cfg, errRes, nil, now, noJitter)
	if d3.ConsecutiveErrors != 3 || d3.BackoffUntil == nil || !d3.NextRunAt.Equal(now.Add(6*time.Minute)) {
		t.Fatalf("3rd transient error expected 6m backoff, got %+v", d3)
	}

	cfg.ConsecutiveErrors = 10
	dCap := ComputeNextSchedule(policy, cfg, errRes, nil, now, noJitter)
	if !dCap.NextRunAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("transient backoff should cap at 1h, got %v", dCap.NextRunAt.Sub(now))
	}
}

func TestComputeNextScheduleRateLimitImmediateAndRetryAfterAndAuthCap(t *testing.T) {
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	policy := DefaultBackoffPolicy()

	// 429 首次即退避，且优先取上游 Retry-After（例如 900 秒 > 360 秒）。
	cfg := &storage.ProbeDetectorConfig{IntervalSeconds: 180, AlertThreshold: 3, ConsecutiveErrors: 0}
	res429 := &DetectorRunResult{
		Status:        storage.IntelligenceStatusError,
		ErrorClass:    llmclient.ErrorClassRateLimit,
		RetryAfterSec: 900,
	}
	d429 := ComputeNextSchedule(policy, cfg, res429, nil, now, noJitter)
	if d429.ConsecutiveErrors != 1 || d429.BackoffUntil == nil || !d429.NextRunAt.Equal(now.Add(900*time.Second)) {
		t.Fatalf("429 should respect Retry-After 900s on first hit, got %+v", d429)
	}

	// 401 鉴权错误连续多次失败应封顶 6 小时。
	cfgAuth := &storage.ProbeDetectorConfig{IntervalSeconds: 180, AlertThreshold: 3, ConsecutiveErrors: 12}
	dAuth := ComputeNextSchedule(policy, cfgAuth, nil, errors.New("HTTP 401: invalid api key"), now, noJitter)
	if dAuth.LastErrorClass != llmclient.ErrorClassAuth || !dAuth.NextRunAt.Equal(now.Add(6*time.Hour)) {
		t.Fatalf("auth error should cap at 6h, got class=%q wait=%v", dAuth.LastErrorClass, dAuth.NextRunAt.Sub(now))
	}

	// 关闭退避开关时保持基础间隔，但仍记录错误次数与类型。
	policyOff := policy
	policyOff.Enabled = false
	dOff := ComputeNextSchedule(policyOff, cfgAuth, res429, nil, now, noJitter)
	if dOff.BackoffUntil != nil || !dOff.NextRunAt.Equal(now.Add(180*time.Second)) || dOff.ConsecutiveErrors != 13 {
		t.Fatalf("backoff disabled should keep base interval, got %+v", dOff)
	}
}

package intelligence

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

type countingStubLLM struct {
	calls   atomic.Int32
	delay   time.Duration
	resp    *llmclient.Response
	err     error
	onEnter chan struct{}
}

func (c *countingStubLLM) Complete(ctx context.Context, _ llmclient.Request) (*llmclient.Response, error) {
	c.calls.Add(1)
	if c.onEnter != nil {
		select {
		case c.onEnter <- struct{}{}:
		default:
		}
	}
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.resp == nil {
		return nil, c.err
	}
	cp := *c.resp
	return &cp, c.err
}

func (c *countingStubLLM) ListModels(_ context.Context, _ llmclient.Endpoint) ([]string, error) {
	return []string{"gpt-6.2-sol"}, nil
}

func TestSaveTargetDetectorsAndSharedCacheDeduplication(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("runner-test-secret-32-bytes!!!!!!")
	stub := &countingStubLLM{
		resp: &llmclient.Response{
			Content:      "21",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 500,
			LatencyMS:    1200,
			OutputTokens: 12,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	svc := NewService(db, cipher, nil, stub, nil)

	// 校验默认注册了内置插件。
	metas := svc.ListDetectorMetas()
	if len(metas) < 4 {
		t.Fatalf("expected at least 4 built-in detectors, got %d", len(metas))
	}

	// 创建两个指向同一上游 + 同 APIKey 的目标。
	t1, err := svc.CreateTarget(TargetInput{
		Name:       "group-a",
		SourceKind: storage.ProbeSourceDirect,
		BaseURL:    "https://same-upstream.example.com",
		APIKey:     "sk-shared-key-123456",
		Protocol:   storage.ProbeProtocolOpenAIChat,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create t1: %v", err)
	}
	t2, err := svc.CreateTarget(TargetInput{
		Name:       "group-b",
		SourceKind: storage.ProbeSourceDirect,
		BaseURL:    "https://same-upstream.example.com",
		APIKey:     "sk-shared-key-123456",
		Protocol:   storage.ProbeProtocolOpenAIChat,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create t2: %v", err)
	}

	// 非法插件名应被拒绝。
	if _, err := svc.SaveTargetDetectors(t1.ID, []DetectorConfigInput{
		{DetectorKey: "unknown_plugin", Enabled: true, ModelName: "gpt-6.2-sol"},
	}); err == nil {
		t.Fatal("expected error for unregistered detector_key")
	}

	// 未启用的 custom_prompt 即使携带前端默认参数（无 prompt/expected）也绝不应阻塞其他插件保存；
	// 但若显式启用 custom_prompt 且缺必填项则必须拒绝。
	if _, err := svc.SaveTargetDetectors(t1.ID, []DetectorConfigInput{
		{
			DetectorKey: DetectorKeyLiveness,
			Enabled:     true,
			ModelName:   "gpt-6.2-sol",
		},
		{
			DetectorKey: DetectorKeyCustomPrompt,
			Enabled:     false,
			ParamsJSON:  `{"match_mode":"contains","case_sensitive":false,"max_tokens":1024}`,
		},
	}); err != nil {
		t.Fatalf("expected disabled custom_prompt with default params to save cleanly, got: %v", err)
	}
	if _, err := svc.SaveTargetDetectors(t1.ID, []DetectorConfigInput{
		{
			DetectorKey: DetectorKeyCustomPrompt,
			Enabled:     true,
			ModelName:   "gpt-6.2-sol",
			ParamsJSON:  `{"match_mode":"contains","case_sensitive":false,"max_tokens":1024}`,
		},
	}); err == nil {
		t.Fatal("expected error when enabling custom_prompt without required prompt/expected")
	}

	// 开启两个目标的 candy 监测。
	for _, tid := range []uint{t1.ID, t2.ID} {
		if _, err := svc.SaveTargetDetectors(tid, []DetectorConfigInput{
			{
				DetectorKey:     DetectorKeyCandy,
				Enabled:         true,
				ModelName:       "gpt-6.2-sol",
				IntervalSeconds: 3600,
				TimeoutSeconds:  60,
				AlertEnabled:    true,
				AlertThreshold:  3,
				EmbedVisible:    true,
			},
		}); err != nil {
			t.Fatalf("save detectors for %d: %v", tid, err)
		}
	}

	// 1. 定时路径执行 t1，应调用上游 1 次。
	res1, err := svc.ExecuteDetector(context.Background(), t1.ID, DetectorKeyCandy, RunOptions{Manual: false})
	if err != nil || res1.Status != storage.IntelligenceStatusNormal {
		t.Fatalf("execute t1 failed: res=%+v err=%v", res1, err)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("expected 1 upstream call after t1, got %d", stub.calls.Load())
	}

	// 2. 定时路径执行同源 t2，在 TTL 内应直接复用 t1 的结果，不增加上游调用！
	res2, err := svc.ExecuteDetector(context.Background(), t2.ID, DetectorKeyCandy, RunOptions{Manual: false})
	if err != nil || res2.Status != storage.IntelligenceStatusNormal {
		t.Fatalf("execute t2 failed: res=%+v err=%v", res2, err)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("expected shared cache reuse (still 1 upstream call), got %d", stub.calls.Load())
	}

	runsT2, err := svc.Runs.ListByTargetAndDetector(t2.ID, DetectorKeyCandy, 10)
	if err != nil || len(runsT2) != 1 || runsT2[0].ReusedFromRunID == nil {
		t.Fatalf("expected t2 run marked with ReusedFromRunID, got %+v err=%v", runsT2, err)
	}

	// 3. 手动触发 t2 应跳过旧缓存，强制重新调用上游！
	if _, err := svc.ExecuteDetector(context.Background(), t2.ID, DetectorKeyCandy, RunOptions{Manual: true}); err != nil {
		t.Fatalf("manual execute t2 failed: %v", err)
	}
	if stub.calls.Load() != 2 {
		t.Fatalf("expected manual run to bypass completed cache (2 calls), got %d", stub.calls.Load())
	}
}

func TestSingleflightInflightDeduplication(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("singleflight-secret-32-bytes!!!!!")
	stub := &countingStubLLM{
		delay: 80 * time.Millisecond,
		resp: &llmclient.Response{
			Content:      "21",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 50,
			LatencyMS:    80,
			OutputTokens: 5,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	svc := NewService(db, cipher, nil, stub, nil)

	t1, _ := svc.CreateTarget(TargetInput{
		Name: "sf-1", SourceKind: storage.ProbeSourceDirect,
		BaseURL: "https://sf.example.com", APIKey: "sk-sf-1234", Protocol: storage.ProbeProtocolOpenAIChat, Enabled: true,
	})
	t2, _ := svc.CreateTarget(TargetInput{
		Name: "sf-2", SourceKind: storage.ProbeSourceDirect,
		BaseURL: "https://sf.example.com", APIKey: "sk-sf-1234", Protocol: storage.ProbeProtocolOpenAIChat, Enabled: true,
	})
	for _, tid := range []uint{t1.ID, t2.ID} {
		_, _ = svc.SaveTargetDetectors(tid, []DetectorConfigInput{
			{DetectorKey: DetectorKeyCandy, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 3600},
		})
	}

	// 并发同时触发 t1 和 t2，应通过 singleflight 合并为 1 次上游 HTTP。
	var wg sync.WaitGroup
	wg.Add(2)
	for _, tid := range []uint{t1.ID, t2.ID} {
		tid := tid
		go func() {
			defer wg.Done()
			if _, err := svc.ExecuteDetector(context.Background(), tid, DetectorKeyCandy, RunOptions{Manual: true}); err != nil {
				t.Errorf("concurrent execute %d failed: %v", tid, err)
			}
		}()
	}
	wg.Wait()

	if stub.calls.Load() != 1 {
		t.Fatalf("expected singleflight to coalesce 2 concurrent identical probes into 1 call, got %d", stub.calls.Load())
	}
}

func TestBackgroundRunnerExecutesDueJobsAndStopsCleanly(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("bg-runner-secret-32-bytes!!!!!!!!")
	stub := &countingStubLLM{
		resp: &llmclient.Response{
			Content:      "A quiet sunrise illuminates the sea.",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 100,
			LatencyMS:    200,
			OutputTokens: 10,
			TPS:          100,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	svc := NewService(db, cipher, nil, stub, nil)
	target, _ := svc.CreateTarget(TargetInput{
		Name: "bg-target", SourceKind: storage.ProbeSourceDirect,
		BaseURL: "https://bg.example.com", APIKey: "sk-bg-1234", Protocol: storage.ProbeProtocolOpenAIChat, Enabled: true,
	})
	_, err := svc.SaveTargetDetectors(target.ID, []DetectorConfigInput{
		{DetectorKey: DetectorKeyLiveness, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 180},
	})
	if err != nil {
		t.Fatalf("save liveness config: %v", err)
	}

	svc.Start(20*time.Millisecond, 2)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stub.calls.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	svc.Stop()

	if stub.calls.Load() != 1 {
		t.Fatalf("expected runner to execute due liveness job exactly once, got %d", stub.calls.Load())
	}
	samples, err := svc.Liveness.ListRecentByTarget(target.ID, 5)
	if err != nil || len(samples) != 1 || !samples[0].Success {
		t.Fatalf("expected 1 persisted liveness sample, got %+v err=%v", samples, err)
	}
	cfg, err := svc.Configs.Find(target.ID, DetectorKeyLiveness)
	if err != nil || cfg.LastRunAt == nil || cfg.NextRunAt == nil || !cfg.NextRunAt.After(time.Now().Add(2*time.Minute)) {
		t.Fatalf("expected schedule advanced by 180s, got %+v err=%v", cfg, err)
	}
}

func TestReconfigureNonBlockingWhileSlowJobInflightAndIdempotent(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("reconf-test-secret-32-bytes!!!!!!")
	entered := make(chan struct{}, 2)
	stub := &countingStubLLM{
		delay:   500 * time.Millisecond,
		onEnter: entered,
		resp: &llmclient.Response{
			Content:      "A quiet sunrise illuminates the sea.",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 120,
			LatencyMS:    500,
			OutputTokens: 12,
			TPS:          48,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	svc := NewService(db, cipher, nil, stub, nil)
	target, err := svc.CreateTarget(TargetInput{
		Name: "slow-target", SourceKind: storage.ProbeSourceDirect,
		BaseURL: "https://slow.example.com", APIKey: "sk-slow-1234", Protocol: storage.ProbeProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := svc.SaveTargetDetectors(target.ID, []DetectorConfigInput{
		{DetectorKey: DetectorKeyLiveness, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 180},
	}); err != nil {
		t.Fatalf("save liveness config: %v", err)
	}

	svc.Start(15*time.Millisecond, 1)
	defer svc.Stop()

	// 等待慢任务进入执行中。
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow job to start")
	}

	// 慢任务在途期间热重建调度器：必须在毫秒级返回，严禁阻塞等待 500ms 慢任务。
	startReconf := time.Now()
	rebuilt := svc.Reconfigure(30*time.Millisecond, 4)
	elapsed := time.Since(startReconf)
	if !rebuilt {
		t.Fatal("expected Reconfigure to return true when parameters changed")
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("expected non-blocking Reconfigure (<200ms), took %s", elapsed)
	}

	running, tick, conc, gen := svc.RunnerStats()
	if !running || tick != 30*time.Millisecond || conc != 4 || gen != 2 {
		t.Fatalf("unexpected runner stats after reconfigure: running=%v tick=%s conc=%d gen=%d", running, tick, conc, gen)
	}

	// 重复以相同参数调用 Reconfigure 必须幂等返回 false 且不递增代际。
	if svc.Reconfigure(30*time.Millisecond, 4) {
		t.Fatal("expected idempotent Reconfigure with same params to return false")
	}
	_, _, _, genAfter := svc.RunnerStats()
	if genAfter != gen {
		t.Fatalf("expected generation unchanged (%d), got %d", gen, genAfter)
	}

	// 验证旧在途慢任务没有被取消，仍优雅完成落库。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		samples, err := svc.Liveness.ListRecentByTarget(target.ID, 5)
		if err == nil && len(samples) >= 1 && samples[0].Success {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("expected inflight slow job to finish and persist sample after Reconfigure")
}

func TestScheduledBackoffAndResetOnTargetUpdate(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("backoff-runner-secret-32-bytes!!!")
	stub := &countingStubLLM{
		resp: &llmclient.Response{
			HTTPStatus:    429,
			ErrorClass:    llmclient.ErrorClassRateLimit,
			ErrorMessage:  "rate limit exceeded",
			RetryAfterSec: 300,
		},
		err: errors.New("http 429: rate limit exceeded"),
	}
	svc := NewService(db, cipher, nil, stub, nil)
	target, err := svc.CreateTarget(TargetInput{
		Name: "rl-target", SourceKind: storage.ProbeSourceDirect,
		BaseURL: "https://rl.example.com", APIKey: "sk-rl-old-key", Protocol: storage.ProbeProtocolOpenAIChat, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := svc.SaveTargetDetectors(target.ID, []DetectorConfigInput{
		{DetectorKey: DetectorKeyLiveness, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 60},
	}); err != nil {
		t.Fatalf("save liveness config: %v", err)
	}

	svc.Start(15*time.Millisecond, 2)
	deadline := time.Now().Add(2 * time.Second)
	var cfg *storage.ProbeDetectorConfig
	for time.Now().Before(deadline) {
		c, err := svc.Configs.Find(target.ID, DetectorKeyLiveness)
		if err == nil && c.ConsecutiveErrors >= 1 && c.BackoffUntil != nil {
			cfg = c
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	svc.Stop()

	if cfg == nil {
		t.Fatal("expected scheduled 429 job to persist consecutive_errors and backoff_until")
	}
	if cfg.ConsecutiveErrors != 1 {
		t.Fatalf("expected ConsecutiveErrors=1, got %d", cfg.ConsecutiveErrors)
	}
	if cfg.LastErrorClass != llmclient.ErrorClassRateLimit {
		t.Fatalf("expected LastErrorClass=rate_limit, got %q", cfg.LastErrorClass)
	}
	if cfg.NextRunAt == nil || !cfg.NextRunAt.After(time.Now().UTC().Add(240*time.Second)) {
		t.Fatalf("expected NextRunAt backed off >= 240s (Retry-After=300s with jitter), got %+v", cfg.NextRunAt)
	}

	// 更新目标（例如更换 API Key）后，应立即清零退避状态并恢复即时可调度。
	if _, err := svc.UpdateTarget(target.ID, TargetInput{
		Name:       target.Name,
		SourceKind: storage.ProbeSourceDirect,
		BaseURL:    target.BaseURL,
		APIKey:     "sk-rl-new-key",
		Protocol:   storage.ProbeProtocolOpenAIChat,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("UpdateTarget: %v", err)
	}
	afterReset, err := svc.Configs.Find(target.ID, DetectorKeyLiveness)
	if err != nil {
		t.Fatalf("find config after target update: %v", err)
	}
	if afterReset.ConsecutiveErrors != 0 || afterReset.LastErrorClass != "" || afterReset.BackoffUntil != nil {
		t.Fatalf("expected backoff state reset after UpdateTarget, got %+v", afterReset)
	}
}

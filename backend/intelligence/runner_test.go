package intelligence

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

type countingStubLLM struct {
	calls atomic.Int32
	delay time.Duration
	resp  *llmclient.Response
}

func (c *countingStubLLM) Complete(ctx context.Context, _ llmclient.Request) (*llmclient.Response, error) {
	c.calls.Add(1)
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	cp := *c.resp
	return &cp, nil
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

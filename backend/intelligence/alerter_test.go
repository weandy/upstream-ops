package intelligence

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/notify"
	"github.com/bejix/upstream-ops/backend/storage"
)

type captureAlertDispatcher struct {
	mu       sync.Mutex
	messages []notify.Message
	policy   notify.Policy
}

func (c *captureAlertDispatcher) Dispatch(_ context.Context, msg notify.Message) error {
	c.mu.Lock()
	c.messages = append(c.messages, msg)
	c.mu.Unlock()
	return nil
}

func (c *captureAlertDispatcher) Policy() notify.Policy { return c.policy }

func (c *captureAlertDispatcher) list() []notify.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notify.Message(nil), c.messages...)
}

func TestAlerterLivenessDownCooldownAndRecovery(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("alert-test-secret-32-bytes!!!!!!!")
	stub := &stubLLMClient{}
	svc := NewService(db, cipher, nil, stub, nil)

	disp := &captureAlertDispatcher{
		policy: notify.Policy{IntelligenceAlertCooldown: time.Hour},
	}
	alerter := NewAlerter(svc.Liveness, svc.Runs, disp, nil)
	svc.SetResultHook(alerter.HandleResult)

	target, _ := svc.CreateTarget(TargetInput{
		Name: "codex-plus", SourceKind: storage.ProbeSourceDirect,
		BaseURL: "https://a.test", APIKey: "sk-1234", Protocol: storage.ProbeProtocolOpenAIChat, Enabled: true,
	})
	_, _ = svc.SaveTargetDetectors(target.ID, []DetectorConfigInput{
		{
			DetectorKey:     DetectorKeyLiveness,
			Enabled:         true,
			ModelName:       "gpt-6.2-sol",
			IntervalSeconds: 180,
			AlertEnabled:    true,
			AlertThreshold:  3,
		},
	})

	// 连续失败 2 次：未达阈值 3，不告警。
	stub.resp = &llmclient.Response{Complete: false, HTTPStatus: 502, ErrorClass: llmclient.ErrorClassUpstream, ErrorMessage: "bad gateway"}
	for range 2 {
		if _, err := svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyLiveness, RunOptions{Manual: true}); err != nil {
			t.Fatalf("exec fail: %v", err)
		}
	}
	if got := len(disp.list()); got != 0 {
		t.Fatalf("expected 0 alerts after 2 failures, got %d", got)
	}

	// 第 3 次失败：触发 probe_down。
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyLiveness, RunOptions{Manual: true})
	msgs := disp.list()
	if len(msgs) != 1 || msgs[0].Event != storage.EventProbeDown {
		t.Fatalf("expected 1 probe_down alert, got %+v", msgs)
	}

	// 第 4 次失败仍在冷却窗口：不重复刷屏。
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyLiveness, RunOptions{Manual: true})
	if len(disp.list()) != 1 {
		t.Fatalf("expected cooldown to suppress duplicate probe_down, got %d", len(disp.list()))
	}

	// 第 5 次恢复成功：触发 probe_recovered。
	stub.resp = &llmclient.Response{Content: "A calm sunrise glows.", Complete: true, HTTPStatus: 200, FirstTokenMS: 200, LatencyMS: 500, OutputTokens: 10, TPS: 33, ErrorClass: llmclient.ErrorClassOK}
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyLiveness, RunOptions{Manual: true})
	msgs = disp.list()
	if len(msgs) != 2 || msgs[1].Event != storage.EventProbeRecovered {
		t.Fatalf("expected probe_recovered after recovery, got %+v", msgs)
	}
}

func TestAlerterIntelligenceSuspectedAndErrorThresholds(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("alert-intel-secret-32-bytes!!!!!!")
	stub := &stubLLMClient{}
	svc := NewService(db, cipher, nil, stub, nil)

	disp := &captureAlertDispatcher{
		policy: notify.Policy{IntelligenceAlertCooldown: time.Hour},
	}
	alerter := NewAlerter(svc.Liveness, svc.Runs, disp, nil)
	svc.SetResultHook(alerter.HandleResult)

	target, _ := svc.CreateTarget(TargetInput{
		Name: "cc-max", SourceKind: storage.ProbeSourceDirect,
		BaseURL: "https://b.test", APIKey: "sk-1234", Protocol: storage.ProbeProtocolOpenAIChat, Enabled: true,
	})
	_, _ = svc.SaveTargetDetectors(target.ID, []DetectorConfigInput{
		{
			DetectorKey:     DetectorKeyCandy,
			Enabled:         true,
			ModelName:       "gpt-6.2-sol",
			IntervalSeconds: 3600,
			AlertEnabled:    true,
			AlertThreshold:  2,
		},
	})

	// 1. 错 1 次 -> 对 1 次 -> 错 1 次：未连续错 2 次，不告警。
	stub.resp = &llmclient.Response{Content: "29", Complete: true, HTTPStatus: 200, ErrorClass: llmclient.ErrorClassOK}
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyCandy, RunOptions{Manual: true})
	stub.resp = &llmclient.Response{Content: "21", Complete: true, HTTPStatus: 200, ErrorClass: llmclient.ErrorClassOK}
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyCandy, RunOptions{Manual: true})
	stub.resp = &llmclient.Response{Content: "29", Complete: true, HTTPStatus: 200, ErrorClass: llmclient.ErrorClassOK}
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyCandy, RunOptions{Manual: true})
	if len(disp.list()) != 0 {
		t.Fatalf("expected 0 alerts without consecutive failures, got %+v", disp.list())
	}

	// 再错 1 次（连续 2 次 suspected）-> 触发 intelligence_suspected！
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyCandy, RunOptions{Manual: true})
	msgs := disp.list()
	if len(msgs) != 1 || msgs[0].Event != storage.EventIntelligenceSuspected || !strings.Contains(msgs[0].Subject, "cc-max") {
		t.Fatalf("expected intelligence_suspected alert, got %+v", msgs)
	}

	// 2. 连续 2 次响应截断报错 -> 触发 intelligence_error！
	stub.resp = &llmclient.Response{Content: "", Complete: false, HTTPStatus: 200, ErrorClass: llmclient.ErrorClassTruncated, ErrorMessage: "eof"}
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyCandy, RunOptions{Manual: true})
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyCandy, RunOptions{Manual: true})
	msgs = disp.list()
	if len(msgs) != 2 || msgs[1].Event != storage.EventIntelligenceError {
		t.Fatalf("expected intelligence_error alert, got %+v", msgs)
	}

	// 3. AlertEnabled=false 时不再触发新告警。
	_, _ = svc.SaveTargetDetectors(target.ID, []DetectorConfigInput{
		{
			DetectorKey:     DetectorKeyCandy,
			Enabled:         true,
			ModelName:       "gpt-6.2-sol",
			IntervalSeconds: 3600,
			AlertEnabled:    false,
			AlertThreshold:  1,
		},
	})
	_, _ = svc.ExecuteDetector(context.Background(), target.ID, DetectorKeyCandy, RunOptions{Manual: true})
	if len(disp.list()) != 2 {
		t.Fatalf("expected no alert when alert_enabled=false, got %d", len(disp.list()))
	}
}

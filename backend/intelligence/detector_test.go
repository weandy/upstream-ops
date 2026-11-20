package intelligence

import (
	"context"
	"testing"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
)

type mockCustomDetector struct {
	key string
}

func (m mockCustomDetector) Key() string { return m.key }
func (m mockCustomDetector) Meta() DetectorMeta {
	return DetectorMeta{
		Key:                m.key,
		Name:               "Mock Detector",
		Category:           DetectorCategoryIntelligence,
		DefaultIntervalSec: 3600,
		DefaultTimeoutSec:  60,
		DefaultAlertThresh: 3,
	}
}
func (m mockCustomDetector) ValidateParams(_ string) error { return nil }
func (m mockCustomDetector) Run(_ context.Context, _ LLMClient, _ DetectorRunRequest) (*DetectorRunResult, error) {
	return &DetectorRunResult{Status: "normal", Summary: "ok"}, nil
}

func TestRegistryRegisterLookupAndOrderedList(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(mockCustomDetector{key: "zeta"}); err != nil {
		t.Fatalf("register zeta: %v", err)
	}
	if err := reg.Register(mockCustomDetector{key: "alpha"}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	// 重复注册同一 Key 必须报错。
	if err := reg.Register(mockCustomDetector{key: "alpha"}); err == nil {
		t.Fatal("expected duplicate detector registration to fail")
	}

	d, ok := reg.Get("alpha")
	if !ok || d.Key() != "alpha" {
		t.Fatalf("expected to lookup alpha, got ok=%v d=%v", ok, d)
	}
	if _, ok := reg.Get("missing"); ok {
		t.Fatal("expected missing detector lookup to return false")
	}

	metas := reg.ListMeta()
	if len(metas) != 2 {
		t.Fatalf("expected 2 metas, got %d", len(metas))
	}
	// 保留注册顺序，便于前端稳定展示插件列表。
	if metas[0].Key != "zeta" || metas[1].Key != "alpha" {
		t.Fatalf("unexpected meta order: %+v", metas)
	}
}

func TestLivenessDetectorAdapter(t *testing.T) {
	d := NewLivenessDetector()
	if d.Key() != DetectorKeyLiveness {
		t.Fatalf("unexpected key: %s", d.Key())
	}
	meta := d.Meta()
	if meta.Category != DetectorCategoryLiveness || meta.DefaultIntervalSec != DefaultLivenessIntervalSec {
		t.Fatalf("unexpected liveness meta: %+v", meta)
	}
	if err := d.ValidateParams(""); err != nil {
		t.Fatalf("empty params should be valid: %v", err)
	}

	stub := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "A bright sunrise shines gently over the calm blue ocean.",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 300,
			LatencyMS:    900,
			OutputTokens: 15,
			TPS:          25,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}
	res, err := d.Run(context.Background(), stub, DetectorRunRequest{
		TargetID: 7,
		Request: llmclient.Request{
			BaseURL: "https://api.example.com",
			APIKey:  "sk-test",
			Model:   "gpt-6.2-sol",
		},
	})
	if err != nil {
		t.Fatalf("liveness detector run failed: %v", err)
	}
	if res.LivenessSample == nil || !res.LivenessSample.Success || res.Status != "normal" {
		t.Fatalf("unexpected liveness detector result: %+v", res)
	}
}

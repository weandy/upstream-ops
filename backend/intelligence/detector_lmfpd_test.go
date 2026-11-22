package intelligence

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/intelligence/lmfpd"
	"github.com/bejix/upstream-ops/backend/storage"
)

type sequenceStubLLM struct {
	replies []string
	idx     int
}

func (s *sequenceStubLLM) Complete(_ context.Context, _ llmclient.Request) (*llmclient.Response, error) {
	text := ""
	if s.idx < len(s.replies) {
		text = s.replies[s.idx]
		s.idx++
	}
	return &llmclient.Response{
		Content:      text,
		Complete:     true,
		HTTPStatus:   200,
		FirstTokenMS: 300,
		LatencyMS:    1200,
		OutputTokens: 320,
		ErrorClass:   llmclient.ErrorClassOK,
	}, nil
}

func (s *sequenceStubLLM) ListModels(_ context.Context, _ llmclient.Endpoint) ([]string, error) {
	return nil, nil
}

func TestLMFPDDetectorRunWithConformanceSample(t *testing.T) {
	// 读取官方一致性样本的第一条合法模型样本，验证 LMFPDDetector 全链路判定。
	raw, err := os.ReadFile("lmfpd/testdata/analyze.json")
	if err != nil {
		t.Fatalf("read analyze.json: %v", err)
	}
	var cases []struct {
		Name    string
		Outputs []struct {
			Text          string
			ExpectedCount int `json:"expected_count"`
		}
		Expected lmfpd.Attribution
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var sampleReplies []string
	var expectedModel string
	for _, tc := range cases {
		if tc.Expected.Prediction != "" && len(tc.Outputs) == lmfpd.ChallengeCount {
			for _, o := range tc.Outputs {
				sampleReplies = append(sampleReplies, o.Text)
			}
			expectedModel = tc.Expected.Prediction
			break
		}
	}
	if len(sampleReplies) != lmfpd.ChallengeCount {
		t.Fatal("failed to locate 3-output conformance sample")
	}

	mgr := lmfpd.NewManager("")
	det := NewLMFPDDetector(mgr)
	if det.Key() != DetectorKeyLMFPD {
		t.Fatalf("unexpected key: %s", det.Key())
	}

	// 1. 声称模型与预测第一一致 -> normal, ClaimedRank=1
	resOK, err := det.Run(context.Background(), &sequenceStubLLM{replies: sampleReplies}, DetectorRunRequest{
		TargetID: 1,
		Request:  llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: expectedModel},
	})
	if err != nil {
		t.Fatalf("lmfpd run ok failed: %v", err)
	}
	if resOK.Status != storage.IntelligenceStatusNormal || resOK.ClaimedRank != 1 || resOK.TopCandidate == "" {
		t.Fatalf("expected normal rank=1 for claimed=%s, got %+v", expectedModel, resOK)
	}
	if resOK.OutputTokens != 320*lmfpd.ChallengeCount {
		t.Fatalf("expected summed output tokens %d, got %d", 320*lmfpd.ChallengeCount, resOK.OutputTokens)
	}

	// 2. 声称模型与预测第一不一致（另一家族模型） -> suspected
	wrongClaim := "gpt-4o"
	if strings.EqualFold(expectedModel, wrongClaim) {
		wrongClaim = "claude-opus-4.6"
	}
	resMismatch, err := det.Run(context.Background(), &sequenceStubLLM{replies: sampleReplies}, DetectorRunRequest{
		TargetID: 1,
		Request:  llmclient.Request{BaseURL: "https://a.test", APIKey: "sk-1", Model: wrongClaim},
	})
	if err != nil {
		t.Fatalf("lmfpd run mismatch failed: %v", err)
	}
	if resMismatch.Status != storage.IntelligenceStatusSuspected {
		t.Fatalf("expected suspected when claimed=%s differs from top=%s, got %+v", wrongClaim, resMismatch.TopCandidate, resMismatch)
	}
}

func TestLiveLMFPDDetector(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("LIVE_TEST_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("LIVE_TEST_API_KEY"))
	model := strings.TrimSpace(os.Getenv("LIVE_TEST_MODEL"))
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("live test env vars not set")
	}

	det := NewLMFPDDetector(lmfpd.NewManager("./data/lmfpd"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := det.Run(ctx, llmclient.New(), DetectorRunRequest{
		TargetID: 1,
		Request: llmclient.Request{
			BaseURL:  baseURL,
			APIKey:   apiKey,
			Protocol: storage.ProbeProtocolOpenAIChat,
			Model:    model,
			Timeout:  90 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("live lmfpd err: %v", err)
	}
	t.Logf("live lmfpd: status=%s top=%q claimed_rank=%d summary=%q latency=%dms tokens=%d",
		res.Status, res.TopCandidate, res.ClaimedRank, res.Summary, res.LatencyMS, res.OutputTokens)
	if res.Status == storage.IntelligenceStatusError {
		t.Fatalf("live lmfpd errored: %s", res.ErrorMessage)
	}
	if res.TopCandidate == "" {
		t.Fatalf("expected non-empty top candidate, got %+v", res)
	}
}

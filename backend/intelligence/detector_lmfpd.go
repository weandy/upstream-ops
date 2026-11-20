package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence/lmfpd"
	"github.com/bejix/upstream-ops/backend/storage"
)

type lmfpdParams struct {
	ClaimedModel string `json:"claimed_model,omitempty"`
	Concurrency  int    `json:"concurrency,omitempty"`
}

// LMFPDDetector 基于 MIT 协议 Ikaleio/lm-detector 的纯 Go 数字指纹检测插件。
type LMFPDDetector struct {
	manager *lmfpd.Manager
}

func NewLMFPDDetector(mgr *lmfpd.Manager) Detector {
	if mgr == nil {
		mgr = lmfpd.NewManager("")
	}
	return &LMFPDDetector{manager: mgr}
}

func (*LMFPDDetector) Key() string { return DetectorKeyLMFPD }

func (*LMFPDDetector) Meta() DetectorMeta {
	return DetectorMeta{
		Key:                DetectorKeyLMFPD,
		Name:               "数字指纹（lmfpd）",
		Category:           DetectorCategoryIntelligence,
		Description:        "随机抽取 3 轮 1-355 随机整数生成挑战，按数值分布与序列特征和 lm-detector 参考库比对，给出最像的模型候选排名（结果仅供参考，不作绝对身份证明）。",
		DefaultIntervalSec: 10800,
		DefaultTimeoutSec:  120,
		DefaultAlertThresh: 2,
		SupportsEmbed:      true,
		SupportsReasoning:  true,
		ParamsSchema: []ParamField{
			{
				Key:         "claimed_model",
				Label:       "声称对照模型名（可选）",
				Type:        "string",
				Description: "留空默认使用本检测项配置的模型名，用于核对在参考库中的排名。",
			},
			{
				Key:         "concurrency",
				Label:       "3 轮采样并发数",
				Type:        "number",
				Default:     3,
				Description: "取值 1-3，默认为 3 并发采样以缩短耗时。",
			},
		},
	}
}

func (*LMFPDDetector) ValidateParams(rawJSON string) error {
	raw := strings.TrimSpace(rawJSON)
	if raw == "" || raw == "{}" {
		return nil
	}
	var p lmfpdParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("invalid lmfpd params: %w", err)
	}
	if p.Concurrency < 0 || p.Concurrency > lmfpd.ChallengeCount {
		return fmt.Errorf("concurrency must be between 0 and %d", lmfpd.ChallengeCount)
	}
	return nil
}

func (d *LMFPDDetector) Run(ctx context.Context, client LLMClient, req DetectorRunRequest) (*DetectorRunResult, error) {
	eng, err := d.manager.Engine()
	if err != nil {
		return &DetectorRunResult{
			Status:       storage.IntelligenceStatusError,
			Summary:      "指纹参考库加载失败",
			ErrorMessage: err.Error(),
		}, nil
	}

	var p lmfpdParams
	if raw := strings.TrimSpace(req.ParamsJSON); raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, fmt.Errorf("invalid lmfpd params: %w", err)
		}
	}
	concurrency := p.Concurrency
	if concurrency <= 0 || concurrency > lmfpd.ChallengeCount {
		concurrency = lmfpd.ChallengeCount
	}
	claimed := strings.TrimSpace(p.ClaimedModel)
	if claimed == "" {
		claimed = strings.TrimSpace(req.Request.Model)
	}

	challenges := eng.Challenges()
	started := time.Now()

	type roundResult struct {
		reply      lmfpd.Reply
		firstToken int64
		latency    int64
		outTokens  int
		errMsg     string
	}
	rounds := make([]roundResult, lmfpd.ChallengeCount)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < lmfpd.ChallengeCount; i++ {
		i := i
		ch := challenges[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				rounds[i].errMsg = ctx.Err().Error()
				return
			}

			llmReq := req.Request
			llmReq.SystemPrompt = ch.Instructions
			llmReq.Prompt = ch.Prompt
			if llmReq.MaxTokens <= 0 {
				llmReq.MaxTokens = 2048
			}
			if llmReq.Timeout <= 0 {
				llmReq.Timeout = 90 * time.Second
			}

			resp, callErr := client.Complete(ctx, llmReq)
			if resp != nil {
				rounds[i].firstToken = resp.FirstTokenMS
				rounds[i].latency = resp.LatencyMS
				rounds[i].outTokens = resp.OutputTokens
			}
			if callErr != nil {
				rounds[i].errMsg = callErr.Error()
				rounds[i].reply = lmfpd.Prepare("", ch.ExpectedCount)
				return
			}
			if resp == nil {
				rounds[i].errMsg = "empty response"
				rounds[i].reply = lmfpd.Prepare("", ch.ExpectedCount)
				return
			}
			rounds[i].reply = lmfpd.Prepare(resp.Content, ch.ExpectedCount)
			if !rounds[i].reply.Valid() {
				rounds[i].errMsg = fmt.Sprintf("有效数字不足（解析 %d 个）", rounds[i].reply.Count())
			}
		}()
	}
	wg.Wait()

	replies := make([]lmfpd.Reply, lmfpd.ChallengeCount)
	var (
		totalTokens int
		minTTFT     int64
		roundErrs   []string
	)
	for i, rr := range rounds {
		replies[i] = rr.reply
		totalTokens += rr.outTokens
		if rr.firstToken > 0 && (minTTFT == 0 || rr.firstToken < minTTFT) {
			minTTFT = rr.firstToken
		}
		if rr.errMsg != "" {
			roundErrs = append(roundErrs, fmt.Sprintf("第%d轮: %s", i+1, rr.errMsg))
		}
	}

	out := &DetectorRunResult{
		FirstTokenMS: minTTFT,
		LatencyMS:    time.Since(started).Milliseconds(),
		OutputTokens: totalTokens,
	}

	attr := eng.Analyze(replies)
	if attr == nil {
		out.Status = storage.IntelligenceStatusError
		out.Summary = "未采集到有效数字序列"
		if len(roundErrs) > 0 {
			out.ErrorMessage = strings.Join(roundErrs, "; ")
		} else {
			out.ErrorMessage = errors.New("no valid numeric replies collected").Error()
		}
		return out, nil
	}

	topK := attr.Results
	if len(topK) > 10 {
		topK = topK[:10]
	}
	claimedRank, matched := lmfpd.FindClaimedRank(attr.Results, claimed)

	detailPayload := map[string]any{
		"claimed_model":     claimed,
		"claimed_rank":      claimedRank,
		"claimed_in_bank":   matched != nil,
		"decision":          attr.Decision,
		"prediction":        attr.Prediction,
		"prediction_name":   attr.PredictionName,
		"probability":       attr.Probability,
		"family_prediction": attr.FamilyPrediction,
		"used_outputs":      attr.Used,
		"sequence":          attr.Sequence,
		"diagnostics":       attr.Diagnostics,
		"top_candidates":    topK,
		"detector_revision": eng.Revision(),
		"detector_source":   eng.Source(),
		"bank_built_at":     eng.BuiltAt(),
		"round_errors":      roundErrs,
	}
	detailRaw, _ := json.Marshal(detailPayload)
	out.DetailJSON = string(detailRaw)
	out.ClaimedRank = claimedRank
	out.TopCandidate = attr.PredictionName
	if out.TopCandidate == "" {
		out.TopCandidate = attr.Prediction
	}

	if attr.Decision == "not_model" {
		out.Status = storage.IntelligenceStatusSuspected
		out.Summary = "异常：数字序列疑似规律/非自然生成"
		return out, nil
	}

	if matched != nil && claimedRank > 1 {
		out.Status = storage.IntelligenceStatusSuspected
		out.Summary = fmt.Sprintf("最像 %s（声称模型排第 %d）", out.TopCandidate, claimedRank)
		return out, nil
	}

	out.Status = storage.IntelligenceStatusNormal
	if matched != nil {
		out.Summary = fmt.Sprintf("最像 %s（排第 1）", out.TopCandidate)
	} else {
		out.Summary = fmt.Sprintf("最像 %s（声称模型未入参考库）", out.TopCandidate)
	}
	return out, nil
}

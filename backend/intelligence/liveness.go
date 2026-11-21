package intelligence

import (
	"context"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	// DefaultLivenessPrompt 轻量级主动测活提示词：控制输出在 ~20 个词，兼顾低成本与稳定测量 TPS。
	DefaultLivenessPrompt = "Write one short sentence of 16 to 24 words describing a calm sunrise over the ocean. Output plain text only."
	// DefaultLivenessIntervalSec 主动测活默认周期：1 分钟（60 秒），与近 60 分钟逐分钟热力条 1:1 对齐。
	DefaultLivenessIntervalSec = 60
	// DefaultLivenessTimeoutSec 主动测活默认超时：30 秒。
	DefaultLivenessTimeoutSec = 30
)

// RunLivenessProbe 执行一次主动流式测活，返回可直接落库的采样记录与上游建议退避秒数（如 429 Retry-After）。
func RunLivenessProbe(ctx context.Context, client LLMClient, req llmclient.Request, targetID uint) *storage.ProbeLivenessSample {
	sample, _ := runLivenessProbeDetailed(ctx, client, req, targetID)
	return sample
}

func runLivenessProbeDetailed(ctx context.Context, client LLMClient, req llmclient.Request, targetID uint) (*storage.ProbeLivenessSample, int) {
	if strings.TrimSpace(req.Prompt) == "" {
		req.Prompt = DefaultLivenessPrompt
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 64
	}
	if req.Timeout <= 0 {
		req.Timeout = DefaultLivenessTimeoutSec * time.Second
	}

	now := time.Now().UTC()
	resp, err := client.Complete(ctx, req)
	retryAfter := 0
	sample := &storage.ProbeLivenessSample{
		TargetID:   targetID,
		ModelName:  req.Model,
		ObservedAt: now,
	}
	if resp != nil {
		sample.HTTPStatus = resp.HTTPStatus
		sample.ErrorClass = resp.ErrorClass
		sample.ErrorMessage = resp.ErrorMessage
		sample.FirstTokenMS = resp.FirstTokenMS
		sample.LatencyMS = resp.LatencyMS
		sample.OutputTokens = resp.OutputTokens
		sample.TPS = resp.TPS
		retryAfter = resp.RetryAfterSec
	}
	if err != nil {
		sample.Success = false
		if sample.ErrorClass == "" || sample.ErrorClass == llmclient.ErrorClassOK {
			sample.ErrorClass = llmclient.ErrorClassNetwork
		}
		if sample.ErrorMessage == "" {
			sample.ErrorMessage = err.Error()
		}
		return sample, retryAfter
	}
	if resp == nil || !resp.Complete || strings.TrimSpace(resp.Content) == "" {
		sample.Success = false
		if sample.ErrorClass == "" || sample.ErrorClass == llmclient.ErrorClassOK {
			sample.ErrorClass = llmclient.ErrorClassTruncated
		}
		if sample.ErrorMessage == "" {
			sample.ErrorMessage = "empty or incomplete liveness response"
		}
		return sample, retryAfter
	}

	sample.Success = true
	sample.ErrorClass = llmclient.ErrorClassOK
	sample.ErrorMessage = ""
	return sample, 0
}

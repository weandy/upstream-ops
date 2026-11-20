// Package intelligence 提供外挂式主动测活与可插拔模型降智监测能力。
package intelligence

import (
	"context"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
)

// 内置检测器唯一标识键。
const (
	DetectorKeyLiveness     = "liveness"
	DetectorKeyCandy        = "candy"
	DetectorKeyLMFPD        = "lmfpd"
	DetectorKeyPelican      = "pelican"
	DetectorKeyCustomPrompt = "custom_prompt"
)

// LLMClient 探测模型客户端抽象（生产实现为 *llmclient.Client，测试可注入 stub）。
type LLMClient interface {
	Complete(ctx context.Context, req llmclient.Request) (*llmclient.Response, error)
	ListModels(ctx context.Context, ep llmclient.Endpoint) ([]string, error)
}

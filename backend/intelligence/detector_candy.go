package intelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

// CandyPrompt 社区标准糖果逻辑题（标准答案为 21）。
const CandyPrompt = `不使用任何外部工具回答以下问题：

在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

        苹果味  桃子味  西瓜味
圆形       7      9      8
五角星形   7      6      4

请只输出最终整数答案。`

var (
	candyAmbiguousRe  = regexp.MustCompile(`(?i)\b\d+\s*(?:或|或者|/|or|,|、)\s*\d+\b`)
	candyNumberToken  = regexp.MustCompile(`\d+(?:\.\d+)?`)
	candyDecimal21Re  = regexp.MustCompile(`\b21\.\d+`)
	candyLeadingDotRe = regexp.MustCompile(`\.\d*21\b`)
)

// GradeCandyAnswer 严格判定糖果题最终答案是否为独立整数 21。
// 拒绝 121、210、21.0、21.5、"21 或 22"、"21/22" 以及空白回答。
func GradeCandyAnswer(answer string) bool {
	cleaned := strings.TrimSpace(answer)
	if cleaned == "" {
		return false
	}
	cleaned = strings.NewReplacer("*", "", "_", "", "`", "").Replace(cleaned)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "21" {
		return true
	}
	if candyAmbiguousRe.MatchString(cleaned) {
		return false
	}
	if candyDecimal21Re.MatchString(cleaned) || candyLeadingDotRe.MatchString(cleaned) {
		return false
	}

	// 取最后一个非空行作为最终结论行，避免多行推导中出现误命中或干扰数字。
	lines := strings.Split(cleaned, "\n")
	lastLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			lastLine = l
			break
		}
	}
	if lastLine == "" {
		return false
	}
	if candyAmbiguousRe.MatchString(lastLine) || candyDecimal21Re.MatchString(lastLine) {
		return false
	}
	nums := candyNumberToken.FindAllString(lastLine, -1)
	if len(nums) == 1 && nums[0] == "21" {
		return true
	}
	return false
}

// CandyDetector 糖果逻辑题降智检测插件。
type CandyDetector struct{}

func NewCandyDetector() Detector { return CandyDetector{} }

func (CandyDetector) Key() string { return DetectorKeyCandy }

func (CandyDetector) Meta() DetectorMeta {
	return DetectorMeta{
		Key:                DetectorKeyCandy,
		Name:               "Candy 智力题",
		Category:           DetectorCategoryIntelligence,
		Description:        "向模型发送经典糖果最坏情况逻辑推理题，仅当最终回答给出唯一独立整数 21 时判定通过。",
		DefaultIntervalSec: 3600,
		DefaultTimeoutSec:  90,
		DefaultAlertThresh: 3,
		SupportsEmbed:      true,
		SupportsReasoning:  true,
	}
}

func (CandyDetector) ValidateParams(_ string) error { return nil }

func (CandyDetector) Run(ctx context.Context, client LLMClient, req DetectorRunRequest) (*DetectorRunResult, error) {
	llmReq := req.Request
	llmReq.Prompt = CandyPrompt
	if llmReq.MaxTokens <= 0 {
		llmReq.MaxTokens = 4096
	}
	if llmReq.Timeout <= 0 {
		llmReq.Timeout = 90 * time.Second
	}

	resp, err := client.Complete(ctx, llmReq)
	out := &DetectorRunResult{}
	if resp != nil {
		out.FirstTokenMS = resp.FirstTokenMS
		out.LatencyMS = resp.LatencyMS
		out.OutputTokens = resp.OutputTokens
		out.ErrorClass = resp.ErrorClass
		out.RetryAfterSec = resp.RetryAfterSec
	}
	if err != nil {
		out.Status = storage.IntelligenceStatusError
		out.ErrorMessage = err.Error()
		out.Summary = "检测失败"
		return out, nil
	}
	if resp == nil || !resp.Complete {
		out.Status = storage.IntelligenceStatusError
		if resp != nil && resp.ErrorMessage != "" {
			out.ErrorMessage = resp.ErrorMessage
		} else {
			out.ErrorMessage = "incomplete response"
		}
		out.Summary = "响应未完整结束"
		return out, nil
	}

	answer := strings.TrimSpace(resp.Content)
	passed := GradeCandyAnswer(answer)
	detail, _ := json.Marshal(map[string]any{
		"answer":    truncateRunes(answer, 2000),
		"passed":    passed,
		"reasoning": truncateRunes(resp.Reasoning, 1000),
	})
	out.DetailJSON = string(detail)
	if passed {
		out.Status = storage.IntelligenceStatusNormal
		out.Summary = "通过：答案为 21"
		return out, nil
	}
	out.Status = storage.IntelligenceStatusSuspected
	if answer == "" {
		out.Summary = "未通过：模型返回空答"
	} else {
		out.Summary = fmt.Sprintf("未通过：模型回答 %s", truncateRunes(strings.ReplaceAll(answer, "\n", " "), 60))
	}
	return out, nil
}

func truncateRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "..."
}

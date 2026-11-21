package intelligence

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
)

type stubLLMClient struct {
	resp *llmclient.Response
	err  error
	reqs []llmclient.Request
}

func (s *stubLLMClient) Complete(_ context.Context, req llmclient.Request) (*llmclient.Response, error) {
	s.reqs = append(s.reqs, req)
	return s.resp, s.err
}

func (s *stubLLMClient) ListModels(_ context.Context, _ llmclient.Endpoint) ([]string, error) {
	return []string{"gpt-6.2-sol"}, nil
}

func TestAggregateLivenessMetricsAnd90MinBuckets(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 30, 0, time.UTC)
	samples := []storage.ProbeLivenessSample{
		// 20 小时前：计入 24h 性能，不计入 90m 趋势条。
		{
			Success:      true,
			FirstTokenMS: 1000,
			LatencyMS:    3000, // 生成耗时 2s
			OutputTokens: 100,
			TPS:          50,
			ObservedAt:   now.Add(-20 * time.Hour),
		},
		// 当前分钟内 2 次：1 成 1 败 -> 50% -> warn
		{
			Success:      true,
			FirstTokenMS: 2000,
			LatencyMS:    6000, // 生成耗时 4s
			OutputTokens: 200,
			TPS:          50,
			ObservedAt:   now.Add(-10 * time.Second),
		},
		{
			Success:      false,
			HTTPStatus:   502,
			ErrorClass:   llmclient.ErrorClassUpstream,
			LatencyMS:    500,
			ObservedAt:   now.Add(-5 * time.Second),
		},
		// 10 分钟前 1 次全成功 -> ok
		{
			Success:      true,
			FirstTokenMS: 1500,
			LatencyMS:    3500, // 生成耗时 2s
			OutputTokens: 100,
			TPS:          50,
			ObservedAt:   now.Add(-10 * time.Minute),
		},
		// 20 分钟前 1 次全失败 -> down
		{
			Success:    false,
			HTTPStatus: 500,
			ErrorClass: llmclient.ErrorClassUpstream,
			ObservedAt: now.Add(-20 * time.Minute),
		},
	}

	perf, buckets := AggregateLiveness(samples, now)
	if perf.SampleCount != 5 || perf.SuccessCount != 3 {
		t.Fatalf("expected 5 samples / 3 success, got %+v", perf)
	}
	if math.Abs(perf.SuccessRate-60.0) > 0.01 {
		t.Fatalf("expected success rate 60.0, got %.2f", perf.SuccessRate)
	}
	// 成功样本的平均首字：(1000 + 2000 + 1500) / 3 = 1500ms
	if perf.AvgFirstTokenMS != 1500 {
		t.Fatalf("expected avg ttft 1500, got %d", perf.AvgFirstTokenMS)
	}
	// 成功样本的平均延迟：(3000 + 6000 + 3500) / 3 = 4166ms
	if perf.AvgLatencyMS != 4166 && perf.AvgLatencyMS != 4167 {
		t.Fatalf("expected avg latency ~4166, got %d", perf.AvgLatencyMS)
	}
	// 加权 TPS：(100+200+100) / (2+4+2) = 50.0
	if math.Abs(perf.TPS-50.0) > 0.01 {
		t.Fatalf("expected weighted TPS 50.0, got %.2f", perf.TPS)
	}

	if len(buckets) != 90 {
		t.Fatalf("expected 90 buckets, got %d", len(buckets))
	}
	// 最后一个桶是当前分钟（index 89）-> 1/2 = 50% -> warn
	if buckets[89].Tone != "warn" || buckets[89].SampleCount != 2 {
		t.Fatalf("unexpected bucket[89]: %+v", buckets[89])
	}
	// 10 分钟前（index 79）-> 1/1 -> ok
	if buckets[79].Tone != "ok" || buckets[79].SampleCount != 1 {
		t.Fatalf("unexpected bucket[79]: %+v", buckets[79])
	}
	// 20 分钟前（index 69）-> 0/1 -> down
	if buckets[69].Tone != "down" || buckets[69].SampleCount != 1 {
		t.Fatalf("unexpected bucket[69]: %+v", buckets[69])
	}
	// 其余无样本桶 -> none
	if buckets[0].Tone != "none" || buckets[0].SampleCount != 0 {
		t.Fatalf("unexpected bucket[0]: %+v", buckets[0])
	}
}

func TestLivenessProbeExecution(t *testing.T) {
	stub := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "Service is operational and streaming normally.",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 900,
			LatencyMS:    1900,
			OutputTokens: 20,
			TPS:          20.0,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}

	sample := RunLivenessProbe(context.Background(), stub, llmclient.Request{
		BaseURL:  "https://api.example.com",
		APIKey:   "sk-test",
		Protocol: storage.ProbeProtocolOpenAIChat,
		Model:    "gpt-6.2-sol",
	}, 42)

	if !sample.Success || sample.TargetID != 42 || sample.FirstTokenMS != 900 || sample.TPS != 20.0 {
		t.Fatalf("unexpected liveness sample: %+v", sample)
	}
	if len(stub.reqs) != 1 || stub.reqs[0].Prompt == "" {
		t.Fatalf("expected default liveness prompt to be set")
	}

	// 响应不完整或空内容应记为失败。
	stubFail := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "",
			Complete:     false,
			HTTPStatus:   200,
			ErrorClass:   llmclient.ErrorClassTruncated,
			ErrorMessage: "stream ended early",
		},
	}
	failSample := RunLivenessProbe(context.Background(), stubFail, llmclient.Request{
		BaseURL:  "https://api.example.com",
		APIKey:   "sk-test",
		Protocol: storage.ProbeProtocolOpenAIChat,
		Model:    "gpt-6.2-sol",
	}, 42)
	if failSample.Success || failSample.ErrorClass != llmclient.ErrorClassTruncated {
		t.Fatalf("expected failed liveness sample, got %+v", failSample)
	}
}

func TestAggregatePerformanceTrendWindowsAndP95(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 15, 0, 0, time.UTC)
	samples := []storage.ProbeLivenessSample{
		// 当前 30 分钟桶（12:00）放入 4 个样本，验证均值与 P95 TTFT。
		{Success: true, FirstTokenMS: 400, LatencyMS: 1400, OutputTokens: 40, ObservedAt: now.Add(-10 * time.Minute)},
		{Success: true, FirstTokenMS: 600, LatencyMS: 1600, OutputTokens: 40, ObservedAt: now.Add(-8 * time.Minute)},
		{Success: true, FirstTokenMS: 800, LatencyMS: 1800, OutputTokens: 40, ObservedAt: now.Add(-6 * time.Minute)},
		{Success: true, FirstTokenMS: 2200, LatencyMS: 3200, OutputTokens: 40, ObservedAt: now.Add(-4 * time.Minute)},
		{Success: false, HTTPStatus: 502, ObservedAt: now.Add(-2 * time.Minute)},
		// 2 小时前（10:00 桶）放 1 个样本。
		{Success: true, FirstTokenMS: 500, LatencyMS: 1500, OutputTokens: 50, ObservedAt: now.Add(-2 * time.Hour)},
	}

	win, bucketMin, points := AggregatePerformanceTrend(samples, "24h", now)
	if win != "24h" || bucketMin != 30 || len(points) != 48 {
		t.Fatalf("expected 24h / 30m / 48 points, got %s / %dm / %d", win, bucketMin, len(points))
	}
	last := points[len(points)-1]
	if last.SampleCount != 5 || last.SuccessCount != 4 || math.Abs(last.SuccessRate-80.0) > 0.01 {
		t.Fatalf("unexpected last bucket counts: %+v", last)
	}
	if last.AvgFirstTokenMS != 1000 || last.P95FirstTokenMS != 2200 {
		t.Fatalf("expected avg TTFT 1000 / P95 2200, got avg=%d p95=%d", last.AvgFirstTokenMS, last.P95FirstTokenMS)
	}
	if math.Abs(last.TPS-40.0) > 0.01 {
		t.Fatalf("expected bucket TPS 40.0, got %.2f", last.TPS)
	}

	// 校验 6h 与 7d 窗口以及非法窗口回退。
	w6, m6, p6 := AggregatePerformanceTrend(samples, "6h", now)
	if w6 != "6h" || m6 != 10 || len(p6) != 36 {
		t.Fatalf("unexpected 6h window: %s / %d / %d", w6, m6, len(p6))
	}
	w7, m7, p7 := AggregatePerformanceTrend(samples, "7d", now)
	if w7 != "7d" || m7 != 180 || len(p7) != 56 {
		t.Fatalf("unexpected 7d window: %s / %d / %d", w7, m7, len(p7))
	}
	wDef, _, _ := AggregatePerformanceTrend(samples, "invalid", now)
	if wDef != "24h" {
		t.Fatalf("expected invalid window fallback to 24h, got %s", wDef)
	}
}

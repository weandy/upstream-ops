package siteops

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestWindowForecastAdmissionAndBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	validResets := now.Add(3 * time.Hour)

	tests := []struct {
		name           string
		in             WindowForecastInput
		wantStatus     ForecastStatus
		wantConf       ForecastConfidence
		wantSampleNil  bool
		wantNilMetrics bool
	}{
		{
			name: "非被动证实平台 openai/oauth 被准入拦截",
			in: WindowForecastInput{
				InstanceID:         1,
				AccountID:          10,
				Platform:           "openai",
				AccountType:        "oauth",
				Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 40, ResetsAt: &validResets},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusInsufficientData,
			wantConf:       ConfidenceLow,
			wantSampleNil:  true,
			wantNilMetrics: true,
		},
		{
			name: "按量 API Key 号被永久排除",
			in: WindowForecastInput{
				InstanceID:         1,
				AccountID:          11,
				Platform:           "anthropic",
				AccountType:        "apikey",
				Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 40, ResetsAt: &validResets},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusInsufficientData,
			wantConf:       ConfidenceLow,
			wantSampleNil:  true,
			wantNilMetrics: true,
		},
		{
			name: "利用率未知 UtilizationUnknown",
			in: WindowForecastInput{
				InstanceID:         1,
				AccountID:          12,
				Platform:           "anthropic",
				AccountType:        "oauth",
				Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, UtilizationUnknown: true, ResetsAt: &validResets},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusInsufficientData,
			wantConf:       ConfidenceLow,
			wantSampleNil:  true,
			wantNilMetrics: true,
		},
		{
			name: "未知窗口周期时长 T_W=0",
			in: WindowForecastInput{
				InstanceID:         1,
				AccountID:          13,
				Platform:           "anthropic",
				AccountType:        "oauth",
				Window:             SanitizedUsageWindow{WindowKey: "mystery_window", WindowDurationSec: 0, Utilization: 30, ResetsAt: &validResets},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusInsufficientData,
			wantConf:       ConfidenceLow,
			wantSampleNil:  true,
			wantNilMetrics: true,
		},
		{
			name: "过期重置点 resets_at <= observed_at 丢弃",
			in: WindowForecastInput{
				InstanceID:  1,
				AccountID:   14,
				Platform:    "anthropic",
				AccountType: "oauth",
				Window: SanitizedUsageWindow{
					WindowKey:         "five_hour",
					WindowDurationSec: 5 * 3600,
					Utilization:       50,
					ResetsAt:          ptr(now.Add(-1 * time.Second)),
				},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusInsufficientData,
			wantConf:       ConfidenceLow,
			wantSampleNil:  true,
			wantNilMetrics: true,
		},
		{
			name: "异常剩余时长 R > T_W + 120s 丢弃",
			in: WindowForecastInput{
				InstanceID:  1,
				AccountID:   15,
				Platform:    "anthropic",
				AccountType: "oauth",
				Window: SanitizedUsageWindow{
					WindowKey:         "five_hour",
					WindowDurationSec: 5 * 3600,
					Utilization:       50,
					ResetsAt:          ptr(now.Add(5*time.Hour + 121*time.Second)),
				},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusInsufficientData,
			wantConf:       ConfidenceLow,
			wantSampleNil:  true,
			wantNilMetrics: true,
		},
		{
			name: "窗口刚重置已过时长不足 E < 15m 且无历史基线 -> insufficient_data 但落库基线样本",
			in: WindowForecastInput{
				InstanceID:  1,
				AccountID:   16,
				Platform:    "anthropic",
				AccountType: "oauth",
				Window: SanitizedUsageWindow{
					WindowKey:         "five_hour",
					WindowDurationSec: 5 * 3600,
					Utilization:       10,
					ResetsAt:          ptr(now.Add(4*time.Hour + 50*time.Minute)), // E = 10m < 15m
				},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusInsufficientData,
			wantConf:       ConfidenceLow,
			wantSampleNil:  false,
			wantNilMetrics: true,
		},
		{
			name: "已耗尽 U >= 100 先行判定",
			in: WindowForecastInput{
				InstanceID:  1,
				AccountID:   17,
				Platform:    "anthropic",
				AccountType: "setup-token",
				Window: SanitizedUsageWindow{
					WindowKey:         "five_hour",
					WindowDurationSec: 5 * 3600,
					Utilization:       100,
					ResetsAt:          ptr(now.Add(4*time.Hour + 55*time.Minute)), // 即使 E=5m 也先判 exhausted
				},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			},
			wantStatus:     ForecastStatusExhausted,
			wantConf:       ConfidenceHigh,
			wantSampleNil:  false,
			wantNilMetrics: false,
		},
		{
			name: "WindowExhausted 显式标记先判 exhausted",
			in: WindowForecastInput{
				InstanceID:  1,
				AccountID:   18,
				Platform:    "anthropic",
				AccountType: "oauth",
				Window: SanitizedUsageWindow{
					WindowKey:         "five_hour",
					WindowDurationSec: 5 * 3600,
					Utilization:       98,
					ResetsAt:          ptr(now.Add(2 * time.Hour)),
				},
				UpstreamObservedAt: now,
				ObservedAt:         now,
				WindowExhausted:    true,
			},
			wantStatus:     ForecastStatusExhausted,
			wantConf:       ConfidenceHigh,
			wantSampleNil:  false,
			wantNilMetrics: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			view, sample := EvaluateWindowForecast(tc.in)
			if view.ForecastStatus != tc.wantStatus {
				t.Fatalf("status = %s, want %s (note=%q)", view.ForecastStatus, tc.wantStatus, view.Note)
			}
			if view.Confidence != tc.wantConf {
				t.Fatalf("confidence = %s, want %s", view.Confidence, tc.wantConf)
			}
			if tc.wantSampleNil && sample != nil {
				t.Fatalf("expected nil sample, got %+v", sample)
			}
			if !tc.wantSampleNil && sample == nil {
				t.Fatalf("expected non-nil sample for persistence")
			}
			if tc.wantNilMetrics {
				if view.BurnRatePerHour != nil || view.ETASeconds != nil || view.PredictedEndUtil != nil || view.MarginHours != nil {
					t.Fatalf("expected all metric pointers nil, got burn=%v eta=%v end=%v margin=%v",
						view.BurnRatePerHour, view.ETASeconds, view.PredictedEndUtil, view.MarginHours)
				}
			}
			if tc.wantStatus == ForecastStatusExhausted {
				if view.ETASeconds == nil || *view.ETASeconds != 0 {
					t.Fatalf("exhausted must set ETASeconds=0, got %v", view.ETASeconds)
				}
				if view.MarginHours == nil || *view.MarginHours >= 0 {
					t.Fatalf("exhausted must set negative MarginHours, got %v", view.MarginHours)
				}
				if view.PredictedEndUtil == nil || *view.PredictedEndUtil < 100 {
					t.Fatalf("exhausted must set PredictedEndUtil>=100, got %v", view.PredictedEndUtil)
				}
			}
		})
	}
}

func TestWindowForecastReplaySkipped(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	resetsAt := now.Add(4*time.Hour + 50*time.Minute) // E = 10m < 15m -> v_avg 无效，完全依赖 v_recent

	// 历史点与当前样本拥有相同的 upstream_observed_at（缓存重放/304 未推进），必须被跳过
	history := []storage.SiteOpsAccountWindowSample{
		{
			InstanceID:         1,
			AccountID:          20,
			Source:             "passive_cache",
			WindowKey:          "five_hour",
			Utilization:        5,
			ResetsAt:           &resetsAt,
			UpstreamObservedAt: now, // 与当前相同，不得计入
			ObservedAt:         now.Add(-15 * time.Minute),
		},
	}

	view, sample := EvaluateWindowForecast(WindowForecastInput{
		InstanceID:         1,
		AccountID:          20,
		Platform:           "anthropic",
		AccountType:        "oauth",
		Source:             "passive_cache",
		Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 20, ResetsAt: &resetsAt},
		UpstreamObservedAt: now,
		ObservedAt:         now,
		History:            history,
	})
	if sample == nil {
		t.Fatalf("expected non-nil sample")
	}
	if view.ForecastStatus != ForecastStatusInsufficientData {
		t.Fatalf("replayed upstream_observed_at must be skipped -> insufficient_data, got %s (burn=%v)", view.ForecastStatus, view.BurnRatePerHour)
	}
}

func TestWindowForecastDropResetsBaseline(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	// E = 10m < 15m（无 v_avg），便于精确验证 v_recent 是否受历史回落点控制
	resetsAt := now.Add(4*time.Hour + 50*time.Minute)

	t.Run("历史 60% -> 20% -> 35% 且当前 50%：回落开启新基线，绝不越过 20% 拼接早期 60%", func(t *testing.T) {
		history := []storage.SiteOpsAccountWindowSample{
			{
				InstanceID: 1, AccountID: 30, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 60, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-40 * time.Minute),
			},
			{
				InstanceID: 1, AccountID: 30, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 20, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-20 * time.Minute),
			},
			{
				InstanceID: 1, AccountID: 30, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 35, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-5 * time.Minute), // dt=5m < 10m
			},
		}

		view, _ := EvaluateWindowForecast(WindowForecastInput{
			InstanceID:         1,
			AccountID:          30,
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 50, ResetsAt: &resetsAt},
			UpstreamObservedAt: now,
			ObservedAt:         now,
			History:            history,
		})
		// 应以 t-20m (20%) 为基线：(50 - 20) / (20/60) = 90 %/h；绝不能拼 t-40m (60%)
		if view.BurnRatePerHour == nil || math.Abs(*view.BurnRatePerHour-90.0) > 0.01 {
			t.Fatalf("expected burn rate 90%%/h from post-drop baseline 20%%, got %v", view.BurnRatePerHour)
		}
		if view.Confidence != ConfidenceLow {
			t.Fatalf("v_recent only should have confidence=low, got %s", view.Confidence)
		}
	})

	t.Run("回落后单调段均短于 dtMin：绝不越过回落点去拼接更早高点", func(t *testing.T) {
		history := []storage.SiteOpsAccountWindowSample{
			{
				InstanceID: 1, AccountID: 31, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 60, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-30 * time.Minute),
			},
			{
				InstanceID: 1, AccountID: 31, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 20, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-8 * time.Minute), // dt=8m < 10m
			},
			{
				InstanceID: 1, AccountID: 31, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 35, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-4 * time.Minute), // dt=4m < 10m
			},
		}

		view, _ := EvaluateWindowForecast(WindowForecastInput{
			InstanceID:         1,
			AccountID:          31,
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 50, ResetsAt: &resetsAt},
			UpstreamObservedAt: now,
			ObservedAt:         now,
			History:            history,
		})
		if view.ForecastStatus != ForecastStatusInsufficientData || view.BurnRatePerHour != nil {
			t.Fatalf("must not cross drop boundary to earlier 60%% point, got status=%s burn=%v", view.ForecastStatus, view.BurnRatePerHour)
		}
	})

	t.Run("当前利用率低于最新前序点 U < U_prev：本轮仅建新基线，不产出 v_recent", func(t *testing.T) {
		history := []storage.SiteOpsAccountWindowSample{
			{
				InstanceID: 1, AccountID: 32, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 40, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-20 * time.Minute),
			},
			{
				InstanceID: 1, AccountID: 32, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 60, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-10 * time.Minute),
			},
		}

		view, _ := EvaluateWindowForecast(WindowForecastInput{
			InstanceID:         1,
			AccountID:          32,
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 55, ResetsAt: &resetsAt},
			UpstreamObservedAt: now,
			ObservedAt:         now,
			History:            history,
		})
		if view.ForecastStatus != ForecastStatusInsufficientData || view.BurnRatePerHour != nil {
			t.Fatalf("U < U_prev must not emit v_recent, got status=%s burn=%v", view.ForecastStatus, view.BurnRatePerHour)
		}
	})
}

func TestWindowForecastZeroBurnRateJSONSafe(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	resetsAt := now.Add(3 * time.Hour) // E = 2h >= 15m

	cases := []struct {
		name       string
		util       float64
		wantStatus ForecastStatus
	}{
		{name: "零速率且 U < 5% -> idle", util: 0.0, wantStatus: ForecastStatusIdle},
		{name: "极低速率且 U = 2% -> idle", util: 0.001, wantStatus: ForecastStatusIdle},
		{name: "零速率且 U >= 5% -> safe", util: 20.0, wantStatus: ForecastStatusSafe},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := WindowForecastInput{
				InstanceID:  1,
				AccountID:   40,
				Platform:    "anthropic",
				AccountType: "oauth",
				Source:      "passive_cache",
				Window: SanitizedUsageWindow{
					WindowKey:         "five_hour",
					WindowDurationSec: 5 * 3600,
					Utilization:       tc.util,
					ResetsAt:          &resetsAt,
				},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			}
			// 对 U=20% 情形，用仅 v_recent=0（E=10m < 15m）触发零速率
			if tc.util >= 5 {
				shortReset := now.Add(4*time.Hour + 50*time.Minute)
				in.Window.ResetsAt = &shortReset
				in.History = []storage.SiteOpsAccountWindowSample{
					{
						InstanceID: 1, AccountID: 40, Source: "passive_cache", WindowKey: "five_hour",
						Utilization: tc.util, ResetsAt: &shortReset, UpstreamObservedAt: now.Add(-15 * time.Minute),
					},
				}
			}

			view, _ := EvaluateWindowForecast(in)
			if view.ForecastStatus != tc.wantStatus {
				t.Fatalf("status = %s, want %s", view.ForecastStatus, tc.wantStatus)
			}
			if view.BurnRatePerHour == nil || *view.BurnRatePerHour != 0 {
				t.Fatalf("expected burn_rate_per_hour=0, got %v", view.BurnRatePerHour)
			}
			if view.ETASeconds != nil || view.MarginHours != nil {
				t.Fatalf("zero burn rate must have nil eta/margin, got eta=%v margin=%v", view.ETASeconds, view.MarginHours)
			}
			if view.PredictedEndUtil == nil || math.Abs(*view.PredictedEndUtil-tc.util) > 0.001 {
				t.Fatalf("predicted_end_util = %v, want %v", view.PredictedEndUtil, tc.util)
			}

			raw, err := json.Marshal(view)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}
			s := string(raw)
			if !strings.Contains(s, `"eta_seconds":null`) || !strings.Contains(s, `"margin_hours":null`) {
				t.Fatalf("expected explicit null eta_seconds and margin_hours in JSON: %s", s)
			}
		})
	}
}

func TestWindowForecastThresholdsAndDualRateFloor(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	resetsAt := now.Add(3 * time.Hour) // 5h 窗口已过 E = 2h，剩余 R = 3h

	// 1. 分级阈值验证（仅 v_avg，置信度 medium）：
	//    v_avg = U / 2h，PredictedEndUtil = U + (U/2)*3 = 2.5*U
	//    - U = 30% -> U_end = 75% (< 85%) -> safe
	//    - U = 36% -> U_end = 90% (85..100) -> tight
	//    - U = 41% -> U_end = 102.5% (100..105) -> will_exhaust
	//    - U = 42% -> U_end = 105.0% (>= 105) -> will_exhaust_high
	gradeCases := []struct {
		name       string
		util       float64
		wantStatus ForecastStatus
		wantEnd    float64
	}{
		{"U_end=75% -> safe", 30, ForecastStatusSafe, 75},
		{"U_end=90% -> tight", 36, ForecastStatusTight, 90},
		{"U_end=102.5% -> will_exhaust", 41, ForecastStatusWillExhaust, 102.5},
		{"U_end=105% -> will_exhaust_high", 42, ForecastStatusWillExhaustHigh, 105},
	}
	for _, tc := range gradeCases {
		t.Run(tc.name, func(t *testing.T) {
			view, _ := EvaluateWindowForecast(WindowForecastInput{
				InstanceID:         1,
				AccountID:          50,
				Platform:           "anthropic",
				AccountType:        "oauth",
				Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: tc.util, ResetsAt: &resetsAt},
				UpstreamObservedAt: now,
				ObservedAt:         now,
			})
			if view.ForecastStatus != tc.wantStatus {
				t.Fatalf("status = %s, want %s", view.ForecastStatus, tc.wantStatus)
			}
			if view.Confidence != ConfidenceMedium {
				t.Fatalf("v_avg only confidence = %s, want medium", view.Confidence)
			}
			if view.PredictedEndUtil == nil || math.Abs(*view.PredictedEndUtil-tc.wantEnd) > 0.01 {
				t.Fatalf("predicted_end_util = %v, want %v", view.PredictedEndUtil, tc.wantEnd)
			}
		})
	}

	// 2. 双速融合保守下界：v_avg = 40%/2h = 20%/h，近期完全平坦 v_recent = 0%/h
	//    v_raw = 0.6*0 + 0.4*20 = 8%/h；保守下界 0.8*v_avg = 16%/h -> v_eff = 16%/h，置信度 high
	t.Run("双速融合触发 0.8*v_avg 保守下界", func(t *testing.T) {
		history := []storage.SiteOpsAccountWindowSample{
			{
				InstanceID: 1, AccountID: 51, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 40, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-15 * time.Minute),
			},
		}
		view, _ := EvaluateWindowForecast(WindowForecastInput{
			InstanceID:         1,
			AccountID:          51,
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 40, ResetsAt: &resetsAt},
			UpstreamObservedAt: now,
			ObservedAt:         now,
			History:            history,
		})
		if view.Confidence != ConfidenceHigh {
			t.Fatalf("dual rate confidence = %s, want high", view.Confidence)
		}
		if view.BurnRatePerHour == nil || math.Abs(*view.BurnRatePerHour-16.0) > 0.01 {
			t.Fatalf("expected conservative floor 16%%/h, got %v", view.BurnRatePerHour)
		}
		// U_end = 40 + 16*3 = 88% -> tight
		if view.ForecastStatus != ForecastStatusTight {
			t.Fatalf("status = %s, want tight", view.ForecastStatus)
		}
	})

	// 3. 双速融合加速情形：v_avg = 20%/h，近期飙升 v_recent = 40%/h
	//    v_raw = 0.6*40 + 0.4*20 = 32%/h (> 16%/h) -> v_eff = 32%/h
	t.Run("双速融合高增速取加权值", func(t *testing.T) {
		history := []storage.SiteOpsAccountWindowSample{
			{
				InstanceID: 1, AccountID: 52, Source: "passive_cache", WindowKey: "five_hour",
				Utilization: 30, ResetsAt: &resetsAt, UpstreamObservedAt: now.Add(-15 * time.Minute),
			},
		}
		view, _ := EvaluateWindowForecast(WindowForecastInput{
			InstanceID:         1,
			AccountID:          52,
			Platform:           "anthropic",
			AccountType:        "oauth",
			Source:             "passive_cache",
			Window:             SanitizedUsageWindow{WindowKey: "five_hour", WindowDurationSec: 5 * 3600, Utilization: 40, ResetsAt: &resetsAt},
			UpstreamObservedAt: now,
			ObservedAt:         now,
			History:            history,
		})
		if view.BurnRatePerHour == nil || math.Abs(*view.BurnRatePerHour-32.0) > 0.01 {
			t.Fatalf("expected weighted burn rate 32%%/h, got %v", view.BurnRatePerHour)
		}
		if view.ForecastStatus != ForecastStatusWillExhaustHigh {
			t.Fatalf("expected will_exhaust_high, got %s", view.ForecastStatus)
		}
	})
}

func TestForecastSummarizeAccountEarliestBlockingWindow(t *testing.T) {
	meta := SanitizedAccountSummary{
		AccountID: 60,
		Name:      "claude-pro-1",
		Platform:  "anthropic",
		Type:      "oauth",
		GroupIDs:  []int64{1},
	}

	t.Run("exhausted 最优先", func(t *testing.T) {
		acc := SummarizeAccountForecast(meta, []WindowForecastView{
			{WindowKey: "five_hour", ForecastStatus: ForecastStatusWillExhaustHigh, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(1800)), MarginHours: ptr(-1.0)},
			{WindowKey: "seven_day", ForecastStatus: ForecastStatusExhausted, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(0)), MarginHours: ptr(-24.0)},
		})
		if acc.OverallStatus != ForecastStatusExhausted || acc.EarliestBlockingKey != "seven_day" {
			t.Fatalf("expected seven_day exhausted, got status=%s key=%s", acc.OverallStatus, acc.EarliestBlockingKey)
		}
	})

	t.Run("多个将耗尽窗口取最小 ETASeconds", func(t *testing.T) {
		acc := SummarizeAccountForecast(meta, []WindowForecastView{
			{WindowKey: "seven_day", ForecastStatus: ForecastStatusWillExhaustHigh, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(14400)), MarginHours: ptr(-20.0)},
			{WindowKey: "five_hour", ForecastStatus: ForecastStatusWillExhaust, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(3600)), MarginHours: ptr(-0.2)},
		})
		if acc.OverallStatus != ForecastStatusWillExhaust || acc.EarliestBlockingKey != "five_hour" {
			t.Fatalf("expected earliest blocking five_hour, got status=%s key=%s", acc.OverallStatus, acc.EarliestBlockingKey)
		}
	})

	t.Run("阻塞窗口优先于将在重置前复位的 safe 窗口", func(t *testing.T) {
		acc := SummarizeAccountForecast(meta, []WindowForecastView{
			{WindowKey: "five_hour", ForecastStatus: ForecastStatusSafe, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(3600)), MarginHours: ptr(0.5)},
			{WindowKey: "seven_day", ForecastStatus: ForecastStatusWillExhaustHigh, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(18000)), MarginHours: ptr(-30.0)},
		})
		if acc.OverallStatus != ForecastStatusWillExhaustHigh || acc.EarliestBlockingKey != "seven_day" {
			t.Fatalf("expected blocking seven_day over safe five_hour, got status=%s key=%s", acc.OverallStatus, acc.EarliestBlockingKey)
		}
	})

	t.Run("按量号始终输出 excluded_payg 与 insufficient_data", func(t *testing.T) {
		payg := SummarizeAccountForecast(SanitizedAccountSummary{
			AccountID: 61, Name: "api-key-1", Platform: "anthropic", Type: "apikey",
		}, []WindowForecastView{
			{WindowKey: "five_hour", ForecastStatus: ForecastStatusWillExhaustHigh, Confidence: ConfidenceHigh},
		})
		if payg.PassiveSupport != PassiveSupportExcludedPayg || payg.OverallStatus != ForecastStatusInsufficientData {
			t.Fatalf("unexpected payg summary: %+v", payg)
		}
	})
}

func TestPoolForecastGroupRiskCalculation(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	groups := []SanitizedGroupInfo{
		{ID: 1, Name: "claude-high-risk", Platform: "anthropic"},
		{ID: 2, Name: "claude-low-sustain", Platform: "anthropic"},
		{ID: 3, Name: "claude-healthy", Platform: "anthropic"},
	}
	accounts := []SanitizedAccountSummary{
		// Group 1: 5 个被动证实账号，2 个高危/耗尽（40% >= 40%）-> CannotSustainRisk = true
		{AccountID: 101, Name: "a1", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{1}},
		{AccountID: 102, Name: "a2", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{1}},
		{AccountID: 103, Name: "a3", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{1}},
		{AccountID: 104, Name: "a4", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{1}},
		{AccountID: 105, Name: "a5", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{1}},
		// Group 1 中混入 1 个按量号和 1 个未证实号，不得稀释分母
		{AccountID: 106, Name: "payg", Platform: "anthropic", Type: "apikey", GroupIDs: []int64{1}},
		{AccountID: 107, Name: "openai", Platform: "openai", Type: "oauth", GroupIDs: []int64{1}},
		// Group 2: 3 个被动证实账号（1 exhausted + 1 safe + 1 low 置信度 safe），有效评估仅 2 个，sustainable=1<=1 且 highRisk=1>0 -> true
		{AccountID: 201, Name: "b1", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{2}},
		{AccountID: 202, Name: "b2", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{2}},
		{AccountID: 203, Name: "b3-low", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{2}},
		// Group 3: 3 个被动证实账号（1 will_exhaust_high + 2 safe -> 33.33% < 40%，且 sustainable=2 > 1）-> false
		{AccountID: 301, Name: "c1", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{3}},
		{AccountID: 302, Name: "c2", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{3}},
		{AccountID: 303, Name: "c3", Platform: "anthropic", Type: "oauth", GroupIDs: []int64{3}},
	}
	windowMap := map[int64][]WindowForecastView{
		101: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusExhausted, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(0))}},
		102: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusWillExhaustHigh, Confidence: ConfidenceMedium, ETASeconds: ptr(int64(3600))}},
		103: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusSafe, Confidence: ConfidenceHigh}},
		104: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusTight, Confidence: ConfidenceHigh}},
		105: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusIdle, Confidence: ConfidenceMedium}},
		201: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusExhausted, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(0))}},
		202: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusSafe, Confidence: ConfidenceMedium}},
		203: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusSafe, Confidence: ConfidenceLow}}, // low 置信度不计入评估
		301: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusWillExhaustHigh, Confidence: ConfidenceHigh, ETASeconds: ptr(int64(3600))}},
		302: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusSafe, Confidence: ConfidenceHigh}},
		303: {{WindowKey: "five_hour", ForecastStatus: ForecastStatusSafe, Confidence: ConfidenceHigh}},
	}

	resp := BuildPoolForecast(1, &now, QualityTrusted, accounts, groups, windowMap)
	if !strings.Contains(resp.ResetConstraintNote, "不保证账号一定恢复可用") {
		t.Fatalf("missing reset constraint note: %q", resp.ResetConstraintNote)
	}
	if resp.PassiveMatrixNote == "" {
		t.Fatalf("expected non-empty PassiveMatrixNote")
	}
	if resp.ProvenCount != 11 || resp.UnverifiedCount != 1 || resp.ExcludedPaygCount != 1 {
		t.Fatalf("unexpected support counts: proven=%d unverified=%d payg=%d", resp.ProvenCount, resp.UnverifiedCount, resp.ExcludedPaygCount)
	}

	byGroup := make(map[int64]GroupForecastRiskView, len(resp.Groups))
	for _, g := range resp.Groups {
		byGroup[g.GroupID] = g
	}

	g1 := byGroup[1]
	if !g1.CannotSustainRisk || g1.HighRiskRatioPct == nil || math.Abs(*g1.HighRiskRatioPct-40.0) > 0.01 {
		t.Fatalf("group 1 expected CannotSustainRisk=true and ratio=40%%, got %+v", g1)
	}
	g2 := byGroup[2]
	if !g2.CannotSustainRisk || g2.HighRiskRatioPct == nil || math.Abs(*g2.HighRiskRatioPct-50.0) > 0.01 {
		t.Fatalf("group 2 expected CannotSustainRisk=true and ratio=50%% (excluding low confidence), got %+v", g2)
	}
	g3 := byGroup[3]
	if g3.CannotSustainRisk || g3.HighRiskRatioPct == nil || math.Abs(*g3.HighRiskRatioPct-33.33) > 0.05 {
		t.Fatalf("group 3 expected CannotSustainRisk=false and ratio=33.33%%, got %+v", g3)
	}
}

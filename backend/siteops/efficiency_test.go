package siteops

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestEfficiencyNormalAndEdgeCases(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	accounts := []SanitizedAccountSummary{
		{
			AccountID:   1,
			Name:        "claude-max-1",
			Platform:    "anthropic",
			Type:        "oauth",
			Status:      "active",
			Concurrency: 5,
		},
		{
			AccountID:   2,
			Name:        "zero-conc-zero-cost",
			Platform:    "openai",
			Type:        "apikey",
			Status:      "active",
			Concurrency: 0, // 零并发 -> OutputPerConcurrencyUSD / ReqPerConcurrency 必为 nil
		},
		{
			AccountID:   3,
			Name:        "low-window-samples",
			Platform:    "anthropic",
			Type:        "oauth",
			Status:      "active",
			Concurrency: 4,
		},
		{
			AccountID:   4,
			Name:        "no-stats-account",
			Platform:    "gemini",
			Type:        "oauth",
			Status:      "active",
			Concurrency: 2,
		},
	}

	stats := map[int64]*SanitizedAccountStats{
		1: {
			AccountID:         1,
			Days:              30,
			ActualDaysUsed:    30,
			TotalRequests:     1000,
			TotalTokens:       5_000_000,
			TotalStandardCost: 600,
			TotalCost:         300,
			TotalUserCost:     450,
			TodayRequests:     50,
			TodayStandardCost: 40,
			TodayCost:         20,
			TodayUserCost:     25,
		},
		2: {
			AccountID:         2,
			Days:              30,
			TotalRequests:     10,
			TotalStandardCost: 10,
			TotalCost:         0, // 零分母 -> MonetizationRatio 必为 nil
			TotalUserCost:     15,
			TodayRequests:     5,
			TodayCost:         0,
			TodayUserCost:     5,
		},
		3: {
			AccountID:         3,
			Days:              30,
			TotalRequests:     100,
			TotalStandardCost: 120,
			TotalCost:         100,
			TotalUserCost:     110,
			TodayRequests:     20,
			TodayCost:         10,
			TodayUserCost:     12,
		},
	}

	windows := map[int64][]SanitizedUsageWindow{
		1: {
			// five_hour 达标：50% -> 100 / 0.5 = 200
			{
				WindowKey:         "five_hour",
				WindowDurationSec: 5 * 3600,
				Utilization:       50,
				WindowStats:       &SanitizedWindowStats{Requests: 40, StandardCost: 100, Cost: 50, UserCost: 75},
			},
			// seven_day 达标且窗口更长：25% -> 150 / 0.25 = 600（应优先选中）
			{
				WindowKey:         "seven_day",
				WindowDurationSec: 7 * 24 * 3600,
				Utilization:       25,
				WindowStats:       &SanitizedWindowStats{Requests: 80, StandardCost: 150, Cost: 75, UserCost: 112.5},
			},
		},
		3: {
			// 利用率 < 10% 不应计算
			{
				WindowKey:         "five_hour",
				WindowDurationSec: 5 * 3600,
				Utilization:       9.5,
				WindowStats:       &SanitizedWindowStats{Requests: 50, StandardCost: 20},
			},
			// 请求数 < 20 不应计算
			{
				WindowKey:         "seven_day",
				WindowDurationSec: 7 * 24 * 3600,
				Utilization:       30,
				WindowStats:       &SanitizedWindowStats{Requests: 19, StandardCost: 60},
			},
		},
	}

	costs := map[int64]storage.SiteOpsAccountCost{
		1: {InstanceID: 1, AccountID: 1, MonthlyCostUSD: 200, Note: "Max 20x 订阅参考价", UpdatedAt: now},
		2: {InstanceID: 1, AccountID: 2, MonthlyCostUSD: 0, Note: "未录入"},
		3: {InstanceID: 1, AccountID: 3, MonthlyCostUSD: -10, Note: "非法负月租"},
	}

	// 不显式传 TodayStatsBatchGated，验证默认开启门禁
	resp := BuildAccountsEfficiency(EfficiencyInput{
		InstanceID:    1,
		ObservedAt:    &now,
		TrustState:    QualityTrusted,
		Accounts:      accounts,
		Stats:         stats,
		LatestWindows: windows,
		Costs:         costs,
	})

	if !resp.TodayStatsBatchGated {
		t.Fatalf("expected TodayStatsBatchGated=true by default")
	}
	if !strings.Contains(resp.TodayStatsBatchGateNote, "v0.2.24") || !strings.Contains(resp.TodayStatsBatchGateNote, "accounts/:id/stats") {
		t.Fatalf("unexpected TodayStatsBatchGateNote: %q", resp.TodayStatsBatchGateNote)
	}
	if !strings.Contains(resp.MetricSemanticsNote, "standard_cost") || !strings.Contains(resp.MetricSemanticsNote, "account_cost") || !strings.Contains(resp.MetricSemanticsNote, "user_cost") {
		t.Fatalf("MetricSemanticsNote must preserve all 3 cost scopes: %q", resp.MetricSemanticsNote)
	}

	byID := make(map[int64]AccountEfficiencyItem, len(resp.Items))
	for _, item := range resp.Items {
		byID[item.AccountID] = item
	}

	// 账号 #1：全指标正常
	a1 := byID[1]
	if a1.StatsSource != EndpointAccountsStats {
		t.Fatalf("expected stats_source=%s, got %s", EndpointAccountsStats, a1.StatsSource)
	}
	if a1.StandardCost30d != 600 || a1.AccountCost30d != 300 || a1.UserCost30d != 450 {
		t.Fatalf("3 cost scopes mismatch: %+v", a1)
	}
	if a1.MonetizationRatio == nil || math.Abs(*a1.MonetizationRatio-1.5) > 0.001 {
		t.Fatalf("monetization_ratio = %v, want 1.5", a1.MonetizationRatio)
	}
	if a1.OutputPerConcurrencyUSD == nil || math.Abs(*a1.OutputPerConcurrencyUSD-5.0) > 0.001 {
		t.Fatalf("output_per_concurrency_usd = %v, want 5.0", a1.OutputPerConcurrencyUSD)
	}
	if a1.ReqPerConcurrency == nil || math.Abs(*a1.ReqPerConcurrency-10.0) > 0.001 {
		t.Fatalf("req_per_concurrency = %v, want 10.0", a1.ReqPerConcurrency)
	}
	if a1.WindowFullLeverageUSD == nil || math.Abs(*a1.WindowFullLeverageUSD-600.0) > 0.001 || a1.WindowLeverageKey != "seven_day" {
		t.Fatalf("window_full_leverage = %v (%s), want 600 (seven_day)", a1.WindowFullLeverageUSD, a1.WindowLeverageKey)
	}
	if a1.MonthlyCostUSD == nil || *a1.MonthlyCostUSD != 200 {
		t.Fatalf("monthly_cost_usd = %v, want 200", a1.MonthlyCostUSD)
	}
	if a1.StandardCostLeverage == nil || math.Abs(*a1.StandardCostLeverage-3.0) > 0.001 {
		t.Fatalf("standard_cost_leverage = %v, want 3.0", a1.StandardCostLeverage)
	}
	if a1.UserCostPerMonthlyRef == nil || math.Abs(*a1.UserCostPerMonthlyRef-2.25) > 0.001 {
		t.Fatalf("user_cost_per_monthly_ref = %v, want 2.25", a1.UserCostPerMonthlyRef)
	}

	// 账号 #2：零并发、零成本、零月租 -> 所有派生指标必为 nil 且 JSON 为 null
	a2 := byID[2]
	if a2.MonetizationRatio != nil || a2.OutputPerConcurrencyUSD != nil || a2.ReqPerConcurrency != nil ||
		a2.MonthlyCostUSD != nil || a2.StandardCostLeverage != nil || a2.UserCostPerMonthlyRef != nil {
		t.Fatalf("zero denominators must return nil pointers, got %+v", a2)
	}
	rawA2, err := json.Marshal(a2)
	if err != nil {
		t.Fatalf("json.Marshal a2 failed: %v", err)
	}
	for _, field := range []string{`"monetization_ratio":null`, `"output_per_concurrency_usd":null`, `"monthly_cost_usd":null`, `"standard_cost_leverage":null`, `"user_cost_per_monthly_ref":null`} {
		if !strings.Contains(string(rawA2), field) {
			t.Fatalf("expected %s in JSON: %s", field, string(rawA2))
		}
	}

	// 账号 #3：窗口样本不足 (<10% / <20 req) 与负月租 -> WindowFullLeverageUSD / MonthlyCostUSD 必为 nil
	a3 := byID[3]
	if a3.WindowFullLeverageUSD != nil || a3.WindowLeverageKey != "" {
		t.Fatalf("insufficient window samples must return nil leverage, got %v (%s)", a3.WindowFullLeverageUSD, a3.WindowLeverageKey)
	}
	if a3.MonthlyCostUSD != nil || a3.StandardCostLeverage != nil || a3.UserCostPerMonthlyRef != nil {
		t.Fatalf("negative monthly cost must be ignored, got %+v", a3)
	}

	// 账号 #4：无 stats -> unavailable 且全部比率为 nil
	a4 := byID[4]
	if a4.StatsSource != "unavailable" || a4.MonetizationRatio != nil || a4.OutputPerConcurrencyUSD != nil {
		t.Fatalf("no-stats account mismatch: %+v", a4)
	}
}

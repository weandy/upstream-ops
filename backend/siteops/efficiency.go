package siteops

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	todayStatsBatchGatedNote   = "受 Sub2API v0.2.24 共享缓存先于逐账号授权校验的风险约束，当前默认禁用 POST /api/v1/admin/accounts/today-stats/batch，已退用经逐账号鉴权的 GET /api/v1/admin/accounts/:id/stats?days=30"
	todayStatsBatchUnblocked   = "目标实例已核验 today-stats/batch 逐账号授权顺序"
	efficiencyMetricSemantics  = "完整保留 Sub2API 三口径：standard_cost（官方标价参考）、account_cost（站内账号折算成本）、user_cost（用户账面扣费）；user_cost_per_monthly_ref 表示「账面扣费 / 参考月租」，绝不等同现金 ROI"
	windowLeverageMinUtilPct   = 10.0
	windowLeverageMinRequests  = int64(20)
)

// EfficiencyInput M5 单号成本效率评估输入。
type EfficiencyInput struct {
	InstanceID              uint
	ObservedAt              *time.Time
	TrustState              FieldQualityState
	Accounts                []SanitizedAccountSummary
	Stats                   map[int64]*SanitizedAccountStats
	LatestWindows           map[int64][]SanitizedUsageWindow
	Costs                   map[int64]storage.SiteOpsAccountCost
	TodayStatsBatchGated    bool
	TodayStatsBatchVerified bool
}

// BuildAccountsEfficiency 聚合全站账号的 M5 三口径成本、容量归一化产出、同窗满额撬动价值与可选参考月租杠杆。
func BuildAccountsEfficiency(in EfficiencyInput) AccountsEfficiencyResponse {
	if in.TrustState == "" {
		in.TrustState = QualityUnavailable
	}
	gated := true
	if in.TodayStatsBatchVerified && !in.TodayStatsBatchGated {
		gated = false
	}
	gateNote := todayStatsBatchGatedNote
	if !gated {
		gateNote = todayStatsBatchUnblocked
	}

	resp := AccountsEfficiencyResponse{
		InstanceID:              in.InstanceID,
		ObservedAt:              cloneTimePtr(in.ObservedAt),
		TrustState:              in.TrustState,
		TodayStatsBatchGated:    gated,
		TodayStatsBatchGateNote: gateNote,
		MetricSemanticsNote:     efficiencyMetricSemantics,
		Items:                   make([]AccountEfficiencyItem, 0, len(in.Accounts)),
	}

	seen := make(map[int64]struct{}, len(in.Accounts))
	for _, acc := range in.Accounts {
		if acc.AccountID <= 0 {
			continue
		}
		if _, dup := seen[acc.AccountID]; dup {
			continue
		}
		seen[acc.AccountID] = struct{}{}

		item := AccountEfficiencyItem{
			AccountID:          acc.AccountID,
			Name:               SanitizeErrorText(acc.Name, 128),
			Platform:           strings.TrimSpace(acc.Platform),
			AccountType:        strings.TrimSpace(acc.Type),
			Status:             strings.TrimSpace(acc.Status),
			CurrentConcurrency: acc.Concurrency,
			StatsSource:        "unavailable",
		}

		st := in.Stats[acc.AccountID]
		if st != nil {
			if gated {
				item.StatsSource = EndpointAccountsStats
			} else {
				item.StatsSource = EndpointAccountsTodayStatsBatch
			}
			if st.Days > 0 {
				item.DaysWindow = st.Days
			} else if st.ActualDaysUsed > 0 {
				item.DaysWindow = st.ActualDaysUsed
			}

			item.TotalRequests = st.TotalRequests
			item.TotalTokens = st.TotalTokens
			item.StandardCost30d = st.TotalStandardCost
			item.AccountCost30d = st.TotalCost
			item.UserCost30d = st.TotalUserCost

			item.TodayRequests = st.TodayRequests
			item.TodayStandardCost = st.TodayStandardCost
			item.TodayAccountCost = st.TodayCost
			item.TodayUserCost = st.TodayUserCost

			// 账号变现倍率比：user_cost / account_cost（优先 30d，零/负分母返回 nil）
			if st.TotalCost > 0 {
				v := round4(st.TotalUserCost / st.TotalCost)
				item.MonetizationRatio = &v
			} else if st.TotalCost == 0 && st.TodayCost > 0 {
				v := round4(st.TodayUserCost / st.TodayCost)
				item.MonetizationRatio = &v
			}

			// 当前容量归一化产出：current_concurrency <= 0 一律返回 nil
			if acc.Concurrency > 0 {
				conc := float64(acc.Concurrency)
				outUSD := round4(st.TodayUserCost / conc)
				reqPerConc := round4(float64(st.TodayRequests) / conc)
				item.OutputPerConcurrencyUSD = &outUSD
				item.ReqPerConcurrency = &reqPerConc
			}
		}

		// 同窗满额撬动价值：仅在同窗样本充足（utilization >= 10%, requests >= 20, standard_cost > 0）时计算
		if leverageUSD, winKey, ok := selectWindowFullLeverage(in.LatestWindows[acc.AccountID]); ok {
			item.WindowFullLeverageUSD = &leverageUSD
			item.WindowLeverageKey = winKey
		}

		// 可选本地参考月租与杠杆（未录入或 <= 0 一律返回 nil）
		if cost, ok := in.Costs[acc.AccountID]; ok && cost.MonthlyCostUSD > 0 && !math.IsNaN(cost.MonthlyCostUSD) && !math.IsInf(cost.MonthlyCostUSD, 0) {
			mc := round4(cost.MonthlyCostUSD)
			item.MonthlyCostUSD = &mc
			item.MonthlyCostNote = SanitizeErrorText(cost.Note, 256)
			if !cost.UpdatedAt.IsZero() {
				ts := cost.UpdatedAt.UTC()
				item.CostUpdatedAt = &ts
			}
			if st != nil {
				stdLev := round4(st.TotalStandardCost / cost.MonthlyCostUSD)
				userRef := round4(st.TotalUserCost / cost.MonthlyCostUSD)
				item.StandardCostLeverage = &stdLev
				item.UserCostPerMonthlyRef = &userRef
			}
		}

		resp.Items = append(resp.Items, item)
	}

	// 稳定排序：按 30d 用户账面扣费降序，其次 30d 官方标价降序，最后按 AccountID 升序
	slices.SortFunc(resp.Items, func(a, b AccountEfficiencyItem) int {
		if a.UserCost30d != b.UserCost30d {
			if a.UserCost30d > b.UserCost30d {
				return -1
			}
			return 1
		}
		if a.StandardCost30d != b.StandardCost30d {
			if a.StandardCost30d > b.StandardCost30d {
				return -1
			}
			return 1
		}
		if a.AccountID < b.AccountID {
			return -1
		}
		if a.AccountID > b.AccountID {
			return 1
		}
		return 0
	})

	return resp
}

func selectWindowFullLeverage(windows []SanitizedUsageWindow) (float64, string, bool) {
	var best *SanitizedUsageWindow
	bestDur := int64(-1)
	bestReq := int64(-1)

	for i := range windows {
		w := &windows[i]
		if w.UtilizationUnknown || math.IsNaN(w.Utilization) || math.IsInf(w.Utilization, 0) || w.Utilization < windowLeverageMinUtilPct {
			continue
		}
		if w.WindowStats == nil || w.WindowStats.Requests < windowLeverageMinRequests || w.WindowStats.StandardCost <= 0 {
			continue
		}
		if math.IsNaN(w.WindowStats.StandardCost) || math.IsInf(w.WindowStats.StandardCost, 0) {
			continue
		}
		dur := w.WindowDurationSec
		if dur <= 0 {
			dur = WindowDurationForKey(w.WindowKey)
		}
		req := w.WindowStats.Requests
		if best == nil ||
			dur > bestDur ||
			(dur == bestDur && req > bestReq) ||
			(dur == bestDur && req == bestReq && strings.TrimSpace(w.WindowKey) < strings.TrimSpace(best.WindowKey)) {
			best = w
			bestDur = dur
			bestReq = req
		}
	}

	if best == nil {
		return 0, "", false
	}
	leverage := round4(best.WindowStats.StandardCost / (best.Utilization / 100.0))
	return leverage, strings.TrimSpace(best.WindowKey), true
}

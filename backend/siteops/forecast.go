package siteops

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

const (
	forecastResetTolerance        = 120 * time.Second
	forecastZeroBurnRateThreshold = 0.001

	PoolResetConstraintNote = "单个窗口到点重置只代表该项约束可能解除，不保证账号一定恢复可用（仍可能受更长周期窗口或账号状态约束）"
	PoolPassiveMatrixNote   = "仅对经被动只读证明矩阵核验的平台与账号类型（首批为 Anthropic OAuth / SetupToken）开启后台周期预测；未证实平台默认不自动拉取，按量 API Key / Upstream 转发号永久排除出预测分母"
)

// WindowForecastInput 单个账号单个窗口的 M4 周期预测评估输入。
type WindowForecastInput struct {
	InstanceID         uint
	AccountID          int64
	AccountName        string
	Platform           string
	AccountType        string
	Source             string
	Window             SanitizedUsageWindow
	UpstreamObservedAt time.Time
	ObservedAt         time.Time
	WindowExhausted    bool
	History            []storage.SiteOpsAccountWindowSample
}

// EvaluateWindowForecast 按 M4 标准化双速燃烧率算法评估单个周期窗口的续航预测，
// 并在满足被动只读准入及基本样本合法性时构造待持久化的 SiteOpsAccountWindowSample。
func EvaluateWindowForecast(in WindowForecastInput) (WindowForecastView, *storage.SiteOpsAccountWindowSample) {
	observedAt := in.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	upstreamObservedAt := in.UpstreamObservedAt.UTC()
	if upstreamObservedAt.IsZero() {
		upstreamObservedAt = observedAt
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "passive_cache"
	}
	windowKey := strings.TrimSpace(in.Window.WindowKey)
	windowDurationSec := in.Window.WindowDurationSec
	if windowDurationSec <= 0 {
		windowDurationSec = WindowDurationForKey(windowKey)
	}

	var resetsAt *time.Time
	if in.Window.ResetsAt != nil && !in.Window.ResetsAt.IsZero() {
		t := in.Window.ResetsAt.UTC()
		resetsAt = &t
	}

	util := in.Window.Utilization
	view := WindowForecastView{
		WindowKey:          windowKey,
		Source:             source,
		WindowDurationSec:  windowDurationSec,
		Utilization:        util,
		ResetsAt:           cloneTimePtr(resetsAt),
		RemainingSeconds:   0,
		UpstreamObservedAt: upstreamObservedAt,
		ForecastStatus:     ForecastStatusInsufficientData,
		Confidence:         ConfidenceLow,
	}

	// 前置准入校验 1：被动只读证明矩阵
	support, supportReason := EvaluatePassiveSupport(in.Platform, in.AccountType)
	if support != PassiveSupportProven {
		view.Note = supportReason
		return view, nil
	}

	// 前置准入校验 2：利用率已知且为合法非负有限值
	if in.Window.UtilizationUnknown || math.IsNaN(util) || math.IsInf(util, 0) || util < 0 {
		view.Utilization = 0
		view.Note = "窗口利用率未知或无效"
		return view, nil
	}

	// 前置准入校验 3：窗口周期总时长必须已知 > 0
	if windowDurationSec <= 0 {
		view.Note = "未知窗口周期时长，无法计算重置周期预测"
		return view, nil
	}

	// 前置准入校验 4：重置时间必须存在且晚于当前观测时间（排除过期重置点）
	if resetsAt == nil || !resetsAt.After(observedAt) {
		view.Note = "窗口重置时间缺失或已过期，已丢弃本轮样本"
		return view, nil
	}

	// 前置准入校验 5：剩余时长满足 0 < R <= T_W + 120s（排除异常剩余时长）
	tw := time.Duration(windowDurationSec) * time.Second
	rem := resetsAt.Sub(observedAt)
	if rem <= 0 || rem > tw+forecastResetTolerance {
		view.Note = "窗口剩余时长超出合法周期范围，已丢弃本轮样本"
		return view, nil
	}

	remSec := int64(math.Round(rem.Seconds()))
	if remSec <= 0 {
		remSec = 1
	}
	view.RemainingSeconds = remSec
	remHours := rem.Hours()

	buildSample := func(v WindowForecastView) *storage.SiteOpsAccountWindowSample {
		s := &storage.SiteOpsAccountWindowSample{
			InstanceID:         in.InstanceID,
			AccountID:          in.AccountID,
			AccountName:        SanitizeErrorText(in.AccountName, 128),
			Platform:           strings.TrimSpace(in.Platform),
			AccountType:        strings.TrimSpace(in.AccountType),
			Source:             source,
			WindowKey:          windowKey,
			WindowDurationSec:  windowDurationSec,
			Utilization:        util,
			ResetsAt:           cloneTimePtr(resetsAt),
			RemainingSeconds:   v.RemainingSeconds,
			UpstreamObservedAt: upstreamObservedAt,
			BurnRatePerHour:    cloneFloatPtr(v.BurnRatePerHour),
			ETASeconds:         cloneInt64Ptr(v.ETASeconds),
			PredictedEndUtil:   cloneFloatPtr(v.PredictedEndUtil),
			MarginHours:        cloneFloatPtr(v.MarginHours),
			ForecastStatus:     string(v.ForecastStatus),
			Confidence:         string(v.Confidence),
			ObservedAt:         observedAt,
		}
		if in.Window.WindowStats != nil {
			sc := in.Window.WindowStats.StandardCost
			uc := in.Window.WindowStats.UserCost
			req := in.Window.WindowStats.Requests
			s.WindowStandardCost = &sc
			s.WindowUserCost = &uc
			s.WindowRequests = &req
		}
		return s
	}

	// 步骤 1（先判已耗尽）
	if util >= 100 || in.WindowExhausted {
		zeroETA := int64(0)
		margin := round4(-remHours)
		predEnd := round4(math.Max(util, 100))
		view.ForecastStatus = ForecastStatusExhausted
		view.Confidence = ConfidenceHigh
		view.ETASeconds = &zeroETA
		view.MarginHours = &margin
		view.PredictedEndUtil = &predEnd
		return view, buildSample(view)
	}

	// 步骤 2（双速燃烧率 + 回落新基线 + 递增校验 + 零速率安全）
	elapsed := tw - rem
	eMin := minElapsedForWindow(windowDurationSec)
	var vAvg float64
	vAvgValid := false
	if elapsed >= eMin && elapsed > 0 {
		vAvg = util / elapsed.Hours()
		vAvgValid = true
	}

	vRecent, vRecentValid := computeRecentBurnRate(in.InstanceID, in.AccountID, windowKey, source, *resetsAt, upstreamObservedAt, util, windowDurationSec, in.History)

	var vEff float64
	var confidence ForecastConfidence
	switch {
	case vAvgValid && vRecentValid:
		vRaw := 0.6*vRecent + 0.4*vAvg
		vEff = math.Max(vRaw, 0.8*vAvg)
		confidence = ConfidenceHigh
	case vAvgValid:
		vEff = vAvg
		confidence = ConfidenceMedium
	case vRecentValid:
		vEff = vRecent
		confidence = ConfidenceLow
	default:
		view.ForecastStatus = ForecastStatusInsufficientData
		view.Confidence = ConfidenceLow
		view.Note = "窗口刚重置或有效历史基线不足，暂不输出预测数值"
		return view, buildSample(view)
	}

	// 零速率安全表达（严禁 +Inf / NaN）
	if math.IsNaN(vEff) || math.IsInf(vEff, 0) || vEff <= forecastZeroBurnRateThreshold {
		zeroBurn := 0.0
		predEnd := round4(util)
		view.BurnRatePerHour = &zeroBurn
		view.ETASeconds = nil
		view.MarginHours = nil
		view.PredictedEndUtil = &predEnd
		view.Confidence = confidence
		if util < 5 {
			view.ForecastStatus = ForecastStatusIdle
		} else {
			view.ForecastStatus = ForecastStatusSafe
		}
		return view, buildSample(view)
	}

	// 步骤 3（分级预测）
	etaHours := (100.0 - util) / vEff
	if etaHours < 0 {
		etaHours = 0
	}
	etaSec := int64(math.Round(etaHours * 3600))
	predEndExact := util + vEff*remHours
	marginHours := etaHours - remHours

	burnRateVal := round4(vEff)
	predEndVal := round4(predEndExact)
	marginVal := round4(marginHours)

	view.BurnRatePerHour = &burnRateVal
	view.ETASeconds = &etaSec
	view.PredictedEndUtil = &predEndVal
	view.MarginHours = &marginVal
	view.Confidence = confidence

	switch {
	case predEndExact >= 105:
		view.ForecastStatus = ForecastStatusWillExhaustHigh
	case predEndExact >= 100:
		view.ForecastStatus = ForecastStatusWillExhaust
	case predEndExact >= 85:
		view.ForecastStatus = ForecastStatusTight
	default:
		view.ForecastStatus = ForecastStatusSafe
	}

	return view, buildSample(view)
}

func computeRecentBurnRate(
	instanceID uint,
	accountID int64,
	windowKey string,
	source string,
	currentResetsAt time.Time,
	currentUpstreamAt time.Time,
	currentUtil float64,
	windowDurationSec int64,
	history []storage.SiteOpsAccountWindowSample,
) (float64, bool) {
	if len(history) == 0 {
		return 0, false
	}

	filtered := make([]storage.SiteOpsAccountWindowSample, 0, len(history))
	for _, h := range history {
		if h.InstanceID != instanceID || h.AccountID != accountID {
			continue
		}
		if strings.TrimSpace(h.WindowKey) != windowKey {
			continue
		}
		hSrc := strings.TrimSpace(h.Source)
		if hSrc == "" {
			hSrc = "passive_cache"
		}
		if hSrc != source {
			continue
		}
		if h.ResetsAt == nil || h.ResetsAt.IsZero() {
			continue
		}
		if absDuration(h.ResetsAt.UTC().Sub(currentResetsAt)) > forecastResetTolerance {
			continue
		}
		hUp := h.UpstreamObservedAt.UTC()
		if hUp.IsZero() || !hUp.Before(currentUpstreamAt) {
			// 严格递增：跳过缓存重放 / 同时间戳 / 未来时间戳样本
			continue
		}
		if math.IsNaN(h.Utilization) || math.IsInf(h.Utilization, 0) || h.Utilization < 0 {
			continue
		}
		filtered = append(filtered, h)
	}
	if len(filtered) == 0 {
		return 0, false
	}

	// 按 upstream_observed_at 升序排序（同时间戳按 observed_at / ID 升序，去重时保留最新一条）
	slices.SortFunc(filtered, func(a, b storage.SiteOpsAccountWindowSample) int {
		at, bt := a.UpstreamObservedAt.UTC(), b.UpstreamObservedAt.UTC()
		if at.Before(bt) {
			return -1
		}
		if at.After(bt) {
			return 1
		}
		ao, bo := a.ObservedAt.UTC(), b.ObservedAt.UTC()
		if ao.Before(bo) {
			return -1
		}
		if ao.After(bo) {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})

	pts := make([]storage.SiteOpsAccountWindowSample, 0, len(filtered))
	for _, item := range filtered {
		if len(pts) > 0 && pts[len(pts)-1].UpstreamObservedAt.UTC().Equal(item.UpstreamObservedAt.UTC()) {
			pts[len(pts)-1] = item
			continue
		}
		pts = append(pts, item)
	}
	if len(pts) == 0 {
		return 0, false
	}

	// 若当前利用率低于最新前序点（发生回落），本轮仅建立新基线，不产出 v_recent
	if currentUtil < pts[len(pts)-1].Utilization {
		return 0, false
	}

	// 从最新前序点向左倒序扫描，遇相邻样本利用率下降即以该回落点为新基线起点，绝不越过回落点拼接更早高点
	monoStart := 0
	for i := len(pts) - 1; i >= 1; i-- {
		if pts[i].Utilization < pts[i-1].Utilization {
			monoStart = i
			break
		}
	}
	validSeg := pts[monoStart:]

	dtMin := minRecentDeltaForWindow(windowDurationSec)
	maxLookback := time.Duration(windowDurationSec) * time.Second

	for j := len(validSeg) - 1; j >= 0; j-- {
		dt := currentUpstreamAt.Sub(validSeg[j].UpstreamObservedAt.UTC())
		if dt < dtMin {
			continue
		}
		if dt > maxLookback {
			break
		}
		deltaU := currentUtil - validSeg[j].Utilization
		if deltaU < 0 {
			deltaU = 0
		}
		return deltaU / dt.Hours(), true
	}

	return 0, false
}

func minElapsedForWindow(windowDurationSec int64) time.Duration {
	switch {
	case windowDurationSec <= 5*3600:
		return 15 * time.Minute
	case windowDurationSec <= 24*3600:
		return 1 * time.Hour
	case windowDurationSec <= 7*24*3600:
		return 3 * time.Hour
	default:
		return 12 * time.Hour
	}
}

func minRecentDeltaForWindow(windowDurationSec int64) time.Duration {
	switch {
	case windowDurationSec <= 5*3600:
		return 10 * time.Minute
	case windowDurationSec <= 24*3600:
		return 30 * time.Minute
	default:
		return 1 * time.Hour
	}
}

// SummarizeAccountForecast 将单账号的多个窗口预测按最早有效阻塞窗口收敛为账号级视图。
func SummarizeAccountForecast(meta SanitizedAccountSummary, windows []WindowForecastView) AccountForecastView {
	support, supportReason := EvaluatePassiveSupport(meta.Platform, meta.Type)
	out := AccountForecastView{
		AccountID:            meta.AccountID,
		Name:                 SanitizeErrorText(meta.Name, 128),
		Platform:             strings.TrimSpace(meta.Platform),
		AccountType:          strings.TrimSpace(meta.Type),
		GroupIDs:             append([]int64(nil), meta.GroupIDs...),
		PassiveSupport:       support,
		PassiveSupportReason: supportReason,
		ExpiresAt:            cloneTimePtr(meta.ExpiresAt),
		OverallStatus:        ForecastStatusInsufficientData,
		OverallConfidence:    ConfidenceLow,
		Windows:              make([]WindowForecastView, 0, len(windows)),
	}
	if len(windows) > 0 {
		out.Windows = append(out.Windows, windows...)
	}

	if support != PassiveSupportProven || len(out.Windows) == 0 {
		return out
	}

	best := out.Windows[0]
	for i := 1; i < len(out.Windows); i++ {
		if isMoreUrgentWindow(out.Windows[i], best) {
			best = out.Windows[i]
		}
	}

	out.OverallStatus = best.ForecastStatus
	out.OverallConfidence = best.Confidence
	out.EarliestETASeconds = cloneInt64Ptr(best.ETASeconds)
	out.EarliestMarginHours = cloneFloatPtr(best.MarginHours)
	if best.ForecastStatus != ForecastStatusInsufficientData {
		out.EarliestBlockingKey = best.WindowKey
	}
	return out
}

func forecastStatusSeverity(s ForecastStatus) int {
	switch s {
	case ForecastStatusExhausted:
		return 6
	case ForecastStatusWillExhaustHigh:
		return 5
	case ForecastStatusWillExhaust:
		return 4
	case ForecastStatusTight:
		return 3
	case ForecastStatusSafe:
		return 2
	case ForecastStatusIdle:
		return 1
	default:
		return 0
	}
}

func windowUrgencyTier(w WindowForecastView) int {
	switch w.ForecastStatus {
	case ForecastStatusExhausted:
		return 3
	case ForecastStatusWillExhaustHigh, ForecastStatusWillExhaust:
		return 2
	default:
		return 1
	}
}

// isMoreUrgentWindow 判断窗口 a 是否比 b 更优先作为账号的最早有效阻塞窗口：
//  1. exhausted 最优先（均 exhausted 时优先剩余解锁时间更长者）；
//  2. 预计将在重置前耗尽的阻塞窗口（will_exhaust_high / will_exhaust）优先于非阻塞窗口，
//     并在阻塞窗口间优先最小非 nil ETASeconds，其次状态严重度；
//  3. 非阻塞窗口间优先状态严重度（tight > safe > idle > insufficient_data），其次最小非 nil ETASeconds。
func isMoreUrgentWindow(a, b WindowForecastView) bool {
	ta, tb := windowUrgencyTier(a), windowUrgencyTier(b)
	if ta != tb {
		return ta > tb
	}
	if ta == 3 {
		if a.RemainingSeconds != b.RemainingSeconds {
			return a.RemainingSeconds > b.RemainingSeconds
		}
		return a.WindowKey < b.WindowKey
	}
	if ta == 2 {
		if cmp, decided := compareOptionalETASeconds(a.ETASeconds, b.ETASeconds); decided {
			return cmp < 0
		}
		sa, sb := forecastStatusSeverity(a.ForecastStatus), forecastStatusSeverity(b.ForecastStatus)
		if sa != sb {
			return sa > sb
		}
		return a.WindowKey < b.WindowKey
	}
	sa, sb := forecastStatusSeverity(a.ForecastStatus), forecastStatusSeverity(b.ForecastStatus)
	if sa != sb {
		return sa > sb
	}
	if cmp, decided := compareOptionalETASeconds(a.ETASeconds, b.ETASeconds); decided {
		return cmp < 0
	}
	return a.WindowKey < b.WindowKey
}

func compareOptionalETASeconds(a, b *int64) (int, bool) {
	switch {
	case a != nil && b == nil:
		return -1, true
	case a == nil && b != nil:
		return 1, true
	case a != nil && b != nil && *a != *b:
		if *a < *b {
			return -1, true
		}
		return 1, true
	default:
		return 0, false
	}
}

func isMediumOrHigherConfidence(c ForecastConfidence) bool {
	return c == ConfidenceHigh || c == ConfidenceMedium
}

// BuildPoolForecast 汇总全站与各分组的被动号池周期预测风险视图。
func BuildPoolForecast(
	instanceID uint,
	observedAt *time.Time,
	trustState FieldQualityState,
	accounts []SanitizedAccountSummary,
	groups []SanitizedGroupInfo,
	windowMap map[int64][]WindowForecastView,
) PoolForecastResponse {
	if trustState == "" {
		trustState = QualityUnavailable
	}
	resp := PoolForecastResponse{
		InstanceID:          instanceID,
		ObservedAt:          cloneTimePtr(observedAt),
		TrustState:          trustState,
		ResetConstraintNote: PoolResetConstraintNote,
		PassiveMatrixNote:   PoolPassiveMatrixNote,
		Groups:              make([]GroupForecastRiskView, 0, len(groups)),
		Accounts:            make([]AccountForecastView, 0, len(accounts)),
	}

	type groupAccum struct {
		view             GroupForecastRiskView
		provenEvaluated  int
		highRiskEval     int
		sustainableEval  int
	}
	groupAccums := make(map[int64]*groupAccum, len(groups))
	for _, g := range groups {
		if g.ID <= 0 {
			continue
		}
		name := SanitizeErrorText(g.Name, 128)
		if name == "" {
			name = fmt.Sprintf("group:%d", g.ID)
		}
		groupAccums[g.ID] = &groupAccum{
			view: GroupForecastRiskView{
				GroupID:   g.ID,
				GroupName: name,
				Platform:  strings.TrimSpace(g.Platform),
			},
		}
	}

	// 按 AccountID 去重处理账号
	seenAccounts := make(map[int64]struct{}, len(accounts))
	for _, acc := range accounts {
		if acc.AccountID <= 0 {
			continue
		}
		if _, dup := seenAccounts[acc.AccountID]; dup {
			continue
		}
		seenAccounts[acc.AccountID] = struct{}{}

		accView := SummarizeAccountForecast(acc, windowMap[acc.AccountID])
		resp.Accounts = append(resp.Accounts, accView)

		switch accView.PassiveSupport {
		case PassiveSupportProven:
			resp.ProvenCount++
			switch accView.OverallStatus {
			case ForecastStatusExhausted:
				resp.ExhaustedCount++
			case ForecastStatusWillExhaustHigh:
				resp.WillExhaustHighCount++
			case ForecastStatusWillExhaust:
				resp.WillExhaustCount++
			case ForecastStatusTight:
				resp.TightCount++
			case ForecastStatusSafe, ForecastStatusIdle:
				resp.SafeOrIdleCount++
			default:
				resp.InsufficientCount++
			}
		case PassiveSupportUnverified:
			resp.UnverifiedCount++
		case PassiveSupportExcludedPayg:
			resp.ExcludedPaygCount++
		}

		// 归并到所属各分组（单账号同组去重）
		seenGroups := make(map[int64]struct{}, len(accView.GroupIDs))
		for _, gid := range accView.GroupIDs {
			if gid <= 0 {
				continue
			}
			if _, dup := seenGroups[gid]; dup {
				continue
			}
			seenGroups[gid] = struct{}{}

			ga, ok := groupAccums[gid]
			if !ok {
				ga = &groupAccum{
					view: GroupForecastRiskView{
						GroupID:   gid,
						GroupName: fmt.Sprintf("group:%d", gid),
						Platform:  accView.Platform,
					},
				}
				groupAccums[gid] = ga
			}

			switch accView.PassiveSupport {
			case PassiveSupportProven:
				ga.view.ProvenAccounts++
				switch accView.OverallStatus {
				case ForecastStatusExhausted:
					ga.view.ExhaustedCount++
				case ForecastStatusWillExhaustHigh:
					ga.view.WillExhaustHighCount++
				case ForecastStatusWillExhaust:
					ga.view.WillExhaustCount++
				case ForecastStatusTight:
					ga.view.TightCount++
				case ForecastStatusSafe, ForecastStatusIdle:
					ga.view.SafeOrIdleCount++
				default:
					ga.view.InsufficientDataCount++
				}

				// 仅基于被动证实且置信度 >= medium 的有效预测账号计算分组断档风险
				if isMediumOrHigherConfidence(accView.OverallConfidence) && accView.OverallStatus != ForecastStatusInsufficientData {
					ga.provenEvaluated++
					switch accView.OverallStatus {
					case ForecastStatusExhausted, ForecastStatusWillExhaustHigh:
						ga.highRiskEval++
					case ForecastStatusSafe, ForecastStatusIdle, ForecastStatusTight:
						ga.sustainableEval++
					}
				}
			case PassiveSupportUnverified:
				ga.view.UnverifiedAccounts++
			case PassiveSupportExcludedPayg:
				ga.view.ExcludedPaygAccounts++
			}
		}
	}

	for _, ga := range groupAccums {
		if ga.provenEvaluated > 0 {
			ratio := math.Round(float64(ga.highRiskEval)/float64(ga.provenEvaluated)*10000) / 100
			ga.view.HighRiskRatioPct = &ratio
			if ratio >= 40 || (ga.sustainableEval <= 1 && ga.highRiskEval > 0) {
				ga.view.CannotSustainRisk = true
				ga.view.RiskNote = fmt.Sprintf(
					"被动证实有效预测中已耗尽/高危占比 %.1f%%（%d/%d），剩余可稳撑账号 %d 个",
					ratio, ga.highRiskEval, ga.provenEvaluated, ga.sustainableEval,
				)
			}
		}
		resp.Groups = append(resp.Groups, ga.view)
	}

	// 稳定排序：高危分组置顶，其次高危占比降序，再按 GroupID 升序
	slices.SortFunc(resp.Groups, func(a, b GroupForecastRiskView) int {
		if a.CannotSustainRisk != b.CannotSustainRisk {
			if a.CannotSustainRisk {
				return -1
			}
			return 1
		}
		ra, rb := -1.0, -1.0
		if a.HighRiskRatioPct != nil {
			ra = *a.HighRiskRatioPct
		}
		if b.HighRiskRatioPct != nil {
			rb = *b.HighRiskRatioPct
		}
		if ra != rb {
			if ra > rb {
				return -1
			}
			return 1
		}
		if a.GroupID < b.GroupID {
			return -1
		}
		if a.GroupID > b.GroupID {
			return 1
		}
		return 0
	})

	// 稳定排序：高危账号置顶，其次最早 ETA 升序，再按 AccountID 升序
	slices.SortFunc(resp.Accounts, func(a, b AccountForecastView) int {
		sa, sb := forecastStatusSeverity(a.OverallStatus), forecastStatusSeverity(b.OverallStatus)
		if sa != sb {
			if sa > sb {
				return -1
			}
			return 1
		}
		if cmp, decided := compareOptionalETASeconds(a.EarliestETASeconds, b.EarliestETASeconds); decided {
			return cmp
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

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func round4(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10000) / 10000
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := t.UTC()
	return &cp
}

func cloneFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func cloneInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

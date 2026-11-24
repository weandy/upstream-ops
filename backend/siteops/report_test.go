package siteops

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestYesterdayWindowUTC8(t *testing.T) {
	// 2026-11-24 09:00:00 UTC+8 == 2026-11-24 01:00:00 UTC
	now := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	startUTC, endUTC, dateLabel := YesterdayWindowUTC8(now)

	if dateLabel != "2026-11-23" {
		t.Fatalf("expected dateLabel 2026-11-23, got %s", dateLabel)
	}
	wantStart := time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC) // 2026-11-23 00:00:00 UTC+8
	wantEnd := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)   // 2026-11-24 00:00:00 UTC+8
	if !startUTC.Equal(wantStart) || !endUTC.Equal(wantEnd) {
		t.Fatalf("window mismatch: got [%v, %v), want [%v, %v)", startUTC, endUTC, wantStart, wantEnd)
	}
}

func TestDailyReportWeightedSLAAndMissing24hLatency(t *testing.T) {
	// 当前时间：2026-11-24 09:00 UTC+8（昨日为 2026-11-23 UTC+8）
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, locUTC8)
	trustedFQ := jsonString(FieldQualityMap{
		DomainReachability:        QualityTrusted,
		DomainOpsOverview:         QualityTrusted,
		DomainInfraMetrics:        QualityTrusted,
		DomainConcurrency:         QualityTrusted,
		DomainAccountAvailability: QualityTrusted,
	})
	unverifiedConcFQ := jsonString(FieldQualityMap{
		DomainReachability: QualityTrusted,
		DomainOpsOverview:  QualityTrusted,
		DomainInfraMetrics: QualityTrusted,
		DomainConcurrency:  QualityUnverifiedZero,
	})

	// 构造不等请求量小时桶：
	// - 昨日 01:55 UTC+8：10 请求，5 错误（1h SLA = 50%）
	// - 昨日 10:55 UTC+8：990 请求，0 错误（1h SLA = 100%）
	// 加权 24h SLA = (1000 - 5) / 1000 * 100 = 99.50%，绝非算术平均 75.00%！
	// 并在 10:15 放一个同小时更早的快照，验证仅取该小时末代表快照而不重复叠加。
	snaps := []storage.SiteOpsSnapshot{
		{
			ObservedAt:           time.Date(2026, 11, 23, 1, 55, 0, 0, locUTC8).UTC(),
			Reachable:            true,
			FieldQualityJSON:     trustedFQ,
			ReqTotal1h:           ptr(int64(10)),
			ErrTotal1h:           ptr(int64(5)),
			SLAPercent1h:         ptr(50.0),
			TTFTP95MS:            ptr(320), // 1h P95 严禁冒充 24h P95
			DurationP95MS:        ptr(1500),
			ConcUsed:             ptr(int64(15)),
			ConcMax:              ptr(int64(50)),
			ConcTrustState:       string(QualityTrusted),
			AccountSwitchCount1m: ptr(int64(4)),
		},
		{
			// 同在 10 点桶但更早，应被 10:55 覆盖
			ObservedAt:           time.Date(2026, 11, 23, 10, 15, 0, 0, locUTC8).UTC(),
			Reachable:            true,
			FieldQualityJSON:     trustedFQ,
			ReqTotal1h:           ptr(int64(500)),
			ErrTotal1h:           ptr(int64(250)),
			SLAPercent1h:         ptr(50.0),
			ConcUsed:             ptr(int64(42)),
			ConcMax:              ptr(int64(50)),
			ConcTrustState:       string(QualityTrusted),
			AccountSwitchCount1m: ptr(int64(9)),
		},
		{
			ObservedAt:           time.Date(2026, 11, 23, 10, 55, 0, 0, locUTC8).UTC(),
			Reachable:            true,
			FieldQualityJSON:     trustedFQ,
			ReqTotal1h:           ptr(int64(990)),
			ErrTotal1h:           ptr(int64(0)),
			SLAPercent1h:         ptr(100.0),
			ConcUsed:             ptr(int64(30)),
			ConcMax:              ptr(int64(50)),
			ConcTrustState:       string(QualityTrusted),
			AccountSwitchCount1m: ptr(int64(6)),
			AccountsTotal:        ptr(int64(20)),
			AccountsError:        ptr(int64(2)),
		},
		{
			// unverified_zero 快照即使 ConcUsed 极大也绝不可计入昨日最高并发水位！
			ObservedAt:           time.Date(2026, 11, 23, 12, 0, 0, 0, locUTC8).UTC(),
			Reachable:            true,
			FieldQualityJSON:     unverifiedConcFQ,
			ConcUsed:             ptr(int64(999)),
			ConcMax:              ptr(int64(1000)),
			ConcTrustState:       string(QualityUnverifiedZero),
			AccountSwitchCount1m: ptr(int64(3)),
		},
		{
			// 窗口外（今日）快照不应计入昨日统计，但可作为最新当前账号数
			ObservedAt:       time.Date(2026, 11, 24, 0, 10, 0, 0, locUTC8).UTC(),
			Reachable:        true,
			FieldQualityJSON: trustedFQ,
			ReqTotal1h:       ptr(int64(1000)),
			ErrTotal1h:       ptr(int64(500)),
			ConcUsed:         ptr(int64(888)),
			ConcMax:          ptr(int64(1000)),
			ConcTrustState:   string(QualityTrusted),
			AccountsTotal:    ptr(int64(25)),
			AccountsError:    ptr(int64(1)),
		},
	}

	// 带敏感样式的告警标题，验证日报默认零 PII / 零密钥
	activeEvents := []storage.SiteOpsEvent{
		{
			ID:          1,
			InstanceID:  1,
			Severity:    SeverityWarning,
			RuleKey:     RuleAccountErrorNew,
			ResourceKey: "account:10",
			State:       EventStateFiring,
			Title:       "账号异常 ops-user@portal.example.com sk-secret1234567890",
		},
		{
			ID:         2,
			InstanceID: 1,
			Severity:   SeverityCritical,
			RuleKey:    RuleSiteOffline,
			State:      EventStateResolved, // 已恢复的不应计入 ActiveFiringAlerts
			Title:      "已恢复事件",
		},
	}

	preview := BuildDailyReport(DailyReportInput{
		Instance:     storage.SiteOpsInstance{ID: 1, Name: "portal-main"},
		Now:          now,
		Snapshots:    snaps,
		ActiveEvents: activeEvents,
	})

	// 1. 加权 24h SLA 必须严格为 99.5%，绝非算术平均 75.0%
	if preview.Metrics.SLAPercent24h == nil {
		t.Fatalf("expected non-nil SLAPercent24h")
	}
	if math.Abs(*preview.Metrics.SLAPercent24h-99.5) > 0.001 {
		t.Fatalf("expected weighted 24h SLA 99.5%%, got %.2f%%", *preview.Metrics.SLAPercent24h)
	}
	if preview.Metrics.ReqTotal24h == nil || *preview.Metrics.ReqTotal24h != 1000 ||
		preview.Metrics.ErrTotal24h == nil || *preview.Metrics.ErrTotal24h != 5 {
		t.Fatalf("unexpected 24h req/err totals: req=%v err=%v", preview.Metrics.ReqTotal24h, preview.Metrics.ErrTotal24h)
	}
	if !strings.Contains(preview.Metrics.SLANote, "加权聚合") {
		t.Fatalf("expected weighted SLA note, got %q", preview.Metrics.SLANote)
	}

	// 2. 未提供上游 24h P95 时，必须保持 nil 并在 note 与正文明确标注「缺失」
	if preview.Metrics.TTFTP95MS24h != nil || preview.Metrics.DurationP95MS24h != nil {
		t.Fatalf("24h P95 must be nil when not provided, got ttft=%v dur=%v", preview.Metrics.TTFTP95MS24h, preview.Metrics.DurationP95MS24h)
	}
	if !strings.Contains(preview.Metrics.Latency24hNote, "缺失") || !strings.Contains(preview.Body, "缺失") {
		t.Fatalf("expected missing 24h latency notice, note=%q body=%q", preview.Metrics.Latency24hNote, preview.Body)
	}

	// 3. 昨日最高并发水位必须为 42/50，忽略 unverified_zero(999) 和今日窗口外(888)
	if preview.Metrics.PeakConcUsed == nil || *preview.Metrics.PeakConcUsed != 42 ||
		preview.Metrics.PeakConcMax == nil || *preview.Metrics.PeakConcMax != 50 {
		t.Fatalf("expected peak conc 42/50 ignoring unverified_zero, got %v/%v", preview.Metrics.PeakConcUsed, preview.Metrics.PeakConcMax)
	}

	// 4. 切号统计：4 + 9 + 6 + 3 = 22，峰值 = 9
	if preview.Metrics.TotalSwitches1m != 22 || preview.Metrics.PeakSwitches1m != 9 {
		t.Fatalf("expected switches total=22 peak=9, got total=%d peak=%d", preview.Metrics.TotalSwitches1m, preview.Metrics.PeakSwitches1m)
	}

	// 5. 当前账号数与未恢复告警数
	if preview.Metrics.CurrentAccountsTotal == nil || *preview.Metrics.CurrentAccountsTotal != 25 ||
		preview.Metrics.CurrentAccountsError == nil || *preview.Metrics.CurrentAccountsError != 1 {
		t.Fatalf("unexpected current accounts: total=%v err=%v", preview.Metrics.CurrentAccountsTotal, preview.Metrics.CurrentAccountsError)
	}
	if preview.Metrics.ActiveFiringAlerts != 1 {
		t.Fatalf("expected 1 active firing alert, got %d", preview.Metrics.ActiveFiringAlerts)
	}

	// 6. 默认日报零 PII / 零密钥泄漏
	if strings.Contains(preview.Body, "ops-user@portal.example.com") || strings.Contains(preview.Body, "sk-secret1234567890") {
		t.Fatalf("daily report must not leak email or secret keys: %s", preview.Body)
	}
}

func TestDailyReportNoTrustedSamples(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, locUTC8)
	staleFQ := jsonString(FieldQualityMap{
		DomainOpsOverview:  QualityStale,
		DomainInfraMetrics: QualityStale,
		DomainConcurrency:  QualityUnverifiedZero,
	})
	snaps := []storage.SiteOpsSnapshot{
		{
			ObservedAt:           time.Date(2026, 11, 23, 8, 0, 0, 0, locUTC8).UTC(),
			Reachable:            false,
			FieldQualityJSON:     staleFQ,
			ReqTotal1h:           ptr(int64(100)),
			ErrTotal1h:           ptr(int64(10)),
			ConcUsed:             ptr(int64(50)),
			ConcTrustState:       string(QualityUnverifiedZero),
			AccountSwitchCount1m: ptr(int64(20)),
		},
	}
	ttft24h := 450
	dur24h := 2100
	preview := BuildDailyReport(DailyReportInput{
		Instance:                 storage.SiteOpsInstance{ID: 2, Name: "portal-backup"},
		Now:                      now,
		Snapshots:                snaps,
		Upstream24hTTFTP95MS:     &ttft24h,
		Upstream24hDurationP95MS: &dur24h,
	})

	if preview.Metrics.SLAPercent24h != nil {
		t.Fatalf("stale samples must produce nil SLAPercent24h, got %v", *preview.Metrics.SLAPercent24h)
	}
	if preview.Metrics.PeakConcUsed != nil {
		t.Fatalf("unverified_zero must produce nil PeakConcUsed, got %v", *preview.Metrics.PeakConcUsed)
	}
	if preview.Metrics.TotalSwitches1m != 0 || preview.Metrics.PeakSwitches1m != 0 {
		t.Fatalf("stale infra_metrics must not accumulate switches")
	}
	if preview.Metrics.TTFTP95MS24h == nil || *preview.Metrics.TTFTP95MS24h != 450 ||
		preview.Metrics.DurationP95MS24h == nil || *preview.Metrics.DurationP95MS24h != 2100 {
		t.Fatalf("provided 24h P95 latency should be preserved: %+v", preview.Metrics)
	}
}

package siteops

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSanitizeErrorTextAndKeyHint(t *testing.T) {
	for _, tc := range []struct{ raw, sensitive string }{
		{"failure Bearer nonprefixed.fixture", "nonprefixed.fixture"},
		{`Bearer "quoted credential"`, "quoted credential"},
		{"invalid sk-example-fixture", "sk-example-fixture"},
		{"invalid adm_sc_example_fixture", "adm_sc_example_fixture"},
		{"invalid adm_example_fixture", "adm_example_fixture"},
		{"invalid admin-example-fixture", "admin-example-fixture"},
		{"dial https://sample:example@portal.example.com/api", "sample:example"},
		{"x-api-key: example; failed", "example"},
		{`{"password": "sample pass"}`, "sample pass"},
		{"Cookie: session=sample; other=example", "other=example"},
		{`Cookie: "session=sample"; other=example`, "other=example"},
		{"refresh_token='example sample'", "example sample"},
		{"Authorization: Basic dGVzdDp0ZXN0", "dGVzdDp0ZXN0"},
		{`Authorization: Digest username="sample", response="example"`, "response="},
	} {
		if got := SanitizeErrorText(tc.raw, 0); strings.Contains(got, tc.sensitive) || !strings.Contains(got, "[redacted]") {
			t.Errorf("sensitive value was not redacted in %q", got)
		}
	}
	if got := SanitizeErrorText("  甲  \n  乙 丙", 3); got != "甲 乙" || !utf8.ValidString(got) {
		t.Fatalf("rune limit = %q", got)
	}
	if got := SanitizeErrorText(strings.Repeat("甲", 201), 0); utf8.RuneCountInString(got) != 200 {
		t.Fatalf("default length = %d", utf8.RuneCountInString(got))
	}
	for key, want := range map[string]string{"adm_sc_abcdef6789": "adm_sc_ab...89", "adm_sc_ab89": "***", "short": "***", "": "***"} {
		if got := MaskAPIKeyHint(key); got != want {
			t.Errorf("hint = %q, want %q", got, want)
		}
	}
}

func TestProjectionFixtures(t *testing.T) {
	overview, err := ParseOpsOverview(fixture(t, "ops_snapshot_v2"))
	if err != nil {
		t.Fatal(err)
	}
	if overview.HealthScore == nil || *overview.HealthScore != 94 ||
		overview.DBConnWaiting == nil || *overview.DBConnWaiting != 0 ||
		overview.SystemMetricsAt == nil || overview.CPUPercent == nil || *overview.CPUPercent != 32.5 ||
		overview.MemPercent == nil || *overview.MemPercent != 58.1 || overview.QueueDepth == nil || *overview.QueueDepth != 0 ||
		overview.SLAPercent1h == nil || *overview.SLAPercent1h != 99.12 ||
		overview.ErrorRatePercent1h == nil || *overview.ErrorRatePercent1h != 0.88 ||
		overview.UpstreamErrorRatePct1h == nil || *overview.UpstreamErrorRatePct1h != 0.62 ||
		overview.TTFTP95MS == nil || *overview.TTFTP95MS != 1480 || len(overview.JobHeartbeats) != 2 {
		t.Fatalf("unexpected overview fields: %+v", overview)
	}
	if okBeat := overview.JobHeartbeats[0]; okBeat.Status != "ok" || okBeat.HasError {
		t.Fatalf("ok heartbeat = %+v", okBeat)
	}
	if beat := overview.JobHeartbeats[1]; beat.Status != "error" || !beat.HasError || beat.ErrorSummary != "dial tcp failed with [redacted]" {
		t.Fatalf("error heartbeat = %+v", beat)
	}
	avail, err := ParseAccountAvailability(fixture(t, "ops_account_availability"))
	if err != nil {
		t.Fatal(err)
	}
	if !avail.Accounts[101].Available || !avail.Accounts[102].HasError ||
		avail.Accounts[102].ErrorSummary != "upstream 401 invalid api key [redacted]" ||
		avail.Groups[10].TotalCount != 1 || avail.Groups[20].TotalCount != 1 || avail.Groups[20].ErrorCount != 1 {
		t.Fatalf("unexpected availability: %+v", avail)
	}
	for _, projected := range []any{overview, avail} {
		body, err := json.Marshal(projected)
		if err != nil || strings.Contains(string(body), "sk-") || strings.Contains(string(body), "error_message") ||
			strings.Contains(string(body), "last_error") || strings.Contains(string(body), "Bearer") {
			t.Fatal("projection contains raw error text or credentials")
		}
	}
	conc, err := ParseConcurrency(fixture(t, "ops_concurrency_zero"))
	if err != nil || conc.RawEmpty || len(conc.Accounts) != 2 || conc.Accounts[101].CurrentInUse != 0 {
		t.Fatalf("zero concurrency was not preserved: %+v, %v", conc, err)
	}
	groups, err := ParseGroupsAll(fixture(t, "groups_all"))
	if err != nil || len(groups) != 3 || groups[0].ID != 10 {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	version, err := ParseSystemVersion(fixture(t, "system_version"))
	if err != nil || version.Version != "v0.2.24" {
		t.Fatalf("version = %+v, %v", version, err)
	}
}

func TestProjectionDirectAndWrapped(t *testing.T) {
	legacy, err := ParseOpsOverview([]byte(`{"health_score":81,"qps":{"current":3},"password":"example"}`))
	if err != nil || legacy.HealthScore == nil || *legacy.HealthScore != 81 || legacy.QPSCurrent == nil || *legacy.QPSCurrent != 3 {
		t.Fatalf("legacy overview = %+v, %v", legacy, err)
	}
	conc, err := ParseConcurrency([]byte(`{"accounts":{"6":{"current_in_use":2,"api_key":"example"}},"groups":{"7":{}}}`))
	if err != nil || conc.RawEmpty || conc.Accounts[6].AccountID != 6 || conc.Groups[7].GroupID != 7 {
		t.Fatalf("concurrency aliases = %+v, %v", conc, err)
	}
	for _, sample := range []string{`{"reason":"ops_disabled"}`, `{"data":{"reason":"OPS_DISABLED"}}`} {
		if yes, reason := DetectOpsDisabled(http.StatusNotFound, []byte(sample)); !yes || reason != "OPS_DISABLED" {
			t.Errorf("disabled 404 = %v, %q", yes, reason)
		}
	}
	for _, sample := range [][]byte{fixture(t, "ops_realtime_disabled_200"), []byte(`{}`)} {
		if yes, _ := DetectOpsDisabled(http.StatusOK, sample); !yes {
			t.Error("empty/disabled realtime payload was not detected")
		}
	}
	if yes, _ := DetectOpsDisabled(http.StatusNotFound, []byte("404 page not found")); yes {
		t.Error("missing route was classified as Ops disabled")
	}
	if _, err := ParseSystemVersion([]byte(`{"code":500,"message":"raw example"}`)); err == nil || strings.Contains(err.Error(), "raw example") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSub2APIV0224SchemaCalibration(t *testing.T) {
	// 1. Ratio (0..1) -> Percentage (0..100) including exact 1.0 -> 100%, plus recovered heartbeat.
	ratioBody := []byte(`{
		"overview": {
			"health_score": 61,
			"sla": 1,
			"error_rate": 0,
			"upstream_error_rate": 0,
			"system_metrics": {
				"created_at": "2026-11-24T04:53:38Z",
				"cpu_usage_percent": 12.4,
				"memory_usage_percent": 44.2,
				"memory_used_mb": 881,
				"concurrency_queue_depth": 3
			},
			"job_heartbeats": [
				{
					"job_name": "ops_metrics_collector",
					"last_run_at": "2026-11-24T04:53:38Z",
					"last_success_at": "2026-11-24T04:53:38Z",
					"last_error_at": "2026-11-24T03:00:00Z",
					"last_error": "transient error before recovery"
				}
			]
		}
	}`)
	ov, err := ParseOpsOverview(ratioBody)
	if err != nil {
		t.Fatal(err)
	}
	if ov.SLAPercent1h == nil || *ov.SLAPercent1h != 100 ||
		ov.ErrorRatePercent1h == nil || *ov.ErrorRatePercent1h != 0 ||
		ov.UpstreamErrorRatePct1h == nil || *ov.UpstreamErrorRatePct1h != 0 ||
		ov.CPUPercent == nil || *ov.CPUPercent != 12.4 ||
		ov.MemPercent == nil || *ov.MemPercent != 44.2 ||
		ov.QueueDepth == nil || *ov.QueueDepth != 3 ||
		ov.SystemMetricsAt == nil {
		t.Fatalf("v0.2.24 ratio/metrics calibration failed: %+v", ov)
	}
	if len(ov.JobHeartbeats) != 1 || ov.JobHeartbeats[0].Status != "ok" || ov.JobHeartbeats[0].HasError {
		t.Fatalf("recovered heartbeat should be ok without error: %+v", ov.JobHeartbeats)
	}

	// 2. Already-percentage payload + legacy alias fields remain supported.
	pctBody := []byte(`{
		"sla": 99.12,
		"error_rate": 0.88,
		"upstream_error_rate": 0.62,
		"system_metrics": {
			"collected_at": "2026-11-24T04:50:00Z",
			"cpu_percent": 20.5,
			"memory_percent": 50.0,
			"queue_depth": 2
		}
	}`)
	ovPct, err := ParseOpsOverview(pctBody)
	if err != nil {
		t.Fatal(err)
	}
	if ovPct.SLAPercent1h == nil || *ovPct.SLAPercent1h != 99.12 ||
		ovPct.ErrorRatePercent1h == nil || *ovPct.ErrorRatePercent1h != 0.88 ||
		ovPct.UpstreamErrorRatePct1h == nil || *ovPct.UpstreamErrorRatePct1h != 0.62 ||
		ovPct.CPUPercent == nil || *ovPct.CPUPercent != 20.5 ||
		ovPct.MemPercent == nil || *ovPct.MemPercent != 50.0 ||
		ovPct.QueueDepth == nil || *ovPct.QueueDepth != 2 ||
		ovPct.SystemMetricsAt == nil {
		t.Fatalf("legacy percentage/alias fallback failed: %+v", ovPct)
	}

	// 3. Group total_accounts (v0.2.24) and legacy total_count alias both work.
	avail, err := ParseAccountAvailability([]byte(`{
		"group": {
			"4": {"group_id": 4, "group_name": "g4", "total_accounts": 4, "available_count": 3, "error_count": 1},
			"5": {"group_id": 5, "group_name": "g5", "total_count": 2, "available_count": 1}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if avail.Groups[4].TotalCount != 4 || avail.Groups[5].TotalCount != 2 {
		t.Fatalf("group totals = %+v", avail.Groups)
	}
}

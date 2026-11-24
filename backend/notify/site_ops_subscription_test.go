package notify

import (
	"testing"
)

func TestSiteOpsSubscriptionDefaultDeny(t *testing.T) {
	denyCases := []struct {
		name       string
		raw        string
		instanceID uint
		eventKey   string
	}{
		{"empty", "", 1, "site_offline"},
		{"whitespace", "   ", 1, "site_offline"},
		{"null", "null", 1, "site_offline"},
		{"empty_object", "{}", 1, "site_offline"},
		{"invalid_json", "{broken", 1, "site_offline"},
		{"json_array", `["1"]`, 1, "site_offline"},
		{"disabled", `{"enabled":false,"instance_ids":["*"],"events":["*"]}`, 1, "site_offline"},
		{"empty_instances", `{"enabled":true,"instance_ids":[],"events":["*"]}`, 1, "site_offline"},
		{"empty_events", `{"enabled":true,"instance_ids":["*"],"events":[]}`, 1, "site_offline"},
		{"zero_instance_id", `{"enabled":true,"instance_ids":["*"],"events":["*"]}`, 0, "site_offline"},
		{"empty_event_key", `{"enabled":true,"instance_ids":["*"],"events":["*"]}`, 1, ""},
		{"instance_mismatch", `{"enabled":true,"instance_ids":["2","3"],"events":["*"]}`, 1, "site_offline"},
		{"event_mismatch", `{"enabled":true,"instance_ids":["1"],"events":["daily_report"]}`, 1, "site_offline"},
	}
	for _, tc := range denyCases {
		t.Run(tc.name, func(t *testing.T) {
			if MatchesSiteOpsChannel(tc.raw, tc.instanceID, tc.eventKey) {
				t.Fatalf("expected false for %s (%s)", tc.name, tc.raw)
			}
		})
	}
}

func TestSiteOpsSubscriptionMatchAndNormalize(t *testing.T) {
	// Support both numeric and string instance_ids and wildcard matching.
	rawNumeric := `{"enabled":true,"instance_ids":[1, 2, "01"],"events":["site_offline","daily_report","site_offline"]}`
	normalized, err := NormalizeSiteOpsSubscription(rawNumeric)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if normalized != `{"enabled":true,"instance_ids":["1","2"],"events":["site_offline","daily_report"]}` {
		t.Fatalf("unexpected normalized payload: %s", normalized)
	}
	if !MatchesSiteOpsChannel(normalized, 1, "site_offline") {
		t.Fatal("expected match for instance 1 and site_offline")
	}
	if !MatchesSiteOpsChannel(normalized, 2, "daily_report") {
		t.Fatal("expected match for instance 2 and daily_report")
	}
	if MatchesSiteOpsChannel(normalized, 3, "site_offline") {
		t.Fatal("unexpected match for unsubscribed instance 3")
	}

	wildcard := `{"enabled":true,"instance_ids":["*"],"events":["*"]}`
	if !MatchesSiteOpsChannel(wildcard, 99, "sla_degraded") {
		t.Fatal("expected wildcard match")
	}

	for _, empty := range []string{"", "   ", "null"} {
		out, err := NormalizeSiteOpsSubscription(empty)
		if err != nil || out != "" {
			t.Fatalf("normalize(%q) = (%q, %v), want (\"\", nil)", empty, out, err)
		}
	}

	for _, bad := range []string{
		"{bad",
		`{"enabled":true,"instance_ids":["abc"],"events":["*"]}`,
		`{"enabled":true,"instance_ids":["0"],"events":["*"]}`,
		`{"enabled":true,"instance_ids":[0],"events":["*"]}`,
		`{"enabled":true,"instance_ids":[-1],"events":["*"]}`,
	} {
		if _, err := NormalizeSiteOpsSubscription(bad); err == nil {
			t.Fatalf("expected error for invalid subscription: %s", bad)
		}
	}
}

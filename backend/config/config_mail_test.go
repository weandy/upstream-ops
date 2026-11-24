package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSiteOpsMailRetentionConfig(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       int
	}{
		{"default", "scheduler: {}", 30},
		{"disabled", "scheduler:\n  retention:\n    siteOpsMailBatchesDays: 0", 0},
		{"custom", "scheduler:\n  retention:\n    siteOpsMailBatchesDays: 7", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Scheduler.Retention.SiteOpsMailBatchesDays; got != tc.want {
				t.Fatalf("mail retention = %d, want %d", got, tc.want)
			}
		})
	}
}

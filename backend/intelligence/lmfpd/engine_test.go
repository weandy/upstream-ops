package lmfpd

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUpstreamConformanceParity(t *testing.T) {
	eng, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded failed: %v", err)
	}

	raw, err := os.ReadFile("testdata/analyze.json")
	if err != nil {
		t.Fatalf("read testdata/analyze.json: %v", err)
	}
	var cases []struct {
		Name    string
		Outputs []struct {
			Text          string
			ExpectedCount int `json:"expected_count"`
		}
		Expected Attribution
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal conformance cases: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			replies := make([]Reply, len(tc.Outputs))
			for i, output := range tc.Outputs {
				replies[i] = Prepare(output.Text, output.ExpectedCount)
			}
			got, want := eng.Analyze(replies), tc.Expected
			if got == nil || got.Decision != want.Decision || got.Prediction != want.Prediction ||
				got.PredictionName != want.PredictionName || got.FamilyPrediction != want.FamilyPrediction ||
				got.Used != want.Used || !slices.Equal(got.Diagnostics, want.Diagnostics) || len(got.Results) != len(want.Results) {
				t.Fatalf("attribution mismatch: got %+v, want %q with diagnostics %v", got, want.Prediction, want.Diagnostics)
			}
			if got.Sequence == nil || got.Sequence.ModelLike != want.Sequence.ModelLike {
				t.Fatalf("sequence = %+v, want %+v", got.Sequence, want.Sequence)
			}
			checkFloat(t, "lag-1 autocorrelation", &got.Sequence.Autocorrelation, &want.Sequence.Autocorrelation)
			checkFloat(t, "small-step share", &got.Sequence.SmallStepShare, &want.Sequence.SmallStepShare)
			checkFloat(t, "top probability", got.Probability, want.Probability)
			for i, cand := range got.Results {
				exp := want.Results[i]
				checkFloat(t, cand.Model+" probability", cand.Probability, exp.Probability)
				checkFloat(t, cand.Model+" score", &cand.Score, &exp.Score)
				cand.Probability, exp.Probability = nil, nil
				cand.Score, exp.Score = 0, 0
				if cand != exp {
					t.Fatalf("rank %d = %+v, want %+v", i+1, cand, exp)
				}
			}
		})
	}
}

func checkFloat(t *testing.T, label string, got, want *float64) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("%s: got ptr=%t, want ptr=%t", label, got != nil, want != nil)
	}
	if got != nil && math.Abs(*got-*want) > 1e-9 {
		t.Fatalf("%s = %.12g, want %.12g", label, *got, *want)
	}
}

func TestReplyPreparation(t *testing.T) {
	if got := parseNumbers("生成 300 个：1, 22, 355, 0, 356, 99。完成 4"); !slices.Equal(got, []int{1, 22, 355, 99}) {
		t.Fatalf("parsed %v", got)
	}
	if Prepare(strings.Repeat("42,", 164), 300).Valid() || !Prepare(strings.Repeat("42,", 165), 300).Valid() {
		t.Fatal("expected minimum 165 valid numbers for expected=300")
	}
}

func TestLoadWithExternalDirFallbackAndOverride(t *testing.T) {
	// 1. 空外挂目录自动回退内置默认库
	emptyDir := t.TempDir()
	eng, source, err := Load(emptyDir)
	if err != nil {
		t.Fatalf("Load empty external dir failed: %v", err)
	}
	if source != SourceEmbedded || eng == nil || len(eng.Challenges()) != ChallengeCount {
		t.Fatalf("expected embedded fallback, got source=%s eng=%v", source, eng)
	}

	// 2. 外挂目录存在完整三件套时优先加载外挂目录
	extDir := t.TempDir()
	for _, name := range []string{"shared_detector.bin", "models.json", "enrollment-suite.json"} {
		raw, err := os.ReadFile(filepath.Join("data", name))
		if err != nil {
			t.Fatalf("read local data %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(extDir, name), raw, 0o644); err != nil {
			t.Fatalf("write ext %s: %v", name, err)
		}
	}
	engExt, sourceExt, err := Load(extDir)
	if err != nil {
		t.Fatalf("Load external dir failed: %v", err)
	}
	if sourceExt != SourceExternal || engExt == nil || engExt.Revision() == "" {
		t.Fatalf("expected external source, got source=%s", sourceExt)
	}
}

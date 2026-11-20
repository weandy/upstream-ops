package embed

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/storage"
)

func openTestEmbedEnv(t *testing.T) (*Service, *intelligence.Service) {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "embed.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cipher, err := crypto.NewCipher("test-embed-secret-0123456789abcdef")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	intel := intelligence.NewService(db, cipher, nil, nil, nil)
	svc := NewService(db, intel)
	return svc, intel
}

func TestSecurityOriginSanitizeAndCSP(t *testing.T) {
	valid, err := SanitizeOriginList([]string{
		"https://ooioo.work/",
		"HTTPS://OOIOO.WORK",
		"https://*.example.com:8443",
		"http://localhost:3000",
	}, true)
	if err != nil {
		t.Fatalf("SanitizeOriginList failed: %v", err)
	}
	if len(valid) != 3 {
		t.Fatalf("expected 3 deduplicated origins, got %v", valid)
	}

	csp := BuildEmbedCSP(valid)
	if !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("missing default-src none: %s", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'self' https://ooioo.work https://*.example.com:8443 http://localhost:3000") {
		t.Fatalf("unexpected frame-ancestors: %s", csp)
	}

	for _, bad := range []string{
		"https://evil.com; script-src 'unsafe-inline'",
		"javascript:alert(1)",
		"https://evil.com/path",
		"https://evil.com\r\nX-Injected: 1",
		"ftp://example.com",
	} {
		if _, err := SanitizeOriginList([]string{bad}, true); err == nil {
			t.Fatalf("expected error for malicious origin %q", bad)
		}
	}

	if got, ok := MatchCORSOrigin("https://ooioo.work", valid); !ok || got != "https://ooioo.work" {
		t.Fatalf("expected CORS match, got (%q, %v)", got, ok)
	}
	if got, ok := MatchCORSOrigin("https://sub.example.com:8443", valid); !ok || got != "https://sub.example.com:8443" {
		t.Fatalf("expected wildcard subdomain CORS match, got (%q, %v)", got, ok)
	}
	if _, ok := MatchCORSOrigin("https://evil-example.com:8443", valid); ok {
		t.Fatalf("unexpected wildcard CORS match for non-subdomain")
	}
	if _, ok := MatchCORSOrigin("https://attacker.com", valid); ok {
		t.Fatalf("unexpected CORS match for attacker origin")
	}
}

func TestSiteCRUDAndResolveFallback(t *testing.T) {
	svc, _ := openTestEmbedEnv(t)

	// 数据库无任何站点时，default 可开箱解析且仅允许 'self'。
	fallback, err := svc.ResolveSite("")
	if err != nil {
		t.Fatalf("resolve empty fallback: %v", err)
	}
	if fallback.Slug != DefaultSiteSlug || len(fallback.AllowedOrigins) != 0 {
		t.Fatalf("unexpected fallback site: %+v", fallback)
	}

	created, err := svc.CreateSite(SiteInput{
		Slug:               "OOIOO_Main",
		Title:              "渠道性能",
		Eyebrow:            "CHANNEL PERFORMANCE",
		Enabled:            true,
		RefreshSeconds:     15,
		AllowedOrigins:     []string{"https://ooioo.work"},
		CORSOrigins:        []string{"https://ooioo.work"},
		Theme:              "paper",
		TargetIDs:          []uint{2, 1, 2},
		ShowIdentityColumn: true,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	if created.Slug != "ooioo-main" {
		t.Fatalf("expected normalized slug ooioo-main, got %q", created.Slug)
	}
	if len(created.TargetIDs) != 2 || created.TargetIDs[0] != 2 || created.TargetIDs[1] != 1 {
		t.Fatalf("expected deduplicated ordered target_ids [2 1], got %v", created.TargetIDs)
	}

	resolved, err := svc.ResolveSite("ooioo-main")
	if err != nil || resolved.ID != created.ID {
		t.Fatalf("resolve created site failed: %v %+v", err, resolved)
	}

	// 禁用后对外必须返回 ErrSiteNotFound。
	if _, err := svc.UpdateSite(created.ID, SiteInput{
		Slug:               created.Slug,
		Title:              created.Title,
		Eyebrow:            created.Eyebrow,
		Enabled:            false,
		RefreshSeconds:     0,
		AllowedOrigins:     created.AllowedOrigins,
		CORSOrigins:        created.CORSOrigins,
		Theme:              "dark",
		TargetIDs:          created.TargetIDs,
		ShowIdentityColumn: false,
	}); err != nil {
		t.Fatalf("disable site: %v", err)
	}
	if _, err := svc.ResolveSite("ooioo-main"); err != ErrSiteNotFound {
		t.Fatalf("expected ErrSiteNotFound for disabled site, got %v", err)
	}
	if _, err := svc.ResolveSite(""); err != ErrSiteNotFound {
		t.Fatalf("expected ErrSiteNotFound when all sites disabled, got %v", err)
	}
}

func TestSnapshotAndArtifactAccessBoundary(t *testing.T) {
	svc, intel := openTestEmbedEnv(t)
	now := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)
	svc.NowFunc = func() time.Time { return now }

	publicTarget, err := intel.CreateTarget(intelligence.TargetInput{
		Name:         "codex-plus",
		Family:       storage.ProbeFamilyCodex,
		SourceKind:   storage.ProbeSourceDirect,
		BaseURL:      "https://secret-upstream.internal",
		APIKey:       "sk-super-secret-value-9999",
		Protocol:     storage.ProbeProtocolOpenAIChat,
		Enabled:      true,
		EmbedVisible: true,
	})
	if err != nil {
		t.Fatalf("create public target: %v", err)
	}
	privateTarget, err := intel.CreateTarget(intelligence.TargetInput{
		Name:         "internal-only",
		Family:       storage.ProbeFamilyClaude,
		SourceKind:   storage.ProbeSourceDirect,
		BaseURL:      "https://private.internal",
		APIKey:       "sk-private-9999",
		Protocol:     storage.ProbeProtocolAnthropic,
		Enabled:      true,
		EmbedVisible: false,
	})
	if err != nil {
		t.Fatalf("create private target: %v", err)
	}

	if _, err := intel.SaveTargetDetectors(publicTarget.ID, []intelligence.DetectorConfigInput{
		{DetectorKey: intelligence.DetectorKeyLiveness, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 180, EmbedVisible: true},
		{DetectorKey: intelligence.DetectorKeyCandy, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 3600, EmbedVisible: true},
		{DetectorKey: intelligence.DetectorKeyLMFPD, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 10800, EmbedVisible: true},
		{DetectorKey: intelligence.DetectorKeyPelican, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 3600, EmbedVisible: true},
	}); err != nil {
		t.Fatalf("save public detectors: %v", err)
	}

	// 写入测活与各插件历史。
	_ = intel.Liveness.Append(&storage.ProbeLivenessSample{
		TargetID:     publicTarget.ID,
		ModelName:    "gpt-6.2-sol",
		Success:      true,
		HTTPStatus:   200,
		FirstTokenMS: 620,
		LatencyMS:    1620,
		OutputTokens: 50,
		TPS:          50.0,
		ObservedAt:   now.Add(-2 * time.Minute),
	})
	_ = intel.Runs.Append(&storage.ProbeIntelligenceRun{
		TargetID:    publicTarget.ID,
		DetectorKey: intelligence.DetectorKeyCandy,
		ModelName:   "gpt-6.2-sol",
		Status:      storage.IntelligenceStatusNormal,
		Summary:     "通过：答案为 21",
		DetailJSON:  `{"answer":"21","passed":true}`,
		ObservedAt:  now.Add(-5 * time.Minute),
	})
	_ = intel.Runs.Append(&storage.ProbeIntelligenceRun{
		TargetID:     publicTarget.ID,
		DetectorKey:  intelligence.DetectorKeyLMFPD,
		ModelName:    "gpt-6.2-sol",
		Status:       storage.IntelligenceStatusNormal,
		Summary:      "最像 gpt-6.2-sol（排第 1）",
		ClaimedRank:  1,
		TopCandidate: "gpt-6.2-sol",
		DetailJSON:   `{"claimed_model":"gpt-6.2-sol","claimed_in_bank":true,"decision":"model","probability":0.92,"top_candidates":[{"id":"gpt-6.2-sol","display_name":"gpt-6.2-sol","probability":0.92}]}`,
		ObservedAt:   now.Add(-4 * time.Minute),
	})
	pubPelican := &storage.ProbeIntelligenceRun{
		TargetID:    publicTarget.ID,
		DetectorKey: intelligence.DetectorKeyPelican,
		ModelName:   "gpt-6.2-sol",
		Status:      storage.IntelligenceStatusNormal,
		Summary:     "已生成画面",
		ArtifactSVG: "<!doctype html><html><body><svg></svg></body></html>",
		ObservedAt:  now.Add(-3 * time.Minute),
	}
	_ = intel.Runs.Append(pubPelican)

	privPelican := &storage.ProbeIntelligenceRun{
		TargetID:    privateTarget.ID,
		DetectorKey: intelligence.DetectorKeyPelican,
		ModelName:   "claude-opus",
		Status:      storage.IntelligenceStatusNormal,
		Summary:     "已生成画面",
		ArtifactSVG: "<!doctype html><html><body><svg>private</svg></body></html>",
		ObservedAt:  now.Add(-1 * time.Minute),
	}
	_ = intel.Runs.Append(privPelican)

	site, err := svc.CreateSite(SiteInput{
		Slug:               "ooioo",
		Title:              "渠道性能",
		Eyebrow:            "CHANNEL PERFORMANCE",
		Enabled:            true,
		RefreshSeconds:     15,
		AllowedOrigins:     []string{"https://ooioo.work"},
		Theme:              "paper",
		ShowIdentityColumn: true,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}

	snap, err := svc.BuildSiteSnapshot(site)
	if err != nil {
		t.Fatalf("BuildSiteSnapshot: %v", err)
	}
	if len(snap.Targets) != 1 || snap.Targets[0].Slug != "codex-plus" {
		t.Fatalf("expected only 1 public target codex-plus, got %+v", snap.Targets)
	}
	row := snap.Targets[0]
	if row.Performance.SuccessRate != 100 || row.Candy == nil || row.Candy.Passed24h != 1 || row.Fingerprint == nil || row.Fingerprint.ClaimedRank != 1 || row.Pelican == nil || row.Pelican.LatestRunUUID != pubPelican.RunUUID {
		t.Fatalf("unexpected row metrics: %+v", row)
	}

	// 公开 Pelican 可访问，私有目标 Pelican 必须拦截防越权。
	if doc, err := svc.ResolvePelicanArtifact(site, pubPelican.RunUUID); err != nil || !strings.Contains(doc, "<svg>") {
		t.Fatalf("expected public pelican artifact, got err=%v", err)
	}
	if _, err := svc.ResolvePelicanArtifact(site, privPelican.RunUUID); err != ErrArtifactNotFound {
		t.Fatalf("expected ErrArtifactNotFound for private target artifact, got %v", err)
	}
}

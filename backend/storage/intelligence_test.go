package storage

import (
	"testing"
	"time"
)

func TestIntelligenceAutoMigrateAndTargetCRUD(t *testing.T) {
	db := openTestDB(t)
	// 重复执行 AutoMigrate 必须幂等。
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate failed: %v", err)
	}

	targets := NewProbeTargets(db)
	detectors := NewProbeDetectorConfigs(db)
	liveness := NewProbeLivenessSamples(db)
	runs := NewProbeIntelligenceRuns(db)

	target := &ProbeTarget{
		Name:         "codex-plus",
		Slug:         "codex-plus",
		Family:       "codex",
		SourceKind:   ProbeSourceDirect,
		BaseURL:      "https://api.example.com",
		APIKeySource: ProbeAPIKeyManual,
		APIKeyCipher: "enc-secret",
		APIKeyHint:   "sk-ab...89",
		Protocol:     ProbeProtocolOpenAIChat,
		AuthStyle:    GatewayProviderAuthBearer,
		SortOrder:    10,
		Enabled:      true,
		EmbedVisible: true,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if target.ID == 0 {
		t.Fatal("expected target ID > 0")
	}

	got, err := targets.FindBySlug("codex-plus")
	if err != nil {
		t.Fatalf("find by slug: %v", err)
	}
	if got.Name != "codex-plus" || got.Family != "codex" || !got.EmbedVisible {
		t.Fatalf("unexpected target: %+v", got)
	}

	second := &ProbeTarget{
		Name:         "cc-max",
		Slug:         "cc-max",
		Family:       "claude",
		SourceKind:   ProbeSourceDirect,
		BaseURL:      "https://claude.example.com",
		APIKeySource: ProbeAPIKeyManual,
		APIKeyCipher: "enc-2",
		Protocol:     ProbeProtocolAnthropic,
		AuthStyle:    GatewayProviderAuthXAPIKey,
		SortOrder:    1,
		Enabled:      true,
		EmbedVisible: false,
	}
	if err := targets.Create(second); err != nil {
		t.Fatalf("create second target: %v", err)
	}

	list, err := targets.List()
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(list) != 2 || list[0].Slug != "cc-max" || list[1].Slug != "codex-plus" {
		t.Fatalf("expected sort_order ASC order, got %+v", list)
	}

	// 写入关联数据，验证删除目标时级联清理。
	if err := detectors.Upsert(&ProbeDetectorConfig{
		TargetID:        target.ID,
		DetectorKey:     "candy",
		Enabled:         true,
		ModelName:       "gpt-6.2-sol",
		IntervalSeconds: 3600,
		TimeoutSeconds:  60,
		AlertEnabled:    true,
		AlertThreshold:  3,
	}); err != nil {
		t.Fatalf("upsert detector config: %v", err)
	}
	if err := liveness.Append(&ProbeLivenessSample{
		TargetID:     target.ID,
		ModelName:    "gpt-6.2-sol",
		Success:      true,
		HTTPStatus:   200,
		ErrorClass:   "ok",
		FirstTokenMS: 1200,
		LatencyMS:    3200,
		OutputTokens: 40,
		TPS:          20.0,
		ObservedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("append liveness: %v", err)
	}
	if err := runs.Append(&ProbeIntelligenceRun{
		RunUUID:     "uuid-del-1",
		TargetID:    target.ID,
		DetectorKey: "candy",
		ModelName:   "gpt-6.2-sol",
		Status:      IntelligenceStatusNormal,
		Summary:     "答案为 21",
		ObservedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("append run: %v", err)
	}

	if err := targets.Delete(target.ID); err != nil {
		t.Fatalf("delete target: %v", err)
	}
	cfgs, err := detectors.ListByTarget(target.ID)
	if err != nil {
		t.Fatalf("list detectors after delete: %v", err)
	}
	if len(cfgs) != 0 {
		t.Fatalf("expected 0 detector configs after target delete, got %d", len(cfgs))
	}
	samples, err := liveness.ListByTargetSince(target.ID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("list samples after delete: %v", err)
	}
	if len(samples) != 0 {
		t.Fatalf("expected 0 liveness samples after target delete, got %d", len(samples))
	}
	runList, err := runs.ListByTargetAndDetector(target.ID, "candy", 10)
	if err != nil {
		t.Fatalf("list runs after delete: %v", err)
	}
	if len(runList) != 0 {
		t.Fatalf("expected 0 runs after target delete, got %d", len(runList))
	}
}

func TestIntelligenceBatchQueriesEquivalentToSingleQueries(t *testing.T) {
	db := openTestDB(t)
	targets := NewProbeTargets(db)
	detectors := NewProbeDetectorConfigs(db)
	liveness := NewProbeLivenessSamples(db)
	runs := NewProbeIntelligenceRuns(db)

	t1 := &ProbeTarget{Name: "batch-1", Slug: "batch-1", SourceKind: ProbeSourceDirect, BaseURL: "https://b1.test", Protocol: ProbeProtocolOpenAIChat, Enabled: true, EmbedVisible: true}
	t2 := &ProbeTarget{Name: "batch-2", Slug: "batch-2", SourceKind: ProbeSourceDirect, BaseURL: "https://b2.test", Protocol: ProbeProtocolOpenAIChat, Enabled: true, EmbedVisible: true}
	if err := targets.Create(t1); err != nil {
		t.Fatalf("create t1: %v", err)
	}
	if err := targets.Create(t2); err != nil {
		t.Fatalf("create t2: %v", err)
	}
	_ = detectors.Upsert(&ProbeDetectorConfig{TargetID: t1.ID, DetectorKey: "liveness", Enabled: true, ModelName: "m1", IntervalSeconds: 60})
	_ = detectors.Upsert(&ProbeDetectorConfig{TargetID: t1.ID, DetectorKey: "candy", Enabled: true, ModelName: "m1", IntervalSeconds: 3600})
	_ = detectors.Upsert(&ProbeDetectorConfig{TargetID: t2.ID, DetectorKey: "pelican", Enabled: true, ModelName: "m2", IntervalSeconds: 3600})

	now := time.Now().UTC()
	_ = liveness.Append(&ProbeLivenessSample{TargetID: t1.ID, ModelName: "m1", Success: true, ObservedAt: now.Add(-2 * time.Minute)})
	_ = liveness.Append(&ProbeLivenessSample{TargetID: t1.ID, ModelName: "m1", Success: false, ObservedAt: now.Add(-1 * time.Minute)})
	_ = liveness.Append(&ProbeLivenessSample{TargetID: t2.ID, ModelName: "m2", Success: true, ObservedAt: now.Add(-3 * time.Minute)})

	_ = runs.Append(&ProbeIntelligenceRun{TargetID: t1.ID, DetectorKey: "candy", ModelName: "m1", Status: IntelligenceStatusSuspected, Summary: "old", ObservedAt: now.Add(-2 * time.Hour)})
	_ = runs.Append(&ProbeIntelligenceRun{TargetID: t1.ID, DetectorKey: "candy", ModelName: "m1", Status: IntelligenceStatusNormal, Summary: "latest-candy", ObservedAt: now.Add(-10 * time.Minute)})
	_ = runs.Append(&ProbeIntelligenceRun{TargetID: t2.ID, DetectorKey: "pelican", ModelName: "m2", Status: IntelligenceStatusNormal, Summary: "art-1", ArtifactSVG: "<svg>huge</svg>", ObservedAt: now.Add(-20 * time.Minute)})
	_ = runs.Append(&ProbeIntelligenceRun{TargetID: t2.ID, DetectorKey: "pelican", ModelName: "m2", Status: IntelligenceStatusError, Summary: "art-err", ObservedAt: now.Add(-5 * time.Minute)})

	ids := []uint{t1.ID, t2.ID}
	cfgMap, err := detectors.ListByTargets(ids)
	if err != nil {
		t.Fatalf("ListByTargets configs: %v", err)
	}
	if len(cfgMap[t1.ID]) != 2 || len(cfgMap[t2.ID]) != 1 {
		t.Fatalf("unexpected batch configs: %+v", cfgMap)
	}

	sampleMap, err := liveness.ListByTargetsSince(ids, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListByTargetsSince liveness: %v", err)
	}
	if len(sampleMap[t1.ID]) != 2 || len(sampleMap[t2.ID]) != 1 {
		t.Fatalf("unexpected batch liveness samples: t1=%d t2=%d", len(sampleMap[t1.ID]), len(sampleMap[t2.ID]))
	}

	candy24h, err := runs.ListByTargetsSince(ids, "candy", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListByTargetsSince candy: %v", err)
	}
	if len(candy24h[t1.ID]) != 2 || len(candy24h[t2.ID]) != 0 {
		t.Fatalf("unexpected batch candy runs: t1=%d t2=%d", len(candy24h[t1.ID]), len(candy24h[t2.ID]))
	}

	latestMap, err := runs.ListLatestByTargets(ids, []string{"candy", "pelican"}, 2)
	if err != nil {
		t.Fatalf("ListLatestByTargets: %v", err)
	}
	if got := latestMap[t1.ID]["candy"]; len(got) != 2 || got[0].Summary != "latest-candy" {
		t.Fatalf("unexpected latest candy runs: %+v", got)
	}
	if got := latestMap[t2.ID]["pelican"]; len(got) != 2 || got[0].Status != IntelligenceStatusError || got[1].ArtifactSVG != "" {
		t.Fatalf("expected pelican runs ordered DESC and ArtifactSVG omitted, got %+v", got)
	}
}

func TestProbeDetectorConfigUpsertAndDueQuery(t *testing.T) {
	db := openTestDB(t)
	targets := NewProbeTargets(db)
	detectors := NewProbeDetectorConfigs(db)

	target := &ProbeTarget{
		Name:       "t1",
		Slug:       "t1",
		SourceKind: ProbeSourceDirect,
		BaseURL:    "https://a.test",
		Protocol:   ProbeProtocolOpenAIChat,
		Enabled:    true,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	past := now.Add(-time.Minute)
	future := now.Add(10 * time.Minute)

	// 首次插入
	if err := detectors.Upsert(&ProbeDetectorConfig{
		TargetID:        target.ID,
		DetectorKey:     "liveness",
		Enabled:         true,
		ModelName:       "gpt-6.2-sol",
		IntervalSeconds: 180,
		TimeoutSeconds:  30,
		NextRunAt:       &past,
	}); err != nil {
		t.Fatalf("upsert liveness: %v", err)
	}
	// 同 (target_id, detector_key) 再次 Upsert 应该原地更新而非报错。
	if err := detectors.Upsert(&ProbeDetectorConfig{
		TargetID:        target.ID,
		DetectorKey:     "liveness",
		Enabled:         true,
		ModelName:       "gpt-6.2-astra",
		IntervalSeconds: 240,
		TimeoutSeconds:  45,
		NextRunAt:       &past,
	}); err != nil {
		t.Fatalf("upsert update liveness: %v", err)
	}
	// 未到期的任务
	if err := detectors.Upsert(&ProbeDetectorConfig{
		TargetID:        target.ID,
		DetectorKey:     "candy",
		Enabled:         true,
		ModelName:       "gpt-6.2-sol",
		IntervalSeconds: 3600,
		TimeoutSeconds:  60,
		NextRunAt:       &future,
	}); err != nil {
		t.Fatalf("upsert candy: %v", err)
	}
	// 未启用的任务（即使 NextRunAt 为空也不应返回）
	if err := detectors.Upsert(&ProbeDetectorConfig{
		TargetID:        target.ID,
		DetectorKey:     "pelican",
		Enabled:         false,
		ModelName:       "gpt-6.2-sol",
		IntervalSeconds: 3600,
		TimeoutSeconds:  120,
	}); err != nil {
		t.Fatalf("upsert pelican: %v", err)
	}

	due, err := detectors.ListDue(now)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 || due[0].DetectorKey != "liveness" || due[0].ModelName != "gpt-6.2-astra" {
		t.Fatalf("unexpected due configs: %+v", due)
	}

	// 推进执行时间后，不再到期。
	nextRun := now.Add(4 * time.Minute)
	if err := detectors.UpdateSchedule(due[0].ID, now, nextRun); err != nil {
		t.Fatalf("update schedule: %v", err)
	}
	dueAfter, err := detectors.ListDue(now)
	if err != nil {
		t.Fatalf("list due after update: %v", err)
	}
	if len(dueAfter) != 0 {
		t.Fatalf("expected 0 due configs after update, got %d", len(dueAfter))
	}
}

func TestProbeLivenessAndIntelligenceRunsQueriesAndRetention(t *testing.T) {
	db := openTestDB(t)
	liveness := NewProbeLivenessSamples(db)
	runs := NewProbeIntelligenceRuns(db)

	now := time.Now().UTC()
	old := now.AddDate(0, 0, -20)

	if err := liveness.Append(&ProbeLivenessSample{
		TargetID:     1,
		ModelName:    "gpt-6.2-sol",
		Success:      true,
		HTTPStatus:   200,
		ErrorClass:   "ok",
		FirstTokenMS: 800,
		LatencyMS:    2800,
		OutputTokens: 100,
		TPS:          50,
		ObservedAt:   old,
	}); err != nil {
		t.Fatalf("append old liveness: %v", err)
	}
	if err := liveness.Append(&ProbeLivenessSample{
		TargetID:     1,
		ModelName:    "gpt-6.2-sol",
		Success:      false,
		HTTPStatus:   502,
		ErrorClass:   "upstream",
		ErrorMessage: "bad gateway",
		LatencyMS:    1500,
		ObservedAt:   now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("append recent liveness: %v", err)
	}

	deleted, err := liveness.DeleteBefore(now.AddDate(0, 0, -14))
	if err != nil {
		t.Fatalf("delete old liveness: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted liveness sample, got %d", deleted)
	}

	// 测试智力检测记录：列表排除 artifact_svg，按 UUID 读取包含 artifact_svg，且支持同源共享查询。
	if err := runs.Append(&ProbeIntelligenceRun{
		RunUUID:        "pelican-uuid-1",
		TargetID:       1,
		DetectorKey:    "pelican",
		ModelName:      "gpt-6.2-sol",
		Status:         IntelligenceStatusNormal,
		Summary:        "已生成画面",
		ArtifactSVG:    "<svg><circle r=\"10\"/></svg>",
		SharedCacheKey: "sha256-same-source",
		ObservedAt:     now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("append pelican run: %v", err)
	}
	if err := runs.Append(&ProbeIntelligenceRun{
		RunUUID:        "pelican-uuid-old",
		TargetID:       1,
		DetectorKey:    "pelican",
		ModelName:      "gpt-6.2-sol",
		Status:         IntelligenceStatusNormal,
		Summary:        "旧记录",
		ArtifactSVG:    "<svg/>",
		SharedCacheKey: "sha256-old",
		ObservedAt:     now.AddDate(0, 0, -100),
	}); err != nil {
		t.Fatalf("append old pelican run: %v", err)
	}

	list, err := runs.ListByTargetAndDetector(1, "pelican", 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(list))
	}
	if list[0].RunUUID != "pelican-uuid-1" {
		t.Fatalf("expected newest first, got %s", list[0].RunUUID)
	}
	if list[0].ArtifactSVG != "" {
		t.Fatalf("expected list query to omit artifact_svg, got %q", list[0].ArtifactSVG)
	}

	full, err := runs.FindByUUID("pelican-uuid-1")
	if err != nil {
		t.Fatalf("find by uuid: %v", err)
	}
	if full.ArtifactSVG == "" {
		t.Fatal("expected FindByUUID to include artifact_svg")
	}

	fresh, err := runs.FindLatestFreshBySharedKey("sha256-same-source", now.Add(-10*time.Minute), 0)
	if err != nil {
		t.Fatalf("find fresh by shared key: %v", err)
	}
	if fresh == nil || fresh.RunUUID != "pelican-uuid-1" {
		t.Fatalf("expected fresh shared run, got %+v", fresh)
	}
	own, err := runs.FindLatestFreshBySharedKey("sha256-same-source", now.Add(-10*time.Minute), 1)
	if err != nil {
		t.Fatalf("find fresh excluding own target: %v", err)
	}
	if own != nil {
		t.Fatalf("expected own target to be excluded from shared cache, got %+v", own)
	}

	deletedRuns, err := runs.DeleteBefore(now.AddDate(0, 0, -90))
	if err != nil {
		t.Fatalf("delete old runs: %v", err)
	}
	if deletedRuns != 1 {
		t.Fatalf("expected 1 deleted run, got %d", deletedRuns)
	}
}

func TestEmbedSitesCRUD(t *testing.T) {
	db := openTestDB(t)
	sites := NewEmbedSites(db)

	site := &EmbedSite{
		Slug:               "portal",
		Title:              "渠道性能",
		Eyebrow:            "CHANNEL PERFORMANCE",
		Enabled:            true,
		RefreshSeconds:     15,
		AllowedOriginsJSON: `["https://portal.example.com"]`,
		Theme:              "paper",
		ShowIdentityColumn: true,
	}
	if err := sites.Create(site); err != nil {
		t.Fatalf("create embed site: %v", err)
	}
	got, err := sites.FindBySlug("portal")
	if err != nil {
		t.Fatalf("find embed site by slug: %v", err)
	}
	if got.Title != "渠道性能" || got.RefreshSeconds != 15 {
		t.Fatalf("unexpected embed site: %+v", got)
	}

	got.Title = "实时性能"
	if err := sites.Update(got); err != nil {
		t.Fatalf("update embed site: %v", err)
	}
	updated, err := sites.FindByID(got.ID)
	if err != nil {
		t.Fatalf("find updated embed site: %v", err)
	}
	if updated.Title != "实时性能" {
		t.Fatalf("expected updated title, got %q", updated.Title)
	}

	all, err := sites.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected 1 embed site, got %d, err=%v", len(all), err)
	}
	if err := sites.Delete(got.ID); err != nil {
		t.Fatalf("delete embed site: %v", err)
	}
}

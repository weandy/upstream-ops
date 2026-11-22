package embed

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/intelligence"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

func setupEmbedBenchmarkFixture(t testing.TB) (*Service, *gin.Engine, *SiteDTO) {
	t.Helper()
	// 复用测试环境初始化逻辑，兼容 *testing.T 和 *testing.B。
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   t.TempDir() + "/embed-cache.db",
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

	intel := intelligence.NewService(db, nil, nil, nil, nil)
	svc := NewService(db, intel)
	off := false
	ttl := 5
	svc.UpdateRuntimeConfig(config.EmbedConfig{
		CacheTTLSeconds:  &ttl,
		RateLimitEnabled: &off,
	})

	now := time.Now().UTC()
	for i := 1; i <= 6; i++ {
		target, err := intel.CreateTarget(intelligence.TargetInput{
			Name:         fmt.Sprintf("channel-%d", i),
			Slug:         fmt.Sprintf("channel-%d", i),
			Family:       storage.ProbeFamilyCodex,
			SourceKind:   storage.ProbeSourceDirect,
			BaseURL:      "https://api.example.com",
			Protocol:     storage.ProbeProtocolOpenAIChat,
			Enabled:      true,
			EmbedVisible: true,
		})
		if err != nil {
			t.Fatalf("create target: %v", err)
		}
		_, _ = intel.SaveTargetDetectors(target.ID, []intelligence.DetectorConfigInput{
			{DetectorKey: intelligence.DetectorKeyLiveness, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 180, EmbedVisible: true},
			{DetectorKey: intelligence.DetectorKeyCandy, Enabled: true, ModelName: "gpt-6.2-sol", IntervalSeconds: 3600, EmbedVisible: true},
		})
		_ = intel.Liveness.Append(&storage.ProbeLivenessSample{
			TargetID:     target.ID,
			ModelName:    "gpt-6.2-sol",
			Success:      true,
			HTTPStatus:   200,
			FirstTokenMS: 450,
			LatencyMS:    1450,
			OutputTokens: 45,
			TPS:          45,
			ObservedAt:   now.Add(-time.Minute),
		})
		_ = intel.Runs.Append(&storage.ProbeIntelligenceRun{
			TargetID:    target.ID,
			DetectorKey: intelligence.DetectorKeyCandy,
			ModelName:   "gpt-6.2-sol",
			Status:      storage.IntelligenceStatusNormal,
			Summary:     "通过：答案为 21",
			ObservedAt:  now.Add(-2 * time.Minute),
		})
	}

	site, err := svc.CreateSite(SiteInput{
		Slug:               "portal",
		Title:              "渠道性能",
		Eyebrow:            "CHANNEL PERFORMANCE",
		Enabled:            true,
		RefreshSeconds:     15,
		AllowedOrigins:     []string{"https://portal.example.com"},
		CORSOrigins:        []string{"https://portal.example.com"},
		Theme:              "paper",
		ShowIdentityColumn: true,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}

	router := gin.New()
	svc.RegisterPublic(router)
	return svc, router, site
}

func TestEmbedMicroCacheSingleflightInvalidationAndCORSIsolation(t *testing.T) {
	svc, router, site := setupEmbedBenchmarkFixture(t)
	now := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	svc.NowFunc = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	// 1. 50 个并发请求同时打 /embed?site=portal，singleflight 必须合并为仅 1 次查库构建。
	const workers = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/embed?site=portal", nil))
			if rec.Code != http.StatusOK {
				errs <- fmt.Errorf("status=%d", rec.Code)
				return
			}
			if !strings.Contains(rec.Body.String(), "theme-paper") {
				errs <- fmt.Errorf("missing theme-paper")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent request failed: %v", err)
	}
	if got := svc.SnapshotBuildCount(); got != 1 {
		t.Fatalf("expected exactly 1 snapshot DB build under 50 concurrent requests, got %d", got)
	}

	// 2. 紧接着查 /api/public/status 共享主快照缓存（不增加查库次数），且多 Origin CORS 头严格隔离！
	reqOK := httptest.NewRequest(http.MethodGet, "/api/public/status?site=portal", nil)
	reqOK.Header.Set("Origin", "https://portal.example.com")
	recOK := httptest.NewRecorder()
	router.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK || recOK.Header().Get("X-Embed-Cache") != cacheStatusHit || recOK.Header().Get("Access-Control-Allow-Origin") != "https://portal.example.com" {
		t.Fatalf("unexpected cached CORS hit: code=%d cache=%q cors=%q", recOK.Code, recOK.Header().Get("X-Embed-Cache"), recOK.Header().Get("Access-Control-Allow-Origin"))
	}

	reqBad := httptest.NewRequest(http.MethodGet, "/api/public/status?site=portal", nil)
	reqBad.Header.Set("Origin", "https://evil.example")
	recBad := httptest.NewRecorder()
	router.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusOK || recBad.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("CORS header leaked across cached responses: %q", recBad.Header().Get("Access-Control-Allow-Origin"))
	}
	if got := svc.SnapshotBuildCount(); got != 1 {
		t.Fatalf("public status should share snapshot cache, got build count %d", got)
	}

	// 3. 管理员更新站点主题为 dark：写穿透主动失效缓存，无需等 5s TTL 立刻生效！
	if _, err := svc.UpdateSite(site.ID, SiteInput{
		Slug:               site.Slug,
		Title:              "暗色性能看板",
		Eyebrow:            site.Eyebrow,
		Enabled:            true,
		RefreshSeconds:     15,
		AllowedOrigins:     site.AllowedOrigins,
		CORSOrigins:        site.CORSOrigins,
		Theme:              "dark",
		ShowIdentityColumn: true,
	}); err != nil {
		t.Fatalf("update site: %v", err)
	}
	recAfterUpdate := httptest.NewRecorder()
	router.ServeHTTP(recAfterUpdate, httptest.NewRequest(http.MethodGet, "/embed?site=portal", nil))
	if recAfterUpdate.Code != http.StatusOK ||
		recAfterUpdate.Header().Get("X-Embed-Cache") != cacheStatusMiss ||
		!strings.Contains(recAfterUpdate.Body.String(), "theme-dark") ||
		!strings.Contains(recAfterUpdate.Body.String(), "暗色性能看板") {
		t.Fatalf("expected immediate cache invalidation after UpdateSite, cache=%q", recAfterUpdate.Header().Get("X-Embed-Cache"))
	}
	if got := svc.SnapshotBuildCount(); got != 2 {
		t.Fatalf("expected build count 2 after invalidation, got %d", got)
	}

	// 4. 推进虚拟时钟超过 5s TTL 后应重新构建一次。
	mu.Lock()
	now = now.Add(6 * time.Second)
	mu.Unlock()
	recExpired := httptest.NewRecorder()
	router.ServeHTTP(recExpired, httptest.NewRequest(http.MethodGet, "/embed?site=portal", nil))
	if recExpired.Code != http.StatusOK || recExpired.Header().Get("X-Embed-Cache") != cacheStatusMiss || svc.SnapshotBuildCount() != 3 {
		t.Fatalf("expected cache expiry rebuild, cache=%q builds=%d", recExpired.Header().Get("X-Embed-Cache"), svc.SnapshotBuildCount())
	}

	// 5. 关闭微缓存（TTL=0）应返回 X-Embed-Cache: BYPASS 并实时查库。
	zero := 0
	off := false
	svc.UpdateRuntimeConfig(config.EmbedConfig{CacheTTLSeconds: &zero, RateLimitEnabled: &off})
	recBypass := httptest.NewRecorder()
	router.ServeHTTP(recBypass, httptest.NewRequest(http.MethodGet, "/embed?site=portal", nil))
	if recBypass.Header().Get("X-Embed-Cache") != cacheStatusBypass || svc.SnapshotBuildCount() != 4 {
		t.Fatalf("expected BYPASS when TTL=0, cache=%q builds=%d", recBypass.Header().Get("X-Embed-Cache"), svc.SnapshotBuildCount())
	}
}

func TestBoundedCacheEvictsWhenFull(t *testing.T) {
	c := newBoundedCache[int](3)
	now := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	c.Set("k1", 1, 5*time.Second, now)
	c.Set("k2", 2, 10*time.Second, now)
	c.Set("k3", 3, 15*time.Second, now)
	// 插入第 4 个时，容量限制 3 必须淘汰最早过期的 k1。
	c.Set("k4", 4, 20*time.Second, now)
	if _, ok := c.Get("k1", now); ok {
		t.Fatalf("expected oldest key k1 to be evicted")
	}
	if v, ok := c.Get("k4", now); !ok || v != 4 {
		t.Fatalf("expected k4=4 present, got (%d, %v)", v, ok)
	}
}

func TestSingleflightGroupRecoversFromPanicWithoutDeadlock(t *testing.T) {
	var sf singleflightGroup[string]
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_, _, _ = sf.Do("k", func() (string, error) {
			panic("embed boom")
		})
	}()
	if !panicked {
		t.Fatal("expected panic to propagate")
	}

	done := make(chan struct{})
	go func() {
		v, shared, err := sf.Do("k", func() (string, error) {
			return "recovered", nil
		})
		if err != nil || shared || v != "recovered" {
			t.Errorf("unexpected follow-up result: v=%q shared=%v err=%v", v, shared, err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deadlock detected after panic in singleflightGroup.Do")
	}
}

func BenchmarkEmbedOverviewCache(b *testing.B) {
	svc, router, _ := setupEmbedBenchmarkFixture(b)
	off := false

	b.Run("NoCache_TTL0", func(b *testing.B) {
		zero := 0
		svc.UpdateRuntimeConfig(config.EmbedConfig{CacheTTLSeconds: &zero, RateLimitEnabled: &off})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/embed?site=portal", nil))
			if rec.Code != http.StatusOK {
				b.Fatalf("status=%d", rec.Code)
			}
		}
	})

	b.Run("MicroCache_TTL5s", func(b *testing.B) {
		five := 5
		svc.UpdateRuntimeConfig(config.EmbedConfig{CacheTTLSeconds: &five, RateLimitEnabled: &off})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/embed?site=portal", nil))
			if rec.Code != http.StatusOK {
				b.Fatalf("status=%d", rec.Code)
			}
		}
	})
}

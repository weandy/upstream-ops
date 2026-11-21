package intelligence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/intelligence/llmclient"
	"github.com/bejix/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func openIntelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "intel.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

type stubChannelKeyAPI struct {
	keys    []connector.APIKey
	secrets map[int64]string
	created int
	updated int
}

func (s *stubChannelKeyAPI) ListAPIKeys(_ context.Context, _ uint, _ connector.APIKeyQuery) (*connector.APIKeyPage, error) {
	return &connector.APIKeyPage{Items: append([]connector.APIKey(nil), s.keys...), Total: int64(len(s.keys))}, nil
}

func (s *stubChannelKeyAPI) CreateAPIKey(_ context.Context, _ uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error) {
	s.created++
	id := int64(100 + s.created)
	k := connector.APIKey{ID: id, Name: req.Name, Group: req.Group, GroupID: req.GroupID}
	s.keys = append(s.keys, k)
	if s.secrets == nil {
		s.secrets = map[int64]string{}
	}
	s.secrets[id] = "sk-generated-secret-9999"
	return &k, nil
}

func (s *stubChannelKeyAPI) UpdateAPIKey(_ context.Context, _ uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error) {
	s.updated++
	for i := range s.keys {
		if s.keys[i].ID == keyID {
			if req.Name != nil {
				s.keys[i].Name = *req.Name
			}
			return &s.keys[i], nil
		}
	}
	return &connector.APIKey{ID: keyID}, nil
}

func (s *stubChannelKeyAPI) RevealAPIKey(_ context.Context, _ uint, keyID int64) (string, error) {
	return s.secrets[keyID], nil
}

func TestServiceTargetCRUDAndKeyPreservation(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, err := crypto.NewCipher("test-secret-key-32-bytes-minimum!!")
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	stubClient := &stubLLMClient{
		resp: &llmclient.Response{
			Content:      "A calm ocean sunrise glows over gentle waves.",
			Complete:     true,
			HTTPStatus:   200,
			FirstTokenMS: 600,
			LatencyMS:    1600,
			OutputTokens: 20,
			TPS:          20,
			ErrorClass:   llmclient.ErrorClassOK,
		},
	}

	svc := NewService(db, cipher, nil, stubClient, nil)
	target, err := svc.CreateTarget(TargetInput{
		Name:         "shengqian-codex",
		Family:       storage.ProbeFamilyCodex,
		SourceKind:   storage.ProbeSourceDirect,
		BaseURL:      "https://api.example.com",
		APIKey:       "sk-abcdef1234567890",
		Protocol:     storage.ProbeProtocolOpenAIChat,
		Enabled:      true,
		EmbedVisible: true,
	})
	if err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	if target.Slug != "shengqian-codex" {
		t.Fatalf("unexpected slug: %q", target.Slug)
	}
	if target.APIKeyHint == "" || strings.Contains(target.APIKeyHint, "123456") {
		t.Fatalf("expected masked api key hint, got %q", target.APIKeyHint)
	}

	// 新建目标默认不开启定时测活。
	cfgs, err := svc.Configs.ListByTarget(target.ID)
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	for _, c := range cfgs {
		if c.Enabled {
			t.Fatalf("expected newly created target detectors disabled by default, got %+v", c)
		}
	}

	// 更新目标时不传 APIKey 应保留旧密文。
	updated, err := svc.UpdateTarget(target.ID, TargetInput{
		Name:         "shengqian-codex-v2",
		Slug:         target.Slug,
		Family:       storage.ProbeFamilyCodex,
		SourceKind:   storage.ProbeSourceDirect,
		BaseURL:      "https://api.example.com",
		Protocol:     storage.ProbeProtocolOpenAIResponses,
		Enabled:      true,
		EmbedVisible: true,
	})
	if err != nil {
		t.Fatalf("UpdateTarget: %v", err)
	}
	if updated.APIKeyCipher == "" || updated.APIKeyCipher != target.APIKeyCipher {
		t.Fatal("expected UpdateTarget with blank APIKey to preserve existing encrypted key")
	}

	// 执行一次手动主动测活并读取 Overview。
	sample, err := svc.RunLivenessNow(context.Background(), target.ID, "gpt-6.2-sol")
	if err != nil {
		t.Fatalf("RunLivenessNow: %v", err)
	}
	if !sample.Success || sample.FirstTokenMS != 600 {
		t.Fatalf("unexpected sample: %+v", sample)
	}

	ov, err := svc.GetTargetOverview(target.ID)
	if err != nil {
		t.Fatalf("GetTargetOverview: %v", err)
	}
	if ov.Performance.SampleCount != 1 || ov.Performance.SuccessRate != 100 || len(ov.Trend) != TrendBucketCount {
		t.Fatalf("unexpected overview: %+v", ov)
	}
}

func TestServiceEnsureChannelAPIKey(t *testing.T) {
	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("test-secret-key-32-bytes-minimum!!")
	chRepo := storage.NewChannels(db)
	ch := &storage.Channel{
		Name:           "my-newapi",
		Type:           storage.ChannelTypeNewAPI,
		SiteURL:        "https://relay.example.com",
		Username:       "admin",
		PasswordCipher: "enc",
		MonitorEnabled: true,
	}
	if err := chRepo.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	keyAPI := &stubChannelKeyAPI{}
	svc := NewService(db, cipher, keyAPI, &stubLLMClient{}, nil)
	target, err := svc.CreateTarget(TargetInput{
		Name:            "relay-default",
		SourceKind:      storage.ProbeSourceChannel,
		ChannelID:       ch.ID,
		SourceGroupName: "codex-plus",
		APIKeySource:    storage.ProbeAPIKeyAutoChannelKey,
		Protocol:        storage.ProbeProtocolOpenAIChat,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("create channel target: %v", err)
	}

	ensured, err := svc.EnsureChannelKey(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("EnsureChannelKey: %v", err)
	}
	if keyAPI.created != 1 || ensured.APIKeyID == 0 || ensured.APIKeyCipher == "" {
		t.Fatalf("expected created key stored encrypted, created=%d target=%+v", keyAPI.created, ensured)
	}
	plain, err := cipher.Decrypt(ensured.APIKeyCipher)
	if err != nil || plain != "sk-generated-secret-9999" {
		t.Fatalf("unexpected decrypted key: %q err=%v", plain, err)
	}

	// 再次调用应复用已有 Key，不重复创建。
	if _, err := svc.EnsureChannelKey(context.Background(), target.ID); err != nil {
		t.Fatalf("second EnsureChannelKey: %v", err)
	}
	if keyAPI.created != 1 || keyAPI.updated != 1 {
		t.Fatalf("expected reuse existing key (created=1 updated=1), got created=%d updated=%d", keyAPI.created, keyAPI.updated)
	}
}

func TestServiceLiveLivenessEndToEnd(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("LIVE_TEST_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("LIVE_TEST_API_KEY"))
	model := strings.TrimSpace(os.Getenv("LIVE_TEST_MODEL"))
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("live test env vars not set")
	}

	db := openIntelTestDB(t)
	cipher, _ := crypto.NewCipher("live-test-secret-key-32-bytes!!!!")
	svc := NewService(db, cipher, nil, llmclient.New(), nil)

	target, err := svc.CreateTarget(TargetInput{
		Name:         "live-shengqian",
		Family:       storage.ProbeFamilyCodex,
		SourceKind:   storage.ProbeSourceDirect,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Protocol:     storage.ProbeProtocolOpenAIChat,
		Enabled:      true,
		EmbedVisible: true,
	})
	if err != nil {
		t.Fatalf("create live target: %v", err)
	}

	models, err := svc.FetchTargetModels(context.Background(), target.ID)
	if err != nil || len(models) == 0 {
		t.Fatalf("FetchTargetModels failed: models=%v err=%v", models, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sample, err := svc.RunLivenessNow(ctx, target.ID, model)
	if err != nil {
		t.Fatalf("RunLivenessNow failed: %v", err)
	}
	if !sample.Success {
		t.Fatalf("expected live liveness success, got %+v", sample)
	}
	ov, err := svc.GetTargetOverview(target.ID)
	if err != nil {
		t.Fatalf("GetTargetOverview failed: %v", err)
	}
	t.Logf("live overview: samples=%d success_rate=%.1f%% ttft=%dms latency=%dms tps=%.1f",
		ov.Performance.SampleCount, ov.Performance.SuccessRate, ov.Performance.AvgFirstTokenMS, ov.Performance.AvgLatencyMS, ov.Performance.TPS)
}

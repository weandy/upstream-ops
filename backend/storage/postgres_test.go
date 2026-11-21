package storage

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type noopConnPool struct{}

func (noopConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) { return nil, nil }
func (noopConnPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil
}
func (noopConnPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}
func (noopConnPool) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func openPostgresDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgresDriver.New(postgresDriver.Config{
		Conn:                 noopConnPool{},
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		DryRun: true,
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open postgres dry-run db: %v", err)
	}
	return db
}

func TestPostgresAndMySQLDSNDefaultsAndEscaping(t *testing.T) {
	// 1. 默认值与特殊字符密码安全转义。
	cfg := DBConfig{
		Driver:   DBDriverPostgres,
		Host:     "pg.internal",
		User:     "ops_user",
		Password: "p@ss:w/ord '123'",
		Name:     "upstreamops",
	}
	dsn := cfg.PostgresDSN()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse postgres dsn %q: %v", dsn, err)
	}
	if parsed.Scheme != "postgres" || parsed.Host != "pg.internal:5432" || parsed.Path != "/upstreamops" {
		t.Fatalf("unexpected postgres uri parts: %+v", parsed)
	}
	if pass, ok := parsed.User.Password(); !ok || pass != "p@ss:w/ord '123'" {
		t.Fatalf("password round-trip mismatch: ok=%v pass=%q", ok, pass)
	}
	if parsed.Query().Get("sslmode") != "disable" || parsed.Query().Get("TimeZone") != "Asia/Shanghai" {
		t.Fatalf("unexpected query params: %s", parsed.RawQuery)
	}

	// 2. 自定义端口、SSLMode 与直传 DSN。
	cfgTLS := DBConfig{
		Driver:   "postgresql",
		Host:     "db.example.com",
		Port:     6432,
		User:     "admin",
		Password: "secret",
		Name:     "prod",
		SSLMode:  "require",
		TimeZone: "UTC",
	}
	if normalizeDriver(cfgTLS.Driver) != DBDriverPostgres {
		t.Fatalf("expected postgresql alias to normalize to postgres")
	}
	if got := cfgTLS.PostgresDSN(); !strings.Contains(got, "db.example.com:6432/prod") || !strings.Contains(got, "sslmode=require") || !strings.Contains(got, "TimeZone=UTC") {
		t.Fatalf("unexpected custom postgres dsn: %q", got)
	}

	rawDSN := "postgres://custom:pw@10.0.0.2:5432/customdb?sslmode=verify-full"
	if got := (DBConfig{Driver: DBDriverPostgres, DSN: rawDSN}).PostgresDSN(); got != rawDSN {
		t.Fatalf("expected raw DSN passthrough, got %q", got)
	}

	// 3. MySQL 默认端口 3306 回退。
	myDSN := (DBConfig{Driver: DBDriverMySQL, User: "u", Password: "p", Name: "db"}).MySQLDSN()
	if !strings.Contains(myDSN, "@tcp(localhost:3306)/db?") {
		t.Fatalf("expected mysql default localhost:3306, got %q", myDSN)
	}
}

func TestPostgresDialectorGeneratesValidPGSQL(t *testing.T) {
	db := openPostgresDryRunDB(t)
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

	// 1. 验证 ProbeDetectorConfig Upsert 生成标准 PostgreSQL ON CONFLICT DO UPDATE。
	cfg := &ProbeDetectorConfig{
		TargetID:        1,
		DetectorKey:     "candy",
		Enabled:         true,
		ModelName:       "gpt-6.2-sol",
		IntervalSeconds: 3600,
	}
	stmt := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "target_id"}, {Name: "detector_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "model_name"}),
	}).Create(cfg).Statement
	upsertSQL := stmt.SQL.String()
	if !strings.Contains(upsertSQL, `ON CONFLICT ("target_id","detector_key") DO UPDATE SET`) {
		t.Fatalf("expected valid postgres ON CONFLICT SQL, got: %s", upsertSQL)
	}
	if strings.Contains(upsertSQL, "`") || strings.Contains(strings.ToUpper(upsertSQL), "INSERT OR REPLACE") {
		t.Fatalf("found non-postgres dialect tokens in upsert SQL: %s", upsertSQL)
	}

	// 2. 验证 GatewayUsageLogs multi_success 与时间区间在 PG 下不含 success = 1 和 SQLite strftime。
	from := now.Add(-time.Hour)
	usageRepo := NewGatewayUsageLogs(db)
	qStmt := usageRepo.db.Model(&GatewayUsageLog{}).
		Where(
			`request_id != '' AND request_id IN (
				SELECT request_id FROM gateway_usage_logs
				WHERE request_id != '' AND request_id IS NOT NULL
				GROUP BY request_id
				HAVING (COUNT(*) > 1 OR MAX(attempt) > 1
					OR SUM(CASE WHEN attempt_kind IN ('retry','failover') THEN 1 ELSE 0 END) > 0)
					AND SUM(CASE WHEN success THEN 1 ELSE 0 END) > 0
			)`,
		).
		Where("created_at >= ?", from).
		Find(&[]GatewayUsageLog{}).Statement
	usageSQL := qStmt.SQL.String()
	if strings.Contains(usageSQL, "success = 1") || strings.Contains(usageSQL, "strftime") {
		t.Fatalf("postgres usage query must not contain sqlite/mysql-only syntax: %s", usageSQL)
	}
	if !strings.Contains(usageSQL, "$1") {
		t.Fatalf("expected postgres positional placeholder $1, got: %s", usageSQL)
	}

	// 3. 验证 ProbeDetectorConfigs.ListDue 联表查询使用 PostgreSQL 占位符。
	dueSQL := db.Model(&ProbeDetectorConfig{}).
		Joins("JOIN probe_targets ON probe_targets.id = probe_detector_configs.target_id").
		Where("probe_targets.enabled = ? AND probe_detector_configs.enabled = ?", true, true).
		Where("probe_detector_configs.next_run_at IS NULL OR probe_detector_configs.next_run_at <= ?", now).
		Find(&[]ProbeDetectorConfig{}).Statement.SQL.String()
	if !strings.Contains(dueSQL, "$1") || !strings.Contains(dueSQL, "$3") {
		t.Fatalf("expected postgres placeholders in ListDue SQL, got: %s", dueSQL)
	}
}

// TestPostgresIntegrationAutoMigrateAndCRUD 可选真机 PostgreSQL 集成测试。
// 设置环境变量 TEST_POSTGRES_DSN（例如 postgres://upstreamops:change-me@127.0.0.1:54329/upstreamops?sslmode=disable）即可执行。
func TestPostgresIntegrationAutoMigrateAndCRUD(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping live PostgreSQL integration test")
	}

	db, err := Open(DBConfig{
		Driver:       DBDriverPostgres,
		DSN:          dsn,
		MaxOpenConns: 5,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open live postgres: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("postgres AutoMigrate failed: %v", err)
	}

	targets := NewProbeTargets(db)
	configs := NewProbeDetectorConfigs(db)
	liveness := NewProbeLivenessSamples(db)
	runs := NewProbeIntelligenceRuns(db)
	sites := NewEmbedSites(db)

	slug := "pg-it-" + time.Now().UTC().Format("20060102150405")
	target := &ProbeTarget{
		Name:         "PG Integration " + slug,
		Slug:         slug,
		Family:       ProbeFamilyCodex,
		SourceKind:   ProbeSourceDirect,
		BaseURL:      "https://example.com",
		Protocol:     ProbeProtocolOpenAIChat,
		Enabled:      true,
		EmbedVisible: true,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("pg create target: %v", err)
	}
	t.Cleanup(func() { _ = targets.Delete(target.ID) })

	now := time.Now().UTC()
	cfg := &ProbeDetectorConfig{
		TargetID:        target.ID,
		DetectorKey:     "candy",
		Enabled:         true,
		ModelName:       "gpt-6.2-sol",
		IntervalSeconds: 3600,
		NextRunAt:       &now,
	}
	if err := configs.Upsert(cfg); err != nil {
		t.Fatalf("pg upsert config 1: %v", err)
	}
	cfg.ModelName = "gpt-6.2-sol-v2"
	if err := configs.Upsert(cfg); err != nil {
		t.Fatalf("pg upsert config 2 (conflict update): %v", err)
	}
	gotCfg, err := configs.Find(target.ID, "candy")
	if err != nil || gotCfg.ModelName != "gpt-6.2-sol-v2" {
		t.Fatalf("pg verify upsert failed: err=%v cfg=%+v", err, gotCfg)
	}

	if err := liveness.Append(&ProbeLivenessSample{
		TargetID:     target.ID,
		ModelName:    "gpt-6.2-sol",
		Success:      true,
		HTTPStatus:   200,
		FirstTokenMS: 300,
		LatencyMS:    900,
		OutputTokens: 30,
		TPS:          50.0,
		ObservedAt:   now,
	}); err != nil {
		t.Fatalf("pg append liveness: %v", err)
	}

	run := &ProbeIntelligenceRun{
		TargetID:    target.ID,
		DetectorKey: "candy",
		ModelName:   "gpt-6.2-sol",
		Status:      IntelligenceStatusNormal,
		Summary:     "通过：答案为 21",
		ObservedAt:  now,
	}
	if err := runs.Append(run); err != nil {
		t.Fatalf("pg append run: %v", err)
	}

	site := &EmbedSite{
		Slug:               slug,
		Title:              "PG Embed",
		Enabled:            true,
		RefreshSeconds:     15,
		Theme:              "paper",
		ShowIdentityColumn: true,
	}
	if err := sites.Create(site); err != nil {
		t.Fatalf("pg create embed site: %v", err)
	}
	t.Cleanup(func() { _ = sites.Delete(site.ID) })
}

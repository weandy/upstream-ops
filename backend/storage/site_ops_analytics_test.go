package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSiteOpsAnalyticsStorage(t *testing.T) {
	s := newTestSiteOpsStore(t)
	db, err := s.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if n := db.Stats().MaxOpenConnections; n != 1 {
		t.Fatalf("MaxOpenConnections = %d", n)
	}
	ctx := context.Background()
	if err := AutoMigrate(s.db); err != nil {
		t.Fatal(err)
	}
	inst := createTestSiteOpsInstance(t, s, "portal", true, 0)
	gen := inst.Generation
	binding := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	row := SiteOpsReferencePrice{InstanceID: inst.ID, Scope: "subscription", ScopeID: 8, TargetBinding: binding, Currency: "CNY", MonthlyMinor: 7200}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	rate := int64(7100000)
	row.CNYPerUSDMicros = &rate
	must(s.PutReferencePrice(ctx, gen, &row))
	initial, err := s.ListReferencePrices(ctx, inst.ID, binding)
	must(err)
	if len(initial) != 1 || initial[0].CNYPerUSDMicros == nil || *initial[0].CNYPerUSDMicros != rate {
		t.Fatal("汇率写入后读取不一致")
	}
	row.CNYPerUSDMicros = nil
	row.MonthlyMinor = 0
	must(s.PutReferencePrice(ctx, gen, &row))
	if !errors.Is(s.PutReferencePrice(ctx, gen+1, &row), ErrSiteOpsGenerationMismatch) {
		t.Fatal("stale upsert")
	}
	if e := s.ClearReferencePrice(ctx, inst.ID, gen+1, binding, "subscription", 8); !errors.Is(e, ErrSiteOpsGenerationMismatch) {
		t.Fatalf("stale clear: %v", e)
	}
	rows, err := s.ListReferencePricesByIDs(ctx, inst.ID, binding, "subscription", []int64{8, 8})
	must(err)
	if len(rows) != 1 || rows[0].MonthlyMinor != 0 || rows[0].CNYPerUSDMicros != nil {
		t.Fatalf("upsert %+v", rows)
	}
	rows, err = s.ListReferencePrices(ctx, inst.ID, other)
	must(err)
	if len(rows) != 0 {
		t.Fatal("cross-target price")
	}
	rows, err = s.ListReferencePrices(ctx, inst.ID, binding)
	must(err)
	if len(rows) != 1 {
		t.Fatal("missing restored price")
	}
	must(s.UpdateAnalyticsConfig(ctx, inst.ID, `{"cny_per_usd":7}`))
	inst.Generation++
	inst.Name = "Portal"
	must(s.UpdateInstance(ctx, &inst))
	current, err := s.GetInstance(ctx, inst.ID)
	must(err)
	if current.Generation != gen+1 || current.AnalyticsConfigJSON != `{"cny_per_usd":7}` {
		t.Fatal("configuration lost")
	}
	gen++
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(time.Hour)
	must(s.ExtendAdminBackoff(ctx, inst.ID, gen, until))
	must(s.ExtendAdminBackoff(ctx, inst.ID, gen, now))
	var clear *time.Time
	patch := SiteOpsRuntimePatch{BackoffUntil: &clear}
	must(s.CommitCollection(ctx, inst.ID, gen, patch, &SiteOpsSnapshot{ObservedAt: now, VerifiedAt: now}, nil))
	must(s.TouchSnapshotVerified(ctx, inst.ID, gen, now.Add(time.Second), patch))
	current, err = s.GetInstance(ctx, inst.ID)
	must(err)
	if current.BackoffUntil == nil || !until.Equal(*current.BackoffUntil) {
		t.Fatal("future backoff overwritten")
	}
	must(s.ClearReferencePrice(ctx, inst.ID, gen, binding, "subscription", 8))
	must(s.ClearReferencePrice(ctx, inst.ID, gen, binding, "subscription", 8))
	must(s.PutReferencePrice(ctx, gen, &row))
	must(s.DeleteInstance(ctx, inst.ID))
	rows, err = s.ListReferencePrices(ctx, inst.ID, binding)
	must(err)
	if len(rows) != 0 {
		t.Fatal("orphan price")
	}
}

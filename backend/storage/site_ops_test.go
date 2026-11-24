package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestSiteOpsStore(t *testing.T) *SiteOpsStore {
	t.Helper()
	db, err := Open(DBConfig{Path: filepath.Join(t.TempDir(), "site-ops.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if sqlDB.Stats().MaxOpenConnections != 1 {
		t.Fatal("expected the SQLite single-connection pool")
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return NewSiteOpsStore(db)
}

func createTestSiteOpsInstance(t *testing.T, s *SiteOpsStore, name string, enabled bool, sort int) SiteOpsInstance {
	t.Helper()
	inst := SiteOpsInstance{Name: name, Slug: name, BaseURL: "https://portal.example.com", Enabled: enabled, SortOrder: sort}
	if err := s.CreateInstance(context.Background(), &inst); err != nil {
		t.Fatal(err)
	}
	return inst
}

func TestSiteOpsCollectionGenerationAndCascade(t *testing.T) {
	s := newTestSiteOpsStore(t)
	ctx := context.Background()
	inst := createTestSiteOpsInstance(t, s, "portal", true, 0)
	oldGeneration := inst.Generation
	inst.Generation++
	if err := s.UpdateInstance(ctx, &inst); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	snapshot := &SiteOpsSnapshot{ObservedAt: at, VerifiedAt: at}
	groups := []SiteOpsGroupSnapshot{{GroupID: 10, ObservedAt: at}}
	status := "online"
	patch := SiteOpsRuntimePatch{LastStatus: &status}
	if err := s.CommitCollection(ctx, inst.ID, oldGeneration, patch, snapshot, groups); !errors.Is(err, ErrSiteOpsGenerationMismatch) {
		t.Fatalf("old generation returned %v", err)
	}
	current, err := s.GetInstance(ctx, inst.ID)
	if err != nil || current.LastStatus == status {
		t.Fatalf("stale runtime patch applied: %+v, %v", current, err)
	}
	if snap, err := s.GetLatestSnapshot(ctx, inst.ID); err != nil || snap != nil {
		t.Fatalf("stale worker created a snapshot: %+v, %v", snap, err)
	}
	if rows, err := s.GetLatestGroupSnapshots(ctx, inst.ID); err != nil || len(rows) != 0 {
		t.Fatalf("stale worker created groups: %+v, %v", rows, err)
	}
	if err := s.CommitCollection(ctx, inst.ID, inst.Generation, patch, snapshot, groups); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitCollection(ctx, inst.ID, inst.Generation, patch, snapshot, groups); !errors.Is(err, ErrSiteOpsNotFound) {
		t.Fatalf("deleted instance returned %v", err)
	}
	if snap, err := s.GetLatestSnapshot(ctx, inst.ID); err != nil || snap != nil {
		t.Fatalf("snapshot survived cascade: %+v, %v", snap, err)
	}
	if rows, err := s.GetLatestGroupSnapshots(ctx, inst.ID); err != nil || len(rows) != 0 {
		t.Fatalf("groups survived cascade: %+v, %v", rows, err)
	}
}

func TestSiteOpsTouchAndLatestQueries(t *testing.T) {
	s := newTestSiteOpsStore(t)
	ctx := context.Background()
	inst := createTestSiteOpsInstance(t, s, "portal", true, 1)
	other := createTestSiteOpsInstance(t, s, "other", false, 0)
	instances, err := s.ListInstances(ctx)
	if err != nil || len(instances) != 2 || instances[0].ID != other.ID || instances[0].Enabled || instances[1].ID != inst.ID {
		t.Fatalf("sorted instances or explicit disabled state: %+v, %v", instances, err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		observed := at.Add(time.Duration(min(i, 1)) * time.Minute)
		snapshot := &SiteOpsSnapshot{ObservedAt: observed, VerifiedAt: observed, ProbeLatencyMS: int64(i), DetailJSON: `{"conc_trust_reason":"synthetic"}`}
		groups := []SiteOpsGroupSnapshot{{GroupID: 10, ObservedAt: observed, AccountsTotal: int64(i)}}
		if i > 0 {
			groups = append(groups, SiteOpsGroupSnapshot{GroupID: 20, ObservedAt: observed})
		}
		patch := SiteOpsRuntimePatch{LastObservedAt: &observed, LastVerifiedAt: &observed}
		if err := s.CommitCollection(ctx, inst.ID, inst.Generation, patch, snapshot, groups); err != nil {
			t.Fatal(err)
		}
	}
	observed, verified := at.Add(time.Minute), at.Add(2*time.Minute)
	if err := s.TouchSnapshotVerified(ctx, inst.ID, inst.Generation-1, verified, SiteOpsRuntimePatch{}); !errors.Is(err, ErrSiteOpsGenerationMismatch) {
		t.Fatalf("stale verification: %v", err)
	}
	if err := s.TouchSnapshotVerified(ctx, inst.ID, inst.Generation, verified, SiteOpsRuntimePatch{LastObservedAt: &verified}); err != nil {
		t.Fatal(err)
	}
	latest, err := s.GetLatestSnapshotsByInstances(ctx, []uint{inst.ID, other.ID})
	if snap := latest[inst.ID]; err != nil || len(latest) != 1 || !snap.ObservedAt.Equal(observed) || !snap.VerifiedAt.Equal(verified) || snap.ProbeLatencyMS != 2 {
		t.Fatalf("latest snapshots: %+v, %v", latest, err)
	}
	current, err := s.GetInstance(ctx, inst.ID)
	if err != nil || current.LastObservedAt == nil || current.LastVerifiedAt == nil || !current.LastObservedAt.Equal(observed) || !current.LastVerifiedAt.Equal(verified) {
		t.Fatalf("unexpected runtime timestamps: %+v, %v", current, err)
	}
	rows, err := s.ListSnapshotsSince(ctx, inst.ID, at, 0)
	if err != nil || len(rows) != 2 || !rows[0].ObservedAt.Equal(at) || !rows[0].VerifiedAt.Equal(at) || rows[1].DetailJSON != "" {
		t.Fatalf("trend projection: %+v, %v", rows, err)
	}
	groups, err := s.GetLatestGroupSnapshots(ctx, inst.ID)
	if err != nil || len(groups) != 2 || groups[0].AccountsTotal != 2 || !groups[1].ObservedAt.Equal(observed) {
		t.Fatalf("latest group archive: %+v, %v", groups, err)
	}
}

func TestSiteOpsConcurrentCollectionAndChunkedRetention(t *testing.T) {
	s := newTestSiteOpsStore(t)
	inst := createTestSiteOpsInstance(t, s, "portal", true, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cutoff := time.Now().UTC().Truncate(time.Second)
	write := func(at time.Time) error {
		snap := &SiteOpsSnapshot{ObservedAt: at, VerifiedAt: at}
		groups := []SiteOpsGroupSnapshot{{GroupID: 10, ObservedAt: at}}
		return s.CommitCollection(ctx, inst.ID, inst.Generation, SiteOpsRuntimePatch{}, snap, groups)
	}
	for i := 1; i <= 50; i++ {
		if err := write(cutoff.Add(-time.Duration(i) * time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	run := func(work func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			work()
		}()
	}
	run(func() {
		for i := range 30 {
			if err := write(cutoff.Add(time.Duration(i) * time.Minute)); err != nil {
				errs <- err
				return
			}
		}
	})
	for _, clear := range []func(context.Context, time.Time, int, int, time.Duration) (int64, error){s.DeleteSnapshotsBeforeChunked, s.DeleteGroupSnapshotsBeforeChunked} {
		run(func() {
			count, err := clear(ctx, cutoff, 3, 17, 4*time.Second)
			if err != nil {
				errs <- err
			} else if count != 17 {
				errs <- errors.New("retention exceeded or did not reach its exact row limit")
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	rows, err := s.ListSnapshotsSince(ctx, inst.ID, cutoff, 0)
	if err != nil || len(rows) != 30 || !rows[0].ObservedAt.Equal(cutoff) {
		t.Fatalf("recent samples affected by retention: %d, %v", len(rows), err)
	}
}

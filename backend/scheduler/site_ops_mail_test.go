package scheduler

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/storage"
)

func TestMailOnlyRetention(t *testing.T) {
	db := openTestDB(t)
	store := storage.NewSiteOpsStore(db)
	inst := storage.SiteOpsInstance{Name: "portal", Slug: "portal", Enabled: true}
	if err := store.CreateInstance(context.Background(), &inst); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -31)
	b := storage.SiteOpsMailBatch{InstanceID: inst.ID, RequestID: "old", Status: "completed", FinishedAt: &old}
	active := storage.SiteOpsMailBatch{InstanceID: inst.ID, RequestID: "active", ActiveSlot: &inst.ID, Status: "running"}
	longLived := []any{&storage.SiteOpsMailScope{InstanceID: inst.ID}, &storage.SiteOpsMailSMTP{InstanceID: inst.ID}, &storage.SiteOpsMailTemplate{InstanceID: inst.ID}, &storage.SiteOpsMailSuppression{InstanceID: inst.ID}}
	for _, row := range append([]any{&b, &active}, longLived...) {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, batch := range []storage.SiteOpsMailBatch{b, active} {
		if err := db.Create(&storage.SiteOpsMailItem{BatchID: batch.ID, InstanceID: inst.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := &Scheduler{cfg: config.SchedulerConfig{}, siteOps: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if s.hasRetention() {
		t.Fatal("unexpected retention with all days zero")
	}
	s.cfg.Retention.SiteOpsMailBatchesDays = 30
	if !s.hasRetention() {
		t.Fatal("mail retention not scheduled")
	}
	s.runRetention()
	for _, row := range append([]any{&storage.SiteOpsMailBatch{}, &storage.SiteOpsMailItem{}}, longLived...) {
		var count int64
		if err := db.Model(row).Where("instance_id = ?", inst.ID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%T count %d, err %v", row, count, err)
		}
	}
	var item storage.SiteOpsMailItem
	if err := db.First(&item).Error; err != nil || item.BatchID != active.ID {
		t.Fatalf("remaining batch %d: %v", item.BatchID, err)
	}
}

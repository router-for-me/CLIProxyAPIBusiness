package modelreference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
)

func TestSyncOnce_FetchesAndStores(t *testing.T) {
	payload := []byte(`{"provider-x":{"name":"Provider X","models":{"model-a":{"name":"Model A","cost":{"input":0.1},"limit":{"context":123,"output":456}}}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if errMigrate := db.AutoMigrate(&models.ModelReference{}); errMigrate != nil {
		t.Fatalf("migrate: %v", errMigrate)
	}

	now := time.Now().UTC().Truncate(time.Second)
	syncer := &Syncer{
		db:       db,
		url:      server.URL,
		interval: time.Minute,
		client:   server.Client(),
		now: func() time.Time {
			return now
		},
	}

	if errSync := syncer.SyncOnce(context.Background()); errSync != nil {
		t.Fatalf("sync once: %v", errSync)
	}

	var row models.ModelReference
	if errFind := db.Where("provider_name = ? AND model_name = ?", "Provider X", "Model A").First(&row).Error; errFind != nil {
		t.Fatalf("find row: %v", errFind)
	}
	if row.ContextLimit != 123 || row.OutputLimit != 456 {
		t.Fatalf("unexpected limits: context=%d output=%d", row.ContextLimit, row.OutputLimit)
	}
	if row.InputPrice == nil || *row.InputPrice != 0.1 {
		t.Fatalf("unexpected input price")
	}
	if !row.LastSeenAt.Equal(now) {
		t.Fatalf("expected last_seen_at to match sync time")
	}
}

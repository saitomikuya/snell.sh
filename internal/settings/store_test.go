package settings

import (
	"context"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
)

func TestLogLimitCanBeUpdated(t *testing.T) {
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db.DB)
	values, err := store.Get(context.Background())
	if err != nil || values.LogMaxMB != DefaultLogMaxMB || !values.LogEnabled {
		t.Fatalf("defaults = %+v, err = %v", values, err)
	}
	values, err = store.SetLogMaxMB(context.Background(), 24)
	if err != nil || values.LogMaxMB != 24 || !values.LogEnabled {
		t.Fatalf("updated = %+v, err = %v", values, err)
	}
	values, err = store.SetLogs(context.Background(), 24, false)
	if err != nil || values.LogEnabled {
		t.Fatalf("logging should be disabled: %+v, err = %v", values, err)
	}
	values, err = store.SetLogMaxMB(context.Background(), 24)
	if err != nil || values.LogEnabled {
		t.Fatalf("changing the size limit must preserve the disabled switch: %+v, err = %v", values, err)
	}
	if _, err = store.SetLogMaxMB(context.Background(), 0); err == nil {
		t.Fatal("expected invalid limit to fail")
	}
}

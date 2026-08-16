package nodes

import (
	"context"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
)

func TestInitializeDefaultsIncludesShadowTLSForSS2022(t *testing.T) {
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secrets, err := secretstore.Open(db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db.DB, secrets)
	if err = store.InitializeDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 4 {
		t.Fatalf("got %d default nodes, want 4", len(list))
	}
	frontend, err := store.Get(context.Background(), "shadowtls-ss-main")
	if err != nil {
		t.Fatal(err)
	}
	if frontend.BackendNodeID != "ss-main" || frontend.ListenPort == 0 {
		t.Fatalf("unexpected ShadowTLS + SS-2022 default: %+v", frontend)
	}
	if err = store.InitializeDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ = store.List(context.Background())
	if len(list) != 4 {
		t.Fatalf("default initialization is not idempotent: %d nodes", len(list))
	}
}

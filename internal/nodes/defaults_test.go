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
	backend, err := store.Get(context.Background(), "ss-main")
	if err != nil {
		t.Fatal(err)
	}
	if backend.ListenHost != "0.0.0.0" {
		t.Fatalf("SS-2022 must stay public for the direct UDP relay, got %s", backend.ListenHost)
	}
	snell, err := store.Get(context.Background(), "snell-main")
	if err != nil {
		t.Fatal(err)
	}
	if snell.ListenHost != "0.0.0.0" {
		t.Fatalf("Snell default must listen on all interfaces, got %s", snell.ListenHost)
	}
	if err = store.InitializeDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ = store.List(context.Background())
	if len(list) != 4 {
		t.Fatalf("default initialization is not idempotent: %d nodes", len(list))
	}
}

func TestInitializeDefaultsMigratesLegacySnellListenAddress(t *testing.T) {
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
	if _, err = store.createWithID(context.Background(), "snell-main", CreateRequest{Type: "snell", Name: "Snell 主节点", RuntimeVersion: "v5.0.1", ListenHost: "127.0.0.1", ListenPort: 6160, Config: Config{Version: "v5"}, Secret: "test-secret"}, "test"); err != nil {
		t.Fatal(err)
	}
	if err = store.InitializeDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	snell, err := store.Get(context.Background(), "snell-main")
	if err != nil {
		t.Fatal(err)
	}
	if snell.ListenHost != "0.0.0.0" {
		t.Fatalf("legacy Snell default was not migrated: %s", snell.ListenHost)
	}
}

func TestCreateDefaultsToPublicListenAddress(t *testing.T) {
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
	node, err := store.Create(context.Background(), CreateRequest{
		Type:           "snell",
		Name:           "新节点",
		RuntimeVersion: "v5.0.1",
		ListenPort:     6161,
		Config:         Config{Version: "v5"},
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if node.ListenHost != DefaultListenHost {
		t.Fatalf("missing listen address defaulted to %q, want %q", node.ListenHost, DefaultListenHost)
	}
}

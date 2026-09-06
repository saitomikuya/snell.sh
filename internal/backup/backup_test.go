package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
	"github.com/proxy-panel/proxy-panel/internal/settings"
	"github.com/proxy-panel/proxy-panel/internal/traffic"
)

func TestCreateAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	secretService, err := secretstore.Open(store.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	nodeStore := nodes.NewStore(store.DB, secretService)
	if err = nodeStore.InitializeDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	service := New(store.DB, dir)
	settingsStore := settings.New(store.DB)
	trafficStore := traffic.New(store.DB)
	if _, err = settingsStore.SetLogMaxMB(ctx, 24); err != nil {
		t.Fatal(err)
	}
	if err = trafficStore.SetProjectQuota(ctx, 1000, 5); err != nil {
		t.Fatal(err)
	}
	entry, err := service.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	node, err := nodeStore.Get(ctx, "snell-main")
	if err != nil {
		t.Fatal(err)
	}
	_, err = nodeStore.Update(ctx, node.ID, nodes.UpdateRequest{Name: "changed", RuntimeVersion: node.RuntimeVersion, ListenHost: node.ListenHost, ListenPort: node.ListenPort, Config: node.Config}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = settingsStore.SetLogMaxMB(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err = trafficStore.SetProjectQuota(ctx, 2000, 8); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Restore(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	node, err = nodeStore.Get(ctx, "snell-main")
	if err != nil {
		t.Fatal(err)
	}
	if node.Name != "Snell 主节点" {
		t.Fatalf("restore did not recover node: %s", node.Name)
	}
	values, err := settingsStore.Get(ctx)
	if err != nil || values.LogMaxMB != 24 {
		t.Fatalf("restore did not recover log settings: %+v, %v", values, err)
	}
	project, err := trafficStore.Project(ctx)
	if err != nil || project.QuotaBytes != 1000 || project.ResetDay != 5 {
		t.Fatalf("restore did not recover project quota: %+v, %v", project, err)
	}
}

func TestPruneKeepsNewestBackups(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store.DB, dir)
	created := make([]Entry, 0, 3)
	for range 3 {
		entry, createErr := service.Create(ctx)
		if createErr != nil {
			t.Fatal(createErr)
		}
		created = append(created, entry)
	}
	if err = service.Prune(ctx, 2); err != nil {
		t.Fatal(err)
	}
	entries, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(entries))
	}
	if _, err = os.Stat(filepath.Join(dir, "backups", created[0].Filename)); !os.IsNotExist(err) {
		t.Fatalf("oldest backup was not removed: %v", err)
	}
}

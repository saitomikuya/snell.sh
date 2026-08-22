package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
)

func TestAppendLogKeepsBoundedArchives(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{}
	path := filepath.Join(dir, "node.log")
	line := strings.Repeat("x", int(maxNodeLogSize))
	for range 4 {
		manager.appendLog(path, line)
	}
	for _, name := range []string{"node.log", "node.log.1", "node.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected retained log %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "node.log.3")); !os.IsNotExist(err) {
		t.Fatalf("unexpected extra log archive: %v", err)
	}
}

func TestManagerWithFakeRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process test")
	}
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
	store := nodes.NewStore(db.DB, secrets)
	node, err := store.Create(context.Background(), nodes.CreateRequest{Type: "snell", Name: "test", RuntimeVersion: "fake", ListenHost: "127.0.0.1", ListenPort: 45678, Config: nodes.Config{Version: "v5"}, Secret: "test-secret"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDesired(context.Background(), node.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "runtime", "snell", "fake", runtime.GOARCH, "snell-server")
	if err = os.MkdirAll(filepath.Dir(binary), 0750); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"
	if err = os.WriteFile(binary, []byte(script), 0750); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(dir, store)
	defer manager.Shutdown()
	if _, err = manager.Apply(node.ID); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config", "snell", node.ID+".conf")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("config mode = %o, want 0640", info.Mode().Perm())
	}
	if _, err = manager.Start(node.ID); err != nil {
		t.Fatal(err)
	}
	if manager.Status().Instances[node.ID].State != "running" {
		t.Fatal("process not running")
	}
	if _, err = manager.Stop(node.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if manager.Status().Instances[node.ID].State == "stopped" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("process did not stop")
}

func TestReconcileDoesNotRestartManagedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process test")
	}
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
	store := nodes.NewStore(db.DB, secrets)
	node, err := store.Create(context.Background(), nodes.CreateRequest{Type: "snell", Name: "test", RuntimeVersion: "fake", ListenHost: "127.0.0.1", ListenPort: 45679, Config: nodes.Config{Version: "v5"}, Secret: "test-secret"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "runtime", "snell", "fake", runtime.GOARCH, "snell-server")
	if err = os.MkdirAll(filepath.Dir(binary), 0750); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"
	if err = os.WriteFile(binary, []byte(script), 0750); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(dir, store)
	defer manager.Shutdown()

	manager.Reconcile()
	first := manager.Status().Instances[node.ID]
	if first.State != "running" || first.PID == 0 {
		t.Fatalf("first reconcile did not start process: %+v", first)
	}
	manager.Reconcile()
	second := manager.Status().Instances[node.ID]
	if second.PID != first.PID {
		t.Fatalf("managed process restarted: first PID %d, second PID %d", first.PID, second.PID)
	}
}

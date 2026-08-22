package database_test

import (
	"context"
	"sync"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
)

func TestConcurrentOpenCanApplyMigrations(t *testing.T) {
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store, err := database.Open(dir)
			if err == nil {
				err = store.Close()
			}
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMaintainBoundsAuditHistory(t *testing.T) {
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for index := 0; index < 550; index++ {
		store.Audit(ctx, "test", "test", "", "local", "{}", true)
	}
	if err = store.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 500 {
		t.Fatalf("expected 500 retained audit rows, got %d", count)
	}
}

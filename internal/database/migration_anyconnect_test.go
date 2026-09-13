package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrationSixPreservesNodesAndAddsAnyConnectTables(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for index := 0; index < 5; index++ {
		if _, err = db.ExecContext(ctx, migrations[index]); err != nil {
			t.Fatalf("apply migration %d: %v", index+1, err)
		}
		if _, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(?,?)`, index+1, Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO nodes(id,type,name,enabled,desired_state,runtime_version,listen_host,listen_port,created_at,updated_at) VALUES('existing','snell','existing',1,'running','v5.0.1','0.0.0.0',6160,?,?)`, Now(), Now()); err != nil {
		t.Fatal(err)
	}
	store := &Store{DB: db}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id='existing'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing node was not preserved: count=%d err=%v", count, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO nodes(id,type,name,enabled,desired_state,runtime_version,listen_host,listen_port,created_at,updated_at) VALUES('vpn','anyconnect','VPN',1,'stopped','v1.5.0','0.0.0.0',443,?,?)`, Now(), Now()); err != nil {
		t.Fatalf("AnyConnect node rejected after migration: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('anyconnect_users','anyconnect_asset_state')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("AnyConnect tables missing: count=%d err=%v", count, err)
	}
}

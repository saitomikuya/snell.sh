package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB      *sql.DB
	DataDir string
}

func Open(dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "db", "panel.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, DataDir: dataDir}
	var migrateErr error
	for attempt := 0; attempt < 6; attempt++ {
		migrateErr = s.Migrate(context.Background())
		if migrateErr == nil || !strings.Contains(strings.ToLower(migrateErr.Error()), "locked") {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	if migrateErr != nil {
		db.Close()
		return nil, migrateErr
	}
	_ = os.Chmod(path, 0600)
	if os.Geteuid() == 0 {
		_ = os.Chown(path, 10001, 10001)
		_ = os.Chmod(path, 0660)
	}
	return s, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	for i, migration := range migrations {
		version := i + 1
		var found int
		err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&found)
		if err != nil {
			return err
		}
		if found > 0 {
			continue
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, migration); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", version, err)
		}
		// The web and runtime-agent processes can start at the same time. All
		// migrations are idempotent, so tolerate the other process recording the
		// same completed version while this transaction was waiting on SQLite.
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.DB.Close() }

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (s *Store) Audit(ctx context.Context, action, targetType, targetID, remoteIP, details string, success bool) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_logs(action,target_type,target_id,remote_ip,details,success,created_at) VALUES(?,?,?,?,?,?,?)`, action, targetType, targetID, remoteIP, details, boolInt(success), Now())
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

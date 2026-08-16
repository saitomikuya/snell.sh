package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/proxy-panel/proxy-panel/internal/database"
)

type Entry struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}
type Service struct {
	db      *sql.DB
	dataDir string
}

func New(db *sql.DB, dataDir string) *Service { return &Service{db: db, dataDir: dataDir} }

func (s *Service) Create(ctx context.Context) (Entry, error) {
	id := uuid.NewString()
	name := "proxy-panel-" + time.Now().UTC().Format("20060102T150405Z") + "-" + id[:8] + ".tar.gz"
	target := filepath.Join(s.dataDir, "backups", name)
	dbSnapshot := filepath.Join(s.dataDir, "backups", "."+id+".db")
	escaped := strings.ReplaceAll(dbSnapshot, "'", "''")
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO '`+escaped+`'`); err != nil {
		return Entry{}, err
	}
	defer os.Remove(dbSnapshot)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Entry{}, err
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	items := []struct{ source, name string }{{dbSnapshot, "db/panel.db"}, {filepath.Join(s.dataDir, "secrets", "master.key"), "secrets/master.key"}, {filepath.Join(s.dataDir, "config"), "config"}, {filepath.Join(s.dataDir, "upstream", "manifests"), "upstream/manifests"}}
	for _, item := range items {
		if err = addPath(tw, item.source, item.name); err != nil {
			tw.Close()
			gz.Close()
			file.Close()
			os.Remove(target)
			return Entry{}, err
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	if err = file.Close(); err != nil {
		return Entry{}, err
	}
	hash, size, err := fileHash(target)
	if err != nil {
		return Entry{}, err
	}
	created := database.Now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO backups(id,filename,sha256,size,created_at) VALUES(?,?,?,?,?)`, id, name, hash, size, created)
	return Entry{ID: id, Filename: name, SHA256: hash, Size: size, CreatedAt: created}, err
}
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,filename,sha256,size,created_at FROM backups ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.Filename, &e.SHA256, &e.Size, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Service) Delete(ctx context.Context, id string) error {
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT filename FROM backups WHERE id=?`, id).Scan(&name); err != nil {
		return err
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid backup name")
	}
	if err := os.Remove(filepath.Join(s.dataDir, "backups", name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE id=?`, id)
	return err
}

func (s *Service) Restore(ctx context.Context, id string) (Entry, error) {
	preBackup, err := s.Create(ctx)
	if err != nil {
		return Entry{}, fmt.Errorf("pre-restore backup: %w", err)
	}
	var name, expectedHash string
	if err = s.db.QueryRowContext(ctx, `SELECT filename,sha256 FROM backups WHERE id=?`, id).Scan(&name, &expectedHash); err != nil {
		return preBackup, err
	}
	if filepath.Base(name) != name {
		return preBackup, errors.New("invalid backup name")
	}
	archivePath := filepath.Join(s.dataDir, "backups", name)
	actualHash, _, err := fileHash(archivePath)
	if err != nil {
		return preBackup, err
	}
	if actualHash != expectedHash {
		return preBackup, errors.New("backup checksum mismatch")
	}
	staging, err := os.MkdirTemp(filepath.Join(s.dataDir, "runtime"), "restore-")
	if err != nil {
		return preBackup, err
	}
	defer os.RemoveAll(staging)
	if err = extractSafe(archivePath, staging); err != nil {
		return preBackup, err
	}
	currentKey, err := os.ReadFile(filepath.Join(s.dataDir, "secrets", "master.key"))
	if err != nil {
		return preBackup, err
	}
	backupKey, err := os.ReadFile(filepath.Join(staging, "secrets", "master.key"))
	if err != nil {
		return preBackup, err
	}
	if !bytes.Equal(currentKey, backupKey) {
		return preBackup, errors.New("backup master key differs; offline restore is required")
	}
	snapshot := filepath.Join(staging, "db", "panel.db")
	if _, err = os.Stat(snapshot); err != nil {
		return preBackup, errors.New("backup database is missing")
	}
	if err = os.MkdirAll(filepath.Join(staging, "config"), 0750); err != nil {
		return preBackup, err
	}
	oldConfig := filepath.Join(s.dataDir, "runtime", "config-before-restore")
	_ = os.RemoveAll(oldConfig)
	if err = os.Rename(filepath.Join(s.dataDir, "config"), oldConfig); err != nil {
		return preBackup, err
	}
	if err = os.Rename(filepath.Join(staging, "config"), filepath.Join(s.dataDir, "config")); err != nil {
		_ = os.Rename(oldConfig, filepath.Join(s.dataDir, "config"))
		return preBackup, err
	}
	if err = restoreDatabase(ctx, s.db, snapshot); err != nil {
		_ = os.RemoveAll(filepath.Join(s.dataDir, "config"))
		_ = os.Rename(oldConfig, filepath.Join(s.dataDir, "config"))
		return preBackup, err
	}
	_ = os.RemoveAll(oldConfig)
	return preBackup, nil
}
func restoreDatabase(ctx context.Context, db *sql.DB, snapshot string) error {
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS restored`, snapshot); err != nil {
		return err
	}
	defer db.ExecContext(context.Background(), `DETACH DATABASE restored`)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer db.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tables := []string{"sessions", "traffic_samples", "node_configs", "config_revisions", "runtime_instances", "traffic_counters", "jobs", "audit_logs", "nodes", "secrets", "auth_state"}
	for _, table := range tables {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	inserts := []string{`INSERT INTO auth_state SELECT * FROM restored.auth_state`, `INSERT INTO secrets SELECT * FROM restored.secrets`, `INSERT INTO nodes SELECT * FROM restored.nodes ORDER BY backend_node_id IS NOT NULL`, `INSERT INTO node_configs SELECT * FROM restored.node_configs`, `INSERT INTO config_revisions SELECT * FROM restored.config_revisions`, `INSERT INTO runtime_instances SELECT * FROM restored.runtime_instances`, `INSERT INTO traffic_counters SELECT * FROM restored.traffic_counters`, `INSERT INTO jobs SELECT * FROM restored.jobs`, `INSERT INTO audit_logs SELECT * FROM restored.audit_logs`}
	for _, query := range inserts {
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}
func extractSafe(archive, target string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(header.Name)
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("unsafe backup path")
		}
		if header.Typeflag != tar.TypeReg {
			return errors.New("backup contains unsupported entry")
		}
		total += header.Size
		if total > 200<<20 {
			return errors.New("backup exceeds extraction limit")
		}
		destination := filepath.Join(target, clean)
		if err = os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
			return err
		}
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(reader, header.Size))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func addPath(tw *tar.Writer, source, name string) error {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = addPath(tw, filepath.Join(source, entry.Name()), filepath.Join(name, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(name)
	header.Mode = 0600
	if err = tw.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(tw, file)
	return err
}
func fileHash(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	return hex.EncodeToString(hash.Sum(nil)), size, err
}

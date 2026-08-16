package traffic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/database"
)

type Counter struct {
	NodeID        string `json:"nodeId"`
	Period        string `json:"period"`
	UploadBytes   int64  `json:"uploadBytes"`
	DownloadBytes int64  `json:"downloadBytes"`
	QuotaBytes    int64  `json:"quotaBytes"`
	ResetDay      int    `json:"resetDay"`
	Paused        bool   `json:"paused"`
	PausedByQuota bool   `json:"pausedByQuota"`
	UpdatedAt     string `json:"updatedAt"`
}

type SampleResult struct {
	NodeID        string
	DeltaUpload   int64
	DeltaDownload int64
	TotalUpload   int64
	TotalDownload int64
	Firewall      string // "pause", "resume", or empty
	Paused        bool
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) List(ctx context.Context) ([]Counter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id,period,upload_bytes,download_bytes,quota_bytes,reset_day,paused,paused_by_quota,updated_at FROM traffic_counters ORDER BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Counter
	for rows.Next() {
		var c Counter
		if err = rows.Scan(&c.NodeID, &c.Period, &c.UploadBytes, &c.DownloadBytes, &c.QuotaBytes, &c.ResetDay, &c.Paused, &c.PausedByQuota, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SetQuota(ctx context.Context, id string, quota int64, resetDay int) error {
	if quota < 0 || resetDay < 1 || resetDay > 28 {
		return errors.New("invalid quota or reset day")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE traffic_counters SET quota_bytes=?,reset_day=?,updated_at=? WHERE node_id=?`, quota, resetDay, database.Now(), id)
	if err != nil {
		return err
	}
	return requireRow(result)
}

func (s *Store) Pause(ctx context.Context, id string, paused bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE traffic_counters SET paused=?,paused_by_quota=0,updated_at=? WHERE node_id=?`, paused, database.Now(), id)
	if err != nil {
		return err
	}
	return requireRow(result)
}

func (s *Store) Reset(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var resetDay int
	if err = tx.QueryRowContext(ctx, `SELECT reset_day FROM traffic_counters WHERE node_id=?`, id).Scan(&resetDay); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE traffic_counters SET period=?,upload_bytes=0,download_bytes=0,paused=0,paused_by_quota=0,last_sample=0,updated_at=? WHERE node_id=?`, BillingPeriod(time.Now().UTC(), resetDay), database.Now(), id)
	if err != nil {
		return err
	}
	if err = requireRow(result); err != nil {
		return err
	}
	// The next sampler establishes the current kernel values as its baseline,
	// so a manual reset never re-adds traffic already counted by nftables.
	if _, err = tx.ExecContext(ctx, `DELETE FROM traffic_samples WHERE node_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Sample persists only the delta from cumulative nftables counters. This keeps
// totals continuous across agent restarts and protects against counter resets.
func (s *Store) Sample(ctx context.Context, id string, rawUpload, rawDownload int64, now time.Time) (SampleResult, error) {
	if rawUpload < 0 || rawDownload < 0 {
		return SampleResult{}, errors.New("raw traffic counters cannot be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SampleResult{}, err
	}
	defer tx.Rollback()

	var counter Counter
	var previousUpload, previousDownload sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT c.node_id,c.period,c.upload_bytes,c.download_bytes,c.quota_bytes,c.reset_day,c.paused,c.paused_by_quota,c.updated_at,s.upload_bytes,s.download_bytes
		FROM traffic_counters c LEFT JOIN traffic_samples s ON s.node_id=c.node_id WHERE c.node_id=?`, id).
		Scan(&counter.NodeID, &counter.Period, &counter.UploadBytes, &counter.DownloadBytes, &counter.QuotaBytes, &counter.ResetDay, &counter.Paused, &counter.PausedByQuota, &counter.UpdatedAt, &previousUpload, &previousDownload)
	if err != nil {
		return SampleResult{}, err
	}

	period := BillingPeriod(now, counter.ResetDay)
	result := SampleResult{NodeID: id}
	if counter.Period != period {
		counter.Period = period
		counter.UploadBytes = 0
		counter.DownloadBytes = 0
		previousUpload.Valid = false
		previousDownload.Valid = false
		if counter.PausedByQuota {
			counter.Paused = false
			counter.PausedByQuota = false
			result.Firewall = "resume"
		}
	}

	if previousUpload.Valid && previousDownload.Valid {
		result.DeltaUpload = rawUpload - previousUpload.Int64
		result.DeltaDownload = rawDownload - previousDownload.Int64
		if result.DeltaUpload < 0 {
			result.DeltaUpload = rawUpload
		}
		if result.DeltaDownload < 0 {
			result.DeltaDownload = rawDownload
		}
		counter.UploadBytes += result.DeltaUpload
		counter.DownloadBytes += result.DeltaDownload
	}

	if counter.QuotaBytes == 0 && counter.PausedByQuota {
		counter.Paused = false
		counter.PausedByQuota = false
		result.Firewall = "resume"
	} else if counter.QuotaBytes > 0 && counter.UploadBytes+counter.DownloadBytes >= counter.QuotaBytes && !counter.Paused {
		counter.Paused = true
		counter.PausedByQuota = true
		result.Firewall = "pause"
	}

	nowText := now.UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO traffic_samples(node_id,upload_bytes,download_bytes,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET upload_bytes=excluded.upload_bytes,download_bytes=excluded.download_bytes,updated_at=excluded.updated_at`, id, rawUpload, rawDownload, nowText); err != nil {
		return SampleResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE traffic_counters SET period=?,upload_bytes=?,download_bytes=?,paused=?,paused_by_quota=?,last_sample=?,updated_at=? WHERE node_id=?`, counter.Period, counter.UploadBytes, counter.DownloadBytes, counter.Paused, counter.PausedByQuota, now.Unix(), nowText, id); err != nil {
		return SampleResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return SampleResult{}, err
	}
	result.TotalUpload = counter.UploadBytes
	result.TotalDownload = counter.DownloadBytes
	result.Paused = counter.Paused
	return result, nil
}

func BillingPeriod(now time.Time, resetDay int) string {
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	now = now.UTC()
	year, month := now.Year(), now.Month()
	if now.Day() < resetDay {
		month--
		if month < time.January {
			month = time.December
			year--
		}
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, resetDay)
}

func requireRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

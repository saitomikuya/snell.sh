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

type ProjectCounter struct {
	Period        string `json:"period"`
	UploadBytes   int64  `json:"uploadBytes"`
	DownloadBytes int64  `json:"downloadBytes"`
	QuotaBytes    int64  `json:"quotaBytes"`
	ResetDay      int    `json:"resetDay"`
	Paused        bool   `json:"paused"`
	PausedByQuota bool   `json:"pausedByQuota"`
	UpdatedAt     string `json:"updatedAt"`
}

// UserCounter tracks an AnyConnect account independently from its node. The
// agent samples ocserv's per-session counters and rolls their deltas into this
// durable monthly record.
type UserCounter struct {
	UserID        string `json:"userId"`
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

type ProjectSampleResult struct {
	DeltaUpload   int64
	DeltaDownload int64
	TotalUpload   int64
	TotalDownload int64
	Firewall      string // "pause", "resume", or empty
	Paused        bool
}

type UserSampleResult struct {
	UserID        string
	DeltaUpload   int64
	DeltaDownload int64
	TotalUpload   int64
	TotalDownload int64
	QuotaBytes    int64
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

func (s *Store) Get(ctx context.Context, id string) (Counter, error) {
	var counter Counter
	err := s.db.QueryRowContext(ctx, `SELECT node_id,period,upload_bytes,download_bytes,quota_bytes,reset_day,paused,paused_by_quota,updated_at FROM traffic_counters WHERE node_id=?`, id).
		Scan(&counter.NodeID, &counter.Period, &counter.UploadBytes, &counter.DownloadBytes, &counter.QuotaBytes, &counter.ResetDay, &counter.Paused, &counter.PausedByQuota, &counter.UpdatedAt)
	return counter, err
}

func (s *Store) Project(ctx context.Context) (ProjectCounter, error) {
	var counter ProjectCounter
	err := s.db.QueryRowContext(ctx, `SELECT period,upload_bytes,download_bytes,quota_bytes,reset_day,paused,paused_by_quota,updated_at FROM project_traffic WHERE id=1`).
		Scan(&counter.Period, &counter.UploadBytes, &counter.DownloadBytes, &counter.QuotaBytes, &counter.ResetDay, &counter.Paused, &counter.PausedByQuota, &counter.UpdatedAt)
	return counter, err
}

func (s *Store) GetUser(ctx context.Context, userID string) (UserCounter, error) {
	var counter UserCounter
	err := s.db.QueryRowContext(ctx, `SELECT user_id,period,upload_bytes,download_bytes,quota_bytes,reset_day,paused,paused_by_quota,updated_at FROM anyconnect_user_traffic WHERE user_id=?`, userID).
		Scan(&counter.UserID, &counter.Period, &counter.UploadBytes, &counter.DownloadBytes, &counter.QuotaBytes, &counter.ResetDay, &counter.Paused, &counter.PausedByQuota, &counter.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		now := database.Now()
		counter = UserCounter{UserID: userID, Period: BillingPeriod(time.Now().UTC(), 1), ResetDay: 1, UpdatedAt: now}
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO anyconnect_user_traffic(user_id,period,reset_day,updated_at) VALUES(?,?,?,?)`, userID, counter.Period, counter.ResetDay, now)
		if err != nil {
			return UserCounter{}, err
		}
		return s.GetUser(ctx, userID)
	}
	return counter, err
}

func (s *Store) SetUserQuota(ctx context.Context, userID string, quota int64, resetDay int) error {
	if quota < 0 || resetDay < 1 || resetDay > 28 {
		return errors.New("invalid user quota or reset day")
	}
	if _, err := s.GetUser(ctx, userID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE anyconnect_user_traffic SET quota_bytes=?,reset_day=?,paused=CASE WHEN ?=0 OR upload_bytes+download_bytes<? THEN 0 ELSE 1 END,paused_by_quota=CASE WHEN ?=0 OR upload_bytes+download_bytes<? THEN 0 ELSE 1 END,updated_at=? WHERE user_id=?`, quota, resetDay, quota, quota, quota, quota, database.Now(), userID)
	if err != nil {
		return err
	}
	return requireRow(result)
}

func (s *Store) ResetUser(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var resetDay int
	if err = tx.QueryRowContext(ctx, `SELECT reset_day FROM anyconnect_user_traffic WHERE user_id=?`, userID).Scan(&resetDay); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE anyconnect_user_traffic SET period=?,upload_bytes=0,download_bytes=0,paused=0,paused_by_quota=0,updated_at=? WHERE user_id=?`, BillingPeriod(time.Now().UTC(), resetDay), database.Now(), userID)
	if err != nil {
		return err
	}
	if err = requireRow(result); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM anyconnect_user_samples WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
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

func (s *Store) SetProjectQuota(ctx context.Context, quota int64, resetDay int) error {
	if quota < 0 || resetDay < 1 || resetDay > 28 {
		return errors.New("invalid quota or reset day")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE project_traffic SET quota_bytes=?,reset_day=?,updated_at=? WHERE id=1`, quota, resetDay, database.Now())
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

func (s *Store) PauseProject(ctx context.Context, paused bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE project_traffic SET paused=?,paused_by_quota=0,updated_at=? WHERE id=1`, paused, database.Now())
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

func (s *Store) ResetProject(ctx context.Context) error {
	var resetDay int
	if err := s.db.QueryRowContext(ctx, `SELECT reset_day FROM project_traffic WHERE id=1`).Scan(&resetDay); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE project_traffic SET period=?,upload_bytes=0,download_bytes=0,paused=0,paused_by_quota=0,updated_at=? WHERE id=1`, BillingPeriod(time.Now().UTC(), resetDay), database.Now())
	if err != nil {
		return err
	}
	return requireRow(result)
}

// SampleProject records the deltas already durably accepted by the per-node
// sampler. Project billing has its own reset day and manual/quota pause state.
func (s *Store) SampleProject(ctx context.Context, upload, download int64, now time.Time) (ProjectSampleResult, error) {
	if upload < 0 || download < 0 {
		return ProjectSampleResult{}, errors.New("project traffic deltas cannot be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectSampleResult{}, err
	}
	defer tx.Rollback()
	var counter ProjectCounter
	if err = tx.QueryRowContext(ctx, `SELECT period,upload_bytes,download_bytes,quota_bytes,reset_day,paused,paused_by_quota,updated_at FROM project_traffic WHERE id=1`).
		Scan(&counter.Period, &counter.UploadBytes, &counter.DownloadBytes, &counter.QuotaBytes, &counter.ResetDay, &counter.Paused, &counter.PausedByQuota, &counter.UpdatedAt); err != nil {
		return ProjectSampleResult{}, err
	}
	result := ProjectSampleResult{DeltaUpload: upload, DeltaDownload: download}
	period := BillingPeriod(now, counter.ResetDay)
	if counter.Period != period {
		counter.Period = period
		counter.UploadBytes = 0
		counter.DownloadBytes = 0
		if counter.PausedByQuota {
			counter.Paused = false
			counter.PausedByQuota = false
			result.Firewall = "resume"
		}
	}
	counter.UploadBytes += upload
	counter.DownloadBytes += download
	if counter.PausedByQuota && (counter.QuotaBytes == 0 || counter.UploadBytes+counter.DownloadBytes < counter.QuotaBytes) {
		counter.Paused = false
		counter.PausedByQuota = false
		result.Firewall = "resume"
	} else if counter.QuotaBytes > 0 && counter.UploadBytes+counter.DownloadBytes >= counter.QuotaBytes && !counter.Paused {
		counter.Paused = true
		counter.PausedByQuota = true
		result.Firewall = "pause"
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE project_traffic SET period=?,upload_bytes=?,download_bytes=?,paused=?,paused_by_quota=?,updated_at=? WHERE id=1`, counter.Period, counter.UploadBytes, counter.DownloadBytes, counter.Paused, counter.PausedByQuota, nowText); err != nil {
		return ProjectSampleResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return ProjectSampleResult{}, err
	}
	result.TotalUpload = counter.UploadBytes
	result.TotalDownload = counter.DownloadBytes
	result.Paused = counter.Paused
	return result, nil
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

	if counter.PausedByQuota && (counter.QuotaBytes == 0 || counter.UploadBytes+counter.DownloadBytes < counter.QuotaBytes) {
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

// SampleUser persists deltas from occtl's cumulative per-session counters.
// A counter drop indicates that ocserv replaced or removed a session; the
// next sample establishes a new baseline without double-counting bytes.
func (s *Store) SampleUser(ctx context.Context, userID string, rawUpload, rawDownload int64, now time.Time) (UserSampleResult, error) {
	if rawUpload < 0 || rawDownload < 0 {
		return UserSampleResult{}, errors.New("raw user traffic counters cannot be negative")
	}
	if _, err := s.GetUser(ctx, userID); err != nil {
		return UserSampleResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserSampleResult{}, err
	}
	defer tx.Rollback()
	var counter UserCounter
	var previousUpload, previousDownload sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT c.user_id,c.period,c.upload_bytes,c.download_bytes,c.quota_bytes,c.reset_day,c.paused,c.paused_by_quota,c.updated_at,s.upload_bytes,s.download_bytes
		FROM anyconnect_user_traffic c LEFT JOIN anyconnect_user_samples s ON s.user_id=c.user_id WHERE c.user_id=?`, userID).
		Scan(&counter.UserID, &counter.Period, &counter.UploadBytes, &counter.DownloadBytes, &counter.QuotaBytes, &counter.ResetDay, &counter.Paused, &counter.PausedByQuota, &counter.UpdatedAt, &previousUpload, &previousDownload)
	if err != nil {
		return UserSampleResult{}, err
	}
	result := UserSampleResult{UserID: userID}
	period := BillingPeriod(now, counter.ResetDay)
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
		if rawUpload >= previousUpload.Int64 {
			result.DeltaUpload = rawUpload - previousUpload.Int64
		}
		if rawDownload >= previousDownload.Int64 {
			result.DeltaDownload = rawDownload - previousDownload.Int64
		}
		counter.UploadBytes += result.DeltaUpload
		counter.DownloadBytes += result.DeltaDownload
	}
	if counter.PausedByQuota && (counter.QuotaBytes == 0 || counter.UploadBytes+counter.DownloadBytes < counter.QuotaBytes) {
		counter.Paused = false
		counter.PausedByQuota = false
		result.Firewall = "resume"
	} else if counter.QuotaBytes > 0 && counter.UploadBytes+counter.DownloadBytes >= counter.QuotaBytes && !counter.Paused {
		counter.Paused = true
		counter.PausedByQuota = true
		result.Firewall = "pause"
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO anyconnect_user_samples(user_id,upload_bytes,download_bytes,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET upload_bytes=excluded.upload_bytes,download_bytes=excluded.download_bytes,updated_at=excluded.updated_at`, userID, rawUpload, rawDownload, nowText); err != nil {
		return UserSampleResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE anyconnect_user_traffic SET period=?,upload_bytes=?,download_bytes=?,paused=?,paused_by_quota=?,updated_at=? WHERE user_id=?`, counter.Period, counter.UploadBytes, counter.DownloadBytes, counter.Paused, counter.PausedByQuota, nowText, userID); err != nil {
		return UserSampleResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return UserSampleResult{}, err
	}
	result.TotalUpload = counter.UploadBytes
	result.TotalDownload = counter.DownloadBytes
	result.QuotaBytes = counter.QuotaBytes
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

package settings

import (
	"context"
	"database/sql"
	"errors"

	"github.com/proxy-panel/proxy-panel/internal/database"
)

const (
	DefaultLogMaxMB = 10
	MinLogMaxMB     = 1
	MaxLogMaxMB     = 1024
)

type Values struct {
	LogMaxMB   int    `json:"logMaxMB"`
	LogEnabled bool   `json:"logEnabled"`
	UpdatedAt  string `json:"updatedAt"`
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Get(ctx context.Context) (Values, error) {
	var values Values
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT log_max_mb,log_enabled,updated_at FROM system_settings WHERE id=1`).Scan(&values.LogMaxMB, &enabled, &values.UpdatedAt)
	values.LogEnabled = enabled != 0
	return values, err
}

func (s *Store) SetLogMaxMB(ctx context.Context, value int) (Values, error) {
	current, err := s.Get(ctx)
	if err != nil {
		return Values{}, err
	}
	return s.SetLogs(ctx, value, current.LogEnabled)
}

func (s *Store) SetLogs(ctx context.Context, value int, enabled bool) (Values, error) {
	if value < MinLogMaxMB || value > MaxLogMaxMB {
		return Values{}, errors.New("日志上限必须为 1–1024 MB")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE system_settings SET log_max_mb=?,log_enabled=?,updated_at=? WHERE id=1`, value, boolInt(enabled), database.Now())
	if err != nil {
		return Values{}, err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return Values{}, rowsErr
	} else if affected == 0 {
		return Values{}, sql.ErrNoRows
	}
	return s.Get(ctx)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

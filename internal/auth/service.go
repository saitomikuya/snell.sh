package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/proxy-panel/proxy-panel/internal/database"
)

var (
	ErrUnauthorized           = errors.New("UNAUTHORIZED")
	ErrPasswordChangeRequired = errors.New("PASSWORD_CHANGE_REQUIRED")
)

type Service struct {
	db  *sql.DB
	ttl time.Duration
}
type Session struct {
	ID         string
	Restricted bool
	ExpiresAt  time.Time
	CSRF       string
}

func New(db *sql.DB) *Service { return &Service{db: db, ttl: 24 * time.Hour} }

func (s *Service) Initialize(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_state`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := HashPassword(DefaultPassword)
	if err != nil {
		return err
	}
	now := database.Now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO auth_state(id,password_hash,must_change_password,created_at,updated_at) VALUES(1,?,1,?,?)`, hash, now, now)
	return err
}

func (s *Service) Login(ctx context.Context, password, remoteIP, userAgent string) (Session, error) {
	var hash string
	var restricted bool
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash,must_change_password FROM auth_state WHERE id=1`).Scan(&hash, &restricted); err != nil {
		return Session{}, ErrUnauthorized
	}
	if !VerifyPassword(hash, password) {
		return Session{}, ErrUnauthorized
	}
	token, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(s.ttl)
	id := uuid.NewString()
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(id,token_hash,csrf_hash,restricted,remote_ip,user_agent,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, digest(token), digest(csrf), restricted, remoteIP, userAgent, expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return Session{}, err
	}
	return Session{ID: token, Restricted: restricted, ExpiresAt: expires, CSRF: csrf}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrUnauthorized
	}
	var id, expiresRaw string
	var restricted bool
	err := s.db.QueryRowContext(ctx, `SELECT id,restricted,expires_at FROM sessions WHERE token_hash=?`, digest(token)).Scan(&id, &restricted, &expiresRaw)
	if err != nil {
		return Session{}, ErrUnauthorized
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || time.Now().After(expires) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
		return Session{}, ErrUnauthorized
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE id=?`, database.Now(), id)
	return Session{ID: id, Restricted: restricted, ExpiresAt: expires}, nil
}

func (s *Service) CheckCSRF(ctx context.Context, sessionID, csrf string) bool {
	var expected string
	if csrf == "" || s.db.QueryRowContext(ctx, `SELECT csrf_hash FROM sessions WHERE id=?`, sessionID).Scan(&expected) != nil {
		return false
	}
	return expected == digest(csrf)
}

func (s *Service) ChangePassword(ctx context.Context, newPassword string) error {
	if err := ValidateNewPassword(newPassword); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	now := database.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_state SET password_hash=?,must_change_password=0,password_changed_at=?,updated_at=? WHERE id=1`, hash, now, now); err != nil {
		tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Service) VerifyCurrent(ctx context.Context, password string) bool {
	var hash string
	if s.db.QueryRowContext(ctx, `SELECT password_hash FROM auth_state WHERE id=1`).Scan(&hash) != nil {
		return false
	}
	return VerifyPassword(hash, password)
}

func (s *Service) ResetPassword(ctx context.Context) error {
	hash, err := HashPassword(DefaultPassword)
	if err != nil {
		return err
	}
	now := database.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_state SET password_hash=?,must_change_password=1,password_changed_at=NULL,updated_at=? WHERE id=1`, hash, now); err != nil {
		tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, sessionID)
	return err
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

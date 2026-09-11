package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/proxy-panel/proxy-panel/internal/database"
)

var anyConnectUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type AnyConnectUser struct {
	ID         string `json:"id"`
	NodeID     string `json:"nodeId"`
	Username   string `json:"username"`
	RouteGroup string `json:"routeGroup"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type AnyConnectUserRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	RouteGroup string `json:"routeGroup"`
	Enabled    bool   `json:"enabled"`
}

type AnyConnectAssetState struct {
	NodeID                 string `json:"nodeId"`
	CertificateLastAttempt string `json:"certificateLastAttempt"`
	CertificateLastSuccess string `json:"certificateLastSuccess"`
	CertificateFingerprint string `json:"certificateFingerprint"`
	CertificateNotAfter    string `json:"certificateNotAfter"`
	CertificateError       string `json:"certificateError,omitempty"`
	CIDRLastAttempt        string `json:"cidrLastAttempt"`
	CIDRLastSuccess        string `json:"cidrLastSuccess"`
	CIDRFingerprint        string `json:"cidrFingerprint"`
	CIDRCount              int    `json:"cidrCount"`
	CIDRError              string `json:"cidrError,omitempty"`
	UpdatedAt              string `json:"updatedAt"`
}

func (s *Store) ListAnyConnectUsers(ctx context.Context, nodeID string) ([]AnyConnectUser, error) {
	if err := s.requireAnyConnectNode(ctx, nodeID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,node_id,username,route_group,enabled,created_at,updated_at FROM anyconnect_users WHERE node_id=? ORDER BY created_at,username`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]AnyConnectUser, 0)
	for rows.Next() {
		var user AnyConnectUser
		if err = rows.Scan(&user.ID, &user.NodeID, &user.Username, &user.RouteGroup, &user.Enabled, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) GetAnyConnectUser(ctx context.Context, nodeID, userID string) (AnyConnectUser, error) {
	var user AnyConnectUser
	err := s.db.QueryRowContext(ctx, `SELECT id,node_id,username,route_group,enabled,created_at,updated_at FROM anyconnect_users WHERE node_id=? AND id=?`, nodeID, userID).Scan(&user.ID, &user.NodeID, &user.Username, &user.RouteGroup, &user.Enabled, &user.CreatedAt, &user.UpdatedAt)
	return user, err
}

func (s *Store) CreateAnyConnectUser(ctx context.Context, nodeID string, req AnyConnectUserRequest) (AnyConnectUser, error) {
	if err := s.requireAnyConnectNode(ctx, nodeID); err != nil {
		return AnyConnectUser{}, err
	}
	if err := validateAnyConnectUser(req, true); err != nil {
		return AnyConnectUser{}, err
	}
	secretID, err := s.secrets.Put(ctx, "anyconnect-user-password", []byte(req.Password))
	if err != nil {
		return AnyConnectUser{}, err
	}
	id := uuid.NewString()
	now := database.Now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO anyconnect_users(id,node_id,username,password_secret_ref,route_group,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, nodeID, req.Username, secretID, req.RouteGroup, boolInt(req.Enabled), now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return AnyConnectUser{}, errors.New("此 AnyConnect 节点已存在同名用户")
		}
		return AnyConnectUser{}, err
	}
	return s.GetAnyConnectUser(ctx, nodeID, id)
}

func (s *Store) UpdateAnyConnectUser(ctx context.Context, nodeID, userID string, req AnyConnectUserRequest) (AnyConnectUser, error) {
	if err := validateAnyConnectUser(req, false); err != nil {
		return AnyConnectUser{}, err
	}
	old, err := s.GetAnyConnectUser(ctx, nodeID, userID)
	if err != nil {
		return AnyConnectUser{}, err
	}
	var secretRef string
	if err = s.db.QueryRowContext(ctx, `SELECT password_secret_ref FROM anyconnect_users WHERE id=?`, userID).Scan(&secretRef); err != nil {
		return AnyConnectUser{}, err
	}
	if req.Password != "" {
		secretRef, err = s.secrets.Put(ctx, "anyconnect-user-password", []byte(req.Password))
		if err != nil {
			return AnyConnectUser{}, err
		}
	}
	if req.Username == "" {
		req.Username = old.Username
	}
	_, err = s.db.ExecContext(ctx, `UPDATE anyconnect_users SET username=?,password_secret_ref=?,route_group=?,enabled=?,updated_at=? WHERE node_id=? AND id=?`, req.Username, secretRef, req.RouteGroup, boolInt(req.Enabled), database.Now(), nodeID, userID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return AnyConnectUser{}, errors.New("此 AnyConnect 节点已存在同名用户")
		}
		return AnyConnectUser{}, err
	}
	return s.GetAnyConnectUser(ctx, nodeID, userID)
}

func (s *Store) DeleteAnyConnectUser(ctx context.Context, nodeID, userID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM anyconnect_users WHERE node_id=? AND id=?`, nodeID, userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) AnyConnectUserPassword(ctx context.Context, userID string) (string, error) {
	var ref string
	if err := s.db.QueryRowContext(ctx, `SELECT password_secret_ref FROM anyconnect_users WHERE id=?`, userID).Scan(&ref); err != nil {
		return "", err
	}
	value, err := s.secrets.Get(ctx, ref)
	return string(value), err
}

func (s *Store) AnyConnectSecrets(ctx context.Context, nodeID string) (AnyConnectSecrets, error) {
	raw, err := s.Secret(ctx, nodeID)
	if err != nil {
		return AnyConnectSecrets{}, err
	}
	var values AnyConnectSecrets
	if err = json.Unmarshal([]byte(raw), &values); err != nil {
		return AnyConnectSecrets{}, errors.New("AnyConnect 加密凭据格式无效")
	}
	if values.PrivateKeyPassphrase == "" {
		values.PrivateKeyPassphrase = values.CertificatePassword
	}
	return values, nil
}

func (s *Store) GetAnyConnectAssetState(ctx context.Context, nodeID string) (AnyConnectAssetState, error) {
	var state AnyConnectAssetState
	err := s.db.QueryRowContext(ctx, `SELECT node_id,certificate_last_attempt,certificate_last_success,certificate_fingerprint,certificate_not_after,certificate_error,cidr_last_attempt,cidr_last_success,cidr_fingerprint,cidr_count,cidr_error,updated_at FROM anyconnect_asset_state WHERE node_id=?`, nodeID).Scan(&state.NodeID, &state.CertificateLastAttempt, &state.CertificateLastSuccess, &state.CertificateFingerprint, &state.CertificateNotAfter, &state.CertificateError, &state.CIDRLastAttempt, &state.CIDRLastSuccess, &state.CIDRFingerprint, &state.CIDRCount, &state.CIDRError, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		now := database.Now()
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO anyconnect_asset_state(node_id,updated_at) VALUES(?,?)`, nodeID, now)
		if err != nil {
			return state, err
		}
		return s.GetAnyConnectAssetState(ctx, nodeID)
	}
	return state, err
}

func (s *Store) RecordAnyConnectCertificate(ctx context.Context, nodeID, attemptedAt, succeededAt, fingerprint, notAfter, message string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO anyconnect_asset_state(node_id,certificate_last_attempt,certificate_last_success,certificate_fingerprint,certificate_not_after,certificate_error,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET certificate_last_attempt=excluded.certificate_last_attempt,certificate_last_success=CASE WHEN excluded.certificate_last_success='' THEN anyconnect_asset_state.certificate_last_success ELSE excluded.certificate_last_success END,certificate_fingerprint=CASE WHEN excluded.certificate_fingerprint='' THEN anyconnect_asset_state.certificate_fingerprint ELSE excluded.certificate_fingerprint END,certificate_not_after=CASE WHEN excluded.certificate_not_after='' THEN anyconnect_asset_state.certificate_not_after ELSE excluded.certificate_not_after END,certificate_error=excluded.certificate_error,updated_at=excluded.updated_at`, nodeID, attemptedAt, succeededAt, fingerprint, notAfter, message, database.Now())
	return err
}

func (s *Store) RecordAnyConnectCIDR(ctx context.Context, nodeID, attemptedAt, succeededAt, fingerprint string, count int, message string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO anyconnect_asset_state(node_id,cidr_last_attempt,cidr_last_success,cidr_fingerprint,cidr_count,cidr_error,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET cidr_last_attempt=excluded.cidr_last_attempt,cidr_last_success=CASE WHEN excluded.cidr_last_success='' THEN anyconnect_asset_state.cidr_last_success ELSE excluded.cidr_last_success END,cidr_fingerprint=CASE WHEN excluded.cidr_fingerprint='' THEN anyconnect_asset_state.cidr_fingerprint ELSE excluded.cidr_fingerprint END,cidr_count=CASE WHEN excluded.cidr_fingerprint='' THEN anyconnect_asset_state.cidr_count ELSE excluded.cidr_count END,cidr_error=excluded.cidr_error,updated_at=excluded.updated_at`, nodeID, attemptedAt, succeededAt, fingerprint, count, message, database.Now())
	return err
}

func (s *Store) requireAnyConnectNode(ctx context.Context, nodeID string) error {
	var kind string
	if err := s.db.QueryRowContext(ctx, `SELECT type FROM nodes WHERE id=?`, nodeID).Scan(&kind); err != nil {
		return err
	}
	if kind != "anyconnect" {
		return errors.New("节点不是 AnyConnect 类型")
	}
	return nil
}

func validateAnyConnectUser(req AnyConnectUserRequest, requirePassword bool) error {
	if req.Username != "" && !anyConnectUsernamePattern.MatchString(req.Username) {
		return errors.New("用户名必须以字母或数字开头，仅包含字母、数字、点、下划线或连字符，最长 64 位")
	}
	if requirePassword && req.Username == "" {
		return errors.New("用户名不能为空")
	}
	if req.Password != "" && (!utf8.ValidString(req.Password) || utf8.RuneCountInString(req.Password) < 4 || utf8.RuneCountInString(req.Password) > 128 || strings.ContainsAny(req.Password, "\r\n")) {
		return errors.New("密码必须为 4–128 个字符且不能包含换行")
	}
	if requirePassword && req.Password == "" {
		return errors.New("密码不能为空")
	}
	if req.RouteGroup != "full" && req.RouteGroup != "cn" && req.RouteGroup != "select" {
		return errors.New("路由组必须是全隧道、中国直连或登录时选择")
	}
	return nil
}

// ValidateAnyConnectUserRequest exposes the same validation used by the
// CRUD methods so bulk imports can reject malformed files before mutating the
// local database.
func ValidateAnyConnectUserRequest(req AnyConnectUserRequest) error {
	return validateAnyConnectUser(req, true)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

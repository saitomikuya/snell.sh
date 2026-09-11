package nodes

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"github.com/google/uuid"
	"github.com/proxy-panel/proxy-panel/internal/database"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
)

type Store struct {
	db      *sql.DB
	secrets *secretstore.Store
}

func NewStore(db *sql.DB, secrets *secretstore.Store) *Store { return &Store{db: db, secrets: secrets} }

func (s *Store) InitializeDefaults(ctx context.Context) error {
	const marker = "default-shadowtls-ss-v1"
	// Older images created the built-in Snell node as a loopback-only
	// listener.  Keep the migration narrowly scoped to that built-in node so
	// user-created ShadowTLS backends are not changed unexpectedly.
	if err := s.migrateDefaultSnellListen(ctx); err != nil {
		return err
	}
	var marked string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key=?`, marker).Scan(&marked); err == nil {
		return nil
	}
	var nodeCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&nodeCount); err != nil {
		return err
	}
	ssPort, err := freePort()
	if existing, getErr := s.Get(ctx, "ss-main"); getErr == nil {
		ssPort = existing.ListenPort
	} else if err != nil {
		return err
	}
	shadowSSPort, err := freePort()
	if err != nil {
		return err
	}
	for shadowSSPort == ssPort || shadowSSPort == 8443 {
		shadowSSPort, err = freePort()
		if err != nil {
			return err
		}
	}
	defaults := []struct {
		id          string
		req         CreateRequest
		secretBytes int
	}{
		{"snell-main", CreateRequest{Type: "snell", Name: "Snell 主节点", RuntimeVersion: "v5.0.1", ListenHost: "0.0.0.0", ListenPort: 6160, Config: Config{Version: "v5", TFO: true}}, 16},
		{"shadowtls-snell-main", CreateRequest{Type: "shadowtls", Name: "Snell ShadowTLS", RuntimeVersion: "v0.2.25", ListenHost: "0.0.0.0", ListenPort: 8443, BackendNodeID: "snell-main", Config: Config{Version: "v3", SNI: "www.microsoft.com", WildcardSNI: "off", TFO: true}}, 24},
		// ShadowTLS only wraps TCP. The SS-2022 listener remains public so its
		// UDP relay is reachable on the original SS port.
		{"ss-main", CreateRequest{Type: "ss2022", Name: "SS-2022 主节点", RuntimeVersion: "v1.24.0", ListenHost: "0.0.0.0", ListenPort: ssPort, Config: Config{Mode: "tcp_and_udp", Method: "2022-blake3-aes-128-gcm", TFO: true}}, 16},
		{"shadowtls-ss-main", CreateRequest{Type: "shadowtls", Name: "SS-2022 ShadowTLS", RuntimeVersion: "v0.2.25", ListenHost: "0.0.0.0", ListenPort: shadowSSPort, BackendNodeID: "ss-main", Config: Config{Version: "v3", SNI: "www.microsoft.com", WildcardSNI: "off", TFO: true}}, 24},
	}
	if nodeCount > 0 {
		// Existing installations receive only the newly introduced default pair.
		// User-deleted original defaults are never recreated on restart.
		defaults = defaults[len(defaults)-1:]
	}
	for _, item := range defaults {
		if _, getErr := s.Get(ctx, item.id); getErr == nil {
			continue
		}
		if item.id == "shadowtls-ss-main" {
			if _, getErr := s.Get(ctx, "ss-main"); getErr != nil {
				continue
			}
		}
		raw, err := secretstore.Random(item.secretBytes)
		if err != nil {
			return err
		}
		item.req.Secret = base64.StdEncoding.EncodeToString(raw)
		if item.req.Type == "shadowtls" {
			item.req.Secret = base64.RawURLEncoding.EncodeToString(raw)
		}
		if _, err = s.createWithID(ctx, item.id, item.req, "bootstrap"); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_meta(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, marker, "complete", database.Now())
	return err
}

// migrateDefaultSnellListen updates only the legacy built-in Snell node.  A
// fresh database gets the public default above, while an existing deployment
// with the previous loopback default is made consistent after upgrading the
// image.  If a user has already occupied the public port, leave their setup
// untouched and retry on the next startup rather than failing panel startup.
func (s *Store) migrateDefaultSnellListen(ctx context.Context) error {
	const marker = "default-snell-public-listen-v1"
	var applied string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key=?`, marker).Scan(&applied)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var host string
	err = s.db.QueryRowContext(ctx, `SELECT listen_host FROM nodes WHERE id=? AND type='snell'`, "snell-main").Scan(&host)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The default node may not exist yet; InitializeDefaults will create it
		// with the new public listen address.
	case err != nil:
		return err
	case host == "127.0.0.1":
		if conflictErr := s.checkConflict(ctx, "snell-main", "snell", "0.0.0.0", 6160, Config{}); conflictErr != nil {
			// A user may already be using the public port. Preserve that
			// configuration and retry the migration on a later startup.
			return nil
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE nodes SET listen_host=?,updated_at=? WHERE id=?`, "0.0.0.0", database.Now(), "snell-main"); err != nil {
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, `INSERT INTO app_meta(key,value,updated_at) VALUES(?,?,?)`, marker, "complete", database.Now())
	if err != nil {
		// Another panel process may have completed this idempotent migration.
		var existing string
		if scanErr := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key=?`, marker).Scan(&existing); scanErr == nil {
			return nil
		}
	}
	return err
}

func (s *Store) Create(ctx context.Context, req CreateRequest, source string) (Node, error) {
	return s.createWithID(ctx, uuid.NewString(), req, source)
}
func (s *Store) createWithID(ctx context.Context, id string, req CreateRequest, source string) (Node, error) {
	Normalize(&req)
	if err := Validate(req); err != nil {
		return Node{}, err
	}
	if req.Type == "anyconnect" {
		payload, _ := json.Marshal(AnyConnectSecrets{CertificatePassword: req.CertificatePassword, PrivateKeyPassphrase: req.PrivateKeyPassphrase})
		req.Secret = string(payload)
	} else if req.Secret == "" {
		raw, _ := secretstore.Random(24)
		req.Secret = base64.RawURLEncoding.EncodeToString(raw)
	}
	if err := s.checkConflict(ctx, "", req.Type, req.ListenHost, req.ListenPort, req.Config); err != nil {
		return Node{}, err
	}
	if req.Type == "shadowtls" {
		if err := s.checkBackend(ctx, req.BackendNodeID); err != nil {
			return Node{}, err
		}
	}
	secretID, err := s.secrets.Put(ctx, req.Type, []byte(req.Secret))
	if err != nil {
		return Node{}, err
	}
	now := database.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO nodes(id,type,name,enabled,desired_state,runtime_version,listen_host,listen_port,backend_node_id,created_at,updated_at) VALUES(?,?,?,1,'running',?,?,?,?,?,?)`, id, req.Type, req.Name, req.RuntimeVersion, req.ListenHost, req.ListenPort, nullString(req.BackendNodeID), now, now)
	if err != nil {
		return Node{}, err
	}
	cfg := ConfigJSON(req.Config)
	_, err = tx.ExecContext(ctx, `INSERT INTO node_configs(node_id,schema_version,config_json,secret_ref,revision) VALUES(?,1,?,?,1)`, id, cfg, secretID)
	if err != nil {
		return Node{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO config_revisions(node_id,revision,config_snapshot,runtime_version,source,created_at) VALUES(?,1,?,?,?,?)`, id, cfg, req.RuntimeVersion, source, now)
	if err != nil {
		return Node{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_instances(node_id,actual_state,restart_count,updated_at) VALUES(?,'stopped',0,?)`, id, now)
	if err != nil {
		return Node{}, err
	}
	period := now[:7]
	_, err = tx.ExecContext(ctx, `INSERT INTO traffic_counters(node_id,period,updated_at) VALUES(?,?,?)`, id, period, now)
	if err != nil {
		return Node{}, err
	}
	if err = tx.Commit(); err != nil {
		return Node{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) List(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, queryNode+` ORDER BY n.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, rows.Err()
}
func (s *Store) Get(ctx context.Context, id string) (Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, queryNode+` WHERE n.id=?`, id))
}

func (s *Store) Secret(ctx context.Context, id string) (string, error) {
	var ref string
	if err := s.db.QueryRowContext(ctx, `SELECT secret_ref FROM node_configs WHERE node_id=?`, id).Scan(&ref); err != nil {
		return "", err
	}
	raw, err := s.secrets.Get(ctx, ref)
	return string(raw), err
}

func (s *Store) Update(ctx context.Context, id string, req UpdateRequest, source string) (Node, error) {
	old, err := s.Get(ctx, id)
	if err != nil {
		return Node{}, err
	}
	NormalizeUpdate(old.Type, &req)
	create := CreateRequest{Type: old.Type, Name: req.Name, RuntimeVersion: req.RuntimeVersion, ListenHost: req.ListenHost, ListenPort: req.ListenPort, BackendNodeID: req.BackendNodeID, Config: req.Config, Secret: req.Secret, CertificatePassword: req.CertificatePassword, PrivateKeyPassphrase: req.PrivateKeyPassphrase}
	if err = Validate(create); err != nil {
		return Node{}, err
	}
	if err = s.checkConflict(ctx, id, old.Type, req.ListenHost, req.ListenPort, req.Config); err != nil {
		return Node{}, err
	}
	if old.Type == "shadowtls" {
		if err = s.checkBackend(ctx, req.BackendNodeID); err != nil {
			return Node{}, err
		}
	}
	var revision int
	var secretRef string
	if err = s.db.QueryRowContext(ctx, `SELECT revision,secret_ref FROM node_configs WHERE node_id=?`, id).Scan(&revision, &secretRef); err != nil {
		return Node{}, err
	}
	if old.Type == "anyconnect" && (req.CertificatePassword != "" || req.PrivateKeyPassphrase != "") {
		values, valuesErr := s.AnyConnectSecrets(ctx, id)
		if valuesErr != nil {
			return Node{}, valuesErr
		}
		if req.CertificatePassword != "" {
			values.CertificatePassword = req.CertificatePassword
		}
		if req.PrivateKeyPassphrase != "" {
			values.PrivateKeyPassphrase = req.PrivateKeyPassphrase
		}
		payload, _ := json.Marshal(values)
		secretRef, err = s.secrets.Put(ctx, old.Type, payload)
		if err != nil {
			return Node{}, err
		}
	} else if req.Secret != "" {
		secretRef, err = s.secrets.Put(ctx, old.Type, []byte(req.Secret))
		if err != nil {
			return Node{}, err
		}
	}
	now := database.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	revision++
	_, err = tx.ExecContext(ctx, `UPDATE nodes SET name=?,runtime_version=?,listen_host=?,listen_port=?,backend_node_id=?,updated_at=? WHERE id=?`, req.Name, req.RuntimeVersion, req.ListenHost, req.ListenPort, nullString(req.BackendNodeID), now, id)
	if err != nil {
		return Node{}, err
	}
	cfg := ConfigJSON(req.Config)
	_, err = tx.ExecContext(ctx, `UPDATE node_configs SET config_json=?,secret_ref=?,revision=? WHERE node_id=?`, cfg, secretRef, revision, id)
	if err != nil {
		return Node{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO config_revisions(node_id,revision,config_snapshot,runtime_version,source,created_at) VALUES(?,?,?,?,?,?)`, id, revision, cfg, req.RuntimeVersion, source, now)
	if err != nil {
		return Node{}, err
	}
	if err = tx.Commit(); err != nil {
		return Node{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	var deps int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE backend_node_id=?`, id).Scan(&deps); err != nil {
		return err
	}
	if deps > 0 {
		return errors.New("存在依赖此后端的 ShadowTLS 节点")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id)
	return err
}
func (s *Store) SetDesired(ctx context.Context, id, state string) error {
	if state != "running" && state != "stopped" {
		return errors.New("无效的期望状态")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET desired_state=?,updated_at=? WHERE id=?`, state, database.Now(), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateRuntime(ctx context.Context, id, state string, pid int, lastError string, restarts int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO runtime_instances(node_id,pid,actual_state,last_error,restart_count,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET pid=excluded.pid,actual_state=excluded.actual_state,last_error=excluded.last_error,restart_count=excluded.restart_count,updated_at=excluded.updated_at`, id, nullInt(pid), state, lastError, restarts, database.Now())
	return err
}

func (s *Store) checkBackend(ctx context.Context, id string) error {
	var kind string
	if err := s.db.QueryRowContext(ctx, `SELECT type FROM nodes WHERE id=?`, id).Scan(&kind); err != nil {
		return errors.New("后端节点不存在")
	}
	if kind != "snell" && kind != "ss2022" {
		return errors.New("只有 Snell 或 SS-2022 节点可以作为 ShadowTLS 后端")
	}
	return nil
}
func (s *Store) checkConflict(ctx context.Context, exclude, newType, host string, port int, newConfig Config) error {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id,n.type,n.listen_host,c.config_json FROM nodes n JOIN node_configs c ON c.node_id=n.id WHERE n.listen_port=? AND n.id<>?`, port, exclude)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, otherHost, raw string
		if err = rows.Scan(&id, &kind, &otherHost, &raw); err != nil {
			return err
		}
		var cfg Config
		_ = json.Unmarshal([]byte(raw), &cfg)
		if host == "0.0.0.0" || host == "::" || otherHost == "0.0.0.0" || otherHost == "::" || host == otherHost {
			if protocolOverlap(kind, cfg, newType, newConfig) {
				return fmt.Errorf("端口 %d 与节点 %s 冲突", port, id)
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if newType != "anyconnect" {
		return nil
	}
	newPool, err := netip.ParsePrefix(newConfig.VPNNetwork)
	if err != nil {
		return err
	}
	poolRows, err := s.db.QueryContext(ctx, `SELECT n.id,c.config_json FROM nodes n JOIN node_configs c ON c.node_id=n.id WHERE n.type='anyconnect' AND n.id<>?`, exclude)
	if err != nil {
		return err
	}
	defer poolRows.Close()
	for poolRows.Next() {
		var id, raw string
		if err = poolRows.Scan(&id, &raw); err != nil {
			return err
		}
		var cfg Config
		if json.Unmarshal([]byte(raw), &cfg) != nil {
			continue
		}
		existing, parseErr := netip.ParsePrefix(cfg.VPNNetwork)
		if parseErr == nil && (existing.Contains(newPool.Addr()) || newPool.Contains(existing.Addr())) {
			return fmt.Errorf("VPN 地址池 %s 与 AnyConnect 节点 %s 重叠", newConfig.VPNNetwork, id)
		}
	}
	return poolRows.Err()
}
func protocolOverlap(existingType string, existingConfig Config, newType string, newConfig Config) bool {
	existing := protocols(existingType, existingConfig)
	incoming := protocols(newType, newConfig)
	for _, left := range existing {
		for _, right := range incoming {
			if left == right {
				return true
			}
		}
	}
	return false
}
func protocols(kind string, config Config) []string {
	if kind == "anyconnect" {
		if config.UDPEnabled {
			return []string{"tcp", "udp"}
		}
		return []string{"tcp"}
	}
	if kind != "ss2022" {
		return []string{"tcp"}
	}
	switch config.Mode {
	case "tcp_only":
		return []string{"tcp"}
	case "udp_only":
		return []string{"udp"}
	default:
		return []string{"tcp", "udp"}
	}
}

const queryNode = `SELECT n.id,n.type,n.name,n.enabled,n.desired_state,n.runtime_version,n.listen_host,n.listen_port,COALESCE(n.backend_node_id,''),c.config_json,c.revision,COALESCE(r.actual_state,'stopped'),COALESCE(r.last_error,''),n.created_at,n.updated_at FROM nodes n JOIN node_configs c ON c.node_id=n.id LEFT JOIN runtime_instances r ON r.node_id=n.id`

type scanner interface{ Scan(...any) error }

func scanNode(row scanner) (Node, error) {
	var n Node
	var cfg string
	if err := row.Scan(&n.ID, &n.Type, &n.Name, &n.Enabled, &n.DesiredState, &n.RuntimeVersion, &n.ListenHost, &n.ListenPort, &n.BackendNodeID, &cfg, &n.Revision, &n.ActualState, &n.LastError, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return Node{}, err
	}
	if err := json.Unmarshal([]byte(cfg), &n.Config); err != nil {
		return Node{}, err
	}
	if n.Type == "anyconnect" {
		normalizeAnyConnectRouting(&n.Config)
	}
	return n, nil
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	_, raw, _ := net.SplitHostPort(listener.Addr().String())
	return strconv.Atoi(raw)
}

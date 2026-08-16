package database

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE auth_state(id INTEGER PRIMARY KEY CHECK(id=1), password_hash TEXT NOT NULL, must_change_password INTEGER NOT NULL, password_changed_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE sessions(id TEXT PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, csrf_hash TEXT NOT NULL, restricted INTEGER NOT NULL, remote_ip TEXT NOT NULL, user_agent TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL, last_seen_at TEXT NOT NULL);
CREATE INDEX sessions_token_hash ON sessions(token_hash);
CREATE TABLE secrets(id TEXT PRIMARY KEY, kind TEXT NOT NULL, ciphertext BLOB NOT NULL, nonce BLOB NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE nodes(id TEXT PRIMARY KEY, type TEXT NOT NULL CHECK(type IN ('snell','ss2022','shadowtls')), name TEXT NOT NULL, enabled INTEGER NOT NULL, desired_state TEXT NOT NULL, runtime_version TEXT NOT NULL, listen_host TEXT NOT NULL, listen_port INTEGER NOT NULL, backend_node_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, FOREIGN KEY(backend_node_id) REFERENCES nodes(id));
CREATE UNIQUE INDEX nodes_listen ON nodes(listen_host,listen_port,type);
CREATE TABLE node_configs(node_id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL, config_json TEXT NOT NULL, secret_ref TEXT NOT NULL, revision INTEGER NOT NULL, FOREIGN KEY(node_id) REFERENCES nodes(id) ON DELETE CASCADE, FOREIGN KEY(secret_ref) REFERENCES secrets(id));
CREATE TABLE config_revisions(id INTEGER PRIMARY KEY AUTOINCREMENT, node_id TEXT NOT NULL, revision INTEGER NOT NULL, config_snapshot TEXT NOT NULL, runtime_version TEXT NOT NULL, source TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(node_id,revision));
CREATE TABLE runtime_instances(node_id TEXT PRIMARY KEY, pid INTEGER, actual_state TEXT NOT NULL, started_at TEXT, exit_code INTEGER, last_error TEXT, restart_count INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL);
CREATE TABLE traffic_counters(node_id TEXT PRIMARY KEY, period TEXT NOT NULL, upload_bytes INTEGER NOT NULL DEFAULT 0, download_bytes INTEGER NOT NULL DEFAULT 0, quota_bytes INTEGER NOT NULL DEFAULT 0, reset_day INTEGER NOT NULL DEFAULT 1, paused INTEGER NOT NULL DEFAULT 0, paused_by_quota INTEGER NOT NULL DEFAULT 0, last_sample INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL);
CREATE TABLE jobs(id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL, progress INTEGER NOT NULL, result TEXT NOT NULL, error TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE audit_logs(id INTEGER PRIMARY KEY AUTOINCREMENT, action TEXT NOT NULL, target_type TEXT NOT NULL, target_id TEXT NOT NULL, remote_ip TEXT NOT NULL, details TEXT NOT NULL, success INTEGER NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE backups(id TEXT PRIMARY KEY, filename TEXT NOT NULL UNIQUE, sha256 TEXT NOT NULL, size INTEGER NOT NULL, created_at TEXT NOT NULL);
`,
	`CREATE TABLE IF NOT EXISTS app_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);`,
	`CREATE TABLE IF NOT EXISTS traffic_samples(node_id TEXT PRIMARY KEY, upload_bytes INTEGER NOT NULL DEFAULT 0, download_bytes INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL);`,
}

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	DataDir      string
	Bind         string
	Port         int
	AgentSocket  string
	SecureCookie bool
	UID          int
	GID          int
}

func Load() (Config, error) {
	c := Config{DataDir: env("PANEL_DATA_DIR", "/data"), Bind: env("PANEL_BIND", "0.0.0.0"), Port: 8080, UID: envInt("PANEL_UID", 10001), GID: envInt("PANEL_GID", 10001)}
	if raw := os.Getenv("PANEL_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("invalid PANEL_PORT")
		}
		c.Port = port
	}
	c.AgentSocket = env("PANEL_AGENT_SOCKET", filepath.Join(c.DataDir, "runtime", "agent.sock"))
	c.SecureCookie = os.Getenv("PANEL_SECURE_COOKIE") == "1"
	return c, nil
}

func (c Config) Address() string { return fmt.Sprintf("%s:%d", c.Bind, c.Port) }

func (c Config) InitDirectories() error {
	directoryMode := os.FileMode(0770) | os.ModeSetgid
	dirs := []string{
		"db", "config/snell", "config/ss", "config/shadowtls", "config/anyconnect", "anyconnect/assets", "runtime",
		"upstream/manifests", "upstream/scripts", "upstream/checksums", "releases", "releases/uploads",
		"backups", "logs", "traffic", "secrets",
	}
	for _, dir := range dirs {
		path := filepath.Join(c.DataDir, dir)
		if err := os.MkdirAll(path, directoryMode); err != nil {
			return err
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(path, c.UID, c.GID); err != nil {
				return err
			}
		}
		if err := os.Chmod(path, directoryMode); err != nil {
			return err
		}
	}
	// The panel HTTP server runs as the unprivileged panel user while the
	// agent keeps root privileges for firewall and process management. Older
	// agent versions therefore may have left root-owned AnyConnect assets and
	// generated configs behind (MkdirTemp creates 0700 directories). Repair
	// those persistent trees when the privileged agent starts so the panel can
	// include them in consistency backups.
	if os.Geteuid() == 0 {
		for _, dir := range []string{"config", "anyconnect", "backups"} {
			if err := RepairOwnership(filepath.Join(c.DataDir, dir), c.UID, c.GID); err != nil {
				return err
			}
		}
	}
	probe, err := os.CreateTemp(c.DataDir, ".write-test-*")
	if err != nil {
		return fmt.Errorf("data directory is not writable: %w", err)
	}
	probePath := probe.Name()
	if _, err = probe.WriteString("ok"); err != nil {
		_ = probe.Close()
		_ = os.Remove(probePath)
		return fmt.Errorf("data directory is not writable: %w", err)
	}
	if err = probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return fmt.Errorf("data directory is not writable: %w", err)
	}
	return os.Remove(probePath)
}

// RepairOwnership recursively assigns a persistent data tree to the panel
// service account. It is intentionally a no-op for unprivileged processes:
// the root-owned agent and Docker entrypoint are the only callers that can
// repair an existing volume, while tests and rootless deployments keep their
// current ownership model.
func RepairOwnership(root string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Do not follow symlinks from a user-managed data volume while
		// repairing ownership; only entries inside this tree may be changed.
		return os.Lchown(path, uid, gid)
	})
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err == nil && value > 0 {
		return value
	}
	return fallback
}

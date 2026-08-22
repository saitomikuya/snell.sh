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
		"db", "config/snell", "config/ss", "config/shadowtls", "runtime",
		"upstream/manifests", "upstream/scripts", "upstream/checksums", "releases",
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
	probe := filepath.Join(c.DataDir, ".write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0600); err != nil {
		return fmt.Errorf("data directory is not writable: %w", err)
	}
	return os.Remove(probe)
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

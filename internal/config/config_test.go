package config

import "testing"

func TestLoadDefaultsToPublicBind(t *testing.T) {
	t.Setenv("PANEL_BIND", "")
	t.Setenv("PANEL_PORT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Bind != "0.0.0.0" {
		t.Fatalf("Bind = %q, want 0.0.0.0", cfg.Bind)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", cfg.Port)
	}
}

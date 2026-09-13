package anyconnect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestListUserTrafficParsesOcctlJSON(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "occtl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '[{\"Username\":\"alice\",\"raw_rx\":\"200\",\"raw_tx\":100},{\"Username\":\"alice\",\"raw_rx\":50,\"raw_tx\":25},{\"Username\":\"(none)\",\"raw_rx\":0,\"raw_tx\":0}]'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCCTL_BINARY", script)
	t.Setenv("PANEL_ANYCONNECT_RUNTIME_DIR", filepath.Join(dir, "runtime"))
	service := New(dir, nil)
	values, err := service.ListUserTraffic(context.Background(), nodes.Node{ID: "node-a", Type: "anyconnect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Username != "alice" || values[0].UploadBytes != 250 || values[0].DownloadBytes != 125 {
		t.Fatalf("unexpected occtl values: %+v", values)
	}
}

func TestListUserTrafficParsesOcctlShowUsersRXTX(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "occtl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '[{\"Username\":\"alice\",\"RX\":\"200\",\"TX\":\"100\"},{\"Username\":\"alice\",\"RX\":50,\"TX\":25}]'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCCTL_BINARY", script)
	t.Setenv("PANEL_ANYCONNECT_RUNTIME_DIR", filepath.Join(dir, "runtime"))
	service := New(dir, nil)
	values, err := service.ListUserTraffic(context.Background(), nodes.Node{ID: "node-a", Type: "anyconnect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Username != "alice" || values[0].UploadBytes != 250 || values[0].DownloadBytes != 125 {
		t.Fatalf("unexpected occtl RX/TX values: %+v", values)
	}
}

func TestListUserTrafficFallsBackToTunSysfsCounters(t *testing.T) {
	dir := t.TempDir()
	stats := filepath.Join(dir, "sys", "vpns0", "statistics")
	if err := os.MkdirAll(stats, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stats, "rx_bytes"), []byte("12345\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stats, "tx_bytes"), []byte("67890\n"), 0644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "occtl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '[{\"Username\":\"alice\",\"Device\":\"vpns0\"}]'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCCTL_BINARY", script)
	t.Setenv("PANEL_ANYCONNECT_RUNTIME_DIR", filepath.Join(dir, "runtime"))
	t.Setenv("PANEL_NET_STATS_DIR", filepath.Join(dir, "sys"))
	service := New(dir, nil)
	values, err := service.ListUserTraffic(context.Background(), nodes.Node{ID: "node-a", Type: "anyconnect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].UploadBytes != 12345 || values[0].DownloadBytes != 67890 {
		t.Fatalf("unexpected sysfs values: %+v", values)
	}
}

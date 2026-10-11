package agent

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeOutputFloodIsDrainedAndSummarized(t *testing.T) {
	manager := NewManager(t.TempDir(), nil)
	path := filepath.Join(manager.dataDir, "node.log")
	reader := strings.NewReader(strings.Repeat("Accept failed: No file descriptors available\n", 10000))
	manager.captureRuntimeOutput(path, "标准输出", reader, func() time.Time { return time.Unix(1, 0) })
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reader.Len() != 0 {
		t.Fatal("runtime output was not fully drained")
	}
	if got := strings.Count(string(content), "\n"); got != runtimeLogBurst+1 {
		t.Fatalf("flood persisted %d lines, want %d", got, runtimeLogBurst+1)
	}
	if !strings.Contains(string(content), "已省略 9980 行") {
		t.Fatalf("missing suppression summary: %s", content)
	}
}

func TestRuntimeOutputResumesAfterRateWindow(t *testing.T) {
	manager := NewManager(t.TempDir(), nil)
	path := filepath.Join(manager.dataDir, "node.log")
	reader := strings.NewReader(strings.Repeat("error\n", runtimeLogBurst+5) + "recovered\n")
	calls := 0
	manager.captureRuntimeOutput(path, "标准输出", reader, func() time.Time {
		calls++
		if calls > runtimeLogBurst+6 {
			return time.Unix(2, 0)
		}
		return time.Unix(1, 0)
	})
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "已省略 5 行") || !strings.Contains(string(content), "recovered") {
		t.Fatalf("missing resumed output or summary: %s", content)
	}
}

func TestRuntimeOutputOversizedLineDoesNotBlockFollowingOutput(t *testing.T) {
	manager := NewManager(t.TempDir(), nil)
	path := filepath.Join(manager.dataDir, "node.log")
	// Scanner's default 64 KiB token cap used to stop capture permanently.
	reader := strings.NewReader(strings.Repeat("x", 1024*1024) + "\nafter-large-line\nfinal-without-newline")
	manager.capture(path, "错误输出", reader)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > maxRuntimeLogLine+1024 || reader.Len() != 0 {
		t.Fatalf("oversized output not bounded/drained: size=%d remaining=%d", len(content), reader.Len())
	}
	for _, expected := range []string{"超长日志已截断", "after-large-line", "final-without-newline"} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("missing %q", expected)
		}
	}
}

type runtimeOutputReader struct {
	io.Reader
	closed bool
}

func (r *runtimeOutputReader) Close() error { r.closed = true; return nil }

func TestDisabledRuntimeLogsStillDrainAndClosePipe(t *testing.T) {
	manager := NewManager(t.TempDir(), nil)
	manager.logEnabled.Store(false)
	path := filepath.Join(manager.dataDir, "node.log")
	input := strings.NewReader(strings.Repeat("x", 100000) + "\nnext\n")
	reader := &runtimeOutputReader{Reader: input}
	manager.capture(path, "标准输出", reader)
	if input.Len() != 0 || !reader.closed {
		t.Fatal("disabled logging must still drain and close the pipe")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected log file: %v", err)
	}
}

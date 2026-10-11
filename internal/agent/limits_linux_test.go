//go:build linux

package agent

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestRuntimeChildrenInheritFileLimit(t *testing.T) {
	// Change limits only in an isolated subprocess. Starting the test binary
	// with a low soft limit reproduces Go's saved exec-child limit.
	for _, hard := range []string{"keep", "2048"} {
		t.Run(hard, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", `ulimit -S -n 1024 || exit; if [ "$2" != keep ]; then ulimit -H -n "$2" || exit; fi; exec "$1" -test.run='^TestPrepareRuntimeLimitsChild$'`, "sh", os.Args[0], hard)
			cmd.Env = append(os.Environ(), "PANEL_TEST_RUNTIME_LIMIT=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child limits (%s): %v\n%s", hard, err, output)
			}
		})
	}
}

func TestPrepareRuntimeLimitsChild(t *testing.T) {
	if os.Getenv("PANEL_TEST_RUNTIME_LIMIT") != "1" {
		t.Skip("subprocess helper")
	}
	var before, after syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &before); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRuntimeLimits(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &after); err != nil {
		t.Fatal(err)
	}
	if after.Max != before.Max || after.Cur < before.Cur || after.Cur < min(uint64(65536), after.Max) {
		t.Fatalf("unexpected limit change: before=%+v after=%+v", before, after)
	}
	output, err := exec.Command("sh", "-c", "ulimit -S -n").Output()
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil || child != after.Cur {
		t.Fatalf("exec child limit = %q, want %d (err=%v)", output, after.Cur, err)
	}
}

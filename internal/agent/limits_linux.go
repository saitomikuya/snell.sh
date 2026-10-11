//go:build linux

package agent

import (
	"fmt"
	"syscall"
)

// PrepareRuntimeLimits must run before the agent starts any child processes.
func PrepareRuntimeLimits() error {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return fmt.Errorf("read runtime file descriptor limit: %w", err)
	}
	// Go raises its own soft limit, but normally restores the original (often
	// 1024) limit in exec children. Explicitly use syscall.Setrlimit, even when
	// Cur is already high: this also disables Go's child-limit restoration.
	// Keep higher existing limits and never exceed the administrator's hard cap.
	if target := min(uint64(65536), limit.Max); limit.Cur < target {
		limit.Cur = target
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return fmt.Errorf("set runtime file descriptor limit: %w", err)
	}
	return nil
}

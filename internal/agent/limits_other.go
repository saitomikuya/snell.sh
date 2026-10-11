//go:build !linux

package agent

func PrepareRuntimeLimits() error { return nil }

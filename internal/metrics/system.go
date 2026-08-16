package metrics

import (
	"runtime"
	"time"
)

var started = time.Now()

type System struct {
	GOOS          string `json:"goos"`
	Arch          string `json:"arch"`
	CPUs          int    `json:"cpus"`
	Goroutines    int    `json:"goroutines"`
	MemoryBytes   uint64 `json:"memoryBytes"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
}

func Read() System {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return System{GOOS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Goroutines: runtime.NumGoroutine(), MemoryBytes: stats.Alloc, UptimeSeconds: int64(time.Since(started).Seconds())}
}

package agent

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	runtimelog "github.com/proxy-panel/proxy-panel/internal/runtime"
)

const (
	maxRuntimeLogLine = 16 * 1024
	runtimeLogBurst   = 20
)

func (m *Manager) capture(path, source string, reader io.Reader) {
	m.captureRuntimeOutput(path, source, reader, time.Now)
}

// Always drain both output pipes, including when logging is disabled, a line
// is oversized, or a runtime is flooding errors. Never let logging prevent a
// proxy from releasing sockets or handling traffic.
func (m *Manager) captureRuntimeOutput(path, source string, reader io.Reader, now func() time.Time) {
	if closer, ok := reader.(io.Closer); ok {
		defer closer.Close()
	}
	buffer := bufio.NewReaderSize(reader, maxRuntimeLogLine)
	line := make([]byte, 0, maxRuntimeLogLine)
	truncated := false
	window := now()
	written, suppressed := 0, 0
	flushSuppressed := func() {
		if suppressed > 0 {
			m.appendLog(path, fmt.Sprintf("[运行时%s] 日志输出过快，已省略 %d 行", source, suppressed))
			suppressed = 0
		}
	}
	defer flushSuppressed()
	for {
		chunk, err := buffer.ReadSlice('\n')
		remaining := maxRuntimeLogLine - len(line)
		if len(chunk) > remaining {
			truncated = true
			chunk = chunk[:remaining]
		}
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if len(line) > 0 {
			current := now()
			if current.Sub(window) >= time.Second {
				flushSuppressed()
				window, written = current, 0
			}
			if m.logEnabled.Load() {
				if written < runtimeLogBurst {
					text := runtimelog.Redact(strings.TrimRight(string(line), "\r\n"))
					if truncated {
						text += " [超长日志已截断]"
					}
					m.appendLog(path, "[运行时"+source+"] "+text)
					written++
				} else {
					suppressed++
				}
			}
			line = line[:0]
			truncated = false
		}
		if err != nil {
			if err != io.EOF {
				m.appendLog(path, "[运行时"+source+"] 读取进程输出失败："+runtimelog.Redact(err.Error()))
			}
			return
		}
	}
}

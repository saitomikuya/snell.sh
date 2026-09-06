package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/configgen"
	"github.com/proxy-panel/proxy-panel/internal/firewall"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	runtimelog "github.com/proxy-panel/proxy-panel/internal/runtime"
	"github.com/proxy-panel/proxy-panel/internal/settings"
	"github.com/proxy-panel/proxy-panel/internal/traffic"
)

type process struct {
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	started   time.Time
	restarts  int
	state     string
	lastError string
	stopping  bool
	exited    bool
}

const (
	maxNodeLogSize      int64 = 2 * 1024 * 1024
	maxNodeLogArchives        = 2
	stableRuntimeWindow       = 10 * time.Minute
)

type Manager struct {
	mu         sync.Mutex
	logMu      sync.Mutex
	dataDir    string
	nodes      *nodes.Store
	traffic    *traffic.Store
	settings   *settings.Store
	processes  map[string]*process
	closed     bool
	logLimit   atomic.Int64
	logEnabled atomic.Bool
}

func NewManager(dataDir string, store *nodes.Store, trafficStores ...*traffic.Store) *Manager {
	manager := &Manager{dataDir: dataDir, nodes: store, processes: map[string]*process{}}
	manager.logLimit.Store(settings.DefaultLogMaxMB << 20)
	manager.logEnabled.Store(true)
	if len(trafficStores) > 0 {
		manager.traffic = trafficStores[0]
	}
	return manager
}

func (m *Manager) SetSettingsStore(store *settings.Store) {
	m.settings = store
	if values, err := store.Get(context.Background()); err == nil {
		m.logLimit.Store(int64(values.LogMaxMB) << 20)
		m.logEnabled.Store(values.LogEnabled)
	}
}

func (m *Manager) Apply(nodeID string) (Result, error) {
	node, err := m.nodes.Get(context.Background(), nodeID)
	if err != nil {
		return Result{}, err
	}
	secret, err := m.nodes.Secret(context.Background(), nodeID)
	if err != nil {
		return Result{}, err
	}
	var backend *nodes.Node
	if node.BackendNodeID != "" {
		value, err := m.nodes.Get(context.Background(), node.BackendNodeID)
		if err != nil {
			return Result{}, err
		}
		backend = &value
	}
	content, err := configgen.Generate(node, secret, backend)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(m.dataDir, "config", configDir(node.Type))
	if err = os.MkdirAll(dir, 0750); err != nil {
		return Result{}, err
	}
	target := filepath.Join(dir, node.ID+configgen.Extension(node.Type))
	candidate := target + ".candidate"
	backup := target + ".last-good"
	if err = os.WriteFile(candidate, content, 0640); err != nil {
		return Result{}, err
	}
	if err = m.validateCandidate(node, candidate); err != nil {
		os.Remove(candidate)
		return Result{}, err
	}
	if _, err = os.Stat(target); err == nil {
		if err = copyFile(target, backup); err != nil {
			os.Remove(candidate)
			return Result{}, err
		}
	}
	if err = os.Rename(candidate, target); err != nil {
		return Result{}, err
	}
	if node.DesiredState == "running" {
		result, err := m.Restart(nodeID)
		if err != nil {
			if _, statErr := os.Stat(backup); statErr == nil {
				_ = copyFile(backup, target)
				_, _ = m.Restart(nodeID)
			}
			return Result{}, fmt.Errorf("apply failed and rolled back: %w", err)
		}
		return result, nil
	}
	return Result{OK: true, State: "stopped", Message: "configuration applied"}, nil
}

func (m *Manager) Start(id string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(id)
}
func (m *Manager) startLocked(id string) (Result, error) {
	return m.startLockedWithRestarts(id, 0)
}
func (m *Manager) startLockedWithRestarts(id string, restarts int) (Result, error) {
	if m.closed {
		return Result{}, errors.New("agent is shutting down")
	}
	if current := m.processes[id]; current != nil && current.cmd != nil && !current.exited {
		return Result{OK: true, State: current.state, PID: current.cmd.Process.Pid, RestartCount: current.restarts}, nil
	}
	node, err := m.nodes.Get(context.Background(), id)
	if err != nil {
		return Result{}, err
	}
	if node.BackendNodeID != "" {
		if _, err = m.startLocked(node.BackendNodeID); err != nil {
			return Result{}, fmt.Errorf("backend: %w", err)
		}
	}
	configPath := filepath.Join(m.dataDir, "config", configDir(node.Type), node.ID+configgen.Extension(node.Type))
	if _, err = os.Stat(configPath); err != nil {
		return Result{}, fmt.Errorf("configuration missing; apply it first")
	}
	binary := m.binaryPath(node)
	if _, err = os.Stat(binary); err != nil {
		message := "runtime missing: " + binary
		_ = m.nodes.UpdateRuntime(context.Background(), id, "failed", 0, message, 0)
		return Result{}, errors.New(message)
	}
	ctx, cancel := context.WithCancel(context.Background())
	args, err := m.commandArgs(node, configPath)
	if err != nil {
		cancel()
		return Result{}, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	if node.Type == "shadowtls" {
		cmd.Env = append(os.Environ(), "MONOIO_FORCE_LEGACY_DRIVER=1", "RUST_LOG=warn")
	}
	cmd.SysProcAttr = childProcessAttributes()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return Result{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return Result{}, err
	}
	logPath := filepath.Join(m.dataDir, "logs", node.ID+".log")
	if err = cmd.Start(); err != nil {
		cancel()
		return Result{}, err
	}
	p := &process{cmd: cmd, cancel: cancel, started: time.Now(), restarts: restarts, state: "running"}
	m.processes[id] = p
	_ = m.nodes.UpdateRuntime(context.Background(), id, "running", cmd.Process.Pid, "", restarts)
	m.appendLog(logPath, fmt.Sprintf("[运行状态] 节点进程已启动（PID %d）", cmd.Process.Pid))
	go m.capture(logPath, "标准输出", stdout)
	go m.capture(logPath, "错误输出", stderr)
	go m.wait(id, p)
	return Result{OK: true, State: "running", PID: cmd.Process.Pid}, nil
}

func (m *Manager) Stop(id string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.processes[id]
	if p == nil || p.cmd == nil || p.exited {
		_ = m.nodes.UpdateRuntime(context.Background(), id, "stopped", 0, "", 0)
		return Result{OK: true, State: "stopped"}, nil
	}
	p.stopping = true
	p.state = "stopping"
	m.appendLog(filepath.Join(m.dataDir, "logs", id+".log"), "[运行状态] 已请求停止节点进程")
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	time.AfterFunc(8*time.Second, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if !p.exited {
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	return Result{OK: true, State: "stopping", PID: p.cmd.Process.Pid}, nil
}
func (m *Manager) Restart(id string) (Result, error) {
	_, _ = m.Stop(id)
	deadline := time.Now().Add(9 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		p := m.processes[id]
		done := p == nil || p.exited
		m.mu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return m.Start(id)
}

func (m *Manager) wait(id string, p *process) {
	err := p.cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	p.exited = true
	if current := m.processes[id]; current != p {
		return
	}
	message := ""
	exit := 0
	if err != nil {
		message = runtimelog.Redact(err.Error())
		if value, ok := err.(*exec.ExitError); ok {
			exit = value.ExitCode()
		}
	}
	p.lastError = message
	p.state = "stopped"
	_ = m.nodes.UpdateRuntime(context.Background(), id, "stopped", 0, message, p.restarts)
	logPath := filepath.Join(m.dataDir, "logs", id+".log")
	if message == "" {
		m.appendLog(logPath, "[运行状态] 节点进程已停止")
	} else {
		m.appendLog(logPath, "[运行状态] 节点进程异常退出："+message)
	}
	if p.stopping || m.closed {
		return
	}
	node, nodeErr := m.nodes.Get(context.Background(), id)
	if nodeErr != nil || node.DesiredState != "running" {
		return
	}
	if time.Since(p.started) >= stableRuntimeWindow {
		p.restarts = 0
	}
	if p.restarts >= 6 {
		p.state = "failed"
		_ = m.nodes.UpdateRuntime(context.Background(), id, "failed", 0, message, p.restarts)
		return
	}
	p.restarts++
	delay := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}[p.restarts-1]
	m.appendLog(logPath, fmt.Sprintf("[运行状态] 将在 %s 后自动重启（第 %d 次）", delay, p.restarts))
	go func(restarts int) {
		time.Sleep(delay)
		m.mu.Lock()
		defer m.mu.Unlock()
		if !m.closed {
			delete(m.processes, id)
			_, _ = m.startLockedWithRestarts(id, restarts)
		}
	}(p.restarts)
	_ = exit
}

func (m *Manager) Status() StatusResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := StatusResult{Instances: map[string]Result{}}
	for id, p := range m.processes {
		pid := 0
		if p.cmd != nil && !p.exited {
			pid = p.cmd.Process.Pid
		}
		out.Instances[id] = Result{OK: p.state == "running", State: p.state, PID: pid, Message: p.lastError, RestartCount: p.restarts}
	}
	return out
}
func (m *Manager) Logs(id string, tail int) (LogsResult, error) {
	if tail < 1 || tail > 1000 {
		tail = 200
	}
	path := filepath.Join(m.dataDir, "logs", filepath.Base(id)+".log")
	lines := make([]string, 0, tail)
	var totalBytes int64
	var updated time.Time
	paths := make([]string, 0, maxNodeLogArchives+1)
	for index := maxNodeLogArchives; index >= 1; index-- {
		paths = append(paths, fmt.Sprintf("%s.%d", path, index))
	}
	paths = append(paths, path)
	for _, current := range paths {
		info, statErr := os.Stat(current)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return LogsResult{}, statErr
		}
		totalBytes += info.Size()
		if info.ModTime().After(updated) {
			updated = info.ModTime()
		}
		file, openErr := os.Open(current)
		if openErr != nil {
			return LogsResult{}, openErr
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
			if len(lines) > tail {
				lines = lines[1:]
			}
		}
		scanErr := scanner.Err()
		_ = file.Close()
		if scanErr != nil {
			return LogsResult{}, scanErr
		}
	}
	updatedAt := ""
	if !updated.IsZero() {
		updatedAt = updated.UTC().Format(time.RFC3339)
	}
	return LogsResult{Lines: lines, TotalBytes: totalBytes, UpdatedAt: updatedAt}, nil
}
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	list, _ := m.nodes.List(context.Background())
	for _, kind := range []string{"shadowtls", "snell", "ss2022"} {
		for _, node := range list {
			if node.Type == kind {
				_, _ = m.Stop(node.ID)
			}
		}
	}
}
func (m *Manager) Reconcile() {
	list, err := m.nodes.List(context.Background())
	if err != nil {
		return
	}
	for _, kind := range []string{"snell", "ss2022", "shadowtls"} {
		for _, node := range list {
			if node.Type == kind && node.DesiredState == "running" {
				m.mu.Lock()
				_, managed := m.processes[node.ID]
				m.mu.Unlock()
				if managed {
					continue
				}
				if _, err := m.Apply(node.ID); err != nil {
					_ = m.nodes.UpdateRuntime(context.Background(), node.ID, "failed", 0, runtimelog.Redact(err.Error()), 0)
				}
			}
		}
	}
}

// SampleTraffic reconciles per-port nftables counters with durable monthly
// totals, enforces quotas, and records observable activity in each node log.
func (m *Manager) SampleTraffic() error {
	if m.traffic == nil {
		return nil
	}
	list, err := m.nodes.List(context.Background())
	if err != nil {
		return err
	}
	active := map[string]bool{}
	nodeByID := map[string]nodes.Node{}
	var problems []error
	for _, node := range list {
		if !isPublicListener(node.ListenHost) || node.DesiredState != "running" {
			continue
		}
		active[node.ID] = true
		nodeByID[node.ID] = node
		if err = firewall.EnsureAccounting(node.ID, node.ListenPort, nodeNetworks(node)); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", node.Name, err))
		}
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	if err = firewall.CleanupUnknownAccounting(active); err != nil {
		return err
	}
	raw, err := firewall.ReadAccounting()
	if err != nil {
		return err
	}
	nodeResults := make(map[string]traffic.SampleResult, len(active))
	var projectUpload, projectDownload int64
	for id := range active {
		value := raw[id]
		result, sampleErr := m.traffic.Sample(context.Background(), id, value.UploadBytes, value.DownloadBytes, time.Now())
		if sampleErr != nil {
			problems = append(problems, fmt.Errorf("%s: %w", nodeByID[id].Name, sampleErr))
			continue
		}
		nodeResults[id] = result
		projectUpload += result.DeltaUpload
		projectDownload += result.DeltaDownload
		if result.DeltaUpload > 0 || result.DeltaDownload > 0 {
			m.appendLog(filepath.Join(m.dataDir, "logs", id+".log"), fmt.Sprintf("[流量活动] 本次上传 +%s，下载 +%s；周期累计上传 %s，下载 %s", humanBytes(result.DeltaUpload), humanBytes(result.DeltaDownload), humanBytes(result.TotalUpload), humanBytes(result.TotalDownload)))
		}
		if result.Firewall == "pause" {
			m.appendLog(filepath.Join(m.dataDir, "logs", id+".log"), "[流量限额] 已达到本周期限额，节点已自动暂停")
		} else if result.Firewall == "resume" {
			m.appendLog(filepath.Join(m.dataDir, "logs", id+".log"), "[流量限额] 新计费周期已开始，节点已自动恢复")
		}
	}
	project, projectErr := m.traffic.SampleProject(context.Background(), projectUpload, projectDownload, time.Now())
	if projectErr != nil {
		problems = append(problems, fmt.Errorf("project traffic: %w", projectErr))
	} else {
		for id, node := range nodeByID {
			result, sampled := nodeResults[id]
			if !sampled {
				if counter, counterErr := m.traffic.Get(context.Background(), id); counterErr == nil {
					result.Paused = counter.Paused
				}
			}
			if blockErr := firewall.SetBlocked(id, node.ListenPort, nodeNetworks(node), result.Paused || project.Paused); blockErr != nil {
				problems = append(problems, fmt.Errorf("%s: %w", node.Name, blockErr))
			}
			if project.Firewall == "pause" {
				m.appendLog(filepath.Join(m.dataDir, "logs", id+".log"), "[项目流量限额] 所有公网节点的总流量已达本周期限额，已自动暂停")
			} else if project.Firewall == "resume" {
				m.appendLog(filepath.Join(m.dataDir, "logs", id+".log"), "[项目流量限额] 新计费周期已开始，未单独暂停的节点已恢复")
			}
		}
	}
	return errors.Join(problems...)
}

// SetProjectBlocked reapplies the effective project + per-node policy to every
// public listener. Per-node pauses remain in force when a project pause ends.
func (m *Manager) SetProjectBlocked(blocked bool) (Result, error) {
	list, err := m.nodes.List(context.Background())
	if err != nil {
		return Result{}, err
	}
	counters, err := m.traffic.List(context.Background())
	if err != nil {
		return Result{}, err
	}
	paused := make(map[string]bool, len(counters))
	for _, counter := range counters {
		paused[counter.NodeID] = counter.Paused
	}
	var problems []error
	for _, node := range list {
		if !isPublicListener(node.ListenHost) || node.DesiredState != "running" {
			continue
		}
		if applyErr := firewall.SetBlocked(node.ID, node.ListenPort, nodeNetworks(node), blocked || paused[node.ID]); applyErr != nil {
			problems = append(problems, fmt.Errorf("%s: %w", node.Name, applyErr))
		}
	}
	err = errors.Join(problems...)
	return Result{OK: err == nil, Message: "project traffic policy applied"}, err
}

func (m *Manager) binaryPath(node nodes.Node) string {
	name := map[string]string{"snell": "snell-server", "ss2022": "ssserver", "shadowtls": "shadow-tls"}[node.Type]
	return filepath.Join(m.dataDir, "runtime", runtimeDir(node.Type), node.RuntimeVersion, runtime.GOARCH, name)
}
func (m *Manager) commandArgs(node nodes.Node, config string) ([]string, error) {
	switch node.Type {
	case "snell":
		return []string{"-c", config}, nil
	case "ss2022":
		return []string{"-c", config}, nil
	default:
		backend, err := m.nodes.Get(context.Background(), node.BackendNodeID)
		if err != nil {
			return nil, err
		}
		secret, err := m.nodes.Secret(context.Background(), node.ID)
		if err != nil {
			return nil, err
		}
		backendHost := backend.ListenHost
		if backendHost == "" {
			backendHost = nodes.DefaultListenHost
		}
		if backendHost == "0.0.0.0" || backendHost == "::" {
			backendHost = "127.0.0.1"
		}
		listenHost := node.ListenHost
		if listenHost == "" {
			listenHost = nodes.DefaultListenHost
		}
		args := []string{"--v3", "server", "--listen", net.JoinHostPort(listenHost, strconv.Itoa(node.ListenPort)), "--server", net.JoinHostPort(backendHost, strconv.Itoa(backend.ListenPort)), "--tls", node.Config.SNI, "--password", secret}
		if node.Config.TFO {
			args = append([]string{"--fastopen"}, args...)
		}
		if node.Config.WildcardSNI != "" && node.Config.WildcardSNI != "off" {
			args = append(args, "--wildcard-sni", node.Config.WildcardSNI)
		}
		return args, nil
	}
}
func (m *Manager) validateCandidate(node nodes.Node, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 || info.Size() > 1024*1024 {
		return errors.New("invalid candidate configuration size")
	}
	return nil
}
func (m *Manager) capture(path, source string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		m.appendLog(path, "[运行时"+source+"] "+runtimelog.Redact(scanner.Text()))
	}
}
func (m *Manager) appendLog(path, line string) {
	if !m.logEnabled.Load() {
		return
	}
	m.logMu.Lock()
	defer m.logMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(path), 0750)
	fileLimit := maxNodeLogSize
	if globalLimit := m.logLimit.Load(); globalLimit > 0 && globalLimit/4 < fileLimit {
		fileLimit = max(globalLimit/4, 64*1024)
	}
	if info, err := os.Stat(path); err == nil && info.Size() > fileLimit {
		for i := maxNodeLogArchives - 1; i >= 1; i-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
		}
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err == nil {
		_, _ = fmt.Fprintln(file, time.Now().UTC().Format(time.RFC3339), strings.ReplaceAll(line, "\r", ""))
		_ = file.Close()
	}
}

// MaintainLogs enforces one configurable budget across every current and
// archived node log, deleting the oldest files first.
func (m *Manager) MaintainLogs() (LogMaintenanceResult, error) {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	limit := m.logLimit.Load()
	if m.settings != nil {
		values, err := m.settings.Get(context.Background())
		if err != nil {
			return LogMaintenanceResult{}, err
		}
		limit = int64(values.LogMaxMB) << 20
		m.logLimit.Store(limit)
		m.logEnabled.Store(values.LogEnabled)
	}
	type logFile struct {
		path    string
		size    int64
		modTime time.Time
	}
	entries, err := os.ReadDir(filepath.Join(m.dataDir, "logs"))
	if os.IsNotExist(err) {
		return LogMaintenanceResult{OK: true, LimitBytes: limit}, nil
	}
	if err != nil {
		return LogMaintenanceResult{}, err
	}
	files := make([]logFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return LogMaintenanceResult{}, infoErr
		}
		if !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		files = append(files, logFile{path: filepath.Join(m.dataDir, "logs", entry.Name()), size: info.Size(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(left, right int) bool { return files[left].modTime.Before(files[right].modTime) })
	removed := 0
	for _, file := range files {
		if total <= limit {
			break
		}
		if removeErr := os.Remove(file.path); removeErr != nil && !os.IsNotExist(removeErr) {
			return LogMaintenanceResult{}, removeErr
		}
		total -= file.size
		removed++
	}
	return LogMaintenanceResult{OK: true, LimitBytes: limit, CurrentBytes: total, RemovedFiles: removed}, nil
}

func isPublicListener(host string) bool {
	return host != "127.0.0.1" && host != "::1" && host != "localhost"
}

func nodeNetworks(node nodes.Node) []string {
	if node.Type != "ss2022" {
		return []string{"tcp"}
	}
	switch node.Config.Mode {
	case "udp_only":
		return []string{"udp"}
	case "tcp_and_udp":
		return []string{"tcp", "udp"}
	default:
		return []string{"tcp"}
	}
}

func humanBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	for _, unit := range units {
		amount /= 1024
		if amount < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", amount, unit)
		}
	}
	return fmt.Sprintf("%d B", value)
}
func configDir(kind string) string {
	if kind == "ss2022" {
		return "ss"
	}
	return kind
}
func runtimeDir(kind string) string {
	return map[string]string{"snell": "snell", "ss2022": "shadowsocks-rust", "shadowtls": "shadowtls"}[kind]
}
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

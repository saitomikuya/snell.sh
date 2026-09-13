package anyconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

// UserTraffic is the live byte counter reported by ocserv for one account.
// occtl exposes RX/TX as cumulative interface counters for the current
// session; the traffic store turns those values into durable deltas.
type UserTraffic struct {
	Username      string
	UploadBytes   int64
	DownloadBytes int64
}

type occtlUser struct {
	Username string `json:"Username"`
	Device   string `json:"Device"`
	// occtl's `show users` output exposes session counters as RX/TX. They are
	// encoded as strings by occtl (and may be numbers in older builds).
	RX json.RawMessage `json:"RX"`
	TX json.RawMessage `json:"TX"`
	// Keep accepting the raw_* names used by status output and older wrappers.
	RawRX json.RawMessage `json:"raw_rx"`
	RawTX json.RawMessage `json:"raw_tx"`
}

var occtlUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ListUserTraffic reads all currently connected users from the node's occtl
// socket. A stopped node or an unavailable occtl binary is reported as an
// error so callers can retain the last durable values instead of overwriting
// them with zeros.
func (s *Service) ListUserTraffic(ctx context.Context, node nodes.Node) ([]UserTraffic, error) {
	if node.Type != "anyconnect" {
		return nil, errors.New("节点不是 AnyConnect 类型")
	}
	output, err := s.runOCCTL(ctx, node, "--json", "show", "users")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("读取 AnyConnect 用户流量: %w", err)
	}
	var payload []occtlUser
	if err = json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("解析 occtl 用户流量: %w", err)
	}
	byUser := make(map[string]UserTraffic, len(payload))
	for _, item := range payload {
		username := strings.TrimSpace(item.Username)
		if username == "" || username == "(none)" {
			continue
		}
		upload, err := parseSessionCounter(item, firstCounter(item.RX, item.RawRX), "rx_bytes")
		if err != nil {
			return nil, fmt.Errorf("解析用户 %s 上传流量: %w", username, err)
		}
		download, err := parseSessionCounter(item, firstCounter(item.TX, item.RawTX), "tx_bytes")
		if err != nil {
			return nil, fmt.Errorf("解析用户 %s 下载流量: %w", username, err)
		}
		value := byUser[username]
		value.Username = username
		value.UploadBytes += upload
		value.DownloadBytes += download
		byUser[username] = value
	}
	result := make([]UserTraffic, 0, len(byUser))
	for _, value := range byUser {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Username < result[j].Username })
	return result, nil
}

// firstCounter prefers the fields emitted by `occtl show users`, while
// retaining compatibility with wrappers that expose raw_rx/raw_tx instead.
func firstCounter(primary, fallback json.RawMessage) json.RawMessage {
	if value := strings.TrimSpace(string(primary)); value != "" && value != "null" {
		return primary
	}
	return fallback
}

// parseSessionCounter reads RX/TX when occtl provides them. Builds of ocserv
// compiled without libnl omit those fields, so fall back to the per-session
// TUN interface counters exposed by sysfs.
func parseSessionCounter(item occtlUser, raw json.RawMessage, stat string) (int64, error) {
	if value := strings.TrimSpace(string(raw)); value != "" && value != "null" {
		return parseCounter(raw)
	}
	device := strings.TrimSpace(item.Device)
	if device == "" || device == "(none)" || strings.ContainsAny(device, `/\\`) || device == "." || device == ".." {
		return 0, nil
	}
	base := strings.TrimSpace(os.Getenv("PANEL_NET_STATS_DIR"))
	if base == "" {
		base = "/sys/class/net"
	}
	path := filepath.Join(base, device, "statistics", stat)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}

// TerminateUser disconnects all active sessions for an account and invalidates
// their cookies. It is used when a user reaches its quota; the account remains
// configured and is eligible again when the next billing period starts.
func (s *Service) TerminateUser(ctx context.Context, node nodes.Node, username string) error {
	if node.Type != "anyconnect" {
		return errors.New("节点不是 AnyConnect 类型")
	}
	if !occtlUsernamePattern.MatchString(username) {
		return errors.New("AnyConnect 用户名无效")
	}
	if _, err := s.runOCCTL(ctx, node, "terminate", "user", username); err != nil {
		return fmt.Errorf("断开超限用户 %s: %w", username, err)
	}
	return nil
}

func (s *Service) runOCCTL(ctx context.Context, node nodes.Node, args ...string) ([]byte, error) {
	binary := strings.TrimSpace(os.Getenv("OCCTL_BINARY"))
	if binary == "" {
		binary = "/usr/bin/occtl"
		if _, err := os.Stat(binary); err != nil {
			var lookErr error
			binary, lookErr = exec.LookPath("occtl")
			if lookErr != nil {
				return nil, errors.New("镜像中缺少 occtl，无法读取 AnyConnect 用户流量")
			}
		}
	}
	commandArgs := []string{"--socket-file", filepath.Join(s.RuntimeDir(node.ID), "occtl.socket")}
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, binary, commandArgs...)
	output, err := command.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

func parseCounter(raw json.RawMessage) (int64, error) {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if value == "" || value == "null" {
		return 0, nil
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 {
		if err == nil {
			err = errors.New("counter cannot be negative")
		}
		return 0, err
	}
	return number, nil
}

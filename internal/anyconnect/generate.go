package anyconnect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func (s *Service) PrepareConfig(ctx context.Context, node nodes.Node) (string, error) {
	if node.Type != "anyconnect" {
		return "", errors.New("节点不是 AnyConnect 类型")
	}
	if err := s.Ensure(ctx, node); err != nil {
		return "", err
	}
	users, err := s.nodes.ListAnyConnectUsers(ctx, node.ID)
	if err != nil {
		return "", err
	}
	root := filepath.Join(s.dataDir, "config", "anyconnect")
	if err = os.MkdirAll(root, 0750); err != nil {
		return "", err
	}
	candidate, err := os.MkdirTemp(root, "."+node.ID+".candidate-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(candidate)
	groupDir := filepath.Join(candidate, "group")
	if err = os.MkdirAll(groupDir, 0750); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(groupDir, "FullTunnel"), []byte("# Full tunnel inherits route = default.\ntunnel-all-dns = true\n"), 0640); err != nil {
		return "", err
	}
	chinaRoutes, err := os.ReadFile(s.ChinaRoutesPath(node.ID))
	if err != nil {
		return "", err
	}
	if len(chinaRoutes) == 0 {
		return "", errors.New("中国直连路由列表为空")
	}
	chinaDirect := renderChinaDirectGroup(chinaRoutes, node.Config.ChinaDirectDNS)
	if err = os.WriteFile(filepath.Join(groupDir, "ChinaDirect"), chinaDirect, 0640); err != nil {
		return "", err
	}
	passwordFile, err := s.renderPasswordFile(ctx, users)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(candidate, "ocpasswd"), passwordFile, 0600); err != nil {
		return "", err
	}
	target := filepath.Join(root, node.ID)
	config, err := s.renderConfig(node, target)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(candidate, "ocserv.conf"), config, 0640); err != nil {
		return "", err
	}
	profile, err := profileForNode(node)
	if err != nil {
		return "", err
	}
	lastGood := target + ".last-good"
	_ = os.RemoveAll(lastGood)
	if _, statErr := os.Stat(target); statErr == nil {
		if err = os.Rename(target, lastGood); err != nil {
			return "", err
		}
	}
	if err = os.Rename(candidate, target); err != nil {
		_ = os.Rename(lastGood, target)
		return "", err
	}
	profilePath := filepath.Join(s.runtimeDir(node.ID), "profile.xml")
	if profile == nil {
		_ = os.Remove(profilePath)
	} else {
		profileData, renderErr := renderProfile(profile)
		if renderErr != nil {
			return "", renderErr
		}
		temporary := profilePath + ".tmp"
		if err = os.WriteFile(temporary, profileData, 0644); err != nil {
			return "", err
		}
		if err = os.Rename(temporary, profilePath); err != nil {
			_ = os.Remove(temporary)
			return "", err
		}
	}
	return filepath.Join(target, "ocserv.conf"), nil
}

func (s *Service) RollbackConfig(nodeID string) error {
	target := filepath.Join(s.dataDir, "config", "anyconnect", nodeID)
	lastGood := target + ".last-good"
	if _, err := os.Stat(lastGood); err != nil {
		if os.IsNotExist(err) {
			return os.RemoveAll(target)
		}
		return err
	}
	failed := target + ".failed"
	_ = os.RemoveAll(failed)
	if err := os.Rename(target, failed); err != nil {
		return err
	}
	if err := os.Rename(lastGood, target); err != nil {
		_ = os.Rename(failed, target)
		return err
	}
	_ = os.RemoveAll(failed)
	return nil
}

func (s *Service) Remove(nodeID string) error {
	for _, path := range []string{
		filepath.Join(s.dataDir, "config", "anyconnect", nodeID),
		filepath.Join(s.dataDir, "config", "anyconnect", nodeID+".last-good"),
		filepath.Join(s.dataDir, "anyconnect", "assets", nodeID),
		s.runtimeDir(nodeID),
		// Clean up the location used before AnyConnect runtime sockets were
		// moved outside the private persistent data directory.
		filepath.Join(s.dataDir, "runtime", "anyconnect", nodeID),
	} {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) renderPasswordFile(ctx context.Context, users []nodes.AnyConnectUser) ([]byte, error) {
	var output strings.Builder
	for _, user := range users {
		if !user.Enabled {
			continue
		}
		password, err := s.nodes.AnyConnectUserPassword(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		hash, err := hashPassword(password)
		if err != nil {
			return nil, err
		}
		group := map[string]string{"full": "FullTunnel", "cn": "ChinaDirect", "select": "FullTunnel,ChinaDirect"}[user.RouteGroup]
		if group == "" {
			return nil, fmt.Errorf("用户 %s 的路由组无效", user.Username)
		}
		fmt.Fprintf(&output, "%s:%s:%s\n", user.Username, group, hash)
	}
	return []byte(output.String()), nil
}

func hashPassword(password string) (string, error) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		return "", errors.New("镜像中缺少 openssl，无法生成 AnyConnect 密码文件")
	}
	command := exec.Command(openssl, "passwd", "-6", "-stdin")
	command.Stdin = strings.NewReader(password + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("生成 AnyConnect 密码哈希: %s", strings.TrimSpace(string(output)))
	}
	hash := strings.TrimSpace(string(output))
	if !strings.HasPrefix(hash, "$6$") || strings.ContainsAny(hash, "\r\n:") {
		return "", errors.New("openssl 返回了无效的 SHA-512 crypt 哈希")
	}
	return hash, nil
}

func (s *Service) renderConfig(node nodes.Node, finalConfigDir string) ([]byte, error) {
	prefix, err := netip.ParsePrefix(node.Config.VPNNetwork)
	if err != nil || !prefix.Addr().Is4() {
		return nil, errors.New("VPN 地址池无效")
	}
	mask := net.CIDRMask(prefix.Bits(), 32)
	runtimeDir := s.runtimeDir(node.ID)
	// ocserv creates the sec-mod socket as run-as-user, while this directory is
	// prepared by the root agent. Workers need search permission on every parent
	// directory to reach that socket. Keep directory listings private, but allow
	// the unprivileged worker to traverse the node-specific runtime directory.
	const runtimeDirectoryMode = 0711
	for _, path := range []string{filepath.Dir(runtimeDir), runtimeDir} {
		if err = os.MkdirAll(path, runtimeDirectoryMode); err != nil {
			return nil, err
		}
		// MkdirAll preserves existing modes. Chmod repairs an older runtime root
		// without exposing the persistent /data directory that stores secrets.
		if err = os.Chmod(path, runtimeDirectoryMode); err != nil {
			return nil, err
		}
	}
	deviceID := strings.ReplaceAll(node.ID, "-", "")
	if len(deviceID) > 8 {
		deviceID = deviceID[:8]
	}
	lines := []string{
		`auth = "plain[passwd=` + filepath.Join(finalConfigDir, "ocpasswd") + `]"`,
		"tcp-port = " + fmt.Sprint(node.ListenPort),
		"udp-port = " + fmt.Sprint(node.ListenPort),
		"listen-host = " + node.ListenHost,
		"run-as-user = nobody",
		"run-as-group = nogroup",
		"server-cert = " + s.CertificatePath(node.ID),
		"server-key = " + s.PrivateKeyPath(node.ID),
		"socket-file = " + filepath.Join(runtimeDir, "ocserv.socket"),
		"occtl-socket-file = " + filepath.Join(runtimeDir, "occtl.socket"),
		"pid-file = " + filepath.Join(runtimeDir, "ocserv.pid"),
		"config-per-group = " + filepath.Join(finalConfigDir, "group"),
		"default-group-config = " + filepath.Join(finalConfigDir, "group", "ChinaDirect"),
		"select-group = FullTunnel[Full tunnel]",
		"select-group = ChinaDirect[China direct]",
		"default-select-group = ChinaDirect",
		"auto-select-group = true",
		"device = pp" + deviceID,
		"ipv4-network = " + prefix.Masked().Addr().String(),
		"ipv4-netmask = " + net.IP(mask).String(),
		"route = default",
		"mtu = " + fmt.Sprint(node.Config.MTU),
		"max-clients = " + fmt.Sprint(node.Config.MaxClients),
		"max-same-clients = " + fmt.Sprint(node.Config.MaxSameClients),
		"cisco-client-compat = true",
		"tunnel-all-dns = true",
		"use-occtl = true",
		// The image compiles ocserv with namespace support disabled, so current
		// isolate-workers enables the seccomp-backed unprivileged worker without
		// requiring CAP_SYS_ADMIN.
		"isolate-workers = true",
		"predictable-ips = false",
		"try-mtu-discovery = false",
		"stats-report-time = 60",
	}
	if _, err = profileForNode(node); err != nil {
		return nil, err
	}
	if node.Config.ProfileEnabled {
		lines = append(lines, "user-profile = "+filepath.Join(runtimeDir, "profile.xml"))
	}
	if !node.Config.UDPEnabled {
		lines = append(lines, "no-udp = true")
	}
	for _, dns := range strings.Split(node.Config.DNS, ",") {
		lines = append(lines, "dns = "+strings.TrimSpace(dns))
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func (s *Service) runtimeDir(nodeID string) string {
	root := strings.TrimSpace(os.Getenv("PANEL_ANYCONNECT_RUNTIME_DIR"))
	if root == "" {
		root = "/run/proxy-panel/anyconnect"
	}
	return filepath.Join(root, nodeID)
}

func renderChinaDirectGroup(routes []byte, dnsList string) []byte {
	result := []byte("# Resolve and route excluded destinations through the client's local network.\ntunnel-all-dns = false\n")
	if strings.TrimSpace(dnsList) == "" {
		dnsList = nodes.DefaultChinaDirectDNS
	}
	for _, dns := range strings.Split(dnsList, ",") {
		dns = strings.TrimSpace(dns)
		result = append(result, []byte("dns = "+dns+"\n")...)
		result = append(result, []byte("no-route = "+dns+"/255.255.255.255\n")...)
	}
	return append(result, routes...)
}

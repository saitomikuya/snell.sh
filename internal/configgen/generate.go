package configgen

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func Generate(node nodes.Node, secret string, backend *nodes.Node) ([]byte, error) {
	switch node.Type {
	case "snell":
		lines := []string{"[snell-server]", fmt.Sprintf("listen = %s", net.JoinHostPort(node.ListenHost, fmt.Sprint(node.ListenPort))), "psk = " + secret}
		if node.Config.DNS != "" {
			lines = append(lines, "dns = "+node.Config.DNS)
		}
		lines = append(lines, fmt.Sprintf("ipv6 = %t", node.Config.IPv6))
		return []byte(strings.Join(lines, "\n") + "\n"), nil
	case "ss2022":
		mode := map[string]string{"tcp_and_udp": "tcp_and_udp", "tcp_only": "tcp_only", "udp_only": "udp_only"}[node.Config.Mode]
		value := map[string]any{"server": node.ListenHost, "server_port": node.ListenPort, "method": node.Config.Method, "password": secret, "mode": mode, "fast_open": node.Config.TFO, "user": "nobody", "timeout": 300}
		if node.Config.DNS != "" {
			value["nameserver"] = node.Config.DNS
		}
		if node.Config.Obfs == "http" || node.Config.Obfs == "tls" {
			value["plugin"] = "obfs-server"
			value["plugin_opts"] = "obfs=" + node.Config.Obfs + ";obfs-host=" + node.Config.ObfsHost
		}
		return json.MarshalIndent(value, "", "  ")
	case "shadowtls":
		if backend == nil {
			return nil, fmt.Errorf("ShadowTLS backend is required")
		}
		backendHost := backend.ListenHost
		if backendHost == "0.0.0.0" || backendHost == "::" {
			backendHost = "127.0.0.1"
		}
		value := map[string]any{"listen": net.JoinHostPort(node.ListenHost, fmt.Sprint(node.ListenPort)), "server": net.JoinHostPort(backendHost, fmt.Sprint(backend.ListenPort)), "tls": node.Config.SNI, "password": secret, "wildcard_sni": node.Config.WildcardSNI, "fast_open": node.Config.TFO}
		return json.MarshalIndent(value, "", "  ")
	default:
		return nil, fmt.Errorf("unsupported node type")
	}
}

func Extension(kind string) string {
	if kind == "snell" {
		return ".conf"
	}
	return ".json"
}

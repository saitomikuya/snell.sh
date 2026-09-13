package configgen

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
	qrcode "github.com/skip2/go-qrcode"
)

type ClientConfigs struct {
	Primary               string `json:"primary,omitempty"`
	PrimaryLabel          string `json:"primaryLabel,omitempty"`
	Surge                 string `json:"surge,omitempty"`
	SurgeLabel            string `json:"surgeLabel,omitempty"`
	SurgeAlternative      string `json:"surgeAlternative,omitempty"`
	SurgeAlternativeLabel string `json:"surgeAlternativeLabel,omitempty"`
	SurgeNote             string `json:"surgeNote,omitempty"`
	Clash                 string `json:"clash"`
	ClashInline           string `json:"clashInline,omitempty"`
	QRCode                string `json:"qrCode,omitempty"`
	QRCodeLabel           string `json:"qrCodeLabel,omitempty"`
	ProtocolLabel         string `json:"protocolLabel"`
	ImportNote            string `json:"importNote,omitempty"`
	OpenConnect           string `json:"openConnect,omitempty"`
	OpenConnectLabel      string `json:"openConnectLabel,omitempty"`
	Warning               string `json:"warning,omitempty"`
	Masked                bool   `json:"masked"`
}

func Client(node nodes.Node, secret, publicHost string, backend *nodes.Node, backendSecret string) (ClientConfigs, error) {
	host := publicHost
	if host == "" || net.ParseIP(host) != nil && host == "0.0.0.0" {
		host = node.ListenHost
	}
	address := net.JoinHostPort(host, fmt.Sprint(node.ListenPort))
	var out ClientConfigs
	switch node.Type {
	case "anyconnect":
		address = net.JoinHostPort(node.Config.ServerName, fmt.Sprint(node.ListenPort))
		if node.ListenPort == 443 {
			address = node.Config.ServerName
		}
		endpoint := "https://" + address
		out.ProtocolLabel = "AnyConnect / OpenConnect"
		out.PrimaryLabel = "Cisco Secure Client 服务器地址"
		out.Primary = endpoint
		out.OpenConnectLabel = "OpenConnect 命令"
		out.OpenConnect = "sudo openconnect --protocol=anyconnect --user USERNAME " + endpoint
		out.Clash = "# AnyConnect 不是 Clash/Mihomo 代理节点；请使用 Cisco Secure Client 或 OpenConnect。"
		out.ImportNote = "用户名由面板管理员创建。固定路由组用户会自动使用全隧道或中国直连；“登录时选择”用户可在连接时选择路由组。"
	case "snell":
		version := strings.TrimPrefix(node.Config.Version, "v")
		recommended, alternative := surgeSnellVersions(version)
		out.ProtocolLabel = "Snell " + node.Config.Version
		out.PrimaryLabel = "Snell 分享链接（仅限支持该私有格式的客户端）"
		out.Primary = fmt.Sprintf("snell://%s@%s", url.QueryEscape(secret), address)
		out.SurgeLabel = "Surge [Proxy] 节点行"
		out.Surge = surgeSnellLine(node.Name, host, node.ListenPort, secret, recommended, "", "")
		if alternative != "" {
			out.SurgeAlternativeLabel = "Surge Snell v5 节点行（备用）"
			out.SurgeAlternative = surgeSnellLine(node.Name+" v5", host, node.ListenPort, secret, alternative, "", "")
		}
		out.SurgeNote = "把节点行加入现有 Surge 配置的 [Proxy] 段。"
		out.Clash = "# Clash Meta 不原生支持该 Snell 分享链接；请使用客户端自己的 Snell 配置格式。"
		out.ImportNote = "Snell 没有通用、可验证的扫码导入 URI，因此这里不生成可能误导客户端的二维码。"
	case "ss2022":
		out.ProtocolLabel = "Shadowsocks 2022"
		out.PrimaryLabel = "SIP002 分享链接（Shadowrocket 可扫码）"
		out.Primary = shadowsocksURI(node.Config.Method, secret, address, node.Name, ssPlugin(node.Config.Obfs, node.Config.ObfsHost))
		out.SurgeLabel = "Surge [Proxy] 节点行"
		if surgeSupportsCipher(node.Config.Method) {
			out.Surge = fmt.Sprintf("%s = ss, %s, %d, encrypt-method=%s, password=%s, udp-relay=%t", surgeName(node.Name), host, node.ListenPort, node.Config.Method, surgeQuote(secret), node.Config.Mode != "tcp_only")
			out.SurgeNote = "把节点行加入现有 Surge 配置的 [Proxy] 段。"
		} else {
			out.Surge = "# 当前 Surge 不支持此 Shadowsocks 加密方法。"
			out.SurgeNote = "如需在 Surge 使用，请把加密方法改为 2022-blake3-aes-128-gcm 或 2022-blake3-aes-256-gcm。"
		}
		out.Clash = fmt.Sprintf("proxies:\n  - name: %s\n    type: ss\n    server: %s\n    port: %d\n    cipher: %s\n    password: %s", yamlQuote(node.Name), host, node.ListenPort, node.Config.Method, yamlQuote(secret))
		out.ClashInline = clashInline(clashProxy{Name: node.Name, Type: "ss", Server: host, Port: node.ListenPort, Cipher: node.Config.Method, Password: secret})
	case "shadowtls":
		if backend == nil {
			return out, fmt.Errorf("backend is required")
		}
		if backend.Type == "snell" {
			version := strings.TrimPrefix(backend.Config.Version, "v")
			recommended, alternative := surgeSnellVersions(version)
			out.ProtocolLabel = "ShadowTLS v3 + Snell " + backend.Config.Version
			out.SurgeLabel = "Surge Snell v" + recommended + " 兼容节点行（推荐）"
			out.Surge = surgeSnellLine(node.Name, host, node.ListenPort, backendSecret, recommended, secret, node.Config.SNI)
			if alternative != "" {
				out.SurgeAlternativeLabel = "Surge Snell v" + alternative + " 节点行（备用）"
				out.SurgeAlternative = surgeSnellLine(node.Name+" v"+alternative, host, node.ListenPort, backendSecret, alternative, secret, node.Config.SNI)
			}
			out.SurgeNote = "需要 Surge iOS 5.5.0+ 或 Surge Mac 5.0.3+；把节点行放入现有配置的 [Proxy] 段。"
			out.Clash = fmt.Sprintf("proxies:\n  - name: %s\n    type: snell\n    server: %s\n    port: %d\n    psk: %s\n    version: %s\n    reuse: true\n    obfs-opts:\n      mode: shadow-tls\n      host: %s\n      password: %s\n      version: 3", yamlQuote(node.Name), host, node.ListenPort, yamlQuote(backendSecret), recommended, yamlQuote(node.Config.SNI), yamlQuote(secret))
			out.ClashInline = clashInline(clashProxy{Name: node.Name, Type: "snell", Server: host, Port: node.ListenPort, PSK: backendSecret, Version: recommended, Reuse: true, ObfsOpts: &clashObfs{Mode: "shadow-tls", Host: node.Config.SNI, Password: secret, Version: 3}})
			out.ImportNote = "Shadowrocket 目前没有标准的 Snell + ShadowTLS 扫码 URI。请使用 Surge/Mihomo 配置，或在“SS-2022 ShadowTLS”节点页面扫描可验证的 SS 链接。"
		} else if backend.Type == "ss2022" {
			out.ProtocolLabel = "ShadowTLS v3 + Shadowsocks 2022"
			out.PrimaryLabel = "Shadowrocket：SS-2022 + ShadowTLS 导入链接"
			out.Primary = shadowrocketShadowTLSURI(backend.Config.Method, backendSecret, host, backend.ListenPort, node.ListenPort, node.Config.SNI, secret, node.Name)
			out.SurgeLabel = "Surge [Proxy] 完整组合节点行"
			if surgeSupportsCipher(backend.Config.Method) {
				out.Surge = fmt.Sprintf("%s = ss, %s, %d, encrypt-method=%s, password=%s, shadow-tls-password=%s, shadow-tls-sni=%s, shadow-tls-version=3, udp-relay=%t, udp-port=%d", surgeName(node.Name), host, node.ListenPort, backend.Config.Method, surgeQuote(backendSecret), surgeQuote(secret), node.Config.SNI, backend.Config.Mode != "tcp_only", backend.ListenPort)
				out.SurgeNote = "TCP 经 ShadowTLS 端口连接；udp-port 指向原始 SS 端口。把节点行放入现有配置的 [Proxy] 段。"
			} else {
				out.Surge = "# 当前 Surge 不支持后端使用的 Shadowsocks 加密方法。"
				out.SurgeNote = "如需在 Surge 使用，请把后端加密方法改为 2022-blake3-aes-128-gcm 或 2022-blake3-aes-256-gcm。"
			}
			out.Clash = fmt.Sprintf("proxies:\n  - name: %s\n    type: ss\n    server: %s\n    port: %d\n    cipher: %s\n    password: %s\n    plugin: shadow-tls\n    plugin-opts:\n      host: %s\n      password: %s\n      version: 3", yamlQuote(node.Name), host, node.ListenPort, backend.Config.Method, yamlQuote(backendSecret), yamlQuote(node.Config.SNI), yamlQuote(secret))
			out.ClashInline = clashInline(clashProxy{Name: node.Name, Type: "ss", Server: host, Port: node.ListenPort, Cipher: backend.Config.Method, Password: backendSecret, Plugin: "shadow-tls", PluginOpts: &clashPlugin{Host: node.Config.SNI, Password: secret, Version: 3}})
			out.QRCodeLabel = "Shadowrocket：SS-2022 + ShadowTLS"
			out.Warning = fmt.Sprintf("ShadowTLS 只承载 TCP；UDP 继续使用 SS 后端端口 %d。", backend.ListenPort)
		} else {
			return out, fmt.Errorf("unsupported ShadowTLS backend type")
		}
	default:
		return out, fmt.Errorf("unsupported node type")
	}
	if strings.HasPrefix(out.Primary, "ss://") {
		png, err := qrcode.Encode(out.Primary, qrcode.Medium, 256)
		if err == nil {
			out.QRCode = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			if out.QRCodeLabel == "" {
				out.QRCodeLabel = out.PrimaryLabel
			}
		}
	}
	return out, nil
}

type shadowTLSShareConfig struct {
	Version  string `json:"version"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Address  string `json:"address"`
}

// shadowrocketShadowTLSURI follows the private combined-link format emitted by
// the upstream ss-2022.sh script. The SS endpoint remains the backend port;
// Shadowrocket reads the public ShadowTLS endpoint from the encoded JSON.
func shadowrocketShadowTLSURI(method, password, host string, backendPort, shadowTLSPort int, sni, shadowPassword, name string) string {
	userinfo := base64.StdEncoding.EncodeToString([]byte(method + ":" + password))
	payload, _ := json.Marshal(shadowTLSShareConfig{Version: "3", Password: shadowPassword, Host: sni, Port: strconv.Itoa(shadowTLSPort), Address: host})
	return fmt.Sprintf("ss://%s@%s?shadow-tls=%s#%s", userinfo, net.JoinHostPort(host, strconv.Itoa(backendPort)), base64.StdEncoding.EncodeToString(payload), url.PathEscape(name))
}

type clashObfs struct {
	Mode     string `json:"mode"`
	Host     string `json:"host"`
	Password string `json:"password"`
	Version  int    `json:"version"`
}
type clashPlugin struct {
	Host     string `json:"host"`
	Password string `json:"password"`
	Version  int    `json:"version"`
}
type clashProxy struct {
	Name       string       `json:"name"`
	Type       string       `json:"type"`
	Server     string       `json:"server"`
	Port       int          `json:"port"`
	Cipher     string       `json:"cipher,omitempty"`
	Password   string       `json:"password,omitempty"`
	PSK        string       `json:"psk,omitempty"`
	Version    string       `json:"version,omitempty"`
	Reuse      bool         `json:"reuse,omitempty"`
	ObfsOpts   *clashObfs   `json:"obfs-opts,omitempty"`
	Plugin     string       `json:"plugin,omitempty"`
	PluginOpts *clashPlugin `json:"plugin-opts,omitempty"`
}

func clashInline(proxy clashProxy) string {
	value, _ := json.Marshal(proxy)
	// JSON mappings are valid YAML, and the list marker makes this directly
	// pasteable below an existing Clash/Mihomo `proxies:` key.
	return "- " + string(value)
}

func shadowsocksURI(method, password, address, name, plugin string) string {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	if strings.HasPrefix(method, "2022-") {
		// SIP002 requires AEAD-2022 userinfo to remain plain and percent encoded.
		userinfo = url.QueryEscape(method) + ":" + url.QueryEscape(password)
	}
	path := ""
	if plugin != "" {
		path = "/?plugin=" + url.QueryEscape(plugin)
	}
	return "ss://" + userinfo + "@" + address + path + "#" + url.PathEscape(name)
}

func ssPlugin(obfs, host string) string {
	if obfs == "http" || obfs == "tls" {
		return "obfs-local;obfs=" + obfs + ";obfs-host=" + host
	}
	return ""
}

func surgeSnellVersions(version string) (string, string) {
	if version == "5" {
		return "4", "5"
	}
	return version, ""
}

func surgeSnellLine(name, host string, port int, psk, version, shadowPassword, sni string) string {
	line := fmt.Sprintf("%s = snell, %s, %d, psk=%s, version=%s, reuse=true, tfo=true", surgeName(name), host, port, surgeQuote(psk), version)
	if shadowPassword != "" {
		line += fmt.Sprintf(", shadow-tls-password=%s, shadow-tls-sni=%s, shadow-tls-version=3", surgeQuote(shadowPassword), sni)
	}
	return line
}

func surgeName(name string) string {
	name = strings.NewReplacer(",", "_", "=", "_", "\r", " ", "\n", " ").Replace(strings.TrimSpace(name))
	if name == "" || strings.EqualFold(name, "PROXY") {
		return "ProxyNode"
	}
	return name
}

func surgeQuote(value string) string {
	return strconv.Quote(value)
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}

func surgeSupportsCipher(method string) bool {
	return method == "2022-blake3-aes-128-gcm" || method == "2022-blake3-aes-256-gcm" || method == "aes-128-gcm" || method == "aes-192-gcm" || method == "aes-256-gcm" || method == "chacha20-ietf-poly1305"
}

func Mask(config ClientConfigs) ClientConfigs {
	config.Primary = maskLine(config.Primary)
	config.Surge = maskLine(config.Surge)
	config.SurgeAlternative = maskLine(config.SurgeAlternative)
	config.Clash = maskLine(config.Clash)
	config.ClashInline = maskLine(config.ClashInline)
	config.QRCode = ""
	config.Masked = true
	return config
}

func maskLine(v string) string {
	for _, key := range []string{"psk=", "password=", "password: ", "snell://", "ss://", "shadowtls://"} {
		if idx := strings.Index(strings.ToLower(v), key); idx >= 0 {
			end := strings.IndexAny(v[idx+len(key):], ", \n&@")
			if end < 0 {
				end = len(v) - idx - len(key)
			}
			start := idx + len(key)
			v = v[:start] + "••••••••" + v[start+end:]
		}
	}
	return v
}

package nodes

import (
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var namePattern = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,64}$`)
var hostnamePattern = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var scheduleTimePattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

const (
	DefaultChinaCIDRSource = "https://ftp.apnic.net/stats/apnic/delegated-apnic-latest"
	DefaultChinaDirectDNS  = "223.5.5.5,119.29.29.29"
)

func Normalize(req *CreateRequest) {
	if req.ListenHost == "" {
		req.ListenHost = DefaultListenHost
	}
	if req.Type != "anyconnect" {
		return
	}
	if req.RuntimeVersion == "" {
		req.RuntimeVersion = "v1.5.0"
	}
	if req.Config.VPNNetwork == "" {
		req.Config.VPNNetwork = "192.168.144.0/24"
	}
	if req.Config.DNS == "" {
		req.Config.DNS = "1.1.1.1,8.8.8.8"
	}
	if req.Config.MTU == 0 {
		req.Config.MTU = 1340
	}
	if req.Config.MaxClients == 0 {
		req.Config.MaxClients = 32
	}
	if req.Config.MaxSameClients == 0 {
		req.Config.MaxSameClients = 2
	}
	if req.Config.CertificateSchedule == "" {
		req.Config.CertificateSchedule = "daily"
	}
	if req.Config.CertificateScheduleTime == "" {
		req.Config.CertificateScheduleTime = "03:30"
	}
	normalizeAnyConnectRouting(&req.Config)
	if req.Config.ChinaCIDRSchedule == "" {
		req.Config.ChinaCIDRSchedule = "daily"
	}
	if req.Config.ChinaCIDRScheduleTime == "" {
		req.Config.ChinaCIDRScheduleTime = "04:10"
	}
}

func NormalizeUpdate(kind string, req *UpdateRequest) {
	create := CreateRequest{Type: kind, RuntimeVersion: req.RuntimeVersion, ListenHost: req.ListenHost, ListenPort: req.ListenPort, Config: req.Config}
	Normalize(&create)
	req.RuntimeVersion = create.RuntimeVersion
	req.ListenHost = create.ListenHost
	req.Config = create.Config
}

func Validate(req CreateRequest) error {
	Normalize(&req)
	if !utf8.ValidString(req.Name) || !namePattern.MatchString(req.Name) {
		return errors.New("节点名称必须为 1–64 个可见字符")
	}
	if req.Type != "snell" && req.Type != "ss2022" && req.Type != "shadowtls" && req.Type != "anyconnect" {
		return errors.New("不支持的节点类型")
	}
	if net.ParseIP(req.ListenHost) == nil {
		return errors.New("监听地址必须是 IP 地址")
	}
	if req.ListenPort < 1 || req.ListenPort > 65535 {
		return errors.New("端口范围必须为 1–65535")
	}
	switch req.Type {
	case "snell":
		if req.Config.Version != "v4" && req.Config.Version != "v5" && req.Config.Version != "v6" {
			return errors.New("Snell 版本必须为 v4、v5 或 v6")
		}
	case "ss2022":
		if req.Config.Mode != "tcp_and_udp" && req.Config.Mode != "tcp_only" && req.Config.Mode != "udp_only" {
			return errors.New("无效的 SS 模式")
		}
		allowed := map[string]bool{"aes-128-gcm": true, "aes-256-gcm": true, "chacha20-ietf-poly1305": true, "plain": true, "none": true, "table": true, "aes-128-cfb": true, "aes-256-cfb": true, "aes-256-ctr": true, "camellia-256-cfb": true, "rc4-md5": true, "arc4-md5": true, "chacha20-ietf": true, "2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true, "2022-blake3-chacha8-poly1305": true}
		if !allowed[req.Config.Method] {
			return errors.New("不支持的 SS 加密方法")
		}
		if req.Config.Obfs != "" && req.Config.Obfs != "off" && req.Config.Obfs != "http" && req.Config.Obfs != "tls" {
			return errors.New("无效的 simple-obfs 模式")
		}
		if (req.Config.Obfs == "http" || req.Config.Obfs == "tls") && !hostnamePattern.MatchString(req.Config.ObfsHost) {
			return errors.New("无效的 obfs host")
		}
		if strings.HasPrefix(req.Config.Method, "2022-") && req.Secret != "" {
			if err := ValidateSS2022Key(req.Config.Method, req.Secret); err != nil {
				return err
			}
		}
	case "shadowtls":
		if req.BackendNodeID == "" {
			return errors.New("ShadowTLS 必须绑定后端节点")
		}
		if !hostnamePattern.MatchString(req.Config.SNI) {
			return errors.New("无效的 SNI")
		}
		if req.Config.WildcardSNI != "off" && req.Config.WildcardSNI != "authed" && req.Config.WildcardSNI != "all" {
			return errors.New("无效的 wildcard-sni")
		}
	case "anyconnect":
		if req.RuntimeVersion != "v1.5.0" {
			return errors.New("当前仅支持 ocserv v1.5.0")
		}
		if !hostnamePattern.MatchString(req.Config.ServerName) {
			return errors.New("AnyConnect 服务器域名无效")
		}
		prefix, err := netip.ParsePrefix(req.Config.VPNNetwork)
		if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.Bits() < 16 || prefix.Bits() > 29 || !prefix.Addr().IsPrivate() {
			return errors.New("VPN 地址池必须是 /16–/29 的规范私有 IPv4 CIDR")
		}
		for _, item := range []struct {
			value string
			label string
		}{{req.Config.DNS, "DNS"}, {req.Config.ChinaDirectDNS, "中国直连 DNS"}} {
			dnsValues := strings.Split(item.value, ",")
			if len(dnsValues) < 1 || len(dnsValues) > 3 {
				return errors.New(item.label + " 服务器必须配置 1–3 个 IP 地址")
			}
			for _, value := range dnsValues {
				parsed := net.ParseIP(strings.TrimSpace(value))
				if parsed == nil {
					return errors.New(item.label + " 服务器必须是有效 IP 地址")
				}
				if item.label == "中国直连 DNS" && parsed.To4() == nil {
					return errors.New(item.label + " 服务器必须是 IPv4 地址")
				}
			}
		}
		if req.Config.MTU < 1200 || req.Config.MTU > 1500 {
			return errors.New("MTU 范围必须为 1200–1500")
		}
		if req.Config.MaxClients < 1 || req.Config.MaxClients > 4096 {
			return errors.New("最大客户端数范围必须为 1–4096")
		}
		if req.Config.MaxSameClients < 1 || req.Config.MaxSameClients > 64 || req.Config.MaxSameClients > req.Config.MaxClients {
			return errors.New("单用户连接数范围必须为 1–64，且不能超过最大客户端数")
		}
		if err := validateHTTPSURL(req.Config.CertificateURL, "证书下载地址"); err != nil {
			return err
		}
		if err := validateHTTPSURL(req.Config.PrivateKeyURL, "私钥下载地址"); err != nil {
			return err
		}
		if len(req.Config.CertificateUsername) > 128 || strings.ContainsAny(req.Config.CertificateUsername, ":\r\n") {
			return errors.New("证书下载用户名无效")
		}
		for _, credential := range []struct {
			value string
			label string
		}{{req.CertificatePassword, "证书下载密码"}, {req.PrivateKeyPassphrase, "私钥口令"}} {
			if !utf8.ValidString(credential.value) || len(credential.value) > 1024 || strings.ContainsAny(credential.value, "\x00\r\n") {
				return errors.New(credential.label + "不能超过 1024 字节，且不能包含 NUL 或换行")
			}
		}
		if err := validateSchedule(req.Config.CertificateSchedule, req.Config.CertificateScheduleDay, req.Config.CertificateScheduleTime, "证书"); err != nil {
			return err
		}
		if err := validateHTTPSURL(req.Config.ChinaCIDRSourceURL, "中国 CIDR 数据源"); err != nil {
			return err
		}
		if req.Config.ChinaCIDRSourceFormat != "apnic" && req.Config.ChinaCIDRSourceFormat != "cidr" {
			return errors.New("中国 CIDR 数据格式必须为 apnic 或 cidr")
		}
		if err := validateSchedule(req.Config.ChinaCIDRSchedule, req.Config.ChinaCIDRScheduleDay, req.Config.ChinaCIDRScheduleTime, "中国 CIDR"); err != nil {
			return err
		}
	}
	return nil
}

func normalizeAnyConnectRouting(config *Config) {
	if config.ChinaDirectDNS == "" {
		config.ChinaDirectDNS = DefaultChinaDirectDNS
	}
	if config.ChinaCIDRSourceURL == "" {
		config.ChinaCIDRSourceURL = DefaultChinaCIDRSource
	}
	if config.ChinaCIDRSourceFormat == "" {
		config.ChinaCIDRSourceFormat = "apnic"
	}
}

func validateHTTPSURL(raw, label string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New(label + "必须是无内嵌凭据的 HTTPS URL")
	}
	return nil
}

func validateSchedule(frequency string, weekday int, at, label string) error {
	if frequency != "manual" && frequency != "daily" && frequency != "weekly" {
		return errors.New(label + "更新频率无效")
	}
	if frequency != "manual" && !scheduleTimePattern.MatchString(at) {
		return errors.New(label + "更新时间必须为 HH:MM")
	}
	if frequency == "weekly" && (weekday < 0 || weekday > 6) {
		return errors.New(label + "每周更新时间必须选择星期")
	}
	return nil
}

func ValidateSS2022Key(method, key string) error {
	expected := map[string]int{"2022-blake3-aes-128-gcm": 16, "2022-blake3-aes-256-gcm": 32, "2022-blake3-chacha20-poly1305": 32, "2022-blake3-chacha8-poly1305": 32}[method]
	if expected == 0 {
		return errors.New("不支持的 SS-2022 加密方法")
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != expected {
		return errors.New("SS-2022 密钥的 Base64 解码长度不正确")
	}
	return nil
}

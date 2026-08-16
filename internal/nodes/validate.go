package nodes

import (
	"encoding/base64"
	"errors"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

var namePattern = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,64}$`)
var hostnamePattern = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func Validate(req CreateRequest) error {
	if !utf8.ValidString(req.Name) || !namePattern.MatchString(req.Name) {
		return errors.New("节点名称必须为 1–64 个可见字符")
	}
	if req.Type != "snell" && req.Type != "ss2022" && req.Type != "shadowtls" {
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

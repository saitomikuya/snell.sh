package anyconnect

import (
	"errors"
	"fmt"
	"html"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

const maxProfileEntries = 64

var profileHostnamePattern = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type ProfileEntry struct {
	Name    string
	Address string
}

// ParseProfileEntries parses the compact panel format: one “display name | host:port”
// entry per line. Blank lines and lines beginning with # are ignored.
func ParseProfileEntries(raw string) ([]ProfileEntry, error) {
	if len(raw) > 16*1024 {
		return nil, errors.New("profile.xml 节点列表不能超过 16 KiB")
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	entries := make([]ProfileEntry, 0, len(lines))
	seen := map[string]struct{}{}
	for lineNumber, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("profile.xml 第 %d 行必须使用“名称 | 域名:端口”格式", lineNumber+1)
		}
		name, address := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if err := validateProfileName(name); err != nil {
			return nil, fmt.Errorf("profile.xml 第 %d 行名称无效：%w", lineNumber+1, err)
		}
		if err := validateProfileAddress(address); err != nil {
			return nil, fmt.Errorf("profile.xml 第 %d 行地址无效：%w", lineNumber+1, err)
		}
		key := strings.ToLower(address)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("profile.xml 第 %d 行地址重复", lineNumber+1)
		}
		seen[key] = struct{}{}
		entries = append(entries, ProfileEntry{Name: name, Address: address})
		if len(entries) > maxProfileEntries {
			return nil, fmt.Errorf("profile.xml 最多支持 %d 个节点", maxProfileEntries)
		}
	}
	return entries, nil
}

func profileForNode(node nodes.Node) ([]ProfileEntry, error) {
	if !node.Config.ProfileEnabled {
		return nil, nil
	}
	entries, err := ParseProfileEntries(node.Config.ProfileEntries)
	if err != nil {
		return nil, err
	}
	current := net.JoinHostPort(node.Config.ServerName, strconv.Itoa(node.ListenPort))
	for _, entry := range entries {
		if strings.EqualFold(entry.Address, current) {
			return entries, nil
		}
	}
	if len(entries) >= maxProfileEntries {
		return nil, fmt.Errorf("当前节点未在列表中时，profile.xml 最多支持 %d 个节点", maxProfileEntries)
	}
	currentName := node.Name
	if validateProfileName(currentName) != nil {
		currentName = "当前节点"
	}
	// Keep the configured order intact: Cisco Secure Client uses the first
	// HostEntry as the default choice. If the current node is not in the shared
	// list, append it so it remains available without overriding that default.
	return append(entries, ProfileEntry{Name: currentName, Address: current}), nil
}

// RenderProfileXML builds the profile.xml served to Cisco Secure Client. It is
// also exposed for authenticated manual downloads from the management panel.
func RenderProfileXML(node nodes.Node) ([]byte, error) {
	entries, err := profileForNode(node)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, errors.New("AnyConnect 节点未启用 profile.xml")
	}
	return renderProfile(entries)
}

func renderProfile(entries []ProfileEntry) ([]byte, error) {
	var output strings.Builder
	output.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	output.WriteString("<AnyConnectProfile xmlns=\"http://schemas.xmlsoap.org/encoding/\" xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xsi:schemaLocation=\"http://schemas.xmlsoap.org/encoding/ AnyConnectProfile.xsd\">\n  <ServerList>\n")
	for _, entry := range entries {
		fmt.Fprintf(&output, "    <HostEntry><HostName>%s</HostName><HostAddress>%s</HostAddress></HostEntry>\n", html.EscapeString(entry.Name), html.EscapeString(entry.Address))
	}
	output.WriteString("  </ServerList>\n</AnyConnectProfile>\n")
	return []byte(output.String()), nil
}

func validateProfileName(value string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 64 {
		return errors.New("名称长度必须为 1–64 个字符")
	}
	if strings.ContainsAny(value, "|\r\n\x00") {
		return errors.New("名称不能包含 | 或换行")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errors.New("名称不能包含控制字符")
		}
	}
	return nil
}

func validateProfileAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return errors.New("必须是 host:port 或 [IPv6]:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("端口必须在 1–65535 范围内")
	}
	if net.ParseIP(host) == nil && !profileHostnamePattern.MatchString(host) {
		return errors.New("主机名或 IP 地址无效")
	}
	return nil
}

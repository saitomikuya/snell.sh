package firewall

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const TableName = "proxy_panel"

var nftMu sync.Mutex

type Counters struct {
	UploadBytes   int64
	DownloadBytes int64
}

type taggedRule struct {
	Comment string
	Handle  string
}

// Cleanup removes only the table exclusively owned by Proxy Panel.
func Cleanup() error {
	nftMu.Lock()
	defer nftMu.Unlock()
	if runtime.GOOS != "linux" {
		return errors.New("firewall management is only available on Linux")
	}
	path, err := exec.LookPath("nft")
	if err != nil {
		return errors.New("nftables is not installed")
	}
	cmd := exec.Command(path, "delete", "table", "inet", TableName)
	if output, err := cmd.CombinedOutput(); err != nil {
		if len(output) > 0 {
			return errors.New(string(output))
		}
		return err
	}
	return nil
}

func SetBlocked(nodeID string, port int, networks []string, blocked bool) error {
	nftMu.Lock()
	defer nftMu.Unlock()
	if err := validate(nodeID, port, networks); err != nil {
		return err
	}
	if err := ensure(); err != nil {
		return err
	}
	// Remove rules created by panel versions that used one shared comment.
	if err := deleteTagged("input", "proxy-panel:"+nodeID+":blocked"); err != nil {
		return err
	}
	prefix := "proxy-panel:" + nodeID + ":blocked:"
	desired := map[string][]string{}
	if blocked {
		for _, network := range networks {
			tag := fmt.Sprintf("%s%s:%d", prefix, network, port)
			desired[tag] = []string{"add", "rule", "inet", TableName, "input", network, "dport", strconv.Itoa(port), "counter", "reject", "comment", quote(tag)}
		}
	}
	return syncTagged("input", prefix, desired)
}

// EnsureAccounting installs stable anonymous counters for public traffic. The
// loopback exclusion prevents ShadowTLS-to-backend traffic being counted twice.
func EnsureAccounting(nodeID string, port int, networks []string) error {
	nftMu.Lock()
	defer nftMu.Unlock()
	if err := validate(nodeID, port, networks); err != nil {
		return err
	}
	if err := ensure(); err != nil {
		return err
	}
	prefix := "proxy-panel:" + nodeID + ":traffic:"
	for _, item := range []struct {
		chain     string
		direction string
		ifname    string
		portName  string
	}{
		{chain: "input", direction: "upload", ifname: "iifname", portName: "dport"},
		{chain: "output", direction: "download", ifname: "oifname", portName: "sport"},
	} {
		desired := make(map[string][]string, len(networks))
		for _, network := range networks {
			tag := fmt.Sprintf("%s%s:%s:%d", prefix, item.direction, network, port)
			desired[tag] = []string{"add", "rule", "inet", TableName, item.chain, item.ifname, "!=", "lo", network, item.portName, strconv.Itoa(port), "counter", "comment", quote(tag)}
		}
		if err := syncTagged(item.chain, prefix+item.direction+":", desired); err != nil {
			return err
		}
	}
	return nil
}

func RemoveAccounting(nodeID string) error {
	nftMu.Lock()
	defer nftMu.Unlock()
	if nodeID == "" || strings.ContainsAny(nodeID, ":\r\n") {
		return errors.New("invalid node id")
	}
	if err := ensure(); err != nil {
		return err
	}
	prefix := "proxy-panel:" + nodeID + ":traffic:"
	for _, chain := range []string{"input", "output"} {
		rules, err := listTagged(chain, prefix)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			if err = deleteHandle(chain, rule.Handle); err != nil {
				return err
			}
		}
	}
	return nil
}

// EnsureVPN adds only rules owned by this project. It does not change global
// forwarding sysctls, default policies, UFW/firewalld state, or Docker chains.
func EnsureVPN(nodeID, network string) error {
	nftMu.Lock()
	defer nftMu.Unlock()
	if nodeID == "" || strings.ContainsAny(nodeID, ":\r\n") {
		return errors.New("invalid node id")
	}
	if _, err := netip.ParsePrefix(network); err != nil {
		return errors.New("invalid VPN network")
	}
	if err := ensureVPN(); err != nil {
		return err
	}
	prefix := "proxy-panel:" + nodeID + ":vpn:"
	desiredForward := map[string][]string{
		prefix + "out": {"add", "rule", "inet", TableName, "forward", "ip", "saddr", network, "counter", "accept", "comment", quote(prefix + "out")},
		prefix + "in":  {"add", "rule", "inet", TableName, "forward", "ip", "daddr", network, "ct", "state", "established,related", "counter", "accept", "comment", quote(prefix + "in")},
	}
	if err := syncTagged("forward", prefix, desiredForward); err != nil {
		return err
	}
	desiredNAT := map[string][]string{
		prefix + "nat": {"add", "rule", "inet", TableName, "postrouting", "ip", "saddr", network, "counter", "masquerade", "comment", quote(prefix + "nat")},
	}
	return syncTagged("postrouting", prefix, desiredNAT)
}

func RemoveVPN(nodeID string) error {
	nftMu.Lock()
	defer nftMu.Unlock()
	if nodeID == "" || strings.ContainsAny(nodeID, ":\r\n") {
		return errors.New("invalid node id")
	}
	if err := ensureVPN(); err != nil {
		return err
	}
	prefix := "proxy-panel:" + nodeID + ":vpn:"
	for _, chain := range []string{"forward", "postrouting"} {
		rules, err := listTagged(chain, prefix)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			if err = deleteHandle(chain, rule.Handle); err != nil {
				return err
			}
		}
	}
	return nil
}

// CleanupUnknownAccounting removes counters belonging to deleted nodes while
// preserving blocking rules and every firewall table not owned by this panel.
func CleanupUnknownAccounting(active map[string]bool) error {
	nftMu.Lock()
	defer nftMu.Unlock()
	if err := ensure(); err != nil {
		return err
	}
	for _, chain := range []string{"input", "output"} {
		rules, err := listTagged(chain, "proxy-panel:")
		if err != nil {
			return err
		}
		for _, rule := range rules {
			parts := strings.Split(rule.Comment, ":")
			if len(parts) != 6 || parts[0] != "proxy-panel" || parts[2] != "traffic" || active[parts[1]] {
				continue
			}
			if err = deleteHandle(chain, rule.Handle); err != nil {
				return err
			}
		}
	}
	return nil
}

func ReadAccounting() (map[string]Counters, error) {
	nftMu.Lock()
	defer nftMu.Unlock()
	if runtime.GOOS != "linux" {
		return nil, errors.New("firewall management is only available on Linux")
	}
	path, err := exec.LookPath("nft")
	if err != nil {
		return nil, errors.New("nftables is not installed")
	}
	output, err := exec.Command(path, "-j", "list", "table", "inet", TableName).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("read nftables counters: %s", strings.TrimSpace(string(output)))
	}
	return ParseAccountingJSON(output)
}

func ParseAccountingJSON(input []byte) (map[string]Counters, error) {
	var document struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(input, &document); err != nil {
		return nil, fmt.Errorf("decode nftables counters: %w", err)
	}
	result := map[string]Counters{}
	for _, item := range document.NFTables {
		raw, ok := item["rule"]
		if !ok {
			continue
		}
		var rule struct {
			Comment string                       `json:"comment"`
			Expr    []map[string]json.RawMessage `json:"expr"`
		}
		if err := json.Unmarshal(raw, &rule); err != nil {
			return nil, fmt.Errorf("decode nftables rule: %w", err)
		}
		parts := strings.Split(rule.Comment, ":")
		if len(parts) != 6 || parts[0] != "proxy-panel" || parts[2] != "traffic" || (parts[3] != "upload" && parts[3] != "download") {
			continue
		}
		var value int64
		for _, expression := range rule.Expr {
			counterRaw, ok := expression["counter"]
			if !ok {
				continue
			}
			var counter struct {
				Bytes int64 `json:"bytes"`
			}
			if err := json.Unmarshal(counterRaw, &counter); err != nil {
				return nil, fmt.Errorf("decode nftables byte counter: %w", err)
			}
			value += counter.Bytes
		}
		current := result[parts[1]]
		if parts[3] == "upload" {
			current.UploadBytes += value
		} else {
			current.DownloadBytes += value
		}
		result[parts[1]] = current
	}
	return result, nil
}

func validate(nodeID string, port int, networks []string) error {
	if runtime.GOOS != "linux" {
		return errors.New("firewall management is only available on Linux")
	}
	if nodeID == "" || strings.ContainsAny(nodeID, ":\r\n") {
		return errors.New("invalid node id")
	}
	if port < 1 || port > 65535 {
		return errors.New("invalid port")
	}
	if len(networks) == 0 {
		return errors.New("at least one network is required")
	}
	for _, network := range networks {
		if network != "tcp" && network != "udp" {
			return errors.New("invalid network")
		}
	}
	return nil
}

func ensure() error {
	if err := run("add", "table", "inet", TableName); err != nil && !strings.Contains(err.Error(), "File exists") {
		return err
	}
	for _, chain := range []string{"input", "output"} {
		args := []string{"add", "chain", "inet", TableName, chain, "{", "type", "filter", "hook", chain, "priority", "-5", ";", "policy", "accept", ";", "}"}
		if err := run(args...); err != nil && !strings.Contains(err.Error(), "File exists") {
			return err
		}
	}
	return nil
}

func ensureVPN() error {
	if err := ensure(); err != nil {
		return err
	}
	chains := [][]string{
		{"add", "chain", "inet", TableName, "forward", "{", "type", "filter", "hook", "forward", "priority", "-5", ";", "policy", "accept", ";", "}"},
		{"add", "chain", "inet", TableName, "postrouting", "{", "type", "nat", "hook", "postrouting", "priority", "srcnat", ";", "policy", "accept", ";", "}"},
	}
	for _, args := range chains {
		if err := run(args...); err != nil && !strings.Contains(err.Error(), "File exists") {
			return err
		}
	}
	return nil
}

func syncTagged(chain, prefix string, desired map[string][]string) error {
	rules, err := listTagged(chain, prefix)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, rule := range rules {
		if _, ok := desired[rule.Comment]; ok && !existing[rule.Comment] {
			existing[rule.Comment] = true
			continue
		}
		if err = deleteHandle(chain, rule.Handle); err != nil {
			return err
		}
	}
	for tag, args := range desired {
		if !existing[tag] {
			if err = run(args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteTagged(chain, tag string) error {
	rules, err := listTagged(chain, tag)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.Comment == tag {
			if err = deleteHandle(chain, rule.Handle); err != nil {
				return err
			}
		}
	}
	return nil
}

func listTagged(chain, prefix string) ([]taggedRule, error) {
	path, err := exec.LookPath("nft")
	if err != nil {
		return nil, errors.New("nftables is not installed")
	}
	output, err := exec.Command(path, "-a", "list", "chain", "inet", TableName, chain).CombinedOutput()
	if err != nil {
		return nil, errors.New(strings.TrimSpace(string(output)))
	}
	var rules []taggedRule
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		commentStart := strings.Index(line, `comment "`)
		if commentStart < 0 {
			continue
		}
		commentStart += len(`comment "`)
		commentEnd := strings.Index(line[commentStart:], `"`)
		if commentEnd < 0 {
			continue
		}
		comment := line[commentStart : commentStart+commentEnd]
		if !strings.HasPrefix(comment, prefix) {
			continue
		}
		fields := strings.Fields(line)
		for index, field := range fields {
			if field == "handle" && index+1 < len(fields) {
				handle := strings.Trim(fields[index+1], "; ")
				if _, err = strconv.Atoi(handle); err != nil {
					return nil, errors.New("invalid nft handle")
				}
				rules = append(rules, taggedRule{Comment: comment, Handle: handle})
				break
			}
		}
	}
	return rules, scanner.Err()
}

func deleteHandle(chain, handle string) error {
	return run("delete", "rule", "inet", TableName, chain, "handle", handle)
}

// nft parses argv as rule-language tokens rather than treating every argv item
// as an already quoted value. Keep the comment quoted so ':' remains data.
func quote(value string) string {
	return `"` + value + `"`
}

func run(args ...string) error {
	path, err := exec.LookPath("nft")
	if err != nil {
		return errors.New("nftables is not installed")
	}
	output, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft %s: %s", args[0], strings.TrimSpace(string(output)))
	}
	return nil
}

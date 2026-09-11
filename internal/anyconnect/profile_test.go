package anyconnect

import (
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestParseProfileEntries(t *testing.T) {
	entries, err := ParseProfileEntries("# comment\n香港 Azure | azurehk.example.com:443\n\n日本 | jp.example.com:8443\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "香港 Azure" || entries[1].Address != "jp.example.com:8443" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	for _, raw := range []string{"missing separator", "bad.example:0", "bad.example:65536", "bad host:443", "a.example:443\na.example:443"} {
		if _, err := ParseProfileEntries(raw); err == nil {
			t.Fatalf("expected invalid profile entry: %q", raw)
		}
	}
}

func TestProfileForNodeIncludesCurrentNode(t *testing.T) {
	node := nodes.Node{ID: "node", Name: "当前香港", ListenPort: 443, Config: nodes.Config{ServerName: "hk.example.com", ProfileEnabled: true, ProfileEntries: "日本 | jp.example.com:443"}}
	entries, err := profileForNode(node)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Address != "hk.example.com:443" {
		t.Fatalf("current node was not prepended: %#v", entries)
	}
	data, err := renderProfile(entries)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "xsi:schemaLocation=\"http://schemas.xmlsoap.org/encoding/ AnyConnectProfile.xsd\"") || !strings.Contains(text, "<HostName>当前香港</HostName>") || !strings.Contains(text, "<HostAddress>jp.example.com:443</HostAddress>") {
		t.Fatalf("profile xml missing entries: %s", text)
	}
}

func TestRenderConfigReferencesProfile(t *testing.T) {
	t.Setenv("PANEL_ANYCONNECT_RUNTIME_DIR", t.TempDir())
	service := &Service{dataDir: t.TempDir()}
	node := nodes.Node{ID: "node", Type: "anyconnect", ListenHost: "0.0.0.0", ListenPort: 443, Config: nodes.Config{ServerName: "hk.example.com", VPNNetwork: "192.168.144.0/24", DNS: "1.1.1.1", ChinaDirectDNS: "223.5.5.5", MTU: 1340, MaxClients: 32, MaxSameClients: 2, ProfileEnabled: true}}
	config, err := service.renderConfig(node, "/data/config/anyconnect/node")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "user-profile = "+service.runtimeDir(node.ID)+"/profile.xml") {
		t.Fatalf("profile path missing from ocserv config: %s", config)
	}
}

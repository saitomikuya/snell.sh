package nodesync

import (
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestEncodeDecode(t *testing.T) {
	original := New([]Node{{ID: "n1", Type: "anyconnect", Name: "香港", ListenHost: "0.0.0.0", ListenPort: 443, Config: nodes.Config{ServerName: "hk.example.com"}, AnyConnectSecrets: &nodes.AnyConnectSecrets{CertificatePassword: "secret"}, Users: []User{{Username: "alice", Password: "pass", RouteGroup: "cn", Enabled: true}}}})
	raw, err := Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Nodes[0].Users[0].Password != "pass" || !strings.Contains(string(raw), "proxy-panel-sync-v1") {
		t.Fatalf("round trip failed: %#v", decoded)
	}
}

func TestDecodeRejectsUnsupportedFormat(t *testing.T) {
	if _, err := Decode([]byte(`{"format":"other","version":1,"nodes":[]}`)); err == nil {
		t.Fatal("expected format error")
	}
}

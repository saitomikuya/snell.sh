package nodesync

import (
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestEncodeDecode(t *testing.T) {
	original := New([]Node{{ID: "n1", Type: "anyconnect", Name: "香港", ListenHost: "0.0.0.0", ListenPort: 443, Config: nodes.Config{ServerName: "hk.example.com"}, AnyConnectSecrets: &nodes.AnyConnectSecrets{CertificatePassword: "secret"}, Users: []User{{Username: "alice", Password: "pass", RouteGroup: "cn", Enabled: true, Quota: &UserQuota{QuotaBytes: 20 * 1024 * 1024 * 1024, ResetDay: 5}}}}})
	raw, err := Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Nodes[0].Users[0].Password != "pass" || decoded.Nodes[0].Users[0].Quota == nil || decoded.Nodes[0].Users[0].Quota.QuotaBytes != 20*1024*1024*1024 || decoded.Nodes[0].Users[0].Quota.ResetDay != 5 || !strings.Contains(string(raw), "proxy-panel-sync-v1") {
		t.Fatalf("round trip failed: %#v", decoded)
	}
}

func TestValidateRejectsInvalidUserQuota(t *testing.T) {
	base := New([]Node{{ID: "n1", Type: "anyconnect", Name: "香港", ListenHost: "0.0.0.0", ListenPort: 443, Config: nodes.Config{ServerName: "hk.example.com"}, AnyConnectSecrets: &nodes.AnyConnectSecrets{CertificatePassword: "secret"}, Users: []User{{Username: "alice", Password: "pass", RouteGroup: "cn", Enabled: true, Quota: &UserQuota{QuotaBytes: -1, ResetDay: 5}}}}})
	if err := Validate(base); err == nil {
		t.Fatal("expected negative user quota to be rejected")
	}
	base.Nodes[0].Users[0].Quota = &UserQuota{QuotaBytes: 1, ResetDay: 29}
	if err := Validate(base); err == nil {
		t.Fatal("expected out-of-range user reset day to be rejected")
	}
}

func TestDecodeRejectsUnsupportedFormat(t *testing.T) {
	if _, err := Decode([]byte(`{"format":"other","version":1,"nodes":[]}`)); err == nil {
		t.Fatal("expected format error")
	}
}

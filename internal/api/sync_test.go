package api

import (
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
	"github.com/proxy-panel/proxy-panel/internal/nodesync"
)

func TestValidateSyncPayload(t *testing.T) {
	base := nodesync.New([]nodesync.Node{{ID: "ac-1", Type: "anyconnect", Name: "AnyConnect", RuntimeVersion: "v1.5.0", ListenHost: "0.0.0.0", ListenPort: 443, Config: nodes.Config{
		ServerName: "vpn.example.com", VPNNetwork: "192.168.144.0/24", DNS: "1.1.1.1", ChinaDirectDNS: "223.5.5.5", MTU: 1340, MaxClients: 32, MaxSameClients: 2,
		CertificateURL: "https://cert.example.com/server.crt.pem", PrivateKeyURL: "https://cert.example.com/server.key.pem", CertificateSchedule: "daily", CertificateScheduleTime: "03:30",
		ChinaCIDRSourceURL: "https://ftp.apnic.net/stats/apnic/delegated-apnic-latest", ChinaCIDRSourceFormat: "apnic", ChinaCIDRSchedule: "daily", ChinaCIDRScheduleTime: "04:10",
	}, AnyConnectSecrets: &nodes.AnyConnectSecrets{CertificatePassword: "secret"}}})
	if err := validateSyncPayload(base); err != nil {
		t.Fatal(err)
	}
	unsupported := base
	unsupported.Nodes = append([]nodesync.Node(nil), base.Nodes...)
	unsupported.Nodes[0].Type = "snell"
	if err := validateSyncPayload(unsupported); err == nil {
		t.Fatal("expected non-AnyConnect node to be rejected")
	}
	base.Nodes[0].Config.ServerName = ""
	if err := validateSyncPayload(base); err == nil {
		t.Fatal("expected invalid node configuration")
	}
}

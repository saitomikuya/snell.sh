package firewall

import (
	"strings"
	"testing"
)

func TestQuoteNFTString(t *testing.T) {
	got := quote("proxy-panel:node-a:traffic:upload:tcp:443")
	if got != `"proxy-panel:node-a:traffic:upload:tcp:443"` {
		t.Fatalf("unexpected quoted nft value: %q", got)
	}
}

func TestParseAccountingJSON(t *testing.T) {
	input := []byte(`{"nftables":[
		{"metainfo":{"json_schema_version":1}},
		{"rule":{"family":"inet","table":"proxy_panel","chain":"input","comment":"proxy-panel:node-a:traffic:upload:tcp:443","expr":[{"counter":{"packets":3,"bytes":120}}]}},
		{"rule":{"family":"inet","table":"proxy_panel","chain":"input","comment":"proxy-panel:node-a:traffic:upload:udp:443","expr":[{"counter":{"packets":2,"bytes":80}}]}},
		{"rule":{"family":"inet","table":"proxy_panel","chain":"output","comment":"proxy-panel:node-a:traffic:download:tcp:443","expr":[{"counter":{"packets":4,"bytes":900}}]}},
		{"rule":{"family":"inet","table":"proxy_panel","chain":"input","comment":"proxy-panel:node-a:blocked","expr":[{"counter":{"packets":1,"bytes":40}}]}}
	]}`)
	values, err := ParseAccountingJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if values["node-a"].UploadBytes != 200 || values["node-a"].DownloadBytes != 900 {
		t.Fatalf("unexpected counters: %+v", values)
	}
}

func TestHostVPNForwardRulesAreScopedToNodeTunnel(t *testing.T) {
	rules := hostVPNForwardRules("5375cbcb-12bd-4447-8efd-82d96694c526", "192.168.144.0/24")
	if len(rules) != 2 {
		t.Fatalf("host forwarding rules = %d, want 2", len(rules))
	}
	out := strings.Join(rules[0].args, " ")
	in := strings.Join(rules[1].args, " ")
	if !strings.Contains(out, "-i pp5375cbcb+ -s 192.168.144.0/24") || !strings.Contains(out, "host-forward:out") {
		t.Fatalf("outbound rule is not tunnel-scoped: %s", out)
	}
	if !strings.Contains(in, "-o pp5375cbcb+ -d 192.168.144.0/24") || !strings.Contains(in, "ESTABLISHED,RELATED") || !strings.Contains(in, "host-forward:in") {
		t.Fatalf("inbound rule is not tunnel-scoped: %s", in)
	}
}

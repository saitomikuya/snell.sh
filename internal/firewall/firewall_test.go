package firewall

import "testing"

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

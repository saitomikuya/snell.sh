package configgen

import (
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestGenerateSS2022(t *testing.T) {
	node := nodes.Node{Type: "ss2022", ListenHost: "0.0.0.0", ListenPort: 12345, Config: nodes.Config{Mode: "tcp_and_udp", Method: "2022-blake3-aes-128-gcm", TFO: true}}
	raw, err := Generate(node, "AAAAAAAAAAAAAAAAAAAAAA==", nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, value := range []string{"tcp_and_udp", "2022-blake3-aes-128-gcm", "12345"} {
		if !strings.Contains(text, value) {
			t.Fatalf("missing %s in %s", value, text)
		}
	}
}

func TestGenerateSnellDefaultsToPublicListenAddress(t *testing.T) {
	raw, err := Generate(nodes.Node{Type: "snell", ListenPort: 6160, Config: nodes.Config{Version: "v5"}}, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "listen = 0.0.0.0:6160") {
		t.Fatalf("generated Snell config did not use public default: %s", raw)
	}
}

func TestGenerateShadowTLSKeepsExplicitLoopbackBackend(t *testing.T) {
	frontend := nodes.Node{Type: "shadowtls", ListenPort: 8443, Config: nodes.Config{SNI: "www.microsoft.com", WildcardSNI: "off"}}
	backend := nodes.Node{Type: "snell", ListenHost: "127.0.0.1", ListenPort: 6160}
	raw, err := Generate(frontend, "secret", &backend)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"listen": "0.0.0.0:8443"`) || !strings.Contains(string(raw), `"server": "127.0.0.1:6160"`) {
		t.Fatalf("generated ShadowTLS config has unexpected endpoints: %s", raw)
	}
}
func TestShadowTLSRequiresBackend(t *testing.T) {
	_, err := Generate(nodes.Node{Type: "shadowtls"}, "secret", nil)
	if err == nil {
		t.Fatal("expected backend error")
	}
}

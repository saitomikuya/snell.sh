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
func TestShadowTLSRequiresBackend(t *testing.T) {
	_, err := Generate(nodes.Node{Type: "shadowtls"}, "secret", nil)
	if err == nil {
		t.Fatal("expected backend error")
	}
}

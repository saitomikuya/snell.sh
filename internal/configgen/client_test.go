package configgen

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestShadowTLSSnellUsesSurgeV4CompatibilityWithoutFakeQRCode(t *testing.T) {
	backend := nodes.Node{Type: "snell", Config: nodes.Config{Version: "v5"}}
	frontend := nodes.Node{Type: "shadowtls", Name: "组合节点", ListenHost: "0.0.0.0", ListenPort: 8443, Config: nodes.Config{SNI: "www.microsoft.com"}}
	config, err := Client(frontend, "shadow-secret", "45.77.250.38", &backend, "snell-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"45.77.250.38", `psk="snell-secret"`, `shadow-tls-password="shadow-secret"`, "shadow-tls-sni=www.microsoft.com", "version=4"} {
		if !strings.Contains(config.Surge, value) {
			t.Fatalf("Surge config is missing %q: %s", value, config.Surge)
		}
	}
	if !strings.Contains(config.SurgeAlternative, "version=5") {
		t.Fatalf("expected a v5 alternative: %s", config.SurgeAlternative)
	}
	if config.Primary != "" || config.QRCode != "" {
		t.Fatalf("Snell + ShadowTLS must not expose a non-standard QR import: %+v", config)
	}
	if !strings.Contains(config.ImportNote, "SS-2022 ShadowTLS") {
		t.Fatalf("missing actionable Shadowrocket note: %s", config.ImportNote)
	}
}

func TestShadowTLSSS2022UsesUpstreamShadowrocketCombinedURIAndQRCode(t *testing.T) {
	backend := nodes.Node{Type: "ss2022", ListenPort: 34059, Config: nodes.Config{Method: "2022-blake3-aes-128-gcm", Mode: "tcp_and_udp"}}
	frontend := nodes.Node{Type: "shadowtls", Name: "SS 组合", ListenHost: "0.0.0.0", ListenPort: 36341, Config: nodes.Config{SNI: "www.microsoft.com"}}
	config, err := Client(frontend, "shadow+secret", "45.77.250.38", &backend, "a2V5L3dpdGgrcGx1cw==")
	if err != nil {
		t.Fatal(err)
	}
	withoutScheme := strings.TrimPrefix(config.Primary, "ss://")
	parts := strings.SplitN(withoutScheme, "@", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected SS endpoint: %s", config.Primary)
	}
	userinfo, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil || string(userinfo) != backend.Config.Method+":a2V5L3dpdGgrcGx1cw==" {
		t.Fatalf("unexpected Shadowrocket userinfo: %q (%v)", userinfo, err)
	}
	endpointAndQuery := strings.SplitN(parts[1], "?shadow-tls=", 2)
	if len(endpointAndQuery) != 2 || endpointAndQuery[0] != "45.77.250.38:34059" {
		t.Fatalf("combined URI must point to the SS backend port: %s", config.Primary)
	}
	encodedPayload := strings.SplitN(endpointAndQuery[1], "#", 2)[0]
	payload, err := base64.StdEncoding.DecodeString(encodedPayload)
	if err != nil {
		t.Fatal(err)
	}
	var shadowTLS map[string]string
	if err = json.Unmarshal(payload, &shadowTLS); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"address": "45.77.250.38", "port": "36341", "host": "www.microsoft.com", "password": "shadow+secret", "version": "3"} {
		if shadowTLS[key] != value {
			t.Fatalf("ShadowTLS payload %s=%q, expected %q", key, shadowTLS[key], value)
		}
	}
	if !strings.HasPrefix(config.QRCode, "data:image/png;base64,") || config.QRCodeLabel == "" {
		t.Fatal("expected a labeled embedded QR code")
	}
	for _, value := range []string{"shadow-tls-version=3", "udp-relay=true", "udp-port=34059"} {
		if !strings.Contains(config.Surge, value) {
			t.Fatalf("Surge config is missing %q: %s", value, config.Surge)
		}
	}
	for _, value := range []string{"plugin: shadow-tls", "host: \"www.microsoft.com\"", "version: 3"} {
		if !strings.Contains(config.Clash, value) {
			t.Fatalf("Clash config is missing %q: %s", value, config.Clash)
		}
	}
	if !strings.HasPrefix(config.ClashInline, `- {`) || !strings.Contains(config.ClashInline, `"plugin":"shadow-tls"`) || strings.Contains(config.ClashInline, "\n") {
		t.Fatalf("expected a pasteable one-line Clash proxy: %s", config.ClashInline)
	}
}

func TestAEAD2022SIP002UserInfoIsNotBase64Encoded(t *testing.T) {
	node := nodes.Node{Type: "ss2022", Name: "SS", ListenHost: "0.0.0.0", ListenPort: 12345, Config: nodes.Config{Method: "2022-blake3-aes-128-gcm", Mode: "tcp_and_udp"}}
	config, err := Client(node, "key+/with=reserved==", "example.com", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(config.Primary, "ss://2022-blake3-aes-128-gcm:key%2B%2Fwith%3Dreserved%3D%3D@example.com:12345") {
		t.Fatalf("unexpected SIP002 URI: %s", config.Primary)
	}
	if !strings.HasPrefix(config.QRCode, "data:image/png;base64,") {
		t.Fatal("expected an SS QR code")
	}
}

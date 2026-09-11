package anyconnect

import (
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestParseAPNICRangeAndRenderNoRoutes(t *testing.T) {
	prefixes, err := parseAPNIC([]byte("apnic|CN|ipv4|1.0.1.0|512|20110414|allocated\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 2 || prefixes[0].String() != "1.0.1.0/24" || prefixes[1].String() != "1.0.2.0/24" {
		t.Fatalf("unexpected prefixes: %v", prefixes)
	}
	rendered := string(renderNoRoutes(prefixes))
	if !strings.Contains(rendered, "no-route = 1.0.1.0/255.255.255.0") {
		t.Fatalf("unexpected group routes: %s", rendered)
	}
}

func TestCIDRParserRejectsSuspiciouslySmallDataSet(t *testing.T) {
	if _, err := ParseChinaCIDRs([]byte("1.0.1.0/24\n"), "cidr"); err == nil {
		t.Fatal("expected the data-set size guard to reject one prefix")
	}
}

func TestPublicAddressRejectsPrivateAndSpecialUseRanges(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "100.100.100.100", "192.0.2.10", "198.18.0.1", "2001:db8::1"} {
		if isPublicAddress(netip.MustParseAddr(value)) {
			t.Fatalf("special-use address was accepted: %s", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !isPublicAddress(netip.MustParseAddr(value)) {
			t.Fatalf("public address was rejected: %s", value)
		}
	}
}

func TestInstallFirstCertificateAndRotate(t *testing.T) {
	service := &Service{dataDir: t.TempDir()}
	first := strings.Repeat("a", 64)
	second := strings.Repeat("b", 64)
	if err := service.installCertificate("node", first, []byte("certificate-one"), []byte("key-one")); err != nil {
		t.Fatal(err)
	}
	if err := service.installCertificate("node", second, []byte("certificate-two"), []byte("key-two")); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(service.CertificatePath("node"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "certificate-two" {
		t.Fatalf("current certificate = %q", current)
	}
}

func TestScheduleDueDailyAndWeekly(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 9, 10, 5, 0, 0, 0, location)
	if !due("daily", 0, "03:30", now.Add(-25*time.Hour), time.Time{}, "", now, location) {
		t.Fatal("daily refresh should be due")
	}
	if due("daily", 0, "03:30", now.Add(-time.Hour), time.Time{}, "", now, location) {
		t.Fatal("daily refresh should not run twice after the scheduled time")
	}
	if !due("weekly", int(now.Weekday()), "04:00", now.AddDate(0, 0, -8), time.Time{}, "", now, location) {
		t.Fatal("weekly refresh should be due")
	}
	if due("manual", 0, "", time.Time{}, time.Time{}, "", now, location) {
		t.Fatal("manual schedule must never run automatically")
	}
}

func TestRenderConfigKeepsCapabilityBoundaryAndRouteGroups(t *testing.T) {
	service := &Service{dataDir: t.TempDir()}
	node := nodes.Node{ID: "12345678-abcd", Type: "anyconnect", ListenHost: "0.0.0.0", ListenPort: 443, Config: nodes.Config{VPNNetwork: "192.168.144.0/24", DNS: "1.1.1.1,8.8.8.8", MTU: 1340, MaxClients: 32, MaxSameClients: 2, UDPEnabled: true}}
	config, err := service.renderConfig(node, "/data/config/anyconnect/node")
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, expected := range []string{"route = default", "select-group = FullTunnel", "select-group = ChinaDirect", "auto-select-group = true", "isolate-workers = true"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("configuration missing %q:\n%s", expected, text)
		}
	}
	if prefix, _ := netip.ParsePrefix("192.168.144.0/24"); !strings.Contains(text, "ipv4-network = "+prefix.Addr().String()) {
		t.Fatal("VPN network was not rendered")
	}
}

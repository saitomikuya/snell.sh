package anyconnect

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
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

func TestCIDRParserAcceptsOcservNoRouteNetmasks(t *testing.T) {
	var source strings.Builder
	for index := 0; index < 100; index++ {
		fmt.Fprintf(&source, "no-route = 11.%d.0.0/255.254.0.0\n", index*2)
	}
	prefixes, err := ParseChinaCIDRs([]byte("\ufeff"+source.String()), "cidr")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 100 || prefixes[0].String() != "11.0.0.0/15" || prefixes[99].String() != "11.198.0.0/15" {
		t.Fatalf("unexpected ocserv routes: first=%v last=%v count=%d", prefixes[0], prefixes[len(prefixes)-1], len(prefixes))
	}
	path := filepath.Join(t.TempDir(), "china-routes.conf")
	if err = os.WriteFile(path, []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if !chinaRoutesUsable(path) {
		t.Fatal("validated cached route file was rejected")
	}
}

func TestCoarseRouteSourceExcludesSpecialUseSubnets(t *testing.T) {
	prefixes := excludeSpecialUsePrefixes([]netip.Prefix{netip.MustParsePrefix("203.0.0.0/8")})
	for _, prefix := range prefixes {
		if prefix.Contains(netip.MustParseAddr("203.0.113.1")) {
			t.Fatalf("documentation range leaked through coarse route sanitizing: %s", prefix)
		}
	}
	if len(prefixes) < 2 {
		t.Fatalf("coarse public prefix was not split around special-use space: %v", prefixes)
	}
}

func TestCIDRParserRejectsRoutesAboveCiscoClientLimit(t *testing.T) {
	var source strings.Builder
	for index := 0; index <= maxChinaCIDRRoutes; index++ {
		fmt.Fprintf(&source, "%d.%d.0.0/18\n", 20+index/256, index%256)
	}
	if _, err := ParseChinaCIDRs([]byte(source.String()), "cidr"); err == nil || !strings.Contains(err.Error(), "Cisco Secure Client") {
		t.Fatalf("expected Cisco route-limit error, got %v", err)
	}
	path := filepath.Join(t.TempDir(), "china-routes.conf")
	if err := os.WriteFile(path, []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if chinaRoutesUsable(path) {
		t.Fatal("oversized cached route file was accepted")
	}
}

func TestAPNICRoutesAreCollapsedAndCappedForCiscoClients(t *testing.T) {
	var source strings.Builder
	for index := 0; index <= maxChinaCIDRRoutes; index++ {
		fmt.Fprintf(&source, "apnic|CN|ipv4|%d.%d.0.0|16384|20260911|allocated\n", 20+index/256, index%256)
	}
	prefixes, err := ParseChinaCIDRs([]byte(source.String()), "apnic")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != maxChinaCIDRRoutes {
		t.Fatalf("APNIC routes = %d, want route limit %d", len(prefixes), maxChinaCIDRRoutes)
	}
	for _, prefix := range prefixes {
		if prefix.Bits() != 18 {
			t.Fatalf("APNIC limiting broadened a route: %s", prefix)
		}
	}
	collapsed := collapsePrefixes([]netip.Prefix{
		netip.MustParsePrefix("30.0.0.0/9"),
		netip.MustParsePrefix("30.128.0.0/9"),
	})
	if len(collapsed) != 1 || collapsed[0].String() != "30.0.0.0/8" {
		t.Fatalf("adjacent prefixes were not losslessly collapsed: %v", collapsed)
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
	runtimeRoot := filepath.Join(t.TempDir(), "ocserv-runtime")
	t.Setenv("PANEL_ANYCONNECT_RUNTIME_DIR", runtimeRoot)
	runtimeDir := filepath.Join(runtimeRoot, "12345678-abcd")
	if err := os.MkdirAll(runtimeDir, 0750); err != nil {
		t.Fatal(err)
	}
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
	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0711 {
		t.Fatalf("runtime directory permissions = %#o, want 0711", got)
	}
	if !strings.Contains(text, "socket-file = "+filepath.Join(runtimeDir, "ocserv.socket")) {
		t.Fatalf("sec-mod socket was not moved to the dedicated runtime directory:\n%s", text)
	}
	if strings.Contains(text, filepath.Join(service.dataDir, "runtime", "anyconnect")) {
		t.Fatalf("configuration still places worker sockets below the private data directory:\n%s", text)
	}
	group := string(renderChinaDirectGroup([]byte("no-route = 11.0.0.0/255.0.0.0\n")))
	if !strings.Contains(group, "tunnel-all-dns = true") || !strings.Contains(group, "no-route = 11.0.0.0/255.0.0.0") || strings.Contains(group, "tunnel-all-dns = false") || strings.Contains(group, "\ndns = ") {
		t.Fatalf("ChinaDirect group does not tunnel DNS and split Chinese routes: %s", group)
	}
}

package anyconnect

import (
	"context"
	"os"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
)

// TestLiveAnyConnectAssets is intentionally skipped in ordinary CI. It allows
// maintainers to verify a real certificate distribution center and the APNIC
// feed without checking credentials into the repository.
func TestLiveAnyConnectAssets(t *testing.T) {
	certificateURL := os.Getenv("ANYCONNECT_TEST_CERT_URL")
	privateKeyURL := os.Getenv("ANYCONNECT_TEST_KEY_URL")
	serverName := os.Getenv("ANYCONNECT_TEST_SERVER_NAME")
	if certificateURL == "" || privateKeyURL == "" || serverName == "" {
		t.Skip("live AnyConnect asset environment is not configured")
	}
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secrets, err := secretstore.Open(db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	store := nodes.NewStore(db.DB, secrets)
	node, err := store.Create(context.Background(), nodes.CreateRequest{
		Type:           "anyconnect",
		Name:           "live asset test",
		ListenPort:     443,
		RuntimeVersion: "v1.5.0",
		Config: nodes.Config{
			ServerName:            serverName,
			VPNNetwork:            "192.168.144.0/24",
			DNS:                   "1.1.1.1,8.8.8.8",
			MTU:                   1340,
			MaxClients:            32,
			MaxSameClients:        2,
			CertificateURL:        certificateURL,
			PrivateKeyURL:         privateKeyURL,
			CertificateSchedule:   "manual",
			ChinaCIDRSourceURL:    nodes.DefaultChinaCIDRSource,
			ChinaCIDRSourceFormat: "apnic",
			ChinaCIDRSchedule:     "manual",
		},
		CertificatePassword:  os.Getenv("ANYCONNECT_TEST_DOWNLOAD_PASSWORD"),
		PrivateKeyPassphrase: os.Getenv("ANYCONNECT_TEST_KEY_PASSPHRASE"),
	}, "live-test")
	if err != nil {
		t.Fatal(err)
	}
	service := New(dir, store)
	certificate, err := service.RefreshCertificates(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if !certificate.OK || certificate.Fingerprint == "" {
		t.Fatalf("unexpected certificate result: %+v", certificate)
	}
	routes, err := service.RefreshCIDRs(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if !routes.OK || routes.Count < 100 {
		t.Fatalf("unexpected CIDR result: %+v", routes)
	}
}

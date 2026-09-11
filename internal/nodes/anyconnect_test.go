package nodes

import (
	"context"
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
)

func newAnyConnectTestStore(t *testing.T) (*Store, *database.Store) {
	t.Helper()
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secretstore.Open(db.DB, dir)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return NewStore(db.DB, secrets), db
}

func anyConnectRequest(name, pool string, port int) CreateRequest {
	return CreateRequest{Type: "anyconnect", Name: name, ListenPort: port, Config: Config{ServerName: "vpn.example.com", VPNNetwork: pool, DNS: "1.1.1.1,8.8.8.8", MTU: 1340, MaxClients: 32, MaxSameClients: 2, CertificateURL: "https://certs.example.com/server.crt.pem", PrivateKeyURL: "https://certs.example.com/server.key.pem", CertificateSchedule: "daily", CertificateScheduleTime: "03:30", ChinaCIDRSourceURL: DefaultChinaCIDRSource, ChinaCIDRSourceFormat: "apnic", ChinaCIDRSchedule: "weekly", ChinaCIDRScheduleDay: 1, ChinaCIDRScheduleTime: "04:10"}, CertificatePassword: "download-secret"}
}

func TestAnyConnectUserPasswordIsEncryptedAndNeverReturned(t *testing.T) {
	store, db := newAnyConnectTestStore(t)
	defer db.Close()
	ctx := context.Background()
	node, err := store.Create(ctx, anyConnectRequest("VPN", "192.168.144.0/24", 443), "test")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAnyConnectUser(ctx, node.ID, AnyConnectUserRequest{Username: "alice", Password: "user-secret", RouteGroup: "cn", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if user.RouteGroup != "cn" || strings.Contains(ConfigJSON(node.Config), "user-secret") {
		t.Fatalf("unexpected user response: %+v", user)
	}
	var ciphertext []byte
	if err = db.DB.QueryRowContext(ctx, `SELECT s.ciphertext FROM secrets s JOIN anyconnect_users u ON u.password_secret_ref=s.id WHERE u.id=?`, user.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "user-secret") {
		t.Fatal("plaintext password was stored in the database")
	}
	password, err := store.AnyConnectUserPassword(ctx, user.ID)
	if err != nil || password != "user-secret" {
		t.Fatalf("password roundtrip = %q, %v", password, err)
	}
	updated, err := store.UpdateAnyConnectUser(ctx, node.ID, user.ID, AnyConnectUserRequest{Username: "alice", RouteGroup: "full", Enabled: true})
	if err != nil || updated.RouteGroup != "full" {
		t.Fatalf("route group update failed: %+v, %v", updated, err)
	}
}

func TestAnyConnectPoolsCannotOverlap(t *testing.T) {
	store, db := newAnyConnectTestStore(t)
	defer db.Close()
	ctx := context.Background()
	if _, err := store.Create(ctx, anyConnectRequest("VPN A", "192.168.144.0/24", 443), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, anyConnectRequest("VPN B", "192.168.144.128/25", 444), "test"); err == nil || !strings.Contains(err.Error(), "重叠") {
		t.Fatalf("expected overlapping pool error, got %v", err)
	}
}

func TestAnyConnectCannotBeShadowTLSBackend(t *testing.T) {
	store, db := newAnyConnectTestStore(t)
	defer db.Close()
	ctx := context.Background()
	vpn, err := store.Create(ctx, anyConnectRequest("VPN", "192.168.144.0/24", 443), "test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(ctx, CreateRequest{Type: "shadowtls", Name: "invalid backend", RuntimeVersion: "v0.2.25", ListenPort: 8443, BackendNodeID: vpn.ID, Config: Config{Version: "v3", SNI: "www.example.com", WildcardSNI: "off"}}, "test")
	if err == nil || !strings.Contains(err.Error(), "只有 Snell 或 SS-2022") {
		t.Fatalf("expected AnyConnect backend rejection, got %v", err)
	}
}

func TestAnyConnectDefaultsAndValidation(t *testing.T) {
	req := anyConnectRequest("VPN", "192.168.144.0/24", 443)
	req.RuntimeVersion = ""
	if err := Validate(req); err != nil {
		t.Fatal(err)
	}
	Normalize(&req)
	if req.RuntimeVersion != "v1.5.0" {
		t.Fatalf("runtime default = %q", req.RuntimeVersion)
	}
	req.Config.CertificateURL = "http://certs.example.com/server.pem"
	if err := Validate(req); err == nil {
		t.Fatal("expected non-HTTPS certificate URL rejection")
	}
}

package updates

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/database"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCheckComparesUpstreamAndEnablesCompatibleUpdate(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0750); err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.DB.Exec(`INSERT INTO nodes(id,type,name,enabled,desired_state,runtime_version,listen_host,listen_port,created_at,updated_at) VALUES
		('snell','snell','Snell',1,'running','v5.0.1','127.0.0.1',6160,'now','now'),
		('ss','ss2022','SS',1,'running','v1.23.5','0.0.0.0',20000,'now','now'),
		('tls','shadowtls','TLS',1,'running','v0.2.25','0.0.0.0',8443,'now','now')`)
	if err != nil {
		t.Fatal(err)
	}
	service := New(store.DB, dir, "test")
	service.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case strings.Contains(request.URL.Host, "nssurge.com"):
			body = `<a href="snell-server-v5.0.1-linux-amd64.zip">download</a>`
		case strings.Contains(request.URL.Path, "shadowsocks-rust/releases/latest"):
			body = `{"tag_name":"v1.24.0"}`
		case strings.Contains(request.URL.Path, "shadow-tls/releases/latest"):
			body = `{"tag_name":"v0.2.25"}`
		case strings.Contains(request.URL.Path, "jinqians/snell.sh/commits/main"):
			body = `{"sha":"65b24cc51fa78a5482a96d6d965fd31b43f31809"}`
		case strings.Contains(request.URL.Path, "jinqians/ss-2022.sh/commits/main"):
			body = `{"sha":"89d5cf4cdd81bcbb54b4be05af9d1280489aa6e9"}`
		default:
			t.Fatalf("unexpected upstream URL %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	status, err := service.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ss Component
	for _, component := range status.Components {
		if component.Key == "shadowsocks-rust" {
			ss = component
		}
	}
	if !ss.UpdateAvailable || !ss.CanApply || ss.LatestVersion != "v1.24.0" {
		t.Fatalf("expected a compatible one-click update, got %+v", ss)
	}
	if _, err = os.Stat(service.statusPath); err != nil {
		t.Fatalf("update status was not persisted: %v", err)
	}
}

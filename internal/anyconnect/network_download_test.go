package anyconnect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

func TestChinaCIDRSourceURLsUsesOfficialMirrorsForDefault(t *testing.T) {
	sources := chinaCIDRSourceURLs(nodes.DefaultChinaCIDRSource)
	if len(sources) != 3 {
		t.Fatalf("expected APNIC source plus two mirrors, got %d", len(sources))
	}
	if sources[0] != nodes.DefaultChinaCIDRSource {
		t.Fatalf("default source moved from first position: %q", sources[0])
	}
	if custom := chinaCIDRSourceURLs("https://routes.example.com/cn.txt"); len(custom) != 1 || custom[0] != "https://routes.example.com/cn.txt" {
		t.Fatalf("custom source unexpectedly got fallback mirrors: %#v", custom)
	}
}

func TestDownloadRetriesServerError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("delegated-apnic-test"))
	}))
	defer server.Close()

	content, err := download(context.Background(), server.Client(), server.URL, "", "")
	if err != nil {
		t.Fatalf("download did not recover from transient server error: %v", err)
	}
	if string(content) != "delegated-apnic-test" {
		t.Fatalf("unexpected content %q", content)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected one retry, got %d requests", requests.Load())
	}
}

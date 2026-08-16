package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/agent"
	"github.com/proxy-panel/proxy-panel/internal/auth"
	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
)

func TestFirstLoginPasswordChangeBoundary(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir}
	if err := cfg.InitDirectories(); err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	authService := auth.New(store.DB)
	if err = authService.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	secretService, err := secretstore.Open(store.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	nodeStore := nodes.NewStore(store.DB, secretService)
	if err = nodeStore.InitializeDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, authService, nodeStore, agent.NewClient(filepath.Join(dir, "missing.sock")), false, "test").Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	response := postJSON(t, client, server.URL+"/api/v1/auth/login", `{"password":"password"}`, "")
	if response.StatusCode != 200 {
		t.Fatalf("login status %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = client.Get(server.URL + "/api/v1/nodes")
	if err != nil {
		t.Fatal(err)
	}
	assertCode(t, response, 403, "PASSWORD_CHANGE_REQUIRED")
	response = postJSON(t, client, server.URL+"/api/v1/auth/change-password", `{"newPassword":"新的安全密码"}`, "")
	assertCode(t, response, 403, "CSRF_FAILED")
	csrf := ""
	for _, cookie := range jar.Cookies(response.Request.URL) {
		if cookie.Name == "panel_csrf" {
			csrf = cookie.Value
		}
	}
	response = postJSON(t, client, server.URL+"/api/v1/auth/change-password", `{"newPassword":"新的安全密码"}`, csrf)
	if response.StatusCode != 200 {
		t.Fatalf("change password status %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = client.Get(server.URL + "/api/v1/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 401 {
		t.Fatalf("old session should be invalid, got %d", response.StatusCode)
	}
	response.Body.Close()
	response = postJSON(t, client, server.URL+"/api/v1/auth/login", `{"password":"新的安全密码"}`, "")
	if response.StatusCode != 200 {
		t.Fatalf("new login status %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = client.Get(server.URL + "/api/v1/nodes")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("nodes status %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = client.Get(server.URL + "/api/v1/nodes/shadowtls-ss-main/client-configs")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("client config status %d", response.StatusCode)
	}
	var clientConfig struct {
		Primary string `json:"primary"`
		QRCode  string `json:"qrCode"`
		Masked  bool   `json:"masked"`
	}
	if err = json.NewDecoder(response.Body).Decode(&clientConfig); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if clientConfig.Masked || strings.Contains(clientConfig.Primary, "••••") || !strings.HasPrefix(clientConfig.Primary, "ss://") || !strings.Contains(clientConfig.Primary, "?shadow-tls=") || !strings.HasPrefix(clientConfig.QRCode, "data:image/png;base64,") {
		t.Fatalf("authenticated client config should be complete and include QR code: %+v", clientConfig)
	}
}

func postJSON(t *testing.T, client *http.Client, url, body, csrf string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func assertCode(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("status %d, want %d", response.StatusCode, status)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code {
		t.Fatalf("code %s, want %s", body.Error.Code, code)
	}
}

var _ = os.ErrNotExist

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/proxy-panel/proxy-panel/internal/agent"
	"github.com/proxy-panel/proxy-panel/internal/auth"
	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
)

type recordingRuntime struct {
	mu       sync.Mutex
	applied  []string
	failID   string
	failOnce bool
	uploaded *agent.RuntimeUploadInstallRequest
}

func (s *recordingRuntime) InstallUploadedRuntime(req agent.RuntimeUploadInstallRequest, out *agent.RuntimeInstallResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploaded = &req
	out.OK = true
	out.Kind = req.Kind
	out.Version = req.Version
	out.Architecture = "amd64"
	return nil
}

func (s *recordingRuntime) Apply(req agent.NodeRequest, out *agent.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, req.NodeID)
	if req.NodeID == s.failID && s.failOnce {
		s.failOnce = false
		return errors.New("injected dependent apply failure")
	}
	out.OK = true
	out.State = "running"
	return nil
}

func (s *recordingRuntime) CheckPort(_ agent.PortRequest, out *agent.PortResult) error {
	out.Available = true
	return nil
}

func (s *recordingRuntime) appliedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.applied...)
}

func TestBackendEndpointUpdateReappliesShadowTLSDependents(t *testing.T) {
	server, nodeStore, runtime := newDependencyUpdateServer(t)
	old, err := nodeStore.Get(context.Background(), "ss-main")
	if err != nil {
		t.Fatal(err)
	}
	newPort := unusedTestPort(t, nodeStore, old.ListenPort+1)

	response := updateNodeRequest(t, server, old, newPort)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	want := []string{"ss-main", "shadowtls-ss-main"}
	if got := runtime.appliedIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied nodes = %v, want %v", got, want)
	}
	updated, err := nodeStore.Get(context.Background(), "ss-main")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ListenPort != newPort {
		t.Fatalf("backend port = %d, want %d", updated.ListenPort, newPort)
	}
}

func TestDependentApplyFailureRollsBackBackendAndDependents(t *testing.T) {
	server, nodeStore, runtime := newDependencyUpdateServer(t)
	runtime.failID = "shadowtls-ss-main"
	runtime.failOnce = true
	old, err := nodeStore.Get(context.Background(), "ss-main")
	if err != nil {
		t.Fatal(err)
	}
	newPort := unusedTestPort(t, nodeStore, old.ListenPort+1)

	response := updateNodeRequest(t, server, old, newPort)
	assertCode(t, response.Result(), http.StatusConflict, "APPLY_ROLLED_BACK")
	want := []string{"ss-main", "shadowtls-ss-main", "ss-main", "shadowtls-ss-main"}
	if got := runtime.appliedIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied nodes = %v, want %v", got, want)
	}
	rolledBack, err := nodeStore.Get(context.Background(), "ss-main")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ListenPort != old.ListenPort {
		t.Fatalf("backend port after rollback = %d, want %d", rolledBack.ListenPort, old.ListenPort)
	}
}

func TestRuntimeUploadInstallsAndUpdatesNodes(t *testing.T) {
	server, nodeStore, runtimeRecorder := newDependencyUpdateServer(t)
	payload := []byte("official runtime archive")
	checksum := fmt.Sprintf("%x", sha256.Sum256(payload))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"component": "snell", "version": "v5.1.0", "format": "zip", "sha256": checksum} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("file", "snell-server-v5.1.0-linux-amd64.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/updates/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.uploadUpdate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	updated, err := nodeStore.Get(context.Background(), "snell-main")
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuntimeVersion != "v5.1.0" {
		t.Fatalf("runtime version = %s", updated.RuntimeVersion)
	}
	runtimeRecorder.mu.Lock()
	installed := runtimeRecorder.uploaded
	runtimeRecorder.mu.Unlock()
	if installed == nil || installed.Kind != "snell" || installed.SHA256 != checksum {
		t.Fatalf("unexpected upload request: %+v", installed)
	}
}

func newDependencyUpdateServer(t *testing.T) (*Server, *nodes.Store, *recordingRuntime) {
	t.Helper()
	dir := t.TempDir()
	if err := (config.Config{DataDir: dir}).InitDirectories(); err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secrets, err := secretstore.Open(store.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	nodeStore := nodes.NewStore(store.DB, secrets)
	if err = nodeStore.InitializeDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}

	recorder := &recordingRuntime{}
	rpcServer := rpc.NewServer()
	if err = rpcServer.RegisterName("Runtime", recorder); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("", "pp-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go rpcServer.ServeConn(connection)
		}
	}()

	apiServer := New(store, auth.New(store.DB), nodeStore, agent.NewClient(socket), false, "test")
	return apiServer, nodeStore, recorder
}

func updateNodeRequest(t *testing.T, server *Server, old nodes.Node, newPort int) *httptest.ResponseRecorder {
	t.Helper()
	requestBody, err := json.Marshal(nodes.UpdateRequest{
		Name:           old.Name,
		RuntimeVersion: old.RuntimeVersion,
		ListenHost:     old.ListenHost,
		ListenPort:     newPort,
		BackendNodeID:  old.BackendNodeID,
		Config:         old.Config,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/nodes/ss-main", strings.NewReader(string(requestBody)))
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", "ss-main")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	response := httptest.NewRecorder()
	server.updateNode(response, request)
	return response
}

func unusedTestPort(t *testing.T, store *nodes.Store, start int) int {
	t.Helper()
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	used := make(map[int]bool, len(list))
	for _, node := range list {
		used[node.ListenPort] = true
	}
	for port := max(start, 1024); port <= 65535; port++ {
		if !used[port] {
			return port
		}
	}
	t.Fatal("no unused test port")
	return 0
}

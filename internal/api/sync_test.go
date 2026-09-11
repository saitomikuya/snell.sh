package api

import (
	"testing"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
	"github.com/proxy-panel/proxy-panel/internal/nodesync"
)

func TestValidateSyncPayload(t *testing.T) {
	base := nodesync.New([]nodesync.Node{{ID: "snell-1", Type: "snell", Name: "Snell", RuntimeVersion: "v5.0.1", ListenHost: "0.0.0.0", ListenPort: 6160, Config: nodes.Config{Version: "v5"}, Secret: "secret"}})
	if err := validateSyncPayload(base); err != nil {
		t.Fatal(err)
	}
	base.Nodes[0].Config.Version = "invalid"
	if err := validateSyncPayload(base); err == nil {
		t.Fatal("expected invalid node configuration")
	}
}

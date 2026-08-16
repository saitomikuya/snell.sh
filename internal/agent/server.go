package agent

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/firewall"
	panelruntime "github.com/proxy-panel/proxy-panel/internal/runtime"
)

type RuntimeService struct{ Manager *Manager }

func (s *RuntimeService) Apply(req NodeRequest, out *Result) error {
	if req.NodeID == "" {
		return errors.New("node id is required")
	}
	value, err := s.Manager.Apply(req.NodeID)
	*out = value
	return err
}
func (s *RuntimeService) Start(req NodeRequest, out *Result) error {
	value, err := s.Manager.Start(req.NodeID)
	*out = value
	return err
}
func (s *RuntimeService) Stop(req NodeRequest, out *Result) error {
	value, err := s.Manager.Stop(req.NodeID)
	*out = value
	return err
}
func (s *RuntimeService) Restart(req NodeRequest, out *Result) error {
	value, err := s.Manager.Restart(req.NodeID)
	*out = value
	return err
}
func (s *RuntimeService) Logs(req LogsRequest, out *LogsResult) error {
	value, err := s.Manager.Logs(req.NodeID, req.Tail)
	*out = value
	return err
}
func (s *RuntimeService) Status(_ Empty, out *StatusResult) error {
	*out = s.Manager.Status()
	return nil
}
func (s *RuntimeService) CheckPort(req PortRequest, out *PortResult) error {
	if req.Port < 1 || req.Port > 65535 {
		return errors.New("invalid port")
	}
	address := net.JoinHostPort(req.Host, strconv.Itoa(req.Port))
	switch req.Network {
	case "tcp":
		listener, err := net.Listen("tcp", address)
		if err != nil {
			out.Message = err.Error()
			return nil
		}
		listener.Close()
		out.Available = true
	case "udp":
		conn, err := net.ListenPacket("udp", address)
		if err != nil {
			out.Message = err.Error()
			return nil
		}
		conn.Close()
		out.Available = true
	default:
		return errors.New("network must be tcp or udp")
	}
	return nil
}
func (s *RuntimeService) SetBlocked(req BlockRequest, out *Result) error {
	node, err := s.Manager.nodes.Get(context.Background(), req.NodeID)
	if err != nil {
		return err
	}
	networks := []string{"tcp"}
	if node.Type == "ss2022" {
		switch node.Config.Mode {
		case "udp_only":
			networks = []string{"udp"}
		case "tcp_and_udp":
			networks = []string{"tcp", "udp"}
		}
	}
	if err = firewall.SetBlocked(node.ID, node.ListenPort, networks, req.Blocked); err != nil {
		return err
	}
	out.OK = true
	out.Message = "firewall policy applied"
	return nil
}
func (s *RuntimeService) CleanupFirewall(_ Empty, out *Result) error {
	err := firewall.Cleanup()
	out.OK = err == nil
	if err != nil {
		out.Message = err.Error()
	}
	return err
}
func (s *RuntimeService) InstallRuntime(req RuntimeInstallRequest, out *RuntimeInstallResult) error {
	if req.Kind == "" || req.Version == "" {
		return errors.New("runtime kind and version are required")
	}
	installer, err := panelruntime.NewInstaller(s.Manager.dataDir)
	if err != nil {
		return err
	}
	record, err := installer.Install(context.Background(), req.Kind, req.Version, stdruntime.GOARCH)
	if err != nil {
		return err
	}
	*out = RuntimeInstallResult{OK: true, Kind: record.Kind, Version: record.Version, Architecture: record.Architecture, InstalledAt: record.InstalledAt}
	return nil
}

func Serve(socket string, manager *Manager) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0750); err != nil {
		return err
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err = os.Chmod(socket, 0660); err != nil {
		return err
	}
	gid := 10001
	if parsed, parseErr := strconv.Atoi(os.Getenv("PANEL_GID")); parseErr == nil && parsed > 0 {
		gid = parsed
	}
	_ = os.Chown(socket, -1, gid)
	server := rpc.NewServer()
	if err = server.RegisterName("Runtime", &RuntimeService{Manager: manager}); err != nil {
		return err
	}
	go func() {
		manager.Reconcile()
		_ = manager.SampleTraffic()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		cycles := 0
		for range ticker.C {
			_ = manager.SampleTraffic()
			cycles++
			if cycles%6 == 0 {
				manager.Reconcile()
			}
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go server.ServeConn(conn)
	}
}

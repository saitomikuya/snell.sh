package agent

import (
	"net"
	"net/rpc"
	"time"
)

type Client struct {
	socket  string
	timeout time.Duration
}

func NewClient(socket string) *Client { return &Client{socket: socket, timeout: 4 * time.Second} }
func (c *Client) call(method string, request, response any) error {
	return c.callWithTimeout(method, request, response, c.timeout)
}
func (c *Client) callWithTimeout(method string, request, response any, timeout time.Duration) error {
	conn, err := net.DialTimeout("unix", c.socket, c.timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	return rpc.NewClient(conn).Call("Runtime."+method, request, response)
}
func (c *Client) InstallRuntime(kind, version string) (RuntimeInstallResult, error) {
	var out RuntimeInstallResult
	err := c.callWithTimeout("InstallRuntime", RuntimeInstallRequest{Kind: kind, Version: version}, &out, 3*time.Minute)
	return out, err
}
func (c *Client) InstallUploadedRuntime(kind, version, format, uploadID, sha256 string) (RuntimeInstallResult, error) {
	var out RuntimeInstallResult
	err := c.callWithTimeout("InstallUploadedRuntime", RuntimeUploadInstallRequest{Kind: kind, Version: version, Format: format, UploadID: uploadID, SHA256: sha256}, &out, 3*time.Minute)
	return out, err
}
func (c *Client) Apply(id string) (Result, error) {
	var out Result
	err := c.call("Apply", NodeRequest{NodeID: id}, &out)
	return out, err
}
func (c *Client) Start(id string) (Result, error) {
	var out Result
	err := c.call("Start", NodeRequest{NodeID: id}, &out)
	return out, err
}
func (c *Client) Stop(id string) (Result, error) {
	var out Result
	err := c.call("Stop", NodeRequest{NodeID: id}, &out)
	return out, err
}
func (c *Client) Restart(id string) (Result, error) {
	var out Result
	err := c.call("Restart", NodeRequest{NodeID: id}, &out)
	return out, err
}
func (c *Client) Logs(id string, tail int) (LogsResult, error) {
	var out LogsResult
	err := c.call("Logs", LogsRequest{NodeID: id, Tail: tail}, &out)
	return out, err
}
func (c *Client) Status() (StatusResult, error) {
	var out StatusResult
	err := c.call("Status", Empty{}, &out)
	return out, err
}
func (c *Client) CheckPort(host string, port int, network string) (PortResult, error) {
	var out PortResult
	err := c.call("CheckPort", PortRequest{Host: host, Port: port, Network: network}, &out)
	return out, err
}
func (c *Client) SetBlocked(id string, blocked bool) (Result, error) {
	var out Result
	err := c.call("SetBlocked", BlockRequest{NodeID: id, Blocked: blocked}, &out)
	return out, err
}
func (c *Client) SetProjectBlocked(blocked bool) (Result, error) {
	var out Result
	err := c.call("SetProjectBlocked", BlockRequest{Blocked: blocked}, &out)
	return out, err
}
func (c *Client) MaintainLogs() (LogMaintenanceResult, error) {
	var out LogMaintenanceResult
	err := c.call("MaintainLogs", Empty{}, &out)
	return out, err
}
func (c *Client) CleanupFirewall() (Result, error) {
	var out Result
	err := c.call("CleanupFirewall", Empty{}, &out)
	return out, err
}
func (c *Client) RefreshAnyConnectAsset(nodeID, kind string) (AssetRefreshResult, error) {
	var out AssetRefreshResult
	err := c.callWithTimeout("RefreshAnyConnectAsset", AssetRefreshRequest{NodeID: nodeID, Kind: kind}, &out, 45*time.Second)
	return out, err
}
func (c *Client) RemoveAnyConnect(nodeID string) (Result, error) {
	var out Result
	err := c.call("RemoveAnyConnect", NodeRequest{NodeID: nodeID}, &out)
	return out, err
}

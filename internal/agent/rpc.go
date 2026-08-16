package agent

type NodeRequest struct{ NodeID string }
type LogsRequest struct {
	NodeID string
	Tail   int
}
type Result struct {
	OK           bool
	State        string
	PID          int
	Message      string
	RestartCount int
}
type LogsResult struct{ Lines []string }
type StatusResult struct{ Instances map[string]Result }
type PortRequest struct {
	Host    string
	Port    int
	Network string
}
type PortResult struct {
	Available bool
	Message   string
}
type BlockRequest struct {
	NodeID  string
	Blocked bool
}
type RuntimeInstallRequest struct {
	Kind    string
	Version string
}
type RuntimeInstallResult struct {
	OK           bool
	Kind         string
	Version      string
	Architecture string
	InstalledAt  string
}
type Empty struct{}

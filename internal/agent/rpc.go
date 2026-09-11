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
type LogsResult struct {
	Lines      []string `json:"lines"`
	TotalBytes int64    `json:"totalBytes"`
	UpdatedAt  string   `json:"updatedAt"`
}
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
type LogMaintenanceResult struct {
	OK           bool
	LimitBytes   int64
	CurrentBytes int64
	RemovedFiles int
}
type RuntimeInstallRequest struct {
	Kind    string
	Version string
}
type RuntimeUploadInstallRequest struct {
	Kind     string
	Version  string
	Format   string
	UploadID string
	SHA256   string
}
type RuntimeInstallResult struct {
	OK           bool
	Kind         string
	Version      string
	Architecture string
	InstalledAt  string
}
type AssetRefreshRequest struct {
	NodeID string
	Kind   string
}
type AssetRefreshResult struct {
	OK          bool   `json:"ok"`
	Changed     bool   `json:"changed"`
	Kind        string `json:"kind"`
	Fingerprint string `json:"fingerprint"`
	Count       int    `json:"count,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
	Message     string `json:"message"`
}
type Empty struct{}

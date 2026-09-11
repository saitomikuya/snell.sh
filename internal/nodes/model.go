package nodes

import "encoding/json"

// DefaultListenHost is used whenever a caller omits a node listen address.
// Wildcard binding keeps newly created proxy nodes reachable on both IPv4 and
// IPv6-capable hosts; callers can still explicitly opt into a loopback bind
// for a private ShadowTLS backend.
const DefaultListenHost = "0.0.0.0"

type Node struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	DesiredState   string `json:"desiredState"`
	RuntimeVersion string `json:"runtimeVersion"`
	ListenHost     string `json:"listenHost"`
	ListenPort     int    `json:"listenPort"`
	BackendNodeID  string `json:"backendNodeId,omitempty"`
	Config         Config `json:"config"`
	Revision       int    `json:"revision"`
	ActualState    string `json:"actualState"`
	LastError      string `json:"lastError,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type Config struct {
	Version                 string `json:"version,omitempty"`
	DNS                     string `json:"dns,omitempty"`
	ChinaDirectDNS          string `json:"chinaDirectDns,omitempty"`
	IPv6                    bool   `json:"ipv6,omitempty"`
	TFO                     bool   `json:"tfo,omitempty"`
	Public                  bool   `json:"public,omitempty"`
	Mode                    string `json:"mode,omitempty"`
	Method                  string `json:"method,omitempty"`
	Obfs                    string `json:"obfs,omitempty"`
	ObfsHost                string `json:"obfsHost,omitempty"`
	BlockMainland           bool   `json:"blockMainland,omitempty"`
	SNI                     string `json:"sni,omitempty"`
	WildcardSNI             string `json:"wildcardSni,omitempty"`
	UDPEnabled              bool   `json:"udpEnabled,omitempty"`
	ServerName              string `json:"serverName,omitempty"`
	VPNNetwork              string `json:"vpnNetwork,omitempty"`
	MTU                     int    `json:"mtu,omitempty"`
	MaxClients              int    `json:"maxClients,omitempty"`
	MaxSameClients          int    `json:"maxSameClients,omitempty"`
	CertificateURL          string `json:"certificateUrl,omitempty"`
	PrivateKeyURL           string `json:"privateKeyUrl,omitempty"`
	CertificateUsername     string `json:"certificateUsername,omitempty"`
	CertificateSchedule     string `json:"certificateSchedule,omitempty"`
	CertificateScheduleDay  int    `json:"certificateScheduleDay,omitempty"`
	CertificateScheduleTime string `json:"certificateScheduleTime,omitempty"`
	ChinaCIDRSourceURL      string `json:"chinaCidrSourceUrl,omitempty"`
	ChinaCIDRSourceFormat   string `json:"chinaCidrSourceFormat,omitempty"`
	ChinaCIDRSchedule       string `json:"chinaCidrSchedule,omitempty"`
	ChinaCIDRScheduleDay    int    `json:"chinaCidrScheduleDay,omitempty"`
	ChinaCIDRScheduleTime   string `json:"chinaCidrScheduleTime,omitempty"`
}

type CreateRequest struct {
	Type                 string `json:"type"`
	Name                 string `json:"name"`
	RuntimeVersion       string `json:"runtimeVersion"`
	ListenHost           string `json:"listenHost"`
	ListenPort           int    `json:"listenPort"`
	BackendNodeID        string `json:"backendNodeId,omitempty"`
	Config               Config `json:"config"`
	Secret               string `json:"secret,omitempty"`
	CertificatePassword  string `json:"certificatePassword,omitempty"`
	PrivateKeyPassphrase string `json:"privateKeyPassphrase,omitempty"`
}
type UpdateRequest struct {
	Name                 string `json:"name"`
	RuntimeVersion       string `json:"runtimeVersion"`
	ListenHost           string `json:"listenHost"`
	ListenPort           int    `json:"listenPort"`
	BackendNodeID        string `json:"backendNodeId,omitempty"`
	Config               Config `json:"config"`
	Secret               string `json:"secret,omitempty"`
	CertificatePassword  string `json:"certificatePassword,omitempty"`
	PrivateKeyPassphrase string `json:"privateKeyPassphrase,omitempty"`
}

type AnyConnectSecrets struct {
	CertificatePassword  string `json:"certificatePassword"`
	PrivateKeyPassphrase string `json:"privateKeyPassphrase"`
}

func ConfigJSON(c Config) string { b, _ := json.Marshal(c); return string(b) }

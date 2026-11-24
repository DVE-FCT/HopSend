// Package pairingv1 describes pairing materials, independent of their transport.
package pairingv1

const (
	MaxJSONBytes  = 64 << 10
	MaxReplyBytes = 256 << 10
)

type Request struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	RequestID     string `json:"request_id"`
	SourceIP      string `json:"source_ip"`
	PublicKey     string `json:"public_key"`
	Name          string `json:"name,omitempty"`
}

type ReplyDevice struct {
	SchemaVersion     int    `json:"schema_version"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id"`
	Name              string `json:"name"`
	AgentURL          string `json:"agent_url"`
	SSHAddress        string `json:"ssh_address"`
	SSHUser           string `json:"ssh_user"`
	HostPublicKey     string `json:"host_public_key"`
	HostFingerprint   string `json:"host_fingerprint"`
	CASHA256          string `json:"ca_sha256"`
	ShareID           string `json:"share_id"`
	RemoteAgentConfig string `json:"remote_agent_config"`
}

// Connection contains file references only. It never embeds a Token or key.
type Connection struct {
	Name              string `json:"name"`
	AgentURL          string `json:"agent_url"`
	SSHAddress        string `json:"ssh_address"`
	SSHUser           string `json:"ssh_user"`
	CAFile            string `json:"ca_file"`
	TokenFile         string `json:"token_file"`
	KeyFile           string `json:"key_file"`
	KnownHostsFile    string `json:"known_hosts_file"`
	ShareID           string `json:"share_id"`
	RemoteAgentConfig string `json:"remote_agent_config"`
	ManageAgent       bool   `json:"manage_agent"`
}

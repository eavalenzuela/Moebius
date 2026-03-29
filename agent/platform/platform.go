package platform

// Platform abstracts OS-specific paths and operations.
type Platform interface {
	// Paths
	ConfigDir() string  // /etc/agent or C:\ProgramData\Agent
	BinaryDir() string  // /usr/local/bin or C:\Program Files\Agent
	DataDir() string    // /var/lib/agent or C:\ProgramData\Agent
	LogDir() string     // /var/log/agent or (empty on Windows — uses EventLog)
	RuntimeDir() string // /run/agent or (empty on Windows — uses named pipe)

	// Derived paths
	ConfigPath() string          // config.toml
	EnrollmentTokenPath() string // enrollment.token
	CACertPath() string          // ca.crt
	ClientCertPath() string      // client.crt
	ClientKeyPath() string       // client.key
	SocketPath() string          // agent.sock or \\.\pipe\agent
	AgentIDPath() string         // agent_id (persisted agent ID)
	CDMStatePath() string        // cdm.json (CDM state persistence)
	CDMAuditLogPath() string     // cdm-audit.log (CDM local audit)

	// Service
	ServiceName() string
}

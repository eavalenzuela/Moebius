package linux

import "path/filepath"

// Platform implements platform.Platform for Linux.
type Platform struct{}

func (p *Platform) ConfigDir() string  { return "/etc/moebius-agent" }
func (p *Platform) BinaryDir() string  { return "/usr/local/bin" }
func (p *Platform) DataDir() string    { return "/var/lib/moebius-agent" }
func (p *Platform) LogDir() string     { return "/var/log/moebius-agent" }
func (p *Platform) RuntimeDir() string { return "/run/moebius-agent" }

func (p *Platform) ConfigPath() string {
	return filepath.Join(p.ConfigDir(), "config.toml")
}

func (p *Platform) EnrollmentTokenPath() string {
	return filepath.Join(p.ConfigDir(), "enrollment.token")
}

func (p *Platform) CACertPath() string {
	return filepath.Join(p.ConfigDir(), "ca.crt")
}

func (p *Platform) ClientCertPath() string {
	return filepath.Join(p.ConfigDir(), "client.crt")
}

func (p *Platform) ClientKeyPath() string {
	return filepath.Join(p.ConfigDir(), "client.key")
}

func (p *Platform) SocketPath() string {
	return filepath.Join(p.RuntimeDir(), "moebius-agent.sock")
}

func (p *Platform) AgentIDPath() string {
	return filepath.Join(p.ConfigDir(), "agent_id")
}

func (p *Platform) CDMStatePath() string {
	return filepath.Join(p.DataDir(), "cdm.json")
}

func (p *Platform) CDMAuditLogPath() string {
	return filepath.Join(p.DataDir(), "cdm-audit.log")
}

func (p *Platform) LocalAuditLogPath() string {
	return filepath.Join(p.DataDir(), "local-audit.log")
}

func (p *Platform) DropDir() string { return "/opt/moebius-agent/drop" }

func (p *Platform) BinaryPath() string { return filepath.Join(p.BinaryDir(), "moebius-agent") }
func (p *Platform) BinaryStagingPath() string {
	return filepath.Join(p.BinaryDir(), "moebius-agent.new")
}
func (p *Platform) BinaryPreviousPath() string {
	return filepath.Join(p.BinaryDir(), "moebius-agent.previous")
}
func (p *Platform) PendingUpdatePath() string {
	return filepath.Join(p.DataDir(), "pending_update.json")
}

func (p *Platform) ServiceName() string { return "moebius-agent" }

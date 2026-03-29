package windows

import "path/filepath"

// Platform implements platform.Platform for Windows.
type Platform struct{}

func (p *Platform) ConfigDir() string  { return `C:\ProgramData\MoebiusAgent` }
func (p *Platform) BinaryDir() string  { return `C:\Program Files\MoebiusAgent` }
func (p *Platform) DataDir() string    { return `C:\ProgramData\MoebiusAgent` }
func (p *Platform) LogDir() string     { return "" } // Windows uses EventLog
func (p *Platform) RuntimeDir() string { return "" } // Windows uses named pipes

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
	return `\\.\pipe\moebius-agent`
}

func (p *Platform) AgentIDPath() string {
	return filepath.Join(p.ConfigDir(), "agent_id")
}

func (p *Platform) ServiceName() string { return "MoebiusAgent" }

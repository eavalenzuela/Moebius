package localcli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/eavalenzuela/Moebius/agent/ipc"
	"github.com/eavalenzuela/Moebius/agent/localauth"
)

// CLI connects to the agent daemon over IPC and runs local commands.
type CLI struct {
	socketPath string
	client     *ipc.Client
	token      string // session token after login
}

// New creates a CLI that connects to the given socket/pipe path.
func New(socketPath string) *CLI {
	return &CLI{socketPath: socketPath}
}

// connect establishes the IPC connection if not already connected.
func (c *CLI) connect() error {
	if c.client != nil {
		return nil
	}
	client, err := ipc.NewClient(c.socketPath)
	if err != nil {
		return fmt.Errorf("connect to agent: %w\n(is the agent running?)", err)
	}
	c.client = client
	return nil
}

// Close closes the IPC connection.
func (c *CLI) Close() {
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
}

// Login authenticates with the agent using OS credentials.
// Accepts username/password directly (for flags/env) or prompts interactively.
func (c *CLI) Login(username, password string) error {
	if err := c.connect(); err != nil {
		return err
	}

	if username == "" {
		username = os.Getenv("MOEBIUS_USERNAME")
	}
	if password == "" {
		password = os.Getenv("MOEBIUS_PASSWORD")
	}
	if username == "" {
		fmt.Print("Username: ")
		if _, err := fmt.Scanln(&username); err != nil {
			return fmt.Errorf("read username: %w", err)
		}
	}
	if password == "" {
		fmt.Print("Password: ")
		if _, err := fmt.Scanln(&password); err != nil {
			return fmt.Errorf("read password: %w", err)
		}
	}

	var result localauth.LoginResult
	err := c.client.Call("auth.login", localauth.LoginParams{
		Username: username,
		Password: password,
	}, &result)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	c.token = result.Token
	return nil
}

// authedCall makes an authenticated IPC call, including the session token.
func (c *CLI) authedCall(method string, params, dest any) error {
	if err := c.connect(); err != nil {
		return err
	}
	if c.token == "" {
		return fmt.Errorf("not authenticated (run login first)")
	}
	return c.client.CallWithToken(method, c.token, params, dest)
}

// RunStatus prints the agent status.
func (c *CLI) RunStatus() error {
	var result StatusResult
	if err := c.authedCall("agent.status", nil, &result); err != nil {
		return err
	}

	fmt.Printf("Agent ID:       %s\n", result.AgentID)
	fmt.Printf("Version:        %s\n", result.Version)
	fmt.Printf("Server URL:     %s\n", result.ServerURL)
	fmt.Printf("Poll Interval:  %ds\n", result.PollInterval)
	fmt.Printf("CDM Enabled:    %v\n", result.CDMEnabled)
	fmt.Printf("Session Active: %v\n", result.SessionActive)
	return nil
}

// RunCDMStatus prints CDM state.
func (c *CLI) RunCDMStatus() error {
	var result CDMStatusResult
	if err := c.authedCall("cdm.status", nil, &result); err != nil {
		return err
	}

	fmt.Printf("CDM Enabled:    %v\n", result.Enabled)
	fmt.Printf("Session Active: %v\n", result.SessionActive)
	if result.SessionExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, result.SessionExpiresAt); err == nil {
			remaining := time.Until(t).Truncate(time.Second)
			fmt.Printf("Session Expires: %s (%s remaining)\n", result.SessionExpiresAt, remaining)
		}
	}
	if result.SessionGrantedBy != "" {
		fmt.Printf("Granted By:     %s\n", result.SessionGrantedBy)
	}
	if result.SessionGrantedAt != "" {
		fmt.Printf("Granted At:     %s\n", result.SessionGrantedAt)
	}
	return nil
}

// RunCDMEnable enables CDM.
func (c *CLI) RunCDMEnable(username string) error {
	return c.authedCall("cdm.enable", map[string]string{"actor": username}, nil)
}

// RunCDMDisable disables CDM.
func (c *CLI) RunCDMDisable(username string) error {
	return c.authedCall("cdm.disable", map[string]string{"actor": username}, nil)
}

// RunCDMGrant grants a CDM session.
func (c *CLI) RunCDMGrant(username, duration string) error {
	return c.authedCall("cdm.grant", map[string]string{
		"actor":    username,
		"duration": duration,
	}, nil)
}

// RunCDMRevoke revokes a CDM session.
func (c *CLI) RunCDMRevoke(username string) error {
	return c.authedCall("cdm.revoke", map[string]string{"actor": username}, nil)
}

// RunLogs prints recent agent log lines.
func (c *CLI) RunLogs(tail int) error {
	var result LogsResult
	if err := c.authedCall("agent.logs", LogsParams{Tail: tail}, &result); err != nil {
		return err
	}

	if len(result.Lines) == 0 {
		fmt.Println("(no log entries)")
		return nil
	}
	fmt.Println(strings.Join(result.Lines, "\n"))
	return nil
}

// RunAudit prints CDM audit log entries.
func (c *CLI) RunAudit() error {
	var result AuditResult
	if err := c.authedCall("agent.audit", nil, &result); err != nil {
		return err
	}

	if len(result.Entries) == 0 {
		fmt.Println("(no audit entries)")
		return nil
	}
	for _, e := range result.Entries {
		line := fmt.Sprintf("%s  %-24s  %-6s  %-12s",
			e.Timestamp.Format("2006-01-02 15:04:05"),
			e.Action,
			e.Interface,
			e.Username,
		)
		var details []string
		if e.OldState != "" {
			details = append(details, e.OldState+" → "+e.NewState)
		}
		if e.Duration != "" {
			details = append(details, "duration: "+e.Duration)
		}
		if e.Reason != "" {
			details = append(details, "reason: "+e.Reason)
		}
		if len(details) > 0 {
			line += "  " + strings.Join(details, ", ")
		}
		fmt.Println(line)
	}
	return nil
}

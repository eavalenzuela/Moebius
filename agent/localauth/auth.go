// Package localauth provides OS-level authentication for the agent's local
// interfaces (CLI and web UI). On Linux it uses PAM; on Windows it uses
// LogonUser via advapi32.dll.
package localauth

// Authenticator validates OS user credentials.
type Authenticator interface {
	// Authenticate verifies username and password against the OS.
	// Returns nil on success, or an error describing the failure.
	Authenticate(username, password string) error
}

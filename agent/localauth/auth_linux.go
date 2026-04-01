//go:build linux && cgo

package localauth

import (
	"fmt"

	"github.com/msteinert/pam/v2"
)

// PAMAuthenticator validates credentials via PAM.
type PAMAuthenticator struct {
	ServiceName string // PAM service name (e.g. "login", "moebius-agent")
}

// NewPAMAuthenticator creates an authenticator using the given PAM service.
// Falls back to "login" if serviceName is empty.
func NewPAMAuthenticator(serviceName string) *PAMAuthenticator {
	if serviceName == "" {
		serviceName = "login"
	}
	return &PAMAuthenticator{ServiceName: serviceName}
}

// Authenticate validates username/password via PAM.
func (a *PAMAuthenticator) Authenticate(username, password string) error {
	tx, err := pam.StartFunc(a.ServiceName, username, func(style pam.Style, _ string) (string, error) {
		switch style {
		case pam.PromptEchoOff:
			return password, nil
		case pam.PromptEchoOn:
			return username, nil
		default:
			return "", fmt.Errorf("unsupported PAM prompt style: %d", style)
		}
	})
	if err != nil {
		return fmt.Errorf("pam start: %w", err)
	}

	if err := tx.Authenticate(0); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	if err := tx.AcctMgmt(0); err != nil {
		return fmt.Errorf("account check failed: %w", err)
	}

	return nil
}

// NewPlatformAuthenticator returns the default Authenticator for Linux.
func NewPlatformAuthenticator() Authenticator {
	return NewPAMAuthenticator("")
}

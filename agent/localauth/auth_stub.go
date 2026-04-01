//go:build (linux && !cgo) || (!linux && !windows)

package localauth

import "fmt"

// StubAuthenticator is a fallback when PAM (CGo) is not available.
// It always returns an error directing the operator to build with CGo.
type StubAuthenticator struct{}

// Authenticate always fails — PAM is not available in this build.
func (s *StubAuthenticator) Authenticate(_, _ string) error {
	return fmt.Errorf("OS authentication unavailable: build with CGo enabled for PAM support")
}

// NewPlatformAuthenticator returns the stub authenticator.
func NewPlatformAuthenticator() Authenticator {
	return &StubAuthenticator{}
}

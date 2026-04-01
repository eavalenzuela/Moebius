//go:build windows

package localauth

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32         = windows.NewLazySystemDLL("advapi32.dll")
	procLogonUserW   = advapi32.NewProc("LogonUserW")
	procCloseHandle_ = advapi32.NewProc("CloseHandle")
)

const (
	logon32LogonInteractive = 2
	logon32ProviderDefault  = 0
)

// WindowsAuthenticator validates credentials via LogonUser.
type WindowsAuthenticator struct{}

// NewWindowsAuthenticator creates a WindowsAuthenticator.
func NewWindowsAuthenticator() *WindowsAuthenticator {
	return &WindowsAuthenticator{}
}

// Authenticate validates username/password via LogonUser.
// The domain is "." (local machine).
func (a *WindowsAuthenticator) Authenticate(username, password string) error {
	usernamePtr, err := syscall.UTF16PtrFromString(username)
	if err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	domainPtr, err := syscall.UTF16PtrFromString(".")
	if err != nil {
		return fmt.Errorf("invalid domain: %w", err)
	}

	passwordPtr, err := syscall.UTF16PtrFromString(password)
	if err != nil {
		return fmt.Errorf("invalid password: %w", err)
	}

	var token syscall.Handle
	ret, _, callErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(usernamePtr)),
		uintptr(unsafe.Pointer(domainPtr)),
		uintptr(unsafe.Pointer(passwordPtr)),
		logon32LogonInteractive,
		logon32ProviderDefault,
		uintptr(unsafe.Pointer(&token)),
	)

	if ret == 0 {
		return fmt.Errorf("authentication failed: %w", callErr)
	}

	// Close the logon token — we only needed to verify credentials.
	_ = syscall.CloseHandle(token)
	return nil
}

// NewPlatformAuthenticator returns the default Authenticator for Windows.
func NewPlatformAuthenticator() Authenticator {
	return NewWindowsAuthenticator()
}

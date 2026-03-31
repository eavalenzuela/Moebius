package platform

// PackageManager abstracts OS-specific package management operations.
// Implementations detect the available package manager at runtime and
// invoke it via a privilege-escalation mechanism (setuid helper on Linux,
// service account on Windows).
type PackageManager interface {
	// Install installs a package. If version is empty, the latest is used.
	Install(name, version string) PackageResult
	// Remove removes an installed package.
	Remove(name string) PackageResult
	// Update updates a package. If version is empty, the latest is used.
	Update(name, version string) PackageResult
	// DetectedManager returns the name of the detected package manager
	// (e.g. "apt", "dnf", "winget", "choco") or "" if none found.
	DetectedManager() string
}

// PackageResult captures the outcome of a package management operation.
type PackageResult struct {
	Success  bool
	ExitCode int
	Stdout   string
	Stderr   string
	Error    string // non-empty on failure
}

//go:build linux

package linux

import (
	"testing"
)

func TestBuildHelperArgs(t *testing.T) {
	tests := []struct {
		name    string
		manager string
		action  string
		pkg     string
		version string
		want    []string
	}{
		{"install no version", "apt", "install", "nginx", "", []string{"apt", "install", "nginx"}},
		{"install with version", "dnf", "install", "httpd", "2.4.6", []string{"dnf", "install", "httpd", "2.4.6"}},
		{"remove", "apt", "remove", "nginx", "", []string{"apt", "remove", "nginx"}},
		{"update", "dnf", "update", "httpd", "2.4.7", []string{"dnf", "update", "httpd", "2.4.7"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildHelperArgs(tt.manager, tt.action, tt.pkg, tt.version)
			if len(got) != len(tt.want) {
				t.Fatalf("len=%d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("arg[%d]=%q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestValidateHelperArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"valid apt install", []string{"apt", "install", "nginx"}, false},
		{"valid dnf install with version", []string{"dnf", "install", "httpd", "2.4.6"}, false},
		{"valid apt remove", []string{"apt", "remove", "curl"}, false},
		{"valid dnf update", []string{"dnf", "update", "vim"}, false},
		{"too few args", []string{"apt", "install"}, true},
		{"too many args", []string{"apt", "install", "pkg", "1.0", "extra"}, true},
		{"bad manager", []string{"yum", "install", "nginx"}, true},
		{"bad action", []string{"apt", "purge", "nginx"}, true},
		{"empty package", []string{"apt", "install", ""}, true},
		{"shell metachar semicolon", []string{"apt", "install", "nginx;rm -rf /"}, true},
		{"shell metachar pipe", []string{"apt", "install", "pkg|evil"}, true},
		{"shell metachar backtick", []string{"apt", "install", "pkg`cmd`"}, true},
		{"shell metachar space", []string{"apt", "install", "pkg name"}, true},
		{"bad version metachar", []string{"apt", "install", "nginx", "1.0;evil"}, true},
		{"version with pipe", []string{"apt", "install", "nginx", "1.0|bad"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHelperArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHelperArgs(%v) error=%v, wantErr=%v", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestBuildNativeArgs_Apt(t *testing.T) {
	tests := []struct {
		action  string
		name    string
		version string
		wantBin string
		wantLen int
	}{
		{"install", "nginx", "", "apt-get", 4},
		{"install", "nginx", "1.18.0-0ubuntu1", "apt-get", 4},
		{"remove", "nginx", "", "apt-get", 3},
		{"update", "nginx", "", "apt-get", 4},
	}
	for _, tt := range tests {
		t.Run(tt.action+"_"+tt.name, func(t *testing.T) {
			bin, args, err := BuildNativeArgs("apt", tt.action, tt.name, tt.version)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bin != tt.wantBin {
				t.Errorf("binary=%q, want %q", bin, tt.wantBin)
			}
			if len(args) != tt.wantLen {
				t.Errorf("args len=%d, want %d: %v", len(args), tt.wantLen, args)
			}
		})
	}
}

func TestBuildNativeArgs_Dnf(t *testing.T) {
	tests := []struct {
		action  string
		name    string
		version string
		wantBin string
	}{
		{"install", "httpd", "", "dnf"},
		{"install", "httpd", "2.4.6", "dnf"},
		{"remove", "httpd", "", "dnf"},
		{"update", "httpd", "2.4.7", "dnf"},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			bin, _, err := BuildNativeArgs("dnf", tt.action, tt.name, tt.version)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bin != tt.wantBin {
				t.Errorf("binary=%q, want %q", bin, tt.wantBin)
			}
		})
	}
}

func TestBuildNativeArgs_Errors(t *testing.T) {
	_, _, err := BuildNativeArgs("yum", "install", "pkg", "")
	if err == nil {
		t.Error("expected error for unsupported manager")
	}

	_, _, err = BuildNativeArgs("apt", "purge", "pkg", "")
	if err == nil {
		t.Error("expected error for unsupported action")
	}
}

func TestBuildAptArgs_VersionFormat(t *testing.T) {
	_, args, err := buildAptArgs("install", "nginx", "1.18.0")
	if err != nil {
		t.Fatal(err)
	}
	// Should contain "nginx=1.18.0"
	found := false
	for _, a := range args {
		if a == "nginx=1.18.0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'nginx=1.18.0' in args: %v", args)
	}
}

func TestBuildDnfArgs_VersionFormat(t *testing.T) {
	_, args, err := buildDnfArgs("install", "httpd", "2.4.6")
	if err != nil {
		t.Fatal(err)
	}
	// Should contain "httpd-2.4.6"
	found := false
	for _, a := range args {
		if a == "httpd-2.4.6" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'httpd-2.4.6' in args: %v", args)
	}
}

func TestNewPkgManager_ManagerHint(t *testing.T) {
	m := NewPkgManager("dnf", "/some/path")
	if m.DetectedManager() != "dnf" {
		t.Errorf("expected dnf, got %s", m.DetectedManager())
	}
}

func TestNewPkgManager_NoManager(t *testing.T) {
	m := &PkgManager{manager: "", helperPath: "/nonexistent"}
	result := m.Install("nginx", "")
	if result.Success {
		t.Error("expected failure with no manager")
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
}

//go:build linux

package inventory

import (
	"bufio"
	"os/exec"
	"strings"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

func collectPackages() ([]protocol.PackageRef, error) {
	var pkgs []protocol.PackageRef

	// Try dpkg (Debian/Ubuntu)
	if dpkgPkgs, err := collectDpkg(); err == nil {
		pkgs = append(pkgs, dpkgPkgs...)
	}

	// Try rpm (RHEL/Fedora)
	if rpmPkgs, err := collectRPM(); err == nil {
		pkgs = append(pkgs, rpmPkgs...)
	}

	return pkgs, nil
}

func collectDpkg() ([]protocol.PackageRef, error) {
	cmd := exec.Command("dpkg-query", "-W", "-f=${Package}\t${Version}\n") //nolint:gosec // fixed command
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTSV(string(out), "apt"), nil
}

func collectRPM() ([]protocol.PackageRef, error) {
	cmd := exec.Command("rpm", "-qa", "--queryformat", "%{NAME}\t%{VERSION}-%{RELEASE}\n") //nolint:gosec // fixed command
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTSV(string(out), "rpm"), nil
}

func parseTSV(output, manager string) []protocol.PackageRef {
	var pkgs []protocol.PackageRef
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		pkgs = append(pkgs, protocol.PackageRef{
			Name:    parts[0],
			Version: parts[1],
			Manager: manager,
		})
	}
	return pkgs
}

//go:build windows

package inventory

import "github.com/eavalenzuela/Moebius/shared/protocol"

func collectPackages() ([]protocol.PackageRef, error) {
	// TODO: implement Windows package enumeration via registry/WMI
	return nil, nil
}

//go:build windows

package inventory

import "github.com/moebius-oss/moebius/shared/protocol"

func collectPackages() ([]protocol.PackageRef, error) {
	// TODO: implement Windows package enumeration via registry/WMI
	return nil, nil
}

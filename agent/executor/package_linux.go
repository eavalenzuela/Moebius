//go:build linux

package executor

import (
	"github.com/eavalenzuela/Moebius/agent/platform"
	"github.com/eavalenzuela/Moebius/agent/platform/linux"
)

func (e *Executor) getPackageManager(managerHint string) platform.PackageManager {
	return linux.NewPkgManager(managerHint, "")
}

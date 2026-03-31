//go:build windows

package executor

import (
	"github.com/eavalenzuela/Moebius/agent/platform"
	"github.com/eavalenzuela/Moebius/agent/platform/windows"
)

func (e *Executor) getPackageManager(managerHint string) platform.PackageManager {
	return windows.NewPkgManager(managerHint)
}

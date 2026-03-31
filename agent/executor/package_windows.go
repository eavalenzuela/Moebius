//go:build windows

package executor

import (
	agentplatform "github.com/eavalenzuela/Moebius/agent/platform"
	"github.com/eavalenzuela/Moebius/agent/platform/windows"
)

func (e *Executor) getPackageManager(managerHint string) agentplatform.PackageManager {
	return windows.NewPkgManager(managerHint)
}

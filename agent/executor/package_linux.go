//go:build linux

package executor

import (
	agentplatform "github.com/eavalenzuela/Moebius/agent/platform"
	"github.com/eavalenzuela/Moebius/agent/platform/linux"
)

func (e *Executor) getPackageManager(managerHint string) agentplatform.PackageManager {
	return linux.NewPkgManager(managerHint, "")
}

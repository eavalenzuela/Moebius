package executor

import (
	"encoding/json"
	"log/slog"

	"github.com/eavalenzuela/Moebius/agent/platform"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// executePackageInstall handles the "package_install" job type.
func (e *Executor) executePackageInstall(payload json.RawMessage) protocol.JobResultSubmission {
	var p protocol.PackageInstallPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "invalid package_install payload: " + err.Error(),
		}
	}
	if p.Name == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "package name is required",
		}
	}

	mgr := e.resolvePackageManager(p.Manager)
	if mgr == nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "package manager not available",
		}
	}

	result := mgr.Install(p.Name, p.Version)
	return e.packageResultToJob(result, "install", p.Name)
}

// executePackageRemove handles the "package_remove" job type.
func (e *Executor) executePackageRemove(payload json.RawMessage) protocol.JobResultSubmission {
	var p protocol.PackageRemovePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "invalid package_remove payload: " + err.Error(),
		}
	}
	if p.Name == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "package name is required",
		}
	}

	mgr := e.resolvePackageManager(p.Manager)
	if mgr == nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "package manager not available",
		}
	}

	result := mgr.Remove(p.Name)
	return e.packageResultToJob(result, "remove", p.Name)
}

// executePackageUpdate handles the "package_update" job type.
func (e *Executor) executePackageUpdate(payload json.RawMessage) protocol.JobResultSubmission {
	var p protocol.PackageUpdatePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "invalid package_update payload: " + err.Error(),
		}
	}
	if p.Name == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "package name is required",
		}
	}

	mgr := e.resolvePackageManager(p.Manager)
	if mgr == nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "package manager not available",
		}
	}

	result := mgr.Update(p.Name, p.Version)
	return e.packageResultToJob(result, "update", p.Name)
}

// resolvePackageManager returns the injected package manager (for testing)
// or falls back to the platform-specific implementation.
func (e *Executor) resolvePackageManager(managerHint string) platform.PackageManager {
	if e.pkgMgr != nil {
		return e.pkgMgr
	}
	return e.getPackageManager(managerHint)
}

// packageResultToJob converts a platform.PackageResult to a protocol.JobResultSubmission.
// On success, it also triggers an inventory refresh so the next check-in
// includes the package delta.
func (e *Executor) packageResultToJob(r platform.PackageResult, action, pkgName string) protocol.JobResultSubmission {
	exitCode := r.ExitCode

	if r.Success {
		// Trigger inventory refresh so the next check-in picks up the delta
		if e.inventory != nil {
			e.inventory.ComputeDelta()
			e.log.Info("inventory delta refreshed after package operation",
				slog.String("action", action), slog.String("package", pkgName))
		}
		return protocol.JobResultSubmission{
			Status:   "completed",
			ExitCode: &exitCode,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Message:  action + " succeeded for " + pkgName,
		}
	}

	return protocol.JobResultSubmission{
		Status:   "failed",
		ExitCode: &exitCode,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Message:  r.Error,
	}
}

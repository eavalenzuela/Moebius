package executor

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	agentplatform "github.com/eavalenzuela/Moebius/agent/platform"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// mockPackageManager implements platform.PackageManager for testing.
type mockPackageManager struct {
	manager string
	result  agentplatform.PackageResult
	calls   []mockCall
}

type mockCall struct {
	action  string
	name    string
	version string
}

func (m *mockPackageManager) Install(name, version string) agentplatform.PackageResult {
	m.calls = append(m.calls, mockCall{"install", name, version})
	return m.result
}

func (m *mockPackageManager) Remove(name string) agentplatform.PackageResult {
	m.calls = append(m.calls, mockCall{"remove", name, ""})
	return m.result
}

func (m *mockPackageManager) Update(name, version string) agentplatform.PackageResult {
	m.calls = append(m.calls, mockCall{"update", name, version})
	return m.result
}

func (m *mockPackageManager) DetectedManager() string { return m.manager }

// testExecutor creates an Executor with a mock package manager injected.
func testExecutor(mgr *mockPackageManager) *Executor {
	e := &Executor{log: slog.Default()}
	e.pkgMgr = mgr
	return e
}

func TestExecutePackageInstall_Success(t *testing.T) {
	mgr := &mockPackageManager{
		manager: "apt",
		result:  agentplatform.PackageResult{Success: true, Stdout: "installed ok"},
	}
	e := testExecutor(mgr)

	payload, _ := json.Marshal(protocol.PackageInstallPayload{Name: "nginx", Version: "1.18.0"})
	result := e.executePackageInstall(payload)

	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s: %s", result.Status, result.Message)
	}
	if len(mgr.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mgr.calls))
	}
	if mgr.calls[0].name != "nginx" || mgr.calls[0].version != "1.18.0" {
		t.Errorf("wrong call args: %+v", mgr.calls[0])
	}
}

func TestExecutePackageInstall_Failure(t *testing.T) {
	mgr := &mockPackageManager{
		manager: "apt",
		result:  agentplatform.PackageResult{ExitCode: 100, Error: "package not found", Stderr: "E: Unable to locate package"},
	}
	e := testExecutor(mgr)

	payload, _ := json.Marshal(protocol.PackageInstallPayload{Name: "nonexistent"})
	result := e.executePackageInstall(payload)

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
	if result.ExitCode == nil || *result.ExitCode != 100 {
		t.Errorf("expected exit code 100, got %v", result.ExitCode)
	}
}

func TestExecutePackageInstall_InvalidPayload(t *testing.T) {
	e := &Executor{log: slog.Default()}
	result := e.executePackageInstall([]byte("not json"))

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
}

func TestExecutePackageInstall_EmptyName(t *testing.T) {
	e := &Executor{log: slog.Default()}
	payload, _ := json.Marshal(protocol.PackageInstallPayload{Name: ""})
	result := e.executePackageInstall(payload)

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
	if result.Message != "package name is required" {
		t.Errorf("unexpected message: %s", result.Message)
	}
}

func TestExecutePackageRemove_Success(t *testing.T) {
	mgr := &mockPackageManager{
		manager: "dnf",
		result:  agentplatform.PackageResult{Success: true, Stdout: "removed"},
	}
	e := testExecutor(mgr)

	payload, _ := json.Marshal(protocol.PackageRemovePayload{Name: "httpd"})
	result := e.executePackageRemove(payload)

	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s: %s", result.Status, result.Message)
	}
	if mgr.calls[0].action != "remove" || mgr.calls[0].name != "httpd" {
		t.Errorf("wrong call: %+v", mgr.calls[0])
	}
}

func TestExecutePackageRemove_InvalidPayload(t *testing.T) {
	e := &Executor{log: slog.Default()}
	result := e.executePackageRemove([]byte("{invalid"))

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
}

func TestExecutePackageRemove_EmptyName(t *testing.T) {
	e := &Executor{log: slog.Default()}
	payload, _ := json.Marshal(protocol.PackageRemovePayload{Name: ""})
	result := e.executePackageRemove(payload)

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
}

func TestExecutePackageUpdate_Success(t *testing.T) {
	mgr := &mockPackageManager{
		manager: "apt",
		result:  agentplatform.PackageResult{Success: true, Stdout: "upgraded"},
	}
	e := testExecutor(mgr)

	payload, _ := json.Marshal(protocol.PackageUpdatePayload{Name: "curl", Version: "7.88.1"})
	result := e.executePackageUpdate(payload)

	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s: %s", result.Status, result.Message)
	}
	if mgr.calls[0].action != "update" || mgr.calls[0].version != "7.88.1" {
		t.Errorf("wrong call: %+v", mgr.calls[0])
	}
}

func TestExecutePackageUpdate_InvalidPayload(t *testing.T) {
	e := &Executor{log: slog.Default()}
	result := e.executePackageUpdate([]byte(""))

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
}

func TestExecutePackageUpdate_EmptyName(t *testing.T) {
	e := &Executor{log: slog.Default()}
	payload, _ := json.Marshal(protocol.PackageUpdatePayload{Name: ""})
	result := e.executePackageUpdate(payload)

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
}

func TestExecutePackage_NilManager(t *testing.T) {
	// No pkgMgr set, getPackageManager returns platform impl which may not have a manager
	e := &Executor{log: slog.Default()}
	e.pkgMgr = nil // ensure nil

	payload, _ := json.Marshal(protocol.PackageInstallPayload{Name: "nginx"})
	result := e.executePackageInstall(payload)

	// Should fail because no manager is set
	if result.Status != "failed" {
		t.Fatalf("expected failed with nil pkgMgr, got %s", result.Status)
	}
}

func TestExecute_PackageJobTypes(t *testing.T) {
	mgr := &mockPackageManager{
		manager: "apt",
		result:  agentplatform.PackageResult{Success: true},
	}
	e := testExecutor(mgr)

	types := []struct {
		jobType string
		payload interface{}
	}{
		{"package_install", protocol.PackageInstallPayload{Name: "nginx"}},
		{"package_remove", protocol.PackageRemovePayload{Name: "nginx"}},
		{"package_update", protocol.PackageUpdatePayload{Name: "nginx"}},
	}

	for _, tt := range types {
		t.Run(tt.jobType, func(t *testing.T) {
			p, _ := json.Marshal(tt.payload)
			result := e.execute(context.TODO(), protocol.JobDispatch{
				JobID:   "test-" + tt.jobType,
				Type:    tt.jobType,
				Payload: p,
			})
			if result.Status != "completed" {
				t.Errorf("expected completed for %s, got %s: %s", tt.jobType, result.Status, result.Message)
			}
		})
	}
}

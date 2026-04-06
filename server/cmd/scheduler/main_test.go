package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestScheduler_NoAuthzImports is a security regression test for invariant I3
// ("all RBAC enforcement is in the API server; background processors trust
// pre-authorized jobs"). The scheduler creates jobs from cron triggers and
// reaps stuck jobs — both operate on rows that were authorized at creation
// time (by the API handler that accepted them) and neither path should need
// to consult RBAC. If a future scheduler change imports server/rbac,
// server/auth, or any API handler package, this test fails and forces a
// review of whether re-authorization is actually needed (it usually isn't,
// and introduces inconsistency risk).
//
// See SEC_VALIDATION.md invariant I3.
func TestScheduler_NoAuthzImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	forbidden := []string{
		"github.com/eavalenzuela/Moebius/server/rbac",
		"github.com/eavalenzuela/Moebius/server/auth",
		"github.com/eavalenzuela/Moebius/server/api",
	}

	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	depSet := make(map[string]struct{}, len(deps))
	for _, d := range deps {
		depSet[d] = struct{}{}
	}

	for _, pkg := range forbidden {
		if _, ok := depSet[pkg]; ok {
			t.Errorf("scheduler binary transitively imports %q — violates invariant I3 "+
				"(background processors must not perform authorization; they trust pre-authorized jobs)", pkg)
		}
	}
}

package auth

import (
	"testing"

	"github.com/eavalenzuela/Moebius/shared/models"
)

func TestDeviceInScope_NilAllowed(t *testing.T) {
	if !DeviceInScope(nil, "any-device") {
		t.Error("nil allowed (unscoped) should allow any device")
	}
}

func TestDeviceInScope_InSet(t *testing.T) {
	allowed := map[string]struct{}{"dev-1": {}, "dev-2": {}}
	if !DeviceInScope(allowed, "dev-1") {
		t.Error("dev-1 should be in scope")
	}
}

func TestDeviceInScope_NotInSet(t *testing.T) {
	allowed := map[string]struct{}{"dev-1": {}}
	if DeviceInScope(allowed, "dev-99") {
		t.Error("dev-99 should NOT be in scope")
	}
}

func TestFilterDeviceIDs_NilAllowed(t *testing.T) {
	ids := []string{"a", "b", "c"}
	got := FilterDeviceIDs(nil, ids)
	if len(got) != 3 {
		t.Errorf("nil allowed should return all, got %d", len(got))
	}
}

func TestFilterDeviceIDs_Filters(t *testing.T) {
	allowed := map[string]struct{}{"a": {}, "c": {}}
	got := FilterDeviceIDs(allowed, []string{"a", "b", "c", "d"})
	if len(got) != 2 {
		t.Fatalf("expected 2 filtered IDs, got %d", len(got))
	}
	if got[0] != "a" || got[1] != "c" {
		t.Errorf("expected [a, c], got %v", got)
	}
}

func TestFilterDeviceIDs_AllOutOfScope(t *testing.T) {
	allowed := map[string]struct{}{"x": {}}
	got := FilterDeviceIDs(allowed, []string{"a", "b"})
	if got != nil {
		t.Errorf("expected nil (no matches), got %v", got)
	}
}

func TestScopeHasField(t *testing.T) {
	scope := &models.APIScope{GroupIDs: []string{"g1"}}
	if !ScopeHasField(scope, "groups") {
		t.Error("scope with GroupIDs should have groups field")
	}
	if ScopeHasField(scope, "tags") {
		t.Error("scope without TagIDs should not have tags field")
	}
	if ScopeHasField(nil, "groups") {
		t.Error("nil scope should not have any field")
	}
}

func TestIDInScopeField_NilScope(t *testing.T) {
	if !IDInScopeField(nil, "groups", "any") {
		t.Error("nil scope should allow any ID")
	}
}

func TestIDInScopeField_EmptyField(t *testing.T) {
	scope := &models.APIScope{GroupIDs: []string{"g1"}} // tags empty
	if !IDInScopeField(scope, "tags", "any-tag") {
		t.Error("empty field should allow any ID")
	}
}

func TestIDInScopeField_InList(t *testing.T) {
	scope := &models.APIScope{GroupIDs: []string{"g1", "g2"}}
	if !IDInScopeField(scope, "groups", "g2") {
		t.Error("g2 should be in scope")
	}
}

func TestIDInScopeField_NotInList(t *testing.T) {
	scope := &models.APIScope{GroupIDs: []string{"g1"}}
	if IDInScopeField(scope, "groups", "g99") {
		t.Error("g99 should NOT be in scope")
	}
}

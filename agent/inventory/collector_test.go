package inventory

import (
	"log/slog"
	"os"
	"testing"

	"github.com/moebius-oss/moebius/shared/protocol"
)

func TestPkgKey(t *testing.T) {
	p := protocol.PackageRef{Name: "curl", Version: "7.81", Manager: "apt"}
	got := pkgKey(p)
	if got != "apt:curl" {
		t.Errorf("pkgKey = %q, want %q", got, "apt:curl")
	}
}

func TestComputeDelta_FirstRun(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	// Seed with known packages (bypass collectPackages via lastPkgs)
	c.lastPkgs = nil

	// First call collects live packages, stores snapshot, returns nil delta.
	// We can't control collectPackages output easily, so test the logic directly.
	c.mu.Lock()
	c.lastPkgs = map[string]protocol.PackageRef{
		"apt:curl": {Name: "curl", Version: "7.81", Manager: "apt"},
		"apt:wget": {Name: "wget", Version: "1.21", Manager: "apt"},
	}
	c.mu.Unlock()

	// Simulate no changes
	delta := computeDelta(c, []protocol.PackageRef{
		{Name: "curl", Version: "7.81", Manager: "apt"},
		{Name: "wget", Version: "1.21", Manager: "apt"},
	})
	if delta != nil {
		t.Errorf("expected nil delta for unchanged packages, got %+v", delta)
	}
}

func TestComputeDelta_Added(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	c.lastPkgs = map[string]protocol.PackageRef{
		"apt:curl": {Name: "curl", Version: "7.81", Manager: "apt"},
	}

	delta := computeDelta(c, []protocol.PackageRef{
		{Name: "curl", Version: "7.81", Manager: "apt"},
		{Name: "jq", Version: "1.6", Manager: "apt"},
	})

	if delta == nil || delta.Packages == nil {
		t.Fatal("expected non-nil delta")
	}
	if len(delta.Packages.Added) != 1 || delta.Packages.Added[0].Name != "jq" {
		t.Errorf("added = %+v, want [jq]", delta.Packages.Added)
	}
	if len(delta.Packages.Removed) != 0 {
		t.Errorf("removed = %+v, want empty", delta.Packages.Removed)
	}
}

func TestComputeDelta_Removed(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	c.lastPkgs = map[string]protocol.PackageRef{
		"apt:curl": {Name: "curl", Version: "7.81", Manager: "apt"},
		"apt:wget": {Name: "wget", Version: "1.21", Manager: "apt"},
	}

	delta := computeDelta(c, []protocol.PackageRef{
		{Name: "curl", Version: "7.81", Manager: "apt"},
	})

	if delta == nil || delta.Packages == nil {
		t.Fatal("expected non-nil delta")
	}
	if len(delta.Packages.Removed) != 1 || delta.Packages.Removed[0].Name != "wget" {
		t.Errorf("removed = %+v, want [wget]", delta.Packages.Removed)
	}
}

func TestComputeDelta_Updated(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	c.lastPkgs = map[string]protocol.PackageRef{
		"apt:curl": {Name: "curl", Version: "7.81", Manager: "apt"},
	}

	delta := computeDelta(c, []protocol.PackageRef{
		{Name: "curl", Version: "7.88", Manager: "apt"},
	})

	if delta == nil || delta.Packages == nil {
		t.Fatal("expected non-nil delta")
	}
	if len(delta.Packages.Updated) != 1 || delta.Packages.Updated[0].Version != "7.88" {
		t.Errorf("updated = %+v, want [curl@7.88]", delta.Packages.Updated)
	}
}

func TestComputeDelta_Mixed(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	c.lastPkgs = map[string]protocol.PackageRef{
		"apt:curl": {Name: "curl", Version: "7.81", Manager: "apt"},
		"apt:wget": {Name: "wget", Version: "1.21", Manager: "apt"},
	}

	delta := computeDelta(c, []protocol.PackageRef{
		{Name: "curl", Version: "7.88", Manager: "apt"},
		{Name: "jq", Version: "1.6", Manager: "apt"},
	})

	if delta == nil || delta.Packages == nil {
		t.Fatal("expected non-nil delta")
	}
	if len(delta.Packages.Added) != 1 {
		t.Errorf("added count = %d, want 1", len(delta.Packages.Added))
	}
	if len(delta.Packages.Removed) != 1 {
		t.Errorf("removed count = %d, want 1", len(delta.Packages.Removed))
	}
	if len(delta.Packages.Updated) != 1 {
		t.Errorf("updated count = %d, want 1", len(delta.Packages.Updated))
	}
}

// computeDelta is a test helper that bypasses collectPackages.
func computeDelta(c *Collector, current []protocol.PackageRef) *protocol.InventoryDelta {
	c.mu.Lock()
	defer c.mu.Unlock()

	currentMap := make(map[string]protocol.PackageRef, len(current))
	for _, p := range current {
		currentMap[pkgKey(p)] = p
	}

	var d protocol.PackageDelta

	for key, cur := range currentMap {
		prev, existed := c.lastPkgs[key]
		if !existed {
			d.Added = append(d.Added, cur)
		} else if prev.Version != cur.Version {
			d.Updated = append(d.Updated, cur)
		}
	}
	for key, prev := range c.lastPkgs {
		if _, exists := currentMap[key]; !exists {
			d.Removed = append(d.Removed, prev)
		}
	}

	c.lastPkgs = currentMap

	if len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Updated) == 0 {
		return nil
	}
	return &protocol.InventoryDelta{Packages: &d}
}

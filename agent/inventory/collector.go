package inventory

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// Collector gathers hardware and package inventory and computes deltas.
type Collector struct {
	log *slog.Logger

	mu       sync.Mutex
	lastPkgs map[string]protocol.PackageRef // keyed by "manager:name"
}

// New creates a Collector.
func New(log *slog.Logger) *Collector {
	return &Collector{log: log}
}

// CollectHardware gathers the current hardware inventory.
func (c *Collector) CollectHardware() *models.HardwareInventory {
	hw, err := collectHardware()
	if err != nil {
		c.log.Error("hardware inventory collection failed", slog.String("error", err.Error()))
		return nil
	}
	return hw
}

// CollectPackages gathers the current package list.
func (c *Collector) CollectPackages() []protocol.PackageRef {
	pkgs, err := collectPackages()
	if err != nil {
		c.log.Error("package inventory collection failed", slog.String("error", err.Error()))
		return nil
	}
	return pkgs
}

// ComputeDelta compares the current package list against the last-sent
// snapshot and returns a delta. Returns nil if nothing changed.
func (c *Collector) ComputeDelta() *protocol.InventoryDelta {
	current := c.CollectPackages()
	if current == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	currentMap := make(map[string]protocol.PackageRef, len(current))
	for _, p := range current {
		currentMap[pkgKey(p)] = p
	}

	// First run — no delta, just store the snapshot
	if c.lastPkgs == nil {
		c.lastPkgs = currentMap
		return nil
	}

	var delta protocol.PackageDelta

	// Added or updated
	for key, cur := range currentMap {
		prev, existed := c.lastPkgs[key]
		if !existed {
			delta.Added = append(delta.Added, cur)
		} else if prev.Version != cur.Version {
			delta.Updated = append(delta.Updated, cur)
		}
	}

	// Removed
	for key, prev := range c.lastPkgs {
		if _, exists := currentMap[key]; !exists {
			delta.Removed = append(delta.Removed, prev)
		}
	}

	c.lastPkgs = currentMap

	if len(delta.Added) == 0 && len(delta.Removed) == 0 && len(delta.Updated) == 0 {
		return nil
	}

	return &protocol.InventoryDelta{Packages: &delta}
}

// CollectFull gathers a complete inventory snapshot for the inventory_full
// job type. Returns JSON-encoded result suitable for a job result payload.
func (c *Collector) CollectFull() json.RawMessage {
	hw := c.CollectHardware()
	pkgs := c.CollectPackages()

	result := struct {
		Hardware *models.HardwareInventory `json:"hardware,omitempty"`
		Packages []protocol.PackageRef     `json:"packages"`
	}{
		Hardware: hw,
		Packages: pkgs,
	}
	if result.Packages == nil {
		result.Packages = []protocol.PackageRef{}
	}

	data, _ := json.Marshal(result)
	return data
}

func pkgKey(p protocol.PackageRef) string {
	return p.Manager + ":" + p.Name
}

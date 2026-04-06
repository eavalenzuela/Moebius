package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InventoryHandler struct {
	pool *pgxpool.Pool
}

func NewInventoryHandler(pool *pgxpool.Pool) *InventoryHandler {
	return &InventoryHandler{pool: pool}
}

type inventoryResponse struct {
	Hardware    *hardwareInfo    `json:"hardware,omitempty"`
	Packages    []models.Package `json:"packages"`
	CollectedAt *time.Time       `json:"collected_at,omitempty"`
}

type hardwareInfo struct {
	CPU               json.RawMessage `json:"cpu,omitempty"`
	RAMMB             int64           `json:"ram_mb,omitempty"`
	Disks             json.RawMessage `json:"disks,omitempty"`
	NetworkInterfaces json.RawMessage `json:"network_interfaces,omitempty"`
}

// GetDeviceInventory handles GET /v1/devices/{device_id}/inventory.
func (h *InventoryHandler) GetDeviceInventory(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	deviceID := chi.URLParam(r, "device_id")
	ctx := r.Context()

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.pool, tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, deviceID) {
				Error(w, http.StatusNotFound, "device not found")
				return
			}
		}
	}

	// Verify device belongs to tenant
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM devices WHERE id = $1 AND tenant_id = $2)`,
		deviceID, tenantID,
	).Scan(&exists)
	if err != nil || !exists {
		Error(w, http.StatusNotFound, "device not found")
		return
	}

	resp := inventoryResponse{
		Packages: []models.Package{},
	}

	// Fetch latest hardware inventory
	var hw hardwareInfo
	var collectedAt time.Time
	err = h.pool.QueryRow(ctx,
		`SELECT cpu, ram_mb, disks, network_interfaces, collected_at
		 FROM inventory_hardware
		 WHERE device_id = $1
		 ORDER BY collected_at DESC LIMIT 1`, deviceID,
	).Scan(&hw.CPU, &hw.RAMMB, &hw.Disks, &hw.NetworkInterfaces, &collectedAt)
	if err == nil {
		resp.Hardware = &hw
		resp.CollectedAt = &collectedAt
	} else if err != pgx.ErrNoRows {
		Error(w, http.StatusInternalServerError, "failed to get hardware inventory")
		return
	}

	// Fetch packages
	rows, err := h.pool.Query(ctx,
		`SELECT id, device_id, name, version, manager, installed_at, last_seen_at
		 FROM inventory_packages
		 WHERE device_id = $1
		 ORDER BY name`, deviceID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to get packages")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var p models.Package
		if err := rows.Scan(&p.ID, &p.DeviceID, &p.Name, &p.Version, &p.Manager, &p.InstalledAt, &p.LastSeenAt); err != nil {
			Error(w, http.StatusInternalServerError, "failed to scan package")
			return
		}
		resp.Packages = append(resp.Packages, p)
	}

	JSON(w, http.StatusOK, resp)
}

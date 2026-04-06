package auth

import (
	"context"
	"fmt"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolveScope expands an APIScope into the set of allowed device IDs.
// Returns nil if scope is nil (meaning unrestricted / unscoped key).
// All scope fields are unioned: GroupIDs ∪ TagIDs ∪ SiteIDs ∪ DeviceIDs.
func ResolveScope(ctx context.Context, pool *pgxpool.Pool, tenantID string, scope *models.APIScope) (map[string]struct{}, error) {
	if scope == nil {
		return nil, nil
	}

	allowed := make(map[string]struct{})

	// Direct device IDs
	for _, id := range scope.DeviceIDs {
		allowed[id] = struct{}{}
	}

	// Expand groups → device IDs
	for _, groupID := range scope.GroupIDs {
		rows, err := pool.Query(ctx,
			`SELECT dg.device_id FROM device_groups dg
			 JOIN devices d ON d.id = dg.device_id
			 WHERE dg.group_id = $1 AND d.tenant_id = $2`,
			groupID, tenantID,
		)
		if err != nil {
			return nil, fmt.Errorf("expand group %s: %w", groupID, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan group device: %w", err)
			}
			allowed[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate group devices: %w", err)
		}
	}

	// Expand tags → device IDs
	for _, tagID := range scope.TagIDs {
		rows, err := pool.Query(ctx,
			`SELECT dt.device_id FROM device_tags dt
			 JOIN devices d ON d.id = dt.device_id
			 WHERE dt.tag_id = $1 AND d.tenant_id = $2`,
			tagID, tenantID,
		)
		if err != nil {
			return nil, fmt.Errorf("expand tag %s: %w", tagID, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan tag device: %w", err)
			}
			allowed[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate tag devices: %w", err)
		}
	}

	// Expand sites → device IDs
	for _, siteID := range scope.SiteIDs {
		rows, err := pool.Query(ctx,
			`SELECT ds.device_id FROM device_sites ds
			 JOIN devices d ON d.id = ds.device_id
			 WHERE ds.site_id = $1 AND d.tenant_id = $2`,
			siteID, tenantID,
		)
		if err != nil {
			return nil, fmt.Errorf("expand site %s: %w", siteID, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan site device: %w", err)
			}
			allowed[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate site devices: %w", err)
		}
	}

	return allowed, nil
}

// DeviceInScope returns true if the device is within the allowed set.
// If allowed is nil (unscoped key), all devices are in scope.
func DeviceInScope(allowed map[string]struct{}, deviceID string) bool {
	if allowed == nil {
		return true
	}
	_, ok := allowed[deviceID]
	return ok
}

// FilterDeviceIDs returns only the device IDs that are within the allowed set.
// If allowed is nil (unscoped key), all device IDs are returned unchanged.
func FilterDeviceIDs(allowed map[string]struct{}, deviceIDs []string) []string {
	if allowed == nil {
		return deviceIDs
	}
	var filtered []string
	for _, id := range deviceIDs {
		if _, ok := allowed[id]; ok {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// ScopeHasField returns true if the scope restricts the given resource type.
// Used by group/tag/site handlers to check if their resource type is scoped.
func ScopeHasField(scope *models.APIScope, field string) bool {
	if scope == nil {
		return false
	}
	switch field {
	case "groups":
		return len(scope.GroupIDs) > 0
	case "tags":
		return len(scope.TagIDs) > 0
	case "sites":
		return len(scope.SiteIDs) > 0
	case "devices":
		return len(scope.DeviceIDs) > 0
	default:
		return false
	}
}

// TargetOverlapsScope checks whether a job target's references fall within the API key's scope.
// Returns true if the scope is nil (unrestricted) or if there is structural overlap.
// This performs a cheap structural check without DB expansion — used for scheduled jobs
// and alert rules where full device resolution would be expensive.
func TargetOverlapsScope(scope *models.APIScope, target *models.JobTarget) bool {
	if scope == nil || target == nil {
		return true
	}

	// Check each target field against the corresponding scope field.
	// If a scope field is empty, that dimension is unrestricted.
	// If a target field references IDs not in the scope, block it.

	if len(scope.GroupIDs) > 0 && len(target.GroupIDs) > 0 {
		if !anyInSet(target.GroupIDs, scope.GroupIDs) {
			return false
		}
	}
	if len(scope.TagIDs) > 0 && len(target.TagIDs) > 0 {
		if !anyInSet(target.TagIDs, scope.TagIDs) {
			return false
		}
	}
	if len(scope.SiteIDs) > 0 && len(target.SiteIDs) > 0 {
		if !anyInSet(target.SiteIDs, scope.SiteIDs) {
			return false
		}
	}
	if len(scope.DeviceIDs) > 0 && len(target.DeviceIDs) > 0 {
		if !anyInSet(target.DeviceIDs, scope.DeviceIDs) {
			return false
		}
	}

	return true
}

// ScopeIsSubset returns true if `inner` is a subset of `outer`.
// If outer is nil, inner is always a valid subset (unrestricted parent).
// If inner is nil, it's unrestricted — only valid if outer is also nil.
func ScopeIsSubset(outer, inner *models.APIScope) bool {
	if outer == nil {
		return true // unrestricted parent allows any child scope
	}
	if inner == nil {
		return false // unrestricted child under a restricted parent is not allowed
	}
	return allInSet(inner.GroupIDs, outer.GroupIDs) &&
		allInSet(inner.TagIDs, outer.TagIDs) &&
		allInSet(inner.SiteIDs, outer.SiteIDs) &&
		allInSet(inner.DeviceIDs, outer.DeviceIDs)
}

func allInSet(candidates, allowed []string) bool {
	if len(candidates) == 0 {
		return true
	}
	if len(allowed) == 0 {
		return false // candidates present but no allowed set = not a subset
	}
	set := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	for _, id := range candidates {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

func anyInSet(candidates, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	for _, id := range candidates {
		if _, ok := set[id]; ok {
			return true
		}
	}
	return false
}

// IDInScopeField returns true if the given ID is listed in the scope's field.
// If the scope is nil or the field is empty, returns true (unrestricted).
func IDInScopeField(scope *models.APIScope, field, id string) bool {
	if scope == nil {
		return true
	}
	var ids []string
	switch field {
	case "groups":
		ids = scope.GroupIDs
	case "tags":
		ids = scope.TagIDs
	case "sites":
		ids = scope.SiteIDs
	case "devices":
		ids = scope.DeviceIDs
	}
	if len(ids) == 0 {
		return true // field not restricted
	}
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

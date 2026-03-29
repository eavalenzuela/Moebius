package rbac

// All permission strings per REST_API_SPEC.md.
const (
	PermDevicesRead          = "devices:read"
	PermDevicesWrite         = "devices:write"
	PermDevicesRevoke        = "devices:revoke"
	PermJobsRead             = "jobs:read"
	PermJobsCreate           = "jobs:create"
	PermJobsRetry            = "jobs:retry"
	PermPackagesDeploy       = "packages:deploy"
	PermInventoryRead        = "inventory:read"
	PermInventoryRequest     = "inventory:request"
	PermGroupsRead           = "groups:read"
	PermGroupsWrite          = "groups:write"
	PermTagsRead             = "tags:read"
	PermTagsWrite            = "tags:write"
	PermSitesRead            = "sites:read"
	PermSitesWrite           = "sites:write"
	PermUsersRead            = "users:read"
	PermUsersWrite           = "users:write"
	PermRolesRead            = "roles:read"
	PermRolesWrite           = "roles:write"
	PermAPIKeysRead          = "api_keys:read" //nolint:gosec // not a credential
	PermAPIKeysWrite         = "api_keys:write"
	PermEnrollmentTokenWrite = "enrollment_tokens:write"
	PermAlertsRead           = "alerts:read"
	PermAlertsWrite          = "alerts:write"
	PermAuditLogRead         = "audit_log:read"
	PermTenantRead           = "tenant:read"
	PermTenantWrite          = "tenant:write"
	PermScheduledJobsRead    = "scheduled_jobs:read"
	PermScheduledJobsWrite   = "scheduled_jobs:write"
)

// AllPermissions is the complete set of permissions.
var AllPermissions = []string{
	PermDevicesRead, PermDevicesWrite, PermDevicesRevoke,
	PermJobsRead, PermJobsCreate, PermJobsRetry,
	PermPackagesDeploy,
	PermInventoryRead, PermInventoryRequest,
	PermGroupsRead, PermGroupsWrite,
	PermTagsRead, PermTagsWrite,
	PermSitesRead, PermSitesWrite,
	PermUsersRead, PermUsersWrite,
	PermRolesRead, PermRolesWrite,
	PermAPIKeysRead, PermAPIKeysWrite,
	PermEnrollmentTokenWrite,
	PermAlertsRead, PermAlertsWrite,
	PermAuditLogRead,
	PermTenantRead, PermTenantWrite,
	PermScheduledJobsRead, PermScheduledJobsWrite,
}

// Predefined role permission sets per FEATURE_REQUIREMENTS.md.
var (
	SuperAdminPermissions = AllPermissions

	TenantAdminPermissions = []string{
		PermDevicesRead, PermDevicesWrite, PermDevicesRevoke,
		PermJobsRead, PermJobsCreate, PermJobsRetry,
		PermPackagesDeploy,
		PermInventoryRead, PermInventoryRequest,
		PermGroupsRead, PermGroupsWrite,
		PermTagsRead, PermTagsWrite,
		PermSitesRead, PermSitesWrite,
		PermUsersRead, PermUsersWrite,
		PermRolesRead, PermRolesWrite,
		PermAPIKeysRead, PermAPIKeysWrite,
		PermEnrollmentTokenWrite,
		PermAlertsRead, PermAlertsWrite,
		PermAuditLogRead,
		PermTenantRead, PermTenantWrite,
		PermScheduledJobsRead, PermScheduledJobsWrite,
	}

	OperatorPermissions = []string{
		PermDevicesRead, PermDevicesWrite, PermDevicesRevoke,
		PermJobsRead, PermJobsCreate, PermJobsRetry,
		PermPackagesDeploy,
		PermInventoryRead, PermInventoryRequest,
		PermGroupsRead, PermGroupsWrite,
		PermTagsRead, PermTagsWrite,
		PermSitesRead, PermSitesWrite,
		PermEnrollmentTokenWrite,
		PermAlertsRead, PermAlertsWrite,
		PermScheduledJobsRead, PermScheduledJobsWrite,
	}

	TechnicianPermissions = []string{
		PermDevicesRead,
		PermJobsRead, PermJobsCreate,
		PermPackagesDeploy,
		PermInventoryRead, PermInventoryRequest,
		PermGroupsRead,
		PermTagsRead,
		PermSitesRead,
	}

	ViewerPermissions = []string{
		PermDevicesRead,
		PermJobsRead,
		PermInventoryRead,
		PermGroupsRead,
		PermTagsRead,
		PermSitesRead,
	}
)

package jobs

import (
	"fmt"

	"github.com/moebius-oss/moebius/shared/models"
)

// allJobTypes is the complete set of recognized job types.
var allJobTypes = map[string]bool{
	models.JobTypeExec:           true,
	models.JobTypePackageInstall: true,
	models.JobTypePackageRemove:  true,
	models.JobTypePackageUpdate:  true,
	models.JobTypeInventoryFull:  true,
	models.JobTypeFileTransfer:   true,
	models.JobTypeAgentUpdate:    true,
	models.JobTypeAgentRollback:  true,
}

// ValidateType returns an error if jobType is not a recognized job type.
func ValidateType(jobType string) error {
	if !allJobTypes[jobType] {
		return fmt.Errorf("unknown job type %q", jobType)
	}
	return nil
}

// defaultRetryPolicies returns the per-type default retry policy.
// Types not listed here have no retries by default.
var defaultRetryPolicies = map[string]models.RetryPolicy{
	models.JobTypePackageInstall: {MaxRetries: 3, RetryDelaySeconds: 300},
	models.JobTypePackageRemove:  {MaxRetries: 3, RetryDelaySeconds: 300},
	models.JobTypePackageUpdate:  {MaxRetries: 3, RetryDelaySeconds: 300},
	models.JobTypeInventoryFull:  {MaxRetries: 5, RetryDelaySeconds: 60},
	models.JobTypeFileTransfer:   {MaxRetries: 3, RetryDelaySeconds: 300},
	models.JobTypeAgentUpdate:    {MaxRetries: 2, RetryDelaySeconds: 600},
}

// DefaultRetryPolicy returns the default retry policy for a job type.
// Returns nil if the type has no default retry policy.
func DefaultRetryPolicy(jobType string) *models.RetryPolicy {
	p, ok := defaultRetryPolicies[jobType]
	if !ok {
		return nil
	}
	cp := p // copy
	return &cp
}

// ShouldRetry returns true if a job should be automatically retried
// given its current status and retry state.
func ShouldRetry(status string, retryCount, maxRetries int) bool {
	return IsRetryable(status) && retryCount < maxRetries
}

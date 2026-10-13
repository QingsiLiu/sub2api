package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

// Keep the existing request-scoped classification except for explicit encrypted
// continuity failures. SLA queries use status>=400 AND NOT is_business_limited.
func encryptedOpsRequestScopedBusinessLimitedGeili(err service.OpsStreamError) bool {
	if !err.CountTowardsSLA {
		return true
	}
	if (err.Code == "encrypted_session_route_unavailable" && err.IntendedStatus == 503) ||
		(err.Code == "invalid_encrypted_content" && err.IntendedStatus == 400) {
		return false
	}
	return true
}

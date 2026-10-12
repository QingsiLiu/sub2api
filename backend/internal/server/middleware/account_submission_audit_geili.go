package middleware

func init() {
	// Full-form payloads include pasted auth JSON and PKCE codes, so never capture
	// their bodies even if a deployment enables auditing of this public route.
	auditBodyOmittedRoutes["POST /api/v1/account-submissions/form"] = struct{}{}
}

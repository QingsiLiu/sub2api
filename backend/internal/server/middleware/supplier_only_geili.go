package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SupplierOnlyGeili allows only supplier users. It must run after JWTAuth, which loads
// the role from the database on every request, so a role change takes effect at once.
func SupplierOnlyGeili() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := GetUserRoleFromContext(c)
		if !ok {
			AbortWithError(c, 401, "UNAUTHORIZED", "User not found in context")
			return
		}
		if role != service.RoleSupplier {
			AbortWithError(c, 403, "FORBIDDEN", "Supplier access required")
			return
		}
		c.Next()
	}
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSupplierOnlyGeiliAllowsOnlySupplierRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for role, want := range map[string]int{
		service.RoleSupplier: http.StatusOK,
		service.RoleUser:     http.StatusForbidden,
		service.RoleAdmin:    http.StatusForbidden,
		"":                   http.StatusUnauthorized,
	} {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			if role != "" {
				c.Set(string(ContextKeyUserRole), role)
			}
		})
		r.GET("/supplier/meta", SupplierOnlyGeili(), func(c *gin.Context) { c.Status(http.StatusOK) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/supplier/meta", nil))
		require.Equal(t, want, w.Code, role)
	}
}

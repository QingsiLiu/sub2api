package routes

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var invoiceRoutesGeili = []string{
	"GET /api/v1/admin/invoices/issuer",
	"PUT /api/v1/admin/invoices/issuer",
	"GET /api/v1/admin/invoices",
	"POST /api/v1/admin/invoices",
	"GET /api/v1/admin/invoices/:id",
	"POST /api/v1/admin/invoices/:id/void",
}

func TestInvoiceGeiliRoutesRegisteredForAdminSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pass := func(c *gin.Context) { c.Next() }
	r := gin.New()
	RegisterAdminRoutes(r.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{
		Invoice: adminhandler.NewInvoiceHandlerGeili(service.NewInvoiceServiceGeili(nil)),
	}}, middleware.AdminAuthMiddleware(pass), middleware.AuditLogMiddleware(pass), middleware.StepUpAuthMiddleware(pass), nil, nil)
	paths := map[string]bool{}
	for _, route := range r.Routes() {
		paths[route.Method+" "+route.Path] = true
	}
	for _, route := range invoiceRoutesGeili {
		require.True(t, paths[route], route)
	}
}

func TestInvoiceGeiliRoutesDeniedToEveryScopedAdminPermission(t *testing.T) {
	var all []string
	for _, p := range adminaccess.PermissionCatalog() {
		all = append(all, p.ID)
	}
	policy := adminaccess.Policy{
		GlobalPermissions: all,
		Rules:             []adminaccess.Rule{{Permissions: all, AllGroups: true, IncludeUngrouped: true}},
	}
	require.True(t, adminaccess.RouteAllowed(policy, "GET", "/api/v1/admin/accounts"), "control: the policy grants catalogued routes")
	for _, route := range invoiceRoutesGeili {
		method, path, _ := strings.Cut(route, " ")
		require.False(t, adminaccess.RouteAllowed(policy, method, path), route)
	}
}

package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/gin-gonic/gin"
)

// Usage charts share the list/stat reporting basis, including their cache keys.
// The separate administrator overview remains explicitly accounting-based.
func parseDashboardFinancialDateBasis(c *gin.Context) (string, bool) {
	raw := strings.TrimSpace(c.Query("date_basis"))
	if !usagestats.ValidFinancialDateBasis(raw) {
		response.BadRequest(c, "Invalid date_basis, use accounting or completed")
		return "", false
	}
	return usagestats.NormalizeFinancialDateBasis(raw), true
}

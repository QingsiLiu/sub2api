package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseAuditFilterBounds(t *testing.T) {
	for _, q := range []string{"status=unsupported", "user_id=-1", "page_size=101", "page=100001", "from=not-a-date", "from=2026-10-01T00:00:00Z&to=2026-10-09T00:00:00Z", "from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z"} {
		t.Run(q, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/?"+q, nil)
			_, err := responseAuditFilter(c)
			require.Error(t, err)
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	f, err := responseAuditFilter(c)
	require.NoError(t, err)
	require.Equal(t, 50, f.PageSize)
	require.Equal(t, 1, f.Page)
	require.Equal(t, 24.0, f.To.Sub(f.From).Hours())
}

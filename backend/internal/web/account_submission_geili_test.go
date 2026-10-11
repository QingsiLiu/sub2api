//go:build embed

package web

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountSubmissionPageHeaders(t *testing.T) {
	for _, path := range []string{"/submit-key", "/submit-key/", "/admin/accounts"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		private := submissionPageHeaders(c)
		require.Equal(t, path != "/admin/accounts", private)
		if private {
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
		}
	}
}

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRecheckAccountGeiliValidatesExplicitModelBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountHandler{}
	router := gin.New()
	router.POST("/accounts/:id/recheck", h.RecheckAccountGeili)
	for _, tc := range []struct {
		id, body string
		status   int
	}{
		{"0", `{"model":"gpt-6"}`, http.StatusBadRequest},
		{"x", `{"model":"gpt-6"}`, http.StatusBadRequest},
		{"1", `{}`, http.StatusBadRequest},
		{"1", `{"model":"   "}`, http.StatusBadRequest},
		{"1", `{"model":"` + strings.Repeat("x", 101) + `"}`, http.StatusBadRequest},
		{"1", `{"model":"gpt-6","confirm_recharged":"true"}`, http.StatusBadRequest},
		{"1", `{"model":"gpt-6"}`, http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/accounts/"+tc.id+"/recheck", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, tc.body)
	}
}

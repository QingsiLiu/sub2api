package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFinancialUsageDateBasisValidatesAndDefaultsToBeijing(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)
	for _, endpoint := range []string{"/usage", "/usage/stats"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, endpoint+"?date_basis=bogus", nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage?start_date=2026-09-25&end_date=2026-09-25&timezone=America%2FLos_Angeles", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "accounting", repo.listFilters.DateBasis)
	require.Equal(t, time.Date(2026, 9, 24, 16, 0, 0, 0, time.UTC), repo.listFilters.StartTime.UTC())
	require.Equal(t, time.Date(2026, 9, 25, 16, 0, 0, 0, time.UTC), repo.listFilters.EndTime.UTC())
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage?date_basis=completed&start_date=2026-09-25&end_date=2026-09-25&timezone=America%2FLos_Angeles", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "completed", repo.listFilters.DateBasis)
	require.Equal(t, time.Date(2026, 9, 25, 7, 0, 0, 0, time.UTC), repo.listFilters.StartTime.UTC())
}

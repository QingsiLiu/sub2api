package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dashboardFinancialBasisCapture struct {
	service.UsageLogRepository
	trends, models, groups []usagestats.UsageLogFilters
	starts                 []time.Time
}

func (r *dashboardFinancialBasisCapture) GetUsageTrendWithUsageFilters(_ context.Context, start, end time.Time, _ string, f usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error) {
	r.trends = append(r.trends, f)
	r.starts = append(r.starts, start)
	n, c := basisFixtureAmount(f.DateBasis)
	return []usagestats.TrendDataPoint{{FinancialSummary: usagestats.FinancialSummary{DateBasis: f.DateBasis}, Date: "2026-09-26", Requests: n, ActualCost: c}}, nil
}
func (r *dashboardFinancialBasisCapture) GetModelStatsWithUsageFiltersBySource(_ context.Context, start, end time.Time, f usagestats.UsageLogFilters, _ string) ([]usagestats.ModelStat, error) {
	r.models = append(r.models, f)
	n, c := basisFixtureAmount(f.DateBasis)
	return []usagestats.ModelStat{{FinancialSummary: usagestats.FinancialSummary{DateBasis: f.DateBasis}, Model: "fixture", Requests: n, ActualCost: c}}, nil
}
func (r *dashboardFinancialBasisCapture) GetGroupStatsWithUsageFilters(_ context.Context, start, end time.Time, f usagestats.UsageLogFilters) ([]usagestats.GroupStat, error) {
	r.groups = append(r.groups, f)
	n, c := basisFixtureAmount(f.DateBasis)
	return []usagestats.GroupStat{{FinancialSummary: usagestats.FinancialSummary{DateBasis: f.DateBasis}, GroupID: 7, Requests: n, ActualCost: c}}, nil
}
func basisFixtureAmount(basis string) (int64, float64) {
	// The legacy completed value is accepted for wire compatibility but is
	// normalized to the single accounting-day basis before reaching the repo.
	return 5, 95
}

func TestDashboardFinancialBasisPropagatesSingleAccountingCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Restore process globals after this isolated test so other cache tests retain
	// their original fixture state. None of these tests run in parallel.
	oldTrend, oldModel, oldGroup, oldSnapshot := dashboardTrendCache, dashboardModelStatsCache, dashboardGroupStatsCache, dashboardSnapshotV2Cache
	dashboardTrendCache = newSnapshotCache(time.Minute)
	dashboardModelStatsCache = newSnapshotCache(time.Minute)
	dashboardGroupStatsCache = newSnapshotCache(time.Minute)
	dashboardSnapshotV2Cache = newSnapshotCache(time.Minute)
	t.Cleanup(func() {
		dashboardTrendCache, dashboardModelStatsCache, dashboardGroupStatsCache, dashboardSnapshotV2Cache = oldTrend, oldModel, oldGroup, oldSnapshot
	})
	repo := &dashboardFinancialBasisCapture{}
	h := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
	router := gin.New()
	router.GET("/trend", h.GetUsageTrend)
	router.GET("/models", h.GetModelStats)
	router.GET("/groups", h.GetGroupStats)
	router.GET("/snapshot", h.GetSnapshotV2)
	for _, basis := range []string{"accounting", "completed", "accounting", "completed"} {
		expectedBasis := "accounting"
		for _, route := range []string{"trend", "models", "groups", "snapshot"} {
			target := "/" + route + "?date_basis=" + basis + "&timezone=Asia%2FShanghai&start_date=2026-09-25&end_date=2026-09-26&include_stats=false&include_trend=true&include_model_stats=true&include_group_stats=true"
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
			require.Equal(t, 200, rec.Code, rec.Body.String())
			var body struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			keys := []string{route}
			if route == "snapshot" {
				keys = []string{"trend", "models", "groups"}
			}
			for _, key := range keys {
				var rows []struct {
					Basis    string  `json:"date_basis"`
					Requests int64   `json:"requests"`
					Amount   float64 `json:"actual_cost"`
				}
				require.NoError(t, json.Unmarshal(body.Data[key], &rows))
				require.Len(t, rows, 1)
				n, c := basisFixtureAmount(expectedBasis)
				require.Equal(t, expectedBasis, rows[0].Basis)
				require.Equal(t, n, rows[0].Requests)
				require.Equal(t, c, rows[0].Amount)
			}
		}
	}
	// Legacy completed requests normalize to the same accounting cache key.
	require.Len(t, repo.trends, 1)
	require.Len(t, repo.models, 1)
	require.Len(t, repo.groups, 1)
	for _, route := range []string{"trend", "models", "groups", "snapshot"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+route+"?date_basis=invalid", nil))
		require.Equal(t, 400, rec.Code)
	}
}

func TestDashboardLegacyCompletedBasisStillUsesAccountingTimezone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?date_basis=completed&timezone=America%2FNew_York&start_date=2026-03-08&end_date=2026-03-08", nil)
	start, end := parseTimeRange(c)
	require.Equal(t, 24*time.Hour, end.Sub(start))
	require.Equal(t, "Asia/Shanghai", start.Location().String())
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?date_basis=accounting&timezone=America%2FNew_York&start_date=2026-03-08&end_date=2026-03-08", nil)
	start, end = parseTimeRange(c)
	require.Equal(t, 24*time.Hour, end.Sub(start))
	require.Equal(t, "Asia/Shanghai", start.Location().String())
}

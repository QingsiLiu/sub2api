package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// UsageFinancialMetadata accompanies a read-only projection. Unknown fields stay
// unknown: legacy UsageLog scalar zero values are never evidence of zero usage.
type UsageFinancialMetadata struct {
	AccountingDate     *string    `json:"accounting_date"`
	SettledAt          *time.Time `json:"settled_at"`
	CompletedAt        *time.Time `json:"completed_at"`
	RecordSource       string     `json:"record_source"`
	RecordCompleteness string     `json:"record_completeness"`
	DetailPending      bool       `json:"detail_pending"`
	UnknownFields      []string   `json:"unknown_fields"`
}

// FinancialUsageRepository is intentionally additive. Old adapters can retain
// their original read contract; production SQL errors must never fall back.
type FinancialUsageRepository interface {
	GetFinancialUsageByID(context.Context, int64) (*UsageLog, error)
	ListFinancialUsage(context.Context, pagination.PaginationParams, usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error)
	GetFinancialUsageStats(context.Context, usagestats.UsageLogFilters) (*usagestats.UsageStats, error)
	GetFinancialDashboardStats(context.Context, int64, int64) (*usagestats.UserDashboardStats, error)
	GetFinancialTrend(context.Context, time.Time, time.Time, string, usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error)
	GetFinancialModels(context.Context, time.Time, time.Time, usagestats.UsageLogFilters, string) ([]usagestats.ModelStat, error)
	GetFinancialGroups(context.Context, time.Time, time.Time, usagestats.UsageLogFilters) ([]usagestats.GroupStat, error)
	GetFinancialBatchAPIKeyStats(context.Context, []int64, time.Time, time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, error)
}

// FinancialAdminUsageRepository keeps administrative financial reports on the
// same evidence source without changing operational supplier usage windows.
type FinancialAdminUsageRepository interface {
	GetFinancialAdminDashboardStats(context.Context) (*usagestats.DashboardStats, error)
	GetFinancialBatchUserStats(context.Context, []int64, time.Time, time.Time) (map[int64]*usagestats.BatchUserUsageStats, error)
	GetFinancialUserRanking(context.Context, time.Time, time.Time, int) (*usagestats.UserSpendingRankingResponse, error)
	GetFinancialUserTrend(context.Context, time.Time, time.Time, string, int) ([]usagestats.UserUsageTrendPoint, error)
	GetFinancialKeyTrend(context.Context, time.Time, time.Time, string, int) ([]usagestats.APIKeyUsageTrendPoint, error)
	GetFinancialUserBreakdown(context.Context, time.Time, time.Time, usagestats.UserBreakdownDimension, int) ([]usagestats.UserBreakdownItem, error)
	GetFinancialGroupSummary(context.Context, time.Time) ([]usagestats.GroupUsageSummary, error)
}

// GetUserFinancialDashboardStats preserves the existing service method while
// exposing a single explicit reporting time basis to the public controller.
func (s *UsageService) GetUserFinancialDashboardStats(ctx context.Context, userID int64, dateBasis, userTimezone string) (*usagestats.UserDashboardStats, error) {
	if repo, ok := s.usageRepo.(interface {
		GetFinancialDashboardStatsWithBasis(context.Context, int64, int64, string, string) (*usagestats.UserDashboardStats, error)
	}); ok {
		return repo.GetFinancialDashboardStatsWithBasis(ctx, userID, 0, dateBasis, userTimezone)
	}
	return s.GetUserDashboardStats(ctx, userID)
}

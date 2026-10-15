package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountAvailabilityFilterGeiliUnrequestedAddsNoProjection(t *testing.T) {
	repo, _ := runtimeTestRepositoryGeili(t)
	for _, filter := range []string{"", "enabled", "stopped"} {
		ctx := service.WithAccountAvailabilityFilterGeili(context.Background(), filter)
		require.NoError(t, applyAccountRuntimeAvailabilityFilterGeili(ctx, repo.client.Account.Query()))
	}
}

func TestAccountAvailabilityFilterGeiliQuotaProjectionErrorIsReturned(t *testing.T) {
	for _, all := range []bool{false, true} {
		repo, mock := runtimeTestRepositoryGeili(t)
		want := errors.New("quota projection unavailable")
		mock.ExpectQuery(`SELECT "accounts"\."id", "accounts"\."type", "accounts"\."extra" FROM "accounts"`).WillReturnError(want)
		ctx := service.WithAccountAvailabilityFilterGeili(context.Background(), "temporarily_stopped")
		if all {
			_, err := repo.ListAllWithFilters(ctx, "", "", "", "", 0, "")
			require.ErrorIs(t, err, want)
		} else {
			_, _, err := repo.ListWithFilters(ctx, pagination.DefaultPagination(), "", "", "", "", 0, "")
			require.ErrorIs(t, err, want)
		}
	}
}

func TestAccountAvailabilityFilterGeiliTemporaryPredicateUsesCurrentFields(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	mock.ExpectQuery(`SELECT "accounts"\."id", "accounts"\."type", "accounts"\."extra" FROM "accounts"`).WillReturnRows(sqlmock.NewRows([]string{"id", "type", "extra"}).AddRow(17, service.AccountTypeAPIKey, `{"quota_limit":5,"quota_used":5}`))
	ctx := service.WithAccountAvailabilityFilterGeili(context.Background(), "temporarily_stopped")
	q := repo.client.Account.Query()
	require.NoError(t, applyAccountRuntimeAvailabilityFilterGeili(ctx, q))
	mock.ExpectQuery(`(?s)SELECT COUNT.*NOT EXISTS \(SELECT 1 FROM account_runtime_geili.*balance_blocked.*rate_limit_reset_at.*overload_until.*temp_unschedulable_until.*auto_pause_on_expired.*expires_at.*ANY\(`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	count, err := q.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestAccountAvailabilityFilterGeiliQuotaUsesExistingResetPolicy(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name    string
		kind    string
		extra   map[string]any
		blocked bool
	}{
		{"total", service.AccountTypeAPIKey, map[string]any{"quota_limit": "5", "quota_used": "5"}, true},
		{"bedrock", service.AccountTypeBedrock, map[string]any{"quota_limit": 5., "quota_used": 7.}, true},
		{"oauth ignores API key quota", service.AccountTypeOAuth, map[string]any{"quota_limit": 5., "quota_used": 7.}, false},
		{"daily rolling", service.AccountTypeAPIKey, map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Add(-time.Hour).Format(time.RFC3339Nano)}, true},
		{"daily expired", service.AccountTypeAPIKey, map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Add(-48 * time.Hour).Format(time.RFC3339Nano)}, false},
		{"weekly rolling", service.AccountTypeAPIKey, map[string]any{"quota_weekly_limit": 5., "quota_weekly_used": 5., "quota_weekly_start": now.Add(-time.Hour).Format(time.RFC3339Nano)}, true},
		{"weekly expired", service.AccountTypeAPIKey, map[string]any{"quota_weekly_limit": 5., "quota_weekly_used": 5., "quota_weekly_start": now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)}, false},
		{"fixed local daily", service.AccountTypeAPIKey, map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Format(time.RFC3339Nano), "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 6, "quota_reset_timezone": "Pacific/Auckland"}, true},
		{"fixed local daily expired", service.AccountTypeAPIKey, map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Add(-48 * time.Hour).Format(time.RFC3339Nano), "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 6, "quota_reset_timezone": "Pacific/Auckland"}, false},
		{"fixed local weekly", service.AccountTypeAPIKey, map[string]any{"quota_weekly_limit": 5., "quota_weekly_used": 5., "quota_weekly_start": now.Format(time.RFC3339Nano), "quota_weekly_reset_mode": "fixed", "quota_weekly_reset_day": 1, "quota_weekly_reset_hour": 6, "quota_reset_timezone": "America/New_York"}, true},
		{"malformed legacy values", service.AccountTypeAPIKey, map[string]any{"quota_limit": "invalid", "quota_used": true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := accountQuotaExceededIDsGeili([]*dbent.Account{{ID: 17, Type: tc.kind, Extra: tc.extra}})
			if tc.blocked {
				require.Equal(t, []int64{17}, ids)
			} else {
				require.Empty(t, ids)
			}
		})
	}
}

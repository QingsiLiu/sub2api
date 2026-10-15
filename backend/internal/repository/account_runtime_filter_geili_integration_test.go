//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountAvailabilityFilterGeiliIntegrationMatchesPresentationAndPagination(t *testing.T) {
	ctx := service.WithAccountRuntimePresentationGeili(context.Background())
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	now := time.Now().UTC()
	prefix := fmt.Sprintf("availability-geili-%d-", now.UnixNano())
	var expectedTemporary, expectedBalance []int64
	for _, tc := range []struct {
		name          string
		fields        string
		extra         map[string]any
		legacyBlocked bool
		debt          bool
		temporary     bool
	}{
		{name: "429", fields: `rate_limit_reset_at=NOW()+INTERVAL '1 hour'`, temporary: true},
		{name: "overload", fields: `overload_until=NOW()+INTERVAL '1 hour'`, temporary: true},
		{name: "transport", fields: `temp_unschedulable_until=NOW()+INTERVAL '1 hour'`, temporary: true},
		{name: "autoexpired", fields: `auto_pause_on_expired=TRUE,expires_at=NOW()-INTERVAL '1 day'`, temporary: true},
		{name: "expiry ignored", fields: `auto_pause_on_expired=FALSE,expires_at=NOW()-INTERVAL '1 day'`},
		{name: "total quota", extra: map[string]any{"quota_limit": 5., "quota_used": 5.}, temporary: true},
		{name: "bedrock quota", fields: `type='bedrock'`, extra: map[string]any{"quota_limit": 5., "quota_used": 5.}, temporary: true},
		{name: "daily rolling quota", extra: map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Add(-time.Hour).Format(time.RFC3339Nano)}, temporary: true},
		{name: "daily expired quota", extra: map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Add(-48 * time.Hour).Format(time.RFC3339Nano)}},
		{name: "weekly fixed quota", extra: map[string]any{"quota_weekly_limit": 5., "quota_weekly_used": 5., "quota_weekly_start": now.Format(time.RFC3339Nano), "quota_weekly_reset_mode": "fixed", "quota_weekly_reset_day": 1, "quota_weekly_reset_hour": 6, "quota_reset_timezone": "America/New_York"}, temporary: true},
		{name: "daily fixed quota", extra: map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Format(time.RFC3339Nano), "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 6, "quota_reset_timezone": "Pacific/Auckland"}, temporary: true},
		{name: "daily fixed expired quota", extra: map[string]any{"quota_daily_limit": 5., "quota_daily_used": 5., "quota_daily_start": now.Add(-48 * time.Hour).Format(time.RFC3339Nano), "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 6, "quota_reset_timezone": "Pacific/Auckland"}},
		{name: "oauth ignores apikey quota", fields: `type='oauth'`, extra: map[string]any{"quota_limit": 5., "quota_used": 5.}},
		{name: "old blocked healthy", legacyBlocked: true},
		{name: "old blocked and real429", legacyBlocked: true, fields: `rate_limit_reset_at=NOW()+INTERVAL '1 hour'`, temporary: true},
		{name: "ordinary manual stop", fields: `status='inactive',schedulable=FALSE,temp_unschedulable_until=NOW()+INTERVAL '1 hour'`},
		{name: "active unschedulable", fields: `schedulable=FALSE,overload_until=NOW()+INTERVAL '1 hour'`},
		{name: "old cooldown expired", fields: `rate_limit_reset_at=NOW()-INTERVAL '1 hour',temp_unschedulable_until=NOW()-INTERVAL '1 hour'`},
		{name: "manual debt", fields: `status='inactive',schedulable=FALSE,rate_limit_reset_at=NOW()+INTERVAL '1 hour'`, debt: true},
		{name: "legacy active debt", fields: `rate_limit_reset_at=NOW()+INTERVAL '1 hour'`, debt: true},
	} {
		account := runtimeIntegrationAccountGeili(t, repo)
		_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET name=$2 WHERE id=$1`, account.ID, prefix+tc.name)
		require.NoError(t, err)
		if tc.fields != "" {
			_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET `+tc.fields+` WHERE id=$1`, account.ID)
			require.NoError(t, err)
		}
		if tc.extra != nil {
			raw, err := json.Marshal(tc.extra)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, account.ID, string(raw))
			require.NoError(t, err)
		}
		if tc.legacyBlocked || tc.debt {
			_, err = integrationDB.ExecContext(ctx, `INSERT INTO account_runtime_geili(account_id,version,balance_blocked,stop_reason,model_states) VALUES($1,1,$2,'temporary_failure','{"legacy-model":{"blocked":true}}'::jsonb)`, account.ID, tc.debt)
			require.NoError(t, err)
		}
		loaded, err := repo.GetByID(ctx, account.ID)
		require.NoError(t, err)
		view := service.PresentAccountRuntimeGeili(loaded)
		require.Equal(t, tc.temporary, view["state"] == "temporarily_stopped", tc.name)
		if tc.temporary {
			expectedTemporary = append(expectedTemporary, account.ID)
		}
		if tc.debt {
			expectedBalance = append(expectedBalance, account.ID)
		}
	}
	for _, tc := range []struct {
		filter string
		ids    []int64
	}{
		{"temporarily_stopped", expectedTemporary},
		{"balance_stopped", expectedBalance},
	} {
		t.Run(tc.filter, func(t *testing.T) {
			filterCtx := service.WithAccountAvailabilityFilterGeili(ctx, tc.filter)
			var found []int64
			for page := 1; ; page++ {
				accounts, result, err := repo.ListWithFilters(filterCtx, pagination.PaginationParams{Page: page, PageSize: 2, SortBy: "id", SortOrder: "asc"}, "", "", "", prefix, 0, "")
				require.NoError(t, err)
				require.Equal(t, int64(len(tc.ids)), result.Total)
				for _, account := range accounts {
					found = append(found, account.ID)
				}
				if page >= result.Pages {
					break
				}
			}
			require.Equal(t, tc.ids, found, "pagination must include exactly the matching current states")
			all, err := repo.ListAllWithFilters(filterCtx, "", "", "", prefix, 0, "")
			require.NoError(t, err)
			allIDs := make([]int64, 0, len(all))
			for _, account := range all {
				allIDs = append(allIDs, account.ID)
			}
			sort.Slice(allIDs, func(i, j int) bool { return allIDs[i] < allIDs[j] })
			require.Equal(t, tc.ids, allIDs)
		})
	}
}

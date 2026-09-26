//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFinancialProjectionPagedHydrationMatchesCanonicalOracle(t *testing.T) {
	tx := testEntTx(t)
	ctx := context.Background()
	c := tx.Client()
	r := newUsageLogRepositoryWithSQL(c, tx)
	u := mustCreateUser(t, c, &service.User{Email: uuid.NewString() + "@example.invalid"})
	k := mustCreateApiKey(t, c, &service.APIKey{UserID: u.ID, Key: uuid.NewString(), Name: "paging"})
	a := mustCreateAccount(t, c, &service.Account{Name: "paging"})
	at := time.Now().In(financialBeijing).Truncate(time.Hour)
	for i := 0; i < 12; i++ {
		log := &service.UsageLog{UserID: u.ID, APIKeyID: k.ID, AccountID: a.ID, RequestID: uuid.NewString(), Model: fmt.Sprintf("model-%d", i%3), ActualCost: float64(i), CreatedAt: at.Add(time.Duration(i/2) * time.Second)}
		_, err := r.Create(ctx, log)
		require.NoError(t, err)
		if i%2 == 0 { // Both delivered and pending receipt mappings, authoritative amounts.
			var logID any
			if i%4 == 0 {
				logID = log.ID
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts(request_id,usage_request_id,usage_log_id,request_fingerprint,user_id,api_key_id,charged_amount,accounting_date,completed_at,settled_at,state,record_completeness) VALUES($1,$1,$2,'page',$3,$4,$5,'2026-09-26',$6,$6,'settled','partial')`, log.RequestID, logID, u.ID, k.ID, float64(i)+.5, log.CreatedAt)
			require.NoError(t, err)
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts(request_id,request_fingerprint,user_id,api_key_id,charged_amount,accounting_date,settled_at,state,record_source,record_completeness) VALUES($1,'page',$2,$3,NULL,'2026-09-26',$4,'settled','historical_recovery','amount_unknown')`, uuid.NewString(), u.ID, k.ID, at)
	require.NoError(t, err)
	start := at.AddDate(0, 0, -1)
	end := at.AddDate(0, 0, 1)
	for _, bounded := range []bool{false, true} {
		for _, sortBy := range []string{"created_at", "id", "model", "actual_cost", "accounting_date"} {
			for _, order := range []string{"asc", "desc"} {
				f := usagestats.UsageLogFilters{UserID: u.ID}
				if bounded {
					f.StartTime = &start
					f.EndTime = &end
				}
				p := pagination.PaginationParams{Page: 1, PageSize: 5, SortBy: sortBy, SortOrder: order}
				where, args := financialUsageWhere(f)
				oracle, err := r.queryFinancialUsage(ctx, "SELECT to_jsonb(f) FROM usage_financial_records f "+where+" ORDER BY "+financialOrder(p), args...)
				require.NoError(t, err)
				var actual []service.UsageLog
				for page := 1; page <= 4; page++ {
					p.Page = page
					rows, total, e := r.ListFinancialUsage(ctx, p, f)
					require.NoError(t, e, "bounded=%v sort=%s %s page=%d", bounded, sortBy, order, page)
					actual = append(actual, rows...)
					if total.Total <= int64(len(actual)) {
						break
					}
				}
				require.Len(t, actual, len(oracle))
				for i := range oracle {
					require.Equal(t, oracle[i].ID, actual[i].ID)
					require.Equal(t, oracle[i].ActualCost, actual[i].ActualCost)
					require.Equal(t, oracle[i].Financial, actual[i].Financial)
				}
			}
		}
	}
	// No user predicate exercises the bounded recent-admin probe and exact fallback.
	future := time.Now().AddDate(0, 0, 2)
	recent := usagestats.UsageLogFilters{StartTime: &start, EndTime: &future}
	params := pagination.DefaultPagination()
	where, args := financialUsageWhere(recent)
	oracle, err := r.queryFinancialUsage(ctx, "SELECT to_jsonb(f) FROM usage_financial_records f "+where+" ORDER BY "+financialOrder(params)+" LIMIT 20", args...)
	require.NoError(t, err)
	records, _, err := r.ListFinancialUsage(ctx, params, recent)
	require.NoError(t, err)
	require.Len(t, records, len(oracle))
	for i := range oracle {
		require.Equal(t, oracle[i].ID, records[i].ID)
	}

}

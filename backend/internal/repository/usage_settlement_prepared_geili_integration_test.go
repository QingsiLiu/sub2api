//go:build integration

package repository

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Private schema/connection pool keeps claim-only fixtures out of the financial
// queue used by the rest of the integration suite.
func isolatedSettlementClaimsGeili(t testing.TB) *sql.DB {
	t.Helper()
	ctx := context.Background()
	schema := "claims_geili_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := integrationDB.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		require.NoError(t, err)
	})
	_, err = integrationDB.ExecContext(ctx, `CREATE TABLE `+schema+`.usage_settlement_receipts (LIKE public.usage_settlement_receipts INCLUDING ALL)`)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `ALTER TABLE `+schema+`.usage_settlement_receipts ALTER COLUMN id DROP DEFAULT`)
	require.NoError(t, err)
	dsn := integrationEncryptedDSNGeili
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	if u.Scheme != "" {
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(ctx))
	return db
}

func TestUsageSettlementPreparedGeiliIntegrationFreshClaimsLimitLockAndLease(t *testing.T) {
	ctx := context.Background()
	db := isolatedSettlementClaimsGeili(t)
	r := NewUsageBillingRepository(nil, db).(*usageBillingRepository)
	for _, delivery := range []bool{false, true} {
		for i := 0; i < 8; i++ {
			rows, err := r.querySettlementClaimGeili(ctx, delivery, 4, uuid.NewString(), settlementLease.Seconds())
			require.NoError(t, err)
			require.False(t, rows.Next())
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		}
	}
	// Warming an empty queue must never cache its result.
	for i := 1; i <= 9; i++ {
		_, err := db.ExecContext(ctx, `INSERT INTO usage_settlement_receipts(id,request_id,api_key_id,request_fingerprint,user_id,accounting_date,command) VALUES($1,$2,1,'fixture',1,CURRENT_DATE,'{}')`, i, uuid.NewString())
		require.NoError(t, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `SELECT id FROM usage_settlement_receipts WHERE id=1 FOR UPDATE`)
	require.NoError(t, err)
	var wg sync.WaitGroup
	ids := make(chan int64, 16)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := r.querySettlementClaimGeili(ctx, false, 4, uuid.NewString(), settlementLease.Seconds())
			if err != nil {
				errs <- err
				return
			}
			defer rows.Close()
			n := 0
			for rows.Next() {
				var id int64
				var body []byte
				if err := rows.Scan(&id, &body); err != nil {
					errs <- err
					return
				}
				ids <- id
				n++
			}
			if err := rows.Err(); err != nil {
				errs <- err
			}
			if n > 4 {
				errs <- sql.ErrNoRows
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	seen := map[int64]bool{}
	for id := range ids {
		require.NotEqual(t, int64(1), id)
		require.False(t, seen[id], "no duplicate live lease")
		seen[id] = true
	}
	require.Len(t, seen, 8)
	require.NoError(t, tx.Rollback())
	// Fresh pool/repository also sees those leases and only claims the unlocked row.
	restarted := NewUsageBillingRepository(nil, db).(*usageBillingRepository)
	rows, err := restarted.querySettlementClaimGeili(ctx, false, 4, uuid.NewString(), settlementLease.Seconds())
	require.NoError(t, err)
	require.True(t, rows.Next())
	var id int64
	var body []byte
	require.NoError(t, rows.Scan(&id, &body))
	require.Equal(t, int64(1), id)
	require.False(t, rows.Next())
	require.NoError(t, rows.Close())
	_, err = db.ExecContext(ctx, `UPDATE usage_settlement_receipts SET settlement_lease_until=NOW()-INTERVAL '1 second' WHERE id=1`)
	require.NoError(t, err)
	rows, err = r.querySettlementClaimGeili(ctx, false, 1, uuid.NewString(), settlementLease.Seconds())
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&id, &body))
	require.Equal(t, int64(1), id)
	require.False(t, rows.Next())
	require.NoError(t, rows.Close())
	// Delivery uses its own fresh plan and its own lease, never a pending result.
	_, err = db.ExecContext(ctx, `UPDATE usage_settlement_receipts SET state='settled',settled_at=NOW(),record_completeness='complete',detail='{}',charged_amount=1 WHERE id=1`)
	require.NoError(t, err)
	rows, err = r.querySettlementClaimGeili(ctx, true, 4, uuid.NewString(), settlementLease.Seconds())
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&id))
	require.Equal(t, int64(1), id)
	require.False(t, rows.Next())
	require.NoError(t, rows.Close())
	rows, err = restarted.querySettlementClaimGeili(ctx, true, 4, uuid.NewString(), settlementLease.Seconds())
	require.NoError(t, err)
	require.False(t, rows.Next())
	require.NoError(t, rows.Close())
}

func TestUsageSettlementPreparedGeiliIntegrationFinancialWorkflowOnce(t *testing.T) {
	ctx := context.Background()
	raw, cmd, log := settlementFixture(t)
	r := NewUsageBillingRepository(nil, integrationDB).(*usageBillingRepository)
	for i := 0; i < 8; i++ {
		_, err := r.ProcessPendingSettlements(ctx, 4)
		require.NoError(t, err)
		_, err = r.DeliverSettledUsage(ctx, 4)
		require.NoError(t, err)
	}
	require.NoError(t, raw.PrepareSettlement(ctx, cmd, log))
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := r.ProcessPendingSettlements(ctx, 128); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for i := 0; i < 4; i++ {
		_, err := r.DeliverSettledUsage(ctx, 128)
		require.NoError(t, err)
	}
	_, err := raw.Apply(ctx, cmd)
	require.NoError(t, err)
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 98.75, balance)
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, log.RequestID, cmd.APIKeyID).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&n))
	require.Equal(t, 1, n)
}

func BenchmarkUsageSettlementPreparedClaimsGeili(b *testing.B) {
	db := isolatedSettlementClaimsGeili(b)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx := context.Background()
	for _, mode := range []string{"raw", "prepared"} {
		b.Run(mode, func(b *testing.B) {
			r := &usageBillingRepository{db: db}
			if mode == "prepared" {
				r = NewUsageBillingRepository(nil, db).(*usageBillingRepository)
			}
			call := func() {
				for _, delivery := range []bool{false, true} {
					rows, err := r.querySettlementClaimGeili(ctx, delivery, 4, uuid.NewString(), settlementLease.Seconds())
					require.NoError(b, err)
					require.False(b, rows.Next())
					require.NoError(b, rows.Err())
					require.NoError(b, rows.Close())
				}
			}
			for i := 0; i < 20; i++ {
				call()
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				call()
			}
			b.StopTimer()
			var generic, custom int64
			require.NoError(b, db.QueryRow(`SELECT COALESCE(SUM(generic_plans),0),COALESCE(SUM(custom_plans),0) FROM pg_prepared_statements WHERE statement LIKE 'WITH selected AS%'`).Scan(&generic, &custom))
			b.Logf("live server plan counters generic=%d custom=%d", generic, custom)
		})
	}
}

//go:build integration

package subscription

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// A real PostgreSQL fixture tests the shipped migration SQL rather than a
// reimplementation of its classifier. Only tables read by these migrations are
// seeded; application repository integration tests cover the complete schema.
func TestContractMigrationV2PostgresRepairReplayAndAtomicity(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("subscription_migration"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("fixture-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, `
 CREATE TABLE users(id BIGINT PRIMARY KEY);
 CREATE TABLE subscription_plans(id BIGINT PRIMARY KEY,name TEXT NOT NULL,validity_days INTEGER NOT NULL,validity_unit TEXT NOT NULL,is_legacy_compat BOOLEAN NOT NULL DEFAULT FALSE,price NUMERIC NOT NULL,daily_limit_usd NUMERIC);
 CREATE TABLE user_subscriptions(id BIGINT PRIMARY KEY,user_id BIGINT REFERENCES users(id),plan_id BIGINT REFERENCES subscription_plans(id),starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ,status TEXT,deleted_at TIMESTAMPTZ,daily_window_start TIMESTAMPTZ,daily_usage_usd NUMERIC DEFAULT 0);
 CREATE TABLE user_subscription_entitlements(id BIGINT PRIMARY KEY,user_subscription_id BIGINT REFERENCES user_subscriptions(id),plan_id BIGINT REFERENCES subscription_plans(id),status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ,daily_limit_usd NUMERIC,daily_window_start TIMESTAMPTZ,daily_usage_usd NUMERIC DEFAULT 0,source_order_id BIGINT);
 CREATE TABLE subscription_requests(id BIGINT PRIMARY KEY,request_key TEXT UNIQUE,subscription_id BIGINT REFERENCES user_subscriptions(id),admitted_at TIMESTAMPTZ);
 CREATE TABLE subscription_usage_allocations(id BIGINT PRIMARY KEY,request_key TEXT REFERENCES subscription_requests(request_key),cost_usd NUMERIC);
 CREATE TABLE payment_orders(id BIGINT PRIMARY KEY,subscription_days INTEGER);
 INSERT INTO subscription_plans VALUES(1,'month45',30,'day',FALSE,30,45),(2,'free',30,'day',FALSE,0,45),(3,'compat',30,'day',TRUE,30,45),(4,'edited',30,'day',FALSE,30,90);
 `)
	require.NoError(t, err)
	var now time.Time
	require.NoError(t, db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now))
	day := DayStart(now)
	type scenario struct {
		id           int64
		name, want   string
		plan         int64
		status       string
		noLot        bool
		futureParent bool
		deleted      bool
		extra        string
	}
	cases := []scenario{
		{1, "ordinary", "v2", 1, "active", false, false, false, ""},
		{2, "free_special", "legacy_daily", 2, "active", false, false, false, ""},
		{3, "marked_legacy", "legacy_daily", 3, "active", false, false, false, ""},
		{4, "plan_edited_quota", "legacy_daily", 4, "active", false, false, false, ""},
		{5, "active_and_future", "legacy_daily", 1, "active", false, false, false, "future"},
		{6, "suspended", "legacy_daily", 1, "suspended", false, false, false, ""},
		{7, "revoked", "legacy_daily", 1, "revoked", false, false, false, ""},
		{8, "deleted", "legacy_daily", 1, "active", false, false, true, ""},
		{9, "future_parent", "legacy_daily", 1, "active", false, true, false, ""},
		{10, "refund_pending_sibling", "legacy_daily", 1, "active", false, false, false, "refund_pending"},
		{11, "parent_only", "legacy_daily", 1, "active", true, false, false, ""},
		{12, "refunded_sibling", "v2", 1, "active", false, false, false, "refunded"},
		{13, "revoked_sibling", "v2", 1, "active", false, false, false, "revoked"},
		{14, "paid_duration_changed", "legacy_daily", 1, "active", false, false, false, ""},
		{20, "changed_v2_never_demoted", "v2", 2, "active", false, false, false, ""},
		{21, "admin_revision_never_demoted", "v2", 2, "active", false, false, false, ""},
		{22, "multiple_pools_a", "legacy_daily", 1, "active", false, false, false, ""},
		{23, "multiple_pools_b", "legacy_daily", 1, "active", false, false, false, ""},
		{24, "lot_inherits_parent_plan", "v2", 1, "active", false, false, false, ""},
	}
	seed := func(tc scenario) {
		t.Helper()
		_, e := db.ExecContext(ctx, `INSERT INTO users VALUES($1)`, tc.id)
		require.NoError(t, e)
		starts := now.Add(-24 * time.Hour)
		if tc.futureParent {
			starts = now.Add(time.Hour)
		}
		var deleted any
		if tc.deleted {
			deleted = now
		}
		_, e = db.ExecContext(ctx, `INSERT INTO user_subscriptions VALUES($1,$1,$2,$3,$4,$5,$6,$7,7.123456789)`, tc.id, tc.plan, starts, now.Add(7*24*time.Hour), tc.status, deleted, day)
		require.NoError(t, e)
		if !tc.noLot {
			_, e = db.ExecContext(ctx, `INSERT INTO user_subscription_entitlements(id,user_subscription_id,plan_id,status,starts_at,expires_at,daily_limit_usd,daily_window_start,daily_usage_usd) VALUES($1,$1,$2,'active',$3,$4,45,$5,7.123456789)`, tc.id, tc.plan, now.Add(-24*time.Hour), now.Add(7*24*time.Hour), day)
			require.NoError(t, e)
		}
		if tc.extra != "" {
			status := "active"
			start := now.Add(time.Hour)
			if tc.extra != "future" {
				status = tc.extra
				start = now.Add(-time.Hour)
			}
			_, e = db.ExecContext(ctx, `INSERT INTO user_subscription_entitlements(id,user_subscription_id,plan_id,status,starts_at,expires_at,daily_limit_usd,daily_window_start,daily_usage_usd) VALUES($1,$2,$3,$4,$5,$6,45,$7,0)`, tc.id+100, tc.id, tc.plan, status, start, now.Add(8*24*time.Hour), day)
			require.NoError(t, e)
		}
		_, e = db.ExecContext(ctx, `INSERT INTO subscription_requests VALUES($1,$2,$1,$3)`, tc.id, tc.name, now.Add(-time.Minute))
		require.NoError(t, e)
		_, e = db.ExecContext(ctx, `INSERT INTO subscription_usage_allocations VALUES($1,$2,7.123456789)`, tc.id, tc.name)
		require.NoError(t, e)
	}
	for _, tc := range cases {
		seed(tc)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO payment_orders VALUES(14,7); UPDATE user_subscription_entitlements SET source_order_id=14 WHERE id=14; UPDATE user_subscriptions SET user_id=22 WHERE id=23; UPDATE user_subscription_entitlements SET plan_id=NULL WHERE id=24`)
	require.NoError(t, err)
	var evidenceBefore string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT JSON_AGG(x ORDER BY id)::TEXT FROM user_subscription_entitlements x`).Scan(&evidenceBefore))
	original, err := migrations.FS.ReadFile("253_subscription_contract_v2.sql")
	require.NoError(t, err)
	repair, err := migrations.FS.ReadFile("254_subscription_contract_classification_repair.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(original))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO subscription_contract_changes(subscription_id,operation,after_contract,before_lots,after_lots,source_type) VALUES(20,'upgrade','{}','[]','[]','test'); UPDATE subscription_contracts SET revision=2 WHERE subscription_id=21`)
	require.NoError(t, err)
	var beforeFuture string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT mode FROM subscription_contracts WHERE subscription_id=5`).Scan(&beforeFuture))
	require.Equal(t, "v2", beforeFuture, "reproduce the shipped 253 issue before proving repair")
	_, err = db.ExecContext(ctx, string(repair))
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mode, term, bound string
			var quota float64
			var expiry time.Time
			require.NoError(t, db.QueryRowContext(ctx, `SELECT mode,term_id,expires_at FROM subscription_contracts WHERE subscription_id=$1`, tc.id).Scan(&mode, &term, &expiry))
			require.Equal(t, tc.want, mode)
			require.True(t, expiry.Equal(now.Add(7*24*time.Hour)))
			require.NoError(t, db.QueryRowContext(ctx, `SELECT term_id FROM subscription_request_contracts WHERE request_key=$1`, tc.name).Scan(&bound))
			require.Equal(t, term, bound)
			require.NoError(t, db.QueryRowContext(ctx, `SELECT used_usd FROM subscription_daily_usage WHERE subscription_id=$1`, tc.id).Scan(&quota))
			require.Equal(t, 7.123456789, quota)
		})
	}
	var firstProjection string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT JSON_AGG(x ORDER BY subscription_id)::TEXT FROM subscription_contracts x`).Scan(&firstProjection))
	for i := 0; i < 2; i++ {
		_, err = db.ExecContext(ctx, string(original))
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(repair))
		require.NoError(t, err)
	}
	var replay string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT JSON_AGG(x ORDER BY subscription_id)::TEXT FROM subscription_contracts x`).Scan(&replay))
	require.Equal(t, firstProjection, replay)
	var evidenceAfter string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT JSON_AGG(x ORDER BY id)::TEXT FROM user_subscription_entitlements x`).Scan(&evidenceAfter))
	require.Equal(t, evidenceBefore, evidenceAfter)
	for i, offset := range []int{-12, 0, 14} {
		requestID := int64(1000 + i)
		key := fmt.Sprintf("cross-offset-%d", offset)
		_, err = db.ExecContext(ctx, `INSERT INTO subscription_requests VALUES($1,$2,1,$3)`, requestID, key, now.In(time.FixedZone("test", offset*3600)))
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `INSERT INTO subscription_usage_allocations VALUES($1,$2,$3)`, requestID, key, float64(i+1)/100)
		require.NoError(t, err)
	}
	overlay, err := ReadDailyUsage(ctx, db, 1, "legacy-1", now)
	require.NoError(t, err)
	require.InDelta(t, 7.183456789, overlay, 1e-10)
	for i := 0; i < 2; i++ {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, LockSubscription(ctx, tx, 1))
		require.NoError(t, SyncDailyLedger(ctx, tx, 1, now))
		require.NoError(t, tx.Commit())
	}
	bridged, err := ReadDailyUsage(ctx, db, 1, "legacy-1", now)
	require.NoError(t, err)
	require.InDelta(t, overlay, bridged, 1e-10)
	seed(scenario{30, "rollback", "v2", 1, "active", false, false, false, ""})
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(original))
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscription_contracts WHERE subscription_id=30`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, tx.Rollback())
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscription_contracts WHERE subscription_id=30`).Scan(&count))
	require.Zero(t, count)
	t.Logf("%d PostgreSQL classification fixtures; 253->254 repair, two replays, source-right immutability and transaction rollback", len(cases))
}

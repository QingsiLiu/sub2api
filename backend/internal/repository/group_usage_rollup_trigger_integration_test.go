//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGroupUsageRollupTriggerInvalidatesCascadedHistoricalDelete(t *testing.T) {
	for _, partitioned := range []bool{false, true} {
		name := "ordinary"
		if partitioned {
			name = "partitioned"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			schema := createGroupUsageRollupTriggerTestSchema(t, ctx, partitioned)
			tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			defer func() { _ = tx.Rollback() }()

			_, err := tx.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
				VALUES (1, 1, 10, 1.25, TIMESTAMPTZ '2020-01-02 08:00:00+08');
				UPDATE usage_group_rollup_state
				SET closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
				WHERE id = 1;
				DELETE FROM users WHERE id = 1;
			`)
			require.NoError(t, err)

			var events int
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_group_rollup_invalidations WHERE group_id=10`).Scan(&events))
			require.Equal(t, 2, events, "insert and cascaded delete append independent transactional events")
		})
	}
}

// The three connections model the old lock chain: rollup publisher holds its
// state row, usage inserts a subscription FK, then billing updates that parent.
func TestGroupUsageRollupTriggerStateLockDoesNotBlockInsertOrBilling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seed.ExecContext(ctx, `CREATE TABLE subscriptions(id BIGINT PRIMARY KEY, amount NUMERIC NOT NULL DEFAULT 0);
		ALTER TABLE usage_logs ADD COLUMN subscription_id BIGINT REFERENCES subscriptions(id);
		INSERT INTO subscriptions(id) VALUES (1);INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);`)
	require.NoError(t, err)
	require.NoError(t, seed.Commit())
	publisher := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer publisher.Rollback()
	var id int
	require.NoError(t, publisher.QueryRowContext(ctx, `SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE`).Scan(&id))
	writer := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer writer.Rollback()
	_, err = writer.ExecContext(ctx, `SET LOCAL statement_timeout='1s'; INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at,subscription_id) VALUES(1,1,10,1.25,'2020-01-02 09:00:00+08',1)`)
	require.NoError(t, err, "usage writer must not wait for the long-running publisher")
	billing := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer billing.Rollback()
	_, err = billing.ExecContext(ctx, `SET LOCAL statement_timeout='1s'; UPDATE subscriptions SET amount=amount+1 WHERE id=1`)
	require.NoError(t, err, "non-key parent update must remain compatible with the usage FK lock")
	require.NoError(t, billing.Commit())
	require.NoError(t, writer.Commit())
}

func TestGroupUsageRollupTriggerPreservesLateLowerIDCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seed.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);
		UPDATE usage_group_rollup_state SET closed_before='2026-08-14',retained_from='2026-08-12 00:00:00+08' WHERE id=1;`)
	require.NoError(t, err)
	require.NoError(t, seed.Commit())
	late := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer late.Rollback()
	_, err = late.ExecContext(ctx, `INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES(1,1,10,2,'2026-08-13 12:00:00+08')`)
	require.NoError(t, err)
	var lower int64
	require.NoError(t, late.QueryRowContext(ctx, `SELECT id FROM usage_group_rollup_invalidations`).Scan(&lower))
	first := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err = first.ExecContext(ctx, `INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES(2,1,10,3,'2026-08-13 13:00:00+08')`)
	require.NoError(t, err)
	require.NoError(t, first.Commit())
	publisher := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	today := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(publisher).SyncGroupUsageRollups(ctx, today))
	require.NoError(t, publisher.Commit())
	require.NoError(t, late.Commit())
	read := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer read.Rollback()
	var stillPresent bool
	require.NoError(t, read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM usage_group_rollup_invalidations WHERE id=$1)`, lower).Scan(&stillPresent))
	require.True(t, stillPresent)
	result, err := newUsageLogRepositoryWithSQL(nil, read).GetAllGroupUsageSummary(ctx, today)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.InDelta(t, 5, result[0].TotalCost, 1e-9)
	require.InDelta(t, 5, result[0].YesterdayCost, 1e-9)
}

func TestGroupUsageRollupTriggerCrossMidnightInFlightInsertLeavesDirtyEvent(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seed.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);
		UPDATE usage_group_rollup_state SET closed_before='2026-08-13',retained_from='2026-08-13 00:00:00+08' WHERE id=1`)
	require.NoError(t, err)
	require.NoError(t, seed.Commit())
	writer := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer writer.Rollback()
	_, err = writer.ExecContext(ctx, `INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES(1,1,10,1.25,'2026-08-13 23:59:59+08')`)
	require.NoError(t, err)
	publisher := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	today := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(publisher).SyncGroupUsageRollups(ctx, today))
	require.NoError(t, publisher.Commit())
	require.NoError(t, writer.Commit())
	read := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer read.Rollback()
	result, err := newUsageLogRepositoryWithSQL(nil, read).GetAllGroupUsageSummary(ctx, today)
	require.NoError(t, err)
	require.InDelta(t, 1.25, result[0].YesterdayCost, 1e-9)
}

func TestGroupUsageRollupTriggerKeepsWatermarkForTodayInsert(t *testing.T) {
	ctx := context.Background()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)

	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, setGroupUsageRollupTriggerTimeZone(ctx, tx, "Asia/Shanghai"))
	_, err := tx.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		UPDATE usage_group_rollup_state
		SET closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		WHERE id = 1;
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
		VALUES (1, 1, 10, 1.25, CURRENT_TIMESTAMP);
	`)
	require.NoError(t, err)

	var unchanged bool
	err = tx.QueryRowContext(ctx, `
		SELECT closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		FROM usage_group_rollup_state
		WHERE id = 1
	`).Scan(&unchanged)
	require.NoError(t, err)
	require.True(t, unchanged)
}

func TestGroupUsageRollupTriggerUsesSessionTimezoneAcrossDST(t *testing.T) {
	ctx := context.Background()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)

	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()
	_, err := tx.ExecContext(ctx, `
		SET LOCAL TIME ZONE 'America/New_York';
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '2026-03-09',
			timezone_name = 'America/New_York'
		WHERE id = 1;
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
		VALUES (1, 1, 10, 1.25, TIMESTAMPTZ '2026-03-08 04:30:00+00');
	`)
	require.NoError(t, err)

	var affectedAt time.Time
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT affected_at FROM usage_group_rollup_invalidations`).Scan(&affectedAt))
	require.True(t, affectedAt.Equal(time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC)), "queue preserves timestamp, not the writer session's local date")
}

func TestGroupUsageSummaryIncludesYesterdayAcrossWatermark(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		closedBefore     string
		includeYesterday bool
	}{
		{name: "closed_rollup", closedBefore: "2026-08-14", includeYesterday: true},
		{name: "raw_tail", closedBefore: "2026-08-13", includeYesterday: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
			tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			defer func() { _ = tx.Rollback() }()

			_, err := tx.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
					(1, 1, 10, 2, TIMESTAMPTZ '2026-08-12 12:00:00+08'),
					(2, 1, 10, 3, TIMESTAMPTZ '2026-08-13 12:00:00+08'),
					(3, 1, 10, 4, TIMESTAMPTZ '2026-08-14 12:00:00+08');
				INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at)
				VALUES (DATE '2026-08-12', 10, 2, NOW());
			`)
			require.NoError(t, err)
			if tt.includeYesterday {
				_, err = tx.ExecContext(ctx, `
					INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at)
					VALUES (DATE '2026-08-13', 10, 3, NOW())
				`)
				require.NoError(t, err)
			}
			_, err = tx.ExecContext(ctx, `
				UPDATE usage_group_rollup_state
				SET closed_before = $1::date,
					retained_from = TIMESTAMPTZ '2026-08-12 00:00:00+08'
				WHERE id = 1
			`, tt.closedBefore)
			require.NoError(t, err)

			repo := newUsageLogRepositoryWithSQL(nil, tx)
			result, err := repo.GetAllGroupUsageSummary(ctx, todayStart)
			require.NoError(t, err)
			require.Len(t, result, 1)
			require.InDelta(t, 9, result[0].TotalCost, 0.0000001)
			require.InDelta(t, 4, result[0].TodayCost, 0.0000001)
			require.InDelta(t, 3, result[0].YesterdayCost, 0.0000001)
		})
	}
}

func TestGroupUsageRollupSyncRebuildsAfterTimezoneChange(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "America/New_York")
	todayStart := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()

	_, err := tx.ExecContext(ctx, `
		SET LOCAL TIME ZONE 'UTC';
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 3, TIMESTAMPTZ '2026-03-08 05:30:00+00'),
			(2, 1, 10, 5, TIMESTAMPTZ '2026-03-09 04:30:00+00');
		INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at)
		VALUES (DATE '2026-03-08', 10, 99, NOW());
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '2026-03-09',
			retained_from = TIMESTAMPTZ '2026-03-08 05:30:00+00',
			timezone_name = 'Asia/Shanghai'
		WHERE id = 1;
	`)
	require.NoError(t, err)

	repo := newDashboardAggregationRepositoryWithSQL(tx)
	require.NoError(t, repo.SyncGroupUsageRollups(ctx, todayStart))

	var stateTimezone string
	var closedBefore string
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT timezone_name, closed_before::text
		FROM usage_group_rollup_state
		WHERE id = 1
	`).Scan(&stateTimezone, &closedBefore))
	require.Equal(t, "America/New_York", stateTimezone)
	require.Equal(t, "2026-03-09", closedBefore)

	var rollupCost float64
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT actual_cost
		FROM usage_group_daily_rollups
		WHERE bucket_date = DATE '2026-03-08' AND group_id = 10
	`).Scan(&rollupCost))
	require.InDelta(t, 3, rollupCost, 0.0000001)

	usageRepo := newUsageLogRepositoryWithSQL(nil, tx)
	result, err := usageRepo.GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.InDelta(t, 8, result[0].TotalCost, 0.0000001)
	require.InDelta(t, 5, result[0].TodayCost, 0.0000001)
	require.InDelta(t, 3, result[0].YesterdayCost, 0.0000001)
}

func TestGroupUsageSummaryUsesConfiguredDSTBoundaries(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "America/New_York")
	todayStart := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()

	_, err := tx.ExecContext(ctx, `
		SET LOCAL TIME ZONE 'UTC';
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 100, TIMESTAMPTZ '2026-03-08 04:30:00+00'),
			(2, 1, 10, 3, TIMESTAMPTZ '2026-03-08 05:30:00+00'),
			(3, 1, 10, 4, TIMESTAMPTZ '2026-03-09 03:30:00+00'),
			(4, 1, 10, 5, TIMESTAMPTZ '2026-03-09 04:30:00+00');
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '1970-01-01',
			retained_from = TIMESTAMPTZ '1970-01-01 00:00:00+00',
			timezone_name = 'America/New_York'
		WHERE id = 1;
	`)
	require.NoError(t, err)

	repo := newUsageLogRepositoryWithSQL(nil, tx)
	result, err := repo.GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.InDelta(t, 112, result[0].TotalCost, 0.0000001)
	require.InDelta(t, 5, result[0].TodayCost, 0.0000001)
	require.InDelta(t, 7, result[0].YesterdayCost, 0.0000001)
}

func createGroupUsageRollupTriggerTestSchema(t *testing.T, ctx context.Context, partitioned bool) string {
	t.Helper()

	schema := fmt.Sprintf("group_usage_rollup_trigger_%d", time.Now().UnixNano())
	quotedSchema := pq.QuoteIdentifier(schema)
	_, err := integrationDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	})

	tx, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, setGroupUsageRollupTriggerSearchPath(ctx, tx, quotedSchema))

	usageLogsDDL := `
		CREATE TABLE usage_logs (
			id BIGINT PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
			actual_cost NUMERIC(20, 10) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		);
	`
	if partitioned {
		usageLogsDDL = `
			CREATE TABLE usage_logs (
				id BIGINT NOT NULL,
				user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
				actual_cost NUMERIC(20, 10) NOT NULL,
				created_at TIMESTAMPTZ NOT NULL
			) PARTITION BY RANGE (created_at);
			CREATE TABLE usage_logs_default PARTITION OF usage_logs DEFAULT;
		`
	}

	_, err = tx.ExecContext(ctx, `
		CREATE TABLE users (id BIGINT PRIMARY KEY);
		CREATE TABLE groups (id BIGINT PRIMARY KEY);
	`+usageLogsDDL)
	require.NoError(t, err)

	for _, migrationName := range []string{
		"222_group_usage_daily_rollups.sql",
		"223_group_usage_rollup_timezone.sql",
		"260_group_usage_rollup_invalidation_queue.sql",
	} {
		migrationSQL, readErr := migrations.FS.ReadFile(migrationName)
		require.NoError(t, readErr)
		for range 2 {
			_, err = tx.ExecContext(ctx, string(migrationSQL))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tx.Commit())

	return schema
}

func beginGroupUsageRollupTriggerTestTx(t *testing.T, ctx context.Context, schema string) *sql.Tx {
	t.Helper()

	tx, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	require.NoError(t, setGroupUsageRollupTriggerSearchPath(ctx, tx, pq.QuoteIdentifier(schema)))
	return tx
}

func setGroupUsageRollupTriggerSearchPath(ctx context.Context, tx *sql.Tx, quotedSchema string) error {
	_, err := tx.ExecContext(ctx, "SET LOCAL search_path TO "+quotedSchema)
	return err
}

func setGroupUsageRollupTriggerTimeZone(ctx context.Context, tx *sql.Tx, name string) error {
	_, err := tx.ExecContext(ctx, "SET LOCAL TIME ZONE "+pq.QuoteLiteral(name))
	return err
}

func TestGroupUsageRollupTriggerDirectPartitionInsertAndUpdateMove(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, true)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer tx.Rollback()
	_, err := tx.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10),(11);INSERT INTO users(id) VALUES(1);
 INSERT INTO usage_logs_default(id,user_id,group_id,actual_cost,created_at) VALUES(1,1,10,2,'2026-08-12 12:00:00+08');
 DO $$ BEGIN
   IF (SELECT COUNT(*) FROM usage_group_rollup_invalidations) <> 1 THEN
     RAISE EXCEPTION 'direct partition insert did not append exactly one event';
   END IF;
 END $$;
 DELETE FROM usage_group_rollup_invalidations;
 UPDATE usage_logs_default SET group_id=11,actual_cost=3,created_at='2026-08-13 12:00:00+08' WHERE id=1;`)
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM usage_group_rollup_invalidations`).Scan(&count))
	require.Equal(t, 2, count)
	var dates int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(DISTINCT (affected_at AT TIME ZONE 'Asia/Shanghai')::date) FROM usage_group_rollup_invalidations`).Scan(&dates))
	require.Equal(t, 2, dates)
}

func TestGroupUsageRollupDirtyOlderThanRetainedAndDeletedEmptyBucket(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer tx.Rollback()
	_, err := tx.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);
 INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES(1,1,10,2,'2026-08-01 12:00:00+08');
 INSERT INTO usage_group_daily_rollups(bucket_date,group_id,actual_cost) VALUES('2026-08-12',10,99);
 INSERT INTO usage_group_rollup_invalidations(affected_at,group_id) VALUES('2026-08-12 12:00:00+08',10);
 UPDATE usage_group_rollup_state SET closed_before='2026-08-14',retained_from='2026-08-12 00:00:00+08' WHERE id=1;`)
	require.NoError(t, err)
	today := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	result, err := newUsageLogRepositoryWithSQL(nil, tx).GetAllGroupUsageSummary(ctx, today)
	require.NoError(t, err)
	require.InDelta(t, 2, result[0].TotalCost, 1e-9)
	repo := newDashboardAggregationRepositoryWithSQL(tx)
	require.NoError(t, repo.SyncGroupUsageRollups(ctx, today))
	require.NoError(t, repo.SyncGroupUsageRollups(ctx, today))
	result, err = newUsageLogRepositoryWithSQL(nil, tx).GetAllGroupUsageSummary(ctx, today)
	require.NoError(t, err)
	require.InDelta(t, 2, result[0].TotalCost, 1e-9)
	var retained string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT (retained_from AT TIME ZONE 'Asia/Shanghai')::date::text FROM usage_group_rollup_state`).Scan(&retained))
	require.Equal(t, "2026-08-01", retained)
	var stale int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_group_daily_rollups WHERE bucket_date='2026-08-12'`).Scan(&stale))
	require.Zero(t, stale)
}

func TestGroupUsageRollupPartitionRangeIncludesLocalBoundaryDaysAndDST(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "America/New_York")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, true)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer tx.Rollback()
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	require.NoError(t, invalidateGroupUsageRollupsRange(ctx, tx, start, end))
	var count int
	var first, last string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*),MIN((affected_at AT TIME ZONE 'America/New_York')::date)::text,MAX((affected_at AT TIME ZONE 'America/New_York')::date)::text FROM usage_group_rollup_invalidations`).Scan(&count, &first, &last))
	require.Equal(t, 32, count)
	require.Equal(t, "2026-02-28", first)
	require.Equal(t, "2026-03-31", last)
	var spring time.Time
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT affected_at FROM usage_group_rollup_invalidations WHERE (affected_at AT TIME ZONE 'America/New_York')::date='2026-03-09'`).Scan(&spring))
	require.True(t, spring.Equal(time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)))
}

func TestGroupUsageRollupSummarySnapshotRemainsConsistentDuringConsumerCommit(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seed.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);
 INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES(1,1,10,3,'2026-08-13 12:00:00+08');
 INSERT INTO usage_group_daily_rollups(bucket_date,group_id,actual_cost) VALUES('2026-08-13',10,99);
 UPDATE usage_group_rollup_state SET closed_before='2026-08-14',retained_from='2026-08-12 00:00:00+08' WHERE id=1;`)
	require.NoError(t, err)
	require.NoError(t, seed.Commit())
	reader := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer reader.Rollback()
	today := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	usageRepo := newUsageLogRepositoryWithSQL(nil, reader)
	_, err = usageRepo.readGroupUsageRollupSnapshot(ctx, "Asia/Shanghai", "2026-08-14")
	require.NoError(t, err)
	publisher := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(publisher).SyncGroupUsageRollups(ctx, today))
	require.NoError(t, publisher.Commit())
	result, err := usageRepo.GetAllGroupUsageSummary(ctx, today)
	require.NoError(t, err)
	require.InDelta(t, 3, result[0].TotalCost, 1e-9)
	var events int
	require.NoError(t, reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_group_rollup_invalidations`).Scan(&events))
	require.Equal(t, 1, events, "old reader must retain matching raw+queue snapshot after consumer commit")
}

func TestGroupUsageRollupConcurrentWritersDoNotWaitForPublisher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seed.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1)`)
	require.NoError(t, err)
	require.NoError(t, seed.Commit())
	publisher := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer publisher.Rollback()
	var id int
	require.NoError(t, publisher.QueryRowContext(ctx, `SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE`).Scan(&id))
	// 128 simultaneous caller goroutines sharing 32 local PG sessions, leaving
	// room for the harness and publisher. None can depend on the singleton.
	oldMax := integrationDB.Stats().MaxOpenConnections
	integrationDB.SetMaxOpenConns(32)
	defer integrationDB.SetMaxOpenConns(oldMax)
	start := make(chan struct{})
	results := make(chan error, 128)
	for i := 1; i <= 128; i++ {
		go func(id int) {
			<-start
			tx, err := integrationDB.BeginTx(ctx, nil)
			if err != nil {
				results <- err
				return
			}
			defer tx.Rollback()
			if err = setGroupUsageRollupTriggerSearchPath(ctx, tx, pq.QuoteIdentifier(schema)); err != nil {
				results <- err
				return
			}
			_, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='2s';`)
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES($1,1,10,0.25,'2026-08-13 12:00:00+08')`, id)
			}
			if err == nil {
				err = tx.Commit()
			}
			results <- err
		}(i)
	}
	close(start)
	for range 128 {
		require.NoError(t, <-results)
	}
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s.usage_group_rollup_invalidations`, pq.QuoteIdentifier(schema))).Scan(&count))
	require.Equal(t, 128, count)
}

func TestGroupUsageRollupSummaryUsesIndexedTailAndDirtyDayRanges(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer tx.Rollback()
	_, err := tx.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);
 CREATE INDEX usage_rollup_fixture_created_at ON usage_logs(created_at);
 INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at)
 SELECT n,1,10,0.01,TIMESTAMPTZ '2026-01-01 00:00:00+08'+n*INTERVAL '2 minutes' FROM generate_series(1,100000) n;
 DELETE FROM usage_group_rollup_invalidations;
 INSERT INTO usage_group_rollup_invalidations(affected_at,group_id) VALUES('2026-02-01 00:00:00+08',10);
 ANALYZE usage_logs;ANALYZE usage_group_rollup_invalidations;`)
	require.NoError(t, err)
	today := time.Date(2026, 5, 19, 16, 0, 0, 0, time.UTC)
	tail := time.Date(2026, 5, 18, 16, 0, 0, 0, time.UTC)
	rows, err := tx.QueryContext(ctx, "EXPLAIN (FORMAT JSON) "+groupUsageRollupSummarySQLGeili, today, today.AddDate(0, 0, -1), "2026-05-19", true, "2026-01-01", "2026-05-19", tail, "Asia/Shanghai")
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var plan string
	require.NoError(t, rows.Scan(&plan))
	// Both raw sources carry sargable timestamp conditions. Aggregation inside
	// LATERAL prevents pulling the dirty join into a full source-table scan.
	require.Contains(t, plan, "usage_rollup_fixture_created_at")
	require.Contains(t, plan, "Index Cond")
	var doc any
	require.NoError(t, json.Unmarshal([]byte(plan), &doc))
	var indexedRaw, seqRaw int
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if v["Relation Name"] == "usage_logs" {
				if _, ok := v["Index Name"]; ok {
					indexedRaw++
				}
				if v["Node Type"] == "Seq Scan" {
					seqRaw++
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(doc)
	require.Equal(t, 0, seqRaw, "raw historical and tail scans must not become a whole-table scan")
	require.GreaterOrEqual(t, indexedRaw, 2)
}

func TestGroupUsageRollupTriggerRollbackAndMultiDayCatchup(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seed.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);
 INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES
 (1,1,10,2,'2026-08-11 12:00:00+08'),(2,1,10,3,'2026-08-12 12:00:00+08'),(3,1,10,4,'2026-08-13 12:00:00+08');`)
	require.NoError(t, err)
	require.NoError(t, seed.Commit())
	failed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err = failed.ExecContext(ctx, `INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES(4,1,10,100,'2026-08-11 12:00:00+08')`)
	require.NoError(t, err)
	require.NoError(t, failed.Rollback())
	today := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	interrupted := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(interrupted).SyncGroupUsageRollups(ctx, today))
	require.NoError(t, interrupted.Rollback()) // crash before commit must retain both old state and every event
	check := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	var pending int
	require.NoError(t, check.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_group_rollup_invalidations`).Scan(&pending))
	require.Equal(t, 3, pending)
	require.NoError(t, check.Rollback())
	// Each separate transaction publishes one day. Unpublished later days must
	// remain in the raw tail after the first successful commit (crash/restart).
	for step := 0; step < 3; step++ {
		worker := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
		require.NoError(t, newDashboardAggregationRepositoryWithSQL(worker).SyncGroupUsageRollups(ctx, today))
		require.NoError(t, worker.Commit())
		read := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
		result, err := newUsageLogRepositoryWithSQL(nil, read).GetAllGroupUsageSummary(ctx, today)
		require.NoError(t, err)
		require.InDelta(t, 9, result[0].TotalCost, 1e-9)
		require.InDelta(t, 4, result[0].YesterdayCost, 1e-9)
		var closed string
		require.NoError(t, read.QueryRowContext(ctx, `SELECT closed_before::text FROM usage_group_rollup_state`).Scan(&closed))
		require.Equal(t, fmt.Sprintf("2026-08-%02d", 12+step), closed)
		require.NoError(t, read.Rollback())
	}
	reader := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer reader.Rollback()
	var events int
	require.NoError(t, reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_group_rollup_invalidations`).Scan(&events))
	require.Zero(t, events)
}

func TestGroupUsageRollupPartialPartitionRemovalDirtySummary(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, true)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer tx.Rollback()
	_, err := tx.ExecContext(ctx, `DROP TABLE usage_logs_default;
 CREATE TABLE usage_logs_202603 PARTITION OF usage_logs FOR VALUES FROM ('2026-03-01 00:00:00+00') TO ('2026-04-01 00:00:00+00');
 CREATE TABLE usage_logs_default PARTITION OF usage_logs DEFAULT;
 INSERT INTO groups(id) VALUES(10);INSERT INTO users(id) VALUES(1);
 INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES
 (1,1,10,2,'2026-03-01 02:00:00+08'),(2,1,10,3,'2026-03-01 12:00:00+08'),(3,1,10,4,'2026-04-01 02:00:00+08'),(4,1,10,5,'2026-04-01 12:00:00+08');
 INSERT INTO usage_group_daily_rollups(bucket_date,group_id,actual_cost) VALUES('2026-03-01',10,5),('2026-04-01',10,9);
 DELETE FROM usage_group_rollup_invalidations;
 UPDATE usage_group_rollup_state SET closed_before='2026-04-02',retained_from='2026-03-01 00:00:00+08' WHERE id=1;`)
	require.NoError(t, err)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, invalidateGroupUsageRollupsRange(ctx, tx, start, start.AddDate(0, 1, 0)))
	_, err = tx.ExecContext(ctx, `DROP TABLE usage_logs_202603`)
	require.NoError(t, err)
	// UTC-month removal removes only part of the first/last Shanghai days.
	result, err := newUsageLogRepositoryWithSQL(nil, tx).GetAllGroupUsageSummary(ctx, time.Date(2026, 4, 1, 16, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.InDelta(t, 7, result[0].TotalCost, 1e-9)
	require.InDelta(t, 5, result[0].YesterdayCost, 1e-9)
}

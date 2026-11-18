//go:build integration

package repository

// Geili uses immutable per-change invalidations, not upstream's shared-row build
// marks. Exercise the same writer-progress and exact-rebuild contract after 251.
import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestGeiliRollupUpgrade251KeepsWritersIndependent(t *testing.T) {
	for _, partitioned := range []bool{false, true} {
		name := "ordinary"
		if partitioned {
			name = "partitioned"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
			schema := createGroupUsageRollupTriggerTestSchema(t, ctx, partitioned)
			dsn, err := url.Parse(integrationDSN)
			require.NoError(t, err)
			q := dsn.Query()
			q.Set("search_path", schema)
			q.Set("TimeZone", "Asia/Shanghai")
			dsn.RawQuery = q.Encode()
			db, err := sql.Open("postgres", dsn.String())
			require.NoError(t, err)
			defer db.Close()
			db.SetMaxOpenConns(8)
			_, err = db.ExecContext(ctx, `INSERT INTO groups(id) VALUES(10); INSERT INTO users(id) VALUES(1);
    INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES
    (1,1,10,1,TIMESTAMPTZ '2020-01-05 08:00+08'),(2,1,10,2,TIMESTAMPTZ '2020-01-06 08:00+08')`)
			require.NoError(t, err)
			// The post-upstream migration is idempotent and cannot lose queued events.
			raw, err := migrations.FS.ReadFile("287_preserve_rollup_queue_after_upstream251_geili.sql")
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, string(raw))
			require.NoError(t, err)
			blocker, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer blocker.Rollback()
			_, err = blocker.ExecContext(ctx, "SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE")
			require.NoError(t, err)
			writeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			_, err = db.ExecContext(writeCtx, `INSERT INTO usage_logs(id,user_id,group_id,actual_cost,created_at) VALUES(3,1,10,8,TIMESTAMPTZ '2020-01-07 08:00+08'); UPDATE usage_logs SET actual_cost=16 WHERE id=1; DELETE FROM usage_logs WHERE id=2`)
			stop()
			require.NoError(t, err, "usage writes must not wait for the locked rollup singleton")
			var events int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_group_rollup_invalidations").Scan(&events))
			require.Equal(t, 6, events, "exact independent old/new row events, no duplicated triggers")
			require.NoError(t, blocker.Rollback())
			today := time.Date(2020, 1, 7, 16, 0, 0, 0, time.UTC)
			require.NoError(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(ctx, today))
			rows, err := (&usageLogRepository{sql: db}).getAllGroupUsageSummaryFromRollups(ctx, today)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.InDelta(t, 24, rows[0].TotalCost, 1e-9)
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_group_rollup_invalidations").Scan(&events))
			require.Zero(t, events)
		})
	}
}

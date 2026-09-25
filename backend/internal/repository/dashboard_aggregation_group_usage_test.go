//go:build unit

package repository

import (
	"context"
	"database/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func expectGroupRollupState(mock sqlmock.Sqlmock, date string, retained time.Time, zone string) {
	mock.ExpectQuery(`SELECT closed_before::text, retained_from.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"closed_before", "retained_from", "timezone_name"}).AddRow(date, retained, zone))
}
func expectGroupRollupTailAndState(mock sqlmock.Sqlmock, today time.Time, closed string, retained time.Time, zone string) {
	mock.ExpectExec(`(?s)WITH consumed AS MATERIALIZED.*affected_at >= \$1.*WHERE event.id = consumed.id`).WithArgs(today).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE usage_group_rollup_state`).WithArgs(closed, retained, zone).WillReturnResult(sqlmock.NewResult(0, 1))
}
func expectGroupRollupNoop(mock sqlmock.Sqlmock, today time.Time, retained time.Time, zone string) {
	mock.ExpectBegin()
	expectGroupRollupState(mock, service.GroupUsageDate(today), retained, zone)
	mock.ExpectQuery(`SELECT MIN\(affected_at\).*affected_at < \$1`).WithArgs(today).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(nil))
	expectGroupRollupTailAndState(mock, today, service.GroupUsageDate(today), retained, zone)
	mock.ExpectCommit()
}
func TestDashboardAggregationRepositorySyncGroupUsageRollupsNoopsAtCurrentDate(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	today := service.GroupUsageTodayStart(time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC))
	retained := today.AddDate(0, 0, -2)
	expectGroupRollupNoop(mock, today, retained, "Asia/Shanghai")
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(context.Background(), today))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositorySyncGroupUsageRollupsPublishesOneDayPerTransaction(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	today := service.GroupUsageTodayStart(time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC))
	retained := today.AddDate(0, 0, -2)
	for cursor := retained; cursor.Before(today); cursor = cursor.AddDate(0, 0, 1) {
		day := service.GroupUsageDate(cursor)
		next := cursor.AddDate(0, 0, 1)
		mock.ExpectBegin()
		expectGroupRollupState(mock, day, retained, "Asia/Shanghai")
		mock.ExpectQuery(`SELECT MIN\(affected_at\)`).WithArgs(today).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(nil))
		mock.ExpectExec(`DELETE FROM usage_group_daily_rollups WHERE bucket_date = \$1`).WithArgs(day).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`(?s)INSERT INTO usage_group_daily_rollups.*created_at >= \$2 AND created_at < \$3`).WithArgs(day, cursor, next).WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectExec(`(?s)WITH consumed AS MATERIALIZED.*affected_at >= \$1 AND affected_at < \$2.*WHERE event.id = consumed.id`).WithArgs(cursor, next).WillReturnResult(sqlmock.NewResult(0, 2))
		expectGroupRollupTailAndState(mock, today, service.GroupUsageDate(next), retained, "Asia/Shanghai")
		mock.ExpectCommit()
	}
	expectGroupRollupNoop(mock, today, retained, "Asia/Shanghai")
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(context.Background(), today))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositorySyncGroupUsageRollupsRejectsFutureWatermark(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	today := service.GroupUsageTodayStart(time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC))
	mock.ExpectBegin()
	expectGroupRollupState(mock, "2026-08-15", today, "Asia/Shanghai")
	mock.ExpectRollback()
	require.ErrorContains(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(context.Background(), today), "未来")
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositorySyncGroupUsageRollupsRollsBackFailedDay(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	today := service.GroupUsageTodayStart(time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC))
	retained := today.AddDate(0, 0, -1)
	mock.ExpectBegin()
	expectGroupRollupState(mock, "2026-08-13", retained, "Asia/Shanghai")
	mock.ExpectQuery(`SELECT MIN\(affected_at\)`).WithArgs(today).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(nil))
	mock.ExpectExec(`DELETE FROM usage_group_daily_rollups`).WithArgs("2026-08-13").WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	require.ErrorIs(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(context.Background(), today), sql.ErrConnDone)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositoryRecomputeRangeInvalidatesWithoutSingletonLock(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	start := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)INSERT INTO usage_group_rollup_invalidations.*generate_series`).WithArgs(start, end, "Asia/Shanghai").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM usage_dashboard_hourly`).WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	require.ErrorIs(t, newDashboardAggregationRepositoryWithSQL(db).RecomputeRange(context.Background(), start, end), sql.ErrConnDone)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositoryRecomputeRangeCommitsBeforeBoundedGroupSync(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	now := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	repo.clock = func() time.Time { return now }
	today := service.GroupUsageTodayStart(now)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO usage_group_rollup_invalidations`).WithArgs(start, end, "Asia/Shanghai").WillReturnResult(sqlmock.NewResult(0, 1))
	for _, query := range []string{`DELETE FROM usage_dashboard_hourly WHERE`, `DELETE FROM usage_dashboard_hourly_users WHERE`, `DELETE FROM usage_dashboard_daily WHERE`, `DELETE FROM usage_dashboard_daily_users WHERE`, `INSERT INTO usage_dashboard_hourly_users`, `INSERT INTO usage_dashboard_daily_users`, `INSERT INTO usage_dashboard_hourly`, `INSERT INTO usage_dashboard_daily`} {
		mock.ExpectExec(query).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	expectGroupRollupNoop(mock, today, start, "Asia/Shanghai")
	require.NoError(t, repo.RecomputeRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositoryCleanupUsageLogsNonPartitionedUsesTransactionalTriggers(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	repo.clock = func() time.Time { return now }
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)DELETE FROM usage_logs.*RETURNING created_at`).WithArgs(cutoff, usageLogsCleanupBatchSize).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(cutoff.Add(-time.Hour)))
	mock.ExpectCommit()
	expectGroupRollupNoop(mock, service.GroupUsageTodayStart(now), cutoff, "Asia/Shanghai")
	require.NoError(t, repo.CleanupUsageLogs(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositoryCleanupUsageLogsNonPartitionedFailureRollsBackWithoutSync(t *testing.T) {
	db, mock := newSQLMock(t)
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)DELETE FROM usage_logs.*RETURNING created_at`).WithArgs(cutoff, usageLogsCleanupBatchSize).WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	require.ErrorIs(t, newDashboardAggregationRepositoryWithSQL(db).CleanupUsageLogs(context.Background(), cutoff), sql.ErrConnDone)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositoryCleanupUsageLogsPartitionedSortsAndInvalidatesFullRange(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	repo.clock = func() time.Time { return now }
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT c.relname`).WillReturnRows(sqlmock.NewRows([]string{"relname"}).AddRow("usage_logs_202606").AddRow("usage_logs_invalid").AddRow("usage_logs_202604").AddRow("usage_logs_202607"))
	for _, month := range []int{4, 6} {
		start := time.Date(2026, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO usage_group_rollup_invalidations.*generate_series`).WithArgs(start, start.AddDate(0, 1, 0), "Asia/Shanghai").WillReturnResult(sqlmock.NewResult(0, 31))
		mock.ExpectExec(`DROP TABLE IF EXISTS "usage_logs_` + start.Format("200601") + `"`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT tableoid, ctid.*WHERE created_at < \$1.*WHERE \(tableoid, ctid\) IN.*RETURNING created_at`).WithArgs(cutoff, usageLogsCleanupBatchSize).WillReturnRows(sqlmock.NewRows([]string{"created_at"}))
	mock.ExpectCommit()
	expectGroupRollupNoop(mock, service.GroupUsageTodayStart(now), cutoff, "Asia/Shanghai")
	require.NoError(t, repo.CleanupUsageLogs(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDashboardAggregationRepositoryCleanupUsageLogsPartitionFailureRollsBackAndStops(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	cutoff := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT c.relname`).WillReturnRows(sqlmock.NewRows([]string{"relname"}).AddRow("usage_logs_202606").AddRow("usage_logs_202604"))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO usage_group_rollup_invalidations`).WithArgs(start, start.AddDate(0, 1, 0), "Asia/Shanghai").WillReturnResult(sqlmock.NewResult(0, 31))
	mock.ExpectExec(`DROP TABLE IF EXISTS "usage_logs_202604"`).WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	require.ErrorIs(t, newDashboardAggregationRepositoryWithSQL(db).CleanupUsageLogs(context.Background(), cutoff), sql.ErrConnDone)
	require.NoError(t, mock.ExpectationsWereMet())
}
func setGroupUsageRollupTestTimezone(t *testing.T) {
	t.Helper()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
}

func TestDashboardAggregationRepositorySyncGroupUsageRollupsRetriesConsumerSnapshotConflict(t *testing.T) {
	setGroupUsageRollupTestTimezone(t)
	db, mock := newSQLMock(t)
	today := service.GroupUsageTodayStart(time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC))
	retained := today.AddDate(0, 0, -2)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT closed_before::text, retained_from.*FOR UPDATE`).WillReturnError(&pq.Error{Code: "40001"})
	mock.ExpectRollback()
	expectGroupRollupNoop(mock, today, retained, "Asia/Shanghai")
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(db).SyncGroupUsageRollups(context.Background(), today))
	require.NoError(t, mock.ExpectationsWereMet())
}

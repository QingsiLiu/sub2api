package repository

import (
	"database/sql/driver"

	"github.com/DATA-DOG/go-sqlmock"
)

// expectCompactUsageDeleteGeili expects one deleteUsageLogsCompactGeili batch
// (without its outer transaction). A non-nil err fails the DELETE statement.
func expectCompactUsageDeleteGeili(mock sqlmock.Sqlmock, deleted int64, err error, args ...driver.Value) {
	mock.ExpectExec(`set_config\('geili.usage_bulk_delete','on',true\)`).WillReturnResult(sqlmock.NewResult(0, 1))
	query := mock.ExpectQuery(`(?s)deleted AS \(DELETE FROM usage_logs.*RETURNING group_id,created_at.*INSERT INTO usage_group_rollup_invalidations`).
		WithArgs(append(args, sqlmock.AnyArg())...)
	if err != nil {
		query.WillReturnError(err)
		return
	}
	query.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(deleted))
	mock.ExpectExec(`set_config\('geili.usage_bulk_delete','off',true\)`).WillReturnResult(sqlmock.NewResult(0, 1))
}

// Package subscriptionv2 installs the additive production side-table schema in
// SQLite fixtures whose Ent schema builder cannot create raw migration tables.
package subscriptionv2

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Wei-Shaw/sub2api/migrations"
)

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func CreateSQLiteSchema(ctx context.Context, db executor) error {
	raw, err := migrations.FS.ReadFile("253_subscription_contract_v2.sql")
	if err != nil {
		return err
	}
	ddl := strings.Split(string(raw), "-- Lock each parent")[0]
	lines := strings.Split(ddl, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines[i] = ""
		}
	}
	ddl = strings.Join(lines, "\n")
	ddl = strings.NewReplacer("BIGSERIAL PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT", "TIMESTAMPTZ", "DATETIME", "DEFAULT NOW()", "DEFAULT CURRENT_TIMESTAMP").Replace(ddl)
	for _, statement := range strings.Split(ddl, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err = db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

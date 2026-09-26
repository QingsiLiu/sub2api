package repository

import (
	"context"
	"fmt"
)

const financialUsageIndexesMigration = "262_usage_financial_indexes_notx.sql"
const financialLookupIndexMigration = "267_subscription_billing_lookup_notx.sql"
const financialLookupIndexName = "subscription_requests_settled_identity_lookup_geili"
const financialRollupTriggerMigration = "260_group_usage_rollup_invalidation_queue.sql"

var financialUsageMigrationIndexes = []string{
	"subscription_requests_billing_identity_geili",
	"subscription_request_contracts_usage_date_geili",
	"subscription_requests_settled_admitted_geili",
	"usage_settlement_accounting_date_geili",
	"usage_settlement_delivered_log_geili",
}

func validateFinancialMigrationIndexes(ctx context.Context, db migrationConnection, name string) error {
	indexes := financialUsageMigrationIndexes
	if name == financialLookupIndexMigration {
		indexes = []string{financialLookupIndexName}
	} else if name != financialUsageIndexesMigration {
		return nil
	}
	for _, indexName := range indexes {
		var ready bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM pg_class idx
 JOIN pg_namespace ns ON ns.oid=idx.relnamespace
 JOIN pg_index i ON i.indexrelid=idx.oid
 WHERE ns.nspname='public' AND idx.relname=$1 AND i.indisvalid AND i.indisready
 )`, indexName).Scan(&ready); err != nil {
			return fmt.Errorf("check financial index %s: %w", indexName, err)
		}
		if !ready {
			return fmt.Errorf("financial index %s is missing or invalid; do not mark this migration applied", indexName)
		}
	}
	return nil
}

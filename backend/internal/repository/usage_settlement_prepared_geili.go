package repository

import (
	"context"
	"database/sql"
)

// Only the two fixed claim plans are retained. Every poll still executes with
// fresh NOW(), limit, lease duration and token; no queue result is cached. The
// service owns the unchanged cadence/concurrency and DB.Close owns statements.
type usageSettlementPreparedClaimsGeili struct {
	db                *sql.DB
	pending, delivery accountRuntimeStatementGeili
}

func newUsageSettlementPreparedClaimsGeili(db *sql.DB) *usageSettlementPreparedClaimsGeili {
	if db == nil {
		return nil
	}
	return &usageSettlementPreparedClaimsGeili{db: db}
}

const pendingSettlementClaimSQLGeili = `WITH selected AS (
 SELECT id FROM usage_settlement_receipts WHERE state='pending' AND command IS NOT NULL AND record_source='live' AND next_settlement_at<=NOW() AND (settlement_lease_until IS NULL OR settlement_lease_until<NOW()) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1)
 UPDATE usage_settlement_receipts r SET settlement_lease_token=$2,settlement_lease_until=NOW()+($3*INTERVAL '1 second'),settlement_attempts=settlement_attempts+1 FROM selected s WHERE r.id=s.id RETURNING r.id,r.command`
const deliverySettlementClaimSQLGeili = `WITH selected AS (
 SELECT id FROM usage_settlement_receipts WHERE state='settled' AND delivered_at IS NULL AND record_source='live' AND record_completeness='complete' AND detail IS NOT NULL AND charged_amount IS NOT NULL AND next_delivery_at<=NOW() AND (delivery_lease_until IS NULL OR delivery_lease_until<NOW()) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1)
 UPDATE usage_settlement_receipts r SET delivery_lease_token=$2,delivery_lease_until=NOW()+($3*INTERVAL '1 second'),delivery_attempts=delivery_attempts+1 FROM selected s WHERE r.id=s.id RETURNING r.id`

func (r *usageBillingRepository) querySettlementClaimGeili(ctx context.Context, delivery bool, args ...any) (*sql.Rows, error) {
	query := pendingSettlementClaimSQLGeili
	if delivery {
		query = deliverySettlementClaimSQLGeili
	}
	plans := r.claimsGeili
	if plans == nil || plans.db != r.db {
		// Legacy explicit repositories retain their raw executor, never a plan
		// prepared against a different database.
		return r.db.QueryContext(ctx, query, args...)
	}
	slot := &plans.pending
	if delivery {
		slot = &plans.delivery
	}
	stmt, err := slot.statement(ctx, r.db, query)
	if err != nil {
		return nil, err
	}
	// Never issue a second raw claim after an uncertain execution error.
	return stmt.QueryContext(ctx, args...)
}

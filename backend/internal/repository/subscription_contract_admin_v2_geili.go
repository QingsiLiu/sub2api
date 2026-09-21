package repository

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
)

// selectContractLots prevents a partial admin adjustment from splitting a V2
// contract back into independent expiry dates. Legacy pools keep explicit IDs.
func selectContractLots(contract *geilisub.Contract, lots []geilisub.Lot, ids []int64, now time.Time) ([]geilisub.Lot, error) {
	if contract == nil || contract.Mode != geilisub.ContractModeV2 {
		return geilisub.ValidateSelection(lots, ids, now)
	}
	if !contract.ExpiresAt.After(now) {
		return nil, geilisub.ErrStateConflict
	}
	if err := geilisub.CheckMutable(lots); err != nil {
		return nil, err
	}
	selected := make([]geilisub.Lot, 0, contract.Quantity)
	for _, lot := range lots {
		if lot.Active(now) {
			selected = append(selected, lot)
		}
	}
	if len(ids) > 0 {
		if len(ids) != len(selected) {
			return nil, geilisub.ErrSelection
		}
		seen := map[int64]bool{}
		for _, id := range ids {
			if seen[id] {
				return nil, geilisub.ErrSelection
			}
			seen[id] = true
		}
		for _, lot := range selected {
			if !seen[lot.ID] {
				return nil, geilisub.ErrSelection
			}
		}
	}
	if len(selected) == 0 {
		return nil, geilisub.ErrStateConflict
	}
	return selected, nil
}

func refreshAdminContractExpiry(ctx context.Context, c *dbent.Client, contract *geilisub.Contract, expiry time.Time, now time.Time) error {
	if contract == nil {
		return nil
	}
	if _, err := c.ExecContext(ctx, `UPDATE subscription_contracts SET expires_at=$2,revision=revision+1,updated_at=$3 WHERE subscription_id=$1`, contract.SubscriptionID, expiry, now); err != nil {
		return err
	}
	_, err := c.ExecContext(ctx, `UPDATE subscription_contract_terms SET expires_at=$2 WHERE term_id=$1`, contract.TermID, expiry)
	return err
}

func resetAdminDailyLedger(ctx context.Context, c *dbent.Client, contract *geilisub.Contract, lots []geilisub.Lot, now time.Time) error {
	if contract == nil {
		return nil
	}
	if err := geilisub.SyncDailyLedger(ctx, c, contract.SubscriptionID, now); err != nil {
		return err
	}
	before, err := geilisub.ReadDailyUsage(ctx, c, contract.SubscriptionID, contract.TermID, now)
	if err != nil {
		return err
	}
	// A legacy pool projects out the usage of portions that expired today.
	// Preserve that audit offset when resetting the remaining live quota.
	offset := 0.0
	if contract.Mode == geilisub.ContractModeLegacy {
		offset = geilisub.RetiredDailyUsage(lots, now, contract.StartsAt)
	}
	effectiveBefore := geilisub.ContractSummary(contract, lots, before, now).DailyUsageUSD
	if _, err = c.ExecContext(ctx, `INSERT INTO subscription_daily_usage(subscription_id,term_id,usage_date,used_usd) VALUES($1,$2,$3,$4) ON CONFLICT(subscription_id,term_id,usage_date) DO UPDATE SET used_usd=EXCLUDED.used_usd`, contract.SubscriptionID, contract.TermID, geilisub.DayStart(now).Format("2006-01-02"), offset); err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, `UPDATE subscription_contracts SET revision=revision+1,updated_at=$2 WHERE subscription_id=$1`, contract.SubscriptionID, now); err != nil {
		return err
	}
	if len(lots) > 0 {
		return geilisub.RecordOperation(ctx, c, contract.SubscriptionID, lots[0].ID, "reset_daily", "admin", "", 0, map[string]any{"term_id": contract.TermID, "usage_date": geilisub.DayStart(now).Format("2006-01-02"), "before_daily_usage_usd": effectiveBefore, "after_daily_usage_usd": 0, "before_ledger_usage_usd": before, "after_ledger_usage_usd": offset})
	}
	return nil
}

func bumpAdminContractRevision(ctx context.Context, c *dbent.Client, id int64, now time.Time) error {
	_, err := c.ExecContext(ctx, `UPDATE subscription_contracts SET revision=revision+1,updated_at=$2 WHERE subscription_id=$1`, id, now)
	return err
}

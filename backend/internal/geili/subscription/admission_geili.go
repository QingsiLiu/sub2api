package subscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// SettleAdmission executes within the existing usage-billing/dedup transaction.
func SettleAdmission(ctx context.Context, tx *sql.Tx, key, billingID string, subID, apiKeyID int64, cost float64, now time.Time) error {
	if err := LockSubscription(ctx, tx, subID); err != nil {
		return err
	}
	var raw []byte
	var state, previousBilling string
	var admitted time.Time
	if err := tx.QueryRowContext(ctx, `SELECT lots,status,admitted_at,billing_request_id FROM subscription_requests WHERE request_key=$1 AND subscription_id=$2 AND api_key_id=$3 FOR UPDATE`, key, subID, apiKeyID).Scan(&raw, &state, &admitted, &previousBilling); err != nil {
		return err
	}
	if state == "settled" {
		if previousBilling == billingID {
			return nil
		}
		return errors.New("subscription request already settled under another billing identity")
	}
	if state != "admitted" {
		return errors.New("subscription request is not chargeable")
	}
	var snapshot []Lot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	current, err := LoadLots(ctx, tx, subID, true)
	if err != nil {
		return err
	}
	originals := append([]Lot(nil), current...)
	index := map[int64]int{}
	for i, e := range current {
		index[e.ID] = i
	}
	// Current usage prevents concurrent settlements from repeatedly consuming the
	// same capacity. Historical window identity prevents a late request charging a new window.
	basis := make([]Lot, 0, len(snapshot))
	for _, old := range snapshot {
		i, ok := index[old.ID]
		if !ok {
			return errors.New("admitted subscription entitlement missing")
		}
		e := current[i]
		if e.Status == "refunded" || e.Status == "refund_pending" {
			return errors.New("admitted entitlement has conflicting refund state")
		}
		e.Normalize(now, false)
		current[i] = e
		old.LifetimeUsageUSD = e.LifetimeUsageUSD

		historical := func(column string, anchor *time.Time, used float64) (float64, error) {
			var spent float64
			err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0) FROM subscription_usage_allocations WHERE entitlement_id=$1 AND id>$2 AND `+column+` IS NOT DISTINCT FROM $3`, old.ID, old.AllocationWatermark, anchor).Scan(&spent)
			return add(used, spent), err
		}
		if sameWindow(old.DailyWindowStart, e.DailyWindowStart) {
			old.DailyUsageUSD = e.DailyUsageUSD
		} else {
			old.DailyUsageUSD, err = historical("daily_window_start", old.DailyWindowStart, old.DailyUsageUSD)
			if err != nil {
				return err
			}
		}
		if sameWindow(old.WeeklyWindowStart, e.WeeklyWindowStart) {
			old.WeeklyUsageUSD = e.WeeklyUsageUSD
		} else {
			old.WeeklyUsageUSD, err = historical("weekly_window_start", old.WeeklyWindowStart, old.WeeklyUsageUSD)
			if err != nil {
				return err
			}
		}
		if sameWindow(old.MonthlyWindowStart, e.MonthlyWindowStart) {
			old.MonthlyUsageUSD = e.MonthlyUsageUSD
		} else {
			old.MonthlyUsageUSD, err = historical("monthly_window_start", old.MonthlyWindowStart, old.MonthlyUsageUSD)
			if err != nil {
				return err
			}
		}

		basis = append(basis, old)
	}
	allocated, err := Allocate(basis, cost, admitted)
	if err != nil {
		return err
	}
	before := map[int64]Lot{}
	for _, e := range basis {
		before[e.ID] = e
	}
	for _, e := range allocated {
		delta := add(e.LifetimeUsageUSD, -before[e.ID].LifetimeUsageUSD)
		if delta == 0 {
			continue
		}
		i := index[e.ID]
		cur := &current[i]
		if sameWindow(cur.DailyWindowStart, e.DailyWindowStart) {
			cur.DailyUsageUSD = add(cur.DailyUsageUSD, delta)
		}
		if sameWindow(cur.WeeklyWindowStart, e.WeeklyWindowStart) {
			cur.WeeklyUsageUSD = add(cur.WeeklyUsageUSD, delta)
		}
		if sameWindow(cur.MonthlyWindowStart, e.MonthlyWindowStart) {
			cur.MonthlyUsageUSD = add(cur.MonthlyUsageUSD, delta)
		}
		cur.LifetimeUsageUSD = add(cur.LifetimeUsageUSD, delta)
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd,daily_window_start,weekly_window_start,monthly_window_start) VALUES($1,$2,$3,$4,$5,$6)`, key, e.ID, delta, e.DailyWindowStart, e.WeeklyWindowStart, e.MonthlyWindowStart); err != nil {
			return err
		}
	}
	dirty := make([]Lot, 0, len(allocated))
	for i, e := range current {
		if UsageChanged(originals[i], e) {
			dirty = append(dirty, e)
		}
	}
	if err := SaveLots(ctx, tx, dirty); err != nil {
		return err
	}
	if err := SyncAggregate(ctx, tx, subID, current, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE subscription_requests SET status='settled',settled_at=$2,billing_request_id=$3,cost_usd=$4 WHERE request_key=$1`, key, now, billingID, cost)
	return err
}
func sameWindow(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

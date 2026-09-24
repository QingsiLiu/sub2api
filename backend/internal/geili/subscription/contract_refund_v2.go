package subscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/shopspring/decimal"
)

type contractRefundRecord struct {
	ID, SubscriptionID    int64
	Before                *Contract
	After                 Contract
	BeforeLots, AfterLots []Lot
	Baseline              decimal.Decimal
	Status, ParentStatus  string
	ReversedAt            *time.Time
}

func loadContractRefund(ctx context.Context, c *dbent.Client, orderID int64) (*contractRefundRecord, error) {
	r := &contractRefundRecord{}
	var before, after, lotsBefore, lotsAfter []byte
	err := scalar(ctx, c, `SELECT id,subscription_id,before_contract,after_contract,before_lots,after_lots,lifetime_usage_baseline,refund_status,frozen_parent_status,reversed_at FROM subscription_contract_changes WHERE order_id=$1`, []any{orderID}, &r.ID, &r.SubscriptionID, &before, &after, &lotsBefore, &lotsAfter, &r.Baseline, &r.Status, &r.ParentStatus, &r.ReversedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrContractRefund
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range []struct {
		raw   []byte
		value any
	}{{before, &r.Before}, {after, &r.After}, {lotsBefore, &r.BeforeLots}, {lotsAfter, &r.AfterLots}} {
		if len(entry.raw) > 0 {
			if err = json.Unmarshal(entry.raw, entry.value); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}

func validateContractRefund(ctx context.Context, c *dbent.Client, orderID int64, allowFrozen bool) (*Contract, *contractRefundRecord, error) {
	record, err := loadContractRefund(ctx, c, orderID)
	if err != nil {
		return nil, nil, err
	}
	if _, err = LockParent(ctx, c, record.SubscriptionID); err != nil {
		return nil, nil, err
	}
	current, err := LoadContract(ctx, c, record.SubscriptionID)
	if err != nil {
		return nil, nil, err
	}
	if current == nil || record.ReversedAt != nil || current.TermID != record.After.TermID || current.Revision != record.After.Revision {
		return nil, nil, ErrContractRefund
	}
	if current.Status != "active" && !(allowFrozen && record.Status == "frozen" && current.Status == "suspended") {
		return nil, nil, ErrContractRefund
	}
	if !allowFrozen && record.Status == "frozen" {
		return nil, nil, ErrContractRefund
	}
	lots, err := ReadLots(ctx, c, record.SubscriptionID)
	if err != nil {
		return nil, nil, err
	}
	for _, lot := range lots {
		if lot.SourceType == "campaign" && lot.ExpiresAt.After(current.StartsAt) {
			return nil, nil, ErrContractRefund
		}
	}
	var spent decimal.Decimal
	var admitted int
	if err = scalar(ctx, c, `SELECT COALESCE(SUM(lifetime_usage_usd),0) FROM user_subscription_entitlements WHERE user_subscription_id=$1`, []any{record.SubscriptionID}, &spent); err != nil {
		return nil, nil, err
	}
	if !spent.Equal(record.Baseline) {
		return nil, nil, ErrContractRefund
	}
	if err = scalar(ctx, c, `SELECT COUNT(*) FROM subscription_requests WHERE subscription_id=$1 AND status='admitted'`, []any{record.SubscriptionID}, &admitted); err != nil {
		return nil, nil, err
	}
	if admitted > 0 {
		return nil, nil, ErrContractRefund
	}
	var last int64
	if err = scalar(ctx, c, `SELECT COALESCE(MAX(id),0) FROM subscription_contract_changes WHERE subscription_id=$1`, []any{record.SubscriptionID}, &last); err != nil {
		return nil, nil, err
	}
	if last != record.ID {
		return nil, nil, ErrContractRefund
	}
	return current, record, nil
}

func ValidateContractRefund(ctx context.Context, c *dbent.Client, orderID int64, now time.Time) (*Contract, error) {
	current, _, err := validateContractRefund(ctx, c, orderID, false)
	return current, err
}

// FreezeContractRefund precedes the external provider call. Unsettled requests
// are never aged out as proof of no usage, and pending refunds remain frozen.
func FreezeContractRefund(ctx context.Context, c *dbent.Client, orderID int64, now time.Time) (*Contract, error) {
	current, record, err := validateContractRefund(ctx, c, orderID, true)
	if err != nil {
		return nil, err
	}
	if record.Status == "frozen" {
		return current, nil
	}
	if _, err = c.ExecContext(ctx, `UPDATE subscription_contract_changes SET refund_status='frozen',frozen_parent_status=$2 WHERE order_id=$1`, orderID, current.Status); err != nil {
		return nil, err
	}
	for _, lot := range record.AfterLots {
		if lot.Status == "active" {
			if err = c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetStatus("refund_pending").Exec(ctx); err != nil {
				return nil, err
			}
		}
	}
	if err = c.UserSubscription.UpdateOneID(current.SubscriptionID).SetStatus("suspended").Exec(ctx); err != nil {
		return nil, err
	}
	current.Status = "suspended"
	return current, nil
}

func RestoreContractRefund(ctx context.Context, c *dbent.Client, orderID int64, now time.Time) (*Contract, error) {
	current, record, err := validateContractRefund(ctx, c, orderID, true)
	if err != nil {
		return nil, err
	}
	if record.Status != "frozen" {
		return current, nil
	}
	status := record.ParentStatus
	if !current.ExpiresAt.After(now) {
		status = "expired"
	}
	for _, lot := range record.AfterLots {
		if lot.Status == "active" {
			if err = c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetStatus(lot.Status).Exec(ctx); err != nil {
				return nil, err
			}
		}
	}
	if err = c.UserSubscription.UpdateOneID(current.SubscriptionID).SetStatus(status).Exec(ctx); err != nil {
		return nil, err
	}
	if _, err = c.ExecContext(ctx, `UPDATE subscription_contract_changes SET refund_status='failed' WHERE order_id=$1`, orderID); err != nil {
		return nil, err
	}
	current.Status = status
	return current, nil
}

func RevertContractChange(ctx context.Context, c *dbent.Client, orderID int64, now time.Time) (*Contract, error) {
	existing, err := loadContractRefund(ctx, c, orderID)
	if err != nil {
		return nil, err
	}
	if existing.ReversedAt != nil {
		return LoadContract(ctx, c, existing.SubscriptionID)
	}
	current, record, err := validateContractRefund(ctx, c, orderID, true)
	if err != nil {
		return nil, err
	}
	if record.Status != "frozen" {
		return nil, ErrContractRefund
	}
	originals := map[int64]Lot{}
	for _, lot := range record.BeforeLots {
		originals[lot.ID] = lot
	}
	for _, lot := range record.AfterLots {
		previous, ok := originals[lot.ID]
		if !ok {
			if err = c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetStatus("refunded").SetRefundedAt(now).Exec(ctx); err != nil {
				return nil, err
			}
			continue
		}
		u := c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetStatus(previous.Status).SetStartsAt(previous.StartsAt).SetExpiresAt(previous.ExpiresAt)
		if previous.PlanID == nil {
			u.ClearPlanID()
		} else {
			u.SetPlanID(*previous.PlanID)
		}
		if previous.DailyLimitUSD == nil {
			u.ClearDailyLimitUsd()
		} else {
			u.SetDailyLimitUsd(*previous.DailyLimitUSD)
		}
		if previous.WeeklyLimitUSD == nil {
			u.ClearWeeklyLimitUsd()
		} else {
			u.SetWeeklyLimitUsd(*previous.WeeklyLimitUSD)
		}
		if previous.MonthlyLimitUSD == nil {
			u.ClearMonthlyLimitUsd()
		} else {
			u.SetMonthlyLimitUsd(*previous.MonthlyLimitUSD)
		}
		if err = u.Exec(ctx); err != nil {
			return nil, err
		}
	}
	restored := *current
	if record.Before != nil {
		restored = *record.Before
	} else {
		restored.ExpiresAt = now
		restored.Status = "expired"
	}
	restored.Revision = current.Revision + 1
	if !restored.ExpiresAt.After(now) {
		restored.Status = "expired"
	} else {
		restored.Status = "active"
	}
	if err = saveContract(ctx, c, &restored); err != nil {
		return nil, err
	}
	used, err := ReadDailyUsage(ctx, c, restored.SubscriptionID, restored.TermID, now)
	if err != nil {
		return nil, err
	}
	if err = c.UserSubscription.UpdateOneID(restored.SubscriptionID).SetPlanID(restored.PlanID).SetStartsAt(restored.StartsAt).SetExpiresAt(restored.ExpiresAt).SetStatus(restored.Status).SetDailyUsageUsd(used).Exec(ctx); err != nil {
		return nil, err
	}
	if _, err = c.ExecContext(ctx, `UPDATE subscription_contract_changes SET refund_status='reversed',reversed_at=$2 WHERE order_id=$1`, orderID, now); err != nil {
		return nil, err
	}
	return &restored, nil
}

// MarkLegacyContract preserves fulfillment of already-paid V1 orders. A legacy
// operation can create independent expiries, so self-service V2 changes stop.
func MarkLegacyContract(ctx context.Context, c *dbent.Client, subID int64, now time.Time) (*Contract, error) {
	if _, err := LockParent(ctx, c, subID); err != nil {
		return nil, err
	}
	contract, err := EnsureContract(ctx, c, subID, now)
	if err != nil {
		return nil, err
	}
	lots, err := ReadLots(ctx, c, subID)
	if err != nil {
		return nil, err
	}
	_, expiry, status := ParentProjection(lots, now)
	contract.Mode = ContractModeLegacy
	contract.Kind = ""
	contract.Quantity = 0
	contract.PeriodDays = 0
	contract.UnitDailyUSD = 0
	contract.ExpiresAt = expiry
	contract.Status = status
	contract.Revision++
	if err = saveContract(ctx, c, contract); err != nil {
		return nil, err
	}
	return contract, nil
}

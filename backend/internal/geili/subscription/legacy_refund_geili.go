package subscription

import (
	"context"
	"encoding/json"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionrequest"
	"github.com/shopspring/decimal"
)

func refundAffectedLots(r *contractRefundRecord) []Lot {
	if r.After.Mode != ContractModeLegacy {
		return r.AfterLots
	}
	before := map[int64]Lot{}
	for _, l := range r.BeforeLots {
		before[l.ID] = l
	}
	var affected []Lot
	for _, l := range r.AfterLots {
		b, ok := before[l.ID]
		if !ok || !sameLegacyRight(l, legacyRight(b)) {
			affected = append(affected, l)
		}
	}
	return affected
}
func validateLegacyRefund(ctx context.Context, c *dbent.Client, current *Contract, r *contractRefundRecord, allowFrozen bool) error {
	frozen := allowFrozen && r.Status == "frozen"
	if current.Status != "active" && !(frozen && current.Status == "expired") {
		return ErrContractRefund
	}
	if !allowFrozen && r.Status == "frozen" {
		return ErrContractRefund
	}
	lots, err := ReadLots(ctx, c, current.SubscriptionID)
	if err != nil {
		return err
	}
	byID := map[int64]Lot{}
	for _, l := range lots {
		byID[l.ID] = l
	}
	affected := refundAffectedLots(r)
	if len(affected) == 0 {
		return ErrContractRefund
	}
	ids := map[int64]bool{}
	for _, a := range affected {
		ids[a.ID] = true
		l, ok := byID[a.ID]
		if !ok {
			return ErrContractRefund
		}
		expected := legacyRight(a)
		if frozen {
			expected.Status = "refund_pending"
		}
		if !frozen && !l.Active(time.Now()) {
			return ErrContractRefund
		}
		if !sameLegacyRight(l, expected) || !decimal.NewFromFloat(l.LifetimeUsageUSD).Equal(decimal.NewFromFloat(a.LifetimeUsageUSD)) {
			return ErrContractRefund
		}
	}
	pending, err := c.SubscriptionRequest.Query().Where(subscriptionrequest.SubscriptionIDEQ(current.SubscriptionID), subscriptionrequest.StatusEQ("admitted")).All(ctx)
	if err != nil {
		return err
	}
	for _, r := range pending {
		var snapshot []Lot
		if json.Unmarshal(r.Lots, &snapshot) != nil {
			return ErrContractRefund
		}
		for _, l := range snapshot {
			if ids[l.ID] {
				return ErrContractRefund
			}
		}
	}
	var last int64
	if err = scalar(ctx, c, `SELECT COALESCE(MAX(id),0) FROM subscription_contract_changes WHERE subscription_id=$1`, []any{current.SubscriptionID}, &last); err != nil {
		return err
	}
	if last != r.ID {
		return ErrContractRefund
	}
	return nil
}
func loadContractAfterLegacyRefund(ctx context.Context, c *dbent.Client, id int64, err error) (*Contract, error) {
	if err != nil {
		return nil, err
	}
	return LoadContract(ctx, c, id)
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionentitlementorder"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionrefund"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionrequest"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var errLotRefundManual = infraerrors.Conflict("SUBSCRIPTION_REFUND_MANUAL_REVIEW", "subscription refund requires manual review; automatic deduction was not applied")

type lotRefundSnapshot struct {
	Lot          geilisub.Lot `json:"lot"`
	Operation    string       `json:"operation"`
	Days         int          `json:"days"`
	BeforeExpiry *time.Time   `json:"before_expiry"`
}

func validateLotRefund(ctx context.Context, c *dbent.Client, o *dbent.PaymentOrder, amount float64, now time.Time) ([]lotRefundSnapshot, error) {
	if amount != o.Amount {
		return nil, errLotRefundManual
	}
	lines, err := c.SubscriptionEntitlementOrder.Query().Where(subscriptionentitlementorder.OrderIDEQ(o.ID)).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, errLotRefundManual
	}
	var result []lotRefundSnapshot
	for _, line := range lines {
		row, err := c.UserSubscriptionEntitlement.Get(ctx, line.EntitlementID)
		if err != nil {
			return nil, err
		}
		e := geilisub.FromEntity(row)
		changes, err := c.QueryContext(ctx, `SELECT 1 FROM subscription_contract_changes WHERE subscription_id=$1 LIMIT 1`, e.UserSubscriptionID)
		if err != nil {
			return nil, err
		}
		hasChanges := changes.Next()
		scanErr := changes.Err()
		_ = changes.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if hasChanges {
			return nil, errLotRefundManual
		}
		if e.SourceType == "legacy" || line.ReversedAt != nil || !e.Active(now) || e.LifetimeUsageUSD > 0 || line.AfterExpiresAt == nil || !line.AfterExpiresAt.Equal(e.ExpiresAt) {
			return nil, errLotRefundManual
		}
		later, err := c.SubscriptionEntitlementOrder.Query().Where(subscriptionentitlementorder.EntitlementIDEQ(e.ID), subscriptionentitlementorder.IDGT(line.ID), subscriptionentitlementorder.ReversedAtIsNil()).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if later {
			return nil, errLotRefundManual
		}
		if line.Operation == "renew" && line.BeforeExpiresAt == nil {
			return nil, errLotRefundManual
		}
		if line.Operation != "create" && line.Operation != "renew" {
			return nil, errLotRefundManual
		}
		pending, err := c.SubscriptionRequest.Query().Where(subscriptionrequest.SubscriptionIDEQ(e.UserSubscriptionID), subscriptionrequest.StatusEQ("admitted")).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if pending {
			return nil, errLotRefundManual
		}
		result = append(result, lotRefundSnapshot{Lot: e, Operation: line.Operation, Days: line.DaysAdded, BeforeExpiry: line.BeforeExpiresAt})
	}
	return result, nil
}

func (s *PaymentService) freezeLotRefund(ctx context.Context, p *RefundPlan) error {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	parent, err := geilisub.LockParent(tc, c, p.SubscriptionID)
	if err != nil {
		return err
	}
	if _, err := geilisub.EnsureContract(tc, c, p.SubscriptionID, time.Now()); err != nil {
		return err
	}
	if parent.Status != "active" {
		return errLotRefundManual
	}
	o, err := c.PaymentOrder.Query().Unique(false).Where(paymentorder.IDEQ(p.OrderID), geilisub.LockRows).Only(tc)
	if err != nil {
		return err
	}
	if o.Status != OrderStatusCompleted && o.Status != OrderStatusRefundRequested && o.Status != OrderStatusRefundFailed {
		return infraerrors.Conflict("CONFLICT", "refund already in progress or completed")
	}
	existing, err := c.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(o.ID)).Only(tc)
	if err != nil && !dbent.IsNotFound(err) {
		return err
	}
	if existing != nil && existing.Status != "failed" {
		return errLotRefundManual
	}
	snap, err := validateLotRefund(tc, c, o, p.RefundAmount, time.Now())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if existing == nil {
		err = c.SubscriptionRefund.Create().SetOrderID(o.ID).SetSubscriptionID(p.SubscriptionID).SetSnapshot(raw).Exec(tc)
	} else {
		err = c.SubscriptionRefund.UpdateOneID(existing.ID).SetStatus("pending").SetSnapshot(raw).Exec(tc)
	}
	if err != nil {
		return err
	}
	for _, r := range snap {
		if err := c.UserSubscriptionEntitlement.UpdateOneID(r.Lot.ID).SetStatus("refund_pending").Exec(tc); err != nil {
			return err
		}
	}
	if err := c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefunding).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).Exec(tc); err != nil {
		return err
	}
	if err := geilisub.RefreshParent(tc, c, p.SubscriptionID, time.Now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidateLotRefundCache(ctx, p.SubscriptionID)
	return nil
}
func (s *PaymentService) executeLotRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	if err := s.freezeLotRefund(ctx, p); err != nil {
		return nil, err
	}
	resp, err := s.gwRefund(ctx, p)
	if err != nil {
		// Transport/unknown outcomes are not evidence of failed money movement.
		return s.markLotRefundPending(ctx, p, resp, err)
	}
	if resp != nil && resp.Status == payment.ProviderStatusFailed {
		return s.finalizeLotRefund(ctx, p, false)
	}
	return s.finishRefund(ctx, p, resp)
}
func (s *PaymentService) markLotRefundPending(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse, cause error) (*RefundResult, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	journal, err := c.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(p.OrderID)).Only(tc)
	if err != nil {
		return nil, err
	}
	if _, err := geilisub.LockParent(tc, c, journal.SubscriptionID); err != nil {
		return nil, err
	}
	journal, err = c.SubscriptionRefund.Query().Unique(false).Where(subscriptionrefund.IDEQ(journal.ID), geilisub.LockRows).Only(tc)
	if err != nil {
		return nil, err
	}
	if journal.Status == "succeeded" {
		return &RefundResult{Success: true}, nil
	}
	if journal.Status != "pending" {
		return &RefundResult{Success: false, Warning: "refund already finalized"}, nil
	}
	changed, err := c.PaymentOrder.Update().Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusIn(OrderStatusRefunding, OrderStatusRefundPending)).SetStatus(OrderStatusRefundPending).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).Save(tc)
	if err != nil {
		return nil, err
	}
	if changed != 1 {
		return nil, errLotRefundManual
	}
	detail, err := json.Marshal(map[string]any{"refundID": refundResponseID(resp), "deductionRollbackOK": false, "lotRefund": true, "error": psErrMsg(cause)})
	if err != nil {
		return nil, err
	}
	if err := c.PaymentAuditLog.Create().SetOrderID(geilisub.PurchaseReference(p.OrderID)).SetAction("REFUND_PENDING").SetOperator("system").SetDetail(string(detail)).Exec(tc); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &RefundResult{Success: false, Warning: "refund outcome is pending; affected entitlements remain frozen"}, nil

}
func (s *PaymentService) finalizeLotRefund(ctx context.Context, p *RefundPlan, success bool) (*RefundResult, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	journal, err := c.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(p.OrderID)).Only(tc)
	if err != nil {
		return nil, err
	}
	if _, err := geilisub.LockParent(tc, c, journal.SubscriptionID); err != nil {
		return nil, err
	}
	journal, err = c.SubscriptionRefund.Query().Unique(false).Where(subscriptionrefund.IDEQ(journal.ID), geilisub.LockRows).Only(tc)
	if err != nil {
		return nil, err
	}
	if journal.Status == "succeeded" {
		return &RefundResult{Success: true}, nil
	}
	if journal.Status != "pending" {
		return nil, errLotRefundManual
	}
	var snap []lotRefundSnapshot
	if err := json.Unmarshal(journal.Snapshot, &snap); err != nil {
		return nil, err
	}
	now := time.Now()
	for _, r := range snap {
		current, err := c.UserSubscriptionEntitlement.Get(tc, r.Lot.ID)
		if err != nil {
			return nil, err
		}
		if current.Status != "refund_pending" {
			return nil, errLotRefundManual
		}
		status := r.Lot.Status
		expiry := r.Lot.ExpiresAt
		if success {
			if r.Operation == "renew" {
				if r.BeforeExpiry == nil {
					return nil, errLotRefundManual
				}
				expiry = *r.BeforeExpiry
				if !expiry.After(now) {
					status = "expired"
				}
			} else {
				status = "refunded"
			}
		}
		u := c.UserSubscriptionEntitlement.UpdateOneID(r.Lot.ID).SetStatus(status).SetExpiresAt(expiry)
		if status == "refunded" {
			u.SetRefundedAt(now)
		}
		if err := u.Exec(tc); err != nil {
			return nil, err
		}
		if success {
			if _, err := c.SubscriptionEntitlementOrder.Update().Where(subscriptionentitlementorder.OrderIDEQ(p.OrderID), subscriptionentitlementorder.EntitlementIDEQ(r.Lot.ID)).SetReversedAt(now).Save(tc); err != nil {
				return nil, err
			}
		}
		action := "refund_restore"
		if success {
			action = "refund"
		}
		if err := geilisub.RecordOperation(tc, c, journal.SubscriptionID, r.Lot.ID, action, "payment", geilisub.PurchaseReference(p.OrderID), 0, map[string]any{"operation": r.Operation, "expires_at": expiry}); err != nil {
			return nil, err
		}
	}
	state := "failed"
	if success {
		state = "succeeded"
	}
	if err := c.SubscriptionRefund.UpdateOneID(journal.ID).SetStatus(state).Exec(tc); err != nil {
		return nil, err
	}
	if err := geilisub.RefreshParent(tc, c, journal.SubscriptionID, now); err != nil {
		return nil, err
	}
	if _, err := geilisub.MarkLegacyContract(tc, c, journal.SubscriptionID, now); err != nil {
		return nil, err
	}
	var result *RefundResult
	if success {
		copy := *p
		copy.SubscriptionLots = nil
		result, err = s.markRefundOkTx(tc, c, &copy)
	} else {
		err = c.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(OrderStatusRefundFailed).SetFailedReason("payment provider confirmed refund failure").Exec(tc)
		result = &RefundResult{Success: false, Warning: "provider confirmed failure; entitlements restored"}
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateLotRefundCache(ctx, journal.SubscriptionID)
	return result, nil
}
func (s *PaymentService) invalidateLotRefundCache(ctx context.Context, id int64) {
	if s.subscriptionSvc == nil {
		return
	}
	if s.entClient != nil {
		row, err := s.entClient.UserSubscription.Get(ctx, id)
		if err == nil {
			gid := int64(0)
			if row.GroupID != nil {
				gid = *row.GroupID
			}
			_ = s.subscriptionSvc.invalidateSubscriptionCaches(row.UserID, gid, id)
		}
		return
	}
	if s.subscriptionSvc.userSubRepo == nil {
		return
	}
	sub, err := s.subscriptionSvc.GetByID(ctx, id)
	if err == nil {
		_ = s.subscriptionSvc.invalidateSubscriptionCaches(sub.UserID, sub.GroupID, id)
	}
}

func (s *PaymentService) hasLotRefund(ctx context.Context, id int64) (bool, error) {
	return s.entClient.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(id)).Exist(ctx)
}

// Used by old in-process callers/tests. The public refund workflow requires a
// persisted journal; this primitive never revokes original rights for a renewal.
func applyLotAdjustments(ctx context.Context, c *dbent.Client, p *RefundPlan) error {
	parents := map[int64]bool{}
	for _, adj := range p.SubscriptionLots {
		e, err := c.UserSubscriptionEntitlement.Get(ctx, adj.ID)
		if err != nil {
			return err
		}
		parents[e.UserSubscriptionID] = true
		u := c.UserSubscriptionEntitlement.UpdateOneID(e.ID)
		if adj.Operation == "renew" {
			if adj.Days <= 0 {
				return errors.New("invalid renewal reversal")
			}
			expiry := e.ExpiresAt.AddDate(0, 0, -adj.Days)
			status := "active"
			if !expiry.After(time.Now()) {
				status = "expired"
			}
			u.SetExpiresAt(expiry).SetStatus(status)
		} else {
			u.SetStatus("refunded").SetRefundedAt(time.Now())
		}
		if err := u.Exec(ctx); err != nil {
			return fmt.Errorf("refund entitlement: %w", err)
		}
	}
	for id := range parents {
		if err := geilisub.RefreshParent(ctx, c, id, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

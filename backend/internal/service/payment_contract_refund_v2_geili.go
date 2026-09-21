package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionrefund"
	"github.com/Wei-Shaw/sub2api/ent/user"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *PaymentService) prepareSubscriptionV2Refund(ctx context.Context, p *RefundPlan) (*RefundPlan, *RefundResult, error) {
	if p.RefundAmount != p.Order.Amount {
		return nil, nil, errLotRefundManual
	}
	assigned, err := hasPaymentSubscriptionAssignmentAudit(ctx, s.entClient, p.OrderID)
	if err != nil {
		return nil, nil, err
	}
	p.SubscriptionV2 = true
	if !assigned {
		p.SubscriptionUnassigned = true
		p.DeductionType = payment.DeductionTypeNone
		return p, nil, nil
	}
	if !p.DeductBalance {
		return nil, nil, errLotRefundManual
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(p.Order.UserID), geilisub.LockRows).Only(ctx); err != nil {
		return nil, nil, err
	}
	contract, err := geilisub.ValidateContractRefund(ctx, c, p.OrderID, time.Now())
	if err != nil {
		return nil, nil, err
	}
	p.SubscriptionID = contract.SubscriptionID
	p.DeductionType = payment.DeductionTypeSubscription
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return p, nil, nil
}

func (s *PaymentService) executeSubscriptionV2Refund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(p.Order.UserID), geilisub.LockRows).Only(tc); err != nil {
		return nil, err
	}
	o, err := c.PaymentOrder.Query().Unique(false).Where(paymentorder.IDEQ(p.OrderID), geilisub.LockRows).Only(tc)
	if err != nil {
		return nil, err
	}
	if o.Status != OrderStatusCompleted && o.Status != OrderStatusRefundRequested && o.Status != OrderStatusRefundFailed && !(o.Status == OrderStatusFailed && o.PaidAt != nil) {
		return nil, infraerrors.Conflict("CONFLICT", "refund already in progress or completed")
	}
	if p.RefundAmount != o.Amount {
		return nil, errLotRefundManual
	}
	assigned, err := hasPaymentSubscriptionAssignmentAudit(tc, c, o.ID)
	if err != nil {
		return nil, err
	}
	if assigned == p.SubscriptionUnassigned {
		return nil, errLotRefundManual
	}
	if assigned {
		contract, err := geilisub.FreezeContractRefund(tc, c, o.ID, time.Now())
		if err != nil {
			return nil, err
		}
		p.SubscriptionID = contract.SubscriptionID
		raw, _ := json.Marshal(map[string]any{"version": 2, "order_id": o.ID, "subscription_id": contract.SubscriptionID})
		journal, err := c.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(o.ID)).Only(tc)
		if err != nil && !dbent.IsNotFound(err) {
			return nil, err
		}
		if journal == nil {
			err = c.SubscriptionRefund.Create().SetOrderID(o.ID).SetSubscriptionID(contract.SubscriptionID).SetSnapshot(raw).Exec(tc)
		} else if journal.Status == "failed" {
			err = c.SubscriptionRefund.UpdateOneID(journal.ID).SetStatus("pending").SetSnapshot(raw).Exec(tc)
		} else {
			return nil, errLotRefundManual
		}
		if err != nil {
			return nil, err
		}
	}
	if err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefunding).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).Exec(tc); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if p.SubscriptionID > 0 {
		s.invalidateLotRefundCache(ctx, p.SubscriptionID)
	}
	resp, gwErr := s.gwRefund(ctx, p)
	if assigned {
		return s.finishSubscriptionV2Refund(ctx, p, resp, gwErr)
	}
	return s.finishUnassignedSubscriptionV2Refund(ctx, p, resp, gwErr)
}

func (s *PaymentService) finishSubscriptionV2Refund(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse, cause error) (*RefundResult, error) {
	if cause != nil || resp == nil {
		return s.markLotRefundPending(ctx, p, resp, cause)
	}
	switch strings.TrimSpace(resp.Status) {
	case payment.ProviderStatusSuccess, payment.ProviderStatusRefunded:
		return s.finalizeSubscriptionV2Refund(ctx, p, true)
	case payment.ProviderStatusFailed:
		return s.finalizeSubscriptionV2Refund(ctx, p, false)
	default:
		return s.markLotRefundPending(ctx, p, resp, nil)
	}
}

func (s *PaymentService) finalizeSubscriptionV2Refund(ctx context.Context, p *RefundPlan, success bool) (*RefundResult, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(p.Order.UserID), geilisub.LockRows).Only(tc); err != nil {
		return nil, err
	}
	journal, err := c.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(p.OrderID)).Only(tc)
	if err != nil {
		return nil, err
	}
	if _, err = geilisub.LockParent(tc, c, journal.SubscriptionID); err != nil {
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
	var contract *geilisub.Contract
	if success {
		contract, err = geilisub.RevertContractChange(tc, c, p.OrderID, time.Now())
	} else {
		contract, err = geilisub.RestoreContractRefund(tc, c, p.OrderID, time.Now())
	}
	if err != nil {
		return nil, err
	}
	state := "failed"
	if success {
		state = "succeeded"
	}
	if err = c.SubscriptionRefund.UpdateOneID(journal.ID).SetStatus(state).Exec(tc); err != nil {
		return nil, err
	}
	var result *RefundResult
	if success {
		copy := *p
		copy.SubscriptionLots = nil
		copy.SubDaysToDeduct = 0
		result, err = s.markRefundOkTx(tc, c, &copy)
	} else {
		err = c.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(OrderStatusRefundFailed).SetFailedReason("payment provider confirmed refund failure").Exec(tc)
		result = &RefundResult{Warning: "provider confirmed failure; subscription restored"}
	}
	if err != nil {
		return nil, fmt.Errorf("finalize subscription refund: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateLotRefundCache(ctx, contract.SubscriptionID)
	return result, nil
}

// Unfulfilled purchases have no subscription parent to freeze. The paid order
// itself is their durable refund journal, locked through every finalization.
func (s *PaymentService) finishUnassignedSubscriptionV2Refund(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse, cause error) (*RefundResult, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	tc := dbent.NewTxContext(ctx, tx)
	c := tx.Client()
	if _, err = c.User.Query().Unique(false).Where(user.IDEQ(p.Order.UserID), geilisub.LockRows).Only(tc); err != nil {
		return nil, err
	}
	order, err := c.PaymentOrder.Query().Unique(false).Where(paymentorder.IDEQ(p.OrderID), geilisub.LockRows).Only(tc)
	if err != nil {
		return nil, err
	}
	if order.Status == OrderStatusRefunded {
		return &RefundResult{Success: true}, nil
	}
	confirmedSuccess := cause == nil && resp != nil && (strings.TrimSpace(resp.Status) == payment.ProviderStatusSuccess || strings.TrimSpace(resp.Status) == payment.ProviderStatusRefunded)
	if order.Status == OrderStatusRefundFailed && !confirmedSuccess {
		return &RefundResult{Warning: "provider confirmed refund failure; payment remains awaiting review"}, nil
	}
	if order.Status != OrderStatusRefunding && order.Status != OrderStatusRefundPending && !(order.Status == OrderStatusRefundFailed && confirmedSuccess) {
		return nil, infraerrors.Conflict("CONFLICT", "refund is not in progress")
	}
	assigned, err := hasPaymentSubscriptionAssignmentAudit(tc, c, order.ID)
	if err != nil {
		return nil, err
	}
	if assigned {
		return nil, errLotRefundManual
	}
	copy := *p
	copy.Order = order
	copy.RefundAmount = order.RefundAmount
	copy.BalanceToDeduct = 0
	copy.SubDaysToDeduct = 0
	copy.SubscriptionLots = nil
	copy.DeductionType = payment.DeductionTypeNone
	if copy.RefundAmount != order.Amount {
		return nil, errLotRefundManual
	}
	status := ""
	if resp != nil {
		status = strings.TrimSpace(resp.Status)
	}
	var result *RefundResult
	if cause == nil && (status == payment.ProviderStatusSuccess || status == payment.ProviderStatusRefunded) {
		result, err = s.markRefundOkTx(tc, c, &copy)
	} else if cause == nil && status == payment.ProviderStatusFailed {
		err = c.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefundFailed).SetFailedAt(time.Now()).SetFailedReason("payment provider confirmed refund failure; paid subscription still awaits review").Exec(tc)
		result = &RefundResult{Warning: "provider confirmed refund failure; payment remains awaiting review"}
	} else {
		err = c.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefundPending).Exec(tc)
		if err == nil {
			raw, _ := json.Marshal(map[string]any{"refundID": refundResponseID(resp), "deductionRollbackOK": false, "error": psErrMsg(cause), "unassignedSubscription": true})
			err = c.PaymentAuditLog.Create().SetOrderID(geilisub.PurchaseReference(order.ID)).SetAction("REFUND_PENDING").SetOperator("system").SetDetail(string(raw)).Exec(tc)
		}
		result = &RefundResult{Warning: "refund outcome is pending confirmation"}
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

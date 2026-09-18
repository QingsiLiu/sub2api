//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionrefund"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func preparedLotRefund(t *testing.T, renew bool) (*PaymentService, *RefundPlan, *dbent.UserSubscriptionEntitlement, time.Time) {
	t.Helper()
	c, lot, original := auditLotFixture(t)
	ctx := context.Background()
	o := auditRefundOrder(t, c, lot)
	_, err := c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusCompleted).Save(ctx)
	require.NoError(t, err)
	_, err = c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetSourceType("payment").SetSourceOrderID(o.ID).Save(ctx)
	require.NoError(t, err)
	op := "create"
	b := c.SubscriptionEntitlementOrder.Create().SetEntitlementID(lot.ID).SetOrderID(o.ID).SetAfterExpiresAt(lot.ExpiresAt).SetDaysAdded(30)
	if renew {
		op = "renew"
		b.SetBeforeExpiresAt(original)
	}
	require.NoError(t, b.SetOperation(op).Exec(ctx))
	svc := &PaymentService{entClient: c}
	p := &RefundPlan{OrderID: o.ID, Order: o, SubscriptionID: lot.UserSubscriptionID, RefundAmount: o.Amount, GatewayAmount: o.PayAmount, Reason: "synthetic refund", DeductBalance: true, DeductionType: payment.DeductionTypeSubscription, SubDaysToDeduct: 30, SubscriptionLots: []SubscriptionLotAdjustment{{ID: lot.ID, Days: 30, Operation: op}}}
	return svc, p, lot, original
}
func TestLotRefundJournalTerminalOutcomes(t *testing.T) {
	for _, outcome := range []string{payment.ProviderStatusSuccess, payment.ProviderStatusFailed, payment.ProviderStatusPending} {
		t.Run(outcome, func(t *testing.T) {
			s, p, lot, _ := preparedLotRefund(t, false)
			ctx := context.Background()
			require.NoError(t, s.freezeLotRefund(ctx, p))
			e, err := s.entClient.UserSubscriptionEntitlement.Get(ctx, lot.ID)
			require.NoError(t, err)
			require.Equal(t, "refund_pending", e.Status)
			result, err := s.finishRefund(ctx, p, &payment.RefundResponse{Status: outcome})
			require.NoError(t, err)
			e, err = s.entClient.UserSubscriptionEntitlement.Get(ctx, lot.ID)
			require.NoError(t, err)
			expected := map[string]string{payment.ProviderStatusSuccess: "refunded", payment.ProviderStatusFailed: "active", payment.ProviderStatusPending: "refund_pending"}
			require.Equal(t, expected[outcome], e.Status)
			require.Equal(t, outcome == payment.ProviderStatusSuccess, result.Success)
			if outcome == payment.ProviderStatusSuccess {
				result, err = s.finishRefund(ctx, p, &payment.RefundResponse{Status: outcome})
				require.NoError(t, err)
				require.True(t, result.Success)
				n, err := s.entClient.PaymentAuditLog.Query().Where(paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
				require.NoError(t, err)
				require.Equal(t, 1, n)
			}
		})
	}
}
func TestLotRefundRenewalPreservesOtherPurchase(t *testing.T) {
	s, p, lot, original := preparedLotRefund(t, true)
	ctx := context.Background()
	sibling, err := s.entClient.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(lot.UserSubscriptionID).SetStatus("active").SetStartsAt(time.Now().Add(-time.Hour)).SetExpiresAt(lot.ExpiresAt.AddDate(0, 0, 5)).SetDailyLimitUsd(45).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, s.freezeLotRefund(ctx, p))
	_, err = s.finishRefund(ctx, p, &payment.RefundResponse{Status: payment.ProviderStatusSuccess})
	require.NoError(t, err)
	got, err := s.entClient.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.Equal(t, "active", got.Status)
	require.WithinDuration(t, original, got.ExpiresAt, time.Microsecond)
	other, err := s.entClient.UserSubscriptionEntitlement.Get(ctx, sibling.ID)
	require.NoError(t, err)
	require.Equal(t, sibling.Status, other.Status)
	require.True(t, sibling.ExpiresAt.Equal(other.ExpiresAt))
	parent, err := s.entClient.UserSubscription.Get(ctx, lot.UserSubscriptionID)
	require.NoError(t, err)
	require.Equal(t, "active", parent.Status)
	require.True(t, parent.ExpiresAt.Equal(sibling.ExpiresAt))
}
func TestLotRefundRejectsUnsafeCases(t *testing.T) {
	for _, kind := range []string{"used", "partial", "inflight", "later_renewal", "expired"} {
		t.Run(kind, func(t *testing.T) {
			s, p, lot, _ := preparedLotRefund(t, false)
			ctx := context.Background()
			switch kind {
			case "used":
				require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetLifetimeUsageUsd(0.01).Exec(ctx))
			case "partial":
				p.RefundAmount = 5
			case "inflight":
				raw, _ := json.Marshal([]int{})
				require.NoError(t, s.entClient.SubscriptionRequest.Create().SetRequestKey("synthetic").SetSubscriptionID(lot.UserSubscriptionID).SetAPIKeyID(1).SetLots(raw).SetAdmittedAt(time.Now()).Exec(ctx))
			case "later_renewal":
				next := auditRefundOrder(t, s.entClient, lot)
				require.NoError(t, s.entClient.SubscriptionEntitlementOrder.Create().SetEntitlementID(lot.ID).SetOrderID(next.ID).SetOperation("renew").SetDaysAdded(30).SetAfterExpiresAt(lot.ExpiresAt).Exec(ctx))
			case "expired":
				require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
			}
			require.ErrorIs(t, s.freezeLotRefund(ctx, p), errLotRefundManual)
			n, err := s.entClient.SubscriptionRefund.Query().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
func TestLotRefundPersistFailureRollsBackEntireFinalization(t *testing.T) {
	s, p, lot, _ := preparedLotRefund(t, false)
	ctx := context.Background()
	require.NoError(t, s.freezeLotRefund(ctx, p))
	journal, err := s.entClient.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(p.OrderID)).Only(ctx)
	require.NoError(t, err)
	var snap []lotRefundSnapshot
	require.NoError(t, json.Unmarshal(journal.Snapshot, &snap))
	bad := snap[0]
	bad.Lot.ID += 999
	snap = append(snap, bad)
	raw, err := json.Marshal(snap)
	require.NoError(t, err)
	require.NoError(t, s.entClient.SubscriptionRefund.UpdateOneID(journal.ID).SetSnapshot(raw).Exec(ctx))
	_, err = s.finishRefund(ctx, p, &payment.RefundResponse{Status: payment.ProviderStatusSuccess})
	require.Error(t, err)
	got, err := s.entClient.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.Equal(t, "refund_pending", got.Status)
	order, err := s.entClient.PaymentOrder.Get(ctx, p.OrderID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefunding, order.Status)
	// Repair fixture and retry the already confirmed outcome; only local finalization repeats.
	raw, err = json.Marshal(snap[:1])
	require.NoError(t, err)
	require.NoError(t, s.entClient.SubscriptionRefund.UpdateOneID(journal.ID).SetSnapshot(raw).Exec(ctx))
	_, err = s.finishRefund(ctx, p, &payment.RefundResponse{Status: payment.ProviderStatusSuccess})
	require.NoError(t, err)
}

func TestLotRefundLatePendingResponseCannotUndoSuccess(t *testing.T) {
	s, p, lot, _ := preparedLotRefund(t, false)
	ctx := context.Background()
	require.NoError(t, s.freezeLotRefund(ctx, p))
	result, err := s.finishRefund(ctx, p, &payment.RefundResponse{Status: payment.ProviderStatusSuccess})
	require.NoError(t, err)
	require.True(t, result.Success)
	result, err = s.finishRefund(ctx, p, &payment.RefundResponse{Status: payment.ProviderStatusPending})
	require.NoError(t, err)
	require.True(t, result.Success)
	o, err := s.entClient.PaymentOrder.Get(ctx, p.OrderID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefunded, o.Status)
	e, err := s.entClient.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.Equal(t, "refunded", e.Status)
}

func TestLotRefundLegacyUsageHistoryRequiresManualReview(t *testing.T) {
	s, p, lot, _ := preparedLotRefund(t, true)
	ctx := context.Background()
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetSourceType("legacy").SetLifetimeUsageUsd(0).Exec(ctx))
	require.ErrorIs(t, s.freezeLotRefund(ctx, p), errLotRefundManual)
}

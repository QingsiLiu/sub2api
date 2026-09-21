//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionV2QuoteTokenFailureMatrix(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
	for _, tc := range []struct {
		name, token string
		uid         int64
		at          time.Time
	}{
		{"empty", "", u.ID, time.Now()}, {"oversize", strings.Repeat("a", 32769), u.ID, time.Now()}, {"missing-signature", "payload", u.ID, time.Now()}, {"signature-altered", q.QuoteID + "x", u.ID, time.Now()}, {"wrong-owner", q.QuoteID, u.ID + 1, time.Now()}, {"exact-expiry", q.QuoteID, u.ID, q.ExpiresAt}, {"late", q.QuoteID, u.ID, q.ExpiresAt.Add(time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.readSubscriptionV2Quote(tc.token, tc.uid, tc.at)
			require.Error(t, err)
		})
	}
	original, err := s.readSubscriptionV2Quote(q.QuoteID, u.ID, time.Now())
	require.NoError(t, err)
	for _, kind := range []string{"future-issued", "excessive-ttl", "wrong-domain", "wrong-version", "zero-owner", "wrong-after-owner", "nonpositive-plan", "zero-price", "negative-price"} {
		t.Run(kind, func(t *testing.T) {
			copy := *original
			switch kind {
			case "future-issued":
				copy.IssuedAt = time.Now().Add(time.Minute)
				copy.ExpiresAt = copy.IssuedAt.Add(time.Minute)
			case "excessive-ttl":
				copy.ExpiresAt = copy.IssuedAt.Add(6 * time.Minute)
			case "wrong-domain":
				copy.TokenType = "wechat_payment_resume"
			case "wrong-version":
				copy.Version = 1
			case "zero-owner":
				copy.UserID = 0
			case "wrong-after-owner":
				copy.Change.After.UserID++
			case "nonpositive-plan":
				copy.Change.After.PlanID = 0
			case "zero-price":
				copy.Change.Amount = copy.Change.Amount.Mul(decimal.Zero)
			case "negative-price":
				copy.Change.Amount = copy.Change.Amount.Neg()
			}
			token, err := s.paymentResume().createSignedToken(copy)
			require.NoError(t, err)
			_, err = s.readSubscriptionV2Quote(token, u.ID, time.Now())
			require.Error(t, err)
		})
	}
	s.resumeService = NewPaymentResumeService(nil)
	_, err = s.readSubscriptionV2Quote(q.QuoteID, u.ID, time.Now())
	require.Equal(t, "PAYMENT_RESUME_NOT_CONFIGURED", infraerrors.Reason(err))
}

func TestSubscriptionV2QuoteOperationIntegerMatrix(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "active"}[active], func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			if active {
				q := v2Quote(t, s, u.ID, p[2], "purchase", 2, 0)
				o, err := v2Create(t, s, u, q)
				require.NoError(t, err)
				v2Fulfill(t, s, o)
			}
			for _, op := range []string{"purchase", "stack", "renew", "upgrade", "", "downgrade"} {
				for _, n := range []int{-1, 0, 1, 10, 11} {
					units, periods := n, 0
					target := p[2]
					if op == "renew" {
						units, periods = 0, n
					}
					if op == "upgrade" {
						target = p[4]
					}
					q, err := s.QuoteSubscription(context.Background(), SubscriptionQuoteRequest{UserID: u.ID, PlanID: target.ID, Operation: op, Units: units, Periods: periods})
					valid := !active && op == "purchase" && n >= 1 && n <= 10 || active && (op == "stack" || op == "renew") && n >= 1 && n <= 10 || active && op == "upgrade" && n == 0
					if valid {
						require.NoError(t, err, "op=%s n=%d", op, n)
						require.NotNil(t, q)
					} else {
						require.Error(t, err, "op=%s n=%d", op, n)
					}
				}
			}
			for _, op := range []string{"purchase", "stack", "renew", "upgrade"} {
				_, err := s.QuoteSubscription(context.Background(), SubscriptionQuoteRequest{UserID: u.ID, PlanID: p[4].ID, Operation: op, Units: 1, Periods: 1})
				require.Error(t, err)
			}
		})
	}
}

func TestSubscriptionV2PendingStatusMatrix(t *testing.T) {
	for _, tc := range []struct {
		status                           string
		paid, expired, assigned, blocked bool
	}{
		{OrderStatusPending, false, false, false, true}, {OrderStatusPending, false, true, false, false}, {OrderStatusCancelled, false, false, false, false}, {OrderStatusExpired, false, false, false, false}, {OrderStatusFailed, false, false, false, false}, {OrderStatusFailed, true, false, false, true}, {OrderStatusPaid, true, false, false, true}, {OrderStatusRecharging, true, false, false, true}, {OrderStatusRefundRequested, true, false, false, true}, {OrderStatusRefunding, true, false, false, true}, {OrderStatusRefundPending, true, false, false, true}, {OrderStatusRefundFailed, true, false, false, true}, {OrderStatusRefundFailed, true, false, true, false}, {OrderStatusRefunded, true, false, false, false},
	} {
		t.Run(tc.status+map[bool]string{true: "-paid", false: "-unpaid"}[tc.paid]+map[bool]string{true: "-expired", false: ""}[tc.expired]+map[bool]string{true: "-assigned", false: ""}[tc.assigned], func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			b := s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(tc.status)
			if tc.paid {
				b.SetPaidAt(time.Now())
			}
			if tc.expired {
				b.SetExpiresAt(time.Now().Add(-time.Second))
			}
			require.NoError(t, b.Exec(context.Background()))
			if tc.assigned {
				require.NoError(t, s.entClient.PaymentAuditLog.Create().SetOrderID(geilisub.PurchaseReference(o.ID)).SetAction("SUBSCRIPTION_ASSIGNED").SetOperator("test").SetDetail("{}").Exec(context.Background()))
			}
			err = checkSubscriptionPending(context.Background(), s.entClient, u.ID)
			if tc.blocked {
				require.Equal(t, "SUBSCRIPTION_ORDER_PENDING", infraerrors.Reason(err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSubscriptionV2UnassignedRefundLateResponsesAndRecovery(t *testing.T) {
	for _, initial := range []string{OrderStatusRefunding, OrderStatusRefundPending, OrderStatusRefundFailed} {
		t.Run(initial, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			ctx := context.Background()
			o, err = s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(initial).SetPaidAt(time.Now()).SetRefundAmount(o.Amount).Save(ctx)
			require.NoError(t, err)
			plan := s.refundFinalizePlan(o)
			result, err := s.finishUnassignedSubscriptionV2Refund(ctx, plan, nil, errors.New("connection closed"))
			require.NoError(t, err)
			require.False(t, result.Success)
			result, err = s.finishUnassignedSubscriptionV2Refund(ctx, plan, &payment.RefundResponse{Status: payment.ProviderStatusSuccess}, nil)
			require.NoError(t, err)
			require.True(t, result.Success)
			for _, late := range []*payment.RefundResponse{nil, {Status: payment.ProviderStatusPending}, {Status: payment.ProviderStatusFailed}, {Status: payment.ProviderStatusSuccess}} {
				result, err = s.finishUnassignedSubscriptionV2Refund(ctx, plan, late, nil)
				require.NoError(t, err)
				require.True(t, result.Success)
			}
			current, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusRefunded, current.Status)
			n, err := s.entClient.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(geilisub.PurchaseReference(o.ID)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, n)
		})
	}
}

func TestSubscriptionV2ExactRefundAllOperationsAfterPriorUsage(t *testing.T) {
	for _, op := range []string{"purchase", "stack", "renew", "upgrade"} {
		for _, priorUse := range []bool{false, true} {
			if op == "purchase" && priorUse {
				continue
			}
			t.Run(op+map[bool]string{true: "-prior-use", false: "-unused"}[priorUse], func(t *testing.T) {
				s, u, p := v2PaymentFixture(t)
				q := v2Quote(t, s, u.ID, p[2], "purchase", 2, 0)
				order, err := v2Create(t, s, u, q)
				require.NoError(t, err)
				before := v2Fulfill(t, s, order)
				ctx := context.Background()
				if priorUse {
					lots, err := geilisub.ReadLots(ctx, s.entClient, before.SubscriptionID)
					require.NoError(t, err)
					require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(lots[0].ID).SetLifetimeUsageUsd(12.345).SetDailyUsageUsd(12.345).Exec(ctx))
					_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_daily_usage SET used_usd=12.345 WHERE subscription_id=$1`, before.SubscriptionID)
					require.NoError(t, err)
				}
				if op != "purchase" {
					switch op {
					case "stack":
						q = v2Quote(t, s, u.ID, p[2], op, 2, 0)
					case "renew":
						q = v2Quote(t, s, u.ID, p[2], op, 0, 2)
					case "upgrade":
						q = v2Quote(t, s, u.ID, p[4], op, 0, 0)
					}
					order, err = v2Create(t, s, u, q)
					require.NoError(t, err)
					v2Fulfill(t, s, order)
				}
				order, err = s.entClient.PaymentOrder.Get(ctx, order.ID)
				require.NoError(t, err)
				plan, _, err := s.prepareSubscriptionV2Refund(ctx, &RefundPlan{Order: order, OrderID: order.ID, RefundAmount: order.Amount, DeductBalance: true})
				require.NoError(t, err)
				tx, err := s.entClient.Tx(ctx)
				require.NoError(t, err)
				_, err = geilisub.FreezeContractRefund(ctx, tx.Client(), order.ID, time.Now())
				require.NoError(t, err)
				require.NoError(t, tx.SubscriptionRefund.Create().SetOrderID(order.ID).SetSubscriptionID(before.SubscriptionID).SetSnapshot(json.RawMessage(`{"version":2}`)).Exec(ctx))
				require.NoError(t, tx.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Exec(ctx))
				require.NoError(t, tx.Commit())
				result, err := s.finishSubscriptionV2Refund(ctx, plan, &payment.RefundResponse{Status: payment.ProviderStatusSuccess}, nil)
				require.NoError(t, err)
				require.True(t, result.Success)
				got, err := geilisub.LoadContract(ctx, s.entClient, before.SubscriptionID)
				require.NoError(t, err)
				if op == "purchase" {
					require.False(t, got.Active(time.Now()))
				} else {
					require.Equal(t, before.Quantity, got.Quantity)
					require.Equal(t, before.UnitDailyUSD, got.UnitDailyUSD)
					require.True(t, before.ExpiresAt.Equal(got.ExpiresAt))
					require.Equal(t, before.TermID, got.TermID)
				}
				used, err := geilisub.ReadDailyUsage(ctx, s.entClient, before.SubscriptionID, before.TermID, time.Now())
				require.NoError(t, err)
				if priorUse {
					require.Equal(t, 12.345, used)
				} else {
					require.Zero(t, used)
				}
				result, err = s.finishSubscriptionV2Refund(ctx, plan, &payment.RefundResponse{Status: payment.ProviderStatusPending}, nil)
				require.NoError(t, err)
				require.True(t, result.Success)
			})
		}
	}
}

func TestSubscriptionV2QuoteAndOrderFeeParityMatrix(t *testing.T) {
	s, u, plans := v2PaymentFixture(t)
	for _, p := range plans {
		for _, currency := range []string{"CNY", "USD"} {
			for _, fee := range []float64{0, 2.5} {
				q := v2Quote(t, s, u.ID, p, "purchase", 3, 0)
				req, err := s.prepareSubscriptionV2Order(CreateOrderRequest{UserID: u.ID, QuoteID: q.QuoteID, OrderType: "subscription", PaymentType: "alipay", Amount: 99999})
				require.NoError(t, err)
				_, paid, err := calculateCreateOrderPayAmountForOrderType(q.OrderAmount, fee, currency, "subscription", 7.14)
				require.NoError(t, err)
				o, err := s.createOrderInTx(context.Background(), req, &User{ID: u.ID, Email: u.Email}, p, &PaymentConfig{MaxPendingOrders: 10}, q.OrderAmount, q.OrderAmount, fee, paid, &payment.InstanceSelection{ProviderKey: "stripe", Config: map[string]string{"currency": currency}})
				require.NoError(t, err)
				snap, err := readSubscriptionV2Snapshot(o)
				require.NoError(t, err)
				require.Equal(t, q.OrderAmount, o.Amount)
				require.Equal(t, q.OrderAmount, snap.Change.Amount.InexactFloat64())
				require.Equal(t, paid, o.PayAmount)
				require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusCancelled).Exec(context.Background()))
			}
		}
	}
	for _, amount := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -1} {
		require.False(t, isValidProviderAmount(amount))
	}
}

// These fixture repositories use the real enclosing Ent transaction: a rejected
// grant must roll back the redeem flag, not merely leave an in-memory stub alone.
type v2RedeemTransactionRepo struct {
	redeemRejectRepo
	c *dbent.Client
}

func (r *v2RedeemTransactionRepo) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	row, err := r.c.RedeemCode.Query().Where(redeemcode.CodeEQ(code)).Only(ctx)
	if err != nil {
		return nil, err
	}
	return &RedeemCode{ID: row.ID, Code: row.Code, Type: row.Type, Status: row.Status, PlanID: row.PlanID, GroupID: row.GroupID, ValidityDays: row.ValidityDays}, nil
}
func (r *v2RedeemTransactionRepo) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	row, err := r.c.RedeemCode.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RedeemCode{ID: row.ID, Code: row.Code, Type: row.Type, Status: row.Status, PlanID: row.PlanID, GroupID: row.GroupID, ValidityDays: row.ValidityDays, UsedBy: row.UsedBy}, nil
}
func (r *v2RedeemTransactionRepo) Use(ctx context.Context, id, userID int64) error {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return errors.New("redeem must use transaction")
	}
	n, err := tx.RedeemCode.Update().Where(redeemcode.IDEQ(id), redeemcode.StatusEQ(StatusUnused)).SetStatus(StatusUsed).SetUsedBy(userID).SetUsedAt(time.Now()).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRedeemCodeUsed
	}
	return nil
}
func TestSubscriptionV2RedeemRollbackPreservesUnusedCode(t *testing.T) {
	for _, kind := range []string{"multiunit", "cross-type", "partial-days", "negative-days", "pending-order"} {
		t.Run(kind, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			s.subscriptionSvc = &SubscriptionService{entClient: s.entClient, userSubRepo: v2SubscriptionReadRepo{c: s.entClient}, now: time.Now}
			q := v2Quote(t, s, u.ID, p[2], "purchase", 2, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			before := v2Fulfill(t, s, o)
			target, days := p[2], 30
			switch kind {
			case "cross-type":
				target, days = p[0], 7
			case "partial-days":
				days = 3
			case "negative-days":
				days = -30
			case "pending-order":
				q = v2Quote(t, s, u.ID, p[2], "stack", 1, 0)
				_, err = v2Create(t, s, u, q)
				require.NoError(t, err)
			}
			row, err := s.entClient.RedeemCode.Create().SetCode("synthetic-" + kind).SetType(RedeemTypeSubscription).SetStatus(StatusUnused).SetPlanID(target.ID).SetValidityDays(days).Save(context.Background())
			require.NoError(t, err)
			r := &v2RedeemTransactionRepo{c: s.entClient}
			redeemer := NewRedeemService(r, &mockUserRepo{getByIDUser: &User{ID: u.ID}}, s.subscriptionSvc, nil, nil, s.entClient, nil, nil)
			_, err = redeemer.Redeem(context.Background(), u.ID, row.Code)
			require.Error(t, err)
			row, err = s.entClient.RedeemCode.Get(context.Background(), row.ID)
			require.NoError(t, err)
			require.Equal(t, StatusUnused, row.Status)
			require.Nil(t, row.UsedBy)
			got, err := geilisub.LoadContract(context.Background(), s.entClient, before.SubscriptionID)
			require.NoError(t, err)
			require.Equal(t, before.Revision, got.Revision)
			require.True(t, before.ExpiresAt.Equal(got.ExpiresAt))
		})
	}
}

func TestSubscriptionV2UnassignedRefundRecoversInterruptedGatewayCall(t *testing.T) {
	for _, state := range []string{OrderStatusRefunding, OrderStatusRefundPending} {
		for _, outcome := range []string{payment.ProviderStatusSuccess, payment.ProviderStatusFailed, payment.ProviderStatusPending} {
			t.Run(state+"-"+outcome, func(t *testing.T) {
				s, u, p := v2PaymentFixture(t)
				q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
				o, err := v2Create(t, s, u, q)
				require.NoError(t, err)
				ctx := context.Background()
				inst, err := s.entClient.PaymentProviderInstance.Create().SetProviderKey(payment.TypeStripe).SetName("synthetic-refund").SetConfig("{}").SetSupportedTypes("stripe").SetRefundEnabled(true).Save(ctx)
				require.NoError(t, err)
				require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(o.ID).SetPaymentType(payment.TypeStripe).SetProviderInstanceID(geilisub.PurchaseReference(inst.ID)).SetPaymentTradeNo("synthetic-trade").SetStatus(state).SetPaidAt(time.Now()).SetRefundAmount(o.Amount).Exec(ctx))
				s.loadBalancer = &captureLoadBalancer{}
				restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{refundResponse: &payment.RefundResponse{RefundID: "synthetic-refund-id", Status: outcome}})
				defer restore()
				result, err := s.QueryAndFinalizeRefund(ctx, o.ID)
				require.NoError(t, err)
				require.Equal(t, outcome == payment.ProviderStatusSuccess, result.Success)
				got, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
				require.NoError(t, err)
				require.Equal(t, map[string]string{payment.ProviderStatusSuccess: OrderStatusRefunded, payment.ProviderStatusFailed: OrderStatusRefundFailed, payment.ProviderStatusPending: OrderStatusRefundPending}[outcome], got.Status)
				changes, err := s.entClient.UserSubscription.Query().Count(ctx)
				require.NoError(t, err)
				require.Zero(t, changes)
			})
		}
	}
}

func TestSubscriptionV2NotificationAmountAndProviderMatrix(t *testing.T) {
	for _, kind := range []string{"zero", "negative", "nan", "infinity", "underpay", "overpay", "wrong-provider"} {
		t.Run(kind, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			amount, provider := o.PayAmount, payment.TypeAlipay
			switch kind {
			case "zero":
				amount = 0
			case "negative":
				amount = -1
			case "nan":
				amount = math.NaN()
			case "infinity":
				amount = math.Inf(1)
			case "underpay":
				amount -= 1
			case "overpay":
				amount += 1
			case "wrong-provider":
				provider = payment.TypeWxpay
			}
			err = s.HandlePaymentNotification(context.Background(), &payment.PaymentNotification{OrderID: o.OutTradeNo, TradeNo: "synthetic-notify", Status: payment.NotificationStatusSuccess, Amount: amount}, provider)
			require.Error(t, err)
			unchanged, err := s.entClient.PaymentOrder.Get(context.Background(), o.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPending, unchanged.Status)
			require.Nil(t, unchanged.PaidAt)
			count, err := s.entClient.UserSubscription.Query().Count(context.Background())
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestSubscriptionV2QuoteChangedByEachAdministrativeState(t *testing.T) {
	for _, kind := range []string{"suspended", "revoked", "expired", "deleted", "expiry-change", "quota-change", "quantity-change", "plan-change", "term-change", "source-price-change", "target-archive", "target-unsale"} {
		t.Run(kind, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			c := v2Fulfill(t, s, o)
			q = v2Quote(t, s, u.ID, p[4], "upgrade", 0, 0)
			ctx := context.Background()
			switch kind {
			case "suspended", "revoked", "expired":
				require.NoError(t, s.entClient.UserSubscription.UpdateOneID(c.SubscriptionID).SetStatus(kind).Exec(ctx))
			case "deleted":
				require.NoError(t, s.entClient.UserSubscription.UpdateOneID(c.SubscriptionID).SetDeletedAt(time.Now()).Exec(ctx))
			case "expiry-change":
				_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_contracts SET expires_at=$2 WHERE subscription_id=$1`, c.SubscriptionID, c.ExpiresAt.Add(time.Hour))
			case "quota-change":
				_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_contracts SET unit_daily_usd=90 WHERE subscription_id=$1`, c.SubscriptionID)
			case "quantity-change":
				_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_contracts SET quantity=quantity+1 WHERE subscription_id=$1`, c.SubscriptionID)
			case "plan-change":
				_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_contracts SET plan_id=$2 WHERE subscription_id=$1`, c.SubscriptionID, p[3].ID)
			case "term-change":
				_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_contracts SET revision=revision+1 WHERE subscription_id=$1`, c.SubscriptionID)
			case "source-price-change":
				require.NoError(t, s.entClient.SubscriptionPlan.UpdateOneID(p[2].ID).SetPrice(16).Exec(ctx))
			case "target-archive":
				require.NoError(t, s.entClient.SubscriptionPlan.UpdateOneID(p[4].ID).SetArchivedAt(time.Now()).Exec(ctx))
			case "target-unsale":
				require.NoError(t, s.entClient.SubscriptionPlan.UpdateOneID(p[4].ID).SetForSale(false).Exec(ctx))
			}
			require.NoError(t, err)
			_, err = v2Create(t, s, u, q)
			require.Equal(t, "SUBSCRIPTION_QUOTE_CHANGED", infraerrors.Reason(err))
		})
	}
}

func TestSubscriptionV2LegacyPaidSnapshotKeepsPromiseAndMovesToCompatibility(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	s.subscriptionSvc = &SubscriptionService{entClient: s.entClient, userSubRepo: v2SubscriptionReadRepo{c: s.entClient}, now: time.Now}
	q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
	o, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	before := v2Fulfill(t, s, o)
	ctx := context.Background()
	snapshot := map[string]any{"version": 1, "daily_limit_usd": 45, "validity_days": 30, "mode": "stack", "quantity": 1}
	old, err := s.entClient.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName("").SetAmount(15).SetPayAmount(15).SetRechargeCode("synthetic-v1-paid").SetOutTradeNo("synthetic-v1-paid").SetPaymentType("alipay").SetPaymentTradeNo("v1trade").SetOrderType("subscription").SetPlanID(p[2].ID).SetSubscriptionDays(30).SetSubscriptionMode("stack").SetSubscriptionQuantity(1).SetSubscriptionSnapshot(snapshot).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("test.invalid").Save(ctx)
	require.NoError(t, err)
	require.NoError(t, s.entClient.SubscriptionPlan.UpdateOneID(p[2].ID).SetPrice(99).SetDailyLimitUsd(180).Exec(ctx))
	require.NoError(t, s.ensurePaymentSubscriptionAssigned(ctx, old, 0, 30))
	require.NoError(t, s.ensurePaymentSubscriptionAssigned(ctx, old, 0, 30))
	lots, err := geilisub.ReadLots(ctx, s.entClient, before.SubscriptionID)
	require.NoError(t, err)
	require.Len(t, lots, 2)
	for _, lot := range lots {
		require.Equal(t, 45.0, *lot.DailyLimitUSD)
	}
	contract, err := geilisub.LoadContract(ctx, s.entClient, before.SubscriptionID)
	require.NoError(t, err)
	require.Equal(t, geilisub.ContractModeLegacy, contract.Mode)
	require.Equal(t, before.TermID, contract.TermID)
	_, err = s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, PlanID: p[4].ID, Operation: "upgrade"})
	require.Equal(t, "SUBSCRIPTION_COMPATIBILITY_MODE", infraerrors.Reason(err))
}

type v2FailGrantReadRepo struct{ v2SubscriptionReadRepo }

func (r v2FailGrantReadRepo) GetByID(context.Context, int64) (*UserSubscription, error) {
	return nil, errors.New("injected failure after contract grant")
}
func TestSubscriptionV2RedeemSuccessAndPostGrantRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success-and-replay", true: "rollback-after-grant"}[fail], func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			before := v2Fulfill(t, s, o)
			repo := v2SubscriptionReadRepo{c: s.entClient}
			subsvc := &SubscriptionService{entClient: s.entClient, userSubRepo: repo, now: time.Now}
			if fail {
				subsvc.userSubRepo = v2FailGrantReadRepo{repo}
			}
			row, err := s.entClient.RedeemCode.Create().SetCode("synthetic-valid-grant").SetType(RedeemTypeSubscription).SetStatus(StatusUnused).SetPlanID(p[2].ID).SetValidityDays(30).Save(context.Background())
			require.NoError(t, err)
			redeemer := NewRedeemService(&v2RedeemTransactionRepo{c: s.entClient}, &mockUserRepo{getByIDUser: &User{ID: u.ID}}, subsvc, nil, nil, s.entClient, nil, nil)
			_, err = redeemer.Redeem(context.Background(), u.ID, row.Code)
			if fail {
				require.ErrorContains(t, err, "injected failure after contract grant")
			} else {
				require.NoError(t, err)
			}
			code, err := s.entClient.RedeemCode.Get(context.Background(), row.ID)
			require.NoError(t, err)
			contract, err := geilisub.LoadContract(context.Background(), s.entClient, before.SubscriptionID)
			require.NoError(t, err)
			if fail {
				require.Equal(t, StatusUnused, code.Status)
				require.Nil(t, code.UsedBy)
				require.Equal(t, before.Revision, contract.Revision)
				require.True(t, before.ExpiresAt.Equal(contract.ExpiresAt))
			} else {
				require.Equal(t, StatusUsed, code.Status)
				require.Equal(t, u.ID, *code.UsedBy)
				require.WithinDuration(t, before.ExpiresAt.Add(30*24*time.Hour), contract.ExpiresAt, time.Microsecond)
				_, err = redeemer.Redeem(context.Background(), u.ID, row.Code)
				require.ErrorIs(t, err, ErrRedeemCodeUsed)
				after, err := geilisub.LoadContract(context.Background(), s.entClient, before.SubscriptionID)
				require.NoError(t, err)
				require.Equal(t, contract.Revision, after.Revision)
			}
		})
	}
}

func TestSubscriptionV2RefundCommitFailureCanRetryWithoutDoubleReversal(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
	o, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	before := v2Fulfill(t, s, o)
	ctx := context.Background()
	q = v2Quote(t, s, u.ID, p[4], "upgrade", 0, 0)
	o, err = v2Create(t, s, u, q)
	require.NoError(t, err)
	after := v2Fulfill(t, s, o)
	o, err = s.entClient.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, err)
	tx, err := s.entClient.Tx(ctx)
	require.NoError(t, err)
	_, err = geilisub.FreezeContractRefund(ctx, tx.Client(), o.ID, time.Now())
	require.NoError(t, err)
	require.NoError(t, tx.SubscriptionRefund.Create().SetOrderID(o.ID).SetSubscriptionID(before.SubscriptionID).SetSnapshot(json.RawMessage(`{"version":2}`)).Exec(ctx))
	require.NoError(t, tx.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefunding).SetRefundAmount(o.Amount).Exec(ctx))
	require.NoError(t, tx.Commit())
	_, err = s.entClient.ExecContext(ctx, `CREATE TRIGGER reject_v2_refund_audit BEFORE INSERT ON payment_audit_logs WHEN NEW.action='REFUND_SUCCESS' BEGIN SELECT RAISE(FAIL,'injected refund audit failure'); END`)
	require.NoError(t, err)
	plan := &RefundPlan{Order: o, OrderID: o.ID, RefundAmount: o.Amount, SubscriptionV2: true, SubscriptionID: before.SubscriptionID}
	_, err = s.finishSubscriptionV2Refund(ctx, plan, &payment.RefundResponse{Status: payment.ProviderStatusSuccess}, nil)
	require.ErrorContains(t, err, "injected refund audit failure")
	still, err := geilisub.LoadContract(ctx, s.entClient, before.SubscriptionID)
	require.NoError(t, err)
	require.Equal(t, after.Revision, still.Revision)
	require.Equal(t, 180.0, still.UnitDailyUSD)
	require.Equal(t, "suspended", still.Status)
	order, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefunding, order.Status)
	_, err = s.entClient.ExecContext(ctx, `DROP TRIGGER reject_v2_refund_audit`)
	require.NoError(t, err)
	result, err := s.finishSubscriptionV2Refund(ctx, plan, &payment.RefundResponse{Status: payment.ProviderStatusSuccess}, nil)
	require.NoError(t, err)
	require.True(t, result.Success)
	got, err := geilisub.LoadContract(ctx, s.entClient, before.SubscriptionID)
	require.NoError(t, err)
	require.Equal(t, 45.0, got.UnitDailyUSD)
	require.Equal(t, after.Revision+1, got.Revision)
}

func TestSubscriptionV2AssignmentAuditFailureRollsBackGrant(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, p[2], "purchase", 2, 0)
	o, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	ctx := context.Background()
	o, err = s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	_, err = s.entClient.ExecContext(ctx, `CREATE TRIGGER reject_v2_assignment BEFORE INSERT ON payment_audit_logs WHEN NEW.action='SUBSCRIPTION_ASSIGNED' BEGIN SELECT RAISE(FAIL,'injected assignment failure'); END`)
	require.NoError(t, err)
	err = s.ensureSubscriptionV2Assigned(ctx, o)
	require.ErrorContains(t, err, "injected assignment failure")
	n, err := s.entClient.UserSubscription.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	_, err = s.entClient.ExecContext(ctx, `DROP TRIGGER reject_v2_assignment`)
	require.NoError(t, err)
	require.NoError(t, s.ensureSubscriptionV2Assigned(ctx, o))
	n, err = s.entClient.UserSubscriptionEntitlement.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestSubscriptionV2RepurchaseDoesNotReuseRevokedOrSuspendedParent(t *testing.T) {
	for _, state := range []string{"revoked", "suspended"} {
		t.Run(state, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			old := v2Fulfill(t, s, o)
			ctx := context.Background()
			b := s.entClient.UserSubscription.UpdateOneID(old.SubscriptionID).SetStatus(state)
			if state == "suspended" {
				b.SetExpiresAt(time.Now().Add(-time.Hour))
				_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_contracts SET expires_at=$2 WHERE subscription_id=$1`, old.SubscriptionID, time.Now().Add(-time.Hour))
				require.NoError(t, err)
			}
			require.NoError(t, b.Exec(ctx))
			q = v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err = v2Create(t, s, u, q)
			require.NoError(t, err)
			fresh := v2Fulfill(t, s, o)
			require.NotEqual(t, old.SubscriptionID, fresh.SubscriptionID)
			require.NotEqual(t, old.TermID, fresh.TermID)
			prior, err := s.entClient.UserSubscription.Get(ctx, old.SubscriptionID)
			require.NoError(t, err)
			require.Equal(t, state, prior.Status)
		})
	}
}

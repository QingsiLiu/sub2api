//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionrefund"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func v2PaymentFixture(t *testing.T) (*PaymentService, *dbent.User, []*dbent.SubscriptionPlan) {
	t.Helper()
	return v2PaymentFixtureWithClient(t, newPaymentConfigServiceTestClient(t))
}
func v2PaymentFixtureWithClient(t *testing.T, c *dbent.Client) (*PaymentService, *dbent.User, []*dbent.SubscriptionPlan) {
	t.Helper()
	ctx := context.Background()
	owner, err := c.User.Create().SetEmail("v2-" + uuid.NewString() + "@example.invalid").SetPasswordHash("synthetic").Save(ctx)
	require.NoError(t, err)
	var plans []*dbent.SubscriptionPlan
	for i, v := range []struct {
		days         int
		daily, price float64
	}{{7, 90, 7}, {7, 180, 14}, {30, 45, 15}, {30, 90, 30}, {30, 180, 60}} {
		p, err := c.SubscriptionPlan.Create().SetName([]string{"week90", "week180", "month45", "month90", "month180"}[i]).SetPrice(v.price).SetDailyLimitUsd(v.daily).SetValidityDays(v.days).SetValidityUnit("day").SetForSale(true).Save(ctx)
		require.NoError(t, err)
		plans = append(plans, p)
	}
	return &PaymentService{entClient: c, configService: &PaymentConfigService{entClient: c}, resumeService: NewPaymentResumeService([]byte("synthetic-v2-quote-signing-key")), subscriptionSvc: &SubscriptionService{}}, owner, plans
}
func v2Quote(t *testing.T, s *PaymentService, uid int64, p *dbent.SubscriptionPlan, op string, units, periods int) *SubscriptionQuoteResponse {
	t.Helper()
	q, err := s.QuoteSubscription(context.Background(), SubscriptionQuoteRequest{UserID: uid, PlanID: p.ID, Operation: op, Units: units, Periods: periods})
	require.NoError(t, err)
	return q
}
func v2Create(t *testing.T, s *PaymentService, u *dbent.User, q *SubscriptionQuoteResponse) (*dbent.PaymentOrder, error) {
	t.Helper()
	ctx := context.Background()
	req, err := s.prepareSubscriptionV2Order(CreateOrderRequest{UserID: u.ID, QuoteID: q.QuoteID, PaymentType: "alipay", OrderType: payment.OrderTypeSubscription})
	if err != nil {
		return nil, err
	}
	plan, err := s.entClient.SubscriptionPlan.Get(ctx, req.PlanID)
	if err != nil {
		return nil, err
	}
	return s.createOrderInTx(ctx, req, &User{ID: u.ID, Email: u.Email, Username: u.Username}, plan, &PaymentConfig{MaxPendingOrders: 10, OrderTimeoutMin: 15}, q.OrderAmount, q.OrderAmount, 0, q.OrderAmount, nil)
}
func v2Fulfill(t *testing.T, s *PaymentService, o *dbent.PaymentOrder) *geilisub.Contract {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Exec(ctx))
	o, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, err)
	require.NoError(t, s.ensureSubscriptionV2Assigned(ctx, o))
	require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusCompleted).Exec(ctx))
	tx, err := s.entClient.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	c, err := geilisub.CurrentContract(ctx, tx.Client(), o.UserID, time.Now())
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return c
}
func TestSubscriptionV2PaymentQuoteIntegrityAndSinglePending(t *testing.T) {
	s, u, plans := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, plans[2], "purchase", 2, 0)
	require.Equal(t, 30.0, q.OrderAmount)
	_, err := s.readSubscriptionV2Quote(q.QuoteID, u.ID+1, time.Now())
	require.Error(t, err)
	_, err = s.readSubscriptionV2Quote(q.QuoteID+"bad", u.ID, time.Now())
	require.Error(t, err)
	_, err = s.readSubscriptionV2Quote(q.QuoteID, u.ID, q.ExpiresAt)
	require.Equal(t, "SUBSCRIPTION_QUOTE_EXPIRED", infraerrors.Reason(err))
	order, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	require.True(t, isSubscriptionV2Order(order))
	require.Equal(t, 30.0, order.Amount)
	_, err = v2Create(t, s, u, q)
	require.Equal(t, "SUBSCRIPTION_ORDER_PENDING", infraerrors.Reason(err))
	before := v2Fulfill(t, s, order)
	require.Equal(t, 2, before.Quantity)
	require.NoError(t, s.ensureSubscriptionV2Assigned(context.Background(), order))
	lots, err := geilisub.ReadLots(context.Background(), s.entClient, before.SubscriptionID)
	require.NoError(t, err)
	require.Len(t, lots, 2)
}
func TestSubscriptionV2PaymentPreservesUsageAndStableID(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, p[2], "purchase", 2, 0)
	o, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	before := v2Fulfill(t, s, o)
	_, err = s.entClient.ExecContext(context.Background(), `UPDATE subscription_daily_usage SET used_usd=87.04857312 WHERE subscription_id=$1`, before.SubscriptionID)
	require.NoError(t, err)
	stack := v2Quote(t, s, u.ID, p[2], "stack", 1, 0)
	require.InDelta(t, 87.04857312, stack.Current.DailyUsageUSD, 1e-9)
	require.InDelta(t, 47.95142688, *stack.Projected.RemainingUSD, 1e-9)
	o, err = v2Create(t, s, u, stack)
	require.NoError(t, err)
	stacked := v2Fulfill(t, s, o)
	require.Equal(t, before.SubscriptionID, stacked.SubscriptionID)
	require.True(t, before.ExpiresAt.Equal(stacked.ExpiresAt))
	require.Equal(t, 3, stacked.Quantity)
	renew := v2Quote(t, s, u.ID, p[2], "renew", 0, 2)
	require.Equal(t, 90.0, renew.OrderAmount)
	o, err = v2Create(t, s, u, renew)
	require.NoError(t, err)
	renewed := v2Fulfill(t, s, o)
	require.WithinDuration(t, before.ExpiresAt.Add(60*24*time.Hour), renewed.ExpiresAt, time.Microsecond)
	upgrade := v2Quote(t, s, u.ID, p[4], "upgrade", 0, 0)
	require.Equal(t, 405.0, upgrade.OrderAmount)
	require.Equal(t, 90, upgrade.BillableDays)
	require.InDelta(t, 87.04857312, upgrade.Projected.DailyUsageUSD, 1e-9)
	o, err = v2Create(t, s, u, upgrade)
	require.NoError(t, err)
	upgraded := v2Fulfill(t, s, o)
	require.Equal(t, before.SubscriptionID, upgraded.SubscriptionID)
	require.Equal(t, before.TermID, upgraded.TermID)
	require.Equal(t, 180.0, upgraded.UnitDailyUSD)
	_, err = s.QuoteSubscription(context.Background(), SubscriptionQuoteRequest{UserID: u.ID, PlanID: p[1].ID, Operation: "upgrade"})
	require.Equal(t, "SUBSCRIPTION_TYPE_MISMATCH", infraerrors.Reason(err))
}
func TestSubscriptionV2QuoteRejectsPlanAndStateChanges(t *testing.T) {
	for _, kind := range []string{"price", "suspend"} {
		t.Run(kind, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			c := v2Fulfill(t, s, o)
			q = v2Quote(t, s, u.ID, p[2], "stack", 1, 0)
			if kind == "price" {
				require.NoError(t, s.entClient.SubscriptionPlan.UpdateOneID(p[2].ID).SetPrice(20).Exec(context.Background()))
			} else {
				require.NoError(t, s.entClient.UserSubscription.UpdateOneID(c.SubscriptionID).SetStatus("suspended").Exec(context.Background()))
			}
			_, err = v2Create(t, s, u, q)
			require.Equal(t, "SUBSCRIPTION_QUOTE_CHANGED", infraerrors.Reason(err))
		})
	}
}
func TestSubscriptionV2ProratesAndLocksPaymentPromise(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
	o, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	c := v2Fulfill(t, s, o)
	expiry := time.Now().Add(49 * time.Hour).Truncate(time.Microsecond)
	ctx := context.Background()
	_, err = s.entClient.ExecContext(ctx, `UPDATE subscription_contracts SET expires_at=$2 WHERE subscription_id=$1`, c.SubscriptionID, expiry)
	require.NoError(t, err)
	require.NoError(t, s.entClient.UserSubscription.UpdateOneID(c.SubscriptionID).SetExpiresAt(expiry).Exec(ctx))
	q = v2Quote(t, s, u.ID, p[2], "stack", 1, 0)
	require.Equal(t, 3, q.BillableDays)
	require.Equal(t, 1.5, q.OrderAmount)
	require.True(t, q.ProjectedContract.ExpiresAt.Equal(expiry))
	o, err = v2Create(t, s, u, q)
	require.NoError(t, err)
	require.NoError(t, s.entClient.SubscriptionPlan.UpdateOneID(p[2].ID).SetPrice(99).SetDailyLimitUsd(180).Exec(ctx))
	after := v2Fulfill(t, s, o)
	require.Equal(t, 45.0, after.UnitDailyUSD)
	require.Equal(t, 2, after.Quantity)
}
func TestSubscriptionV2RefundExactSnapshotAndGatewayRecovery(t *testing.T) {
	for _, outcome := range []string{payment.ProviderStatusSuccess, payment.ProviderStatusFailed, payment.ProviderStatusPending} {
		t.Run(outcome, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			before := v2Fulfill(t, s, o)
			q = v2Quote(t, s, u.ID, p[4], "upgrade", 0, 0)
			o, err = v2Create(t, s, u, q)
			require.NoError(t, err)
			after := v2Fulfill(t, s, o)
			ctx := context.Background()
			o, err = s.entClient.PaymentOrder.Get(ctx, o.ID)
			require.NoError(t, err)
			tx, err := s.entClient.Tx(ctx)
			require.NoError(t, err)
			_, err = geilisub.FreezeContractRefund(ctx, tx.Client(), o.ID, time.Now())
			require.NoError(t, err)
			raw, _ := json.Marshal(map[string]any{"version": 2})
			require.NoError(t, tx.SubscriptionRefund.Create().SetOrderID(o.ID).SetSubscriptionID(before.SubscriptionID).SetSnapshot(raw).Exec(ctx))
			require.NoError(t, tx.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusRefunding).Exec(ctx))
			require.NoError(t, tx.Commit())
			plan := &RefundPlan{OrderID: o.ID, Order: o, RefundAmount: o.Amount, GatewayAmount: o.PayAmount, SubscriptionV2: true, SubscriptionID: before.SubscriptionID, Reason: "synthetic"}
			result, err := s.finishRefund(ctx, plan, &payment.RefundResponse{Status: outcome})
			require.NoError(t, err)
			got, err := geilisub.LoadContract(ctx, s.entClient, before.SubscriptionID)
			require.NoError(t, err)
			switch outcome {
			case payment.ProviderStatusSuccess:
				require.True(t, result.Success)
				require.Equal(t, 45.0, got.UnitDailyUSD)
				require.Greater(t, got.Revision, after.Revision)
				require.Equal(t, before.TermID, got.TermID)
			case payment.ProviderStatusFailed:
				require.Equal(t, "active", got.Status)
				require.Equal(t, 180.0, got.UnitDailyUSD)
			case payment.ProviderStatusPending:
				require.Equal(t, "suspended", got.Status)
				journal, err := s.entClient.SubscriptionRefund.Query().Where(subscriptionrefund.OrderIDEQ(o.ID)).Only(ctx)
				require.NoError(t, err)
				require.Equal(t, "pending", journal.Status)
			}
		})
	}
}
func TestSubscriptionV2PaidConflictDoesNotGrant(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
	o, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	before := v2Fulfill(t, s, o)
	q = v2Quote(t, s, u.ID, p[4], "upgrade", 0, 0)
	o, err = v2Create(t, s, u, q)
	require.NoError(t, err)
	_, err = s.entClient.ExecContext(context.Background(), `UPDATE subscription_contracts SET revision=revision+1 WHERE subscription_id=$1`, before.SubscriptionID)
	require.NoError(t, err)
	require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Exec(context.Background()))
	err = s.ExecuteSubscriptionFulfillment(context.Background(), o.ID)
	require.Error(t, err)
	order, err := s.entClient.PaymentOrder.Get(context.Background(), o.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusFailed, order.Status)
	require.NotNil(t, order.PaidAt)
	require.True(t, strings.HasPrefix(*order.FailedReason, "SUBSCRIPTION_PAID_REVIEW_REQUIRED"))
	got, err := geilisub.LoadContract(context.Background(), s.entClient, before.SubscriptionID)
	require.NoError(t, err)
	require.Equal(t, 45.0, got.UnitDailyUSD)
}

type v2SubscriptionReadRepo struct {
	userSubRepoNoop
	c *dbent.Client
}

func (r v2SubscriptionReadRepo) GetByID(ctx context.Context, id int64) (*UserSubscription, error) {
	c := r.c
	if tx := dbent.TxFromContext(ctx); tx != nil {
		c = tx.Client()
	}
	row, err := c.UserSubscription.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	lots, err := geilisub.ReadLots(ctx, c, id)
	if err != nil {
		return nil, err
	}
	return &UserSubscription{ID: row.ID, UserID: row.UserID, PlanID: row.PlanID, StartsAt: row.StartsAt, ExpiresAt: row.ExpiresAt, Status: row.Status, Notes: psStringValue(row.Notes), Entitlements: lots}, nil
}
func TestSubscriptionV2AdminAndHistoricalGrantSafety(t *testing.T) {
	s, u, p := v2PaymentFixture(t)
	s.subscriptionSvc = &SubscriptionService{entClient: s.entClient, userSubRepo: v2SubscriptionReadRepo{c: s.entClient}, now: time.Now}
	q := v2Quote(t, s, u.ID, p[2], "purchase", 2, 0)
	o, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	before := v2Fulfill(t, s, o)
	ctx := context.Background()
	_, _, err = s.subscriptionSvc.assignPlanSubscription(ctx, &AssignSubscriptionInput{UserID: u.ID, PlanID: &p[2].ID, ValidityDays: 30, SourceType: "redeem"}, true, false)
	require.Equal(t, "SUBSCRIPTION_GRANT_MANUAL_REVIEW", infraerrors.Reason(err))
	granted, reused, err := s.subscriptionSvc.assignPlanSubscription(ctx, &AssignSubscriptionInput{UserID: u.ID, PlanID: &p[2].ID, ValidityDays: 60, SourceType: "admin"}, true, false)
	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, before.SubscriptionID, granted.ID)
	require.WithinDuration(t, before.ExpiresAt.Add(60*24*time.Hour), granted.ExpiresAt, time.Microsecond)
	current, err := geilisub.LoadContract(ctx, s.entClient, before.SubscriptionID)
	require.NoError(t, err)
	require.Equal(t, 2, current.Quantity)
	_, _, err = s.subscriptionSvc.assignPlanSubscription(ctx, &AssignSubscriptionInput{UserID: u.ID, PlanID: &p[0].ID, ValidityDays: 7}, true, false)
	require.Error(t, err)
	_, _, err = s.subscriptionSvc.assignLegacyEntitled(ctx, &AssignSubscriptionInput{UserID: u.ID, GroupID: 999, ValidityDays: 7}, true, false)
	require.Equal(t, "SUBSCRIPTION_GRANT_MANUAL_REVIEW", infraerrors.Reason(err))
}
func TestSubscriptionV2UnsafeAndHistoricalRefunds(t *testing.T) {
	for _, kind := range []string{"usage", "inflight", "later", "v1"} {
		t.Run(kind, func(t *testing.T) {
			s, u, p := v2PaymentFixture(t)
			q := v2Quote(t, s, u.ID, p[2], "purchase", 1, 0)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			contract := v2Fulfill(t, s, o)
			ctx := context.Background()
			lots, err := geilisub.ReadLots(ctx, s.entClient, contract.SubscriptionID)
			require.NoError(t, err)
			switch kind {
			case "usage":
				require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(lots[0].ID).SetLifetimeUsageUsd(.1).Exec(ctx))
			case "inflight":
				require.NoError(t, s.entClient.SubscriptionRequest.Create().SetRequestKey("pending-v2").SetSubscriptionID(contract.SubscriptionID).SetAPIKeyID(1).SetLots(json.RawMessage("[]")).SetAdmittedAt(time.Now()).Exec(ctx))
			case "later":
				q = v2Quote(t, s, u.ID, p[4], "upgrade", 0, 0)
				later, err := v2Create(t, s, u, q)
				require.NoError(t, err)
				v2Fulfill(t, s, later)
			case "v1":
				require.NoError(t, s.entClient.SubscriptionEntitlementOrder.Create().SetEntitlementID(lots[0].ID).SetOrderID(o.ID).SetOperation("create").SetAfterExpiresAt(lots[0].ExpiresAt).SetDaysAdded(30).Exec(ctx))
				_, err := validateLotRefund(ctx, s.entClient, o, o.Amount, time.Now())
				require.Equal(t, "SUBSCRIPTION_REFUND_MANUAL_REVIEW", infraerrors.Reason(err))
				return
			}
			tx, err := s.entClient.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = geilisub.ValidateContractRefund(ctx, tx.Client(), o.ID, time.Now())
			require.Equal(t, "SUBSCRIPTION_REFUND_MANUAL_REVIEW", infraerrors.Reason(err))
		})
	}
}

func TestSubscriptionV2UnassignedRefundFailureKeepsPaidReviewState(t *testing.T) {
	s, u, plans := v2PaymentFixture(t)
	q := v2Quote(t, s, u.ID, plans[2], "purchase", 1, 0)
	order, err := v2Create(t, s, u, q)
	require.NoError(t, err)
	ctx := context.Background()
	order, err = s.entClient.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusFailed).SetPaidAt(time.Now()).SetFailedReason("SUBSCRIPTION_PAID_REVIEW_REQUIRED: synthetic conflict").Save(ctx)
	require.NoError(t, err)
	plan, _, err := s.prepareSubscriptionV2Refund(ctx, &RefundPlan{Order: order, OrderID: order.ID, RefundAmount: order.Amount})
	require.NoError(t, err)
	require.True(t, plan.SubscriptionUnassigned)
	require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Exec(ctx))
	s.restoreStatus(ctx, plan)
	got, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusFailed, got.Status)
	require.NotNil(t, got.PaidAt)
}

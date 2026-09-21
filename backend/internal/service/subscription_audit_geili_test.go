//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

// Regression assertions for the 2026-09-18 entitlement audit.
func auditLotFixture(t *testing.T) (*dbent.Client, *dbent.UserSubscriptionEntitlement, time.Time) {
	t.Helper()
	ctx := context.Background()
	c := newPaymentConfigServiceTestClient(t)
	u, err := c.User.Create().SetEmail("audit@example.invalid").SetPasswordHash("synthetic-fixture").Save(ctx)
	require.NoError(t, err)
	original := time.Now().AddDate(0, 0, 3).Truncate(time.Microsecond)
	s, err := c.UserSubscription.Create().SetUserID(u.ID).SetStartsAt(time.Now().AddDate(0, 0, -27)).SetExpiresAt(original.AddDate(0, 0, 30)).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	lot, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(s.ID).SetStartsAt(s.StartsAt).SetExpiresAt(s.ExpiresAt).SetDailyLimitUsd(45).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	return c, lot, original
}

func TestSubscriptionAuditRenewRefundPreservesOriginalPurchase(t *testing.T) {
	c, lot, original := auditLotFixture(t)
	err := applySubscriptionLotRefundWithClient(context.Background(), c, &RefundPlan{SubscriptionLots: []SubscriptionLotAdjustment{{ID: lot.ID, Operation: "renew", Days: 30}}})
	require.NoError(t, err)
	got, err := c.UserSubscriptionEntitlement.Get(context.Background(), lot.ID)
	require.NoError(t, err)
	require.Equal(t, "active", got.Status, "refunding the extension must keep the original purchase active")
	require.WithinDuration(t, original, got.ExpiresAt, time.Millisecond)
}

func TestSubscriptionAuditQuoteHasNextExpiry(t *testing.T) {
	c, _, _ := auditLotFixture(t)
	plan, err := c.SubscriptionPlan.Create().SetName("audit plan").SetPrice(10).SetValidityDays(30).SetValidityUnit("day").SetForSale(true).SetDailyLimitUsd(45).Save(context.Background())
	require.NoError(t, err)
	svc := &PaymentService{entClient: c, configService: &PaymentConfigService{entClient: c}, resumeService: NewPaymentResumeService([]byte("synthetic-quote-signing-key"))}
	// Use a new owner: an existing historical lot must remain compatibility-only.
	newOwner, err := c.User.Create().SetEmail("quote@example.invalid").SetPasswordHash("test").Save(context.Background())
	require.NoError(t, err)
	got, err := svc.QuoteSubscription(context.Background(), SubscriptionQuoteRequest{UserID: newOwner.ID, PlanID: plan.ID, Operation: "purchase", Units: 2})
	require.NoError(t, err)
	require.NotNil(t, got.Projected.NextExpiryAt, "first purchase also has a next quota change")
}

func TestSubscriptionAuditQuoteRejectsNegativeQuantity(t *testing.T) {
	c, _, _ := auditLotFixture(t)
	plan, err := c.SubscriptionPlan.Create().SetName("audit plan").SetPrice(10).SetValidityDays(30).SetValidityUnit("day").SetForSale(true).Save(context.Background())
	require.NoError(t, err)
	svc := &PaymentService{configService: &PaymentConfigService{entClient: c}}
	_, err = svc.QuoteSubscription(context.Background(), SubscriptionQuoteRequest{UserID: 1, PlanID: plan.ID, Operation: "purchase", Units: -2})
	require.Error(t, err, "negative quantity must not silently become one")
}

func auditEligibility(t *testing.T, lots []SubscriptionEntitlement, usage float64) error {
	t.Helper()
	id := int64(1)
	sub := &UserSubscription{ID: 1, UserID: 10, PlanID: &id, Plan: &SubscriptionQuotaPlan{ID: 1}, Status: "active", StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().AddDate(0, 0, 30), Entitlements: lots, DailyUsageUSD: usage, WeeklyUsageUSD: usage, MonthlyUsageUSD: usage}
	svc := &BillingCacheService{subRepo: &resetQuotaUserSubRepoStub{sub: sub}}
	return svc.checkSubscriptionEligibility(context.Background(), 10, nil, sub)
}

func TestSubscriptionAuditExpiryDoesNotBlockRemainingUnusedLot(t *testing.T) {
	now := time.Now()
	limit := 45.0
	lots := []SubscriptionEntitlement{
		{ID: 1, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second), DailyLimitUSD: &limit, DailyUsageUSD: 45},
		{ID: 2, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 30), DailyLimitUSD: &limit},
	}
	require.Equal(t, 45.0, geilisub.Aggregate(lots, now).AvailableUSD)
	require.NoError(t, auditEligibility(t, lots, 45), "expired usage must not block the remaining unused quota")
}

func TestSubscriptionAuditExhaustedLotsCannotPoolIncompatibleDimensions(t *testing.T) {
	now := time.Now()
	limit := 10.0
	lots := []SubscriptionEntitlement{
		{ID: 1, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 1), DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, DailyUsageUSD: 10},
		{ID: 2, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 2), DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, WeeklyUsageUSD: 10},
	}
	require.Zero(t, geilisub.Aggregate(lots, now).AvailableUSD)
	require.Error(t, auditEligibility(t, lots, 10), "no individual lot has spendable quota; request must be refused")
}

func TestSubscriptionAuditRefundedLotsAreNotUnlimited(t *testing.T) {
	now := time.Now()
	limit := 45.0
	lots := []SubscriptionEntitlement{{ID: 1, Status: "refunded", StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 0, 30), DailyLimitUSD: &limit}}
	require.Zero(t, geilisub.Aggregate(lots, now).ActiveLotCount)
	require.Error(t, auditEligibility(t, lots, 0), "stale active aggregate must not authorize a subscription with no active lots")
}

func TestSubscriptionAuditLegacyGroupRenewalActuallyExtends(t *testing.T) {
	ctx := context.Background()
	c := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, c)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, c, OrderStatusPaid, time.Now())
	order, err := c.PaymentOrder.UpdateOneID(order.ID).ClearPlanID().Save(ctx)
	require.NoError(t, err)
	expiry := time.Now().AddDate(0, 0, 3)
	repo := &auditLegacySubscriptionRepo{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()}
	repo.seed(&UserSubscription{ID: 99, UserID: order.UserID, GroupID: *order.SubscriptionGroupID, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: expiry, Status: SubscriptionStatusActive})
	groups := &subscriptionGroupRepoStub{group: &Group{ID: *order.SubscriptionGroupID, Status: "active", SubscriptionType: SubscriptionTypeSubscription}}
	svc := &PaymentService{entClient: c, groupRepo: groups, subscriptionSvc: NewSubscriptionService(groups, repo, nil, nil, nil)}
	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	got, err := repo.GetByID(ctx, 99)
	require.NoError(t, err)
	require.WithinDuration(t, expiry.AddDate(0, 0, *order.SubscriptionDays), got.ExpiresAt, time.Millisecond, "completed legacy renewal must deliver the purchased days")
}

func auditRefundOrder(t *testing.T, c *dbent.Client, lot *dbent.UserSubscriptionEntitlement) *dbent.PaymentOrder {
	t.Helper()
	ctx := context.Background()
	sub, err := c.UserSubscription.Get(ctx, lot.UserSubscriptionID)
	require.NoError(t, err)
	o, err := c.PaymentOrder.Create().SetUserID(sub.UserID).SetUserEmail("audit@example.invalid").SetUserName("audit").SetAmount(10).SetPayAmount(10).SetRechargeCode("audit-refund").SetPaymentType("test").SetPaymentTradeNo("").SetClientIP("127.0.0.1").SetSrcHost("audit.invalid").SetOrderType("subscription").SetStatus(OrderStatusRefunding).SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx)
	require.NoError(t, err)
	return o
}

func TestSubscriptionAuditRefundPendingFreezesAffectedLot(t *testing.T) {
	c, lot, _ := auditLotFixture(t)
	ctx := context.Background()
	order := auditRefundOrder(t, c, lot)
	svc := &PaymentService{entClient: c}
	p := &RefundPlan{OrderID: order.ID, Order: order, RefundAmount: 10, DeductionType: payment.DeductionTypeSubscription, SubscriptionLots: []SubscriptionLotAdjustment{{ID: lot.ID, Operation: "revoke"}}}
	_, err := c.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).Save(ctx)
	require.NoError(t, err)
	_, err = c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetSourceType("payment").SetSourceOrderID(order.ID).Save(ctx)
	require.NoError(t, err)
	err = c.SubscriptionEntitlementOrder.Create().SetEntitlementID(lot.ID).SetOrderID(order.ID).SetOperation("create").SetAfterExpiresAt(lot.ExpiresAt).SetDaysAdded(30).Exec(ctx)
	require.NoError(t, err)
	p.SubscriptionID = lot.UserSubscriptionID
	require.NoError(t, svc.freezeLotRefund(ctx, p))
	result, err := svc.finishRefund(ctx, p, &payment.RefundResponse{Status: payment.ProviderStatusPending})
	require.NoError(t, err)
	require.False(t, result.Success)
	got, err := c.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.Equal(t, "refund_pending", got.Status, "pending refund must not leave quota spendable")
}

func TestSubscriptionAuditRefundFailureIsAtomicAcrossLots(t *testing.T) {
	c, lot, _ := auditLotFixture(t)
	ctx := context.Background()
	order := auditRefundOrder(t, c, lot)
	svc := &PaymentService{entClient: c}
	_, err := svc.finishRefund(ctx, &RefundPlan{OrderID: order.ID, Order: order, RefundAmount: 10, SubscriptionLots: []SubscriptionLotAdjustment{{ID: lot.ID, Operation: "revoke"}, {ID: lot.ID + 999, Operation: "revoke"}}}, &payment.RefundResponse{Status: payment.ProviderStatusSuccess})
	require.Error(t, err, "second missing lot simulates a mid-finalization failure")
	got, err := c.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.Equal(t, "active", got.Status, "first lot must roll back when a later lot update fails")
}

type auditLegacySubscriptionRepo struct{ *subscriptionUserSubRepoStub }

func (r *auditLegacySubscriptionRepo) ExtendExpiry(_ context.Context, id int64, expiry time.Time) error {
	r.byID[id].ExpiresAt = expiry
	return nil
}
func (r *auditLegacySubscriptionRepo) UpdateNotes(_ context.Context, id int64, notes string) error {
	r.byID[id].Notes = notes
	return nil
}
func (r *auditLegacySubscriptionRepo) UpdateStatus(_ context.Context, id int64, status string) error {
	r.byID[id].Status = status
	return nil
}

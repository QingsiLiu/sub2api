//go:build unit

package service

import (
	"context"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func legacyPaymentFixture(t *testing.T) (*PaymentService, *dbent.User, []*dbent.SubscriptionPlan, *dbent.UserSubscription, []int64) {
	s, u, plans := v2PaymentFixture(t)
	return legacyPaymentFixtureWithService(t, s, u, plans)
}
func legacyPaymentFixtureWithService(t *testing.T, s *PaymentService, u *dbent.User, plans []*dbent.SubscriptionPlan) (*PaymentService, *dbent.User, []*dbent.SubscriptionPlan, *dbent.UserSubscription, []int64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	_, err := s.entClient.Setting.Create().SetKey(SettingLegacySubscriptionManagement).SetValue("true").OnConflictColumns(setting.FieldKey).UpdateNewValues().ID(ctx)
	require.NoError(t, err)
	parent, err := s.entClient.UserSubscription.Create().SetUserID(u.ID).SetPlanID(plans[0].ID).SetStartsAt(now.Add(-6 * 24 * time.Hour)).SetExpiresAt(now.Add(2 * 24 * time.Hour)).Save(ctx)
	require.NoError(t, err)
	var ids []int64
	for i := 1; i <= 2; i++ {
		l, e := s.entClient.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetPlanID(plans[0].ID).SetSourceType("payment").SetStatus("active").SetStartsAt(parent.StartsAt).SetExpiresAt(now.Add(time.Duration(i) * 24 * time.Hour)).SetDailyLimitUsd(90).SetDailyWindowStart(geilisub.DayStart(now)).SetDailyUsageUsd(10).SetLifetimeUsageUsd(10).Save(ctx)
		require.NoError(t, e)
		ids = append(ids, l.ID)
	}
	_, err = geilisub.EnsureContract(ctx, s.entClient, parent.ID, now)
	require.NoError(t, err)
	return s, u, plans, parent, ids
}
func TestLegacyManagementPaymentFlow(t *testing.T) {
	for _, op := range []string{"renew", "upgrade", "purchase", "stack"} {
		t.Run(op, func(t *testing.T) {
			s, u, plans, parent, ids := legacyPaymentFixture(t)
			ctx := context.Background()
			req := SubscriptionQuoteRequest{UserID: u.ID, SubscriptionID: parent.ID, PlanID: plans[0].ID, Operation: op}
			switch op {
			case "renew":
				req.EntitlementIDs = ids[:1]
				req.Periods = 1
			case "upgrade":
				req.EntitlementIDs = ids[:1]
				req.PlanID = plans[1].ID
			case "purchase":
				req.Units = 2
			case "stack":
				req.Units = 1
				req.ExpiryAnchorEntitlementID = ids[1]
			}
			before, e := geilisub.ReadLots(ctx, s.entClient, parent.ID)
			require.NoError(t, e)
			q, e := s.QuoteSubscription(ctx, req)
			require.NoError(t, e)
			require.Equal(t, "legacy_lots", q.ManagementMode)
			signed, e := s.readSubscriptionV2Quote(q.QuoteID, u.ID, time.Now())
			require.NoError(t, e)
			require.Equal(t, 3, signed.Version)
			order, e := v2Create(t, s, u, q)
			require.NoError(t, e)
			require.True(t, isSubscriptionV2Order(order))
			// Closing the feature must never strand an accepted payment.
			_, e = s.entClient.Setting.Update().Where(setting.KeyEQ(SettingLegacySubscriptionManagement)).SetValue("false").Save(ctx)
			require.NoError(t, e)
			contract := v2Fulfill(t, s, order)
			require.Equal(t, parent.ID, contract.SubscriptionID)
			require.Equal(t, geilisub.ContractModeLegacy, contract.Mode)
			require.NoError(t, s.ensureSubscriptionV2Assigned(ctx, order))
			after, e := geilisub.ReadLots(ctx, s.entClient, parent.ID)
			require.NoError(t, e)
			for _, l := range after {
				if l.ID == ids[1] {
					require.Equal(t, before[1], l)
				}
				if l.ID == ids[0] {
					require.Equal(t, 10.0, l.DailyUsageUSD)
					if op == "renew" {
						require.Equal(t, before[0].ExpiresAt.Add(7*24*time.Hour), l.ExpiresAt)
					}
					if op == "upgrade" {
						require.Equal(t, 180.0, *l.DailyLimitUSD)
						require.Equal(t, before[0].ExpiresAt, l.ExpiresAt)
					}
				}
			}
			expected := 2
			if op == "purchase" {
				expected = 4
			}
			if op == "stack" {
				expected = 3
			}
			require.Len(t, after, expected)
			if op == "renew" || op == "upgrade" {
				_, e = geilisub.FreezeContractRefund(ctx, s.entClient, order.ID, time.Now())
				require.NoError(t, e)
				_, e = geilisub.RevertContractChange(ctx, s.entClient, order.ID, time.Now())
				require.NoError(t, e)
				restored, e := geilisub.ReadLots(ctx, s.entClient, parent.ID)
				require.NoError(t, e)
				require.Equal(t, before, restored)
			}
		})
	}
}
func TestLegacyManagementQuoteSafety(t *testing.T) {
	s, u, plans, parent, ids := legacyPaymentFixture(t)
	ctx := context.Background()
	req := SubscriptionQuoteRequest{UserID: u.ID, SubscriptionID: parent.ID, PlanID: plans[0].ID, Operation: "renew", EntitlementIDs: ids[:1], Periods: 1}
	q, e := s.QuoteSubscription(ctx, req)
	require.NoError(t, e)
	wrong := req
	wrong.UserID = u.ID + 123
	_, e = s.QuoteSubscription(ctx, wrong)
	require.Error(t, e)
	wrong = req
	wrong.EntitlementIDs = []int64{ids[0], ids[0]}
	_, e = s.QuoteSubscription(ctx, wrong)
	require.Error(t, e)
	wrong = req
	wrong.EntitlementIDs = []int64{999}
	_, e = s.QuoteSubscription(ctx, wrong)
	require.Error(t, e)
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(ids[0]).AddDailyUsageUsd(1).AddLifetimeUsageUsd(1).Exec(ctx))
	order, e := v2Create(t, s, u, q)
	require.NoError(t, e, "normal consumption does not invalidate quote")
	require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCancelled).Exec(ctx))
	q, e = s.QuoteSubscription(ctx, req)
	require.NoError(t, e)
	require.NoError(t, s.entClient.SubscriptionPlan.UpdateOneID(plans[0].ID).SetPrice(8).Exec(ctx))
	_, e = v2Create(t, s, u, q)
	require.ErrorIs(t, e, errSubscriptionQuoteChanged)
	_, e = s.entClient.Setting.Update().Where(setting.KeyEQ(SettingLegacySubscriptionManagement)).SetValue("false").Save(ctx)
	require.NoError(t, e)
	_, e = s.QuoteSubscription(ctx, req)
	require.ErrorIs(t, e, errLegacyManagementDisabled)
}
func TestLegacyManagementOptionsAndAmbiguity(t *testing.T) {
	s, u, plans, parent, ids := legacyPaymentFixture(t)
	ctx := context.Background()
	options, e := s.LegacySubscriptionOptions(ctx, u.ID)
	require.NoError(t, e)
	require.True(t, options.Enabled)
	require.Len(t, options.Pools, 1)
	require.Equal(t, parent.ID, options.Pools[0].SubscriptionID)
	require.Equal(t, plans[0].ID, options.Pools[0].Lots[0].PlanID)
	require.Contains(t, options.Pools[0].Lots[0].UpgradePlanIDs, plans[1].ID)
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(ids[0]).SetSourceType("campaign").Exec(ctx))
	options, e = s.LegacySubscriptionOptions(ctx, u.ID)
	require.NoError(t, e)
	require.Equal(t, "gift", options.Pools[0].Lots[0].Reason)
	require.Empty(t, options.Pools[0].Lots[1].Reason)
	_, e = s.entClient.SubscriptionPlan.Create().SetName("ambiguous week").SetPrice(7).SetDailyLimitUsd(90).SetValidityDays(7).SetValidityUnit("day").SetForSale(true).Save(ctx)
	require.NoError(t, e)
	options, e = s.LegacySubscriptionOptions(ctx, u.ID)
	require.NoError(t, e)
	require.Equal(t, "price_unavailable", options.Pools[0].Lots[1].Reason)
}
func TestLegacyManagementExpiredAfterPaymentNeverGrants(t *testing.T) {
	s, u, plans, parent, ids := legacyPaymentFixture(t)
	ctx := context.Background()
	q, e := s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, SubscriptionID: parent.ID, PlanID: plans[0].ID, Operation: "renew", EntitlementIDs: ids[:1], Periods: 1})
	require.NoError(t, e)
	order, e := v2Create(t, s, u, q)
	require.NoError(t, e)
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(ids[0]).SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
	require.NoError(t, s.entClient.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Exec(ctx))
	e = s.ensureSubscriptionV2Assigned(ctx, order)
	require.Error(t, e)
	require.True(t, subscriptionV2Conflict(e))
	assigned, e := hasPaymentSubscriptionAssignmentAudit(ctx, s.entClient, order.ID)
	require.NoError(t, e)
	require.False(t, assigned)
}

func TestLegacyManagementFreeAlignmentPreviewAndIdempotency(t *testing.T) {
	s, u, _, parent, ids := legacyPaymentFixture(t)
	ctx := context.Background()
	svc := &SubscriptionService{entClient: s.entClient}
	request := LegacyAlignmentRequest{UserID: u.ID, EntitlementIDs: ids, IdempotencyKey: "test-align-once", Reason: "customer support complimentary alignment"}
	before, e := geilisub.ReadLots(ctx, s.entClient, parent.ID)
	require.NoError(t, e)
	preview, e := svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.NoError(t, e)
	require.False(t, preview.Applied)
	require.True(t, preview.ExpiresAt.Equal(before[1].ExpiresAt))
	unchanged, e := geilisub.ReadLots(ctx, s.entClient, parent.ID)
	require.NoError(t, e)
	require.Equal(t, before, unchanged)
	request.Apply = true
	request.ExpectedSnapshot = "stale"
	_, e = svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.Error(t, e)
	request.ExpectedSnapshot = preview.Snapshot
	done, e := svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.NoError(t, e)
	require.True(t, done.Applied)
	done, e = svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.NoError(t, e)
	require.True(t, done.Applied)
	after, e := geilisub.ReadLots(ctx, s.entClient, parent.ID)
	require.NoError(t, e)
	for _, l := range after {
		require.True(t, l.ExpiresAt.Equal(preview.ExpiresAt))
		require.Equal(t, 10.0, l.DailyUsageUSD)
	}
	request.Reason = "different operation with reused key"
	_, e = svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.Error(t, e)
}

func TestLegacyManagementRefundKeepsLiveSiblingUsable(t *testing.T) {
	s, u, plans, parent, ids := legacyPaymentFixture(t)
	ctx := context.Background()
	q, e := s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, SubscriptionID: parent.ID, PlanID: plans[0].ID, Operation: "renew", EntitlementIDs: ids[:1], Periods: 1})
	require.NoError(t, e)
	order, e := v2Create(t, s, u, q)
	require.NoError(t, e)
	v2Fulfill(t, s, order)
	_, e = geilisub.FreezeContractRefund(ctx, s.entClient, order.ID, time.Now())
	require.NoError(t, e)
	lots, e := geilisub.ReadLots(ctx, s.entClient, parent.ID)
	require.NoError(t, e)
	c, e := geilisub.LoadContract(ctx, s.entClient, parent.ID)
	require.NoError(t, e)
	require.Equal(t, "active", c.Status)
	used, e := geilisub.ReadDailyUsage(ctx, s.entClient, parent.ID, c.TermID, time.Now())
	require.NoError(t, e)
	summary := geilisub.ContractSummary(c, lots, used, time.Now())
	require.Equal(t, 1, summary.ActiveLotCount)
	require.Equal(t, 80.0, summary.AvailableUSD)
	// An unrelated sibling may continue consuming while the provider processes a refund.
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(ids[1]).AddDailyUsageUsd(2).AddLifetimeUsageUsd(2).Exec(ctx))
	_, e = geilisub.RestoreContractRefund(ctx, s.entClient, order.ID, time.Now())
	require.NoError(t, e)
	_, e = geilisub.FreezeContractRefund(ctx, s.entClient, order.ID, time.Now())
	require.NoError(t, e)
	_, e = geilisub.RevertContractChange(ctx, s.entClient, order.ID, time.Now())
	require.NoError(t, e)
	sibling, e := s.entClient.UserSubscriptionEntitlement.Get(ctx, ids[1])
	require.NoError(t, e)
	require.Equal(t, 12.0, sibling.DailyUsageUsd)
}

func TestLegacyManagementRolloutAllowlist(t *testing.T) {
	s, u, _, _, _ := legacyPaymentFixture(t)
	ctx := context.Background()
	svc := &SubscriptionService{entClient: s.entClient}
	_, e := svc.SetLegacyRollout(ctx, LegacyRollout{Enabled: true, UserIDs: []int64{u.ID}})
	require.NoError(t, e)
	yes, e := legacyManagementEnabled(ctx, s.entClient, u.ID)
	require.NoError(t, e)
	require.True(t, yes)
	no, e := legacyManagementEnabled(ctx, s.entClient, u.ID+1)
	require.NoError(t, e)
	require.False(t, no)
	_, e = svc.SetLegacyRollout(ctx, LegacyRollout{Enabled: true, UserIDs: []int64{u.ID, u.ID}})
	require.Error(t, e)
	_, e = svc.SetLegacyRollout(ctx, LegacyRollout{Enabled: false})
	require.NoError(t, e)
	no, e = legacyManagementEnabled(ctx, s.entClient, u.ID)
	require.NoError(t, e)
	require.False(t, no)
}
func TestLegacyManagementOrderLinesUseActualFulfillment(t *testing.T) {
	s, u, plans, parent, _ := legacyPaymentFixture(t)
	ctx := context.Background()
	q, e := s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, SubscriptionID: parent.ID, PlanID: plans[0].ID, Operation: "purchase", Units: 1})
	require.NoError(t, e)
	order, e := v2Create(t, s, u, q)
	require.NoError(t, e)
	require.Zero(t, PaymentLegacyLines(order)[0].EntitlementID)
	v2Fulfill(t, s, order)
	current, e := s.entClient.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, e)
	lines := PaymentLegacyLines(current)
	require.Len(t, lines, 1)
	require.Positive(t, lines[0].EntitlementID)
	lot, e := s.entClient.UserSubscriptionEntitlement.Get(ctx, lines[0].EntitlementID)
	require.NoError(t, e)
	require.True(t, lot.ExpiresAt.Equal(lines[0].After.ExpiresAt))
	require.Equal(t, 7*24*time.Hour, lot.ExpiresAt.Sub(lot.StartsAt))
}

func TestLegacyManagementGiftAndExpiredRightsNeverAligned(t *testing.T) {
	s, u, _, parent, ids := legacyPaymentFixture(t)
	ctx := context.Background()
	svc := &SubscriptionService{entClient: s.entClient}
	request := LegacyAlignmentRequest{UserID: u.ID, EntitlementIDs: ids, IdempotencyKey: "unsafe-alignment-test", Reason: "test"}
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(ids[0]).SetSourceType("campaign").Exec(ctx))
	_, err := svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.Error(t, err)
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(ids[0]).SetSourceType("payment").SetExpiresAt(time.Now().Add(-time.Hour)).Exec(ctx))
	_, err = svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.Error(t, err)
	// A later independent gift is not the target when selecting two paid units.
	require.NoError(t, s.entClient.UserSubscriptionEntitlement.UpdateOneID(ids[0]).SetExpiresAt(time.Now().Add(time.Hour)).Exec(ctx))
	gift, err := s.entClient.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetSourceType("campaign").SetStatus("active").SetStartsAt(time.Now().Add(-time.Hour)).SetExpiresAt(time.Now().Add(7 * 24 * time.Hour)).SetDailyLimitUsd(45).Save(ctx)
	require.NoError(t, err)
	preview, err := svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.NoError(t, err)
	require.True(t, preview.ExpiresAt.Before(gift.ExpiresAt))
	request.Apply = true
	request.ExpectedSnapshot = preview.Snapshot
	_, err = svc.AlignLegacyEntitlements(ctx, parent.ID, 1, request)
	require.NoError(t, err)
	unchanged, err := s.entClient.UserSubscriptionEntitlement.Get(ctx, gift.ID)
	require.NoError(t, err)
	require.True(t, unchanged.ExpiresAt.Equal(gift.ExpiresAt))
	require.Equal(t, gift.DailyLimitUsd, unchanged.DailyLimitUsd)
}

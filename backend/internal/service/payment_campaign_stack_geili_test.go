//go:build unit && integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionV2PostgresCampaignStack(t *testing.T) {
	c, db := v2PaymentPostgres(t)
	ctx := context.Background()
	fixture := func(t *testing.T, remaining time.Duration) (*PaymentService, *dbent.User, []*dbent.SubscriptionPlan, *dbent.UserSubscriptionEntitlement) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		expiry := time.Now().UTC().Add(remaining).Truncate(time.Microsecond)
		parent, err := c.UserSubscription.Create().SetUserID(u.ID).SetStartsAt(expiry.Add(-7 * 24 * time.Hour)).SetExpiresAt(expiry).SetStatus("active").Save(ctx)
		require.NoError(t, err)
		gift, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetSourceType("campaign").SetStatus("active").SetStartsAt(parent.StartsAt).SetExpiresAt(expiry).SetDailyLimitUsd(45).SetDailyWindowStart(geilisub.DayStart(time.Now())).SetDailyUsageUsd(20).SetLifetimeUsageUsd(20).Save(ctx)
		require.NoError(t, err)
		return s, u, p, gift
	}
	t.Run("both-tiers-complete-with-usage-and-idempotency", func(t *testing.T) {
		for _, idx := range []int{0, 1} {
			s, u, p, gift := fixture(t, 72*time.Hour)
			q := v2Quote(t, s, u.ID, p[idx], "stack", 2, 0)
			require.Equal(t, "campaign_stack", q.ManagementMode)
			require.Equal(t, 3, q.BillableDays)
			require.Equal(t, float64(6*(idx+1)), q.OrderAmount)
			require.Equal(t, 20.0, q.Projected.DailyUsageUSD)
			require.Equal(t, 45+2**p[idx].DailyLimitUsd, *q.Projected.DailyLimitUSD)
			snapshot, err := s.readSubscriptionV2Quote(q.QuoteID, u.ID, time.Now())
			require.NoError(t, err)
			require.Equal(t, 4, snapshot.Version)
			_, err = s.readSubscriptionV2Quote(q.QuoteID, u.ID+1, time.Now())
			require.Error(t, err)
			_, err = s.readSubscriptionV2Quote(q.QuoteID+"bad", u.ID, time.Now())
			require.Error(t, err)
			// Normal usage between quote and payment does not change the signed terms.
			_, err = db.Exec(`UPDATE subscription_daily_usage SET used_usd=25 WHERE subscription_id=$1`, gift.UserSubscriptionID)
			require.NoError(t, err)
			o, err := v2Create(t, s, u, q)
			require.NoError(t, err)
			require.True(t, isSubscriptionV2Order(o))
			after := v2Fulfill(t, s, o)
			require.Equal(t, gift.UserSubscriptionID, after.SubscriptionID)
			require.Equal(t, snapshot.Change.Before.TermID, after.TermID)
			require.True(t, gift.ExpiresAt.Equal(after.ExpiresAt))
			require.NoError(t, s.ensureSubscriptionV2Assigned(ctx, o))
			used, err := geilisub.ReadDailyUsage(ctx, c, after.SubscriptionID, after.TermID, time.Now())
			require.NoError(t, err)
			require.Equal(t, 25.0, used)
			lots, err := geilisub.ReadLots(ctx, c, after.SubscriptionID)
			require.NoError(t, err)
			require.Len(t, lots, 3)
			renew := v2Quote(t, s, u.ID, p[idx], "renew", 0, 1)
			require.Equal(t, 2*p[idx].Price, renew.OrderAmount)
			renewOrder, err := v2Create(t, s, u, renew)
			require.NoError(t, err)
			v2Fulfill(t, s, renewOrder)
			unchanged, err := c.UserSubscriptionEntitlement.Get(ctx, gift.ID)
			require.NoError(t, err)
			require.True(t, gift.ExpiresAt.Equal(unchanged.ExpiresAt))
			require.Equal(t, gift.DailyLimitUsd, unchanged.DailyLimitUsd)
		}
	})
	t.Run("expired-paid-contract-can-stack-again", func(t *testing.T) {
		s, u, p := v2PaymentFixtureWithClient(t, c)
		paid := v2Fulfill(t, s, v2CreateOrderForCampaignTest(t, s, u, p[3]))
		now := time.Now().UTC().Truncate(time.Microsecond)
		expiry := now.Add(3 * 24 * time.Hour)
		_, err := db.Exec(`UPDATE subscription_contracts SET expires_at=$2 WHERE subscription_id=$1`, paid.SubscriptionID, now.Add(-time.Second))
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE user_subscription_entitlements SET expires_at=$2 WHERE user_subscription_id=$1`, paid.SubscriptionID, now.Add(-time.Second))
		require.NoError(t, err)
		_, err = c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(paid.SubscriptionID).SetSourceType("campaign").SetStatus("active").SetStartsAt(expiry.Add(-7 * 24 * time.Hour)).SetExpiresAt(expiry).SetDailyLimitUsd(45).Save(ctx)
		require.NoError(t, err)
		q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		after := v2Fulfill(t, s, o)
		require.Equal(t, paid.TermID, after.TermID)
		require.Equal(t, 1, after.Quantity)
		require.Equal(t, "week", after.Kind)
	})
	t.Run("month-purchase-renew-and-cross-pool-stay-blocked", func(t *testing.T) {
		s, u, p, gift := fixture(t, 72*time.Hour)
		for _, req := range []SubscriptionQuoteRequest{{PlanID: p[2].ID, Operation: "stack", Units: 1}, {PlanID: p[0].ID, Operation: "purchase", Units: 1}, {PlanID: p[0].ID, Operation: "renew", Periods: 1}} {
			req.UserID = u.ID
			_, err := s.QuoteSubscription(ctx, req)
			require.Error(t, err)
		}
		_, err := c.UserSubscription.Create().SetUserID(u.ID).SetStartsAt(time.Now()).SetExpiresAt(gift.ExpiresAt).SetStatus("suspended").Save(ctx)
		require.NoError(t, err)
		_, err = s.QuoteSubscription(ctx, SubscriptionQuoteRequest{UserID: u.ID, PlanID: p[0].ID, Operation: "stack", Units: 1})
		require.Error(t, err)
	})
	t.Run("one-pending-order-under-concurrency", func(t *testing.T) {
		s, u, p, _ := fixture(t, 72*time.Hour)
		q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := v2Create(t, s, u, q); errs <- err }()
		}
		wg.Wait()
		close(errs)
		success := 0
		for err := range errs {
			if err == nil {
				success++
			} else {
				require.Equal(t, "SUBSCRIPTION_ORDER_PENDING", infraerrors.Reason(err))
			}
		}
		require.Equal(t, 1, success)
	})
	t.Run("catalog-and-gift-changes-invalidate-orders", func(t *testing.T) {
		for _, kind := range []string{"price", "gift"} {
			s, u, p, gift := fixture(t, 72*time.Hour)
			q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
			if kind == "price" {
				require.NoError(t, c.SubscriptionPlan.UpdateOneID(p[0].ID).SetPrice(9).Exec(ctx))
			} else {
				require.NoError(t, c.UserSubscriptionEntitlement.UpdateOneID(gift.ID).SetDailyLimitUsd(90).Exec(ctx))
			}
			_, err := v2Create(t, s, u, q)
			require.Equal(t, "SUBSCRIPTION_QUOTE_CHANGED", infraerrors.Reason(err))
		}
	})
	t.Run("late-payment-preserves-paid-review", func(t *testing.T) {
		s, u, p, gift := fixture(t, 72*time.Hour)
		q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
		o, err := v2Create(t, s, u, q)
		require.NoError(t, err)
		require.NoError(t, c.UserSubscriptionEntitlement.UpdateOneID(gift.ID).SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
		o, err = c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Save(ctx)
		require.NoError(t, err)
		err = s.fulfillPaymentWebhook(ctx, o.ID)
		require.NoError(t, err)
		o, err = c.PaymentOrder.Get(ctx, o.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusFailed, o.Status)
		require.Contains(t, *o.FailedReason, "SUBSCRIPTION_PAID_REVIEW_REQUIRED")
		require.NoError(t, s.fulfillPaymentWebhook(ctx, o.ID))
		lots, err := geilisub.ReadLots(ctx, c, gift.UserSubscriptionID)
		require.NoError(t, err)
		require.Len(t, lots, 1)
	})
	t.Run("quote-never-outlives-gift", func(t *testing.T) {
		s, u, p, gift := fixture(t, 2*time.Minute)
		q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
		require.Equal(t, 1, q.BillableDays)
		require.True(t, gift.ExpiresAt.Equal(q.ExpiresAt))
		_, err := s.readSubscriptionV2Quote(q.QuoteID, u.ID, gift.ExpiresAt)
		require.Equal(t, "SUBSCRIPTION_QUOTE_EXPIRED", infraerrors.Reason(err))
	})
	t.Run("quote-expired-while-waiting-for-price-lock", func(t *testing.T) {
		s, u, p, _ := fixture(t, 72*time.Hour)
		q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
		payload, err := s.readSubscriptionV2Quote(q.QuoteID, u.ID, time.Now())
		require.NoError(t, err)
		payload.ExpiresAt = time.Now().Add(300 * time.Millisecond)
		q.QuoteID, err = s.paymentResume().createSignedToken(payload)
		require.NoError(t, err)
		locker, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = locker.Exec(`SELECT id FROM subscription_plans WHERE id=$1 FOR UPDATE`, p[0].ID)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := v2Create(t, s, u, q); done <- err }()
		waitPaymentLock(t, db, "subscription_plans")
		time.Sleep(time.Until(payload.ExpiresAt) + 10*time.Millisecond)
		require.NoError(t, locker.Commit())
		require.Equal(t, "SUBSCRIPTION_QUOTE_EXPIRED", infraerrors.Reason(<-done))
	})
	t.Run("new-gift-serialized-against-order", func(t *testing.T) {
		s, u, p, gift := fixture(t, 72*time.Hour)
		q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
		locker, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = locker.Exec(`SELECT id FROM users WHERE id=$1 FOR UPDATE`, u.ID)
		require.NoError(t, err)
		_, err = locker.Exec(`INSERT INTO user_subscription_entitlements(user_subscription_id,source_type,source_reference,status,starts_at,expires_at,daily_limit_usd,created_at,updated_at) VALUES($1,'campaign','second-claim','active',$2,$3,45,NOW(),NOW())`, gift.UserSubscriptionID, gift.StartsAt, gift.ExpiresAt)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := v2Create(t, s, u, q); done <- err }()
		waitPaymentLock(t, db, "users")
		require.NoError(t, locker.Commit())
		require.Equal(t, "SUBSCRIPTION_QUOTE_CHANGED", infraerrors.Reason(<-done))
	})
	t.Run("earlier-gift-expiry-during-plan-lock-invalidates-order", func(t *testing.T) {
		s, u, p, gift := fixture(t, 72*time.Hour)
		early := time.Now().UTC().Add(2 * time.Second).Truncate(time.Microsecond)
		_, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(gift.UserSubscriptionID).SetSourceType("campaign").SetStatus("active").SetStartsAt(early.Add(-7 * 24 * time.Hour)).SetExpiresAt(early).SetDailyLimitUsd(45).Save(ctx)
		require.NoError(t, err)
		q := v2Quote(t, s, u.ID, p[0], "stack", 1, 0)
		locker, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer locker.Rollback()
		_, err = locker.Exec(`SELECT id FROM subscription_plans WHERE id=$1 FOR UPDATE`, p[0].ID)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := v2Create(t, s, u, q); done <- err }()
		waitPaymentLock(t, db, "subscription_plans")
		time.Sleep(time.Until(early) + 10*time.Millisecond)
		require.NoError(t, locker.Commit())
		require.Equal(t, "SUBSCRIPTION_QUOTE_CHANGED", infraerrors.Reason(<-done))
	})
}

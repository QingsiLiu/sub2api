//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type entitlementFixture struct {
	c       *dbent.Client
	svc     *service.SubscriptionService
	sub     *service.UserSubscription
	plan    *dbent.SubscriptionPlan
	key     *service.APIKey
	user    *service.User
	account *service.Account
}

func newEntitlementFixture(t *testing.T) entitlementFixture {
	t.Helper()
	ctx := context.Background()
	c := testEntClient(t)
	suffix := uuid.NewString()
	user := mustCreateUser(t, c, &service.User{Email: "lot-" + suffix + "@example.invalid", PasswordHash: "fixture"})
	group := mustCreateGroup(t, c, &service.Group{Name: "lot-" + suffix, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard, SubscriptionEnabled: true})
	plan, err := c.SubscriptionPlan.Create().SetName("lot-" + suffix).SetPrice(10).SetValidityDays(30).SetDailyLimitUsd(200).SetWeeklyLimitUsd(500).SetMonthlyLimitUsd(1000).Save(ctx)
	require.NoError(t, err)
	svc := service.NewSubscriptionService(NewGroupRepository(c, integrationDB), NewUserSubscriptionRepository(c), nil, c, nil)
	t.Cleanup(svc.Stop)
	sub, err := svc.AssignSubscription(ctx, &service.AssignSubscriptionInput{UserID: user.ID, PlanID: &plan.ID})
	require.NoError(t, err)
	require.Len(t, sub.Entitlements, 1)
	key := mustCreateApiKey(t, c, &service.APIKey{UserID: user.ID, GroupID: &group.ID, SubscriptionID: &sub.ID, BillingSource: "subscription", Key: "synthetic-" + suffix, Name: "lot-key"})
	account := mustCreateAccount(t, c, &service.Account{Name: "lot-" + suffix, Type: service.AccountTypeAPIKey})

	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM subscription_usage_allocations WHERE request_key IN (SELECT request_key FROM subscription_requests WHERE subscription_id=$1)`,
			`DELETE FROM subscription_media_tasks WHERE subscription_id=$1`,
			`DELETE FROM subscription_requests WHERE subscription_id=$1`,
			`DELETE FROM subscription_refunds WHERE subscription_id=$1`,
			`DELETE FROM subscription_operations WHERE subscription_id=$1`,
			`DELETE FROM user_subscriptions WHERE id=$1`,
		} {
			_, err := integrationDB.ExecContext(ctx, query, sub.ID)
			require.NoError(t, err)
		}
		for _, query := range []string{`DELETE FROM payment_orders WHERE user_id=$1`, `DELETE FROM api_keys WHERE user_id=$1`, `DELETE FROM users WHERE id=$1`} {
			_, err := integrationDB.ExecContext(ctx, query, user.ID)
			require.NoError(t, err)
		}
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM subscription_plans WHERE id=$1`, plan.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	return entitlementFixture{c, svc, sub, plan, key, user, account}
}
func (f entitlementFixture) order(t *testing.T, mode string, quantity int) *dbent.PaymentOrder {
	t.Helper()
	now := time.Now()
	o, err := f.c.PaymentOrder.Create().SetUserID(f.user.ID).SetUserEmail(f.user.Email).SetUserName("fixture").SetAmount(10 * float64(quantity)).SetPayAmount(10 * float64(quantity)).SetRechargeCode(uuid.NewString()).SetPaymentType("test").SetPaymentTradeNo("").SetOutTradeNo(uuid.NewString()).SetOrderType("subscription").SetPlanID(f.plan.ID).SetSubscriptionDays(30).SetSubscriptionQuantity(quantity).SetSubscriptionMode(mode).SetStatus("PAID").SetPaidAt(now).SetExpiresAt(now.Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("fixture.invalid").Save(context.Background())
	require.NoError(t, err)
	return o
}
func (f entitlementFixture) purchase(o *dbent.PaymentOrder, now time.Time) error {
	ctx := context.Background()
	tx, err := f.c.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tc := dbent.NewTxContext(ctx, tx)
	if err := geilisub.Purchase(tc, tx.Client(), f.sub.ID, &f.plan.ID, o.ID, 30, o.SubscriptionQuantity, o.SubscriptionMode, f.plan.DailyLimitUsd, f.plan.WeeklyLimitUsd, f.plan.MonthlyLimitUsd, "payment", fmt.Sprint(o.ID), 0, now); err != nil {
		return err
	}
	return tx.Commit()
}
func TestEntitlementPurchasesAndReplay(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	now := time.Now()
	stack := f.order(t, "stack", 2)
	require.NoError(t, f.purchase(stack, now))
	require.NoError(t, f.purchase(stack, now))
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.Len(t, lots, 3)
	a := geilisub.Aggregate(lots, now)
	require.Equal(t, 600.0, *a.DailyLimitUSD)
	renew := f.order(t, "renew", 2)
	require.NoError(t, f.purchase(renew, now))
	require.NoError(t, f.purchase(renew, now))
	after, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.Len(t, after, 3)
	unchanged := 0
	before := map[int64]time.Time{}
	for _, e := range lots {
		before[e.ID] = e.ExpiresAt
	}
	for _, e := range after {
		if e.ExpiresAt.Equal(before[e.ID]) {
			unchanged++
		} else {
			require.True(t, e.ExpiresAt.Equal(before[e.ID].AddDate(0, 0, 30)))
		}
	}
	require.Equal(t, 1, unchanged)
	excessive := f.order(t, "renew", 4)
	require.ErrorIs(t, f.purchase(excessive, now), geilisub.ErrRenewQuantity)
	count, err := f.c.SubscriptionEntitlementOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 4)
}
func TestEntitlementConcurrentSettlements100(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	repo := NewUsageBillingRepository(f.c, integrationDB)
	const n = 100
	commands := make([]*service.UsageBillingCommand, n)
	for i := 0; i < n; i++ {
		sub, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
		require.NoError(t, err)
		commands[i] = &service.UsageBillingCommand{RequestID: uuid.NewString(), APIKeyID: f.key.ID, UserID: f.user.ID, AccountID: f.account.ID, AccountType: "apikey", SubscriptionID: &f.sub.ID, SubscriptionAdmissionKey: sub.AdmissionKey, SubscriptionCost: 1}
	}
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for _, cmd := range commands {
		wg.Add(1)
		go func(cmd *service.UsageBillingCommand) {
			defer wg.Done()
			<-start
			_, err := repo.Apply(ctx, cmd)
			errs <- err
		}(cmd)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for _, cmd := range commands {
		result, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, result.Applied)
	}
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, lots[0].LifetimeUsageUSD)
	require.Equal(t, 100.0, lots[0].DailyUsageUSD)
	var allocated float64
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COALESCE(sum(a.cost_usd),0),count(*) FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key WHERE r.subscription_id=$1`, f.sub.ID).Scan(&allocated, &count))
	require.Equal(t, 100.0, allocated)
	require.Equal(t, n, count)
	parent, err := f.c.UserSubscription.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, parent.DailyUsageUsd)
}
func TestEntitlementLateSettlementKeepsAdmissionWindow(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	original := lots[0]
	future := time.Now().AddDate(0, 0, 1)
	day := future.Truncate(24 * time.Hour)
	_, err = f.c.UserSubscriptionEntitlement.UpdateOneID(original.ID).SetDailyWindowStart(day).SetDailyUsageUsd(7).SetExpiresAt(time.Now().Add(-time.Second)).Save(ctx)
	require.NoError(t, err)
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, geilisub.SettleAdmission(ctx, tx, admitted.AdmissionKey, uuid.NewString(), f.sub.ID, f.key.ID, 3, future))
	require.NoError(t, tx.Commit())
	row, err := f.c.UserSubscriptionEntitlement.Get(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, 7.0, row.DailyUsageUsd)
	require.Equal(t, 3.0, row.LifetimeUsageUsd)
	var raw []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT lots FROM subscription_requests WHERE request_key=$1", admitted.AdmissionKey).Scan(&raw))
	var snap []geilisub.Lot
	require.NoError(t, json.Unmarshal(raw, &snap))
	require.True(t, snap[0].ExpiresAt.Equal(original.ExpiresAt))
}
func TestEntitlementZeroLotLegacyAndPurchaseFailureRollback(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	o := f.order(t, "stack", 2)
	tx, err := f.c.Tx(ctx)
	require.NoError(t, err)
	tc := dbent.NewTxContext(ctx, tx)
	require.NoError(t, geilisub.Purchase(tc, tx.Client(), f.sub.ID, &f.plan.ID, o.ID, 30, 2, "stack", f.plan.DailyLimitUsd, nil, nil, "payment", fmt.Sprint(o.ID), 0, time.Now()))
	require.NoError(t, tx.Rollback())
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.Len(t, lots, 1)
}

func TestEntitlementMigrationRunnerReplayPreservesPaidLots(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	o := f.order(t, "stack", 2)
	require.NoError(t, f.purchase(o, time.Now()))
	before, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	after, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestEntitlementUpgrade251BackfillsOnlyUnrepresentedParents(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	c := tx.Client()
	suffix := uuid.NewString()
	user, err := c.User.Create().SetEmail("upgrade-" + suffix + "@example.invalid").SetPasswordHash("fixture").Save(ctx)
	require.NoError(t, err)
	plan, err := c.SubscriptionPlan.Create().SetName("upgrade-" + suffix).SetPrice(10).SetDailyLimitUsd(45).Save(ctx)
	require.NoError(t, err)
	now := time.Now()
	missing, err := c.UserSubscription.Create().SetUserID(user.ID).SetPlanID(plan.ID).SetStartsAt(now.Add(-time.Hour)).SetExpiresAt(now.AddDate(0, 0, 30)).SetDailyUsageUsd(.125).SetWeeklyUsageUsd(.125).SetMonthlyUsageUsd(.125).Save(ctx)
	require.NoError(t, err)
	secondUser, err := c.User.Create().SetEmail("paid-upgrade-" + suffix + "@example.invalid").SetPasswordHash("fixture").Save(ctx)
	require.NoError(t, err)
	paidParent, err := c.UserSubscription.Create().SetUserID(secondUser.ID).SetPlanID(plan.ID).SetStartsAt(now).SetExpiresAt(now.AddDate(0, 0, 30)).Save(ctx)
	require.NoError(t, err)
	order, err := c.PaymentOrder.Create().SetUserID(secondUser.ID).SetUserEmail(secondUser.Email).SetUserName("fixture").SetAmount(20).SetPayAmount(20).SetRechargeCode(suffix).SetPaymentType("test").SetPaymentTradeNo("").SetOrderType("subscription").SetStatus("COMPLETED").SetExpiresAt(now).SetClientIP("127.0.0.1").SetSrcHost("fixture.invalid").Save(ctx)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		require.NoError(t, c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(paidParent.ID).SetPlanID(plan.ID).SetSourceOrderID(order.ID).SetLotIndex(i).SetSourceType("legacy").SetStartsAt(now).SetExpiresAt(paidParent.ExpiresAt).SetDailyLimitUsd(45).Exec(ctx))
	}
	repair, err := migrations.FS.ReadFile("251_subscription_entitlement_consistency.sql")
	require.NoError(t, err)
	for pass := 0; pass < 2; pass++ {
		_, err = c.ExecContext(ctx, string(repair))
		require.NoError(t, err)
		legacy, err := geilisub.ReadLots(ctx, c, missing.ID)
		require.NoError(t, err)
		require.Len(t, legacy, 1)
		require.Equal(t, .125, legacy[0].LifetimeUsageUSD)
		require.Equal(t, .125, legacy[0].DailyUsageUSD)
		paid, err := geilisub.ReadLots(ctx, c, paidParent.ID)
		require.NoError(t, err)
		require.Len(t, paid, 2)
		for _, e := range paid {
			require.NotNil(t, e.SourceOrderID)
			require.Equal(t, "payment", e.SourceType)
		}
	}
}

func TestEntitlementCancelledUnsentRequestDoesNotRemainInFlight(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	require.NoError(t, f.svc.CancelUnsentConsumption(ctx, admitted))
	require.NoError(t, f.svc.CancelUnsentConsumption(ctx, admitted))
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT status FROM subscription_requests WHERE request_key=$1", admitted.AdmissionKey).Scan(&state))
	require.Equal(t, "cancelled", state)
}
func TestEntitlementBillingReplayClosesNewAdmissionWithoutCharging(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	repo := NewUsageBillingRepository(f.c, integrationDB)
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	cmd := &service.UsageBillingCommand{RequestID: uuid.NewString(), APIKeyID: f.key.ID, UserID: f.user.ID, AccountID: f.account.ID, AccountType: "apikey", SubscriptionID: &f.sub.ID, SubscriptionAdmissionKey: admitted.AdmissionKey, SubscriptionCost: 1}
	_, err = repo.Apply(ctx, cmd)
	require.NoError(t, err)
	second, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	cmd.SubscriptionAdmissionKey = second.AdmissionKey
	result, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	var pending int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM subscription_requests WHERE subscription_id=$1 AND status='admitted'", f.sub.ID).Scan(&pending))
	require.Zero(t, pending)
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 1.0, lots[0].LifetimeUsageUSD)
}

func TestEntitlementCacheOutboxVersionPreservesConcurrentUpdates(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT version FROM subscription_cache_outbox WHERE subscription_id=$1", f.sub.ID).Scan(&version))
	require.NoError(t, f.c.UserSubscription.UpdateOneID(f.sub.ID).SetNotes("new invalidation").Exec(ctx))
	result, err := integrationDB.ExecContext(ctx, "DELETE FROM subscription_cache_outbox WHERE subscription_id=$1 AND version=$2", f.sub.ID, version)
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, n)
	var newer int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT version FROM subscription_cache_outbox WHERE subscription_id=$1", f.sub.ID).Scan(&newer))
	require.Greater(t, newer, version)
}

func TestEntitlementAsyncMediaKeepsOriginalAdmissionAfterExpiry(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	task := "synthetic-video-" + uuid.NewString()
	require.NoError(t, f.svc.BindMediaConsumption(ctx, task, admitted, f.key.ID))
	require.NoError(t, f.svc.BindMediaConsumption(ctx, task, admitted, f.key.ID))
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(lots[0].ID).SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
	require.NoError(t, f.c.UserSubscription.UpdateOneID(f.sub.ID).SetStatus("expired").SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
	resumed, err := f.svc.ResumeMediaConsumption(ctx, task, f.user.ID, f.key.ID)
	require.NoError(t, err)
	require.True(t, resumed.MediaLookupAdmission)
	require.Equal(t, admitted.AdmissionKey, resumed.AdmissionKey)
	_, err = f.svc.ResumeMediaConsumption(ctx, task, f.user.ID+1, f.key.ID)
	require.ErrorIs(t, err, service.ErrSubscriptionNotFound)
	_, err = f.svc.ResumeMediaConsumption(ctx, task, f.user.ID, f.key.ID+1)
	require.ErrorIs(t, err, service.ErrSubscriptionNotFound)
	repo := NewUsageBillingRepository(f.c, integrationDB)
	cmd := &service.UsageBillingCommand{RequestID: uuid.NewString(), APIKeyID: f.key.ID, UserID: f.user.ID, AccountID: f.account.ID, AccountType: "apikey", SubscriptionID: &f.sub.ID, SubscriptionAdmissionKey: resumed.AdmissionKey, SubscriptionCost: 2, MediaType: "video"}
	result, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	result, err = repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	row, err := f.c.UserSubscriptionEntitlement.Get(ctx, lots[0].ID)
	require.NoError(t, err)
	require.Equal(t, 2.0, row.LifetimeUsageUsd)
}

func TestEntitlementPurchaseNormalizesPostgresMicroseconds(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	o := f.order(t, "stack", 1)
	// Round-up nanoseconds must not make the newly persisted lot start "after now".
	now := time.Now().Truncate(time.Microsecond).Add(999 * time.Nanosecond)
	require.NoError(t, f.purchase(o, now))
	parent, err := f.c.UserSubscription.Get(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, "active", parent.Status)
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	created := lots[len(lots)-1]
	require.False(t, created.StartsAt.After(now))
	require.Equal(t, 2, geilisub.Aggregate(lots, now).ActiveLotCount)
}

//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func v2TestCommand(f entitlementFixture, admitted *service.UserSubscription, keyID int64, cost float64) *service.UsageBillingCommand {
	return &service.UsageBillingCommand{RequestID: uuid.NewString(), APIKeyID: keyID, UserID: f.user.ID, AccountID: f.account.ID, AccountType: "apikey", SubscriptionID: &f.sub.ID, SubscriptionAdmissionKey: admitted.AdmissionKey, SubscriptionCost: cost}
}
func v2ApplyCommand(t *testing.T, f entitlementFixture, cmd *service.UsageBillingCommand) {
	t.Helper()
	result, err := NewUsageBillingRepository(f.c, integrationDB).Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
}
func v2StandardFixture(t *testing.T) (entitlementFixture, *geilisub.Contract) {
	t.Helper()
	f := newEntitlementFixture(t)
	ctx := context.Background()
	require.NoError(t, f.c.SubscriptionPlan.UpdateOneID(f.plan.ID).SetDailyLimitUsd(90).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(f.sub.Entitlements[0].ID).SetDailyLimitUsd(90).Exec(ctx))
	contract := ensureBillingV2Contract(t, f, time.Now())
	require.Equal(t, geilisub.ContractModeV2, contract.Mode)
	return f, contract
}
func v2LegacyWithExpiredSibling(t *testing.T) (entitlementFixture, *geilisub.Contract) {
	t.Helper()
	f := newEntitlementFixture(t)
	ctx := context.Background()
	require.NoError(t, f.c.SubscriptionPlan.UpdateOneID(f.plan.ID).SetIsLegacyCompat(true).Exec(ctx))
	now := time.Now()
	day := geilisub.DayStart(now)
	require.NoError(t, f.c.UserSubscription.UpdateOneID(f.sub.ID).SetStartsAt(now.AddDate(0, 0, -30)).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(f.sub.Entitlements[0].ID).SetStartsAt(now.AddDate(0, 0, -30)).SetExpiresAt(now.Add(-time.Minute)).SetDailyLimitUsd(45).SetDailyWindowStart(day).SetDailyUsageUsd(30).SetLifetimeUsageUsd(30).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(f.sub.ID).SetPlanID(f.plan.ID).SetStartsAt(now.AddDate(0, 0, -1)).SetExpiresAt(now.AddDate(0, 0, 29)).SetDailyLimitUsd(45).SetDailyWindowStart(day).SetDailyUsageUsd(10).SetLifetimeUsageUsd(10).Exec(ctx))
	contract := ensureBillingV2Contract(t, f, now)
	require.Equal(t, geilisub.ContractModeLegacy, contract.Mode)
	return f, contract
}

func TestSubscriptionBillingV2LegacyResetKeepsExpiredAuditOffset(t *testing.T) {
	f, contract := v2LegacyWithExpiredSibling(t)
	ctx := context.Background()
	repo := NewUserSubscriptionRepository(f.c)
	now := time.Now()
	before, err := repo.GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 10.0, before.DailyUsageUSD)
	admitted, err := f.svc.AdmitConsumption(ctx, before, f.key.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ResetUsageWindows(ctx, f.sub.ID, true, false, false, now, now))
	v2ApplyCommand(t, f, v2TestCommand(f, admitted, f.key.ID, 2))
	got, err := repo.GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 2.0, got.DailyUsageUSD, "expired sibling's audit usage must not hide new spending after reset")
	require.Equal(t, 43.0, *got.QuotaSummary.RemainingUSD)
	raw, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 32.0, raw, "raw ledger retains the retired portion's 30 dollars as its projection offset")
	var recorded string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT detail::text FROM subscription_operations WHERE subscription_id=$1 AND operation='reset_daily' ORDER BY id DESC LIMIT 1`, f.sub.ID).Scan(&recorded))
	require.Equal(t, 10.0, gjson.Get(recorded, "before_daily_usage_usd").Float())
	require.Equal(t, 0.0, gjson.Get(recorded, "after_daily_usage_usd").Float())
}

func TestSubscriptionBillingV2MissingAdmissionCanRetryWithoutLostDedup(t *testing.T) {
	f, contract := v2StandardFixture(t)
	ctx := context.Background()
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	cmd := v2TestCommand(f, admitted, f.key.ID, 4)
	cmd.SubscriptionAdmissionKey = ""
	_, err = NewUsageBillingRepository(f.c, integrationDB).Apply(ctx, cmd)
	require.ErrorIs(t, err, geilisub.ErrAdmissionRequired)
	var dedup int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&dedup))
	require.Zero(t, dedup)
	used, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, time.Now())
	require.NoError(t, err)
	require.Zero(t, used)
	cmd.SubscriptionAdmissionKey = admitted.AdmissionKey
	v2ApplyCommand(t, f, cmd)
	result, err := NewUsageBillingRepository(f.c, integrationDB).Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	used, err = geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, time.Now())
	require.NoError(t, err)
	require.Equal(t, 4.0, used)
}

func TestSubscriptionBillingV2ConcurrentResetAndSettlementsConserveUsage(t *testing.T) {
	f, contract := v2StandardFixture(t)
	ctx := context.Background()
	const count = 48
	commands := make([]*service.UsageBillingCommand, count)
	for i := range commands {
		admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
		require.NoError(t, err)
		commands[i] = v2TestCommand(f, admitted, f.key.ID, 0.125)
	}
	start := make(chan struct{})
	errs := make(chan error, count+1)
	var wg sync.WaitGroup
	for _, cmd := range commands {
		wg.Add(1)
		go func(cmd *service.UsageBillingCommand) {
			defer wg.Done()
			<-start
			_, err := NewUsageBillingRepository(f.c, integrationDB).Apply(ctx, cmd)
			errs <- err
		}(cmd)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		now := time.Now()
		errs <- NewUserSubscriptionRepository(f.c).ResetUsageWindows(ctx, f.sub.ID, true, false, false, now, now)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	used, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, time.Now())
	require.NoError(t, err)
	var resetAmount, lifetime, allocated float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT (detail->>'before_daily_usage_usd')::numeric FROM subscription_operations WHERE subscription_id=$1 AND operation='reset_daily' ORDER BY id DESC LIMIT 1`, f.sub.ID).Scan(&resetAmount))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT sum(lifetime_usage_usd) FROM user_subscription_entitlements WHERE user_subscription_id=$1`, f.sub.ID).Scan(&lifetime))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT sum(a.cost_usd) FROM subscription_usage_allocations a JOIN subscription_requests r USING(request_key) WHERE r.subscription_id=$1`, f.sub.ID).Scan(&allocated))
	require.Equal(t, 6.0, lifetime)
	require.Equal(t, 6.0, allocated)
	require.Equal(t, 6.0, used+resetAmount, "every settlement is either explicitly reset or remains in the shared day ledger")
	for _, cmd := range commands {
		result, err := NewUsageBillingRepository(f.c, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, result.Applied)
	}
	after, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, time.Now())
	require.NoError(t, err)
	require.Equal(t, used, after)
}

func TestSubscriptionBillingV2ProjectionAcrossHTTPAndAdminUsers(t *testing.T) {
	f, _ := v2LegacyWithExpiredSibling(t)
	ctx := context.Background()
	h := handler.NewSubscriptionHandler(f.svc)
	admin := adminhandler.NewSubscriptionHandler(f.svc)
	for _, endpoint := range []struct {
		path    string
		run     gin.HandlerFunc
		prefix  string
		summary bool
	}{
		{"/subscriptions", h.List, "data.0.", false},
		{"/subscriptions/active", h.GetActive, "data.0.", false},
		{"/subscriptions/summary", h.GetSummary, "data.subscriptions.0.", true},
		{"/subscriptions/progress", h.GetProgress, "data.0.subscription.", false},
		{fmt.Sprintf("/admin/subscriptions?user_id=%d", f.user.ID), admin.List, "data.items.0.", false},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			gc, _ := gin.CreateTestContext(w)
			gc.Request = httptest.NewRequest(http.MethodGet, endpoint.path, nil)
			gc.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: f.user.ID})
			endpoint.run(gc)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := w.Body.String()
			field := "daily_usage_usd"
			if endpoint.summary {
				field = "daily_used_usd"
			}
			require.Equal(t, 10.0, gjson.Get(body, endpoint.prefix+field).Float(), body)
			require.Equal(t, 35.0, gjson.Get(body, endpoint.prefix+"quota_summary.remaining_usd").Float(), body)
			require.Equal(t, "legacy_daily", gjson.Get(body, endpoint.prefix+"contract.mode").String(), body)
			require.Equal(t, gjson.Null, gjson.Get(body, endpoint.prefix+"quota_summary.weekly_limit_usd").Type, body)
		})
	}
	users, _, err := NewUserRepository(f.c, integrationDB).ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.UserListFilters{Search: f.user.Email})
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Len(t, users[0].Subscriptions, 1)
	require.Equal(t, 10.0, users[0].Subscriptions[0].DailyUsageUSD)
	require.Equal(t, 35.0, *users[0].Subscriptions[0].QuotaSummary.RemainingUSD)
}

func TestSubscriptionBillingV2SuspendRestoreInvalidatesPendingChange(t *testing.T) {
	f, contract := v2StandardFixture(t)
	ctx := context.Background()
	now := time.Now()
	repo := NewUserSubscriptionRepository(f.c)
	plan := geilisub.Plan{ID: f.plan.ID, Name: f.plan.Name, Kind: "month", DailyUSD: 90, PeriodDays: 30, Price: decimal.NewFromInt(10)}
	quoted, err := geilisub.PreviewContract(contract, plan, plan, "renew", 0, 1, now)
	require.NoError(t, err)
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateStatus(ctx, f.sub.ID, service.SubscriptionStatusSuspended))
	_, err = f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.ErrorIs(t, err, service.ErrSubscriptionInvalid)
	v2ApplyCommand(t, f, v2TestCommand(f, admitted, f.key.ID, 2))
	require.NoError(t, repo.UpdateStatus(ctx, f.sub.ID, service.SubscriptionStatusActive))
	tx, err := f.c.Tx(ctx)
	require.NoError(t, err)
	tc := dbent.NewTxContext(ctx, tx)
	_, err = geilisub.ApplyContractChange(tc, tx.Client(), quoted, 0, "payment", "stale-suspended-quote", 0, time.Now())
	require.ErrorIs(t, err, geilisub.ErrStateConflict)
	require.NoError(t, tx.Rollback())
	require.NoError(t, f.svc.RevokeSubscription(ctx, f.sub.ID))
	_, err = f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.Error(t, err)
	restored, err := f.svc.RestoreSubscription(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 2.0, restored.DailyUsageUSD)
	tx, err = f.c.Tx(ctx)
	require.NoError(t, err)
	tc = dbent.NewTxContext(ctx, tx)
	_, err = geilisub.ApplyContractChange(tc, tx.Client(), quoted, 0, "payment", "stale-revoked-quote", 0, time.Now())
	require.ErrorIs(t, err, geilisub.ErrStateConflict)
	require.NoError(t, tx.Rollback())
	require.Greater(t, restored.Contract.Revision, contract.Revision)
}

func TestSubscriptionBillingV2CrossGroupRatesShareOneDailyLedger(t *testing.T) {
	f, contract := v2StandardFixture(t)
	ctx := context.Background()
	repo := NewUserSubscriptionRepository(f.c)
	cache := service.NewBillingCacheService(nil, nil, repo, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(cache.Stop)
	total := decimal.Zero
	for i, rate := range []float64{0, 0.5, 1.3, 2} {
		group := mustCreateGroup(t, f.c, &service.Group{Name: "v2-rate-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard, SubscriptionEnabled: true, RateMultiplier: 8, SubscriptionRateMultiplier: &rate})
		key := mustCreateApiKey(t, f.c, &service.APIKey{UserID: f.user.ID, GroupID: &group.ID, SubscriptionID: &f.sub.ID, BillingSource: "subscription", Key: "synthetic-rate-" + uuid.NewString(), Name: fmt.Sprint(i)})
		t.Cleanup(func() {
			_, err := integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
			require.NoError(t, err)
		})
		fresh, err := repo.GetByID(ctx, f.sub.ID)
		require.NoError(t, err)
		require.NoError(t, cache.CheckBillingEligibility(ctx, f.user, key, group, fresh, service.PlatformOpenAI))
		admitted, err := f.svc.AdmitConsumption(ctx, fresh, key.ID)
		require.NoError(t, err)
		cost := decimal.NewFromFloat(group.BillingRateMultiplier(true)).Mul(decimal.NewFromInt(2))
		total = total.Add(cost)
		v2ApplyCommand(t, f, v2TestCommand(f, admitted, key.ID, cost.InexactFloat64()))
		fresh, err = repo.GetByID(ctx, f.sub.ID)
		require.NoError(t, err)
		require.Equal(t, total.InexactFloat64(), fresh.DailyUsageUSD)
	}
	used, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, time.Now())
	require.NoError(t, err)
	require.Equal(t, 7.6, used)
	var pending int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM subscription_requests WHERE subscription_id=$1 AND status='admitted'`, f.sub.ID).Scan(&pending))
	require.Zero(t, pending, "zero multiplier requests must also close admission")
}

func TestSubscriptionBillingV2LatePriorDayCannotDebitCurrentDay(t *testing.T) {
	f, contract := v2StandardFixture(t)
	ctx := context.Background()
	now := time.Now()
	day := geilisub.DayStart(now)
	admittedAt := day.Add(-time.Minute)
	require.NoError(t, f.c.UserSubscription.UpdateOneID(f.sub.ID).SetStartsAt(admittedAt.Add(-time.Hour)).Exec(ctx))
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	lots[0].StartsAt = admittedAt.Add(-time.Hour)
	geilisub.NormalizeContractLot(&lots[0], admittedAt, true)
	raw, err := json.Marshal(lots)
	require.NoError(t, err)
	request := uuid.NewString()
	require.NoError(t, f.c.SubscriptionRequest.Create().SetRequestKey(request).SetSubscriptionID(f.sub.ID).SetAPIKeyID(f.key.ID).SetLots(raw).SetAdmittedAt(admittedAt).Exec(ctx))
	require.NoError(t, geilisub.BindAdmission(ctx, f.c, request, contract, admittedAt))
	fresh, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	v2ApplyCommand(t, f, v2TestCommand(f, fresh, f.key.ID, 2))
	v2ApplyCommand(t, f, v2TestCommand(f, &service.UserSubscription{AdmissionKey: request}, f.key.ID, 3))
	today, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 2.0, today)
	yesterday, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, admittedAt)
	require.NoError(t, err)
	require.Equal(t, 3.0, yesterday)
	got, err := NewUserSubscriptionRepository(f.c).GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 2.0, got.DailyUsageUSD)
}

func TestSubscriptionBillingV2AdmissionRechecksExpiryAfterRowLock(t *testing.T) {
	f, _ := v2StandardFixture(t)
	ctx := context.Background()
	lock, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer lock.Rollback()
	require.NoError(t, geilisub.LockSubscription(ctx, lock, f.sub.ID))
	expiry := time.Now().Add(350 * time.Millisecond)
	_, err = lock.ExecContext(ctx, `UPDATE user_subscriptions SET expires_at=$2 WHERE id=$1`, f.sub.ID, expiry)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { _, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID); result <- err }()
	require.Eventually(t, func() bool {
		var waiting int
		err := integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%user_subscriptions%'`).Scan(&waiting)
		return err == nil && waiting > 0
	}, time.Second, 5*time.Millisecond, "admission must be waiting on the locked row before expiry")
	time.Sleep(time.Until(expiry.Add(50 * time.Millisecond)))
	require.NoError(t, lock.Commit())
	require.ErrorIs(t, <-result, service.ErrSubscriptionInvalid, "the time before a row lock is not the admission time")
	var admitted int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM subscription_requests WHERE subscription_id=$1`, f.sub.ID).Scan(&admitted))
	require.Zero(t, admitted)
}

func TestSubscriptionBillingV2RestoreCannotCreateSecondActiveContract(t *testing.T) {
	f, _ := v2StandardFixture(t)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, f.svc.RevokeSubscription(ctx, f.sub.ID))
	replacement, err := f.c.UserSubscription.Create().SetUserID(f.user.ID).SetPlanID(f.plan.ID).SetStartsAt(now).SetExpiresAt(now.AddDate(0, 0, 30)).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM user_subscriptions WHERE id=$1`, replacement.ID)
		require.NoError(t, err)
	})
	plan := geilisub.Plan{ID: f.plan.ID, Name: f.plan.Name, Kind: "month", DailyUSD: 90, PeriodDays: 30, Price: decimal.NewFromInt(10)}
	change, err := geilisub.PreviewContract(nil, geilisub.Plan{}, plan, "purchase", 1, 0, now)
	require.NoError(t, err)
	change.After.SubscriptionID = replacement.ID
	change.After.UserID = f.user.ID
	tx, err := f.c.Tx(ctx)
	require.NoError(t, err)
	tc := dbent.NewTxContext(ctx, tx)
	_, err = geilisub.ApplyContractChange(tc, tx.Client(), change, 0, "payment", "replacement-after-revocation", 0, now)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	_, err = f.svc.RestoreSubscription(ctx, f.sub.ID)
	require.ErrorIs(t, err, service.ErrSubscriptionRestoreConflict)
	old, err := NewUserSubscriptionRepository(f.c).GetByIDIncludeDeleted(ctx, f.sub.ID)
	require.NoError(t, err)
	require.NotNil(t, old.DeletedAt)
	var active int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM user_subscriptions WHERE user_id=$1 AND status='active' AND deleted_at IS NULL AND expires_at>now()`, f.user.ID).Scan(&active))
	require.Equal(t, 1, active)
}

func TestSubscriptionBillingV2ExpiredLegacyLateSettlementPreservesSiblingQuota(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	now := time.Now()
	day := geilisub.DayStart(now)
	require.NoError(t, f.c.SubscriptionPlan.UpdateOneID(f.plan.ID).SetIsLegacyCompat(true).Exec(ctx))
	require.NoError(t, f.c.UserSubscription.UpdateOneID(f.sub.ID).SetStartsAt(now.AddDate(0, 0, -30)).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(f.sub.Entitlements[0].ID).SetStartsAt(now.AddDate(0, 0, -30)).SetExpiresAt(now.Add(time.Hour)).SetDailyLimitUsd(45).SetDailyWindowStart(day).SetDailyUsageUsd(30).SetLifetimeUsageUsd(30).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(f.sub.ID).SetPlanID(f.plan.ID).SetStartsAt(now.AddDate(0, 0, -1)).SetExpiresAt(now.AddDate(0, 0, 29)).SetDailyLimitUsd(45).SetDailyWindowStart(day).SetDailyUsageUsd(10).SetLifetimeUsageUsd(10).Exec(ctx))
	contract := ensureBillingV2Contract(t, f, now)
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(f.sub.Entitlements[0].ID).SetExpiresAt(now.Add(-time.Second)).Exec(ctx))
	cmd := v2TestCommand(f, admitted, f.key.ID, 5)
	v2ApplyCommand(t, f, cmd)
	got, err := NewUserSubscriptionRepository(f.c).GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 10.0, got.DailyUsageUSD)
	require.Equal(t, 35.0, *got.QuotaSummary.RemainingUSD)
	used, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 45.0, used)
	second, err := f.svc.AdmitConsumption(ctx, got, f.key.ID)
	require.NoError(t, err)
	v2ApplyCommand(t, f, v2TestCommand(f, second, f.key.ID, 2))
	got, err = NewUserSubscriptionRepository(f.c).GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 12.0, got.DailyUsageUSD)
	result, err := NewUsageBillingRepository(f.c, integrationDB).Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
}

func TestSubscriptionBillingV2RealtimeTurnsRecheckSharedQuota(t *testing.T) {
	f, _ := v2StandardFixture(t)
	ctx, _ := ctxkey.WithUpstreamDispatch(context.Background())
	ctx = service.WithSubscriptionAdmissionFactory(ctx, func(c context.Context) (*service.UserSubscription, error) {
		return f.svc.AdmitConsumption(c, f.sub, f.key.ID)
	})
	ctx = service.WithSubscriptionAdmissionCancellation(ctx, func(c context.Context, sub *service.UserSubscription) error {
		return f.svc.CancelUnsentConsumption(c, sub)
	})
	turns := service.NewSubscriptionTurnAdmission(ctx, f.sub)
	require.NoError(t, turns.Begin())
	first := turns.Current()
	ctxkey.MarkUpstreamDispatched(ctx)
	v2ApplyCommand(t, f, v2TestCommand(f, first, f.key.ID, 90))
	turns.End(true, false)
	require.ErrorIs(t, turns.Begin(), service.ErrDailyLimitExceeded, "a connected realtime client cannot reuse a completed admission to bypass shared quota")
	var requests int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM subscription_requests WHERE subscription_id=$1`, f.sub.ID).Scan(&requests))
	require.Equal(t, 1, requests)
	_, err := f.svc.AdminResetQuota(ctx, f.sub.ID, true, false, false)
	require.NoError(t, err)
	require.NoError(t, turns.Begin())
	require.NotEqual(t, first.AdmissionKey, turns.Current().AdmissionKey)
	turns.CloseUnsent()
	var pending int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM subscription_requests WHERE subscription_id=$1 AND status='admitted'`, f.sub.ID).Scan(&pending))
	require.Zero(t, pending)
}

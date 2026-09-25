//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func durableVideoFixture(t *testing.T) *service.GrokVideoTaskSnapshot {
	t.Helper()
	client := testEntClient(t)
	u := mustCreateUser(t, client, &service.User{Email: "video-" + uuid.NewString() + "@example.com", PasswordHash: "h", Balance: 10})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Name: "video", Key: "sk-video-" + uuid.NewString()})
	account := mustCreateAccount(t, client, &service.Account{Name: "video-" + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformGrok})
	id := uuid.NewString()
	registerBillingFixtureCleanup(t, u.ID, account.ID)
	return &service.GrokVideoTaskSnapshot{Version: 1, TaskID: id, FinancialRequestID: service.StableGrokVideoBillingRequestID(id), UserID: u.ID, APIKeyID: key.ID, AccountID: account.ID, Pending: service.GrokVideoPendingBilling{Model: "grok-imagine-video", VideoResolution: "720p", VideoDurationSeconds: 8, CreatedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}, ChargePerUnit: "0.07", StandardPerUnit: "0.07", Unit: "second", ChargeBalance: true, Template: service.UsageBillingCommand{UserID: u.ID, APIKeyID: key.ID, AccountID: account.ID, AccountType: service.AccountTypeAPIKey, Model: "grok-imagine-video"}, Detail: service.UsageSettlementDetail{UserID: u.ID, APIKeyID: key.ID, AccountID: account.ID, Model: "grok-imagine-video", RequestedModel: "grok-imagine-video", RateMultiplier: 1, VideoCount: 1, BillingType: service.BillingTypeBalance}}
}
func TestDurableVideoIntegration_RestartConcurrentObservationChargesOnce(t *testing.T) {
	ctx := context.Background()
	r := &usageBillingRepository{db: integrationDB}
	s := durableVideoFixture(t)
	s.Detail.RouteBillingSnapshot = &service.RouteBillingSnapshot{BillingMode: "balance", ActualCost: 0, RawCost: 0, TargetGroupID: 1}
	require.NoError(t, r.StoreGrokVideoTask(ctx, s))
	restarted := &usageBillingRepository{db: integrationDB}
	loaded, err := restarted.LoadGrokVideoTask(ctx, s.TaskID, s.UserID, s.APIKeyID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	o := service.GrokVideoObservation{Status: "done", DurationSeconds: 8, CompletedAt: time.Now().UTC().Truncate(time.Microsecond)}
	cmd, log, err := service.BuildGrokVideoSettlement(loaded, o)
	require.NoError(t, err)
	require.Zero(t, loaded.Detail.RouteBillingSnapshot.ActualCost, "building settlement must not mutate the accepted SQL snapshot")
	require.InDelta(t, .56, log.RouteBillingSnapshot.ActualCost, 1e-10)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *cmd
			errs <- restarted.ObserveGrokVideoTask(ctx, loaded, "", o, &copy, log)
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	rows, err := restarted.ProcessPendingSettlements(ctx, 128)
	require.NoError(t, err)
	_ = rows
	var amount string
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, s.UserID).Scan(&amount))
	require.Equal(t, "9.44000000", amount)
	_, err = restarted.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, s.UserID).Scan(&amount))
	require.Equal(t, "9.44000000", amount)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, s.FinancialRequestID, s.APIKeyID).Scan(&count))
	require.Equal(t, 1, count)
}
func TestDurableVideoIntegration_FailedExpiredAndZeroNoDebit(t *testing.T) {
	r := &usageBillingRepository{db: integrationDB}
	ctx := context.Background()
	for _, status := range []string{"failed", "expired", "cancelled", "done"} {
		t.Run(status, func(t *testing.T) {
			s := durableVideoFixture(t)
			s.ChargePerUnit = "0"
			require.NoError(t, r.StoreGrokVideoTask(ctx, s))
			o := service.GrokVideoObservation{Status: status, DurationSeconds: 8, CompletedAt: time.Now().UTC()}
			var cmd *service.UsageBillingCommand
			var log *service.UsageLog
			var err error
			if status == "done" {
				cmd, log, err = service.BuildGrokVideoSettlement(s, o)
				require.NoError(t, err)
			}
			require.NoError(t, r.ObserveGrokVideoTask(ctx, s, "", o, cmd, log))
			_, err = r.ProcessPendingSettlements(ctx, 128)
			require.NoError(t, err)
			var balance float64
			require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, s.UserID).Scan(&balance))
			require.Equal(t, 10.0, balance)
		})
	}
}
func TestDurableVideoIntegration_ConflictAndStaleLease(t *testing.T) {
	r := &usageBillingRepository{db: integrationDB}
	ctx := context.Background()
	s := durableVideoFixture(t)
	require.NoError(t, r.StoreGrokVideoTask(ctx, s))
	changed := *s
	changed.ChargePerUnit = "99"
	require.ErrorIs(t, r.StoreGrokVideoTask(ctx, &changed), service.ErrUsageBillingRequestConflict)
	_, err := integrationDB.Exec(`UPDATE grok_video_tasks_geili SET lease_token='new-owner',lease_until=NOW()+INTERVAL '1 minute' WHERE task_id=$1 AND api_key_id=$2`, s.TaskID, s.APIKeyID)
	require.NoError(t, err)
	o := service.GrokVideoObservation{Status: "done", DurationSeconds: 8, CompletedAt: time.Now()}
	cmd, log, err := service.BuildGrokVideoSettlement(s, o)
	require.NoError(t, err)
	require.ErrorContains(t, r.ObserveGrokVideoTask(ctx, s, "stale", o, cmd, log), "stale")
	err = r.RetryGrokVideoTask(ctx, s, "stale", "error")
	require.NoError(t, err)
	var token string
	require.NoError(t, integrationDB.QueryRow(`SELECT lease_token FROM grok_video_tasks_geili WHERE task_id=$1 AND api_key_id=$2`, s.TaskID, s.APIKeyID).Scan(&token))
	require.Equal(t, "new-owner", token)
	require.NoError(t, r.ObserveGrokVideoTask(ctx, s, "new-owner", o, cmd, log), fmt.Sprint(o))
}

func TestDurableVideoIntegration_SeedanceReportedTokensReceipt(t *testing.T) {
	r := &usageBillingRepository{db: integrationDB}
	ctx := context.Background()
	s := durableVideoFixture(t)
	s.TaskID = "seedance:" + s.TaskID
	s.FinancialRequestID = service.StableGrokVideoBillingRequestID(s.TaskID)
	s.Unit = "output_token"
	s.ChargePerUnit = "0.000001"
	s.StandardPerUnit = "0.000002"
	require.NoError(t, r.StoreGrokVideoTask(ctx, s))
	n := 123456
	o := service.GrokVideoObservation{Status: "done", OutputTokens: &n, CompletedAt: time.Now()}
	cmd, log, err := service.BuildGrokVideoSettlement(s, o)
	require.NoError(t, err)
	require.NoError(t, r.ObserveGrokVideoTask(ctx, s, "", o, cmd, log))
	_, err = r.ProcessPendingSettlements(ctx, 128)
	require.NoError(t, err)
	var balance string
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, s.UserID).Scan(&balance))
	require.Equal(t, "9.87654400", balance)
	var tokens int
	require.NoError(t, integrationDB.QueryRow(`SELECT (detail->>'output_tokens')::int FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, s.FinancialRequestID, s.APIKeyID).Scan(&tokens))
	require.Equal(t, n, tokens)
}

func TestDurableVideoIntegration_LateSubscriptionCompletionUsesOriginalDay(t *testing.T) {
	ctx := context.Background()
	r := &usageBillingRepository{db: integrationDB}
	s := durableVideoFixture(t)
	c := testEntClient(t)
	group := mustCreateGroup(t, c, &service.Group{Name: "video-sub-" + uuid.NewString(), Platform: service.PlatformGrok, SubscriptionType: service.SubscriptionTypeSubscription})
	sub := mustCreateSubscription(t, c, &service.UserSubscription{UserID: s.UserID, GroupID: group.ID})
	t.Cleanup(func() { cleanupBillingFixture(t, s.UserID, []int64{s.AccountID}, []int64{group.ID}) })
	admitted := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	expires := admitted.Add(time.Hour)
	limit := 45.0
	var lotID int64
	err := integrationDB.QueryRow(`INSERT INTO user_subscription_entitlements(user_subscription_id,status,starts_at,expires_at,daily_limit_usd,lifetime_usage_usd,daily_window_start)VALUES($1,'expired',$2,$3,$4,0,$2)RETURNING id`, sub.ID, admitted.Add(-time.Minute), expires, limit).Scan(&lotID)
	require.NoError(t, err)
	lot := geilisub.Lot{ID: lotID, UserSubscriptionID: sub.ID, Status: "active", StartsAt: admitted.Add(-time.Minute), ExpiresAt: expires, DailyLimitUSD: &limit, DailyWindowStart: &admitted}
	raw, err := json.Marshal([]geilisub.Lot{lot})
	require.NoError(t, err)
	admission := "video-admit-" + uuid.NewString()
	_, err = integrationDB.Exec(`INSERT INTO subscription_requests(request_key,subscription_id,api_key_id,status,lots,admitted_at)VALUES($1,$2,$3,'admitted',$4,$5)`, admission, sub.ID, s.APIKeyID, string(raw), admitted)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE user_subscriptions SET status='expired',expires_at=$2 WHERE id=$1`, sub.ID, expires)
	require.NoError(t, err)
	s.ChargeBalance = false
	s.ChargeSubscription = true
	s.Template.SubscriptionID = &sub.ID
	s.Template.SubscriptionAdmissionKey = admission
	s.Template.BillingType = service.BillingTypeSubscription
	s.Detail.SubscriptionID = &sub.ID
	s.Detail.BillingType = service.BillingTypeSubscription
	require.NoError(t, r.StoreGrokVideoTask(ctx, s))
	subs := service.NewSubscriptionService(nil, NewUserSubscriptionRepository(c), nil, c, nil)
	defer subs.Stop()
	resumed, e := subs.ResumeMediaConsumption(ctx, s.TaskID, s.UserID, s.APIKeyID)
	require.NoError(t, e)
	require.Equal(t, sub.ID, resumed.ID)
	require.Equal(t, admission, resumed.AdmissionKey)
	require.True(t, resumed.MediaLookupAdmission)
	_, e = subs.ResumeMediaConsumption(ctx, s.TaskID, s.UserID+1, s.APIKeyID)
	require.ErrorIs(t, e, service.ErrSubscriptionNotFound)
	_, e = subs.ResumeMediaConsumption(ctx, s.TaskID, s.UserID, s.APIKeyID+1)
	require.ErrorIs(t, e, service.ErrSubscriptionNotFound)
	o := service.GrokVideoObservation{Status: "done", DurationSeconds: 8, CompletedAt: time.Now()}
	cmd, log, err := service.BuildGrokVideoSettlement(s, o)
	require.NoError(t, err)
	require.NoError(t, r.ObserveGrokVideoTask(ctx, s, "", o, cmd, log))
	_, err = r.ProcessPendingSettlements(ctx, 128)
	require.NoError(t, err)
	var amount, date string
	require.NoError(t, integrationDB.QueryRow(`SELECT charged_amount::text,accounting_date::text FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2 AND state='settled'`, s.FinancialRequestID, s.APIKeyID).Scan(&amount, &date))
	require.Equal(t, "0.5600000000", amount)
	require.Equal(t, admitted.In(time.FixedZone("Beijing", 8*3600)).Format("2006-01-02"), date)
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, s.UserID).Scan(&balance))
	require.Equal(t, 10.0, balance)
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDurableVideoSettlementFrozenPricingAndZero(t *testing.T) {
	s := &GrokVideoTaskSnapshot{Version: 1, TaskID: "test", FinancialRequestID: "grok-video:test", APIKeyID: 2, UserID: 1, AccountID: 3, Unit: "second", ChargePerUnit: "0.07", StandardPerUnit: "0.1", ChargeBalance: true, Template: UsageBillingCommand{UserID: 1, APIKeyID: 2, AccountID: 3}, Detail: UsageSettlementDetail{UserID: 1, APIKeyID: 2, AccountID: 3, Model: "video"}, Pending: GrokVideoPendingBilling{VideoDurationSeconds: 8, CreatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}}
	for _, tc := range []struct {
		unit, price string
		expected    float64
	}{{"second", "0.07", .56}, {"request", "0.07", .07}, {"second", "0", 0}} {
		s.Unit = tc.unit
		s.ChargePerUnit = tc.price
		cmd, log, err := BuildGrokVideoSettlement(s, GrokVideoObservation{Status: "done", DurationSeconds: 8, CompletedAt: time.Now()})
		require.NoError(t, err)
		require.Equal(t, tc.expected, cmd.BalanceCost)
		require.Equal(t, tc.expected, log.ActualCost)
		require.NotEmpty(t, cmd.RequestFingerprint)
	}
	_, _, err := BuildGrokVideoSettlement(s, GrokVideoObservation{Status: "pending"})
	require.Error(t, err)
}

type videoIngressRepo struct {
	GrokVideoTaskRepository
	failure bool
	task    *GrokVideoTaskSnapshot
}

func (r *videoIngressRepo) StoreGrokVideoTask(_ context.Context, s *GrokVideoTaskSnapshot) error {
	if r.failure {
		return errors.New("offline")
	}
	copy := *s
	r.task = &copy
	return nil
}
func TestDurableVideoIngressSurvivesSQLFailureAndRestart(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	repo := &videoIngressRepo{failure: true}
	s := &GrokVideoTaskSnapshot{Version: 1, TaskID: "accepted", FinancialRequestID: "grok-video:accepted", APIKeyID: 2, UserID: 1, AccountID: 3, Unit: "second", ChargePerUnit: "0.07", StandardPerUnit: "0.07"}
	require.NoError(t, persistGrokVideoIngress(context.Background(), cfg, repo, s))
	root, err := openGrokVideoIngress(cfg)
	require.NoError(t, err)
	file, err := root.Open(".")
	require.NoError(t, err)
	names, err := file.Readdirnames(-1)
	require.NoError(t, err)
	file.Close()
	root.Close()
	require.NotEmpty(t, names)
	fresh := &videoIngressRepo{}
	for i := 0; i < 3; i++ {
		require.NoError(t, replayGrokVideoIngress(context.Background(), cfg, fresh))
	}
	require.NotNil(t, fresh.task)
	require.Equal(t, "accepted", fresh.task.TaskID)
	raw, err := json.Marshal(fresh.task)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "authorization")
	require.NotContains(t, string(raw), "credentials")
	entries, err := os.ReadDir(cfg.Pricing.DataDir + "/" + usageSettlementIngressDir + "/video-tasks")
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasSuffix(e.Name(), ".json"))
	}
}

func TestDurableVideoSnapshotNeverContainsKeyCredentialsOrPrompt(t *testing.T) {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	cfg.Pricing.DataDir = t.TempDir()
	repo := &videoSnapshotBillingRepo{}
	svc := &OpenAIGatewayService{cfg: cfg, usageBillingRepo: repo, billingService: NewBillingService(nil, nil)}
	key := &APIKey{ID: 2, UserID: 1, Key: "sk-private", User: &User{ID: 1}, Group: &Group{RateMultiplier: 1, VideoRateIndependent: true, VideoRateMultiplier: 1}, BillingSource: BillingSourceBalance}
	account := &Account{ID: 3, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "supplier-private"}}
	ctx, err := svc.PrepareGrokVideoTaskContext(context.Background(), key, nil, account, GrokMediaRequestInfo{Model: "grok-imagine-video", Resolution: "720p", DurationSeconds: 8, Prompt: "secret prompt"}, "grok-imagine-video", PlatformGrok, time.Now(), GrokMediaEndpointVideosGenerations)
	require.NoError(t, err)
	contract := ctx.Value(grokVideoCreateContextKey{}).(grokVideoCreateContract)
	raw, err := json.Marshal(contract.snapshot)
	require.NoError(t, err)
	for _, secret := range []string{"sk-private", "supplier-private", "secret prompt"} {
		require.NotContains(t, string(raw), secret)
	}
	require.Equal(t, "second", contract.snapshot.Unit)
	require.Equal(t, "0.07", contract.snapshot.ChargePerUnit)
}

type videoSnapshotBillingRepo struct {
	UsageBillingRepository
	GrokVideoTaskRepository
}

func TestDurableVideoSeedanceUsesReportedTokensNotDuration(t *testing.T) {
	s := &GrokVideoTaskSnapshot{Version: 1, TaskID: "seedance:task", FinancialRequestID: "grok-video:seedance:task", APIKeyID: 2, UserID: 1, AccountID: 3, Unit: "output_token", ChargePerUnit: "0.000001", StandardPerUnit: "0.000002", ChargeBalance: true, Template: UsageBillingCommand{UserID: 1, APIKeyID: 2, AccountID: 3}, Detail: UsageSettlementDetail{UserID: 1, APIKeyID: 2, AccountID: 3, Model: "seedance-2.0"}}
	_, _, err := BuildGrokVideoSettlement(s, GrokVideoObservation{Status: "done", DurationSeconds: 60})
	require.ErrorContains(t, err, "reported completion tokens")
	for _, n := range []int{0, 123456} {
		cmd, log, err := BuildGrokVideoSettlement(s, GrokVideoObservation{Status: "done", DurationSeconds: 60, OutputTokens: &n, CompletedAt: time.Now()})
		require.NoError(t, err)
		require.InDelta(t, float64(n)*0.000001, cmd.BalanceCost, 1e-10)
		require.Equal(t, n, log.OutputTokens)
		require.Nil(t, log.VideoDurationSeconds)
		require.Zero(t, log.VideoCount)
	}
}

func TestDurableVideoDurationCanonicalAcrossHTTPAndWorker(t *testing.T) {
	for _, raw := range []string{`{"status":"done","video":{"url":"https://example.invalid/v","duration":"8"}}`, `{"status":"done","video":{"url":"https://example.invalid/v","duration":8.5}}`, `{"status":"done","video":{"url":"https://example.invalid/v","duration":1e30}}`} {
		_, err := observedGrokVideoDuration([]byte(raw))
		require.Error(t, err)
		require.Nil(t, ExtractGrokVideoBillingFromStatusBody([]byte(raw), nil, "task"))
	}
	raw := []byte(`{"status":"done","video":{"url":"https://example.invalid/v","duration":999}}`)
	n, err := observedGrokVideoDuration(raw)
	require.NoError(t, err)
	httpResult := ExtractGrokVideoBillingFromStatusBody(raw, nil, "task")
	require.NotNil(t, httpResult)
	require.Equal(t, n, httpResult.VideoDurationSeconds)
	require.Equal(t, VideoBillingMaxDurationSeconds, n)
}

type videoLookupBillingRepo struct {
	UsageBillingRepository
	*videoIngressRepo
}

func (r *videoLookupBillingRepo) LoadGrokVideoTask(_ context.Context, _ string, _ int64, _ int64) (*GrokVideoTaskSnapshot, error) {
	return nil, nil
}
func TestDurableVideoLookupFindsWALBeforeSQLReplay(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	offline := &videoIngressRepo{failure: true}
	s := &GrokVideoTaskSnapshot{Version: 1, TaskID: "accepted", FinancialRequestID: "grok-video:accepted", APIKeyID: 2, UserID: 1, AccountID: 3, ChargePerUnit: "0.07", StandardPerUnit: "0.07"}
	require.NoError(t, persistGrokVideoIngress(context.Background(), cfg, offline, s))
	live := &videoLookupBillingRepo{videoIngressRepo: &videoIngressRepo{}}
	svc := &OpenAIGatewayService{cfg: cfg, usageBillingRepo: live}
	got, err := svc.LoadDurableGrokVideoTask(context.Background(), "accepted", 1, 2)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "0.07", got.ChargePerUnit)
	require.NotNil(t, live.task)
	_, err = svc.LoadDurableGrokVideoTask(context.Background(), "accepted", 999, 2)
	require.ErrorIs(t, err, ErrUsageBillingRequestConflict)
}

func TestDurableVideoBuildPreservesImmutableRouteSnapshot(t *testing.T) {
	groupID := int64(9)
	source := &GrokVideoTaskSnapshot{Version: 1, TaskID: "accepted", FinancialRequestID: "grok-video:accepted", UserID: 15, APIKeyID: 15, AccountID: 6, Unit: "second", ChargePerUnit: "0.035", StandardPerUnit: "0.07", ChargeBalance: true, Template: UsageBillingCommand{UserID: 15, APIKeyID: 15, AccountID: 6, Platform: "grok"}, Detail: UsageSettlementDetail{UserID: 15, APIKeyID: 15, AccountID: 6, GroupID: &groupID, Model: "grok-imagine-video-1.5", RouteBillingSnapshot: &RouteBillingSnapshot{ActualCost: 0, RawCost: 0, EffectiveMultiplier: .5, TargetGroupID: 9}}}
	before, err := json.Marshal(source)
	require.NoError(t, err)
	cmd, log, err := BuildGrokVideoSettlement(source, GrokVideoObservation{Status: "done", DurationSeconds: 5, CompletedAt: time.Now()})
	require.NoError(t, err)
	after, err := json.Marshal(source)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	require.InDelta(t, .175, cmd.BalanceCost, 1e-12)
	require.InDelta(t, .175, log.RouteBillingSnapshot.ActualCost, 1e-12)
	require.InDelta(t, .35, log.RouteBillingSnapshot.RawCost, 1e-12)
	require.NotSame(t, source.Detail.RouteBillingSnapshot, log.RouteBillingSnapshot)
	again, second, err := BuildGrokVideoSettlement(source, GrokVideoObservation{Status: "done", DurationSeconds: 5, CompletedAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	twice, err := json.Marshal(source)
	require.NoError(t, err)
	require.Equal(t, string(before), string(twice))
	require.Equal(t, cmd.BalanceCost, again.BalanceCost)
	require.Equal(t, cmd.RequestFingerprint, again.RequestFingerprint)
	require.NotSame(t, log.RouteBillingSnapshot, second.RouteBillingSnapshot)
}

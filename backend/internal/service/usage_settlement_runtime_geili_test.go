//go:build unit

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

type runtimeSettlementRepo struct {
	UsageBillingRepository
	mu                   sync.Mutex
	order                []string
	prepareErr, applyErr error
	prepared             *UsageBillingCommand
	prepareLog           *UsageLog
	processFn            func(context.Context) ([]UsageSettlementApplied, error)
	deliveryFn           func(context.Context) (UsageSettlementDeliveryStats, error)
	onPrepare            func()
}

func (r *runtimeSettlementRepo) PrepareSettlement(ctx context.Context, c *UsageBillingCommand, l *UsageLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, "prepare")
	copy := *c
	r.prepared = &copy
	r.prepareLog = l
	if r.onPrepare != nil {
		r.onPrepare()
	}
	return r.prepareErr
}
func (r *runtimeSettlementRepo) Apply(ctx context.Context, c *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, "apply")
	return &UsageBillingApplyResult{Applied: true}, r.applyErr
}
func (r *runtimeSettlementRepo) ProcessPendingSettlements(ctx context.Context, _ int) ([]UsageSettlementApplied, error) {
	if r.processFn != nil {
		return r.processFn(ctx)
	}
	return nil, nil
}
func (r *runtimeSettlementRepo) DeliverSettledUsage(ctx context.Context, _ int) (UsageSettlementDeliveryStats, error) {
	if r.deliveryFn != nil {
		return r.deliveryFn(ctx)
	}
	return UsageSettlementDeliveryStats{}, nil
}
func (r *runtimeSettlementRepo) SettlementHealth(context.Context) (UsageSettlementHealth, error) {
	return UsageSettlementHealth{}, nil
}
func (r *runtimeSettlementRepo) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func runtimeBillingFixture() (*postUsageBillingParams, *UsageLog) {
	p := &postUsageBillingParams{Cost: &CostBreakdown{ActualCost: 1.25, TotalCost: 2.5}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2, Quota: 20, RateLimit5h: 10}, Account: &Account{ID: 3, Type: AccountTypeAPIKey}, Platform: PlatformOpenAI, APIKeyService: apiKeyQuotaUpdaterStub{}}
	l := &UsageLog{UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "runtime-request", Model: "gpt-5.1", InputTokens: 10, TotalCost: 2.5, ActualCost: 1.25, CreatedAt: time.Now()}
	return p, l
}

func TestSettlementRuntimePreparePrecedesApplyAndErrorsPreventDebit(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want []string
	}{{"prepared", nil, []string{"prepare", "apply"}}, {"prepare failed", errors.New("storage unavailable"), []string{"prepare"}}, {"identity conflict", ErrUsageBillingRequestConflict, []string{"prepare"}}} {
		t.Run(tc.name, func(t *testing.T) {
			p, l := runtimeBillingFixture()
			r := &runtimeSettlementRepo{prepareErr: tc.err}
			deps := &billingDeps{deferredService: &DeferredService{}}
			_, err := applyUsageBilling(context.Background(), l.RequestID, l, p, deps, r)
			if tc.err != nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, r.snapshot())
			require.True(t, p.DurableSettlement)
		})
	}
}

func TestSettlementRuntimeWALRetainsOutageAndNeverAppliesConflict(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prepareErr error
		apply      bool
	}{{"database outage", context.DeadlineExceeded, true}, {"conflict", ErrUsageBillingRequestConflict, false}} {
		t.Run(tc.name, func(t *testing.T) {
			p, l := runtimeBillingFixture()
			cfg := &config.Config{}
			cfg.Pricing.DataDir = t.TempDir()
			r := &runtimeSettlementRepo{prepareErr: tc.prepareErr, applyErr: context.DeadlineExceeded}
			r.onPrepare = func() {
				files, err := filepath.Glob(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, "*.json"))
				require.NoError(t, err)
				require.Len(t, files, 1, "fsync snapshot must exist before SQL Prepare")
			}
			_, err := applyUsageBilling(context.Background(), l.RequestID, l, p, &billingDeps{cfg: cfg, deferredService: &DeferredService{}}, r)
			require.Error(t, err)
			want := []string{"prepare"}
			if tc.apply {
				want = append(want, "apply")
			}
			require.Equal(t, want, r.snapshot())
			files, err := filepath.Glob(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, "*.json"))
			require.NoError(t, err)
			require.Len(t, files, 1)
			raw, err := os.ReadFile(files[0])
			require.NoError(t, err)
			require.Contains(t, string(raw), `"actual_cost":1.25`)
		})
	}
}

func TestSettlementRuntimeRecordUsageNeverWritesLegacyLogQueue(t *testing.T) {
	for _, gateway := range []string{"claude", "openai"} {
		for _, failed := range []bool{false, true} {
			t.Run(gateway+map[bool]string{true: "_apply_error", false: "_success"}[failed], func(t *testing.T) {
				logs := &openAIRecordUsageBestEffortLogRepoStub{}
				r := &runtimeSettlementRepo{}
				if failed {
					r.applyErr = errors.New("database unavailable")
				}
				var err error
				if gateway == "claude" {
					svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, r, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
					err = svc.RecordUsage(context.Background(), &RecordUsageInput{Result: &ForwardResult{RequestID: "durable-claude", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 20, OutputTokens: 5}, Duration: time.Second}, APIKey: &APIKey{ID: 2, Quota: 100}, User: &User{ID: 1}, Account: &Account{ID: 3}, APIKeyService: apiKeyQuotaUpdaterStub{}})
				} else {
					svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, r, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
					err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "durable-openai", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 20, OutputTokens: 5}, Duration: time.Second}, APIKey: &APIKey{ID: 2, Quota: 100, Group: &Group{RateMultiplier: 1}}, User: &User{ID: 1}, Account: &Account{ID: 3, Type: AccountTypeAPIKey}, APIKeyService: apiKeyQuotaUpdaterStub{}})
				}
				if failed {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, []string{"prepare", "apply"}, r.snapshot())
				require.Zero(t, logs.bestEffortCalls)
				require.Zero(t, logs.createCalls)
				require.NotNil(t, r.prepareLog)
				require.Positive(t, r.prepareLog.ActualCost)
			})
		}
	}
}

type runtimeCache struct {
	BillingCache
	balanceInvalid, rateInvalid, platformInvalid, arithmetic, reads, dirtyReads atomic.Int64
}

func (c *runtimeCache) InvalidateUserBalance(context.Context, int64) error {
	c.balanceInvalid.Add(1)
	return nil
}
func (c *runtimeCache) InvalidateAPIKeyRateLimit(context.Context, int64) error {
	c.rateInvalid.Add(1)
	return nil
}
func (c *runtimeCache) DeleteUserPlatformQuotaCache(context.Context, int64, string) error {
	c.platformInvalid.Add(1)
	return nil
}
func (c *runtimeCache) DeductUserBalance(context.Context, int64, float64) error {
	c.arithmetic.Add(1)
	return nil
}
func (c *runtimeCache) UpdateAPIKeyRateLimitUsage(context.Context, int64, float64) error {
	c.arithmetic.Add(1)
	return nil
}
func (c *runtimeCache) IncrUserPlatformQuotaUsageCache(context.Context, int64, string, float64, time.Duration, bool) error {
	c.arithmetic.Add(1)
	return nil
}
func (c *runtimeCache) GetUserBalance(context.Context, int64) (float64, error) {
	c.reads.Add(1)
	return 999, nil
}
func (c *runtimeCache) GetAPIKeyRateLimit(context.Context, int64) (*APIKeyRateLimitCacheData, error) {
	c.reads.Add(1)
	return &APIKeyRateLimitCacheData{}, nil
}
func (c *runtimeCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*UserPlatformQuotaCacheEntry, bool, error) {
	c.reads.Add(1)
	return &UserPlatformQuotaCacheEntry{SchemaVersion: 1}, true, nil
}
func (c *runtimeCache) PopDirtyUserPlatformQuotaKeys(context.Context, int) ([]UserPlatformQuotaKey, error) {
	c.dirtyReads.Add(1)
	return nil, nil
}

type runtimeQuotaRepo struct {
	UserPlatformQuotaRepository
	record *UserPlatformQuotaRecord
	err    error
	calls  int
}

func (r *runtimeQuotaRepo) GetByUserPlatform(context.Context, int64, string) (*UserPlatformQuotaRecord, error) {
	r.calls++
	return r.record, r.err
}

type runtimeUserRepo struct {
	UserRepository
	balance float64
	err     error
}

func (r *runtimeUserRepo) GetByID(context.Context, int64) (*User, error) {
	return &User{Balance: r.balance}, r.err
}

func TestSettlementRuntimeCacheFinalizationOnlyInvalidates(t *testing.T) {
	p, _ := runtimeBillingFixture()
	p.DurableSettlement = true
	cache := &runtimeCache{}
	svc := &BillingCacheService{cache: cache, cfg: &config.Config{}, cacheWriteChan: make(chan cacheWriteTask, 8)}
	deps := &billingDeps{billingCacheService: svc, deferredService: &DeferredService{}}
	for i := 0; i < 2; i++ {
		finalizePostUsageBilling(context.Background(), p, deps, &UsageBillingApplyResult{Applied: true})
	}
	require.Equal(t, int64(2), cache.balanceInvalid.Load())
	require.Equal(t, int64(2), cache.rateInvalid.Load())
	require.Equal(t, int64(2), cache.platformInvalid.Load())
	require.Zero(t, cache.arithmetic.Load())
	require.Empty(t, svc.cacheWriteChan)
}

func TestSettlementRuntimeAdmissionIgnoresStaleRedisAndFailsClosed(t *testing.T) {
	cache := &runtimeCache{}
	now := time.Now()
	daily := timezone.StartOfDay(now)
	limit := 1.0
	quota := &runtimeQuotaRepo{record: &UserPlatformQuotaRecord{DailyLimitUSD: &limit, DailyUsageUSD: 1, DailyWindowStart: &daily}}
	svc := &BillingCacheService{durableSettlement: true, cache: cache, userRepo: &runtimeUserRepo{balance: .25}, userPlatformQuotaRepo: quota, apiKeyRateLimitLoader: &simpleModeRateLimitLoaderStub{data: &APIKeyRateLimitData{Usage5h: 10, Window5hStart: &now}}}
	amount, err := svc.GetUserBalance(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, .25, amount)
	require.ErrorIs(t, svc.checkUserPlatformQuotaEligibility(context.Background(), 1, PlatformOpenAI), ErrUserPlatformDailyQuotaExhausted)
	require.ErrorIs(t, svc.checkAPIKeyRateLimits(context.Background(), &APIKey{ID: 2, RateLimit5h: 10}), ErrAPIKeyRateLimit5hExceeded)
	quota.err = errors.New("db offline")
	require.ErrorIs(t, svc.checkUserPlatformQuotaEligibility(context.Background(), 1, PlatformOpenAI), ErrBillingServiceUnavailable)
	svc.apiKeyRateLimitLoader = &simpleModeRateLimitLoaderStub{err: errors.New("db offline")}
	require.ErrorIs(t, svc.checkAPIKeyRateLimits(context.Background(), &APIKey{ID: 2, RateLimit5h: 10}), ErrBillingServiceUnavailable)
	require.Zero(t, cache.reads.Load())
}

func TestSettlementRuntimeProviderDisablesFlusherIncludingShutdown(t *testing.T) {
	cfg := &config.Config{}
	cfg.Database.UserPlatformQuotaFlusherEnabled = true
	cfg.Database.UserPlatformQuotaFlushBatchSize = 10
	cfg.Database.UserPlatformQuotaFlushIntervalMs = 1000
	cache := &runtimeCache{}
	wheel, err := NewTimingWheelService()
	require.NoError(t, err)
	defer wheel.Stop()
	f := ProvideUserPlatformQuotaUsageFlusher(cfg, cache, &runtimeQuotaRepo{}, wheel, &runtimeSettlementRepo{})
	f.Stop()
	require.Zero(t, cache.dirtyReads.Load(), "durable shutdown must never flush stale absolute snapshots")
}

func TestSettlementRuntimeWorkerIndependentDeliveryAndBoundedStop(t *testing.T) {
	blocked := make(chan struct{}, 4)
	var delivery atomic.Int64
	r := &runtimeSettlementRepo{processFn: func(ctx context.Context) ([]UsageSettlementApplied, error) {
		select {
		case blocked <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}, deliveryFn: func(context.Context) (UsageSettlementDeliveryStats, error) {
		delivery.Add(1)
		return UsageSettlementDeliveryStats{Delivered: 1}, nil
	}}
	w := NewUsageSettlementWorker(r, nil, nil)
	w.Start()
	w.Start()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("settlement workers did not start")
	}
	require.Eventually(t, func() bool { return delivery.Load() > 0 }, time.Second, time.Millisecond)
	done := make(chan struct{})
	go func() { w.Stop(); w.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker shutdown ignored cancellation")
	}
	require.False(t, w.running.Load())
	require.Positive(t, w.delivered.Load())
}

func TestSettlementRuntimeSimpleWindowCommandKeepsNominalCostWithoutMoney(t *testing.T) {
	p, l := runtimeBillingFixture()
	p.SimpleModeKeyRateLimitOnly = true
	r := &runtimeSettlementRepo{}
	_, err := applyUsageBilling(context.Background(), l.RequestID, l, p, &billingDeps{deferredService: &DeferredService{}}, r)
	require.NoError(t, err)
	require.Zero(t, r.prepared.BalanceCost)
	require.Zero(t, r.prepared.SubscriptionCost)
	require.Zero(t, r.prepared.APIKeyQuotaCost)
	require.Zero(t, r.prepared.AccountQuotaCost)
	require.Zero(t, r.prepared.PlatformQuotaCost)
	require.Equal(t, 1.25, r.prepared.APIKeyRateLimitCost)
	require.Equal(t, 2.5, r.prepareLog.TotalCost)
	// Repository tests separately prove the receipt/log charged amount becomes0,
	// while the command's1.25 window cost is applied once and standard2.5 remains.
}

func TestSettlementRuntimeOpenAIBillingDepsPreservesPersistentConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pricing.DataDir = t.TempDir()
	svc := &OpenAIGatewayService{cfg: cfg}
	require.Same(t, cfg, svc.billingDeps().cfg, "OpenAI must not bypass ingress WAL by dropping config")
	require.Same(t, cfg, (&GatewayService{cfg: cfg}).billingDeps().cfg)
}

func TestSettlementRuntimeActualGatewaysPersistWALWhenSQLUnavailable(t *testing.T) {
	for _, kind := range []string{"claude", "openai"} {
		t.Run(kind, func(t *testing.T) {
			logs := &openAIRecordUsageBestEffortLogRepoStub{}
			repo := &runtimeSettlementRepo{prepareErr: context.DeadlineExceeded, applyErr: context.DeadlineExceeded}
			cfg := &config.Config{}
			cfg.Default.RateMultiplier = 1
			cfg.Pricing.DataDir = t.TempDir()
			var err error
			if kind == "claude" {
				svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
				svc.cfg = cfg
				err = svc.RecordUsage(context.Background(), &RecordUsageInput{Result: &ForwardResult{RequestID: "final-observed-claude", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 100}, Duration: time.Second}, APIKey: &APIKey{ID: 2}, User: &User{ID: 1}, Account: &Account{ID: 3}, APIKeyService: apiKeyQuotaUpdaterStub{}})
			} else {
				svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				svc.cfg = cfg
				err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "final-observed-openai", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 100}, Duration: time.Second}, APIKey: &APIKey{ID: 2, Group: &Group{RateMultiplier: 1}}, User: &User{ID: 1}, Account: &Account{ID: 3, Type: AccountTypeAPIKey}, APIKeyService: apiKeyQuotaUpdaterStub{}})
			}
			require.Error(t, err)
			require.Equal(t, []string{"prepare", "apply"}, repo.snapshot())
			require.Zero(t, logs.createCalls)
			require.Zero(t, logs.bestEffortCalls)
			files, e := filepath.Glob(filepath.Join(cfg.Pricing.DataDir, usageSettlementIngressDir, "*.json"))
			require.NoError(t, e)
			require.Len(t, files, 1)
			data, e := os.ReadFile(files[0])
			require.NoError(t, e)
			require.Contains(t, string(data), `"input_tokens":1000`)
			require.Contains(t, string(data), `"output_tokens":100`)
		})
	}
}

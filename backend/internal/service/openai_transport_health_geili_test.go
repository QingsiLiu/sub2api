package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type transportHealthRepoGeili struct {
	schedulerTestOpenAIAccountRepo
	err    error
	writes int
	until  time.Time
}

func (r *transportHealthRepoGeili) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, _ string) error {
	r.writes++
	r.until = until
	return r.err
}

type transportHealthCacheGeili struct {
	TempUnschedCache
	mu       sync.Mutex
	state    *openAITransportHealthStateGeili
	err      error
	setUntil time.Time
	reads    int
	batches  int
}

func (c *transportHealthCacheGeili) RecordOpenAITransportFailureGeili(_ context.Context, id int64) (OpenAITransportHealthDecisionGeili, error) {
	if c.err != nil {
		return OpenAITransportHealthDecisionGeili{}, c.err
	}
	decision := c.state.record(id, time.Now(), nil)
	decision.generation = 0
	return decision, nil
}
func (c *transportHealthCacheGeili) ResetOpenAITransportFailuresGeili(_ context.Context, id int64) error {
	if c.err != nil {
		return c.err
	}
	c.state.success(id, time.Now())
	return nil
}
func (c *transportHealthCacheGeili) OpenAITransportBlockedUntilGeili(_ context.Context, id int64) (time.Time, error) {
	c.reads++
	if c.err != nil {
		return time.Time{}, c.err
	}
	return c.state.blockedUntil(id, time.Now()), nil
}
func (c *transportHealthCacheGeili) SetTempUnsched(_ context.Context, _ int64, state *TempUnschedState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until := time.Unix(state.UntilUnix, 0); until.After(c.setUntil) {
		c.setUntil = until
	}
	return c.err
}

func transportHealthContextGeili() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestOpenAITransportHealthGeiliLegacyAndAdvancedAvoidFailedSticky(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
			t.Run(advanced+"/"+accountType, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				group := int64(4)
				bad := Account{ID: 6300, Type: accountType, Platform: PlatformOpenAI, Priority: 1, Status: StatusActive, Schedulable: true, Concurrency: 10, GroupIDs: []int64{group}}
				good := bad
				good.ID, good.Priority, good.Type = 6254, 5, AccountTypeAPIKey
				repo := &transportHealthRepoGeili{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{bad, good}}, err: errors.New("database unavailable")}
				cache := &transportHealthCacheGeili{state: &openAITransportHealthStateGeili{entries: make(map[int64]openAITransportHealthEntryGeili)}}
				rls := newOpenAIAdvancedSchedulerRateLimitService(advanced)
				rls.tempUnschedCache = cache
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = true
				makeService := func() *OpenAIGatewayService {
					return &OpenAIGatewayService{accountRepo: repo, rateLimitService: rls, cfg: cfg,
						cache: &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session": bad.ID}}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
				}
				first, second := makeService(), makeService()
				for i := 0; i < 3; i++ {
					var failover *UpstreamFailoverError
					err := first.handleOpenAIUpstreamTransportError(context.Background(), transportHealthContextGeili(), &bad, errors.New("Post upstream: Bad Request"), false)
					require.ErrorAs(t, err, &failover)
				}
				require.Equal(t, 1, repo.writes)
				for _, svc := range []*OpenAIGatewayService{first, second} {
					selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &group, "", "session", "gpt-5.5", nil, OpenAIUpstreamTransportAny, false)
					require.NoError(t, err)
					require.Equal(t, good.ID, selection.Account.ID, "stale DB and sticky must not defeat local/shared transport health")
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
				}
			})
		}
	}
}

func TestOpenAITransportHealthGeiliBatchedBlockSurvivesCacheOutage(t *testing.T) {
	cache := &transportHealthCacheGeili{state: &openAITransportHealthStateGeili{entries: make(map[int64]openAITransportHealthEntryGeili)}}
	until := time.Now().Add(time.Minute)
	cache.state.record(6300, time.Now(), &OpenAITransportHealthDecisionGeili{Until: until})
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: cache}}
	stale := Account{ID: 6300, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	svc.prefetchOpenAITransportHealthGeili(withOpenAITransportSelectionGeili(context.Background()), []Account{stale})
	cache.err = errors.New("Redis unavailable after batch")
	require.True(t, svc.openAITransportBlockedGeili(withOpenAITransportSelectionGeili(context.Background()), &stale, false))
}

func TestOpenAITransportHealthGeiliSuccessResetAndLocalOutage(t *testing.T) {
	cache := &transportHealthCacheGeili{err: errors.New("redis unavailable")}
	repo := &transportHealthRepoGeili{err: errors.New("database unavailable")}
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{tempUnschedCache: cache}}
	account := &Account{ID: 6300, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for i := 0; i < 2; i++ {
		svc.observeOpenAITransportFailureGeili(context.Background(), account, "EOF")
	}
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-5.5", true, nil)
	for i := 0; i < 2; i++ {
		svc.observeOpenAITransportFailureGeili(context.Background(), account, "EOF")
	}
	require.Zero(t, repo.writes)
	svc.observeOpenAITransportFailureGeili(context.Background(), account, "EOF")
	require.Equal(t, 1, repo.writes)
	stale := &Account{ID: account.ID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedGeili(context.Background(), stale, "gpt-5.5"))
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-5.5", true, nil)
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedGeili(context.Background(), stale, "gpt-5.5"), "late success must not lift quarantine")
}

func TestOpenAITransportHealthGeiliLocalExpiryAndBound(t *testing.T) {
	h := &openAITransportHealthStateGeili{entries: make(map[int64]openAITransportHealthEntryGeili)}
	now := time.Now()
	h.record(1, now, nil)
	h.record(1, now, nil)
	require.EqualValues(t, 1, h.record(1, now.Add(OpenAITransportFailureTTLGeili), nil).Count)
	h.record(1, now.Add(OpenAITransportFailureTTLGeili), nil)
	decision := h.record(1, now.Add(OpenAITransportFailureTTLGeili), nil)
	require.True(t, decision.Tripped)
	require.False(t, h.blockedUntil(1, decision.Until.Add(-time.Nanosecond)).IsZero())
	require.True(t, h.blockedUntil(1, decision.Until).IsZero())
	require.EqualValues(t, 1, h.record(1, decision.Until, nil).Count)
	h.record(2, now, &OpenAITransportHealthDecisionGeili{Count: 3, Tripped: true, Until: now.Add(time.Minute)})
	recovered := h.record(2, now.Add(time.Minute), nil)
	require.EqualValues(t, 1, recovered.Count, "Redis trip mirrors reset streak for outage fallback")
	require.False(t, recovered.Tripped)
	for i := int64(2); i <= openAITransportHealthMaxEntriesGeili+20; i++ {
		h.record(i, now, nil)
	}
	require.Len(t, h.entries, openAITransportHealthMaxEntriesGeili)
}

func TestOpenAITransportHealthGeiliExemptionsAndLongerCooldown(t *testing.T) {
	for _, tt := range []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{"client canceled", context.Background(), context.Canceled},
		{"request deadline", func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(), context.DeadlineExceeded},
		{"plugin sent", context.Background(), &PluginTransportError{RequestSent: true, Message: "EOF"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			account := &Account{ID: 6300, Type: AccountTypeOAuth, Platform: PlatformOpenAI}
			for i := 0; i < 3; i++ {
				svc.handleOpenAIUpstreamTransportError(tt.ctx, transportHealthContextGeili(), account, tt.err, false)
			}
			require.True(t, svc.transportHealthStateGeili().blockedUntil(account.ID, time.Now()).IsZero())
		})
	}
	svc := &OpenAIGatewayService{accountRepo: &transportHealthRepoGeili{}}
	longer := time.Now().Add(20 * time.Minute)
	account := &Account{ID: 6300, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, TempUnschedulableUntil: &longer}
	for i := 0; i < 3; i++ {
		svc.observeOpenAITransportFailureGeili(context.Background(), account, "Bad Request")
	}
	require.Equal(t, longer, *account.TempUnschedulableUntil)
	require.Equal(t, longer, svc.accountRepo.(*transportHealthRepoGeili).until)
	require.Equal(t, longer, svc.transportHealthStateGeili().blockedUntil(account.ID, time.Now().Add(61*time.Second)))
}

func (c *transportHealthCacheGeili) OpenAITransportBlocksGeili(_ context.Context, ids []int64) (map[int64]time.Time, error) {
	c.batches++
	if c.err != nil {
		return nil, c.err
	}
	blocks := make(map[int64]time.Time)
	for _, id := range ids {
		blocks[id] = c.state.blockedUntil(id, time.Now())
	}
	return blocks, nil
}

func TestOpenAITransportHealthGeiliSelectionBatchesAndStopsAfterCacheError(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, outage := range []bool{false, true} {
			t.Run(advanced+"/"+fmt.Sprint(outage), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				accounts := make([]Account, 100)
				for i := range accounts {
					accounts[i] = Account{ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 10, GroupIDs: []int64{4}}
				}
				cache := &transportHealthCacheGeili{state: &openAITransportHealthStateGeili{entries: make(map[int64]openAITransportHealthEntryGeili)}}
				if outage {
					cache.err = errors.New("cache unavailable")
				}
				rls := newOpenAIAdvancedSchedulerRateLimitService(advanced)
				rls.tempUnschedCache = cache
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = true
				svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, rateLimitService: rls, cfg: cfg, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
				group := int64(4)
				selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &group, "", "", "gpt-5.5", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				require.Equal(t, 1, cache.batches)
				if outage {
					require.Zero(t, cache.reads)
				} else {
					require.LessOrEqual(t, cache.reads, 1)
				}
			})
		}
	}
}

type delayedTransportCacheGeili struct {
	*transportHealthCacheGeili
	entered chan struct{}
	release chan struct{}
	delay   bool
}

func (c *delayedTransportCacheGeili) RecordOpenAITransportFailureGeili(ctx context.Context, id int64) (OpenAITransportHealthDecisionGeili, error) {
	decision, err := c.transportHealthCacheGeili.RecordOpenAITransportFailureGeili(ctx, id)
	if c.delay {
		close(c.entered)
		<-c.release
	}
	return decision, err
}
func TestOpenAITransportHealthGeiliPendingTripSurvivesConcurrentFailure(t *testing.T) {
	h := &openAITransportHealthStateGeili{entries: make(map[int64]openAITransportHealthEntryGeili)}
	now := time.Now()
	h.record(6300, now, nil, true)
	h.record(6300, now, nil, true)
	third := h.record(6300, now, nil, true) // Redis is still in flight.
	require.True(t, third.Tripped)
	fourth := h.record(6300, now, nil, true)
	require.False(t, fourth.Tripped)
	committed := h.record(6300, now, &third) // Redis has failed.
	require.True(t, committed.Tripped)
	require.Equal(t, third.Until, h.blockedUntil(6300, now))

	// A successful completion invalidates a pending failure trip.
	h.success(6300, third.Until)
	require.False(t, h.record(6300, third.Until, &third).Tripped)
}

func TestOpenAITransportHealthGeiliOlderRedisReplyDoesNotReviveStreak(t *testing.T) {
	cache := &delayedTransportCacheGeili{transportHealthCacheGeili: &transportHealthCacheGeili{state: &openAITransportHealthStateGeili{entries: make(map[int64]openAITransportHealthEntryGeili)}}, entered: make(chan struct{}), release: make(chan struct{})}
	svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{tempUnschedCache: cache}}
	account := &Account{ID: 6300, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	svc.observeOpenAITransportFailureGeili(context.Background(), account, "EOF")
	cache.delay = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.observeOpenAITransportFailureGeili(context.Background(), account, "EOF")
	}()
	<-cache.entered
	svc.resetOpenAITransportHealthGeili(account)
	close(cache.release)
	<-done
	cache.delay = false
	cache.err = errors.New("Redis unavailable")
	svc.observeOpenAITransportFailureGeili(context.Background(), account, "EOF")
	require.True(t, svc.transportHealthStateGeili().blockedUntil(account.ID, time.Now()).IsZero())
	require.EqualValues(t, 1, svc.transportHealthStateGeili().entries[account.ID].count)
}
func TestOpenAITransportHealthGeiliExpiredSharedBudgetAndHTTP400(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), time.Second)
	require.True(t, BeginRequestRecovery(ctx))
	state := requestRecoveryFromContext(ctx)
	expired := time.Now().Add(time.Minute)
	state.now = func() time.Time { return expired }
	require.NoError(t, ctx.Err())
	require.False(t, RequestRecoveryAllowed(ctx))
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 6300, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for i := 0; i < 3; i++ {
		svc.handleOpenAIUpstreamTransportError(ctx, transportHealthContextGeili(), account, errors.New("connection refused"), false)
	}
	require.Empty(t, svc.transportHealthStateGeili().entries)
	for i := 0; i < 3; i++ {
		svc.handleOpenAIAccountUpstreamError(context.Background(), account, 400, nil, []byte(`{"error":{"code":"invalid_request_error","message":"bad input"}}`))
	}
	require.Empty(t, svc.transportHealthStateGeili().entries)
}

//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type partialUsageHandlerGeiliBillingRepo struct {
	service.UsageBillingRepository
	commands []*service.UsageBillingCommand
	balance  float64
}

func (r *partialUsageHandlerGeiliBillingRepo) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	copy := *cmd
	r.commands = append(r.commands, &copy)
	r.balance -= cmd.BalanceCost
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type partialUsageHandlerGeiliLogRepo struct {
	service.UsageLogRepository
	logs []*service.UsageLog
}

func (r *partialUsageHandlerGeiliLogRepo) Create(_ context.Context, row *service.UsageLog) (bool, error) {
	copy := *row
	r.logs = append(r.logs, &copy)
	return true, nil
}

type partialUsageHandlerGeiliUpstream struct {
	calls                 int
	firstHasContent       bool
	secondWaitForDeadline bool
}

type partialUsageDeadlineGeiliBody struct {
	*strings.Reader
	ctx context.Context
}

func (b *partialUsageDeadlineGeiliBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		<-b.ctx.Done()
		return 0, b.ctx.Err()
	}
	return n, err
}

func (b *partialUsageDeadlineGeiliBody) Close() error { return nil }

type recoveryBlockingSchedulerGeiliCache struct {
	*fakeSchedulerCache
	upstream *partialUsageHandlerGeiliUpstream
}

func (s *recoveryBlockingSchedulerGeiliCache) GetSnapshot(ctx context.Context, bucket service.SchedulerBucket) ([]*service.Account, bool, error) {
	if s.upstream.calls > 0 {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(300 * time.Millisecond):
			return nil, false, context.DeadlineExceeded
		}
	}
	return s.fakeSchedulerCache.GetSnapshot(ctx, bucket)
}

func (u *partialUsageHandlerGeiliUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_attempt\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3-5-sonnet\",\"usage\":{\"input_tokens\":11}}}\n\n"
	if u.firstHasContent || u.calls > 1 {
		body += "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"visible\"}}\n\n"
		body += "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":null},\"usage\":{\"output_tokens\":5}}\n\n"
	}
	if u.calls > 1 && !u.secondWaitForDeadline {
		body += "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	responseBody := io.ReadCloser(io.NopCloser(strings.NewReader(body)))
	if u.calls > 1 && u.secondWaitForDeadline {
		responseBody = &partialUsageDeadlineGeiliBody{Reader: strings.NewReader(body), ctx: req.Context()}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {fmt.Sprintf("attempt-%d", u.calls)}}, Body: responseBody}, nil
}

func (u *partialUsageHandlerGeiliUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

// Exercise the real ingress handler, protocol converter and billing command.
// A delivered partial answer must settle its observed usage once; an abandoned
// prelude attempt must not settle at all when a second account completes it.
func TestGatewayPartialUsageGeiliConvertedHandlersSettleExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, scenario := range []string{"partial-A", "recovered-B", "deadline-B"} {
			t.Run(path+"/"+scenario, func(t *testing.T) {
				partial := scenario == "partial-A"
				deadlineFailure := scenario == "deadline-B"
				groupID := int64(41)
				group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
				accounts := []*service.Account{}
				for _, id := range []int64{1, 2} {
					accounts = append(accounts, &service.Account{ID: id, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: int(id), Concurrency: 1, Credentials: map[string]any{"api_key": "test-token"}})
				}
				cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 1 << 20}, Default: config.DefaultConfig{RateMultiplier: 1}}
				billing := &partialUsageHandlerGeiliBillingRepo{balance: 10}
				logs := &partialUsageHandlerGeiliLogRepo{}
				upstream := &partialUsageHandlerGeiliUpstream{firstHasContent: partial, secondWaitForDeadline: deadlineFailure}
				snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: accounts}, nil, nil, nil, nil)
				gateway := service.NewGatewayService(nil, &fakeGroupRepo{group: group}, logs, billing, nil, nil, nil, nil, cfg, snapshot, nil, service.NewBillingService(cfg, nil), nil, nil, nil, upstream, service.NewDeferredService(nil, nil, time.Second), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				// Admission is unrelated to this settlement regression. Actual
				// pricing and money application above retain standard billing mode.
				admission := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
				t.Cleanup(admission.Stop)
				h := &GatewayHandler{gatewayService: gateway, billingCacheService: admission, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0), maxAccountSwitches: 3, cfg: cfg}
				body := `{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hello"}]}`
				if path == "/v1/responses" {
					body = `{"model":"claude-3-5-sonnet","stream":true,"input":"hello"}`
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				budget := time.Minute
				if deadlineFailure {
					budget = 25 * time.Millisecond
				}
				ctx := context.WithValue(service.WithRequestRecovery(context.Background(), budget), ctxkey.Group, group)
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
				c.Request.Header.Set("Content-Type", "application/json")
				key := &service.APIKey{ID: 31, UserID: 51, BillingSource: service.BillingSourceBalance, GroupID: &groupID, Group: group, User: &service.User{ID: 51, Balance: 10, Concurrency: 10}}
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})
				if path == "/v1/responses" {
					h.Responses(c)
				} else {
					h.ChatCompletions(c)
				}
				require.Contains(t, rec.Body.String(), "visible")
				require.Len(t, billing.commands, 1, "one logical request settles once")
				require.Len(t, logs.logs, 1)
				command := billing.commands[0]
				require.Equal(t, 11, command.InputTokens)
				require.Equal(t, 5, command.OutputTokens)
				require.InDelta(t, 0.000108, command.BalanceCost, 1e-10)
				require.InDelta(t, 10-command.BalanceCost, billing.balance, 1e-10)
				if partial {
					require.Equal(t, 1, upstream.calls, "never replay delivered content")
					require.Equal(t, "attempt-1", command.RequestID)
					require.Contains(t, rec.Body.String(), "Upstream response did not complete")
					require.NotContains(t, rec.Body.String(), "response.completed")
				} else {
					require.Equal(t, 2, upstream.calls)
					require.Equal(t, "attempt-2", command.RequestID, "failed prelude contributes no money command")
					if deadlineFailure {
						require.Contains(t, rec.Body.String(), "upstream_recovery_timeout")
						require.NotContains(t, rec.Body.String(), "response.completed")
					} else {
						require.NotContains(t, rec.Body.String(), "Upstream response did not complete")
					}
				}
			})
		}
	}
}

func TestGatewayPartialUsageGeiliRecoveryDeadlineBoundsAccountSelection(t *testing.T) {
	groupID := int64(41)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive}
	account := &service.Account{ID: 1, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test-token"}}
	upstream := &partialUsageHandlerGeiliUpstream{}
	scheduler := &recoveryBlockingSchedulerGeiliCache{fakeSchedulerCache: &fakeSchedulerCache{accounts: []*service.Account{account}}, upstream: upstream}
	serviceCfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 1 << 20}}
	gateway := service.NewGatewayService(nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, serviceCfg, service.NewSchedulerSnapshotService(scheduler, nil, nil, nil, nil), nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	admission := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
	t.Cleanup(admission.Stop)
	h := &GatewayHandler{gatewayService: gateway, billingCacheService: admission, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0), maxAccountSwitches: 3, cfg: serviceCfg}
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			upstream.calls = 0
			body := `{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hello"}]}`
			if path == "/v1/responses" {
				body = `{"model":"claude-3-5-sonnet","stream":true,"input":"hello"}`
			}
			root := service.WithRequestRecovery(context.Background(), 25*time.Millisecond)
			ctx := context.WithValue(root, ctxkey.Group, group)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
			key := &service.APIKey{ID: 31, UserID: 51, GroupID: &groupID, Group: group, User: &service.User{ID: 51, Balance: 10, Concurrency: 10}}
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})
			start := time.Now()
			if path == "/v1/responses" {
				h.Responses(c)
			} else {
				h.ChatCompletions(c)
			}
			require.Less(t, time.Since(start), 200*time.Millisecond, "Redis/DB selection cannot extend recovery beyond its shared budget")
			require.NoError(t, root.Err(), "the downstream client remains connected")
			require.Contains(t, rec.Body.String(), "upstream_recovery_timeout")
			require.NotEqual(t, statusClientClosedRequest, c.Writer.Status())
			require.Equal(t, 1, upstream.calls)
		})
	}
}

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

type openAIPartialUsageGeiliUpstream struct {
	calls    int
	scenario string
}

func (u *openAIPartialUsageGeiliUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	body := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-6-astra\",\"usage\":{\"input_tokens\":11,\"output_tokens\":0}}}\n\n"
	if u.scenario == "partial" || u.scenario == "cancel" || (u.scenario == "recovered" && u.calls > 1) {
		body += "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_partial\",\"output_index\":0,\"content_index\":0,\"delta\":\"visible\"}\n\n"
		body += "event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-6-astra\",\"usage\":{\"input_tokens\":11,\"output_tokens\":5}}}\n\n"
	}
	if u.scenario == "recovered" && u.calls > 1 {
		body += "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-6-astra\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"visible\"}]}],\"usage\":{\"input_tokens\":11,\"output_tokens\":5}}}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {fmt.Sprintf("attempt-%d", u.calls)}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *openAIPartialUsageGeiliUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

type openAIPartialUsageCancelWriterGeili struct {
	gin.ResponseWriter
	cancel context.CancelFunc
}

func (w *openAIPartialUsageCancelWriterGeili) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if strings.Contains(string(p), "visible") {
		w.cancel()
	}
	return n, err
}

// Use the real Responses ingress, native service reader, protocol guard,
// price calculation and money command. This prevents a service nil-result
// regression from silently deleting a partial/canceled generation's receipt.
func TestOpenAIResponsesPartialUsageGeiliHandlerSettlesOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []string{"partial", "cancel", "recovered", "all-prelude"} {
		t.Run(scenario, func(t *testing.T) {
			groupID := int64(41)
			group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1}
			accounts := []*service.Account{}
			for _, id := range []int64{1, 2} {
				accounts = append(accounts, &service.Account{ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: int(id), Concurrency: 1, Credentials: map[string]any{"api_key": "test-token", "base_url": "https://provider.example"}})
			}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 1 << 20}, Default: config.DefaultConfig{RateMultiplier: 1}}
			billing := &partialUsageHandlerGeiliBillingRepo{balance: 10}
			logs := &partialUsageHandlerGeiliLogRepo{}
			upstream := &openAIPartialUsageGeiliUpstream{scenario: scenario}
			snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: accounts}, nil, nil, nil, nil)
			gateway := service.NewOpenAIGatewayService(nil, logs, billing, nil, nil, nil, nil, cfg, snapshot, nil, service.NewBillingService(cfg, nil), nil, nil, upstream, service.NewDeferredService(nil, nil, time.Second), nil, nil, nil, nil, nil, nil, nil)
			admission := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
			t.Cleanup(admission.Stop)
			h := &OpenAIGatewayHandler{apiKeyService: &service.APIKeyService{}, gatewayService: gateway, billingCacheService: admission, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatNone, 0), maxAccountSwitches: 3, cfg: cfg}
			body := `{"model":"gpt-6-astra","stream":true,"input":"hello"}`
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			root, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := context.WithValue(service.WithRequestRecovery(root, time.Minute), ctxkey.Group, group)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			if scenario == "cancel" {
				c.Writer = &openAIPartialUsageCancelWriterGeili{ResponseWriter: c.Writer, cancel: cancel}
			}
			key := &service.APIKey{ID: 31, UserID: 51, BillingSource: service.BillingSourceBalance, GroupID: &groupID, Group: group, User: &service.User{ID: 51, Balance: 10, Concurrency: 10}}
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 10})
			h.Responses(c)
			if scenario == "all-prelude" {
				require.Len(t, billing.commands, 0)
				require.Len(t, logs.logs, 0)
				require.Equal(t, 2, upstream.calls)
				return
			}
			require.Contains(t, rec.Body.String(), "visible")
			require.Len(t, billing.commands, 1, "one admitted Python request creates one balance command")
			require.Len(t, logs.logs, 1)
			command := billing.commands[0]
			require.Equal(t, 11, command.InputTokens)
			require.Equal(t, 5, command.OutputTokens)
			require.InDelta(t, 0.00036, command.BalanceCost, 1e-10)
			require.InDelta(t, 10-command.BalanceCost, billing.balance, 1e-10)
			if scenario == "recovered" {
				require.Equal(t, 2, upstream.calls)
				require.Equal(t, "attempt-2", command.RequestID, "abandoned prelude has no receipt")
				require.Contains(t, rec.Body.String(), "response.completed")
			} else {
				require.Equal(t, 1, upstream.calls, "partial and canceled content must not restart another generation")
				require.Equal(t, "attempt-1", command.RequestID)
				require.NotContains(t, rec.Body.String(), "response.completed")
				if scenario == "partial" {
					require.Contains(t, rec.Body.String(), "response.failed")
				}
			}
		})
	}
}

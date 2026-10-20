//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type textRetryHandlerRepoGeili struct {
	openAIImagesFailoverAccountRepo
}

func (r textRetryHandlerRepoGeili) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return r.accounts, nil
}

type textRetryUpstreamGeili struct {
	service.HTTPUpstream
	mu       sync.Mutex
	calls    []int64
	bodies   [][]byte
	failures int
	cancel   context.CancelFunc
}

func (u *textRetryUpstreamGeili) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, id)
	body, _ := io.ReadAll(req.Body)
	u.bodies = append(u.bodies, body)
	status, response := 200, astraProSuccessResponse
	if len(u.calls) <= u.failures {
		status = 502
		if len(u.calls)%2 == 0 {
			status = 429
		}
		response = `{"error":{"type":"server_error","message":"relay temporarily unavailable"}}`
		if len(u.calls) == 2 && u.cancel != nil {
			time.AfterFunc(50*time.Millisecond, u.cancel)
		}
	} else if strings.Contains(req.URL.Path, "chat/completions") {
		response = `{"id":"chat_retry","object":"chat.completion","model":"gpt-6-astra","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
	}
	contentType := "application/json"
	if status == 200 && gjson.GetBytes(body, "stream").Bool() {
		contentType = "text/event-stream"
		response = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_astra\",\"model\":\"gpt-6-astra\"}}\n\n" +
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\",\"output_index\":0,\"content_index\":0}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + astraProSuccessResponse + "}\n\n"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func newTextRetryHandlerGeili(t *testing.T, upstream *textRetryUpstreamGeili, cooldown bool) *OpenAIGatewayHandler {
	t.Helper()
	accounts := make([]service.Account, 2)
	for i := range accounts {
		accounts[i] = service.Account{ID: int64(i + 1), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: i,
			Credentials: map[string]any{"api_key": "fixture", "base_url": "https://relay.example.test", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}},
			Extra:       map[string]any{"openai_responses_supported": true}}
		if cooldown {
			until := time.Now().Add(150 * time.Millisecond)
			accounts[i].RateLimitResetAt = &until
		}
	}
	repo := textRetryHandlerRepoGeili{openAIImagesFailoverAccountRepo{accounts: accounts}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.maxAccountSwitches = 10
	return h
}

func textRetryContextGeili(t *testing.T, path string, ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	body := `{"model":"gpt-6-astra","input":"hello","stream":false}`
	if strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/messages") {
		body = `{"model":"gpt-6-astra","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":false}`
	}
	c, rec := newAstraProFailoverContext(t, body)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "cc-global-switch/0.159.2")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 100, Concurrency: 0})
	key, _ := middleware.GetAPIKeyFromContext(c)
	key.Group.AllowMessagesDispatch = true
	return c, rec
}

func TestOpenAITextRetryGeiliWholePoolFailureThenRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			u := &textRetryUpstreamGeili{failures: 2}
			h := newTextRetryHandlerGeili(t, u, false)
			c, rec := textRetryContextGeili(t, path, service.WithRequestRecovery(context.Background(), 5*time.Second))
			switch path {
			case "/v1/responses":
				h.Responses(c)
			case "/v1/chat/completions":
				h.ChatCompletions(c)
			default:
				h.Messages(c)
			}
			require.Equal(t, 200, rec.Code, rec.Body.String())
			require.Len(t, u.calls, 3, "same client request must outlive one completely failed pool round")
			require.NotEqual(t, u.calls[0], u.calls[1], "first round must try distinct accounts")
			for _, body := range u.bodies {
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
			}
		})
	}
}

func TestOpenAITextRetryGeiliInitiallyCoolingPoolWaits(t *testing.T) {
	u := &textRetryUpstreamGeili{}
	h := newTextRetryHandlerGeili(t, u, true)
	c, rec := textRetryContextGeili(t, "/v1/responses", service.WithRequestRecovery(context.Background(), 3*time.Second))
	h.Responses(c)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Len(t, u.calls, 1)
}

func TestOpenAITextRetryGeiliCancellationAndDeadlineStopReplay(t *testing.T) {
	for _, cancelClient := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "client_cancel"}[cancelClient], func(t *testing.T) {
			ctx, cancel := context.WithCancel(service.WithRequestRecovery(context.Background(), 250*time.Millisecond))
			defer cancel()
			u := &textRetryUpstreamGeili{failures: 100}
			if cancelClient {
				ctx = service.WithRequestRecovery(context.Background(), 5*time.Second)
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				u.cancel = cancel
			}
			h := newTextRetryHandlerGeili(t, u, false)
			c, rec := textRetryContextGeili(t, "/v1/responses", ctx)
			h.Responses(c)
			require.Len(t, u.calls, 2, "never issue a third upstream attempt after cancellation/budget exhaustion")
			if !cancelClient {
				require.Equal(t, 504, rec.Code)
				require.Equal(t, 1, strings.Count(rec.Body.String(), "upstream_recovery_timeout"))
			}
		})
	}
}

func TestOpenAITextRetryGeiliNeverReplaysVisibleOutputOrFinalResponse(t *testing.T) {
	for _, committed := range []bool{false, true} {
		c, _ := textRetryContextGeili(t, "/v1/responses", service.WithRequestRecovery(context.Background(), time.Second))
		if committed {
			service.MarkResponseCommitted(c)
		} else {
			_, _ = c.Writer.WriteString("already delivered answer")
		}
		state := newOpenAITextRetryStateGeili(true)
		require.Equal(t, openAITextRetryNoneGeili, state.recover(c, nil, nil, "gpt-6-astra", service.PlatformOpenAI, nil, "", service.ErrNoAvailableAccounts))
	}
}

//go:build unit

package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var geiliToolProtocolHTTPRequests = []struct {
	path, body string
}{
	{"responses", `{"model":"gpt-6-astra","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"reasoning":{"effort":"high"}}`},
	{"chat/completions", `{"model":"gpt-6-astra","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"reasoning_effort":"high"}`},
	{"messages", `{"model":"gpt-6-astra","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"output_config":{"effort":"high"}}`},
}

func TestGeiliToolProtocolHTTPRoutesToResponsesAccount(t *testing.T) {
	for _, tc := range geiliToolProtocolHTTPRequests {
		for _, stream := range []bool{false, true} {
			for _, alias := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/alias=%t", tc.path, stream, alias), func(t *testing.T) {
					answer := astra200()
					if stream || tc.path != "responses" {
						answer.Header.Set("Content-Type", "text/event-stream")
						answer.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + astraProSuccessResponse + "}\n\n"))
					}
					upstream := newAstraProCapturedUpstream(answer)
					channel := service.NewChannelService(geiliToolProtocolChannelRepo{}, nil, nil, nil, nil)
					h := newGeiliToolProtocolHandler(t, upstream, true, channel)
					h.maxAccountSwitches = 0
					body, err := sjson.Set(tc.body, "stream", stream)
					require.NoError(t, err)
					if alias {
						body, err = sjson.Set(body, "model", "public-astra")
						require.NoError(t, err)
					}
					c, rec := newGeiliToolProtocolContext(tc.path, body)
					callGeiliToolProtocolHandler(h, c, tc.path)
					urls, ids, bodies := upstream.snapshot()
					require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
					require.Equal(t, []int64{2}, ids, "Chat-only account must be skipped without consuming failover budget")
					require.Equal(t, []string{"https://api.openai.com/v1/responses"}, urls)
					require.Equal(t, "gpt-6-astra", gjson.GetBytes(bodies[0], "model").String())
					require.Equal(t, "high", gjson.GetBytes(bodies[0], "reasoning.effort").String())
					require.Equal(t, "lookup", gjson.GetBytes(bodies[0], "tools.0.name").String())
					if stream {
						require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
					}
				})
			}
		}
	}
}

func TestGeiliToolProtocolHTTPNoCapableAccountMakesNoUpstreamCall(t *testing.T) {
	for _, tc := range geiliToolProtocolHTTPRequests {
		t.Run(tc.path, func(t *testing.T) {
			upstream := newAstraProCapturedUpstream(astra200())
			h := newGeiliToolProtocolHandler(t, upstream, false)
			c, rec := newGeiliToolProtocolContext(tc.path, tc.body)
			callGeiliToolProtocolHandler(h, c, tc.path)
			_, ids, _ := upstream.snapshot()
			require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
			require.Equal(t, "Service temporarily unavailable", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
			require.Empty(t, ids)
		})
	}
}

func callGeiliToolProtocolHandler(h *OpenAIGatewayHandler, c *gin.Context, path string) {
	switch path {
	case "responses":
		h.Responses(c)
	case "chat/completions":
		h.ChatCompletions(c)
	case "messages":
		h.Messages(c)
	}
}

func newGeiliToolProtocolHandler(t *testing.T, upstream service.HTTPUpstream, includeResponses bool, channels ...*service.ChannelService) *OpenAIGatewayHandler {
	t.Helper()
	accounts := []service.Account{{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Priority: 0,
		Credentials: map[string]any{"api_key": "fixture-key"},
		Extra:       map[string]any{"openai_responses_supported": false},
	}}
	if includeResponses {
		accounts = append(accounts, service.Account{
			ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: 1,
			Credentials: map[string]any{"api_key": "fixture-key"},
			Extra:       map[string]any{"openai_responses_supported": true},
		})
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	var channel *service.ChannelService
	if len(channels) > 0 {
		channel = channels[0]
	}
	gateway := service.NewOpenAIGatewayService(
		geiliToolProtocolAccountRepo{openAIImagesFailoverAccountRepo{accounts: accounts}}, nil, nil, nil, nil, nil, nil,
		cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, channel, nil, nil, nil,
	)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	return NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
}

func newGeiliToolProtocolContext(path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	groupID := int64(3131)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: 99, GroupID: &groupID, User: &service.User{ID: 100},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowMessagesDispatch: true},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 0})
	return c, rec
}

type geiliToolProtocolChannelRepo struct{ service.ChannelRepository }

func (geiliToolProtocolChannelRepo) ListAll(context.Context) ([]service.Channel, error) {
	return []service.Channel{{ID: 1, Status: service.StatusActive, GroupIDs: []int64{3131}, ModelMapping: map[string]map[string]string{service.PlatformOpenAI: {"public-astra": "gpt-6-astra"}}}}, nil
}
func (geiliToolProtocolChannelRepo) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{3131: service.PlatformOpenAI}, nil
}

type geiliToolProtocolAccountRepo struct {
	openAIImagesFailoverAccountRepo
}

func (r geiliToolProtocolAccountRepo) ListModelAvailabilityCandidates(_ context.Context, _ *int64, platforms []string, _ bool) ([]service.Account, error) {
	var accounts []service.Account
	for _, platform := range platforms {
		accounts = append(accounts, r.accountsForPlatform(platform)...)
	}
	return accounts, nil
}

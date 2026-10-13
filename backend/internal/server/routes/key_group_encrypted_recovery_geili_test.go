package routes

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"net/http"
	"net/http/httptest"
	"testing"
)

type earlyEncryptedCacheGeili struct {
	service.GatewayCache
	records map[string]service.OpenAIEncryptedRouteGeili
}

func (s *earlyEncryptedCacheGeili) GetOpenAIEncryptedRoutesGeili(_ context.Context, _ int64, digests []string) (map[string]service.OpenAIEncryptedRouteGeili, error) {
	result := map[string]service.OpenAIEncryptedRouteGeili{}
	for _, d := range digests {
		if r, ok := s.records[d]; ok {
			result[d] = r
		}
	}
	return result, nil
}
func (s *earlyEncryptedCacheGeili) SetOpenAIEncryptedRoutesGeili(_ context.Context, _ int64, records []service.OpenAIEncryptedRouteGeili) error {
	for _, r := range records {
		s.records[r.Digest] = r
	}
	return nil
}
func earlyEncryptedOwnerContextGeili(t *testing.T, body []byte) (context.Context, *service.OpenAIEncryptedRouteGeili) {
	t.Helper()
	cache := &earlyEncryptedCacheGeili{records: map[string]service.OpenAIEncryptedRouteGeili{}}
	cfg := &config.Config{}
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	key := &service.APIKey{ID: 10, UserID: 3885}
	base := service.WithOpenAIEncryptedRoutePolicyGeili(context.Background(), service.OpenAIEncryptedRouteSettingsGeili{Mode: "protect", ProtectedUserIDs: []int64{3885}, ReasoningRecoveryEnabled: true})
	ctx, _, err := gateway.PrepareOpenAIEncryptedTurnGeili(base, key, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	source := int64(128)
	account := &service.Account{ID: 10, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-only", "base_url": "https://synthetic.example"}}
	ctx = service.WithOpenAIEncryptedActualOutputOriginGeili(ctx, &source, account, "gpt-6-astra")
	service.ObserveOpenAIEncryptedOutputGeili(ctx, []byte(`{"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"known-cipher","summary":[]}}`))
	ctx, owner, err := gateway.PrepareOpenAIEncryptedTurnGeili(base, key, body)
	require.NoError(t, err)
	require.NotNil(t, owner)
	return ctx, owner
}
func earlyEncryptedAuthorizedGeili() []service.CompositeRouteDecision {
	available := int64(131)
	return []service.CompositeRouteDecision{{TargetGroupID: &available, TargetPlatform: service.PlatformOpenAI, UpstreamModel: "gpt-6-astra"}}
}
func TestEarlyEncryptedRouteRecoveryUsesOnlyFrozenAuthorizedCandidatesGeili(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","nonce":9007199254740993,"input":[{"type":"reasoning","id":"rs_old","encrypted_content":"known-cipher","summary":[{"type":"summary_text","text":"keep progress"}]},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"retained result"}]}`)
	ctx, owner := earlyEncryptedOwnerContextGeili(t, body)
	candidates := earlyEncryptedAuthorizedGeili()
	authorized := append([]service.CompositeRouteDecision(nil), candidates...)
	next, selected, err := recoverUnavailableEncryptedRouteGeili(ctx, body, candidates, authorized, owner)
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, int64(131), *selected[0].TargetGroupID)
	require.False(t, service.OpenAIEncryptedRoutePinnedGeili(ctx))
	require.False(t, gjson.GetBytes(next, "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(next, "input.0.id").Exists())
	require.Equal(t, "keep progress", gjson.GetBytes(next, "input.0.summary.0.text").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(next, "nonce").Raw)
	for _, path := range []string{"input.1", "input.2"} {
		require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(next, path).Raw)
	}
	require.Equal(t, "known-cipher", gjson.GetBytes(body, "input.0.encrypted_content").String(), "ingress body remains immutable")
	selected[0].TargetPlatform = service.PlatformAnthropic
	require.Equal(t, service.PlatformOpenAI, authorized[0].TargetPlatform, "no alias to the frozen forced-platform subset")
	_, _, err = recoverUnavailableEncryptedRouteGeili(ctx, body, candidates, authorized, owner)
	require.Error(t, err, "cannot claim another recovery in the same client request")
}
func TestEarlyEncryptedRouteRecoveryRejectsOpaqueOrUnknownHistoryGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, input := range []string{
		`[{"type":"compaction","encrypted_content":"known-cipher"}]`,
		`[{"type":"function_call_output","call_id":"call_1","output":[{"type":"encrypted_content","encrypted_content":"known-cipher"}]}]`,
		`[{"type":"reasoning","encrypted_content":"known-cipher"},{"type":"reasoning","encrypted_content":"unknown-cipher"}]`,
	} {
		t.Run(input, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-astra","input":` + input + `}`)
			ctx, owner := earlyEncryptedOwnerContextGeili(t, body)
			authorized := earlyEncryptedAuthorizedGeili()
			next, selected, err := recoverUnavailableEncryptedRouteGeili(ctx, body, authorized, authorized, owner)
			require.Error(t, err)
			require.Empty(t, selected)
			require.Equal(t, body, next)
			require.True(t, service.OpenAIEncryptedRoutePinnedGeili(ctx))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			(&handler.OpenAIGatewayHandler{}).WriteOpenAIEncryptedRouteErrorGeili(c, err)
			require.Equal(t, 409, rec.Code)
			require.Equal(t, "encrypted_session_route_conflict", gjson.Get(rec.Body.String(), "error.code").String())
		})
	}
}
func TestEarlyEncryptedRouteRecoveryCannotInventAuthorizationGeili(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"known-cipher","summary":[]}]}`)
	ctx, owner := earlyEncryptedOwnerContextGeili(t, body)
	next, selected, err := recoverUnavailableEncryptedRouteGeili(ctx, body, nil, nil, owner)
	require.Error(t, err)
	require.Equal(t, body, next)
	require.Empty(t, selected)
	require.True(t, service.OpenAIEncryptedRoutePinnedGeili(ctx), "absence of authorization must not consume the recovery claim")
	authorized := earlyEncryptedAuthorizedGeili()
	_, selected, err = recoverUnavailableEncryptedRouteGeili(ctx, body, authorized, authorized, owner)
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, int64(131), *selected[0].TargetGroupID)
}

package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type runtimeEncryptedCaptureCacheGeili struct{ *earlyEncryptedCacheGeili }

func (*runtimeEncryptedCaptureCacheGeili) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

type runtimeEncryptedSettingsGeili struct{ service.SettingRepository }

func (s *runtimeEncryptedSettingsGeili) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	values := map[string]string{}
	for _, key := range keys {
		value, err := s.GetValue(ctx, key)
		if err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}
func (*runtimeEncryptedSettingsGeili) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyOpenAIEncryptedRouteGeili {
		return `{"mode":"protect","protected_user_ids":[9001],"reasoning_recovery_enabled":true}`, nil
	}
	return "", nil
}

type runtimeEncryptedHTTPGeili struct{ service.HTTPUpstream }

func (*runtimeEncryptedHTTPGeili) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body := `{"id":"resp_seed","object":"response","status":"completed","model":"gpt-6-astra","output":[{"type":"function_call_output","call_id":"call_seed","output":[{"type":"encrypted_content","encrypted_content":"seed-tool-cipher"}]},{"type":"reasoning","encrypted_content":"seed-reasoning-cipher","summary":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"seed delivered"}]}],"usage":{"input_tokens":1000,"output_tokens":100}}`
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}
func TestEncryptedHTTPRuntimeConstructorAndCompositeWriterCaptureGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			cache := &runtimeEncryptedCaptureCacheGeili{earlyEncryptedCacheGeili: &earlyEncryptedCacheGeili{records: map[string]service.OpenAIEncryptedRouteGeili{}}}
			cfg := &config.Config{}
			settings := service.NewSettingService(&runtimeEncryptedSettingsGeili{}, cfg)
			gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, &runtimeEncryptedHTTPGeili{}, nil, nil, nil, nil, nil, nil, settings, nil)
			group := int64(131)
			key := &service.APIKey{ID: 2001, UserID: 9001, GroupID: &group, Group: &service.Group{ID: group, Platform: service.PlatformOpenAI}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Set("api_key", key)
			_, err := gateway.PrepareOpenAIEncryptedRouteGeili(c, key, []byte(`{"model":"gpt-6-astra","input":[{"role":"user","content":"seed"}],"stream":false}`))
			require.NoError(t, err)
			writer := newKeyRouteWriter(c.Writer)
			writer.ctx = c
			c.Writer = writer
			account := &service.Account{ID: 3001, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 10, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://synthetic.example/fixture", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}, Extra: map[string]any{"openai_responses_mode": "force_responses", "openai_passthrough": passthrough}}
			c.Request = c.Request.WithContext(service.WithOpenAIEncryptedOutputOriginGeili(c.Request.Context(), key.GroupID, account, "gpt-6-astra"))
			_, err = gateway.Forward(c.Request.Context(), c, account, []byte(`{"model":"gpt-6-astra","input":[{"role":"user","content":"seed"}],"stream":false}`))
			require.NoError(t, err)
			writer.commit()
			require.Contains(t, rec.Body.String(), "seed-tool-cipher")
			require.Len(t, cache.records, 2)
			for _, r := range cache.records {
				require.Equal(t, int64(3001), r.AccountID)
				require.Equal(t, int64(131), r.GroupID)
				require.Equal(t, "gpt-6-astra", r.Model)
			}
		})
	}
}

type failingEncryptedRuntimeWriterGeili struct{ gin.ResponseWriter }

func (*failingEncryptedRuntimeWriterGeili) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (w *failingEncryptedRuntimeWriterGeili) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}
func TestEncryptedHTTPRuntimeFailedWriteDoesNotRecordGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &runtimeEncryptedCaptureCacheGeili{earlyEncryptedCacheGeili: &earlyEncryptedCacheGeili{records: map[string]service.OpenAIEncryptedRouteGeili{}}}
	cfg := &config.Config{}
	settings := service.NewSettingService(&runtimeEncryptedSettingsGeili{}, cfg)
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, &runtimeEncryptedHTTPGeili{}, nil, nil, nil, nil, nil, nil, settings, nil)
	group := int64(131)
	key := &service.APIKey{ID: 2001, UserID: 9001, GroupID: &group, Group: &service.Group{ID: group, Platform: service.PlatformOpenAI}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("api_key", key)
	body := []byte(`{"model":"gpt-6-astra","input":"seed","stream":false}`)
	_, err := gateway.PrepareOpenAIEncryptedRouteGeili(c, key, body)
	require.NoError(t, err)
	writer := newKeyRouteWriter(&failingEncryptedRuntimeWriterGeili{ResponseWriter: c.Writer})
	writer.ctx = c
	c.Writer = writer
	account := &service.Account{ID: 3001, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 10, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://synthetic.example/fixture"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
	c.Request = c.Request.WithContext(service.WithOpenAIEncryptedOutputOriginGeili(c.Request.Context(), &group, account, "gpt-6-astra"))
	_, err = gateway.Forward(c.Request.Context(), c, account, body)
	require.Error(t, err)
	require.Empty(t, cache.records, "no output was delivered to establish a replay source")
}

type runtimeEncryptedStreamHTTPGeili struct{ service.HTTPUpstream }

func (*runtimeEncryptedStreamHTTPGeili) Do(*http.Request, string, int64, int) (*http.Response, error) {
	body := `data: {"type":"response.output_text.delta","delta":"delivered"}` + "\n\n" +
		`data: {"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"undelivered-cipher","summary":[]}}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"resp_drained","status":"completed","usage":{"input_tokens":11,"output_tokens":7}}}` + "\n\n"
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

type disconnectingEncryptedRuntimeWriterGeili struct {
	gin.ResponseWriter
	writes int
}

func (w *disconnectingEncryptedRuntimeWriterGeili) Write(p []byte) (int, error) {
	if w.writes > 0 {
		return 0, io.ErrClosedPipe
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}
func (w *disconnectingEncryptedRuntimeWriterGeili) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}
func TestEncryptedHTTPRuntimeStreamDisconnectDrainsWithoutUndeliveredSourceGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &runtimeEncryptedCaptureCacheGeili{earlyEncryptedCacheGeili: &earlyEncryptedCacheGeili{records: map[string]service.OpenAIEncryptedRouteGeili{}}}
	cfg := &config.Config{}
	settings := service.NewSettingService(&runtimeEncryptedSettingsGeili{}, cfg)
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, &runtimeEncryptedStreamHTTPGeili{}, nil, nil, nil, nil, nil, nil, settings, nil)
	group := int64(131)
	key := &service.APIKey{ID: 2001, UserID: 9001, GroupID: &group, Group: &service.Group{ID: group, Platform: service.PlatformOpenAI}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("api_key", key)
	body := []byte(`{"model":"gpt-6-astra","input":"seed","stream":true}`)
	_, err := gateway.PrepareOpenAIEncryptedRouteGeili(c, key, body)
	require.NoError(t, err)
	writer := newKeyRouteWriter(&disconnectingEncryptedRuntimeWriterGeili{ResponseWriter: c.Writer})
	writer.ctx = c
	c.Writer = writer
	account := &service.Account{ID: 3001, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 10, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://synthetic.example/fixture"}, Extra: map[string]any{"openai_responses_mode": "force_responses", "openai_passthrough": true}}
	c.Request = c.Request.WithContext(service.WithOpenAIEncryptedOutputOriginGeili(c.Request.Context(), &group, account, "gpt-6-astra"))
	result, err := gateway.Forward(c.Request.Context(), c, account, body)
	require.NoError(t, err, "established streaming usage-drain contract remains successful")
	require.NotNil(t, result)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), "delivered")
	require.NotContains(t, rec.Body.String(), "undelivered-cipher")
	require.Empty(t, cache.records, "a cipher read only while draining cannot establish provenance")
}

type timeoutRuntimeEncryptedSettingsGeili struct{ runtimeEncryptedSettingsGeili }

func (s *timeoutRuntimeEncryptedSettingsGeili) GetValue(ctx context.Context, key string) (string, error) {
	if key == service.SettingKeyOpenAIEncryptedRouteGeili {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return s.runtimeEncryptedSettingsGeili.GetValue(ctx, key)
}
func TestEncryptedHTTPRuntimeColdPolicyTimeoutDeliversButDoesNotRecordGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			cache := &runtimeEncryptedCaptureCacheGeili{earlyEncryptedCacheGeili: &earlyEncryptedCacheGeili{records: map[string]service.OpenAIEncryptedRouteGeili{}}}
			cfg := &config.Config{}
			settings := service.NewSettingService(&timeoutRuntimeEncryptedSettingsGeili{}, cfg)
			gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, &runtimeEncryptedHTTPGeili{}, nil, nil, nil, nil, nil, nil, settings, nil)
			group := int64(131)
			key := &service.APIKey{ID: 2001, UserID: 9001, GroupID: &group, Group: &service.Group{ID: group, Platform: service.PlatformOpenAI}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Set("api_key", key)
			body := []byte(`{"model":"gpt-6-astra","input":"seed","stream":false}`)
			before := service.OpenAIEncryptedRouteSettingsRefreshFailuresGeili()
			_, err := gateway.PrepareOpenAIEncryptedRouteGeili(c, key, body)
			require.NoError(t, err)
			require.Equal(t, before+1, service.OpenAIEncryptedRouteSettingsRefreshFailuresGeili())
			require.Equal(t, "off", settings.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
			writer := newKeyRouteWriter(c.Writer)
			writer.ctx = c
			c.Writer = writer
			account := &service.Account{ID: 3001, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 10, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://synthetic.example/fixture"}, Extra: map[string]any{"openai_responses_mode": "force_responses", "openai_passthrough": passthrough}}
			c.Request = c.Request.WithContext(service.WithOpenAIEncryptedOutputOriginGeili(c.Request.Context(), &group, account, "gpt-6-astra"))
			_, err = gateway.Forward(c.Request.Context(), c, account, body)
			require.NoError(t, err)
			writer.commit()
			require.Contains(t, rec.Body.String(), "seed-tool-cipher")
			require.Empty(t, cache.records, "cold timeout falls back to off before source observation, despite successful delivery")
			require.Equal(t, before+1, service.OpenAIEncryptedRouteSettingsRefreshFailuresGeili(), "Forward does not refresh cached off policy")
		})
	}
}

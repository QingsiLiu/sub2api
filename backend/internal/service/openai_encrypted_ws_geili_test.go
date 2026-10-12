package service

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEncryptedWSTurnContextGeiliTracksFrozenTurn(t *testing.T) {
	type marker struct{}
	first := context.WithValue(context.Background(), marker{}, "first")
	second := context.WithValue(context.Background(), marker{}, "second")
	carrier := &openAIEncryptedWSTurnGeili{ctx: first}
	transport := context.WithValue(context.Background(), openAIEncryptedWSTurnKeyGeili{}, carrier)
	require.Equal(t, "first", encryptedWSContextGeili(transport).Value(marker{}))
	carrier.mu.Lock()
	carrier.ctx = second
	carrier.mu.Unlock()
	require.Equal(t, "second", encryptedWSContextGeili(transport).Value(marker{}))
	require.Equal(t, "first", first.Value(marker{}), "previous turn snapshot remains immutable")
}

type encryptedWSSequenceDialerGeili struct {
	conns []*stagedPassthroughConn
	calls atomic.Int32
}

func (d *encryptedWSSequenceDialerGeili) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	index := int(d.calls.Add(1)) - 1
	if index >= len(d.conns) {
		return nil, 502, nil, errOpenAIWSConnClosed
	}
	return d.conns[index], http.StatusSwitchingProtocols, http.Header{}, nil
}

func TestEncryptedWSPassthroughGeiliReconnectsSameAccountBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name, terminal, wantEvent string
		input, output             int
	}{
		{"success", `{"type":"response.completed","response":{"id":"resp_recovered","status":"completed","usage":{"input_tokens":5,"output_tokens":2}}}`, "response.completed", 5, 2},
		{"failed_again", `{"type":"response.failed","response":{"id":"resp_failed_again","error":{"code":"invalid_encrypted_content","message":"bad cipher"}}}`, "response.failed", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := newStagedPassthroughConn(), newStagedPassthroughConn()
			first.Send(`{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"bad cipher"}}}`)
			second.Send(tc.terminal)
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), first)
			dialer := &encryptedWSSequenceDialerGeili{conns: []*stagedPassthroughConn{first, second}}
			svc.openaiWSPassthroughDialer = dialer
			state := &openAIEncryptedRouteStateGeili{protected: true, recovery: true}
			ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
			turns := make(chan *OpenAIForwardResult, 4)
			server, _ := startPassthroughLifecycleServerWithHooks(t, ctx, svc, passthroughLifecycleAccount(), func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) { turns <- result }}
			})
			defer server.Close()
			clientCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(clientCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer client.CloseNow()
			body := []byte(`{"type":"response.create","model":"gpt-5.1","input":[{"type":"reasoning","id":"rs_old","encrypted_content":"cipher","summary":[{"type":"summary_text","text":"progress"}]}]}`)
			require.NoError(t, client.Write(clientCtx, coderws.MessageText, body))
			_, response, err := client.Read(clientCtx)
			require.NoError(t, err)
			require.Contains(t, string(response), tc.wantEvent)
			require.Equal(t, int32(2), dialer.calls.Load())
			select {
			case outbound := <-second.writes:
				require.NotContains(t, string(outbound), "encrypted_content")
				require.Contains(t, string(outbound), "progress")
			case <-clientCtx.Done():
				t.Fatal("missing recovered upstream request")
			}
			select {
			case result := <-turns:
				require.NotNil(t, result)
				require.Equal(t, tc.input, result.Usage.InputTokens)
				require.Equal(t, tc.output, result.Usage.OutputTokens)
			case <-clientCtx.Done():
				t.Fatal("missing final usage")
			}
			select {
			case <-turns:
				t.Fatal("failed attempt incorrectly settled")
			default:
			}

		})
	}
}

func TestEncryptedWSTurnContextGeiliConcurrentReaders(t *testing.T) {
	carrier := &openAIEncryptedWSTurnGeili{ctx: context.Background()}
	transport := context.WithValue(context.Background(), openAIEncryptedWSTurnKeyGeili{}, carrier)
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 100; j++ {
				require.NotNil(t, encryptedWSContextGeili(transport))
			}
		}()
	}
	for i := 0; i < 100; i++ {
		carrier.mu.Lock()
		carrier.ctx = context.Background()
		carrier.mu.Unlock()
	}
	readers.Wait()
}

func TestEncryptedWSRecoveryGeiliPreservesUnsafeState(t *testing.T) {
	failure := []byte(`{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"bad cipher"}}}`)
	reasoning := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"reasoning","id":"rs_old","encrypted_content":"cipher","summary":[{"type":"summary_text","text":"progress"}]}]}`)
	state := &openAIEncryptedRouteStateGeili{protected: true, recovery: true}
	ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
	_, ok := prepareOpenAIEncryptedWSRecoveryGeili(ctx, reasoning, []byte(`{"type":"error","error":{"code":"invalid_encrypted_content"}}`), false, OpenAIUsage{})
	require.False(t, ok, "bare error may precede a metered failed terminal")
	_, ok = prepareOpenAIEncryptedWSRecoveryGeili(ctx, reasoning, failure, true, OpenAIUsage{})
	require.False(t, ok, "any delivered frame prevents replay")
	_, ok = prepareOpenAIEncryptedWSRecoveryGeili(ctx, reasoning, failure, false, OpenAIUsage{InputTokens: 1})
	require.False(t, ok, "observed upstream usage prevents unmetered replay")
	next, ok := prepareOpenAIEncryptedWSRecoveryGeili(ctx, reasoning, failure, false, OpenAIUsage{})
	require.True(t, ok)
	require.Contains(t, string(next), "progress")
	require.NotContains(t, string(next), "encrypted_content")
	_, ok = prepareOpenAIEncryptedWSRecoveryGeili(ctx, reasoning, failure, false, OpenAIUsage{})
	require.False(t, ok, "same turn may recover only once")
}

func TestEncryptedWSFailureGeiliOnlyReclassifiesProtectedFailure(t *testing.T) {
	state := &openAIEncryptedRouteStateGeili{protected: true}
	ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
	payload := []byte(`{"type":"error","error":{"type":"upstream_error","message":"Encrypted function output content could not be decrypted or decoded."}}`)
	require.Equal(t, payload, normalizeOpenAIEncryptedWSFailureGeili(context.Background(), payload))
	updated := normalizeOpenAIEncryptedWSFailureGeili(ctx, payload)
	require.Contains(t, string(updated), `"code":"invalid_encrypted_content"`)
	item := []byte(`{"type":"response.output_item.done","item":{"encrypted_content":"cipher"}}`)
	require.Equal(t, item, normalizeOpenAIEncryptedWSFailureGeili(ctx, item))
}

func TestEncryptedWSAliasGeiliUsesMappedSourceAndMapsOnce(t *testing.T) {
	account := passthroughLifecycleAccount()
	group := int64(131)
	owner := &OpenAIEncryptedRouteGeili{AccountID: account.ID, GroupID: group, Model: "gpt-6-astra", BackendFingerprint: encryptedBackendFingerprintGeili(account)}
	state := &openAIEncryptedRouteStateGeili{protected: true, owner: owner, observed: map[string]bool{}}
	base := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
	c, _ := gin.CreateTestContext(nil)
	c.Set("api_key", &APIKey{ID: 4987, UserID: 3885, GroupID: &group})
	mappedCalls := 0
	hooks := &OpenAIWSIngressHooks{MapRequestModel: func(_ int, original string) (string, error) {
		mappedCalls++
		require.Equal(t, "public-astra", original)
		return "gpt-6-astra", nil
	}}
	ctx, wrapped, err := (&OpenAIGatewayService{}).withOpenAIEncryptedWSHooksGeili(base, c, nil, account, []byte(`{"type":"response.create","model":"public-astra","input":[]}`), hooks)
	require.NoError(t, err, "HTTP origin uses the real upstream model")
	model, err := wrapped.MapRequestModel(1, "public-astra")
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", model)
	require.Equal(t, 1, mappedCalls)
	require.NoError(t, wrapped.BeforeRequest(1, []byte(`{"model":"public-astra"}`), "public-astra"))
	require.Equal(t, 1, mappedCalls, "first-frame admission must reuse mapping and frozen source")
	active := encryptedRouteStateGeili(encryptedWSContextGeili(ctx))
	require.Equal(t, "gpt-6-astra", active.origin.Model, "WS origin remains compatible with HTTP capture")
	require.NoError(t, ValidateOpenAIEncryptedAccountGeili(encryptedWSContextGeili(ctx), account, "gpt-6-astra"))
}

func TestEncryptedWSAuthorizationGeiliRejectsBeforeDial(t *testing.T) {
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	dialer := &encryptedWSSequenceDialerGeili{conns: []*stagedPassthroughConn{upstream}}
	svc.openaiWSPassthroughDialer = dialer
	state := &openAIEncryptedRouteStateGeili{protected: true}
	ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
	group := int64(131)
	server, _ := startPassthroughLifecycleServerWithHooks(t, ctx, svc, passthroughLifecycleAccount(), func(c *gin.Context) *OpenAIWSIngressHooks {
		c.Set("api_key", &APIKey{ID: 4987, UserID: 3885, GroupID: &group})
		return &OpenAIWSIngressHooks{AuthorizeEncryptedTurn: func(context.Context, int, []byte, string) error {
			return &OpenAIEncryptedRouteErrorGeili{Status: 409, Code: "encrypted_session_route_conflict", Message: "group revoked"}
		}}
	})
	defer server.Close()
	clientCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(clientCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer client.CloseNow()
	require.NoError(t, client.Write(clientCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra"}`)))
	_, body, err := client.Read(clientCtx)
	require.NoError(t, err)
	require.Contains(t, string(body), "group revoked")
	require.Equal(t, int32(0), dialer.calls.Load())
	select {
	case <-upstream.writes:
		t.Fatal("revoked Key dispatched upstream")
	default:
	}
}

func TestEncryptedWSObserveGeiliKeepsMappingOrder(t *testing.T) {
	group := int64(131)
	state := &openAIEncryptedRouteStateGeili{}
	base := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
	c, _ := gin.CreateTestContext(nil)
	c.Set("api_key", &APIKey{ID: 4987, UserID: 3885, GroupID: &group})
	called := 0
	ctx, hooks, err := (&OpenAIGatewayService{}).withOpenAIEncryptedWSHooksGeili(base, c, nil, passthroughLifecycleAccount(), []byte(`{"model":"public-astra"}`), &OpenAIWSIngressHooks{MapRequestModel: func(int, string) (string, error) { called++; return "gpt-6-astra", nil }})
	require.NoError(t, err)
	require.Equal(t, 0, called, "unprotected admission must not move mapping before security checks")
	_, err = hooks.MapRequestModel(1, "public-astra")
	require.NoError(t, err)
	require.Equal(t, 1, called)
	require.Equal(t, "gpt-6-astra", encryptedRouteStateGeili(encryptedWSContextGeili(ctx)).origin.Model)
}

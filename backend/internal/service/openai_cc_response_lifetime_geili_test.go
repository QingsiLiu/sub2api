package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ccLifecycleUpstreamGeili struct {
	do func(*http.Request) (*http.Response, error)
}

func (u ccLifecycleUpstreamGeili) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(r)
}

func (u ccLifecycleUpstreamGeili) DoWithTLS(r *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, proxy, id, concurrency)
}

func ccLifecycleServiceGeili(upstream HTTPUpstream) *OpenAIGatewayService {
	return &OpenAIGatewayService{httpUpstream: upstream, cfg: &config.Config{
		Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			AllowInsecureHTTP: true, AllowPrivateHosts: true,
		}},
	}}
}

func ccLifecycleContextGeili(t *testing.T, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder, context.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	ctx := WithRequestRecovery(context.Background(), 5*time.Second)
	require.True(t, BeginRequestRecovery(ctx))
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)).WithContext(ctx)
	return c, r, ctx
}

func ccLifecycleAccountGeili(base string) *Account {
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "synthetic-test", "base_url": base}}
}

// A real HTTP response exposes headers before its body. The recovery deadline
// must stay live across the helper return until the caller closes that body.
func TestCCResponseLifetimeGeili_RecoveryHeadersBeforeDelayedBody(t *testing.T) {
	for _, test := range []struct {
		name   string
		stream bool
	}{
		{"buffered", false},
		{"streaming", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ccRecoveryHeadersBeforeDelayedBodyGeili(t, test.stream)
		})
	}
}

func ccRecoveryHeadersBeforeDelayedBodyGeili(t *testing.T, stream bool) {
	t.Helper()
	allowBody := make(chan struct{})
	wantBody := `{"complete":true}`
	if stream {
		wantBody = "data: " + wantBody + "\n\n"
	}
	var allowOnce sync.Once
	releaseBody := func() { allowOnce.Do(func() { close(allowBody) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := "application/json"
		if stream {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush response headers: %v", err)
			return
		}
		select {
		case <-allowBody:
			_, _ = io.WriteString(w, wantBody)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(releaseBody)
	var request *http.Request
	upstream := ccLifecycleUpstreamGeili{do: func(r *http.Request) (*http.Response, error) {
		request = r
		return server.Client().Do(r)
	}}
	c, _, ctx := ccLifecycleContextGeili(t, "/v1/chat/completions", nil)
	resp, err := ccLifecycleServiceGeili(upstream).sendCCUpstreamRequest(ctx, c, ccLifecycleAccountGeili(server.URL),
		server.URL+"/v1/chat/completions", []byte(`{"model":"gpt-4.1-mini"}`), stream, "synthetic-test", "", "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
	releaseBody()
	result, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "returning headers must not cancel the unread response body")
	require.Equal(t, wantBody, string(result))
	require.NoError(t, request.Context().Err())
	deadline, present := request.Context().Deadline()
	require.True(t, present)
	require.Equal(t, requestRecoveryFromContext(ctx).deadline, deadline)
	require.NoError(t, resp.Body.Close())
	require.ErrorIs(t, request.Context().Err(), context.Canceled)
	require.NoError(t, ctx.Err(), "closing one response must not cancel the client recovery state")
}

type ccDelayedBodyGeili struct {
	ctx    context.Context
	reader io.Reader
	ready  <-chan struct{}
	closed atomic.Int32
}

func (b *ccDelayedBodyGeili) Read(p []byte) (int, error) {
	select {
	case <-b.ready:
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.reader.Read(p)
}

func (b *ccDelayedBodyGeili) Close() error {
	b.closed.Add(1)
	return nil
}

// All three public protocol implementations share sendCCUpstreamRequest. Their
// complete response and usage must survive a live recovery deadline, not just
// an initial context whose release happens to be a no-op.
func TestCCResponseLifetimeGeili_AllCallersReadAfterHelper(t *testing.T) {
	for _, test := range []struct {
		path string
		body string
	}{
		{"/v1/chat/completions", `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"test"}]}`},
		{"/v1/responses", `{"model":"gpt-4.1-mini","input":"test"}`},
		{"/v1/messages", `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"test"}],"max_tokens":16}`},
	} {
		t.Run(test.path, func(t *testing.T) {
			ready := make(chan struct{})
			body := &ccDelayedBodyGeili{ready: ready, reader: strings.NewReader(
				`{"id":"synthetic","object":"chat.completion","model":"gpt-4.1-mini","choices":[{"index":0,"message":{"role":"assistant","content":"complete output"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)}
			upstream := ccLifecycleUpstreamGeili{do: func(r *http.Request) (*http.Response, error) {
				body.ctx = r.Context()
				// Body availability follows response headers; no bytes are cached
				// when the shared helper hands the response to its caller.
				time.AfterFunc(5*time.Millisecond, func() { close(ready) })
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
			}}
			c, rec, ctx := ccLifecycleContextGeili(t, test.path, []byte(test.body))
			svc := ccLifecycleServiceGeili(upstream)
			account := ccLifecycleAccountGeili("https://upstream.invalid/v1")
			var result *OpenAIForwardResult
			var err error
			switch test.path {
			case "/v1/chat/completions":
				result, err = svc.forwardAsRawChatCompletions(ctx, c, account, []byte(test.body), "")
			case "/v1/responses":
				result, err = svc.forwardResponsesViaRawChatCompletions(ctx, c, account, []byte(test.body))
			case "/v1/messages":
				result, err = svc.forwardAnthropicViaRawChatCompletions(ctx, c, account, []byte(test.body), "")
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 200, rec.Code)
			require.Contains(t, rec.Body.String(), "complete output")
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			require.ErrorIs(t, body.ctx.Err(), context.Canceled)
			require.EqualValues(t, 1, body.closed.Load())
			require.NoError(t, ctx.Err())
		})
	}
}

func TestCCResponseLifetimeGeili_TransportFailureReleasesContext(t *testing.T) {
	var request *http.Request
	upstream := ccLifecycleUpstreamGeili{do: func(r *http.Request) (*http.Response, error) {
		request = r
		return nil, context.Canceled
	}}
	c, _, ctx := ccLifecycleContextGeili(t, "/v1/chat/completions", nil)
	resp, err := ccLifecycleServiceGeili(upstream).sendCCUpstreamRequest(ctx, c, ccLifecycleAccountGeili("https://upstream.invalid"),
		"https://upstream.invalid/v1/chat/completions", []byte(`{"model":"gpt-4.1-mini"}`), false, "synthetic-test", "", "")
	require.Nil(t, resp)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, request)
	require.ErrorIs(t, request.Context().Err(), context.Canceled)
	require.NoError(t, ctx.Err())
}

func TestCCResponseLifetimeGeili_RequestBuildErrorNeverDispatches(t *testing.T) {
	var calls atomic.Int32
	upstream := ccLifecycleUpstreamGeili{do: func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not reach transport")
	}}
	c, _, ctx := ccLifecycleContextGeili(t, "/v1/chat/completions", nil)
	resp, err := ccLifecycleServiceGeili(upstream).sendCCUpstreamRequest(ctx, c, ccLifecycleAccountGeili("https://upstream.invalid"),
		"http://[invalid", []byte(`{"model":"gpt-4.1-mini"}`), false, "synthetic-test", "", "")
	require.Nil(t, resp)
	require.ErrorContains(t, err, "build upstream request")
	require.Zero(t, calls.Load())
	require.NoError(t, ctx.Err())
}

func TestCCResponseLifetimeGeili_DeadlineStillStopsDelayedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush response headers: %v", err)
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	var request *http.Request
	upstream := ccLifecycleUpstreamGeili{do: func(r *http.Request) (*http.Response, error) {
		request = r
		return server.Client().Do(r)
	}}
	c, _, _ := ccLifecycleContextGeili(t, "/v1/chat/completions", nil)
	ctx := WithRequestRecovery(context.Background(), 100*time.Millisecond)
	require.True(t, BeginRequestRecovery(ctx))
	c.Request = c.Request.WithContext(ctx)
	resp, err := ccLifecycleServiceGeili(upstream).sendCCUpstreamRequest(ctx, c, ccLifecycleAccountGeili(server.URL),
		server.URL+"/v1/chat/completions", []byte(`{"model":"gpt-4.1-mini"}`), false, "synthetic-test", "", "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
	_, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, context.DeadlineExceeded, "body close ownership must not remove the recovery timeout")
	require.ErrorIs(t, request.Context().Err(), context.DeadlineExceeded)
	require.NoError(t, ctx.Err())
}

type ccCloseTrackingBodyGeili struct {
	ctx      context.Context
	closeErr error
	closed   atomic.Int32
}

func (b *ccCloseTrackingBodyGeili) Read([]byte) (int, error) { return 0, io.EOF }

func (b *ccCloseTrackingBodyGeili) Close() error {
	if b.ctx.Err() != context.Canceled {
		return errors.New("Close must first release context to unblock an upstream reader")
	}
	b.closed.Add(1)
	return b.closeErr
}

func TestCCResponseLifetimeGeili_CloseOnceAndPreserveCloseError(t *testing.T) {
	want := errors.New("synthetic close failure")
	body := &ccCloseTrackingBodyGeili{closeErr: want}
	upstream := ccLifecycleUpstreamGeili{do: func(r *http.Request) (*http.Response, error) {
		body.ctx = r.Context()
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	}}
	c, _, ctx := ccLifecycleContextGeili(t, "/v1/chat/completions", nil)
	resp, err := ccLifecycleServiceGeili(upstream).sendCCUpstreamRequest(ctx, c, ccLifecycleAccountGeili("https://upstream.invalid"),
		"https://upstream.invalid/v1/chat/completions", []byte(`{"model":"gpt-4.1-mini"}`), false, "synthetic-test", "", "")
	require.NoError(t, err)
	require.NoError(t, body.ctx.Err())
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { errs <- resp.Body.Close() })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.ErrorIs(t, err, want)
	}
	require.EqualValues(t, 1, body.closed.Load())
	require.ErrorIs(t, body.ctx.Err(), context.Canceled)
	require.NoError(t, ctx.Err())
}

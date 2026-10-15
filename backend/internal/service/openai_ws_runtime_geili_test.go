//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Implement the optional runtime interfaces so an accidentally reintroduced
// success-path query or supplier-debt write is observable in real forwarders.
type transportRuntimeIORepoGeili struct {
	*recoveryRuntimeRepoGeili
	runtimeCalls atomic.Int64
}

func newTransportRuntimeIORepoGeili(a Account) *transportRuntimeIORepoGeili {
	return &transportRuntimeIORepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
}
func (r *transportRuntimeIORepoGeili) GetAccountRuntimeGeili(context.Context, int64) (*AccountRuntimeStateGeili, error) {
	r.runtimeCalls.Add(1)
	return nil, errors.New("unexpected runtime state read")
}
func (r *transportRuntimeIORepoGeili) CheckAccountRuntimeAdmissionGeili(context.Context, int64, string) (*AccountRuntimeAdmissionGeili, error) {
	r.runtimeCalls.Add(1)
	return nil, errors.New("unexpected runtime admission read")
}
func (r *transportRuntimeIORepoGeili) MarkAccountBalanceBlockedGeili(context.Context, int64, string) error {
	r.runtimeCalls.Add(1)
	return errors.New("unexpected automatic supplier debt write")
}
func (r *transportRuntimeIORepoGeili) ResetAccountModelSuccessGeili(context.Context, int64, string) error {
	r.runtimeCalls.Add(1)
	return errors.New("unexpected success state reset")
}
func (r *transportRuntimeIORepoGeili) RecordModelFailureGeili(context.Context, int64, string, string, time.Time) error {
	r.runtimeCalls.Add(1)
	return errors.New("unexpected failure counter write")
}

type transportRuntimeIOCacheGeili struct {
	TempUnschedCache
	calls atomic.Int64
}

func (c *transportRuntimeIOCacheGeili) GetAccountBalanceLatchGeili(context.Context, int64) (string, error) {
	c.calls.Add(1)
	return "", errors.New("unexpected debt latch read")
}
func (c *transportRuntimeIOCacheGeili) SetAccountBalanceLatchGeili(context.Context, int64) (string, error) {
	c.calls.Add(1)
	return "", errors.New("unexpected debt latch write")
}
func (c *transportRuntimeIOCacheGeili) ClearAccountBalanceLatchGeili(context.Context, int64, string) (bool, error) {
	c.calls.Add(1)
	return false, errors.New("unexpected debt latch clear")
}

func TestOpenAIWSRuntimeGeiliTurnsDoNotReadHealthOrPersistSupplierDebt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	repo := newTransportRuntimeIORepoGeili(*account)
	cache := &transportRuntimeIOCacheGeili{}
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	svc.accountRepo = repo
	svc.rateLimitService = &RateLimitService{accountRepo: repo, tempUnschedCache: cache}
	server, serverErr := startPassthroughLifecycleServer(t, context.Background(), svc, account)
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	for turn := 1; turn <= 3; turn++ {
		upstream.Send(`{"type":"response.output_text.delta","response_id":"resp_fixture","delta":"visible"}`)
		frame, err := readPassthroughLifecycleFrame(t, client, time.Second)
		require.NoError(t, err)
		require.Equal(t, "visible", gjson.GetBytes(frame, "delta").String())
		if turn != 2 {
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_fixture","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
		} else {
			upstream.Send(`{"type":"response.failed","response":{"id":"resp_fixture","error":{"code":"insufficient_balance","message":"declined"}}}`)
		}
		frame, err = readPassthroughLifecycleFrame(t, client, time.Second)
		require.NoError(t, err)
		if turn == 2 {
			require.Equal(t, "response.failed", gjson.GetBytes(frame, "type").String())
		} else {
			require.Equal(t, "response.completed", gjson.GetBytes(frame, "type").String())
		}
		if turn < 3 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err = client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1"}`))
			cancel()
			require.NoError(t, err)
			requirePassthroughUpstreamWrite(t, upstream, time.Second)
		}
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, ""))
	select {
	case <-serverErr:
	case <-time.After(2 * time.Second):
		t.Fatal("WS forwarder did not close")
	}
	require.Zero(t, repo.runtimeCalls.Load())
	require.Zero(t, cache.calls.Load())
	stored, err := repo.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	require.False(t, stored.RuntimeState.BalanceBlocked)
}

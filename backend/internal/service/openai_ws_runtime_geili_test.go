//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/stretchr/testify/require"
)

type accountDebtLatchTestGeili struct {
	TempUnschedCache
	tokens         map[int64]string
	sets           int
	setErr, getErr error
}

func (c *accountDebtLatchTestGeili) SetAccountBalanceLatchGeili(ctx context.Context, id int64) (string, error) {
	c.sets++
	if c.setErr != nil {
		return "", c.setErr
	}
	token := fmt.Sprintf("observation-%d", c.sets)
	c.tokens[id] = token
	return token, nil
}
func (c *accountDebtLatchTestGeili) GetAccountBalanceLatchGeili(ctx context.Context, id int64) (string, error) {
	return c.tokens[id], c.getErr
}
func (c *accountDebtLatchTestGeili) ClearAccountBalanceLatchGeili(ctx context.Context, id int64, token string) (bool, error) {
	if c.tokens[id] != token {
		return false, nil
	}
	delete(c.tokens, id)
	return true, nil
}
func newAccountDebtLatchTestGeili() *accountDebtLatchTestGeili {
	return &accountDebtLatchTestGeili{tokens: map[int64]string{}}
}

func TestAccountBalanceGuardGeiliIndependentLatchAndLocalDebt(t *testing.T) {
	for _, failed := range []string{"postgres", "redis", "both", "neither"} {
		t.Run(failed, func(t *testing.T) {
			a := stoppedRecoveryAccountGeili(1, 7)
			a.Status, a.Schedulable = StatusActive, true
			r := newRecoveryRuntimeRepoGeili(a)
			cache := newAccountDebtLatchTestGeili()
			if failed == "postgres" || failed == "both" {
				r.markError = errors.New("private database")
			}
			if failed == "redis" || failed == "both" {
				cache.setErr = errors.New("private cache")
			}
			svc := &RateLimitService{accountRepo: r, tempUnschedCache: cache}
			require.True(t, svc.stopSupplierBalanceGeili(context.Background(), &a, 402, []byte(`{"error":{"code":"insufficient_balance"}}`)))
			require.True(t, a.RuntimeState.BalanceBlocked)
			require.False(t, a.Schedulable)
			require.Equal(t, 1, cache.sets)
			if cache.setErr == nil {
				require.NotEmpty(t, cache.tokens[1])
			}
			// Duplicate hooks retain the token, while PostgreSQL persistence retries.
			token := cache.tokens[1]
			r.markError = nil
			require.True(t, svc.stopSupplierBalanceGeili(context.Background(), &a, 402, []byte(`{"error":{"code":"insufficient_balance"}}`)))
			require.Equal(t, 1, cache.sets)
			require.Equal(t, token, cache.tokens[1])
			stored, _ := r.GetByID(context.Background(), 1)
			require.True(t, stored.RuntimeState.BalanceBlocked)
		})
	}
}

func TestAccountBalanceGuardGeiliShadowDebtStopsCredentialOwner(t *testing.T) {
	parent := stoppedRecoveryAccountGeili(1, 7)
	child := stoppedRecoveryAccountGeili(2, 7)
	owner := parent.ID
	child.ParentAccountID = &owner
	r := newRecoveryRuntimeRepoGeili(parent, child)
	cache := newAccountDebtLatchTestGeili()
	svc := &RateLimitService{accountRepo: r, tempUnschedCache: cache}
	require.True(t, svc.stopSupplierBalanceGeili(context.Background(), &child, 402, []byte(`{"code":"insufficient_credit"}`)))
	require.NotEmpty(t, cache.tokens[parent.ID])
	require.Empty(t, cache.tokens[child.ID])
	stored, _ := r.GetByID(context.Background(), parent.ID)
	require.True(t, stored.RuntimeState.BalanceBlocked)
	childStored, _ := r.GetByID(context.Background(), child.ID)
	require.False(t, childStored.RuntimeState.BalanceBlocked)
	require.True(t, child.RuntimeState.BalanceBlocked)
}

func TestOpenAIWSRuntimeGeiliEveryDispatchUsesActualModelAndCurrentDebt(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	r := newRecoveryRuntimeRepoGeili(a)
	cache := newAccountDebtLatchTestGeili()
	svc := &OpenAIGatewayService{accountRepo: r, rateLimitService: &RateLimitService{accountRepo: r, tempUnschedCache: cache}}
	payload := []byte(`{"type":"response.create","model":"upstream-model"}`)
	require.NoError(t, svc.checkOpenAIWSRuntimeGeili(context.Background(), &a, payload, "public-model"))
	// The final mapped model matters, including a retry on a connection already acquired.
	r.accounts[a.ID].RuntimeState.Models["upstream-model"] = AccountModelHealthGeili{Blocked: true}
	var failure *UpstreamFailoverError
	require.ErrorAs(t, svc.checkOpenAIWSRuntimeGeili(context.Background(), &a, payload, "public-model"), &failure)
	require.Equal(t, GatewayFailureScopeAccount, failure.Scope)
	delete(r.accounts[a.ID].RuntimeState.Models, "upstream-model")
	cache.tokens[a.ID] = "debt"
	require.ErrorAs(t, svc.checkOpenAIWSRuntimeGeili(context.Background(), &a, payload, "public-model"), &failure)
	require.Contains(t, string(failure.ResponseBody), "supplier_balance_exhausted")
	cache.tokens[a.ID] = ""
	cache.getErr = errors.New("private cache")
	require.ErrorAs(t, svc.checkOpenAIWSRuntimeGeili(context.Background(), &a, payload, "public-model"), &failure)
	require.Equal(t, NextAccountStop, failure.NextAccountAction)
	require.Equal(t, http.StatusServiceUnavailable, failure.StatusCode)
}

func TestOpenAIWSRuntimeGeiliObservedDebtAfterOutputAndCancellation(t *testing.T) {
	for _, event := range []string{`{"type":"error","error":{"code":"insufficient_balance"}}`, `{"type":"response.failed","response":{"error":{"code":"insufficient_credit"}}}`} {
		a := stoppedRecoveryAccountGeili(1, 7)
		r := newRecoveryRuntimeRepoGeili(a)
		cache := newAccountDebtLatchTestGeili()
		svc := &OpenAIGatewayService{accountRepo: r, rateLimitService: &RateLimitService{accountRepo: r, tempUnschedCache: cache}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.False(t, svc.observeOpenAIWSBalanceGeili(ctx, &a, []byte(`{"type":"response.output_text.delta","delta":"insufficient balance"}`)))
		require.True(t, svc.observeOpenAIWSBalanceGeili(ctx, &a, []byte(event)))
		require.NotEmpty(t, cache.tokens[a.ID])
		require.True(t, a.RuntimeState.BalanceBlocked)
		require.Error(t, svc.checkOpenAIWSRuntimeGeili(context.Background(), &a, []byte(`{"model":"another-model"}`), ""))
	}
}

func TestOpenAIWSRuntimeGeiliPublicPassthroughRejectsDebtBeforeFirstWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	repo := newRecoveryRuntimeRepoGeili(*account)
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(context.Background(), account.ID, "supplier_insufficient_balance"))
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	svc.accountRepo = repo
	svc.rateLimitService = &RateLimitService{accountRepo: repo}
	server, serverErr := startPassthroughLifecycleServer(t, context.Background(), svc, account)
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	select {
	case err := <-serverErr:
		var failure *UpstreamFailoverError
		require.ErrorAs(t, err, &failure)
		require.Contains(t, string(failure.ResponseBody), "supplier_balance_exhausted")
	case <-time.After(time.Second):
		t.Fatal("initial debt admission did not stop")
	}
	select {
	case <-upstream.writes:
		t.Fatal("blocked account dispatched first WS turn")
	default:
	}
}

func TestOpenAIWSRuntimeGeiliPublicPassthroughObservesPostOutputDebtAndStopsNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	repo := newRecoveryRuntimeRepoGeili(*account)
	cache := newAccountDebtLatchTestGeili()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	svc.accountRepo = repo
	svc.rateLimitService = &RateLimitService{accountRepo: repo, tempUnschedCache: cache}
	server, serverErr := startPassthroughLifecycleServer(t, context.Background(), svc, account)
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.output_text.delta","response_id":"resp_debt","delta":"already delivered"}`)
	frame, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.Equal(t, "already delivered", gjson.GetBytes(frame, "delta").String())
	upstream.Send(`{"type":"response.failed","response":{"id":"resp_debt","error":{"code":"insufficient_credit","message":"declined"},"usage":{"input_tokens":2,"output_tokens":1}}}`)
	frame, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.failed", gjson.GetBytes(frame, "type").String())
	stored, _ := repo.GetByID(context.Background(), account.ID)
	require.True(t, stored.RuntimeState.BalanceBlocked)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err = client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"different-model"}`))
	cancel()
	require.NoError(t, err)
	select {
	case err := <-serverErr:
		require.Error(t, err)
		retryPayload, currentTurn := OpenAIWSCurrentTurnRetryPayload(err)
		require.True(t, currentTurn)
		require.Empty(t, retryPayload, "handler must stop, never replay its retained initial frame")
	case <-time.After(2 * time.Second):
		t.Fatal("next WS turn after supplier debt did not stop")
	}
	select {
	case <-upstream.writes:
		t.Fatal("next request or replay was sent after observed supplier debt")
	default:
	}
	require.Equal(t, 1, cache.sets, "duplicate SSE/WS hooks must retain latch token")
}

func TestOpenAIWSRuntimeGeiliUpgradeDebtAndClassifiedAdmission(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	repo := newRecoveryRuntimeRepoGeili(a)
	cache := newAccountDebtLatchTestGeili()
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{accountRepo: repo, tempUnschedCache: cache}}
	existing := NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	wrapped := fmt.Errorf("wrapped: %w", existing)
	require.Same(t, wrapped, svc.openAIWSDialAdmissionGeili(context.Background(), &a, wrapped))
	require.Same(t, wrapped, svc.mapOpenAIWSPassthroughDialError(wrapped, 502, http.Header{}))
	upgrade := &openAIWSDialError{StatusCode: 402, ResponseBody: []byte(`{"code":"insufficient_credit"}`), Err: errors.New("upgrade rejected")}
	err := svc.openAIWSDialAdmissionGeili(context.Background(), &a, upgrade)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.False(t, failure.ShouldReportAccountScheduleFailure())
	require.Equal(t, GatewayFailureScopeAccount, failure.Scope)
	require.Contains(t, string(failure.ResponseBody), "supplier_balance_exhausted")
	require.NotEmpty(t, cache.tokens[a.ID])
}

func TestOpenAIWSRuntimeGeiliSessionStopsWhenBothStoresFail(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	repo := newRecoveryRuntimeRepoGeili(a)
	repo.markError = errors.New("private database")
	cache := newAccountDebtLatchTestGeili()
	cache.setErr = errors.New("private cache")
	svc := &OpenAIGatewayService{accountRepo: repo, rateLimitService: &RateLimitService{accountRepo: repo, tempUnschedCache: cache}}
	ctx := withAccountWSBalanceObservationGeili(context.Background())
	require.True(t, svc.observeOpenAIWSBalanceGeili(ctx, &a, []byte(`{"type":"error","error":{"code":"insufficient_balance"}}`)))
	require.Empty(t, cache.tokens[a.ID])
	stored, _ := repo.GetByID(context.Background(), a.ID)
	require.False(t, stored.RuntimeState.BalanceBlocked)
	require.Error(t, svc.checkOpenAIWSRuntimeGeili(ctx, &a, []byte(`{"model":"other"}`), ""), "local atomic debt must guard a retained native session even without durable evidence")
}

func TestAccountBalanceGuardGeiliClineWalletsDoNotLatchWholeAccountWithRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"credits402", clineInsufficientCreditsBody, 402},
		{"unrecognized402", `{"error":{"code":"insufficient_balance","message":"insufficient balance"}}`, 402},
		{"credits429", clineInsufficientCreditsBody, 429},
		{"paidWallet", `{"error":{"code":"insufficient_balance","wallet":{"scope":"credits"}}}`, 403},
		{"passWallet", `{"response":{"error":{"code":"insufficient_balance","wallet":{"scope":"pass"}}}}`, 502},
		{"freeWallet", `{"error":{"code":"insufficient_balance","details":{"wallet":{"scope":"free"}}}}`, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := clineTestAccount(1)
			repo := newRecoveryRuntimeRepoGeili(*account)
			cache := newAccountDebtLatchTestGeili()
			svc := &RateLimitService{accountRepo: repo, tempUnschedCache: cache}
			require.False(t, svc.stopSupplierBalanceGeili(context.Background(), account, tc.status, []byte(tc.body)))
			require.Equal(t, 0, cache.sets)
			require.True(t, account.Schedulable)
			stored, _ := repo.GetByID(context.Background(), account.ID)
			require.False(t, stored.RuntimeState.BalanceBlocked)
		})
	}
}

func TestAccountBalanceGuardGeiliLegacyRepositoryKeepsProviderPolicy(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	a.Status, a.Schedulable = StatusActive, true
	svc := &RateLimitService{accountRepo: &rateLimitAccountRepoStub{}}
	require.False(t, svc.stopSupplierBalanceGeili(context.Background(), &a, 402, []byte(`{"error":{"code":"insufficient_balance"}}`)))
	require.Equal(t, StatusActive, a.Status)
	require.True(t, a.Schedulable)
	require.Nil(t, a.RuntimeState)
}

type wsRuntimeThresholdRepoGeili struct{ *recoveryRuntimeRepoGeili }

func (r *wsRuntimeThresholdRepoGeili) RecordModelFailureGeili(ctx context.Context, id int64, model, reason string, next time.Time) error {
	if err := r.recoveryRuntimeRepoGeili.RecordModelFailureGeili(ctx, id, model, reason, next); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	health := r.accounts[id].RuntimeState.Models[model]
	if health.FailureCount >= 3 {
		health.Blocked = true
	}
	r.accounts[id].RuntimeState.Models[model] = health
	return nil
}

func TestOpenAIWSRuntimeGeiliPublicPassthroughEachTurnCountsAndSuccessResets(t *testing.T) {
	for _, withSuccess := range []bool{false, true} {
		t.Run(fmt.Sprintf("success=%v", withSuccess), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			upstream := newStagedPassthroughConn()
			account := passthroughLifecycleAccount()
			repo := &wsRuntimeThresholdRepoGeili{newRecoveryRuntimeRepoGeili(*account)}
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			svc.accountRepo = repo
			svc.rateLimitService = &RateLimitService{accountRepo: repo}
			server, serverErr := startPassthroughLifecycleServer(t, context.Background(), svc, account)
			defer server.Close()
			client := dialPassthroughLifecycleClient(t, server)
			defer client.CloseNow()
			requirePassthroughUpstreamWrite(t, upstream, time.Second)
			count := 0
			for turn := 1; turn <= 3; turn++ {
				upstream.Send(fmt.Sprintf(`{"type":"response.failed","response":{"id":"resp_bad_%d","error":{"code":"server_error","type":"server_error","message":"internal server error","status":500}}}`, turn))
				frame, err := readPassthroughLifecycleFrame(t, client, time.Second)
				require.NoError(t, err)
				require.Equal(t, "response.failed", gjson.GetBytes(frame, "type").String())
				count++
				stored, _ := repo.GetByID(context.Background(), account.ID)
				require.Equal(t, count, stored.RuntimeState.Models["gpt-5.1"].FailureCount)
				if withSuccess && turn == 1 {
					writeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
					require.NoError(t, client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1"}`)))
					cancel()
					requirePassthroughUpstreamWrite(t, upstream, time.Second)
					upstream.Send(`{"type":"response.output_text.delta","response_id":"resp_ok","delta":"OK"}`)
					_, err = readPassthroughLifecycleFrame(t, client, time.Second)
					require.NoError(t, err)
					upstream.Send(`{"type":"response.completed","response":{"id":"resp_ok","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
					_, err = readPassthroughLifecycleFrame(t, client, time.Second)
					require.NoError(t, err)
					stored, _ = repo.GetByID(context.Background(), account.ID)
					require.Zero(t, stored.RuntimeState.Models["gpt-5.1"].FailureCount)
					count = 0
				}
				writeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				require.NoError(t, client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1"}`)))
				cancel()
				if turn < 3 || withSuccess {
					requirePassthroughUpstreamWrite(t, upstream, time.Second)
				}
			}
			if withSuccess {
				// A success interrupted the streak: two later failures are still dispatchable.
				stored, _ := repo.GetByID(context.Background(), account.ID)
				require.False(t, stored.RuntimeState.Models["gpt-5.1"].Blocked)
				require.NoError(t, client.Close(coderws.StatusNormalClosure, ""))
			} else {
				select {
				case err := <-serverErr:
					require.Error(t, err)
					retry, current := OpenAIWSCurrentTurnRetryPayload(err)
					require.True(t, current)
					require.Empty(t, retry)
				case <-time.After(2 * time.Second):
					t.Fatal("fourth turn bypassed three-failure threshold")
				}
				select {
				case <-upstream.writes:
					t.Fatal("blocked fourth turn reached supplier")
				default:
				}
			}
		})
	}
}

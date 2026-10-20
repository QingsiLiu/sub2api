//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func auapiNativeBalanceAccountGeili() Account {
	return Account{ID: 87, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://provider.example.test", "image_provider": "auapi"}}
}

func TestAUAPINativeBalanceGeiliLocalAdmissionIsNotUnknownAcceptance(t *testing.T) {
	for _, reason := range []string{"balance", "verification_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			a := auapiNativeBalanceAccountGeili()
			denied := NewAccountRuntimeAdmissionErrorGeili(reason)
			up := &nativePaidDebtUpstreamGeili{err: denied}
			gateway := &OpenAIGatewayService{httpUpstream: up, cfg: &config.Config{}}
			ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
			client := &auapiImageClient{account: &a, gateway: gateway}
			_, _, err := client.request(ctx, http.MethodPost, "/v1/images/tasks", []byte(`{}`), "fixture", 1024)
			require.Same(t, denied, err)
			require.False(t, dispatch.Started())
			require.Zero(t, up.reads)
		})
	}
}

func TestAUAPINativeBalanceGeiliSupplierErrorsLeaveDebtManual(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
		debt                     bool
	}{
		{"image debt", "POST", "/v1/images/tasks", `{"error":{"code":"insufficient_balance","message":"NEVER_STORE_SUPPLIER_TEXT"}}`, 503, true},
		{"video debt", "POST", "/v1/videos/tasks", `{"error":{"message":"credit balance is too low"}}`, 402, true},
		{"quota", "POST", "/v1/images/tasks", `{"error":{"code":"rate_limit_exceeded","message":"quota exceeded"}}`, 503, false},
		{"HTML", "POST", "/v1/images/tasks", `<html>insufficient balance</html>`, 503, false},
		{"request echo", "POST", "/v1/images/tasks", `{"error":{"message":"bad input"},"input":"insufficient balance"}`, 503, false},
		{"pricing", "POST", "/v1/pricing/estimate", `{"error":{"code":"insufficient_balance"}}`, 503, false},
		{"accepted poll", "GET", "/v1/tasks/task_fixture", `{"error":{"code":"insufficient_balance"}}`, 503, false},
		{"oversized", "POST", "/v1/images/tasks", `{"error":{"code":"insufficient_balance"},"padding":"` + strings.Repeat("x", 64<<10) + `"}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := auapiNativeBalanceAccountGeili()
			r := &nativePaidDebtRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			up := &nativePaidDebtUpstreamGeili{cancel: cancel, response: &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}}
			gateway := &OpenAIGatewayService{httpUpstream: up, cfg: &config.Config{}, rateLimitService: &RateLimitService{accountRepo: r}}
			client := &auapiImageClient{account: &a, gateway: gateway}
			_, _, err := client.request(ctx, tc.method, tc.path, []byte(`{}`), "fixture", 1024)
			var failure *auapiHTTPError
			require.ErrorAs(t, err, &failure)
			require.Zero(t, r.markCalls)
			require.NotContains(t, err.Error(), "NEVER_STORE_SUPPLIER_TEXT")
			if tc.debt {
				require.Equal(t, tc.status >= 500, failure.Retryable(), "an actual supplier 5xx still has unknown acceptance")
				state, e := r.GetAccountRuntimeGeili(context.Background(), a.ID)
				require.NoError(t, e)
				require.False(t, state.BalanceBlocked)
				require.Empty(t, state.BalanceReason)
			}
		})
	}
}

func TestAUAPINativeBalanceGeiliRejectedSubmissionReleasesHold(t *testing.T) {
	for _, kind := range []string{"image", "video"} {
		t.Run(kind, func(t *testing.T) {
			a := auapiNativeBalanceAccountGeili()
			accounts := newRecoveryRuntimeRepoGeili(a)
			task := &AUAPIImageTaskRecord{TaskID: "auimgtask_denied", Kind: kind, Phase: "submit", BaseURL: a.GetCredential("base_url"), AccountID: a.ID,
				UserID: 1, APIKeyID: 2, GroupID: 3, Model: "gpt-image-2", Count: 1, HoldAmount: 1, CreatedAt: time.Now(), RequestJSON: []byte(`{"kind":"` + kind + `","model":"gpt-image-2","input":{"prompt":"fixture"}}`)}
			repo := &auapiMediaRecoveryRepo{record: task}
			billing := &auapiSettlementBilling{}
			up := &nativePaidDebtUpstreamGeili{err: NewAccountRuntimeAdmissionErrorGeili("balance")}
			cfg := &config.Config{AUAPIImage: config.AUAPIImageConfig{RequestTimeoutSeconds: 30, LeaseSeconds: 45}}
			svc := &AUAPIImageTaskService{repo: repo, accounts: accounts, billing: billing, cfg: cfg, gateway: &OpenAIGatewayService{httpUpstream: up, cfg: cfg}}
			_, err := svc.Process(context.Background(), auapiQueueID(task.TaskID))
			require.NoError(t, err)
			require.Equal(t, "fail", task.Phase)
			require.Zero(t, up.reads)
			result, err := svc.Process(context.Background(), auapiQueueID(task.TaskID))
			require.NoError(t, err)
			require.True(t, result.Terminal)
			require.Len(t, billing.released, 1)
			require.Empty(t, billing.captured)
			require.Equal(t, 1, up.requests, "terminal cleanup never resubmits")
		})
	}
}

func TestAUAPINativeBalanceGeiliRealTransportUncertaintyRetainsHold(t *testing.T) {
	a := auapiNativeBalanceAccountGeili()
	task := &AUAPIImageTaskRecord{TaskID: "auimgtask_uncertain", Phase: "submit", AccountID: a.ID, BaseURL: a.GetCredential("base_url"), HoldAmount: 1,
		RequestJSON: []byte(`{"model":"gpt-image-2","input":{"prompt":"fixture"}}`)}
	up := &nativePaidDebtUpstreamGeili{err: errors.New("connection reset")}
	cfg := &config.Config{AUAPIImage: config.AUAPIImageConfig{RequestTimeoutSeconds: 30, LeaseSeconds: 45}}
	billing := &auapiSettlementBilling{}
	svc := &AUAPIImageTaskService{repo: &auapiMediaRecoveryRepo{record: task}, accounts: newRecoveryRuntimeRepoGeili(a), billing: billing, cfg: cfg,
		gateway: &OpenAIGatewayService{httpUpstream: up, cfg: cfg}}
	_, err := svc.Process(context.Background(), auapiQueueID(task.TaskID))
	require.NoError(t, err)
	require.Equal(t, "reconcile", task.Phase)
	require.Empty(t, billing.released)
	require.Empty(t, billing.captured)
}

func TestAUAPINativeBalanceGeiliDebtLabelDoesNotBlockUnsubmittedPricing(t *testing.T) {
	a := auapiNativeBalanceAccountGeili()
	accounts := newRecoveryRuntimeRepoGeili(a)
	require.NoError(t, accounts.MarkAccountBalanceBlockedGeili(context.Background(), a.ID, "supplier_insufficient_balance"))
	up := &nativePaidDebtUpstreamGeili{err: errors.New("fixture transport failure")}
	cfg := &config.Config{AUAPIImage: config.AUAPIImageConfig{MaxPollSeconds: 600}}
	svc := &AUAPIImageTaskService{accounts: accounts, cfg: cfg, gateway: &OpenAIGatewayService{httpUpstream: up, cfg: cfg}}
	err := svc.step(context.Background(), &AUAPIImageTaskRecord{TaskID: "auimgtask_known_debt", Phase: "estimate", AccountID: a.ID, CreatedAt: time.Now()})
	require.Error(t, err)
	require.False(t, isAccountRuntimeAdmissionFailureGeili(err))
	require.Equal(t, 1, up.requests)

}

type auapiCompleteErrorEOFGeili struct{ raw []byte }

func (r *auapiCompleteErrorEOFGeili) Read(p []byte) (int, error) {
	if len(r.raw) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.raw)
	r.raw = r.raw[n:]
	if len(r.raw) == 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func TestAUAPINativeBalanceGeiliCompleteErrorRetainsUncertaintyWithoutDebt(t *testing.T) {
	a := auapiNativeBalanceAccountGeili()
	r := &nativePaidDebtRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
	up := &nativePaidDebtUpstreamGeili{response: &http.Response{StatusCode: 503, Header: make(http.Header),
		Body: io.NopCloser(&auapiCompleteErrorEOFGeili{raw: []byte(`{"error":{"code":"insufficient_balance"}}`)})}}
	client := &auapiImageClient{account: &a, gateway: &OpenAIGatewayService{httpUpstream: up, cfg: &config.Config{}, rateLimitService: &RateLimitService{accountRepo: r}}}
	_, _, err := client.request(context.Background(), "POST", "/v1/images/tasks", []byte(`{}`), "fixture", 1024)
	var failure *auapiHTTPError
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Retryable(), "preserve real supplier acceptance uncertainty")
	require.Zero(t, r.markCalls)
}

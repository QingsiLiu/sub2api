//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type plainAdmissionRepoGeili struct {
	*recoveryRuntimeRepoGeili
	reads int
}

func (r *plainAdmissionRepoGeili) CheckAccountRuntimeAdmissionGeili(ctx context.Context, id int64, model string) (*AccountRuntimeAdmissionGeili, error) {
	r.reads++
	return r.recoveryRuntimeRepoGeili.CheckAccountRuntimeAdmissionGeili(ctx, id, model)
}

type plainFinalAdmissionTransportGeili struct {
	HTTPUpstream
	required bool
	repo     *plainAdmissionRepoGeili
	reads    int
}

func (u *plainFinalAdmissionTransportGeili) RequiresFinalAccountRuntimeAdmissionGeili() bool {
	return u.required
}

func (u *plainFinalAdmissionTransportGeili) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	gate, err := u.repo.CheckAccountRuntimeAdmissionGeili(req.Context(), id, AccountRuntimeForwardModelGeili(req.Context()))
	if err != nil || gate == nil {
		return nil, NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	}
	if !gate.Allowed {
		return nil, NewAccountRuntimeAdmissionErrorGeili(gate.Reason)
	}
	u.reads++
	ctxkey.MarkUpstreamDispatched(req.Context())
	return &http.Response{StatusCode: 200}, nil
}

func TestPlainAnthropicAdmissionGeiliOnlyCentralUnchangedNoToolPath(t *testing.T) {
	for _, kind := range []string{"plain", "legacy", "missing transport", "mapped", "OAuth", "Bedrock", "OpenAI", "web search", "function tool", "body model mismatch", "missing body"} {
		t.Run(kind, func(t *testing.T) {
			a := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{}}
			r := &plainAdmissionRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
			up := &plainFinalAdmissionTransportGeili{required: true, repo: r}
			parsed := &ParsedRequest{Model: "m", Body: NewRequestBodyRef([]byte(`{"model":"m","messages":[]}`))}
			svc := &GatewayService{accountRepo: r, httpUpstream: up}
			switch kind {
			case "legacy":
				up.required = false
			case "missing transport":
				svc.httpUpstream = nil
			case "mapped":
				a.Credentials["model_mapping"] = map[string]any{"m": "other"}
			case "OAuth":
				a.Type = AccountTypeOAuth
			case "Bedrock":
				a.Type = AccountTypeBedrock
			case "OpenAI":
				a.Platform = PlatformOpenAI
			case "web search":
				parsed.Body.Replace([]byte(`{"model":"m","tools":[{"type":"web_search_20250305"}]}`))
			case "function tool":
				parsed.Body.Replace([]byte(`{"model":"m","tools":[{"name":"fixture"}]}`))
			case "body model mismatch":
				parsed.Body.Replace([]byte(`{"model":"other"}`))
			case "missing body":
				parsed.Body = nil
			}
			require.NoError(t, svc.checkPlainAnthropicAdmissionGeili(context.Background(), &a, parsed))
			if kind == "plain" {
				require.Zero(t, r.reads)
			} else {
				require.Equal(t, 1, r.reads, "auxiliary/unknown/model-changing paths retain early verification")
			}
		})
	}
}

func TestPlainAnthropicAdmissionGeiliLatestDebtStillStopsBeforeDispatch(t *testing.T) {
	a := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	r := &plainAdmissionRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
	up := &plainFinalAdmissionTransportGeili{required: true, repo: r}
	svc := &GatewayService{accountRepo: r, httpUpstream: up}
	ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
	parsed := &ParsedRequest{Model: "m", Body: NewRequestBodyRef([]byte(`{"model":"m"}`))}
	require.NoError(t, svc.checkPlainAnthropicAdmissionGeili(ctx, &a, parsed))
	require.Zero(t, r.reads)
	// Another instance records debt after the early boundary. The final fresh
	// read still denies; the stale account snapshot never grants admission.
	require.NoError(t, r.MarkAccountBalanceBlockedGeili(ctx, a.ID, "supplier_insufficient_balance"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.example.test/v1/messages", nil)
	require.NoError(t, err)
	_, err = up.Do(req, "", a.ID, 1)
	require.Error(t, err)
	require.Equal(t, 1, r.reads)
	require.Zero(t, up.reads)
	require.False(t, dispatch.Started())
}

package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type runtimeHTTPReaderGeili struct {
	calls     int
	verdict   *service.AccountRuntimeAdmissionGeili
	err       error
	model     string
	accountID int64
}

func (r *runtimeHTTPReaderGeili) CheckAccountRuntimeAdmissionGeili(ctx context.Context, id int64, model string) (*service.AccountRuntimeAdmissionGeili, error) {
	r.calls++
	r.accountID = id
	r.model = model
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
		return nil, errors.New("missing admission deadline")
	}
	return r.verdict, r.err
}

type runtimeHTTPBodyGeili struct{ reads int }

func (b *runtimeHTTPBodyGeili) Read(p []byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *runtimeHTTPBodyGeili) Close() error               { return nil }

func TestHTTPUpstreamRuntimeGeiliEndpointClassification(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/messages/", "/api/v1/responses", "/backend-api/codex/responses", "/v1/responses/compact", "/openai/deployments/x/chat/completions", "/v1beta/models/gemini-x:generateContent", "/v1internal:streamGenerateContent", "/model/x/invoke-with-response-stream", "/v1/projects/p/locations/l/publishers/anthropic/models/claude-x:rawPredict", "/v1/projects/p/locations/l/publishers/anthropic/models/claude-x:streamRawPredict"} {
		req := httptest.NewRequest(http.MethodPost, "http://example.test"+path, nil)
		require.True(t, isGenerationRequestRuntimeGeili(req), path)
	}
	for _, path := range []string{"/oauth/token", "/v1/sub2api/billing", "/user/balance", "/v1/messages/count_tokens", "/v1beta/models/gemini-x:countTokens", "/v1internal:fetchAvailableModels", "/v1/models", "/v1/messages/status", "/v1/cachedContents"} {
		req := httptest.NewRequest(http.MethodPost, "http://example.test"+path, nil)
		require.False(t, isGenerationRequestRuntimeGeili(req), path)
	}
	require.False(t, isGenerationRequestRuntimeGeili(httptest.NewRequest(http.MethodGet, "http://example.test/v1/messages", nil)))
}

func TestHTTPUpstreamRuntimeGeiliDeniedDispatchReadsNoBodyAndSendsNoRequest(t *testing.T) {
	var dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dispatched.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	for _, mode := range []string{"plain", "nil TLS profile", "HTTP TLS fallback", "real fingerprinted TLS"} {
		t.Run(mode, func(t *testing.T) {
			reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: false, BalanceBlocked: true, Reason: "balance"}}
			upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
			upstream.runtimeAdmissionRepoGeili = reader
			body := &runtimeHTTPBodyGeili{}
			url := server.URL + "/v1/messages"
			if mode == "real fingerprinted TLS" {
				url = "https://example.test/v1/messages"
			}
			req, err := http.NewRequest(http.MethodPost, url, body)
			require.NoError(t, err)
			var resp *http.Response
			switch mode {
			case "plain":
				resp, err = upstream.Do(req, "", 17, 1)
			case "nil TLS profile":
				resp, err = upstream.DoWithTLS(req, "", 17, 1, nil)
			default:
				resp, err = upstream.DoWithTLS(req, "", 17, 1, &tlsfingerprint.Profile{Name: "fixture"})
			}
			require.Nil(t, resp)
			var failure *service.UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, service.GatewayFailureScopeAccount, failure.Scope)
			require.Equal(t, 1, reader.calls)
			require.Equal(t, int64(17), reader.accountID)
			require.Empty(t, reader.model)
			require.Zero(t, body.reads)
			require.Zero(t, dispatched.Load())
			require.Empty(t, upstream.clients)
		})
	}
}

func TestHTTPUpstreamRuntimeGeiliVerificationFailureStopsRequest(t *testing.T) {
	for _, reader := range []*runtimeHTTPReaderGeili{nil, {err: errors.New("private database credential")}, {verdict: nil}} {
		upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
		upstream.runtimeAdmissionRequiredGeili = true
		upstream.runtimeAdmissionRepoGeili = reader
		// Avoid assigning a typed nil reader into the interface.
		if reader == nil {
			upstream.runtimeAdmissionRepoGeili = nil
		}
		req := httptest.NewRequest(http.MethodPost, "http://example.test/v1/responses", nil)
		err := upstream.checkAccountRuntimeGenerationGeili(req, 17)
		var failure *service.UpstreamFailoverError
		require.ErrorAs(t, err, &failure)
		require.Equal(t, service.GatewayFailureScopeRequest, failure.Scope)
		require.Equal(t, service.NextAccountStop, failure.NextAccountAction)
		require.NotContains(t, string(failure.ResponseBody), "private database credential")
	}
}

func TestHTTPUpstreamRuntimeGeiliClientCannotForgeProbeExemption(t *testing.T) {
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Reason: "balance", BalanceBlocked: true}}
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	upstream.runtimeAdmissionRepoGeili = reader
	for _, path := range []string{"/v1/messages", "/v1/images/generations", "/vendor/images/tasks"} {
		req := httptest.NewRequest(http.MethodPost, "http://example.test"+path+"?account_recovery_probe=true", nil)
		req.Header.Set("X-Account-Recovery-Probe", "true")
		req.Header.Set("X-Geili-Probe", "true")
		req = req.WithContext(context.WithValue(req.Context(), "account_recovery_probe", true))
		require.Error(t, upstream.checkAccountRuntimeGenerationGeili(req, 17))
	}
	require.Equal(t, 3, reader.calls)
}

func TestHTTPUpstreamRuntimeGeiliHealthyDispatchAndManagementPaths(t *testing.T) {
	var dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dispatched.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: true}}
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	upstream.runtimeAdmissionRepoGeili = reader
	for _, test := range []struct{ method, path string }{{http.MethodPost, "/v1/chat/completions"}, {http.MethodGet, "/user/balance"}, {http.MethodPost, "/oauth/token"}, {http.MethodPost, "/v1/messages/count_tokens"}} {
		req, err := http.NewRequest(test.method, server.URL+test.path, nil)
		require.NoError(t, err)
		resp, err := upstream.Do(req, "", 17, 1)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	require.Equal(t, 1, reader.calls)
	require.Equal(t, int64(4), dispatched.Load())
}

func TestHTTPUpstreamRuntimeGeiliProductionProviderRetainsSubmissionPolicy(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	accounts := newAccountRepositoryWithSQL(nil, db, nil)
	upstream := ProvideHTTPUpstreamGeili(nil, accounts, nil).(*httpUpstreamService)
	require.Same(t, db, upstream.submissionDB)
	require.Same(t, accounts, upstream.runtimeAdmissionRepoGeili)
	require.True(t, upstream.runtimeAdmissionRequiredGeili)
	legacy := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.False(t, legacy.runtimeAdmissionRequiredGeili)
	require.Nil(t, legacy.runtimeAdmissionRepoGeili)
}

func TestHTTPUpstreamRuntimeGeiliFinalAdmissionCapabilityIsRequiredPolicyNotDependencyHealth(t *testing.T) {
	var absent *httpUpstreamService
	require.False(t, absent.RequiresFinalAccountRuntimeAdmissionGeili())
	legacy := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.False(t, legacy.RequiresFinalAccountRuntimeAdmissionGeili())
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: true}}
	legacy.runtimeAdmissionRepoGeili = reader
	require.False(t, legacy.RequiresFinalAccountRuntimeAdmissionGeili(), "an optional injected reader does not declare mandatory production policy")
	production := ProvideHTTPUpstreamGeili(nil, nil, nil).(*httpUpstreamService)
	require.True(t, production.RequiresFinalAccountRuntimeAdmissionGeili(), "missing dependencies cannot disable the production barrier")
	for _, tls := range []bool{false, true} {
		body := &runtimeHTTPBodyGeili{}
		req, err := http.NewRequest(http.MethodPost, "https://unresolvable.invalid/v1/messages", body)
		require.NoError(t, err)
		var resp *http.Response
		if tls {
			resp, err = production.DoWithTLS(req, "", 17, 1, &tlsfingerprint.Profile{Name: "fixture"})
		} else {
			resp, err = production.Do(req, "", 17, 1)
		}
		var failure *service.UpstreamFailoverError
		require.ErrorAs(t, err, &failure)
		require.Nil(t, resp)
		require.Equal(t, service.GatewayFailureScopeRequest, failure.Scope)
		require.Equal(t, service.NextAccountStop, failure.NextAccountAction)
		require.Contains(t, string(failure.ResponseBody), "account_verification_unavailable")
		require.Zero(t, body.reads)
		require.Empty(t, production.clients)
	}
	require.Zero(t, reader.calls, "capability checks must not read the database")
}

type runtimeHTTPProbeRepositoryGeili struct {
	service.AccountRepository
	service.AccountRuntimeRepositoryGeili
	reader *runtimeHTTPReaderGeili
}

func (r *runtimeHTTPProbeRepositoryGeili) GetByID(context.Context, int64) (*service.Account, error) {
	return &service.Account{ID: 17, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusError, Credentials: map[string]any{"api_key": "fixture"}}, nil
}
func (r *runtimeHTTPProbeRepositoryGeili) GetAccountRuntimeGeili(context.Context, int64) (*service.AccountRuntimeStateGeili, error) {
	return &service.AccountRuntimeStateGeili{AccountID: 17, Models: map[string]service.AccountModelHealthGeili{}}, nil
}
func (r *runtimeHTTPProbeRepositoryGeili) AcquireProbeLeaseGeili(context.Context, int64, string, time.Duration) (*service.AccountProbeLeaseGeili, error) {
	return &service.AccountProbeLeaseGeili{AccountID: 17, Token: "fixture", ExpiresAt: time.Now().Add(35 * time.Second)}, nil
}
func (r *runtimeHTTPProbeRepositoryGeili) ReleaseProbeLeaseGeili(context.Context, *service.AccountProbeLeaseGeili) error {
	return nil
}
func (r *runtimeHTTPProbeRepositoryGeili) RecordAccountProbeGeili(context.Context, service.AccountProbeResultGeili) error {
	return nil
}
func (r *runtimeHTTPProbeRepositoryGeili) ActivateAccountAfterProbeGeili(context.Context, service.AccountProbeActivationGeili) (*service.AccountRuntimeStateGeili, error) {
	return &service.AccountRuntimeStateGeili{AccountID: 17}, nil
}
func (r *runtimeHTTPProbeRepositoryGeili) CheckAccountRuntimeAdmissionGeili(ctx context.Context, id int64, model string) (*service.AccountRuntimeAdmissionGeili, error) {
	return r.reader.CheckAccountRuntimeAdmissionGeili(ctx, id, model)
}

func TestHTTPUpstreamRuntimeGeiliLegitimateRecoveryMintsProbeExemption(t *testing.T) {
	var dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dispatched.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Reason: "balance"}}
	accounts := &runtimeHTTPProbeRepositoryGeili{reader: reader}
	upstream := ProvideHTTPUpstreamGeili(nil, accounts, nil)
	recovery := service.NewAccountRecoveryServiceGeili(accounts, func(ctx context.Context, a *service.Account, model string) (bool, error) {
		require.True(t, service.IsAccountRecoveryProbeGeili(ctx))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/messages", nil)
		if err != nil {
			return false, err
		}
		resp, err := upstream.Do(req, "", a.ID, 1)
		if err != nil {
			return false, err
		}
		return true, resp.Body.Close()
	}, nil)
	require.NotNil(t, recovery)
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 17, "claude-opus-5", false)
	require.NoError(t, err)
	require.Equal(t, int64(1), dispatched.Load())
	require.Zero(t, reader.calls)
}

type runtimeHTTPLatchGeili struct {
	tokens map[int64]string
	err    error
	reads  []int64
}

func (l *runtimeHTTPLatchGeili) SetAccountBalanceLatchGeili(context.Context, int64) (string, error) {
	return "", nil
}
func (l *runtimeHTTPLatchGeili) GetAccountBalanceLatchGeili(_ context.Context, id int64) (string, error) {
	l.reads = append(l.reads, id)
	return l.tokens[id], l.err
}
func (l *runtimeHTTPLatchGeili) ClearAccountBalanceLatchGeili(context.Context, int64, string) (bool, error) {
	return true, nil
}

func TestHTTPUpstreamRuntimeGeiliRedisDebtBlocksHealthyPostgresAndShadowOwner(t *testing.T) {
	for _, owner := range []int64{17, 9} {
		t.Run(fmt.Sprint(owner), func(t *testing.T) {
			reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: true, CredentialAccountID: owner}}
			latch := &runtimeHTTPLatchGeili{tokens: map[int64]string{owner: "debt"}}
			upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
			upstream.runtimeAdmissionRepoGeili = reader
			upstream.runtimeBalanceLatchGeili = latch
			body := &runtimeHTTPBodyGeili{}
			req, err := http.NewRequest(http.MethodPost, "http://example.test/v1/messages", body)
			require.NoError(t, err)
			resp, err := upstream.Do(req, "", 17, 1)
			require.Nil(t, resp)
			var failover *service.UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Contains(t, string(failover.ResponseBody), "supplier_balance_exhausted")
			require.Zero(t, body.reads)
			require.Empty(t, upstream.clients)
		})
	}
}

func TestHTTPUpstreamRuntimeGeiliRedisReadFailureStopsBeforePostgres(t *testing.T) {
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: true}}
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	upstream.runtimeAdmissionRepoGeili = reader
	upstream.runtimeBalanceLatchGeili = &runtimeHTTPLatchGeili{err: errors.New("redis unavailable")}
	err := upstream.checkAccountRuntimeGenerationGeili(httptest.NewRequest(http.MethodPost, "http://example.test/v1/responses", nil), 17)
	var failover *service.UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, service.GatewayFailureScopeRequest, failover.Scope)
	require.Zero(t, reader.calls)
}

func TestHTTPUpstreamRuntimeGeiliNativePaidDebtBlockedBeforeEgress(t *testing.T) {
	var dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatched.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: true, BalanceBlocked: true}}
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	upstream.runtimeAdmissionRepoGeili = reader
	body := &runtimeHTTPBodyGeili{}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/images/generations", body)
	require.NoError(t, err)
	resp, err := upstream.Do(req, "", 17, 1)
	if resp != nil {
		_ = resp.Body.Close()
		t.Logf("mock upstream HTTP%d, PG checks=%d, supplier calls=%d, body reads=%d", resp.StatusCode, reader.calls, dispatched.Load(), body.reads)
	}
	// Regression proof: the unguarded endpoint returns200 from the mock supplier
	// with zero PostgreSQL checks, even though this account is already in debt.
	require.Error(t, err, "paid media must check debt before supplier dispatch")
	require.Nil(t, resp)
	require.Equal(t, 1, reader.calls)
	require.Zero(t, body.reads)
	require.Zero(t, dispatched.Load())
	require.Empty(t, upstream.clients)
}

func nativePaidRequestsGeili() []struct{ method, path string } {
	return []struct{ method, path string }{
		{http.MethodPost, "/v1/images/generations"}, {http.MethodPost, "/v1/images/edits"},
		{http.MethodPost, "/v1/videos/generations"}, {http.MethodPost, "/v1/videos/edits"},
		{http.MethodPost, "/v1/videos/extensions"}, {http.MethodPost, "/openai/v2/embeddings"},
		{http.MethodPost, "/v1beta/cachedContents"}, {http.MethodPost, "/vendor-prefix/images/tasks"},
		{http.MethodPost, "/vendor-prefix/videos/tasks"}, {http.MethodPost, "/vendor-prefix/contents/generations/tasks"},
	}
}

func TestHTTPUpstreamRuntimeGeiliNativePaidClassificationIsMethodAndEndpointBounded(t *testing.T) {
	for _, test := range nativePaidRequestsGeili() {
		for _, trailing := range []string{"", "/"} {
			req := httptest.NewRequest(test.method, "http://example.test"+test.path+trailing+"?token=irrelevant", nil)
			require.True(t, isNativePaidBalanceRequestRuntimeGeili(req), test.method+" "+test.path+trailing)
			require.False(t, isGenerationRequestRuntimeGeili(req), "native operations must not become text recovery eligible")
			req.Method = http.MethodGet
			require.False(t, isNativePaidBalanceRequestRuntimeGeili(req))
		}
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/v1/images/variations"}, {http.MethodPost, "/v1/videos/video-id"},
		{http.MethodPost, "/v1/videos/video-id/content"}, {http.MethodPost, "/v1beta/models/x:countTokens"},
		{http.MethodPost, "/v1beta/cachedContents/cache-id"}, {http.MethodPatch, "/v1beta/cachedContents"},
		{http.MethodPatch, "/v1beta/cachedContents/cache-id"},
		{http.MethodPatch, "/v1beta/cachedContents/cache-id/content"}, {http.MethodDelete, "/v1beta/cachedContents/cache-id"},
		{http.MethodPost, "/oauth/token"}, {http.MethodPost, "/user/balance"}, {http.MethodPost, "/v1/models"},
	} {
		require.False(t, isNativePaidBalanceRequestRuntimeGeili(httptest.NewRequest(test.method, "http://example.test"+test.path, nil)), test.path)
	}
	require.False(t, isNativePaidBalanceRequestRuntimeGeili(nil))
	require.False(t, isNativePaidBalanceRequestRuntimeGeili(&http.Request{Method: http.MethodPost}))
}

func TestHTTPUpstreamRuntimeGeiliAllNativePaidPathsDenyDebtBeforeBodyDNSOrClientPool(t *testing.T) {
	for _, test := range nativePaidRequestsGeili() {
		for _, tls := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s %s TLS%t", test.method, test.path, tls), func(t *testing.T) {
				reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: true, BalanceBlocked: true}}
				latch := &runtimeHTTPLatchGeili{}
				upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
				upstream.runtimeAdmissionRepoGeili, upstream.runtimeBalanceLatchGeili = reader, latch
				upstream.runtimeAdmissionRequiredGeili = true
				body := &runtimeHTTPBodyGeili{}
				req, err := http.NewRequest(test.method, "https://unresolvable.invalid"+test.path+"/", body)
				require.NoError(t, err)
				var resp *http.Response
				if tls {
					resp, err = upstream.DoWithTLS(req, "", 17, 1, &tlsfingerprint.Profile{Name: "fixture"})
				} else {
					resp, err = upstream.Do(req, "", 17, 1)
				}
				require.Nil(t, resp)
				var failure *service.UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Contains(t, string(failure.ResponseBody), "supplier_balance_exhausted")
				require.Equal(t, service.GatewayFailureScopeAccount, failure.Scope)
				require.Equal(t, 1, reader.calls)
				require.Equal(t, []int64{17}, latch.reads)
				require.Zero(t, body.reads)
				require.Empty(t, upstream.clients)
			})
		}
	}
}

func TestHTTPUpstreamRuntimeGeiliNativeNonDebtStopAllowsExplicitTestsButTextStillDenied(t *testing.T) {
	var dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatched.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: false, Reason: "temporary_failure"}}
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	upstream.runtimeAdmissionRequiredGeili = true
	upstream.runtimeAdmissionRepoGeili, upstream.runtimeBalanceLatchGeili = reader, &runtimeHTTPLatchGeili{}
	for _, test := range nativePaidRequestsGeili() {
		req, err := http.NewRequest(test.method, server.URL+test.path, nil)
		require.NoError(t, err)
		resp, err := upstream.Do(req, "", 17, 1)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	require.Equal(t, int64(len(nativePaidRequestsGeili())), dispatched.Load())
	req := httptest.NewRequest(http.MethodPost, server.URL+"/v1/messages", nil)
	resp, err := upstream.Do(req, "", 17, 1)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, len(nativePaidRequestsGeili())+1, reader.calls)
	require.Equal(t, int64(len(nativePaidRequestsGeili())), dispatched.Load())
}

func TestHTTPUpstreamRuntimeGeiliNativeDebtBarrierAndParentLatchFailures(t *testing.T) {
	for _, scenario := range []string{"missing reader", "missing latch", "PG error", "nil PG verdict", "Redis error", "account latch", "parent latch", "parent Redis error"} {
		t.Run(scenario, func(t *testing.T) {
			reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: true, CredentialAccountID: 9}}
			latch := &runtimeHTTPLatchGeili{tokens: map[int64]string{}}
			upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
			upstream.runtimeAdmissionRequiredGeili = true
			upstream.runtimeAdmissionRepoGeili, upstream.runtimeBalanceLatchGeili = reader, latch
			switch scenario {
			case "missing reader":
				upstream.runtimeAdmissionRepoGeili = nil
			case "missing latch":
				upstream.runtimeBalanceLatchGeili = nil
			case "PG error":
				reader.err = errors.New("private database credential")
			case "nil PG verdict":
				reader.verdict = nil
			case "Redis error":
				latch.err = errors.New("private Redis credential")
			case "account latch":
				latch.tokens[17] = "debt"
			case "parent latch":
				latch.tokens[9] = "debt"
			default:
				upstream.runtimeBalanceLatchGeili = &runtimeHTTPParentErrorLatchGeili{runtimeHTTPLatchGeili: latch}
			}
			body := &runtimeHTTPBodyGeili{}
			req, err := http.NewRequest(http.MethodPost, "https://unresolvable.invalid/v1/videos/generations", body)
			require.NoError(t, err)
			resp, err := upstream.Do(req, "", 17, 1)
			require.Nil(t, resp)
			var failure *service.UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.NotContains(t, string(failure.ResponseBody), "private")
			if scenario == "account latch" || scenario == "parent latch" {
				require.Equal(t, service.GatewayFailureScopeAccount, failure.Scope)
				require.Contains(t, string(failure.ResponseBody), "supplier_balance_exhausted")
			} else {
				require.Equal(t, service.GatewayFailureScopeRequest, failure.Scope)
				require.Equal(t, service.NextAccountStop, failure.NextAccountAction)
				require.Contains(t, string(failure.ResponseBody), "account_verification_unavailable")
			}
			require.Zero(t, body.reads)
			require.Empty(t, upstream.clients)
			if scenario == "parent latch" || scenario == "parent Redis error" {
				require.Equal(t, []int64{17, 9}, latch.reads)
				require.Equal(t, 1, reader.calls)
			}
		})
	}
}

type runtimeHTTPParentErrorLatchGeili struct{ *runtimeHTTPLatchGeili }

func (l *runtimeHTTPParentErrorLatchGeili) GetAccountBalanceLatchGeili(ctx context.Context, id int64) (string, error) {
	token, err := l.runtimeHTTPLatchGeili.GetAccountBalanceLatchGeili(ctx, id)
	if id == 9 {
		return "", errors.New("private parent Redis credential")
	}
	return token, err
}

func TestHTTPUpstreamRuntimeGeiliNativeManagementRequestsKeepExistingTransport(t *testing.T) {
	var dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatched.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{BalanceBlocked: true}}
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	upstream.runtimeAdmissionRequiredGeili = true
	upstream.runtimeAdmissionRepoGeili, upstream.runtimeBalanceLatchGeili = reader, &runtimeHTTPLatchGeili{tokens: map[int64]string{17: "debt"}}
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/v1/videos/video-id"}, {http.MethodGet, "/v1/videos/video-id/content"},
		{http.MethodGet, "/v1/images/generations"}, {http.MethodGet, "/v1beta/cachedContents/cache-id"},
		{http.MethodPatch, "/v1beta/cachedContents/cache-id"},
		{http.MethodDelete, "/v1beta/cachedContents/cache-id"}, {http.MethodPost, "/oauth/token"},
		{http.MethodGet, "/user/balance"}, {http.MethodPost, "/v1beta/models/x:countTokens"},
		{http.MethodGet, "/vendor-prefix/images/tasks/task-id"}, {http.MethodGet, "/vendor-prefix/videos/tasks/task-id"},
		{http.MethodGet, "/vendor-prefix/contents/generations/tasks/task-id"},
	} {
		req, err := http.NewRequest(test.method, server.URL+test.path, nil)
		require.NoError(t, err)
		resp, err := upstream.Do(req, "", 17, 1)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	require.Zero(t, reader.calls)
	require.Equal(t, int64(12), dispatched.Load())
}

func TestHTTPUpstreamRuntimeGeiliPrivateTextProbeDoesNotExemptNativePaidDebt(t *testing.T) {
	var dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatched.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{BalanceBlocked: true}}
	accounts := &runtimeHTTPProbeRepositoryGeili{reader: reader}
	upstream := ProvideHTTPUpstreamGeili(nil, accounts, &runtimeHTTPProbeLatchGeili{runtimeHTTPLatchGeili: &runtimeHTTPLatchGeili{}})
	recovery := service.NewAccountRecoveryServiceGeili(accounts, func(ctx context.Context, account *service.Account, model string) (bool, error) {
		require.True(t, service.IsAccountRecoveryProbeGeili(ctx))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/images/generations", nil)
		require.NoError(t, err)
		resp, err := upstream.Do(req, "", account.ID, 1)
		require.Nil(t, resp)
		var failure *service.UpstreamFailoverError
		require.ErrorAs(t, err, &failure)
		require.Contains(t, string(failure.ResponseBody), "supplier_balance_exhausted")
		var callbackFailure *service.UpstreamFailoverError
		require.ErrorAs(t, upstream.(*httpUpstreamService).CheckNativeBalanceBeforeDispatchGeili(req, account.ID), &callbackFailure)
		require.Equal(t, failure.ResponseBody, callbackFailure.ResponseBody)
		return false, err
	}, nil)
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 17, "claude-opus-5", false)
	require.Error(t, err)
	require.Equal(t, 2, reader.calls)
	require.Zero(t, dispatched.Load())
}

type runtimeHTTPProbeLatchGeili struct {
	service.TempUnschedCache
	*runtimeHTTPLatchGeili
}

func TestHTTPUpstreamRuntimeGeiliNativePluginCallbackMatchesCentralBarrier(t *testing.T) {
	for _, scenario := range []string{"PG debt", "parent Redis debt", "PG error", "Redis error", "stopped nondebt"} {
		t.Run(scenario, func(t *testing.T) {
			reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{Allowed: false, Reason: "temporary_failure", CredentialAccountID: 9}}
			latch := &runtimeHTTPLatchGeili{tokens: map[int64]string{}}
			upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
			upstream.runtimeAdmissionRequiredGeili = true
			upstream.runtimeAdmissionRepoGeili, upstream.runtimeBalanceLatchGeili = reader, latch
			switch scenario {
			case "PG debt":
				reader.verdict.BalanceBlocked = true
			case "parent Redis debt":
				latch.tokens[9] = "debt"
			case "PG error":
				reader.err = errors.New("private PG credential")
			case "Redis error":
				latch.err = errors.New("private Redis credential")
			}
			body := &runtimeHTTPBodyGeili{}
			req, err := http.NewRequest(http.MethodPost, "https://unresolvable.invalid/v1/images/edits", body)
			require.NoError(t, err)
			callbackErr := upstream.CheckNativeBalanceBeforeDispatchGeili(req, 17)
			centralErr := upstream.checkAccountRuntimeGenerationGeili(req, 17)
			if scenario == "stopped nondebt" {
				require.NoError(t, callbackErr)
				require.NoError(t, centralErr)
			} else {
				var callbackFailure, centralFailure *service.UpstreamFailoverError
				require.ErrorAs(t, callbackErr, &callbackFailure)
				require.ErrorAs(t, centralErr, &centralFailure)
				require.Equal(t, centralFailure.Scope, callbackFailure.Scope)
				require.Equal(t, centralFailure.NextAccountAction, callbackFailure.NextAccountAction)
				require.Equal(t, centralFailure.ResponseBody, callbackFailure.ResponseBody)
				require.NotContains(t, string(callbackFailure.ResponseBody), "private")
			}
			require.Zero(t, body.reads)
			require.Empty(t, upstream.clients)
			if scenario == "Redis error" {
				require.Zero(t, reader.calls)
			} else {
				require.Equal(t, 2, reader.calls, "both dispatch branches must execute fresh PG reads")
			}
		})
	}
}

func TestHTTPUpstreamRuntimeGeiliNativePluginCallbackIgnoresTextMetadataAndLegacyDependency(t *testing.T) {
	reader := &runtimeHTTPReaderGeili{verdict: &service.AccountRuntimeAdmissionGeili{BalanceBlocked: true}}
	latch := &runtimeHTTPLatchGeili{tokens: map[int64]string{17: "debt"}}
	upstream := NewHTTPUpstream(nil).(*httpUpstreamService)
	upstream.runtimeAdmissionRequiredGeili = true
	upstream.runtimeAdmissionRepoGeili, upstream.runtimeBalanceLatchGeili = reader, latch
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/v1/messages"}, {http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/responses"}, {http.MethodGet, "/v1/videos/task-id"},
		{http.MethodPatch, "/v1beta/cachedContents/cache-id"}, {http.MethodPost, "/oauth/token"},
	} {
		req := httptest.NewRequest(test.method, "http://example.test"+test.path, nil)
		require.NoError(t, upstream.CheckNativeBalanceBeforeDispatchGeili(req, 17))
	}
	require.NoError(t, upstream.CheckNativeBalanceBeforeDispatchGeili(nil, 17))
	require.Zero(t, reader.calls)
	require.Empty(t, latch.reads)
	req := httptest.NewRequest(http.MethodPost, "http://example.test/v1/embeddings", nil)
	legacy := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.NoError(t, legacy.CheckNativeBalanceBeforeDispatchGeili(req, 17))
	production := ProvideHTTPUpstreamGeili(nil, nil, nil).(*httpUpstreamService)
	var failure *service.UpstreamFailoverError
	require.ErrorAs(t, production.CheckNativeBalanceBeforeDispatchGeili(req, 17), &failure)
	require.Equal(t, service.GatewayFailureScopeRequest, failure.Scope)
	require.Equal(t, service.NextAccountStop, failure.NextAccountAction)
}

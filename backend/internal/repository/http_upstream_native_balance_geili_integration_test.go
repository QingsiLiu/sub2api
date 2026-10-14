//go:build integration

package repository

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type nativeBalanceIntegrationCacheGeili struct{ service.TempUnschedCache }

func (*nativeBalanceIntegrationCacheGeili) GetAccountBalanceLatchGeili(context.Context, int64) (string, error) {
	return "", nil
}
func (*nativeBalanceIntegrationCacheGeili) SetAccountBalanceLatchGeili(context.Context, int64) (string, error) {
	return "fixture", nil
}
func (*nativeBalanceIntegrationCacheGeili) ClearAccountBalanceLatchGeili(context.Context, int64, string) (bool, error) {
	return true, nil
}

type nativeBalanceIntegrationBodyGeili struct{ reads int }

func (b *nativeBalanceIntegrationBodyGeili) Read([]byte) (int, error) {
	b.reads++
	return 0, io.EOF
}

func TestNativePaidBalanceGeiliIntegrationPreparedInstancesReadCurrentDebt(t *testing.T) {
	ctx := context.Background()
	first := NewAccountRepository(integrationEntClient, integrationDB, nil).(*accountRepository)
	second := NewAccountRepository(integrationEntClient, integrationDB, nil).(*accountRepository)
	a := runtimeIntegrationAccountGeili(t, first)
	t.Cleanup(func() {
		// Runtime mutations emit scheduler events outside the account FK;
		// remove only this fixture before the shared account cleanup runs.
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM scheduler_outbox WHERE account_id=$1`, a.ID)
		require.NoError(t, err)
	})
	cache := &nativeBalanceIntegrationCacheGeili{}
	upstreams := []*httpUpstreamService{
		ProvideHTTPUpstreamGeili(nil, first, cache).(*httpUpstreamService),
		ProvideHTTPUpstreamGeili(nil, second, cache).(*httpUpstreamService),
	}
	for _, up := range upstreams {
		for i := 0; i < 8; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.example.test/v1/images/generations", nil)
			require.NoError(t, err)
			require.NoError(t, up.CheckNativeBalanceBeforeDispatchGeili(req, a.ID))
		}
	}
	// A writer separate from both prepared readers records debt. Neither a
	// warmed plan nor an old active account object may authorize later egress.
	writer := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	require.NoError(t, writer.MarkAccountBalanceBlockedGeili(ctx, a.ID, "supplier_insufficient_balance"))
	for _, up := range upstreams {
		for _, path := range []string{"/v1/images/generations", "/backend-api/codex/images/edits", "/v1/videos/generations", "/v1/videos/edits", "/v1/videos/extensions", "/v1/embeddings", "/v1beta/cachedContents", "/v1/images/tasks", "/v1/videos/tasks", "/api/v3/contents/generations/tasks"} {
			body := &nativeBalanceIntegrationBodyGeili{}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.example.test"+path, body)
			require.NoError(t, err)
			response, err := up.Do(req, "", a.ID, 1)
			require.Nil(t, response)
			var denied *service.UpstreamFailoverError
			require.ErrorAs(t, err, &denied)
			require.Equal(t, service.GatewayFailureScopeAccount, denied.Scope)
			require.Contains(t, string(denied.ResponseBody), "supplier_balance_exhausted")
			require.Zero(t, body.reads)
			require.Empty(t, up.clients, "the final database verdict precedes transport allocation")
		}
	}
}

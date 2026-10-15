package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHTTPUpstreamRuntimeGeiliProductionProviderRetainsSubmissionPolicy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	accounts := newAccountRepositoryWithSQL(nil, db, nil)
	upstream := ProvideHTTPUpstreamGeili(nil, accounts).(*httpUpstreamService)
	require.Same(t, db, upstream.submissionDB)
	require.NoError(t, mock.ExpectationsWereMet(), "construction is lazy")
}

// An account with a warm invitation-policy entry must add no SQL for supplier
// debt or health. Unexpected sqlmock queries fail the real dispatch below.
func TestHTTPUpstreamRuntimeGeiliDispatchAddsNoAccountHealthQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"fixture"}`, string(body))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, path := range []string{
		"/v1/messages", "/v1/responses", "/v1/chat/completions", "/v1beta/models/gemini:generateContent",
		"/v1/images/generations", "/v1/images/edits", "/v1/videos/generations", "/v1/embeddings",
		"/v1beta/cachedContents", "/v1/images/tasks", "/v1/videos/tasks", "/api/v3/contents/generations/tasks",
	} {
		for _, mode := range []string{"http", "tls-nil", "tls-http-fallback"} {
			t.Run(mode+path, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer db.Close()
				upstream := ProvideHTTPUpstreamGeili(nil, newAccountRepositoryWithSQL(nil, db, nil)).(*httpUpstreamService)
				upstream.submissionPolicyCache.Store(int64(17), submissionPolicyCacheEntryGeili{expires: time.Now().Add(time.Minute)})
				ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, strings.NewReader(`{"model":"fixture"}`))
				require.NoError(t, err)
				var resp *http.Response
				switch mode {
				case "tls-nil":
					resp, err = upstream.DoWithTLS(req, "", 17, 1, nil)
				case "tls-http-fallback":
					resp, err = upstream.DoWithTLS(req, "", 17, 1, &tlsfingerprint.Profile{Name: "unused-for-http"})
				default:
					resp, err = upstream.Do(req, "", 17, 1)
				}
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.NoError(t, resp.Body.Close())
				require.True(t, dispatch.Started(), "preserve dispatched-request billing")
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestHTTPUpstreamRuntimeGeiliInvitationPolicyStillReadsItsOwnPolicy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	upstream := ProvideHTTPUpstreamGeili(nil, newAccountRepositoryWithSQL(nil, db, nil)).(*httpUpstreamService)
	req := httptest.NewRequest(http.MethodPost, "https://provider.example.test/v1/messages", nil)
	proxy, err := upstream.submissionProxyPolicyGeili(req, "", 17)
	require.NoError(t, err)
	require.Empty(t, proxy)
	require.NoError(t, mock.ExpectationsWereMet())
	_, err = upstream.submissionProxyPolicyGeili(req, "", 17)
	require.NoError(t, err, "only the existing invitation policy is cached")
}

var _ service.HTTPUpstream = (*httpUpstreamService)(nil)

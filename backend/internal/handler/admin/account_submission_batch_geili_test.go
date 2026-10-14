package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type submissionBatchHandlerRepo struct {
	submissionHandlerRepo
	count int
}

func (r *submissionBatchHandlerRepo) InspectInvite(context.Context, string) (*service.AccountSubmissionInvite, error) {
	return &service.AccountSubmissionInvite{Config: service.AccountSubmissionConfig{Platform: service.PlatformAnthropic}, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (r *submissionBatchHandlerRepo) SubmitInviteBatch(_ context.Context, _ string, keys []string, _ service.SubmissionOptions) error {
	r.count += len(keys)
	return nil
}

func TestAccountSubmissionBatchStrictCredentialBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &submissionBatchHandlerRepo{}
	h := &AccountHandler{accountSubmission: service.NewAccountSubmissionService(repo, newStubAdminService())}
	router := gin.New()
	router.POST("/batch", h.SubmitAccountKeyBatch)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	valid := `{"token":"` + token + `","api_keys":["synthetic-one","synthetic-two"]}`
	for _, body := range []string{strings.Replace(valid, "}", `,"group_ids":[1]}`, 1), strings.Replace(valid, "}", `,"account_id":6381}`, 1), valid + ` {}`, `{"api_keys":["` + strings.Repeat("s", 385*1024) + `"]}`} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/batch", strings.NewReader(body)))
		require.Equal(t, 400, rec.Code)
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		require.NotContains(t, rec.Body.String(), "synthetic-one")
	}
	for _, body := range []string{
		`{"token":"` + token + `","items":[{"api_key":"synthetic-a","connection":{"mode":"direct","proxy_id":7}}]}`,
		`{"token":"` + token + `","items":[{"api_key":"synthetic-a","connection":{"mode":"direct"}}],"api_keys":["synthetic-b"]}`,
		`{"token":"` + token + `","items":[{"api_key":"synthetic-a"}]}`,
		`{"token":"` + token + `","items":[{"api_key":"synthetic-a","connection":{"mode":"direct"}}],"connection":{"mode":"direct"}}`,
		`{"token":"` + token + `","api_keys":["synthetic-a"],"PerKeyConnections":[]}`,
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/batch", strings.NewReader(body)))
		require.Equal(t, 400, rec.Code)
		require.NotContains(t, rec.Body.String(), "synthetic-a")
	}
	require.Zero(t, repo.count)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/batch", strings.NewReader(valid)))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, 2, repo.count)
	require.NotContains(t, rec.Body.String(), token)
	// The dedicated bound accepts a full batch beyond the old single-key 8 KiB limit.
	keys := make([]string, 100)
	for n := range keys {
		keys[n] = strings.Repeat("x", 2040) + string(rune('一'+n))
	}
	body, err := json.Marshal(map[string]any{"token": token, "api_keys": keys})
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/batch", strings.NewReader(string(body))))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, 102, repo.count)
	individual := `{"token":"` + token + `","items":[{"api_key":"synthetic-a","connection":{"mode":"direct"}},{"api_key":"synthetic-b","connection":{"mode":"custom","proxy":{"name":"test","protocol":"http","host":"8.8.8.8","port":8080}}}]}`
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/batch", strings.NewReader(individual)))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, 104, repo.count)

}

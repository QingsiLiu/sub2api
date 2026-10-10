package admin

import (
	"context"
	"encoding/base64"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type submissionHandlerRepo struct {
	service.AccountSubmissionRepository
	protected map[int64]bool
	err       error
}

func (r *submissionHandlerRepo) ProtectedAccountIDs(context.Context, []int64) (map[int64]bool, error) {
	return r.protected, r.err
}
func (r *submissionHandlerRepo) HasProtectedAccounts(context.Context) (bool, error) {
	return len(r.protected) > 0, r.err
}
func (r *submissionHandlerRepo) InspectInvite(context.Context, string) (*service.AccountSubmissionInvite, error) {
	return &service.AccountSubmissionInvite{ID: 99, CreatedBy: 1, Config: service.AccountSubmissionConfig{Name: "private account", Platform: service.PlatformOpenAI, GroupIDs: []int64{88}}, Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestAccountSubmissionExportAndDuplicateProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "protected", true: "lookup-failure"}[fail], func(t *testing.T) {
			adminSvc := newStubAdminService()
			adminSvc.accounts = []service.Account{{ID: 21, Name: "external", Platform: service.PlatformOpenAI, Type: "apikey", Credentials: map[string]any{"api_key": "external-synthetic-secret"}}, {ID: 22, Name: "ordinary", Platform: service.PlatformOpenAI, Type: "apikey", Credentials: map[string]any{"api_key": "ordinary-synthetic-secret"}}}
			repo := &submissionHandlerRepo{protected: map[int64]bool{21: true}}
			if fail {
				repo.err = service.ErrSubmissionUnavailable
			}
			h := &AccountHandler{adminService: adminSvc, accountSubmission: service.NewAccountSubmissionService(repo, adminSvc)}
			router := gin.New()
			router.GET("/data", h.ExportData)
			router.POST("/accounts/:id/duplicate", h.Duplicate)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/data?include_proxies=false", nil))
			require.NotContains(t, recorder.Body.String(), "external-synthetic-secret")
			if fail {
				require.Equal(t, 503, recorder.Code)
			} else {
				require.Equal(t, 200, recorder.Code)
				require.Contains(t, recorder.Body.String(), "ordinary-synthetic-secret")
				require.Contains(t, recorder.Body.String(), `"skipped_external":1`)
			}
			duplicate := httptest.NewRecorder()
			router.ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/accounts/21/duplicate", nil))
			require.Contains(t, []int{400, 503}, duplicate.Code)
			require.NotContains(t, duplicate.Body.String(), "external-synthetic-secret")
		})
	}
}

func TestAccountSubmissionPublicMetadataAndMalformedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &submissionHandlerRepo{}
	h := &AccountHandler{accountSubmission: service.NewAccountSubmissionService(repo, nil)}
	router := gin.New()
	router.POST("/inspect", h.InspectSubmissionInvite)
	router.POST("/submit", h.SubmitAccountKey)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	inspect := httptest.NewRecorder()
	router.ServeHTTP(inspect, httptest.NewRequest(http.MethodPost, "/inspect", strings.NewReader(`{"token":"`+token+`"}`)))
	require.Equal(t, 200, inspect.Code)
	require.Equal(t, "no-store", inspect.Header().Get("Cache-Control"))
	require.NotContains(t, inspect.Body.String(), "private account")
	require.NotContains(t, inspect.Body.String(), "group_ids")
	require.NotContains(t, inspect.Body.String(), token)
	for _, body := range []string{`{"token":"x","api_key":"synthetic-should-not-leak","platform":"openai"}`, `{"token":false,"api_key":"synthetic-should-not-leak"}`, `{"token":"x","api_key":"synthetic-should-not-leak"} {}`, `{"api_key":"` + strings.Repeat("x", 9000) + `"}`} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(body)))
		require.Equal(t, 400, rec.Code)
		require.NotContains(t, rec.Body.String(), "synthetic-should-not-leak")
	}
}

func TestAccountSubmissionDatabaseBackupDownloadProtection(t *testing.T) {
	repo := &submissionHandlerRepo{protected: map[int64]bool{1: true}}
	h := &BackupHandler{accountSubmission: service.NewAccountSubmissionService(repo, nil)}
	router := gin.New()
	router.GET("/backups/:id/download-url", h.GetDownloadURL)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/backups/old/download-url", nil))
	require.Equal(t, 403, rec.Code)
	require.Contains(t, rec.Body.String(), "SUBMISSION_BACKUP_PROTECTED")
}

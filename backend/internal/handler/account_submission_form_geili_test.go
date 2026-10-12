package handler

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type submissionFormRepoFixture struct {
	service.AccountSubmissionRepository
	prepareCalls int
}

func (r *submissionFormRepoFixture) InspectInvite(context.Context, string) (*service.AccountSubmissionInvite, error) {
	return &service.AccountSubmissionInvite{ID: 1, Status: "pending", Config: service.AccountSubmissionConfig{Name: "holder", Platform: service.PlatformAnthropic}, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (r *submissionFormRepoFixture) PrepareSubmissionProxy(context.Context, string, service.SubmissionConnectionInput) (*int64, error) {
	r.prepareCalls++
	return nil, nil
}
func TestSubmissionFormGeiliRejectsForeignOAuthSessionAndUnknownActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &submissionFormRepoFixture{}
	account := &admin.AccountHandler{}
	account.SetAccountSubmissionService(service.NewAccountSubmissionService(repo, nil))
	bridge := NewSubmissionFormHandlerGeili(&AdminHandlers{Account: account}, unavailableSubmissionSessions{})
	token := strings.Repeat("B", 42) + "A"
	for _, action := range []string{"/admin/accounts/1/refresh-token", "/admin/openai/exchange-code", "/admin/accounts/exchange-code"} {
		raw, _ := json.Marshal(map[string]any{"token": token, "action": action, "payload": map[string]any{"session_id": "foreign-session", "code": "synthetic-secret-code"}, "connection": map[string]any{"mode": "direct"}})
		req := httptest.NewRequest(http.MethodPost, "/form", strings.NewReader(string(raw)))
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		bridge.Handle(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.NotContains(t, w.Body.String(), "synthetic-secret-code")
		require.NotContains(t, w.Body.String(), "foreign-session")
		require.NotContains(t, w.Body.String(), token)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

type unavailableSubmissionSessions struct{}

func (unavailableSubmissionSessions) Save(context.Context, string, string, []byte, time.Duration) error {
	return service.ErrSubmissionUnavailable
}
func (unavailableSubmissionSessions) Take(context.Context, string, string) ([]byte, error) {
	return nil, service.ErrSubmissionInvalid
}

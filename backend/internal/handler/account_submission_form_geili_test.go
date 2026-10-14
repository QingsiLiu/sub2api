package handler

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
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
	batchKeys    []string
	batchOptions service.SubmissionOptions
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

func (r *submissionFormRepoFixture) SubmitInviteBatch(_ context.Context, _ string, keys []string, opts service.SubmissionOptions) error {
	r.batchKeys = append([]string(nil), keys...)
	r.batchOptions = opts
	return nil
}
func TestSubmissionFormGeiliBatchUsesOfficialValidationAndNoSharedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &submissionFormRepoFixture{}
	adminService := service.NewAdminService(&config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	account := admin.NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	account.SetAccountSubmissionService(service.NewAccountSubmissionService(repo, adminService))
	bridge := NewSubmissionFormHandlerGeili(&AdminHandlers{Account: account}, unavailableSubmissionSessions{})
	body := map[string]any{"token": strings.Repeat("B", 42) + "A", "action": "create-batch", "payload": map[string]any{"name": "Advanced", "notes": "shared notes", "platform": "anthropic", "type": "apikey", "concurrency": 9, "priority": 7, "rate_multiplier": 0, "proxy_id": 12345, "group_ids": []int64{12345}, "credentials": map[string]any{"api_key": "synthetic-ignored-template", "base_url": "https://8.8.8.8", "model_mapping": map[string]any{"from": "to"}}}, "items": []service.SubmissionBatchItem{{APIKey: "synthetic-first", Connection: service.SubmissionConnectionInput{Mode: "direct"}}, {APIKey: "synthetic-second", Connection: service.SubmissionConnectionInput{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: "B", Protocol: "http", Host: "8.8.8.8", Port: 8080}}}}}
	invoke := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/form", strings.NewReader(string(raw)))
		bridge.Handle(c)
		return w
	}
	w := invoke()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "synthetic-first")
	require.Equal(t, []string{"synthetic-first", "synthetic-second"}, repo.batchKeys)
	require.Zero(t, repo.prepareCalls)
	prepared := repo.batchOptions.PreparedAccount
	require.NotNil(t, prepared)
	require.Equal(t, "shared notes", *prepared.Notes)
	require.Equal(t, 9, prepared.Concurrency)
	require.Equal(t, 7, prepared.Priority)
	require.Zero(t, *prepared.RateMultiplier)
	require.Nil(t, prepared.ProxyID)
	require.Equal(t, "synthetic-first", prepared.Credentials["api_key"])
	require.Equal(t, "8.8.8.8", repo.batchOptions.PerKeyConnections[1].Proxy.Host)
	payload := body["payload"].(map[string]any)
	payload["rate_multiplier"] = -1
	repo.batchKeys = nil
	require.Equal(t, http.StatusBadRequest, invoke().Code)
	require.Empty(t, repo.batchKeys)
	payload["rate_multiplier"] = 0
	body["connection"] = map[string]any{"mode": "direct"}
	require.Equal(t, http.StatusBadRequest, invoke().Code)
	delete(body, "connection")
	payload["type"] = "oauth"
	require.Equal(t, http.StatusBadRequest, invoke().Code)
}

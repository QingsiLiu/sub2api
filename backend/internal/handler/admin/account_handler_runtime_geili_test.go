package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type manualBalanceHandlerRepoGeili struct {
	service.AccountRepository
	blocked bool
	err     error
}

func (r *manualBalanceHandlerRepoGeili) SetManualAccountBalanceStopGeili(_ context.Context, _ int64, blocked bool) error {
	r.blocked = blocked
	return r.err
}
func (*manualBalanceHandlerRepoGeili) GetByID(context.Context, int64) (*service.Account, error) {
	return &service.Account{ID: 17, Name: "manual", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: "inactive"}, nil
}
func (r *manualBalanceHandlerRepoGeili) GetAccountRuntimeGeili(context.Context, int64) (*service.AccountRuntimeStateGeili, error) {
	return &service.AccountRuntimeStateGeili{AccountID: 17, BalanceBlocked: r.blocked}, nil
}

func TestRecheckAccountGeiliValidatesExplicitModelBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountHandler{}
	router := gin.New()
	router.POST("/accounts/:id/recheck", h.RecheckAccountGeili)
	for _, tc := range []struct {
		id, body string
		status   int
	}{
		{"0", `{"model":"gpt-6"}`, http.StatusBadRequest},
		{"x", `{"model":"gpt-6"}`, http.StatusBadRequest},
		{"1", `{}`, http.StatusBadRequest},
		{"1", `{"model":"   "}`, http.StatusBadRequest},
		{"1", `{"model":"` + strings.Repeat("x", 101) + `"}`, http.StatusBadRequest},
		{"1", `{"model":"gpt-6","confirm_recharged":"true"}`, http.StatusBadRequest},
		{"1", `{"model":"gpt-6"}`, http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/accounts/"+tc.id+"/recheck", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, tc.body)
	}
}

func TestManualBalanceStopGeiliValidatesBooleanBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountHandler{}
	router := gin.New()
	router.PUT("/accounts/:id/manual-balance-stop", h.SetManualBalanceStopGeili)
	for _, tc := range []struct {
		id, body string
		status   int
	}{
		{"0", `{"blocked":true}`, http.StatusBadRequest},
		{"x", `{"blocked":true}`, http.StatusBadRequest},
		{"1", `{}`, http.StatusBadRequest},
		{"1", `{"blocked":null}`, http.StatusBadRequest},
		{"1", `{"blocked":"true"}`, http.StatusBadRequest},
		{"1", `{"blocked":1}`, http.StatusBadRequest},
		{"1", `{"blocked":true}`, http.StatusServiceUnavailable},
		{"1", `{"blocked":false}`, http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/accounts/"+tc.id+"/manual-balance-stop", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, tc.body)
	}
}

func TestManualBalanceStopGeiliReturnsStoppedDTOAndDomainErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		body   string
		err    error
		status int
	}{
		{`{"blocked":true}`, nil, http.StatusOK},
		{`{"blocked":false}`, nil, http.StatusOK},
		{`{"blocked":true}`, service.ErrAccountManualBalanceShadowGeili, http.StatusBadRequest},
		{`{"blocked":true}`, service.ErrAccountNotFound, http.StatusNotFound},
	} {
		repo := &manualBalanceHandlerRepoGeili{err: tc.err}
		h := &AccountHandler{rateLimitService: service.NewRateLimitService(repo, nil, nil, nil, nil)}
		router := gin.New()
		router.PUT("/accounts/:id/manual-balance-stop", h.SetManualBalanceStopGeili)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/accounts/17/manual-balance-stop", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code)
		if tc.status == http.StatusOK {
			var envelope struct {
				Data struct {
					Status      string `json:"status"`
					Schedulable bool   `json:"schedulable"`
					Runtime     struct {
						BalanceBlocked bool `json:"balance_blocked"`
					} `json:"runtime_state"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
			require.Equal(t, "inactive", envelope.Data.Status)
			require.False(t, envelope.Data.Schedulable)
			require.Equal(t, repo.blocked, envelope.Data.Runtime.BalanceBlocked)
		}
	}
}

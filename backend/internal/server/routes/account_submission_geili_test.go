package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAccountSubmissionPublicRateLimitSharedByRoutesAndInstances(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	routers := []*gin.Engine{gin.New(), gin.New()}
	for _, router := range routers {
		RegisterAccountSubmissionPublicRoutes(router.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{Account: &admin.AccountHandler{}}}, rdb)
	}
	request := func(index int, ip string) *httptest.ResponseRecorder {
		action := "inspect"
		if index%2 == 1 {
			action = "submit"
		}
		// Malformed JSON reaches the real decoder without touching a service or consuming an invite.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/account-submissions/"+action, strings.NewReader(`{"api_key":`))
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		routers[index%2].ServeHTTP(rec, req)
		return rec
	}
	for i := 0; i < 30; i++ {
		require.Equal(t, http.StatusBadRequest, request(i, "198.51.100.20").Code)
	}
	rec := request(30, "198.51.100.20")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, http.StatusBadRequest, request(31, "198.51.100.21").Code)
	mr.FastForward(time.Minute + time.Second)
	require.Equal(t, http.StatusBadRequest, request(32, "198.51.100.20").Code)
}

func TestAccountSubmissionPublicRateLimitFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 20 * time.Millisecond, ReadTimeout: 20 * time.Millisecond, WriteTimeout: 20 * time.Millisecond, MaxRetries: -1})
	defer func() { _ = rdb.Close() }()
	router := gin.New()
	RegisterAccountSubmissionPublicRoutes(router.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{Account: &admin.AccountHandler{}}}, rdb)
	for _, action := range []string{"inspect", "submit"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/account-submissions/"+action, strings.NewReader(`{"token":"synthetic-token","api_key":"synthetic-key"}`))
		req.RemoteAddr = "203.0.113.20:1234"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		require.NotContains(t, rec.Body.String(), "synthetic-key")
	}
}

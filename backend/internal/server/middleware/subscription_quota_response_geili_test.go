package middleware

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionQuotaGeiliProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reset := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)
	for _, google := range []bool{false, true} {
		t.Run(strconv.FormatBool(google), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			err := service.ErrDailyLimitExceeded.WithMetadata(map[string]string{"window_resets_at": reset, "remaining_usd": "0", "daily_limit_usd": "90", "daily_usage_usd": "90"})
			require.True(t, abortSubscriptionQuotaGeili(c, err, google))
			require.True(t, c.IsAborted())
			require.Equal(t, 429, w.Code)
			wait, e := strconv.Atoi(w.Header().Get("Retry-After"))
			require.NoError(t, e)
			require.InDelta(t, 3600, wait, 2)
			require.Contains(t, w.Body.String(), "DAILY_LIMIT_EXCEEDED")
			require.Contains(t, w.Body.String(), reset)
			require.NotContains(t, w.Body.String(), "metadata=map")
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			if google {
				require.Equal(t, "RESOURCE_EXHAUSTED", body["error"].(map[string]any)["status"])
			} else {
				require.Equal(t, "DAILY_LIMIT_EXCEEDED", body["code"])
			}
		})
	}
}
func TestSubscriptionQuotaGeiliDoesNotMisclassifyOtherFailures(t *testing.T) {
	for _, err := range []error{service.ErrSubscriptionExpired, service.ErrInsufficientBalance, errors.New("database unavailable")} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		require.False(t, abortSubscriptionQuotaGeili(c, err, false))
		require.False(t, c.IsAborted())
		require.Empty(t, w.Body.String())
		require.Empty(t, w.Header().Get("Retry-After"))
	}
}

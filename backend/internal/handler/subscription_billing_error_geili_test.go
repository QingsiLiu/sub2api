package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionDailyBillingErrorGeili(t *testing.T) {
	reset := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)
	err := service.ErrDailyLimitExceeded.WithMetadata(map[string]string{"window_resets_at": reset})
	status, code, msg, retry := billingErrorDetails(err)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "DAILY_LIMIT_EXCEEDED", code)
	require.Contains(t, msg, reset)
	require.InDelta(t, 3600, retry, 2)
}

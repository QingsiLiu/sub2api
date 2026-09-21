package subscription

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestContractV2AuditMirrorUsesBeijingWithUTCServer(t *testing.T) {
	previous := timezone.Name()
	require.NoError(t, timezone.Init("UTC"))
	t.Cleanup(func() { require.NoError(t, timezone.Init(previous)) })
	midnight := time.Date(2026, 9, 22, 0, 0, 0, 0, beijing)
	limit := 90.0
	t.Run("short stack resets at Beijing midnight", func(t *testing.T) {
		day := DayStart(midnight.Add(-time.Minute))
		lot := Lot{Status: "active", StartsAt: midnight.Add(-time.Minute), ExpiresAt: midnight.Add(time.Hour), DailyLimitUSD: &limit, DailyWindowStart: &day, DailyUsageUSD: 80, LifetimeUsageUSD: 80}
		NormalizeContractLot(&lot, midnight, true)
		require.Zero(t, lot.DailyUsageUSD)
		require.Equal(t, midnight, *lot.DailyWindowStart)
		require.Equal(t, 80.0, lot.LifetimeUsageUSD)
	})
	t.Run("UTC midnight does not reset again", func(t *testing.T) {
		day := midnight
		lot := Lot{Status: "active", StartsAt: midnight.Add(-48 * time.Hour), ExpiresAt: midnight.Add(48 * time.Hour), DailyLimitUSD: &limit, DailyWindowStart: &day, DailyUsageUSD: 20, LifetimeUsageUSD: 80}
		NormalizeContractLot(&lot, midnight.Add(8*time.Hour), true)
		require.Equal(t, 20.0, lot.DailyUsageUSD)
		require.Equal(t, midnight, *lot.DailyWindowStart)
		require.Equal(t, 80.0, lot.LifetimeUsageUSD)
	})
}

package subscription

import (
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
)

func TestUsageLockIsNarrowAndManagementLockRemainsStrong(t *testing.T) {
	usage := entsql.Dialect(dialect.Postgres).Select("id").From(entsql.Table("user_subscriptions"))
	lockUsageRows(usage)
	q, _ := usage.Query()
	require.Contains(t, q, "FOR NO KEY UPDATE")
	management := entsql.Dialect(dialect.Postgres).Select("id").From(entsql.Table("user_subscriptions"))
	LockRows(management)
	q, _ = management.Query()
	require.Contains(t, q, "FOR UPDATE")
	sqlite := entsql.Dialect(dialect.SQLite).Select("id").From(entsql.Table("user_subscriptions"))
	lockUsageRows(sqlite)
	q, _ = sqlite.Query()
	require.NotContains(t, q, "FOR")
}

func TestUsageWindowPrecisionPreservesFirstSettlementAndLegacySnapshots(t *testing.T) {
	now := time.Date(2026, 9, 26, 1, 0, 0, 123456321, time.UTC)
	lot := Lot{Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(40 * 24 * time.Hour)}
	lot.Normalize(now, true)
	require.Equal(t, 0, lot.WeeklyWindowStart.Nanosecond()%1000)
	require.Equal(t, 0, lot.MonthlyWindowStart.Nanosecond()%1000)
	require.True(t, sameWindow(&now, lot.WeeklyWindowStart), "legacy JSON nanoseconds compare at SQL precision")
	later := now.Add(time.Microsecond)
	require.False(t, sameWindow(&later, lot.WeeklyWindowStart), "do not collapse distinct persisted windows")
	require.False(t, sameWindow(nil, lot.WeeklyWindowStart))
	require.True(t, sameWindow(nil, nil))
	next := now.Add(7 * 24 * time.Hour)
	lot.WeeklyUsageUSD = 12
	lot.Normalize(next, false)
	require.Zero(t, lot.WeeklyUsageUSD)
	require.Equal(t, 0, lot.WeeklyWindowStart.Nanosecond()%1000)
}

func TestLegacyWindowTimeRoundsLikePostgres(t *testing.T) {
	base := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct{ nano, want int }{{499, 0}, {500, 0}, {501, 1000}, {789, 1000}, {999, 1000}, {1499, 1000}, {1500, 2000}, {1501, 2000}, {999999999, 1000000000}} {
		require.Equal(t, base.Add(time.Duration(test.want)), postgresWindowTime(base.Add(time.Duration(test.nano))))
	}
}

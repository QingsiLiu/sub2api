//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestAdminResetQuotaV2UsesBeijingDayInsteadOfConfiguredZone(t *testing.T) {
	previous := timezone.Location().String()
	require.NoError(t, timezone.Init("UTC"))
	t.Cleanup(func() { require.NoError(t, timezone.Init(previous)) })
	at := time.Date(2026, 9, 21, 18, 30, 0, 0, time.UTC)
	stub := &resetQuotaUserSubRepoStub{sub: &UserSubscription{ID: 1, UserID: 10, Contract: &geilisub.Contract{Mode: geilisub.ContractModeV2}}}
	svc := newResetQuotaSvc(stub)
	t.Cleanup(svc.Stop)
	svc.now = func() time.Time { return at }
	_, err := svc.AdminResetQuota(context.Background(), 1, true, false, false)
	require.NoError(t, err)
	require.True(t, stub.dailyStart.Equal(geilisub.DayStart(at)), "a reset after Beijing midnight must target today's ledger even if server timezone is UTC")
	require.Equal(t, at, stub.periodicStart)
}

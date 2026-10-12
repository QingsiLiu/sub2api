package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func encryptedRouteWaitFixtureGeili(t *testing.T) (*OpenAIGatewayService, *Account, context.Context, int64, *openAIEncryptedRouteStateGeili) {
	t.Helper()
	svc, a := encryptedRecoveryFixtureGeili(nil)
	a.Status = StatusActive
	a.Schedulable = true
	a.GroupIDs = []int64{131}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*a}}
	svc.cache = &stubGatewayCache{}
	state := &openAIEncryptedRouteStateGeili{service: svc, userID: 3885, enabled: true, protected: true, owner: &OpenAIEncryptedRouteGeili{AccountID: a.ID, GroupID: 131, Model: canonicalOpenAIAccountSchedulingModel(a, "gpt-6-astra"), BackendFingerprint: encryptedBackendFingerprintGeili(a)}}
	ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, state)
	return svc, a, ctx, 131, state
}
func encryptedRouteWaitSelectGeili(s *OpenAIGatewayService, ctx context.Context, group int64) (*AccountSelectionResult, bool, error) {
	return s.selectOpenAIEncryptedRouteGeili(ctx, &group, "session", "gpt-6-astra", PlatformOpenAI, nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, "", false, false)
}
func TestEncryptedRouteWaitGeiliShortCooldownRechecksOrigin(t *testing.T) {
	for _, runtime := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "model-runtime"}[runtime], func(t *testing.T) {
			svc, a, ctx, group, state := encryptedRouteWaitFixtureGeili(t)
			until := time.Now().Add(40 * time.Millisecond)
			if runtime {
				transient := svc.getOpenAIAccountModelTransientState()
				key, _ := openAIAccountModelTransientKey(a.ID, openAIAccountModelTransientModel(state.owner.Model))
				transient.entries[key] = openAIAccountModelTransientEntry{lastFailure: time.Now(), blockUntil: until}
			} else {
				a.RateLimitResetAt = &until
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*a}}
			}
			start := time.Now()
			selection, handled, err := encryptedRouteWaitSelectGeili(svc, ctx, group)
			require.NoError(t, err)
			require.True(t, handled)
			require.NotNil(t, selection)
			require.Equal(t, a.ID, selection.Account.ID)
			require.True(t, selection.Acquired)
			require.GreaterOrEqual(t, time.Since(start), 30*time.Millisecond)
			require.Less(t, state.waitSpent, encryptedRouteMaxWaitGeili)
		})
	}
}
func TestEncryptedRouteWaitGeiliPermanentAndLongFailuresDoNotWait(t *testing.T) {
	for _, kind := range []string{"disabled", "unauthorized", "long", "exhausted", "request-deadline"} {
		t.Run(kind, func(t *testing.T) {
			svc, a, ctx, group, state := encryptedRouteWaitFixtureGeili(t)
			until := time.Now().Add(100 * time.Millisecond)
			a.RateLimitResetAt = &until
			switch kind {
			case "disabled":
				a.Schedulable = false
			case "unauthorized":
				a.TempUnschedulableUntil = &until
				a.TempUnschedulableReason = "HTTP 401 unauthorized"
			case "long":
				until = time.Now().Add(time.Minute)
			case "exhausted":
				state.waitSpent = encryptedRouteMaxWaitGeili - 10*time.Millisecond
			case "request-deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*a}}
			start := time.Now()
			selection, handled, err := encryptedRouteWaitSelectGeili(svc, ctx, group)
			require.Error(t, err)
			require.True(t, handled)
			require.Nil(t, selection)
			require.Less(t, time.Since(start), 50*time.Millisecond)
		})
	}
}
func TestEncryptedRouteWaitGeiliQueueSharesCooldownBudget(t *testing.T) {
	svc, a, ctx, group, state := encryptedRouteWaitFixtureGeili(t)
	until := time.Now().Add(30 * time.Millisecond)
	a.OverloadUntil = &until
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*a}}
	svc.cfg.Gateway.Scheduling.StickySessionWaitTimeout = time.Minute
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{a.ID: false}})
	state.waitSpent = 14 * time.Second
	selection, _, err := encryptedRouteWaitSelectGeili(svc, ctx, group)
	require.NoError(t, err)
	require.NotNil(t, selection.WaitPlan)
	require.Less(t, selection.WaitPlan.Timeout, time.Second)
	require.InDelta(t, 15*time.Second, state.waitSpent, float64(time.Millisecond))
	require.NoError(t, ctx.Err())
	// A retry cannot obtain another independent full timeout.
	selection, _, err = encryptedRouteWaitSelectGeili(svc, ctx, group)
	require.Error(t, err)
	require.Nil(t, selection)
}

//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountRecoveryGeiliRestoredAccountMayRetryOnlyAfterProbe(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) { return true, nil }, nil)
	defer recovery.Stop()
	ctx := WithRequestRecovery(context.Background(), time.Minute)
	excluded := map[int64]struct{}{1: {}, 2: {}}
	require.Equal(t, excluded, recoveredAccountExclusionsGeili(ctx, excluded))
	require.True(t, recovery.RecoverGroupGeili(ctx, 7, "claude-sonnet", nil))
	require.Equal(t, map[int64]struct{}{2: {}}, recoveredAccountExclusionsGeili(ctx, excluded))
	require.Len(t, excluded, 2)
	require.False(t, recovery.RecoverGroupGeili(ctx, 7, "claude-sonnet", nil), "same request must not repeat the group recovery")
}

func TestAccountRecoveryGeiliFreshGroupMembershipRequired(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	var probed bool
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) { probed = true; return true, nil }, nil)
	defer recovery.Stop()
	account, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	repo.mu.Lock()
	changed := repo.accounts[1]
	changed.GroupIDs = []int64{8}
	repo.accounts[1] = changed
	repo.mu.Unlock()
	err = recovery.check(context.Background(), account, "claude-sonnet", "automatic", false, func(_ context.Context, fresh *Account) bool {
		for _, id := range fresh.GroupIDs {
			if id == 7 {
				return true
			}
		}
		return false
	})
	require.ErrorIs(t, err, ErrAccountRuntimeConflictGeili)
	require.False(t, probed)
}

func TestAccountRecoveryGeiliDoesNotAppendLegacySelectionBackoff(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), time.Minute)
	require.True(t, BeginRequestRecovery(ctx))
	state := requestRecoveryFromContext(ctx)
	state.mu.Lock()
	state.accountRecoveryDeadline = time.Now().Add(30 * time.Second)
	state.mu.Unlock()
	require.False(t, ClaimRequestRecoverySelectionWait(ctx), "standby recovery is already the bounded selection wait")
	require.Greater(t, RequestRecoveryRemaining(ctx), 30*time.Second, "recovered customer generation retains the original request budget")
}

func TestAccountRecoveryGeiliMappedStandbyUsesNormalChannelPricingBasis(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI} {
		for _, basis := range []string{BillingModelSourceRequested, BillingModelSourceUpstream} {
			t.Run(platform+"/"+basis, func(t *testing.T) {
				groupID := int64(10)
				channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, RestrictModels: true, BillingModelSource: basis, ModelPricing: []ChannelModelPricing{{Platform: platform, Models: []string{"public-alias"}}}}
				channels := newTestChannelService(makeStandardRepo(channel, map[int64]string{groupID: platform}))
				account := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"public-alias": "supplier-model"}}}
				gateway := &GatewayService{channelService: channels}
				openai := &OpenAIGatewayService{channelService: channels}
				ctx := context.Background()
				require.False(t, gateway.checkChannelPricingRestriction(ctx, &groupID, "public-alias"))
				require.False(t, openai.checkChannelPricingRestriction(ctx, &groupID, "public-alias"))
				require.Equal(t, basis == BillingModelSourceUpstream, gateway.recoveryUpstreamChannelRestrictedGeili(ctx, &groupID, account, "public-alias"))
				require.Equal(t, basis == BillingModelSourceUpstream, openai.recoveryUpstreamChannelRestrictedGeili(ctx, &groupID, account, "public-alias"))
				if basis == BillingModelSourceRequested {
					require.True(t, gateway.checkChannelPricingRestriction(ctx, &groupID, "forbidden-model"), "public pricing restriction is still enforced before recovery")
				}
			})
		}
	}
}

func TestAccountRecoveryGeiliNewCallerCanWakeJobWithDifferentAdmission(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) { return true, nil }, nil)
	defer recovery.Stop()
	first, cancelFirst := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelFirst()
	require.False(t, recovery.RecoverGroupGeili(first, 7, "claude-sonnet", func(context.Context, *Account) bool { return false }))
	second, cancelSecond := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecond()
	require.True(t, recovery.RecoverGroupGeili(second, 7, "claude-sonnet", func(context.Context, *Account) bool { return true }), "a first caller's profit/capability veto must not freeze the shared group job for later authorized callers")
}

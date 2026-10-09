//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestRecoveryGeiliAliasLongModelClassification(t *testing.T) {
	groupID := int64(10)
	decision := CompositeRouteDecision{TargetGroupID: &groupID, TargetPlatform: PlatformAnthropic, PublicModel: "public-claude", UpstreamModel: "public-claude"}
	for _, tc := range []struct {
		name     string
		channel  map[string]string
		accounts []Account
		want     bool
	}{
		{name: "account alias", accounts: []Account{longAliasAccountGeili(PlatformAnthropic, map[string]any{"public-claude": "claude-opus-5-5"})}, want: true},
		{name: "channel alias", channel: map[string]string{"public-claude": "claude-opus-5-5"}, want: true},
		{name: "channel then account", channel: map[string]string{"public-claude": "private-opus"}, accounts: []Account{longAliasAccountGeili(PlatformAnthropic, map[string]any{"private-opus": "claude-opus-5-5"})}, want: true},
		{name: "channel then ordinary account", channel: map[string]string{"public-claude": "private-sonnet"}, accounts: []Account{longAliasAccountGeili(PlatformAnthropic, map[string]any{"private-sonnet": "claude-sonnet-4-5", "other-model": "claude-opus-5-5"})}},
		{name: "unrelated long model", accounts: []Account{longAliasAccountGeili(PlatformAnthropic, map[string]any{"public-claude": "claude-sonnet-4-5", "other-model": "claude-opus-5-5"})}},
		{name: "wrong provider", accounts: []Account{longAliasAccountGeili(PlatformOpenAI, map[string]any{"public-claude": "gpt-6-astra"})}},
		{name: "disabled account", accounts: []Account{{Platform: PlatformAnthropic, Status: StatusError, Schedulable: false, Credentials: map[string]any{"model_mapping": map[string]any{"public-claude": "claude-opus-5-5"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := NewCompositeRouteResolver(nil)
			resolver.accountRepo = &routingAccountRepo{accounts: tc.accounts}
			if tc.channel != nil {
				channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, ModelMapping: map[string]map[string]string{PlatformAnthropic: tc.channel}}
				resolver.pricing = NewModelPricingResolver(newTestChannelService(makeStandardRepo(channel, map[int64]string{groupID: PlatformAnthropic})), nil)
			}
			require.Equal(t, tc.want, resolver.LongThinkingRoute(context.Background(), decision))
		})
	}

	decision.TargetPlatform = PlatformOpenAI
	resolver := NewCompositeRouteResolver(nil)
	resolver.accountRepo = &routingAccountRepo{accounts: []Account{longAliasAccountGeili(PlatformOpenAI, map[string]any{"public-claude": "gpt-6-astra"})}}
	require.True(t, resolver.LongThinkingRoute(context.Background(), decision))
}

func longAliasAccountGeili(platform string, mapping map[string]any) Account {
	return Account{Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": mapping}}
}

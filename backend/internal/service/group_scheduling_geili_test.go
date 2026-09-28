package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGroupSchedulingGeiliProjectionIsLocal(t *testing.T) {
	a := Account{ID: 1, Platform: PlatformOpenAI, Priority: 70, AccountGroups: []AccountGroup{
		{GroupID: 4, Priority: 0, PriorityEnabled: true, PriorityMode: GroupPriorityFixed},
		{GroupID: 126, Priority: 20, PriorityEnabled: true, PriorityMode: GroupPriorityAuto},
	}}
	g4, g126 := int64(4), int64(126)
	four := projectGroupPrioritiesGeili([]Account{a}, &g4)
	other := projectGroupPrioritiesGeili(four, &g126)
	require.Equal(t, 0, four[0].Priority)
	require.Equal(t, 20, other[0].Priority)
	require.Equal(t, 70, a.Priority)
	require.Equal(t, 70, four[0].DefaultSchedulingPriority())
	a.AccountGroups[0].PriorityEnabled = false
	p, mode := EffectiveGroupPriority(&a, 4)
	require.Equal(t, 70, p)
	require.Equal(t, GroupPriorityInherit, mode)
	a.AccountGroups[0].PriorityEnabled = true
	a.AccountGroups[0].PriorityMode = GroupPriorityInherit
	p, _ = EffectiveGroupPriority(&a, 4)
	require.Equal(t, 70, p)
	p, _ = EffectiveGroupPriority(&a, 90)
	require.Equal(t, 70, p)
}

func TestGroupSchedulingGeiliNativeSelectionBothEngines(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		t.Run(advanced, func(t *testing.T) {
			ctx := context.Background()
			gid := int64(4)
			accounts := []Account{
				{ID: 1, Name: "first", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 100, GroupIDs: []int64{4, 126}, AccountGroups: []AccountGroup{{GroupID: 4, Priority: 0, PriorityEnabled: true, PriorityMode: GroupPriorityFixed}, {GroupID: 126, Priority: 100, PriorityEnabled: true, PriorityMode: GroupPriorityAuto}}},
				{ID: 2, Name: "second", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{4, 126}, AccountGroups: []AccountGroup{{GroupID: 4, Priority: 50, PriorityEnabled: true, PriorityMode: GroupPriorityAuto}, {GroupID: 126, Priority: 0, PriorityEnabled: true, PriorityMode: GroupPriorityFixed}}},
			}
			cfg := newSchedulerTestSubscriptionPriorityConfig()
			// Deliberately adverse score: the explicit layer must win even at high load.
			cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 0
			cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Load = 10
			svc := &OpenAIGatewayService{accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}, cache: &schedulerTestGatewayCache{}, cfg: cfg, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 90}, 2: {AccountID: 2, LoadRate: 0}}})}
			for _, test := range []struct {
				gid  int64
				want int64
			}{{4, 1}, {126, 2}} {
				gid = test.gid
				selected, _, err := svc.SelectAccountWithScheduler(ctx, &gid, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.Equal(t, test.want, selected.Account.ID)
				if selected.ReleaseFunc != nil {
					selected.ReleaseFunc()
				}
			}
			require.Equal(t, 100, accounts[0].Priority)
			// Hard eligibility still wins over a fixed tier.
			accounts[0].Schedulable = false
			gid = 4
			svc.accountRepo = schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}
			selected, _, err := svc.SelectAccountWithScheduler(ctx, &gid, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selected.Account.ID)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
		})
	}
}

func TestGroupSchedulingGeiliValidationAndRevision(t *testing.T) {
	v := int64(0)
	zero := 0
	input := GroupSchedulingUpdateGeili{ExpectedVersion: &v, ExpectedMembersVersion: GroupSchedulingMembersVersionGeili(nil), Source: "manual", Rows: []GroupSchedulingUpdateRowGeili{{AccountID: 1, Mode: GroupPriorityFixed, Priority: &zero}}}
	require.NoError(t, input.Validate())
	input.Source = "automatic"
	require.Error(t, input.Validate())
	input.Rows[0].Mode = GroupPriorityAuto
	require.NoError(t, input.Validate())
	enabled := true
	input.Enabled = &enabled
	require.Error(t, input.Validate())
	a := []GroupSchedulingRowGeili{{AccountID: 1, Mode: "auto", GroupPriority: 5}, {AccountID: 2, Mode: "fixed", GroupPriority: 0}}
	b := []GroupSchedulingRowGeili{a[1], a[0]}
	require.Equal(t, GroupSchedulingMembersVersionGeili(a), GroupSchedulingMembersVersionGeili(b))
	b[0].GroupPriority = 3
	require.NotEqual(t, GroupSchedulingMembersVersionGeili(a), GroupSchedulingMembersVersionGeili(b))
}

func TestGroupSchedulingGeiliFullTierFallsBack(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		for _, batch := range []bool{false, true} {
			t.Run(advanced+map[bool]string{true: "-batch", false: "-serial"}[batch], func(t *testing.T) {
				gid := int64(4)
				accounts := []Account{}
				for i, p := range []int{0, 50} {
					id := int64(i + 1)
					accounts = append(accounts, Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{4}, AccountGroups: []AccountGroup{{GroupID: 4, Priority: p, PriorityMode: "auto", PriorityEnabled: true}}})
				}
				cfg := newSchedulerTestSubscriptionPriorityConfig()
				cfg.Gateway.Scheduling.LoadBatchEnabled = batch
				svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{}, cfg: cfg, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true}, loadMap: map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 100}, 2: {AccountID: 2}}})}
				selected, _, err := svc.SelectAccountWithScheduler(context.Background(), &gid, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.Equal(t, int64(2), selected.Account.ID)
				if selected.ReleaseFunc != nil {
					selected.ReleaseFunc()
				}
			})
		}
	}
}

func TestGroupSchedulingGeiliHealthyStickyIsPreserved(t *testing.T) {
	ctx := context.Background()
	gid := int64(4)
	accounts := []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{4}, AccountGroups: []AccountGroup{{GroupID: 4, Priority: 0, PriorityMode: "fixed", PriorityEnabled: true}}}, {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 10, GroupIDs: []int64{4}, AccountGroups: []AccountGroup{{GroupID: 4, Priority: 100, PriorityMode: "auto", PriorityEnabled: true}}}}
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{}, cfg: newSchedulerTestSubscriptionPriorityConfig(), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("false"), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
	require.NoError(t, svc.setStickySessionAccountID(ctx, &gid, "kept-session", 2, openaiStickySessionTTL))
	selected, decision, err := svc.SelectAccountWithScheduler(ctx, &gid, "", "kept-session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	require.True(t, decision.StickySessionHit)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
}

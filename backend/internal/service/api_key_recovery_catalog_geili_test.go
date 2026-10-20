//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type stoppedTextCatalogRepoGeili struct{ *recoveryRuntimeRepoGeili }

func (r *stoppedTextCatalogRepoGeili) ListModelAvailabilityCandidates(ctx context.Context, gid *int64, platforms []string, _ bool) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Account
	for _, a := range r.accounts {
		if a.Status != StatusActive || !a.Schedulable || gid != nil && !accountRecoveryGroupMemberGeili(&a, *gid) {
			continue
		}
		for _, platform := range platforms {
			if a.Platform == platform {
				out = append(out, *copyRecoveryAccountGeili(a))
				break
			}
		}
	}
	return out, nil
}

func stoppedTextCatalogFixtureGeili(a Account) (*APIKeyService, *APIKey, *CompositeRouteResolver, *Group, *stoppedTextCatalogRepoGeili) {
	group := &Group{ID: 7, Status: StatusActive, Platform: PlatformAnthropic}
	key := &APIKey{UserID: 1, BillingSource: BillingSourceBalance, RoutingMode: KeyRoutingSingle, GroupID: &group.ID}
	svc := &APIKeyService{userRepo: &routingUserRepo{user: &User{ID: 1}}, groupRepo: &routingGroupRepo{groups: map[int64]*Group{7: group}}}
	repo := &stoppedTextCatalogRepoGeili{newRecoveryRuntimeRepoGeili(a)}
	resolver := NewCompositeRouteResolver(nil)
	resolver.accountRepo = repo
	resolver.SetRouteValidation(svc.groupRepo, NewModelPricingResolver(nil, NewBillingService(&config.Config{}, nil)))
	return svc, key, resolver, group, repo
}

func stoppedTextDeclaredAccountGeili() Account {
	a := stoppedRecoveryAccountGeili(1, 7)
	a.Credentials["model_mapping"] = map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"}
	return a
}

func TestExplicitKeyRecoveryCatalogGeiliStoppedDeclaredTextRequestOnly(t *testing.T) {
	for _, status := range []string{StatusError} {
		t.Run(status, func(t *testing.T) {
			a := stoppedTextDeclaredAccountGeili()
			a.Status = status
			a.Schedulable = false
			svc, key, resolver, _, repo := stoppedTextCatalogFixtureGeili(a)
			old, err := repo.ListByGroup(context.Background(), 7)
			require.NoError(t, err)
			if status != StatusActive {
				require.Empty(t, old, "legacy active-only list cannot discover this stop")
			}
			decisions, err := svc.ResolveExplicitKeyRoutes(context.Background(), key, "claude-sonnet-4-5", CompositeRouteEndpointMessages, resolver, true)
			require.NoError(t, err)
			require.Len(t, decisions, 1)
			require.Equal(t, int64(7), *decisions[0].TargetGroupID)
			models, err := svc.ExplicitKeyModels(context.Background(), key, resolver)
			require.NoError(t, err)
			require.Empty(t, models, "stopped declarations are request-only, never advertised healthy")
			_, err = svc.ResolveExplicitKeyRoutes(context.Background(), key, "claude-sonnet-4-5", CompositeRouteEndpointMessages, resolver)
			require.Error(t, err, "unmarked service callers must retain their original catalog contract")
		})
	}
}

func TestExplicitKeyRecoveryCatalogGeiliPreservesAdmissionBoundaries(t *testing.T) {
	for _, scenario := range []string{"unknown", "allowlist", "other_group", "other_platform", "manual_stop", "future_probe_backoff", "unpriced", "group_inactive", "unauthorized", "embeddings", "media", "compact_optout", "continuity_optout"} {
		t.Run(scenario, func(t *testing.T) {
			a := stoppedTextDeclaredAccountGeili()
			model, endpoint, allow := "claude-sonnet-4-5", CompositeRouteEndpointMessages, true
			if scenario == "other_group" {
				a.GroupIDs = []int64{8}
			}
			if scenario == "other_platform" {
				a.Platform = PlatformOpenAI
			}
			if scenario == "manual_stop" {
				a.Status = "inactive"
			}
			if scenario == "future_probe_backoff" {
				later := time.Now().Add(time.Hour)
				a.RuntimeState = &AccountRuntimeStateGeili{AccountID: a.ID, Models: map[string]AccountModelHealthGeili{model: {LastProbe: &AccountProbeResultGeili{Model: model, NextProbeAt: &later}}}}
			}
			if scenario == "unknown" {
				model = "not-declared"
			}
			if scenario == "unpriced" {
				model = "fixture-unpriced"
				a.Credentials["model_mapping"] = map[string]any{model: model}
			}
			if scenario == "embeddings" {
				endpoint = CompositeRouteEndpointEmbeddings
			}
			if scenario == "media" {
				model = "gpt-image-2"
				a.Credentials["model_mapping"] = map[string]any{model: model}
				endpoint = CompositeRouteEndpointImages
			}
			if scenario == "compact_optout" || scenario == "continuity_optout" {
				allow = false
			}
			svc, key, resolver, group, _ := stoppedTextCatalogFixtureGeili(a)
			if scenario == "allowlist" {
				group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"other-model"}}
			}
			if scenario == "group_inactive" {
				group.Status = "inactive"
			}
			if scenario == "unauthorized" {
				group.IsExclusive = true
			}
			_, err := svc.ResolveExplicitKeyRoutes(context.Background(), key, model, endpoint, resolver, allow)
			require.Error(t, err)
			if scenario == "unpriced" {
				require.ErrorIs(t, err, ErrCompositeModelUnpriced)
			}
		})
	}
}

func TestExplicitKeyRecoveryCatalogGeiliBodyToolsAndContinuity(t *testing.T) {
	require.True(t, ExplicitKeyStoppedTextRecoveryAllowedGeili(context.Background(), []byte(`{"tools":[{"type":"function","name":"f"}],"input":[{"type":"function_call_output","call_id":"x","output":"OK"}]}`)))
	for _, body := range []string{`{"previous_response_id":"resp_retained"}`, `{"tools":[{"type":"image_generation"}]}`} {
		require.False(t, ExplicitKeyStoppedTextRecoveryAllowedGeili(context.Background(), []byte(body)))
	}
}

func TestExplicitKeyRecoveryCatalogGeiliPaginatedWorkerDiscoversLaterManualStop(t *testing.T) {
	var accounts []Account
	for i := 1; i <= 100; i++ {
		a := stoppedTextDeclaredAccountGeili()
		a.ID = int64(i)
		a.Priority = i
		a.Status = StatusActive
		a.Schedulable = true
		accounts = append(accounts, a)
	}
	paused := stoppedTextDeclaredAccountGeili()
	paused.ID = 101
	paused.Priority = 101
	paused.Status = StatusError
	accounts = append(accounts, paused)
	repo := newRecoveryRuntimeRepoGeili(accounts...)
	svc := NewAccountRecoveryServiceGeili(repo, func(ctx context.Context, a *Account, model string) (bool, error) {
		if a.ID != 101 {
			return false, fmt.Errorf("unexpected healthy candidate")
		}
		return true, nil
	}, nil)
	defer svc.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.True(t, svc.RecoverGroupGeili(ctx, 7, "claude-sonnet-4-5", nil))
	actual, _ := repo.GetByID(context.Background(), 101)
	require.Equal(t, StatusActive, actual.Status)
	require.True(t, actual.Schedulable)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.records, 1)
	require.GreaterOrEqual(t, len(repo.groups), 2)
}

func TestExplicitKeyRecoveryCatalogGeiliSharesDiscoveryClockWithoutStartingUpstreamBudget(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), 600*time.Second)
	state := requestRecoveryFromContext(ctx)
	now := time.Now()
	state.now = func() time.Time { return now }
	require.Equal(t, 30*time.Second, accountRecoveryForegroundRemainingGeili(ctx, true))
	initial := state.accountRecoveryDeadline
	require.True(t, state.deadline.IsZero(), "a local catalog check does not start the generation retry window")
	now = now.Add(3 * time.Second)
	require.Equal(t, 27*time.Second, accountRecoveryForegroundRemainingGeili(ctx, true), "a second group cannot replenish time spent scanning")
	require.Equal(t, initial, state.accountRecoveryDeadline)
	require.True(t, BeginRequestRecovery(ctx))
	require.Equal(t, now.Add(600*time.Second), state.deadline, "existing retry budget begins only when an upstream recovery is attempted")
	now = now.Add(25 * time.Second)
	require.Equal(t, 2*time.Second, accountRecoveryForegroundRemainingGeili(ctx, true), "catalog scans and scheduler wait share remaining time")
	now = now.Add(3 * time.Second)
	require.Zero(t, accountRecoveryForegroundRemainingGeili(ctx, true))
	require.Equal(t, initial, state.accountRecoveryDeadline)
}

type recoveryCatalogClockRepoGeili struct {
	*stoppedTextCatalogRepoGeili
	calls  int
	budget time.Duration
}

func (r *recoveryCatalogClockRepoGeili) ListWithFilters(ctx context.Context, p pagination.PaginationParams, platform, accountType, status, search string, gid int64, privacy string) ([]Account, *pagination.PaginationResult, error) {
	r.calls++
	if deadline, ok := ctx.Deadline(); ok {
		r.budget = time.Until(deadline)
	}
	return r.recoveryRuntimeRepoGeili.ListWithFilters(ctx, p, platform, accountType, status, search, gid, privacy)
}
func TestExplicitKeyRecoveryCatalogGeiliPublicScanUsesRemainingForegroundClock(t *testing.T) {
	svc, key, resolver, _, repo := stoppedTextCatalogFixtureGeili(stoppedTextDeclaredAccountGeili())
	spy := &recoveryCatalogClockRepoGeili{stoppedTextCatalogRepoGeili: repo}
	resolver.accountRepo = spy
	ctx := WithRequestRecovery(context.Background(), 600*time.Second)
	state := requestRecoveryFromContext(ctx)
	now := time.Now()
	state.now = func() time.Time { return now }
	_, err := svc.ResolveExplicitKeyRoutes(ctx, key, "claude-sonnet-4-5", CompositeRouteEndpointMessages, resolver, true)
	require.NoError(t, err)
	require.True(t, state.deadline.IsZero())
	initial := state.accountRecoveryDeadline
	now = now.Add(28 * time.Second)
	_, err = svc.ResolveExplicitKeyRoutes(ctx, key, "claude-sonnet-4-5", CompositeRouteEndpointMessages, resolver, true)
	require.NoError(t, err)
	require.LessOrEqual(t, spy.budget, 2*time.Second)
	require.Greater(t, spy.budget, time.Second)
	require.Equal(t, initial, state.accountRecoveryDeadline)
	require.True(t, state.deadline.IsZero())
	now = now.Add(3 * time.Second)
	calls := spy.calls
	_, err = svc.ResolveExplicitKeyRoutes(ctx, key, "claude-sonnet-4-5", CompositeRouteEndpointMessages, resolver, true)
	require.Error(t, err)
	require.Equal(t, calls, spy.calls, "expired shared clock must stop catalog I/O")
}

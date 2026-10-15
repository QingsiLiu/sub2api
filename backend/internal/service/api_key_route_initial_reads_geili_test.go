package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type initialRouteUserRepoGeili struct {
	UserRepository
	get func(context.Context, int64) (*User, error)
}

func (r *initialRouteUserRepoGeili) GetByID(ctx context.Context, id int64) (*User, error) {
	return r.get(ctx, id)
}

type initialRouteGroupRepoGeili struct {
	GroupRepository
	get func(context.Context, int64) (*Group, error)
}

func (r *initialRouteGroupRepoGeili) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	return r.get(ctx, id)
}

func TestExplicitRouteInitialGeiliOverlapsAndReadsFreshValues(t *testing.T) {
	// A rendezvous proves overlap without asserting noisy wall-clock timing.
	for _, version := range []int64{1, 2} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		userStarted, groupStarted := make(chan struct{}), make(chan struct{})
		s := &APIKeyService{
			userRepo: &initialRouteUserRepoGeili{get: func(ctx context.Context, id int64) (*User, error) {
				close(userStarted)
				select {
				case <-groupStarted:
					return &User{ID: id, Balance: float64(version)}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}},
			groupRepo: &initialRouteGroupRepoGeili{get: func(ctx context.Context, id int64) (*Group, error) {
				close(groupStarted)
				select {
				case <-userStarted:
					return &Group{ID: id, RateMultiplier: float64(version)}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}},
		}
		read, err := s.readExplicitRouteInitialGeili(ctx, 17, []int64{29})
		cancel()
		require.NoError(t, err)
		require.NoError(t, read.firstGroupErr)
		require.Equal(t, int64(17), read.owner.ID)
		require.Equal(t, int64(29), read.firstGroup.ID)
		require.Equal(t, float64(version), read.owner.Balance)
		require.Equal(t, float64(version), read.firstGroup.RateMultiplier)
	}
}

func TestExplicitRouteInitialGeiliOwnerFailureCancelsAndJoins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, finished := make(chan struct{}), make(chan struct{})
	want := errors.New("owner unavailable")
	s := &APIKeyService{
		userRepo: &initialRouteUserRepoGeili{get: func(ctx context.Context, _ int64) (*User, error) {
			select {
			case <-started:
				return nil, want
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}},
		groupRepo: &initialRouteGroupRepoGeili{get: func(ctx context.Context, _ int64) (*Group, error) {
			close(started)
			<-ctx.Done()
			close(finished)
			return nil, ctx.Err()
		}},
	}
	_, err := s.readExplicitRouteInitialGeili(ctx, 1, []int64{7})
	require.ErrorIs(t, err, want)
	select {
	case <-finished:
	default:
		t.Fatal("first group database read escaped the request")
	}
}

func TestExplicitRouteInitialGeiliNoGroupsKeepsOwnerLookup(t *testing.T) {
	want := errors.New("owner missing")
	s := &APIKeyService{userRepo: &initialRouteUserRepoGeili{get: func(context.Context, int64) (*User, error) {
		return nil, want
	}}}
	_, err := s.readExplicitRouteInitialGeili(context.Background(), 1, nil)
	require.ErrorIs(t, err, want)
}

func TestExplicitRouteInitialGeiliGroupPanicReachesRequestRecovery(t *testing.T) {
	s := &APIKeyService{
		userRepo: &routingUserRepo{user: &User{ID: 1}},
		groupRepo: &initialRouteGroupRepoGeili{get: func(context.Context, int64) (*Group, error) {
			panic("group read failed")
		}},
	}
	require.PanicsWithValue(t, "group read failed", func() {
		_, _ = s.readExplicitRouteInitialGeili(context.Background(), 1, []int64{7})
	})
	want := errors.New("owner missing")
	s.userRepo = &initialRouteUserRepoGeili{get: func(context.Context, int64) (*User, error) {
		return nil, want
	}}
	_, err := s.readExplicitRouteInitialGeili(context.Background(), 1, []int64{7})
	require.ErrorIs(t, err, want, "owner error still takes precedence")
}

func TestExplicitRouteInitialGeiliCompositeOrderAndFirstFailure(t *testing.T) {
	model := "grok-imagine"
	var mu sync.Mutex
	reads := map[int64]int{}
	groups := map[int64]*Group{}
	for _, id := range []int64{8, 9} {
		groups[id] = &Group{ID: id, Status: StatusActive, Platform: PlatformGrok,
			ImagePrice1K: ptrFloatRoute(.05), ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{model}}}
	}
	s := &APIKeyService{
		userRepo: &routingUserRepo{user: &User{ID: 1}},
		groupRepo: &initialRouteGroupRepoGeili{get: func(_ context.Context, id int64) (*Group, error) {
			mu.Lock()
			defer mu.Unlock()
			reads[id]++
			if id == 7 {
				return nil, errors.New("first group unavailable")
			}
			return groups[id], nil
		}},
	}
	r := &CompositeRouteResolver{accountRepo: &thinModelCatalogRepoGeili{}}
	r.SetRouteValidation(s.groupRepo, NewModelPricingResolver(nil, NewBillingService(&config.Config{}, nil)))
	key := &APIKey{UserID: 1, BillingSource: BillingSourceBalance, RoutingMode: KeyRoutingComposite, GroupIDs: []int64{7, 9, 8}}
	for iteration := 0; iteration < 2; iteration++ {
		rows, err := s.ResolveExplicitKeyRoutes(context.Background(), key, model, CompositeRouteEndpointImages, r)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		require.Equal(t, int64(9), rows[0].TargetGroup.ID)
		require.Equal(t, int64(8), rows[1].TargetGroup.ID)
	}
	require.Equal(t, map[int64]int{7: 2, 8: 2, 9: 2}, reads, "one fresh read per group per request")
}

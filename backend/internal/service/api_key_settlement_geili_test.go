//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type explicitGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *explicitGroupRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	return r.groups[id], nil
}

type explicitSubRepo struct {
	UserSubscriptionRepository
	subs []UserSubscription
}

func (r *explicitSubRepo) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	for _, s := range r.subs {
		if s.ID == id {
			return &s, nil
		}
	}
	return nil, ErrSubscriptionNotFound
}
func (r *explicitSubRepo) ListActiveByUserID(_ context.Context, id int64) ([]UserSubscription, error) {
	out := []UserSubscription{}
	for _, s := range r.subs {
		if s.UserID == id {
			out = append(out, s)
		}
	}
	return out, nil
}

type settlementUserRepo struct {
	UserRepository
	user *User
}

func (r *settlementUserRepo) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type settlementGroupRepo struct {
	GroupRepository
	groups []Group
}

func (r *settlementGroupRepo) ListActive(context.Context) ([]Group, error) { return r.groups, nil }

func TestGetSettlementGroupsExposeUsagePanel(t *testing.T) {
	s := &APIKeyService{
		userRepo: &settlementUserRepo{user: &User{ID: 7}},
		groupRepo: &settlementGroupRepo{groups: []Group{
			{ID: 1, Status: StatusActive, Platform: PlatformOpenAI, UsagePanel: UsagePanelGPT, SubscriptionEnabled: true},
			{ID: 2, Status: StatusActive, Platform: PlatformOpenAI, UsagePanel: UsagePanelNational},
		}},
	}
	groups, err := s.GetSettlementGroups(context.Background(), 7, BillingSourceBalance)
	require.NoError(t, err)
	require.Equal(t, []string{UsagePanelGPT, UsagePanelNational}, []string{groups[0].UsagePanel, groups[1].UsagePanel})
}

func TestExplicitSettlementSeparatesQuotaOwnerFromGroup(t *testing.T) {
	ctx := context.Background()
	gid := int64(1)
	sid := int64(42)
	other := int64(43)
	user := &User{ID: 7}
	repo := &explicitSubRepo{subs: []UserSubscription{{ID: sid, UserID: 7, Status: StatusActive, StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour)}, {ID: other, UserID: 8, Status: StatusActive, ExpiresAt: time.Now().Add(time.Hour)}}}
	s := &APIKeyService{userSubRepo: repo, groupRepo: &explicitGroupRepo{groups: map[int64]*Group{1: {ID: 1, Status: StatusActive, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard, SubscriptionEnabled: true}, 2: {ID: 2, Status: StatusActive, Platform: PlatformOpenAI}, 3: {ID: 3, Status: StatusActive, Platform: PlatformOpenAI, SubscriptionEnabled: true, IsExclusive: true}}}}
	selected, err := s.validateSettlementRouting(ctx, user, "subscription", "single", &gid, nil, nil)
	require.NoError(t, err)
	require.Equal(t, &sid, selected)
	_, err = s.validateSettlementRouting(ctx, user, "balance", "single", &gid, nil, &sid)
	require.Error(t, err)
	_, err = s.validateSettlementRouting(ctx, user, "subscription", "single", &gid, nil, &other)
	require.Error(t, err)
	for _, ids := range [][]int64{{1, 1}, {1, 2}, {1, 3}} {
		_, err = s.validateSettlementRouting(ctx, user, "subscription", "composite", nil, ids, &sid)
		require.Error(t, err)
	}
	_, err = s.validateSettlementRouting(ctx, user, "subscription", "composite", &gid, []int64{1}, &sid)
	require.Error(t, err)
	repo.subs = append(repo.subs, UserSubscription{ID: 44, UserID: 7, Status: StatusActive, ExpiresAt: time.Now().Add(time.Hour)})
	_, err = s.resolveExplicitSubscription(ctx, user.ID, nil)
	require.ErrorIs(t, err, ErrSubscriptionSelectionRequired)
	repo.subs[0].StartsAt = time.Now().Add(time.Hour)
	_, err = s.resolveExplicitSubscription(ctx, user.ID, &sid)
	require.Error(t, err)
}
func TestExplicitKeyCanBeDisabledWithoutRevalidatingExpiredSubscription(t *testing.T) {
	sid := int64(42)
	inactive := "inactive"
	s, repo := newUpdateFieldsAPIKeyService(&APIKey{ID: 1, UserID: 7, BillingSource: BillingSourceSubscription, SubscriptionID: &sid, Status: StatusActive})
	_, err := s.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{Status: &inactive})
	require.NoError(t, err)
	require.True(t, repo.updateFields[0].Status)
	require.False(t, repo.updateFields[0].SettlementRouting)
}
func TestKeyRequestRPMCountsOneRequestAcrossConcurrentAttempts(t *testing.T) {
	ctx := WithKeyRequestRPM(context.Background())
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := onceKeyRequestRPM(ctx, 7, func() error { calls.Add(1); return ErrUserRPMExceeded })
			if !errors.Is(err, ErrUserRPMExceeded) {
				t.Errorf("unexpected result: %v", err)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
}
func TestPlanQuotaPatchPreservesMissingAndUnlimited(t *testing.T) {
	var req UpdatePlanRequest
	require.NoError(t, json.Unmarshal([]byte(`{"daily_limit_usd":null,"weekly_limit_usd":0,"monthly_limit_usd":12}`), &req))
	require.True(t, req.DailyLimitUSD.Set)
	require.Nil(t, req.DailyLimitUSD.Value)
	require.Equal(t, 0.0, *req.WeeklyLimitUSD.Value)
	require.Equal(t, 12.0, *req.MonthlyLimitUSD.Value)
	var omitted UpdatePlanRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"rename"}`), &omitted))
	require.False(t, omitted.DailyLimitUSD.Set)
}

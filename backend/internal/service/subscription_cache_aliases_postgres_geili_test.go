//go:build unit && integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/usersubscriptiongroup"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionInvalidationAliasQueryPostgresGeili(t *testing.T) {
	c, _ := v2PaymentPostgres(t)
	ctx := context.Background()
	u, err := c.User.Create().SetEmail("alias-query@synthetic.invalid").SetPasswordHash("synthetic").Save(ctx)
	require.NoError(t, err)
	parent, err := c.Group.Create().SetName("parent alias").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	alias, err := c.Group.Create().SetName("entitled alias").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	other, err := c.Group.Create().SetName("new parent alias").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	sub, err := c.UserSubscription.Create().SetUserID(u.ID).SetGroupID(parent.ID).
		SetStartsAt(time.Now()).SetExpiresAt(time.Now().Add(time.Hour)).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	s := &SubscriptionService{entClient: c}
	got, err := s.subscriptionInvalidationAliasesGeili(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{parent.ID}, got, "parent fallback remains valid without an edge")
	_, err = c.UserSubscriptionGroup.Create().SetUserSubscriptionID(sub.ID).SetGroupID(alias.ID).Save(ctx)
	require.NoError(t, err)
	got, err = s.subscriptionInvalidationAliasesGeili(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{alias.ID}, got, "existing aliases suppress the legacy fallback, as GetByID does")
	_, err = c.UserSubscriptionGroup.Delete().Where(usersubscriptiongroup.UserSubscriptionIDEQ(sub.ID)).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, c.UserSubscription.UpdateOneID(sub.ID).SetGroupID(other.ID).Exec(ctx))
	got, err = s.subscriptionInvalidationAliasesGeili(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{other.ID}, got, "resolve the current parent even if the caller's group is older")
	pool, err := c.UserSubscription.Create().SetUserID(u.ID).
		SetStartsAt(time.Now()).SetExpiresAt(time.Now().Add(time.Hour)).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	got, err = s.subscriptionInvalidationAliasesGeili(ctx, pool.ID)
	require.NoError(t, err)
	require.Empty(t, got, "a groupless v2 pool must not expand to unrelated groups")
}

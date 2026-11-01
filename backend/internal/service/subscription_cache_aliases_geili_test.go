//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

type invalidationProjectionRepoGeili struct {
	UserSubscriptionRepository
	reads int
}

func (r *invalidationProjectionRepoGeili) GetByID(context.Context, int64) (*UserSubscription, error) {
	r.reads++
	return nil, errors.New("financial projection must not run during invalidation")
}

type invalidationAliasCacheGeili struct {
	BillingCache
	SubscriptionIDCache
	subscriptionCacheInvalidationPubSub
	ids    []int64
	groups []int64
	events []string
}

func (c *invalidationAliasCacheGeili) InvalidateSubscriptionCacheByID(_ context.Context, id int64) error {
	c.ids = append(c.ids, id)
	return nil
}

func (c *invalidationAliasCacheGeili) InvalidateSubscriptionCache(_ context.Context, _ int64, group int64) error {
	c.groups = append(c.groups, group)
	return nil
}

func (c *invalidationAliasCacheGeili) PublishSubscriptionCacheInvalidation(_ context.Context, key string) error {
	c.events = append(c.events, key)
	return nil
}

func TestSubscriptionInvalidationGeiliReadsOnlyAliasesAndKeepsBothCacheProtocols(t *testing.T) {
	for _, tc := range []struct {
		name    string
		parent  int64
		aliases []int64
		want    []int64
	}{
		{"legacy aliases", 4, []int64{2, 3, 4}, []int64{2, 3, 4}},
		{"parent changed since mutation", 4, []int64{5}, []int64{4, 5}},
		{"groupless v2", 0, nil, []int64{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			rows := sqlmock.NewRows([]string{"group_id"})
			for _, id := range tc.aliases {
				rows.AddRow(id)
			}
			mock.ExpectQuery(`SELECT .*group_id.* FROM .*user_subscription_groups.*user_subscription_id.*`).
				WithArgs(int64(19)).WillReturnRows(rows)
			repo := &invalidationProjectionRepoGeili{}
			cache := &invalidationAliasCacheGeili{}
			s := &SubscriptionService{entClient: dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db))),
				userSubRepo: repo, billingCacheService: &BillingCacheService{cache: cache}}
			require.NoError(t, s.invalidateSubscriptionCaches(7, tc.parent, 19))
			require.Zero(t, repo.reads)
			require.Equal(t, []int64{19}, cache.ids)
			require.ElementsMatch(t, tc.want, cache.groups)
			wantEvents := []string{"sub:id:v2:19"}
			for _, group := range tc.want {
				wantEvents = append(wantEvents, subCacheKey(7, group))
			}
			require.ElementsMatch(t, wantEvents, cache.events)
			require.NoError(t, mock.ExpectationsWereMet(), "exactly one alias read, with no projection queries")
		})
	}
}

func TestSubscriptionInvalidationGeiliAliasReadFailureRetainsRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`SELECT .*user_subscription_groups.*`).WithArgs(int64(19)).WillReturnError(context.DeadlineExceeded)
	cache := &invalidationAliasCacheGeili{}
	s := &SubscriptionService{entClient: dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db))),
		userSubRepo: &invalidationProjectionRepoGeili{}, billingCacheService: &BillingCacheService{cache: cache}}
	require.ErrorIs(t, s.invalidateSubscriptionCaches(7, 4, 19), context.DeadlineExceeded)
	require.Empty(t, cache.ids, "failed alias resolution must not acknowledge incomplete invalidation")
	require.Empty(t, cache.groups)
	require.Empty(t, cache.events)
	require.NoError(t, mock.ExpectationsWereMet())
}

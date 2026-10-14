//go:build integration

package repository

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRecoveryGeiliIntegrationDiscoversInactiveAndErrorInRealGroup(t *testing.T) {
	for _, status := range []string{"inactive", service.StatusError} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
			group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "recovery-discovery-" + status, Platform: service.PlatformAnthropic})
			t.Cleanup(func() {
				_, err := integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, group.ID)
				require.NoError(t, err)
			})
			account := runtimeIntegrationAccountGeili(t, repo)
			// Registered after the account helper so LIFO cleanup removes only
			// this fixture's scheduling events before deleting its account/group.
			// The outbox intentionally has no cascading foreign keys.
			t.Cleanup(func() {
				_, err := integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id=$1 OR group_id=$2`, account.ID, group.ID)
				require.NoError(t, err)
			})
			require.NoError(t, repo.AddToGroup(ctx, account.ID, group.ID, 1))
			_, err := integrationDB.Exec(`UPDATE accounts SET status=$2,schedulable=FALSE,updated_at=NOW() WHERE id=$1`, account.ID, status)
			require.NoError(t, err)
			legacy, err := repo.ListByGroup(ctx, group.ID)
			require.NoError(t, err)
			require.Empty(t, legacy, "real legacy query filters status=active")
			page, metadata, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 100, SortBy: "priority", SortOrder: pagination.SortOrderAsc}, "", "", "", "", group.ID, "")
			require.NoError(t, err)
			require.Equal(t, int64(1), metadata.Total)
			require.Len(t, page, 1)
			require.Equal(t, status, page[0].Status)
			var probes atomic.Int64
			recovery := service.NewAccountRecoveryServiceGeili(repo, func(ctx context.Context, a *service.Account, model string) (bool, error) {
				require.Equal(t, account.ID, a.ID)
				require.Equal(t, "claude-sonnet-4-5", model)
				probes.Add(1)
				return true, nil
			}, nil)
			defer recovery.Stop()
			wait, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			require.True(t, recovery.RecoverGroupGeili(wait, group.ID, "claude-sonnet-4-5", nil))
			restored, err := repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, service.StatusActive, restored.Status)
			require.True(t, restored.Schedulable)
			require.Equal(t, int64(1), probes.Load())
		})
	}
}

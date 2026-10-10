//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestAccountSubmissionPostgresConcurrentOneTimeAndProvenance(t *testing.T) {
	for _, platform := range []string{service.PlatformAnthropic, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			repo := NewAccountSubmissionRepository(integrationDB)
			suffix := time.Now().UnixNano()
			group, err := testEntClient(t).Group.Create().SetName(fmt.Sprintf("intake-group-%d", suffix)).SetPlatform(platform).Save(ctx)
			require.NoError(t, err)
			config := service.AccountSubmissionConfig{Name: fmt.Sprintf("intake-account-%d", suffix), Platform: platform, Concurrency: 2, Priority: 50, RateMultiplier: 0, GroupIDs: []int64{group.ID}}
			hash := fmt.Sprintf("%064x", suffix)
			invite, err := repo.CreateInvite(ctx, hash, 1, config)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_submission_invites_geili WHERE id=$1`, invite.ID)
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id IN (SELECT id FROM accounts WHERE name=$1)`, config.Name)
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE name=$1`, config.Name)
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
			})
			// Independent repositories/connections model two instances racing and replaying a lost response.
			var wg sync.WaitGroup
			errs := make(chan error, 12)
			for index := 0; index < 12; index++ {
				wg.Add(1)
				go func(index int) {
					defer wg.Done()
					errs <- NewAccountSubmissionRepository(integrationDB).SubmitInvite(ctx, hash, fmt.Sprintf("synthetic-key-%d", index))
				}(index)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			committed, err := repo.InspectInvite(ctx, hash)
			require.NoError(t, err)
			require.Equal(t, "submitted", committed.Status)
			require.NotNil(t, committed.AccountID)
			var count int
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE name=$1`, config.Name).Scan(&count))
			require.Equal(t, 1, count)
			var status, baseURL, key string
			var schedulable bool
			var multiplier float64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status,schedulable,rate_multiplier,credentials->>'base_url',credentials->>'api_key' FROM accounts WHERE id=$1`, *committed.AccountID).Scan(&status, &schedulable, &multiplier, &baseURL, &key))
			require.Equal(t, "inactive", status)
			require.False(t, schedulable)
			require.Zero(t, multiplier)
			official := "https://api.anthropic.com"
			if platform == service.PlatformOpenAI {
				official = "https://api.openai.com"
			}
			require.Equal(t, official, baseURL)
			require.NoError(t, repo.SubmitInvite(ctx, hash, "replacement-must-not-persist"))
			var unchanged string
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT credentials->>'api_key' FROM accounts WHERE id=$1`, *committed.AccountID).Scan(&unchanged))
			require.Equal(t, key, unchanged)
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, *committed.AccountID).Scan(&count))
			require.Equal(t, 1, count)
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM account_groups WHERE account_id=$1 AND group_id=$2`, *committed.AccountID, group.ID).Scan(&count))
			require.Equal(t, 1, count)
			// Ordinary metadata edits and soft deletion cannot remove credential provenance.
			_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET extra='{}',deleted_at=now() WHERE id=$1`, *committed.AccountID)
			require.NoError(t, err)
			protected, err := repo.ProtectedAccountIDs(ctx, []int64{*committed.AccountID})
			require.NoError(t, err)
			require.True(t, protected[*committed.AccountID])
			exists, err := repo.HasProtectedAccounts(ctx)
			require.NoError(t, err)
			require.True(t, exists)
			err = repo.RevokeInvite(ctx, invite.ID)
			require.ErrorIs(t, err, service.ErrSubmissionInactive)
		})
	}
}

func TestAccountSubmissionPostgresInactiveAndReferenceChanges(t *testing.T) {
	ctx := context.Background()
	repo := NewAccountSubmissionRepository(integrationDB)
	suffix := time.Now().UnixNano()
	config := service.AccountSubmissionConfig{Name: fmt.Sprintf("intake-reject-%d", suffix), Platform: service.PlatformOpenAI, Concurrency: 1, Priority: 50, RateMultiplier: 1}
	for index, kind := range []string{"revoked", "expired", "deleted-group", "wrong-platform", "disabled-group", "deleted-proxy"} {
		t.Run(kind, func(t *testing.T) {
			c := config
			hash := fmt.Sprintf("%064x", suffix+int64(index))
			group, err := testEntClient(t).Group.Create().SetName(fmt.Sprintf("intake-reject-%d-%d", suffix, index)).SetPlatform(service.PlatformOpenAI).Save(ctx)
			require.NoError(t, err)
			c.GroupIDs = []int64{group.ID}
			var proxyID *int64
			if kind == "deleted-proxy" {
				proxy, err := testEntClient(t).Proxy.Create().SetName("intake-test-proxy").SetProtocol("http").SetHost("127.0.0.1").SetPort(9999).Save(ctx)
				require.NoError(t, err)
				proxyID = &proxy.ID
				c.ProxyID = proxyID
			}
			invite, err := repo.CreateInvite(ctx, hash, 1, c)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_submission_invites_geili WHERE id=$1`, invite.ID)
				_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, group.ID)
				if proxyID != nil {
					_, _ = integrationDB.ExecContext(ctx, `DELETE FROM proxies WHERE id=$1`, *proxyID)
				}
			})
			switch kind {
			case "revoked":
				require.NoError(t, repo.RevokeInvite(ctx, invite.ID))
			case "expired":
				_, err = integrationDB.ExecContext(ctx, `UPDATE account_submission_invites_geili SET expires_at=now()-interval '1 second' WHERE id=$1`, invite.ID)
			case "deleted-group":
				_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET deleted_at=now() WHERE id=$1`, group.ID)
			case "wrong-platform":
				_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET platform='anthropic' WHERE id=$1`, group.ID)
			case "disabled-group":
				_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET status='inactive' WHERE id=$1`, group.ID)
			case "deleted-proxy":
				_, err = integrationDB.ExecContext(ctx, `UPDATE proxies SET deleted_at=now() WHERE id=$1`, *proxyID)
			}
			require.NoError(t, err)
			require.Error(t, repo.SubmitInvite(ctx, hash, "synthetic-rejected-key"))
			var count int
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE name=$1`, config.Name).Scan(&count))
			require.Zero(t, count)
			current, err := repo.InspectInvite(ctx, hash)
			require.NoError(t, err)
			require.Nil(t, current.AccountID)
		})
	}
}

func TestAccountSubmissionPostgresRevokeRacesSubmit(t *testing.T) {
	ctx := context.Background()
	repo := NewAccountSubmissionRepository(integrationDB)
	config := service.AccountSubmissionConfig{Name: fmt.Sprintf("intake-race-%d", time.Now().UnixNano()), Platform: service.PlatformAnthropic, Concurrency: 1, Priority: 50, RateMultiplier: 1}
	hash := fmt.Sprintf("%064x", time.Now().UnixNano())
	invite, err := repo.CreateInvite(ctx, hash, 1, config)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_submission_invites_geili WHERE id=$1`, invite.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id IN (SELECT id FROM accounts WHERE name=$1)`, config.Name)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE name=$1`, config.Name)
	})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = repo.SubmitInvite(ctx, hash, "synthetic-race-key") }()
	go func() { defer wg.Done(); _ = repo.RevokeInvite(ctx, invite.ID) }()
	wg.Wait()
	current, err := repo.InspectInvite(ctx, hash)
	require.NoError(t, err)
	require.Contains(t, []string{"submitted", "revoked"}, current.Status)
	if current.Status == "submitted" {
		require.Nil(t, current.RevokedAt)
		require.NotNil(t, current.AccountID)
	} else {
		require.Nil(t, current.AccountID)
	}
	raw, err := json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "synthetic-race-key")
}

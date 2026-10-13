//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func batchInviteFixture(t *testing.T, platform string) (*accountSubmissionRepository, *service.AccountSubmissionInvite, string) {
	t.Helper()
	repo, ok := NewAccountSubmissionRepository(integrationDB).(*accountSubmissionRepository)
	require.True(t, ok)
	hash := fmt.Sprintf("%064x", time.Now().UnixNano())
	i, err := repo.CreateInvite(context.Background(), hash, 1, service.AccountSubmissionConfig{Name: "batch-" + hash[45:], Platform: platform, Concurrency: 3, Priority: 50, RateMultiplier: 0})
	require.NoError(t, err)
	t.Cleanup(func() {
		// The source rows intentionally retain credentials until the capability is removed.
		rows, e := integrationDB.Query(`SELECT account_id FROM account_submission_invites_geili WHERE id=$1 OR parent_invite_id=$1`, i.ID)
		if e != nil {
			return
		}
		var ids []int64
		for rows.Next() {
			var id *int64
			_ = rows.Scan(&id)
			if id != nil {
				ids = append(ids, *id)
			}
		}
		_ = rows.Close()
		_, _ = integrationDB.Exec(`DELETE FROM account_submission_connections_geili WHERE invite_id IN (SELECT id FROM account_submission_invites_geili WHERE id=$1 OR parent_invite_id=$1)`, i.ID)
		_, _ = integrationDB.Exec(`DELETE FROM account_submission_invites_geili WHERE id=$1`, i.ID)
		_, _ = integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id=ANY($1)`, pq.Array(ids))
		_, _ = integrationDB.Exec(`DELETE FROM usage_logs WHERE account_id=ANY($1)`, pq.Array(ids))
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=ANY($1)`, pq.Array(ids))
	})
	return repo, i, hash
}

func TestAccountSubmissionBatchPostgresConcurrentMaximumAndProtection(t *testing.T) {
	ctx := context.Background()
	group, err := testEntClient(t).Group.Create().SetName(fmt.Sprintf("batch-group-%d", time.Now().UnixNano())).SetPlatform(service.PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, group.ID) })
	repo, invite, hash := batchInviteFixture(t, service.PlatformOpenAI)
	_, err = integrationDB.Exec(`UPDATE account_submission_invites_geili SET config=jsonb_set(config,'{group_ids}',$2::jsonb) WHERE id=$1`, invite.ID, fmt.Sprintf("[%d]", group.ID))
	require.NoError(t, err)
	keys := make([]string, service.MaxSubmissionBatchKeys)
	for n := range keys {
		keys[n] = fmt.Sprintf("synthetic-batch-%d", n)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.SubmitInviteBatch(ctx, hash, keys, service.SubmissionOptions{}) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	rows, err := integrationDB.Query(`SELECT a.id,a.status,a.schedulable,a.concurrency,a.rate_multiplier,a.credentials->>'base_url',a.name FROM account_submission_invites_geili i JOIN accounts a ON a.id=i.account_id WHERE i.id=$1 OR i.parent_invite_id=$1 ORDER BY i.id`, invite.ID)
	require.NoError(t, err)
	var ids []int64
	for rows.Next() {
		var id int64
		var status, url, name string
		var enabled bool
		var concurrency int
		var rate float64
		require.NoError(t, rows.Scan(&id, &status, &enabled, &concurrency, &rate, &url, &name))
		ids = append(ids, id)
		require.Equal(t, "inactive", status)
		require.False(t, enabled)
		require.Equal(t, 3, concurrency)
		require.Zero(t, rate)
		require.Equal(t, "https://api.openai.com", url)
		require.Contains(t, name, fmt.Sprintf(" #%d", len(ids)))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Len(t, ids, 100)
	var bindings int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_groups WHERE group_id=$1 AND account_id=ANY($2)`, group.ID, pq.Array(ids)).Scan(&bindings))
	require.Equal(t, 100, bindings)
	protected, err := repo.ProtectedAccountIDs(ctx, ids)
	require.NoError(t, err)
	require.Len(t, protected, 100)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=ANY($1)`, pq.Array(ids)).Scan(&count))
	require.Equal(t, 100, count)
	_, err = integrationDB.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"replacement"') WHERE id=$1`, ids[99])
	require.Error(t, err)
	require.NoError(t, repo.SubmitInvite(ctx, hash, "replacement-must-not-save"))
	items, _, err := repo.ListInvites(ctx, 1, 100)
	require.NoError(t, err)
	for _, item := range items {
		if item.ID == invite.ID {
			require.Equal(t, 100, item.AccountCount)
		}
		if item.AccountID != nil && *item.AccountID != ids[0] {
			for _, id := range ids[1:] {
				require.NotEqual(t, id, *item.AccountID)
			}
		}
	}
}

func TestAccountSubmissionBatchPostgresUsageAndMemberCapability(t *testing.T) {
	ctx := context.Background()
	repo, invite, hash := batchInviteFixture(t, service.PlatformAnthropic)
	require.NoError(t, repo.SubmitInviteBatch(ctx, hash, []string{"synthetic-first", "synthetic-second", "synthetic-third"}, service.SubmissionOptions{}))
	from, to, err := service.SubmissionUsageDates("", "", time.Now())
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE account_submission_invites_geili SET submitted_at=$2 WHERE id=$1 OR parent_invite_id=$1`, invite.ID, from)
	require.NoError(t, err)
	result, err := repo.SubmissionUsage(ctx, hash, from, to)
	require.NoError(t, err)
	require.Len(t, result.Accounts, 3)
	client := testEntClient(t)
	u := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("batch-owner-%d@test.com", time.Now().UnixNano())})
	k := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Key: fmt.Sprintf("sk-batch-fixture-%d", time.Now().UnixNano()), Name: "private customer"})
	logs := newUsageLogRepositoryWithSQL(client, integrationDB)
	var ids []int64
	for n, account := range result.Accounts {
		var id int64
		require.NoError(t, integrationDB.QueryRow(`SELECT account_id FROM account_submission_invites_geili WHERE account_ref=$1`, account.AccountRef).Scan(&id))
		ids = append(ids, id)
		if n != 2 {
			_, err = logs.Create(ctx, &service.UsageLog{UserID: u.ID, APIKeyID: k.ID, AccountID: id, Model: "claude-synthetic", InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, TotalCost: float64(n+1) / 4, ActualCost: 999, CreatedAt: from.Add(time.Hour)})
			require.NoError(t, err)
		}
	}
	_, _, otherHash := batchInviteFixture(t, service.PlatformAnthropic)
	require.NoError(t, repo.SubmitInvite(ctx, otherHash, "synthetic-other"))
	_, err = repo.ReadSubmissionMemberConnection(ctx, otherHash, result.Accounts[1].AccountRef)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	result, err = repo.SubmissionUsage(ctx, hash, from, to)
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Summary.Requests)
	require.Equal(t, .75, result.Summary.ReferenceCost)
	require.Equal(t, .25, result.Accounts[0].Summary.ReferenceCost)
	require.Equal(t, .5, result.Accounts[1].Summary.ReferenceCost)
	require.Zero(t, result.Accounts[2].Summary.Requests)
	require.Len(t, result.Daily, 1)
	require.Len(t, result.Models, 1)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	for _, private := range []string{"synthetic-second", "private customer", "actual_cost", "account_id", "token_hash"} {
		require.NotContains(t, string(raw), private)
	}
	ref := result.Accounts[1].AccountRef
	state, err := repo.UpdateSubmissionMemberConnection(ctx, hash, ref, 0, "11111111-1111-1111-1111-111111111111", service.SubmissionConnectionInput{Mode: "direct"})
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Version)
	first, err := repo.ReadSubmissionMemberConnection(ctx, hash, result.Accounts[0].AccountRef)
	require.NoError(t, err)
	require.Zero(t, first.Version)
	_, err = repo.UpdateSubmissionMemberConnection(ctx, hash, ref, 0, "22222222-2222-2222-2222-222222222222", service.SubmissionConnectionInput{Mode: "direct"})
	require.ErrorIs(t, err, service.ErrSubmissionConnectionConflict)
	require.NoError(t, repo.RevokeSubmissionView(ctx, invite.ID))
	_, err = repo.ReadSubmissionMemberConnection(ctx, hash, ref)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	_, err = repo.UpdateSubmissionMemberConnection(ctx, hash, ref, 1, "22222222-2222-2222-2222-222222222222", service.SubmissionConnectionInput{Mode: "direct"})
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	newHash := fmt.Sprintf("%064x", time.Now().UnixNano())
	require.NoError(t, repo.ResetSubmissionView(ctx, invite.ID, newHash))
	_, err = repo.ReadSubmissionMemberConnection(ctx, hash, ref)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	_, err = repo.ReadSubmissionMemberConnection(ctx, newHash, ref)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE accounts SET deleted_at=now() WHERE id=$1`, ids[0])
	require.NoError(t, err)
	result, err = repo.SubmissionUsage(ctx, newHash, from, to)
	require.NoError(t, err)
	require.Len(t, result.Accounts, 2)
	require.Equal(t, .5, result.Summary.ReferenceCost)
	require.NoError(t, repo.ResetSubmissionView(ctx, invite.ID, hash))
}

func TestAccountSubmissionBatchPostgresRollbackAllMembers(t *testing.T) {
	ctx := context.Background()
	repo, invite, hash := batchInviteFixture(t, service.PlatformAnthropic)
	_, err := integrationDB.Exec(`ALTER TABLE accounts ADD CONSTRAINT batch_rollback_fixture_geili CHECK(name <> ` + pq.QuoteLiteral(invite.Config.Name+" #3") + `) NOT VALID`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`ALTER TABLE accounts DROP CONSTRAINT IF EXISTS batch_rollback_fixture_geili`)
	})
	proxyName := fmt.Sprintf("batch-proxy-%d", time.Now().UnixNano())
	err = repo.SubmitInviteBatch(ctx, hash, []string{"synthetic-a", "synthetic-b", "synthetic-c"}, service.SubmissionOptions{Connection: &service.SubmissionConnectionInput{Mode: "custom", Proxy: &service.SubmissionProxyDraft{Name: proxyName, Protocol: "http", Host: "8.8.8.8", Port: 8080, Password: "synthetic-proxy-secret"}}})
	require.ErrorIs(t, err, service.ErrSubmissionUnavailable)
	require.NotContains(t, err.Error(), "synthetic")
	current, err := repo.InspectInvite(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "pending", current.Status)
	for _, query := range []string{`SELECT count(*) FROM account_submission_invites_geili WHERE parent_invite_id=$1`, `SELECT count(*) FROM account_submission_connections_geili WHERE invite_id=$1`} {
		var n int
		require.NoError(t, integrationDB.QueryRow(query, invite.ID).Scan(&n))
		require.Zero(t, n)
	}
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM accounts WHERE name LIKE $1`, invite.Config.Name+" #%").Scan(&n))
	require.Zero(t, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM proxies WHERE name=$1`, "[external] "+proxyName).Scan(&n))
	require.Zero(t, n)
}

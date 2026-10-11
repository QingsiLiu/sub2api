//go:build integration

package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountSubmissionUsagePostgresIdentityAndRevocation(t *testing.T) {
	ctx := context.Background()
	repo := NewAccountSubmissionRepository(integrationDB)
	usageRepo := repo.(service.AccountSubmissionUsageRepository)
	suffix := time.Now().UnixNano()
	hash := fmt.Sprintf("%064x", suffix)
	invite, err := repo.CreateInvite(ctx, hash, 1, service.AccountSubmissionConfig{Name: fmt.Sprintf("usage-owner-%d", suffix), Platform: service.PlatformAnthropic, Concurrency: 1, Priority: 5, RateMultiplier: 0})
	require.NoError(t, err)
	from, to, err := service.SubmissionUsageDates("", "", time.Now())
	require.NoError(t, err)
	_, err = usageRepo.SubmissionUsage(ctx, hash, from, to)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	require.NoError(t, repo.SubmitInvite(ctx, hash, "synthetic-owned-key"))
	committed, err := repo.InspectInvite(ctx, hash)
	require.NoError(t, err)
	id := *committed.AccountID
	_, err = integrationDB.ExecContext(ctx, `UPDATE account_submission_invites_geili SET submitted_at=$2,expires_at=now()-interval '1 hour' WHERE id=$1`, invite.ID, from)
	require.NoError(t, err)
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("owner-%d@test.com", suffix)})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: fmt.Sprintf("sk-owner-%d", suffix), Name: "private customer"})
	other := mustCreateAccount(t, client, &service.Account{Name: "unrelated usage"})
	logs := newUsageLogRepositoryWithSQL(client, integrationDB)
	zero := 0.0
	for _, row := range []struct {
		id     int64
		model  string
		cost   float64
		offset int
	}{{id, "claude-sonnet-4-6", .25, 0}, {id, "claude-opus-4-6", .5, 1}, {other.ID, "private-other-model", 10000, 0}} {
		_, err = logs.Create(ctx, &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: row.id, Model: row.model, InputTokens: 10, OutputTokens: 20, CacheCreationTokens: 30, CacheReadTokens: 40, TotalCost: row.cost, ActualCost: 999, AccountRateMultiplier: &zero, CreatedAt: from.AddDate(0, 0, row.offset).Add(time.Hour)})
		require.NoError(t, err)
	}
	result, err := usageRepo.SubmissionUsage(ctx, hash, from, to)
	require.NoError(t, err)
	require.False(t, result.Enabled)
	require.Equal(t, int64(2), result.Summary.Requests)
	require.Equal(t, .75, result.Summary.ReferenceCost)
	require.Equal(t, int64(80), result.Summary.CacheReadTokens)
	require.Len(t, result.Daily, 2)
	require.Len(t, result.Models, 2)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET name='renamed',status='active',schedulable=true WHERE id=$1`, id)
	require.NoError(t, err)
	result2, err := NewAccountSubmissionRepository(integrationDB).(service.AccountSubmissionUsageRepository).SubmissionUsage(ctx, hash, from, to)
	require.NoError(t, err)
	require.True(t, result2.Enabled)
	require.Equal(t, "renamed", result2.Name)
	require.Equal(t, result.Summary, result2.Summary)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"replacement"') WHERE id=$1`, id)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "synthetic-owned-key")
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET name='must rollback' WHERE id=$1`, other.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials='{}' WHERE id=$1`, id)
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
	var otherName string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT name FROM accounts WHERE id=$1`, other.ID).Scan(&otherName))
	require.Equal(t, "unrelated usage", otherName)
	require.NoError(t, usageRepo.RevokeSubmissionView(ctx, invite.ID))
	require.NoError(t, usageRepo.RevokeSubmissionView(ctx, invite.ID))
	_, err = usageRepo.SubmissionUsage(ctx, hash, from, to)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	newHash := fmt.Sprintf("%064x", suffix+1)
	require.NoError(t, usageRepo.ResetSubmissionView(ctx, invite.ID, newHash))
	_, err = usageRepo.SubmissionUsage(ctx, hash, from, to)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	result, err = usageRepo.SubmissionUsage(ctx, newHash, from, to)
	require.NoError(t, err)
	require.Equal(t, .75, result.Summary.ReferenceCost)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET deleted_at=now() WHERE id=$1`, id)
	require.NoError(t, err)
	_, err = usageRepo.SubmissionUsage(ctx, newHash, from, to)
	require.ErrorIs(t, err, service.ErrSubmissionInactive)
	require.ErrorIs(t, usageRepo.ResetSubmissionView(ctx, invite.ID, hash), service.ErrSubmissionInactive)
}

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

func TestAccountSubmissionConnectionRollbackPostgres(t *testing.T) {
	ctx := context.Background()
	repo := NewAccountSubmissionRepository(integrationDB).(*accountSubmissionRepository)
	suffix := fmt.Sprint(time.Now().UnixNano())
	name := "rollback-owner-" + suffix
	hash := fmt.Sprintf("%064x", time.Now().UnixNano())
	p := &service.SubmissionProxyDraft{Name: suffix, Protocol: "http", Host: "8.8.8.8", Port: 8080}
	i, err := repo.CreateInvite(ctx, hash, 1, service.AccountSubmissionConfig{Name: name, Platform: service.PlatformOpenAI, Concurrency: 1})
	require.NoError(t, err)
	_, err = integrationDB.Exec(fmt.Sprintf(`ALTER TABLE accounts ADD CONSTRAINT connection_rollback_fixture_geili CHECK(name<>'%s') NOT VALID`, name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`ALTER TABLE accounts DROP CONSTRAINT IF EXISTS connection_rollback_fixture_geili`)
		_, _ = integrationDB.Exec(`DELETE FROM account_submission_connections_geili WHERE invite_id=$1`, i.ID)
		_, _ = integrationDB.Exec(`DELETE FROM account_submission_invites_geili WHERE id=$1`, i.ID)
	})
	err = repo.SubmitInviteWithOptions(ctx, hash, "synthetic-secret", service.SubmissionOptions{Connection: &service.SubmissionConnectionInput{Mode: "custom", Proxy: p}})
	require.ErrorIs(t, err, service.ErrSubmissionUnavailable)
	require.NotContains(t, err.Error(), "synthetic-secret")
	state, err := repo.InspectInvite(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "pending", state.Status)
	require.Nil(t, state.AccountID)
	for _, q := range []string{`SELECT count(*) FROM proxies WHERE name=$1`, `SELECT count(*) FROM accounts WHERE name=$1`} {
		var n int
		value := name
		if q == `SELECT count(*) FROM proxies WHERE name=$1` {
			value = "[external] " + suffix
		}
		require.NoError(t, integrationDB.QueryRow(q, value).Scan(&n))
		require.Zero(t, n)
	}
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_submission_connections_geili WHERE invite_id=$1`, i.ID).Scan(&n))
	require.Zero(t, n)
}

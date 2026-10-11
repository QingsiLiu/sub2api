//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountSubmissionConnectionPostgres(t *testing.T) {
	for _, platform := range []string{service.PlatformAnthropic, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			repo := NewAccountSubmissionRepository(integrationDB).(*accountSubmissionRepository)
			hash := fmt.Sprintf("%064x", time.Now().UnixNano())
			i, err := repo.CreateInvite(ctx, hash, 1, service.AccountSubmissionConfig{Name: "invited", Platform: platform, Concurrency: 1, Priority: 9, RateMultiplier: 0})
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = integrationDB.Exec(`DELETE FROM account_submission_connections_geili WHERE invite_id=$1`, i.ID)
				_, _ = integrationDB.Exec(`DELETE FROM account_submission_invites_geili WHERE id=$1`, i.ID)
				if i.AccountID != nil {
					_, _ = integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id=$1`, *i.AccountID)
					_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, *i.AccountID)
				}
				_, _ = integrationDB.Exec(`DELETE FROM proxies WHERE name=$1`, "[external] "+hash[len(hash)-15:])
			})
			name := "holder" + hash[len(hash)-15:]
			concurrency := 37
			p := &service.SubmissionProxyDraft{Name: hash[len(hash)-15:], Protocol: "http", Host: "8.8.8.8", Port: 8080, Username: "owner", Password: "synthetic-proxy-secret"}
			require.NoError(t, repo.SubmitInviteWithOptions(ctx, hash, "synthetic-api-key", service.SubmissionOptions{Name: &name, Concurrency: &concurrency, Connection: &service.SubmissionConnectionInput{Mode: "custom", Proxy: p}}))
			i, err = repo.InspectInvite(ctx, hash)
			require.NoError(t, err)
			var actual string
			var n, priority int
			var multiplier float64
			var enabled bool
			require.NoError(t, integrationDB.QueryRow(`SELECT name,concurrency,priority,rate_multiplier,schedulable FROM accounts WHERE id=$1`, *i.AccountID).Scan(&actual, &n, &priority, &multiplier, &enabled))
			require.Equal(t, name, actual)
			require.Equal(t, 37, n)
			require.Equal(t, 9, priority)
			require.Zero(t, multiplier)
			require.False(t, enabled)
			state, err := repo.ReadSubmissionConnection(ctx, hash)
			require.NoError(t, err)
			require.Equal(t, "custom", state.Mode)
			require.True(t, state.HasPassword)
			b, _ := json.Marshal(state)
			require.NotContains(t, string(b), "synthetic-proxy-secret")
			require.NoError(t, repo.SubmitInviteWithOptions(ctx, hash, "replacement-key", service.SubmissionOptions{Name: &actual}))
			var key string
			require.NoError(t, integrationDB.QueryRow(`SELECT credentials->>'api_key' FROM accounts WHERE id=$1`, *i.AccountID).Scan(&key))
			require.Equal(t, "synthetic-api-key", key)
			p.Password = ""
			requestID := "10000000-0000-4000-8000-000000000001"
			updated, err := repo.UpdateSubmissionConnection(ctx, hash, 0, requestID, service.SubmissionConnectionInput{Mode: "custom", Proxy: p})
			require.NoError(t, err)
			require.Equal(t, int64(1), updated.Version)
			require.True(t, updated.HasPassword)
			replay, err := repo.UpdateSubmissionConnection(ctx, hash, 0, requestID, service.SubmissionConnectionInput{Mode: "custom", Proxy: p})
			require.NoError(t, err)
			require.Equal(t, updated, replay)
			p.ClearPassword = true
			cleared, err := repo.UpdateSubmissionConnection(ctx, hash, 1, "10000000-0000-4000-8000-000000000002", service.SubmissionConnectionInput{Mode: "custom", Proxy: p})
			require.NoError(t, err)
			require.False(t, cleared.HasPassword)
			require.Equal(t, int64(2), cleared.Version)
			_, err = integrationDB.Exec(`UPDATE proxies SET name='remove-policy' WHERE id=(SELECT proxy_id FROM accounts WHERE id=$1)`, *i.AccountID)
			require.Error(t, err)

			_, err = repo.UpdateSubmissionConnection(ctx, hash, 0, "10000000-0000-4000-8000-000000000099", service.SubmissionConnectionInput{Mode: "direct"})
			require.ErrorIs(t, err, service.ErrSubmissionConnectionConflict)
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, id := range []string{"10000000-0000-4000-8000-000000000003", "10000000-0000-4000-8000-000000000004"} {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					_, e := repo.UpdateSubmissionConnection(ctx, hash, 2, id, service.SubmissionConnectionInput{Mode: "direct"})
					results <- e
				}(id)
			}
			wg.Wait()
			close(results)
			success, conflict := 0, 0
			for e := range results {
				if e == nil {
					success++
				} else {
					require.ErrorIs(t, e, service.ErrSubmissionConnectionConflict)
					conflict++
				}
			}
			require.Equal(t, 1, success)
			require.Equal(t, 1, conflict)
			state, err = repo.ReadSubmissionConnection(ctx, hash)
			require.NoError(t, err)
			require.Equal(t, "direct", state.Mode)
			var count int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM proxies WHERE name=$1`, "[external] "+p.Name).Scan(&count))
			require.Zero(t, count)
			_, err = integrationDB.Exec(`UPDATE account_submission_invites_geili SET expires_at=now()-interval '1 day' WHERE id=$1`, i.ID)
			require.NoError(t, err)
			_, err = repo.ReadSubmissionConnection(ctx, hash)
			require.NoError(t, err)
			require.NoError(t, repo.RevokeSubmissionView(ctx, i.ID))
			_, err = repo.UpdateSubmissionConnection(ctx, hash, 3, "10000000-0000-4000-8000-000000000005", service.SubmissionConnectionInput{Mode: "direct"})
			require.ErrorIs(t, err, service.ErrSubmissionInactive)
			require.NoError(t, repo.ResetSubmissionView(ctx, i.ID, strings.Repeat("f", 64)))
			_, err = repo.ReadSubmissionConnection(ctx, hash)
			require.ErrorIs(t, err, service.ErrSubmissionInactive)
		})
	}
}

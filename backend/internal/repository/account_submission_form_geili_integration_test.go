//go:build integration

package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountSubmissionFullFormPostgres(t *testing.T) {
	for _, kind := range []struct{ platform, typ string }{{service.PlatformAnthropic, service.AccountTypeOAuth}, {service.PlatformAnthropic, service.AccountTypeSetupToken}, {service.PlatformOpenAI, service.AccountTypeOAuth}, {service.PlatformOpenAI, service.AccountTypeAPIKey}} {
		t.Run(kind.platform+kind.typ, func(t *testing.T) {
			ctx := context.Background()
			repo := NewAccountSubmissionRepository(integrationDB).(*accountSubmissionRepository)
			hash := fmt.Sprintf("%064x", time.Now().UnixNano())
			invite, err := repo.CreateInvite(ctx, hash, 1, service.AccountSubmissionConfig{Name: "invited", Platform: kind.platform, Concurrency: 1, Priority: 50, RateMultiplier: 1})
			require.NoError(t, err)
			var accountID int64
			t.Cleanup(func() {
				_, _ = integrationDB.Exec(`DELETE FROM account_submission_connections_geili WHERE invite_id=$1`, invite.ID)
				_, _ = integrationDB.Exec(`DELETE FROM account_submission_invites_geili WHERE id=$1`, invite.ID)
				if accountID > 0 {
					_, _ = integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id=$1`, accountID)
					_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, accountID)
				}
			})
			note := "holder note"
			load := 12
			zero := 0.0
			expiry := time.Now().Add(time.Hour)
			credentials := map[string]any{"access_token": "synthetic-access", "refresh_token": "synthetic-refresh", "chatgpt_account_id": "synthetic-account"}
			if kind.typ == service.AccountTypeAPIKey {
				credentials = map[string]any{"api_key": "synthetic-key", "model_mapping": map[string]any{"from": "to"}}
			}
			prepared := &service.Account{Name: "holder " + hash[len(hash)-8:], Platform: kind.platform, Type: kind.typ, Notes: &note, Credentials: credentials, Extra: map[string]any{"external_submission_form_geili": true, "quota_daily_limit": 7}, Concurrency: 37, Priority: 5, RateMultiplier: &zero, LoadFactor: &load, ExpiresAt: &expiry, AutoPauseOnExpired: true}
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for n := 0; n < 2; n++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- repo.SubmitInviteWithOptions(ctx, hash, "", service.SubmissionOptions{PreparedAccount: prepared})
				}()
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				require.NoError(t, e)
			}
			invite, err = repo.InspectInvite(ctx, hash)
			require.NoError(t, err)
			require.NotNil(t, invite.AccountID)
			accountID = *invite.AccountID
			var typ, status, actualNote string
			var enabled bool
			var concurrency, priority, factor int
			var multiplier float64
			var expiryPresent bool
			require.NoError(t, integrationDB.QueryRow(`SELECT type,status,schedulable,notes,concurrency,priority,load_factor,rate_multiplier,expires_at IS NOT NULL FROM accounts WHERE id=$1`, accountID).Scan(&typ, &status, &enabled, &actualNote, &concurrency, &priority, &factor, &multiplier, &expiryPresent))
			require.Equal(t, kind.typ, typ)
			require.Equal(t, "inactive", status)
			require.False(t, enabled)
			require.Equal(t, note, actualNote)
			require.Equal(t, 37, concurrency)
			require.Equal(t, 5, priority)
			require.Equal(t, 12, factor)
			require.Zero(t, multiplier)
			require.True(t, expiryPresent)
			var count int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, accountID).Scan(&count))
			require.Equal(t, 1, count)
			_, err = integrationDB.Exec(`UPDATE accounts SET extra=extra-'external_submission_form_geili' WHERE id=$1`, accountID)
			require.Error(t, err)
			if kind.typ == service.AccountTypeOAuth {
				_, err = integrationDB.Exec(`UPDATE accounts SET credentials=credentials||'{"chatgpt_account_id":"other"}'::jsonb WHERE id=$1`, accountID)
				require.Error(t, err)
				_, err = integrationDB.Exec(`UPDATE accounts SET credentials=credentials||'{"access_token":"synthetic-rotated"}'::jsonb WHERE id=$1`, accountID)
				require.NoError(t, err, "normal provider token refresh keeps the supplier identity")
			}
			err = repo.SubmitInviteWithOptions(ctx, hash, "", service.SubmissionOptions{PreparedAccount: &service.Account{Name: strings.Repeat("x", 1000)}})
			require.NoError(t, err, "a committed invitation cannot create a replacement on retry")
		})
	}
}

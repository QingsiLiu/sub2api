//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// registerBillingFixtureCleanup removes only the explicitly owned synthetic
// user's evidence and entities. New no-FK journals outlive parent deletion in
// production, so test teardown must enumerate them rather than rely on cascades.
// Call after constructing a committed fixture; LIFO runs this before any earlier
// parent cleanup. Groups are deliberately not inferred: they may be shared.
func registerBillingFixtureCleanup(t *testing.T, userID int64, accountIDs ...int64) {
	t.Helper()
	t.Cleanup(func() { cleanupBillingFixture(t, userID, accountIDs, nil) })
}

func cleanupBillingFixture(t *testing.T, userID int64, accountIDs, groupIDs []int64) {
	t.Helper()
	require.Positive(t, userID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	// Evidence before request, entitlement, key and user parents. Every predicate
	// is tied to this fixture's own user; no global DELETE/TRUNCATE is permitted.
	statements := []string{
		`DELETE FROM usage_settlement_receipts WHERE user_id=$1`,
		`DELETE FROM usage_balance_holds WHERE user_id=$1`,
		`DELETE FROM grok_video_tasks_geili WHERE user_id=$1`,
		`DELETE FROM auapi_image_tasks WHERE user_id=$1`,
		`DELETE FROM usage_billing_dedup WHERE api_key_id IN(SELECT id FROM api_keys WHERE user_id=$1)`,
		`DELETE FROM usage_billing_dedup_archive WHERE api_key_id IN(SELECT id FROM api_keys WHERE user_id=$1)`,
		`DELETE FROM usage_logs WHERE user_id=$1`,
		`DELETE FROM benefit_campaign_claims WHERE user_id=$1`,
		`DELETE FROM subscription_usage_allocations WHERE request_key IN(SELECT r.request_key FROM subscription_requests r JOIN user_subscriptions s ON s.id=r.subscription_id WHERE s.user_id=$1)`,
		`DELETE FROM subscription_media_tasks WHERE user_id=$1`,
		`DELETE FROM subscription_request_contracts WHERE request_key IN(SELECT r.request_key FROM subscription_requests r JOIN user_subscriptions s ON s.id=r.subscription_id WHERE s.user_id=$1)`,
		`DELETE FROM subscription_requests WHERE subscription_id IN(SELECT id FROM user_subscriptions WHERE user_id=$1)`,
		`DELETE FROM subscription_refunds WHERE subscription_id IN(SELECT id FROM user_subscriptions WHERE user_id=$1)`,
		`DELETE FROM subscription_operations WHERE subscription_id IN(SELECT id FROM user_subscriptions WHERE user_id=$1)`,
		`DELETE FROM subscription_contract_changes WHERE subscription_id IN(SELECT id FROM user_subscriptions WHERE user_id=$1)`,
		`DELETE FROM subscription_contracts WHERE user_id=$1`,
		`DELETE FROM user_subscriptions WHERE user_id=$1`,
		`DELETE FROM payment_refund_journals WHERE order_id IN(SELECT id FROM payment_orders WHERE user_id=$1)`,
		`DELETE FROM payment_orders WHERE user_id=$1`,
		`DELETE FROM api_keys WHERE user_id=$1`,
		`DELETE FROM users WHERE id=$1`,
	}
	for _, query := range statements {
		_, err = tx.ExecContext(ctx, query, userID)
		require.NoError(t, err, "fixture cleanup: %s", query)
	}
	for _, id := range accountIDs {
		require.Positive(t, id)
		_, err = tx.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
		require.NoError(t, err)
	}
	for _, id := range groupIDs {
		require.Positive(t, id)
		_, err = tx.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, id)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	for _, query := range []string{
		`SELECT COUNT(*) FROM users WHERE id=$1`, `SELECT COUNT(*) FROM api_keys WHERE user_id=$1`,
		`SELECT COUNT(*) FROM user_subscriptions WHERE user_id=$1`, `SELECT COUNT(*) FROM usage_logs WHERE user_id=$1`,
		`SELECT COUNT(*) FROM usage_settlement_receipts WHERE user_id=$1`, `SELECT COUNT(*) FROM usage_balance_holds WHERE user_id=$1`,
		`SELECT COUNT(*) FROM grok_video_tasks_geili WHERE user_id=$1`, `SELECT COUNT(*) FROM subscription_media_tasks WHERE user_id=$1`,
	} {
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, query, userID).Scan(&n))
		require.Zero(t, n, "fixture residue: %s", query)
	}
}

func TestBillingFixtureCleanupRemovesOwnedAdmissionAndRetainsOtherUser(t *testing.T) {
	c := testEntClient(t)
	ctx := context.Background()
	own := mustCreateUser(t, c, &service.User{Email: "cleanup-own-" + uuid.NewString() + "@example.invalid"})
	other := mustCreateUser(t, c, &service.User{Email: "cleanup-other-" + uuid.NewString() + "@example.invalid"})
	registerBillingFixtureCleanup(t, other.ID)
	key := mustCreateApiKey(t, c, &service.APIKey{UserID: own.ID, Key: "sk-" + uuid.NewString()})
	account := mustCreateAccount(t, c, &service.Account{Name: "cleanup-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	group := mustCreateGroup(t, c, &service.Group{Name: "cleanup-" + uuid.NewString(), Platform: service.PlatformGrok, SubscriptionType: service.SubscriptionTypeSubscription})
	sub := mustCreateSubscription(t, c, &service.UserSubscription{UserID: own.ID, GroupID: group.ID})
	admission := uuid.NewString()
	_, err := integrationDB.Exec(`INSERT INTO subscription_requests(request_key,subscription_id,api_key_id,lots,admitted_at)VALUES($1,$2,$3,'[]',NOW())`, admission, sub.ID, key.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`INSERT INTO subscription_media_tasks(task_id,api_key_id,user_id,subscription_id,admission_key)VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), key.ID, own.ID, sub.ID, admission)
	require.NoError(t, err)
	_, err = (&usageBillingRepository{db: integrationDB}).Apply(ctx, &service.UsageBillingCommand{RequestID: uuid.NewString(), UserID: own.ID, APIKeyID: key.ID, AccountID: account.ID, BalanceCost: 1})
	require.NoError(t, err)
	cleanupBillingFixture(t, own.ID, []int64{account.ID}, []int64{group.ID})
	cleanupBillingFixture(t, own.ID, []int64{account.ID}, []int64{group.ID}) // teardown is repeatable
	var id int64
	require.NoError(t, integrationDB.QueryRow(`SELECT id FROM users WHERE id=$1`, other.ID).Scan(&id))
	require.Equal(t, other.ID, id)
	for _, table := range []string{"accounts", "groups"} {
		target := account.ID
		if table == "groups" {
			target = group.ID
		}
		err = integrationDB.QueryRow(`SELECT id FROM `+table+` WHERE id=$1`, target).Scan(&id)
		require.ErrorIs(t, err, sql.ErrNoRows)
	}
}

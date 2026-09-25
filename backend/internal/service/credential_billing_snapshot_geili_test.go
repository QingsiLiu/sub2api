package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type credentialBillingCountingRepo struct {
	AccountRepository
	parent *Account
	calls  int
	err    error
}

func (r *credentialBillingCountingRepo) GetByID(context.Context, int64) (*Account, error) {
	r.calls++
	return r.parent, r.err
}

func TestCredentialBillingFrozenShadowSurvivesDatabaseOutage(t *testing.T) {
	parentID := int64(42)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "super-secret", "refresh_token": "never-copy"}, Extra: map[string]any{openAILongContextBillingEnabledKey: true, "private_config": "secret"}}
	selected := &Account{ID: 9, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	repo := &credentialBillingCountingRepo{parent: parent}
	ctx := WithCredentialBillingSnapshot(context.Background())
	_, err := resolveCredentialAccount(ctx, repo, selected)
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	detached := CopyCredentialBillingSnapshot(ctx, context.Background())
	repo.err = errors.New("database unavailable after upstream response")
	parent.Extra[openAILongContextBillingEnabledKey] = false
	parent.Type = AccountTypeAPIKey
	billing, err := resolveCredentialBillingAccount(detached, repo, selected)
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	require.True(t, billing.IsOpenAIOAuthLike())
	require.True(t, billing.IsOpenAILongContextBillingEnabled())
	require.Nil(t, billing.Credentials)
	require.Nil(t, billing.ParentAccountID)
	require.Len(t, billing.Extra, 1)
	raw, err := json.Marshal(billing)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "super-secret")
	require.NotContains(t, string(raw), "never-copy")
	require.NotContains(t, string(raw), "private_config")
	tier := ResolveOpenAIServiceTierBilling(billing, "priority", "default")
	require.Equal(t, "priority", tier.Billing)
}

func TestCredentialBillingMissingShadowSnapshotFailsWithoutDatabaseRead(t *testing.T) {
	parentID := int64(42)
	selected := &Account{ID: 9, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	repo := &credentialBillingCountingRepo{err: errors.New("must not read")}
	_, err := resolveCredentialBillingAccount(WithCredentialBillingSnapshot(context.Background()), repo, selected)
	require.ErrorIs(t, err, ErrCredentialBillingSnapshotMissing)
	require.Zero(t, repo.calls)
}

func TestCredentialBillingRequestIsolationAndConcurrentCopies(t *testing.T) {
	account := &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Extra: map[string]any{openAILongContextBillingEnabledKey: true}}
	first := WithCredentialBillingSnapshot(context.Background())
	FreezeCredentialBillingAccount(first, account)
	account.Extra[openAILongContextBillingEnabledKey] = false
	FreezeCredentialBillingAccount(first, account) // first observation cannot be repriced
	second := WithCredentialBillingSnapshot(context.Background())
	FreezeCredentialBillingAccount(second, account)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			billing, err := resolveCredentialBillingAccount(CopyCredentialBillingSnapshot(first, context.Background()), nil, account)
			if err != nil || !billing.IsOpenAILongContextBillingEnabled() {
				t.Errorf("lost frozen facts: %v", err)
			}
		}()
	}
	wg.Wait()
	billing, err := resolveCredentialBillingAccount(second, nil, account)
	require.NoError(t, err)
	require.False(t, billing.IsOpenAILongContextBillingEnabled())
	require.True(t, billing.IsOpenAIOAuthLike())
}

func TestCredentialBillingNormalAccountNeverFetchesParent(t *testing.T) {
	repo := &credentialBillingCountingRepo{err: errors.New("must not read")}
	account := &Account{ID: 4, Platform: PlatformGrok, Type: AccountTypeAPIKey}
	for _, ctx := range []context.Context{context.Background(), WithCredentialBillingSnapshot(context.Background())} {
		billing, err := resolveCredentialBillingAccount(ctx, repo, account)
		require.NoError(t, err)
		require.Equal(t, PlatformGrok, billing.Platform)
		require.Nil(t, openAILongContextBillingGate(billing))
	}
	require.Zero(t, repo.calls)
}

func TestCredentialBillingFactsContainOnlySafeScalars(t *testing.T) {
	// The output account must be a fresh value every time; mutating an output
	// cannot modify a previously copied snapshot or request-selected account.
	account := &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{openAILongContextBillingEnabledKey: true}}
	ctx := WithCredentialBillingSnapshot(context.Background())
	FreezeCredentialBillingAccount(ctx, account)
	a, err := resolveCredentialBillingAccount(ctx, nil, account)
	require.NoError(t, err)
	a.Extra[openAILongContextBillingEnabledKey] = false
	b, err := resolveCredentialBillingAccount(ctx, nil, account)
	require.NoError(t, err)
	require.True(t, b.IsOpenAILongContextBillingEnabled())
	require.True(t, account.IsOpenAILongContextBillingEnabled())
}

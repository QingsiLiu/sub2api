package service

import (
	"context"
	"errors"
	"sync"
)

var ErrCredentialBillingSnapshotMissing = errors.New("credential billing facts were not frozen before forwarding")

type credentialBillingSnapshotKey struct{}

// credentialBillingFacts is deliberately not an Account clone: no credentials,
// endpoint URLs, provider state or mutable maps can enter the request snapshot.
type credentialBillingFacts struct {
	accountID           int64
	credentialAccountID int64
	platform            string
	accountType         string
	longContextEnabled  bool
}

type credentialBillingSnapshots struct {
	mu    sync.RWMutex
	facts map[int64]credentialBillingFacts
}

// WithCredentialBillingSnapshot allocates request-owned state before dispatch.
// Existing holders are preserved across forwarding wrappers and retries.
func WithCredentialBillingSnapshot(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(credentialBillingSnapshotKey{}).(*credentialBillingSnapshots); ok {
		return ctx
	}
	return context.WithValue(ctx, credentialBillingSnapshotKey{}, &credentialBillingSnapshots{facts: make(map[int64]credentialBillingFacts)})
}

// CopyCredentialBillingSnapshot freezes a value copy for the detached usage
// task. Later connection/turn work cannot change the price of a completed turn.
func CopyCredentialBillingSnapshot(parent, base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if parent == nil {
		return base
	}
	source, _ := parent.Value(credentialBillingSnapshotKey{}).(*credentialBillingSnapshots)
	if source == nil {
		return base
	}
	source.mu.RLock()
	copied := &credentialBillingSnapshots{facts: make(map[int64]credentialBillingFacts, len(source.facts))}
	for key, value := range source.facts {
		copied.facts[key] = value
	}
	source.mu.RUnlock()
	return context.WithValue(base, credentialBillingSnapshotKey{}, copied)
}

// FreezeCredentialBillingAccount records only pricing switches. Call after
// credential resolution and before sending to the provider. First observation
// wins so parent config updates mid-request cannot reprice consumed usage.
func FreezeCredentialBillingAccount(ctx context.Context, account *Account) {
	freezeCredentialBillingAlias(ctx, account, account)
}

func freezeCredentialBillingAlias(ctx context.Context, selected, credential *Account) {
	if ctx == nil || selected == nil || credential == nil {
		return
	}
	holder, _ := ctx.Value(credentialBillingSnapshotKey{}).(*credentialBillingSnapshots)
	if holder == nil {
		return
	}
	value := credentialBillingFacts{accountID: selected.ID, credentialAccountID: credential.ID, platform: credential.Platform, accountType: credential.Type, longContextEnabled: credential.IsOpenAILongContextBillingEnabled()}
	holder.mu.Lock()
	if _, exists := holder.facts[selected.ID]; !exists {
		holder.facts[selected.ID] = value
	}
	holder.mu.Unlock()
}

// resolveCredentialBillingAccount never fetches credentials when a request
// holder exists: durable post-upstream capture must not depend on another DB
// read. A new non-secret Account satisfies existing pure pricing predicates.
func resolveCredentialBillingAccount(ctx context.Context, repo AccountRepository, account *Account) (*Account, error) {
	if account == nil {
		return nil, nil
	}
	if ctx != nil {
		if holder, ok := ctx.Value(credentialBillingSnapshotKey{}).(*credentialBillingSnapshots); ok && holder != nil {
			holder.mu.RLock()
			facts, exists := holder.facts[account.ID]
			holder.mu.RUnlock()
			if !exists {
				// Normal accounts are already request-selected immutable scheduler values.
				// Shadows cannot use their own flags in place of the credential parent's.
				if account.IsShadow() {
					return nil, ErrCredentialBillingSnapshotMissing
				}
				return credentialBillingAccountFromFacts(credentialBillingFacts{accountID: account.ID, credentialAccountID: account.ID, platform: account.Platform, accountType: account.Type, longContextEnabled: account.IsOpenAILongContextBillingEnabled()}), nil
			}
			return credentialBillingAccountFromFacts(facts), nil
		}
	}
	// Backward-compatible internal callers which never entered a gateway request
	// can still resolve normally. Runtime HTTP/WS entries always install a holder.
	resolved, err := resolveCredentialAccount(ctx, repo, account)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	return credentialBillingAccountFromFacts(credentialBillingFacts{accountID: account.ID, credentialAccountID: resolved.ID, platform: resolved.Platform, accountType: resolved.Type, longContextEnabled: resolved.IsOpenAILongContextBillingEnabled()}), nil
}

func credentialBillingAccountFromFacts(facts credentialBillingFacts) *Account {
	return &Account{ID: facts.credentialAccountID, Platform: facts.platform, Type: facts.accountType, Extra: map[string]any{openAILongContextBillingEnabledKey: facts.longContextEnabled}}
}

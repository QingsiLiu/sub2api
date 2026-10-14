package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type balanceIdentityRepoGeili struct{ AccountRepository }

func (*balanceIdentityRepoGeili) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}

type balanceIdentityUpstreamGeili struct{ oldStarted, release chan struct{} }

func (u *balanceIdentityUpstreamGeili) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body := `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"0"}]}`
	if req.Header.Get("Authorization") == "Bearer old" {
		close(u.oldStarted)
		select {
		case <-u.release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		body = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"100"}]}`
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}
func (u *balanceIdentityUpstreamGeili) DoWithTLS(req *http.Request, p string, id int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, p, id, c)
}
func TestAccountBalanceIdentityGeiliNewCredentialCannotReuseInFlightPositiveBalance(t *testing.T) {
	upstream := &balanceIdentityUpstreamGeili{oldStarted: make(chan struct{}), release: make(chan struct{})}
	defer close(upstream.release)
	repo := &balanceIdentityRepoGeili{}
	balance := NewCNProviderBalanceService(repo, nil, upstream, nil)
	old := &Account{ID: 42, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Credentials: map[string]any{"account_mode": AccountModePayG, "api_key": "old", "base_url": "https://relay.example.com"}}
	oldDone := make(chan struct{})
	go func() { defer close(oldDone); _, _ = balance.QueryBalanceForAccount(context.Background(), old) }()
	select {
	case <-upstream.oldStarted:
	case <-time.After(time.Second):
		t.Fatal("old balance request did not start")
	}
	current := *old
	current.Credentials = map[string]any{"account_mode": AccountModePayG, "api_key": "new", "base_url": "https://relay.example.com"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := balance.QueryBalanceForAccount(ctx, &current)
	require.NoError(t, err, "new credential must have an independent request while old request is blocked")
	require.True(t, result.Success)
	require.False(t, result.Available)
	require.Zero(t, result.Balance)
}
func TestAccountBalanceIdentityGeiliTimestampObservationKeepsIdentity(t *testing.T) {
	a := Account{ID: 1, Credentials: map[string]any{"api_key": "synthetic"}, UpdatedAt: time.Now()}
	before := accountBalanceIdentityGeili(&a)
	a.UpdatedAt = time.Now().Add(time.Minute)
	require.Equal(t, before, accountBalanceIdentityGeili(&a))
	a.Credentials = map[string]any{"api_key": "replacement"}
	require.NotEqual(t, before, accountBalanceIdentityGeili(&a))
}

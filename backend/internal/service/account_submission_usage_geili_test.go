package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountSubmissionUsageDates(t *testing.T) {
	now := time.Date(2026, 10, 10, 16, 30, 0, 0, time.UTC) // Beijing October 11.
	from, to, err := SubmissionUsageDates("", "", now)
	require.NoError(t, err)
	require.Equal(t, "2026-10-05", from.Format("2006-01-02"))
	require.Equal(t, "2026-10-12", to.Format("2006-01-02"))
	for _, pair := range [][2]string{{"2026-10-11", "2026-10-11"}, {"2026-07-14", "2026-10-11"}} {
		from, to, err = SubmissionUsageDates(pair[0], pair[1], now)
		require.NoError(t, err)
		require.LessOrEqual(t, to.Sub(from), 90*24*time.Hour)
	}
	for _, pair := range [][2]string{{"2026-07-13", "2026-10-11"}, {"", "2026-10-11"}, {"2026-10-11", ""}, {"bad", "bad"}, {"2026-10-12", "2026-10-12"}, {"2026-10-11", "2026-10-10"}, {"2026-02-30", "2026-03-01"}} {
		_, _, err = SubmissionUsageDates(pair[0], pair[1], now)
		require.ErrorIs(t, err, ErrSubmissionInvalid)
	}
}

type usageCredentialRepo struct {
	AccountRepository
	protected bool
	calls     int
}

func (r *usageCredentialRepo) ProtectedAccountIDs(context.Context, []int64) (map[int64]bool, error) {
	r.calls++
	return map[int64]bool{9: r.protected}, nil
}
func TestAccountSubmissionCredentialIdentity(t *testing.T) {
	a := &Account{ID: 9, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "original"}}
	r := &usageCredentialRepo{protected: true}
	require.NoError(t, protectSubmissionCredentialUpdate(context.Background(), r, a, &UpdateAccountInput{Name: "metadata"}))
	require.Zero(t, r.calls)
	require.NoError(t, protectSubmissionCredentialUpdate(context.Background(), r, a, &UpdateAccountInput{Credentials: map[string]any{"api_key": "original", "model_mapping": map[string]any{}}}))
	require.ErrorIs(t, protectSubmissionCredentialUpdate(context.Background(), r, a, &UpdateAccountInput{Credentials: map[string]any{"api_key": "replacement"}}), ErrSubmissionCredentialsImmutable)
	require.ErrorIs(t, protectSubmissionCredentialUpdate(context.Background(), r, a, &UpdateAccountInput{Type: "oauth"}), ErrSubmissionCredentialsImmutable)
	r.protected = false
	require.NoError(t, protectSubmissionCredentialUpdate(context.Background(), r, a, &UpdateAccountInput{Credentials: map[string]any{"api_key": "replacement"}}))
}

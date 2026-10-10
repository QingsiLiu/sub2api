package service

import (
	"context"
	"encoding/base64"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type submissionRepoStub struct {
	AccountSubmissionRepository
	hash    string
	config  AccountSubmissionConfig
	invite  *AccountSubmissionInvite
	submits int
}

func (r *submissionRepoStub) CreateInvite(_ context.Context, hash string, actor int64, c AccountSubmissionConfig) (*AccountSubmissionInvite, error) {
	r.hash = hash
	r.config = c
	return &AccountSubmissionInvite{ID: 1, CreatedBy: actor, Config: c, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (r *submissionRepoStub) InspectInvite(_ context.Context, hash string) (*AccountSubmissionInvite, error) {
	r.hash = hash
	return r.invite, nil
}
func (r *submissionRepoStub) SubmitInvite(_ context.Context, _, _ string) error {
	r.submits++
	return nil
}

type submissionAdminStub struct {
	AdminService
	groupErr error
}

func (s submissionAdminStub) ValidateAccountGroupBindings(context.Context, []int64) error {
	return s.groupErr
}
func (s submissionAdminStub) CheckMixedChannelRisk(context.Context, int64, string, []int64) error {
	return s.groupErr
}

func TestAccountSubmissionTokenAndConfig(t *testing.T) {
	repo := &submissionRepoStub{}
	svc := NewAccountSubmissionService(repo, submissionAdminStub{})
	config := AccountSubmissionConfig{Name: " owner ", Platform: PlatformAnthropic, Concurrency: 1, Priority: 50, RateMultiplier: 0}
	first, token, err := svc.Create(context.Background(), 1, config)
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	require.Len(t, raw, 32)
	require.Len(t, repo.hash, 64)
	require.NotEqual(t, token, repo.hash)
	require.Equal(t, "owner", first.Config.Name)
	require.Zero(t, first.Config.RateMultiplier)
	_, second, err := svc.Create(context.Background(), 1, config)
	require.NoError(t, err)
	require.NotEqual(t, token, second)
	for _, invalid := range []string{"", token + "=", token[:42], "not-an-invite"} {
		_, err := AccountSubmissionTokenHash(invalid)
		require.ErrorIs(t, err, ErrSubmissionInvalid)
	}
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		config.Platform = platform
		require.NoError(t, ValidateAccountSubmissionConfig(&config))
	}
	config.Platform = PlatformGemini
	require.ErrorIs(t, ValidateAccountSubmissionConfig(&config), ErrSubmissionConfig)
	config.Platform = PlatformOpenAI
	config.GroupIDs = []int64{1, 1}
	require.ErrorIs(t, ValidateAccountSubmissionConfig(&config), ErrSubmissionConfig)
}

func TestAccountSubmissionInvalidKeysNeverConsumeInvite(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	repo := &submissionRepoStub{invite: &AccountSubmissionInvite{Config: AccountSubmissionConfig{Platform: PlatformOpenAI}, ExpiresAt: time.Now().Add(time.Hour)}}
	svc := NewAccountSubmissionService(repo, submissionAdminStub{})
	for _, key := range []string{"", "   ", "key\nsecond", "key\x00"} {
		require.ErrorIs(t, svc.Submit(context.Background(), token, key), ErrSubmissionInvalid)
	}
	require.Zero(t, repo.submits)
	require.NoError(t, svc.Submit(context.Background(), token, "synthetic-provider-key"))
	require.Equal(t, 1, repo.submits)
	now := time.Now()
	repo.invite.SubmittedAt = &now
	require.NoError(t, svc.Submit(context.Background(), token, "different-synthetic-key"))
	require.Equal(t, 1, repo.submits)
	repo.invite.SubmittedAt = nil
	repo.invite.RevokedAt = &now
	require.ErrorIs(t, svc.Submit(context.Background(), token, "synthetic-provider-key"), ErrSubmissionInactive)
	repo.invite.RevokedAt = nil
	repo.invite.ExpiresAt = now.Add(-time.Hour)
	require.ErrorIs(t, svc.Submit(context.Background(), token, "synthetic-provider-key"), ErrSubmissionInactive)
	require.Equal(t, 1, repo.submits)
}

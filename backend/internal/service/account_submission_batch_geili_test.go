package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type submissionBatchRepoStub struct {
	submissionRepoStub
	keys    []string
	options SubmissionOptions
}

func (r *submissionBatchRepoStub) SubmitInviteBatch(_ context.Context, _ string, keys []string, options SubmissionOptions) error {
	r.submits++
	r.keys = append([]string(nil), keys...)
	r.options = options
	return nil
}

func TestAccountSubmissionBatchValidationAndReplay(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	repo := &submissionBatchRepoStub{submissionRepoStub: submissionRepoStub{invite: &AccountSubmissionInvite{Config: AccountSubmissionConfig{Platform: PlatformAnthropic}, ExpiresAt: time.Now().Add(time.Hour)}}}
	svc := NewAccountSubmissionService(repo, submissionAdminStub{})
	tooMany := make([]string, 101)
	for n := range tooMany {
		tooMany[n] = fmt.Sprintf("synthetic-%d", n)
	}
	for _, keys := range [][]string{nil, {}, {"a", " a "}, {"a", ""}, {"a", "b\n" + "c"}, {"a", "b\x00"}, {strings.Repeat("x", 2049)}, tooMany} {
		require.ErrorIs(t, svc.SubmitBatch(context.Background(), token, keys, SubmissionOptions{}), ErrSubmissionInvalid)
	}
	require.Zero(t, repo.submits)
	name := " batch "
	concurrency := 7
	require.NoError(t, svc.SubmitBatch(context.Background(), token, []string{" synthetic-a ", "synthetic-b"}, SubmissionOptions{Name: &name, Concurrency: &concurrency}))
	require.Equal(t, []string{"synthetic-a", "synthetic-b"}, repo.keys)
	require.Equal(t, "batch", *repo.options.Name)
	now := time.Now()
	repo.invite.SubmittedAt = &now
	require.NoError(t, svc.SubmitBatch(context.Background(), token, []string{"replacement"}, SubmissionOptions{}))
	require.Equal(t, 1, repo.submits)
	repo.invite.SubmittedAt = nil
	repo.invite.RevokedAt = &now
	require.ErrorIs(t, svc.SubmitBatch(context.Background(), token, []string{"synthetic"}, SubmissionOptions{}), ErrSubmissionInactive)
}

func TestAccountSubmissionBatchNormalizationDoesNotMutateCaller(t *testing.T) {
	input := []string{" synthetic-a ", "synthetic-b"}
	keys, err := NormalizeSubmissionBatchKeys(input)
	require.NoError(t, err)
	require.Equal(t, []string{"synthetic-a", "synthetic-b"}, keys)
	keys[1] = "replacement"
	require.Equal(t, []string{" synthetic-a ", "synthetic-b"}, input)
}

func TestAccountSubmissionMemberRejectsIDsBeforeRepositoryAccess(t *testing.T) {
	svc := NewAccountSubmissionService(&submissionRepoStub{}, nil)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, ref := range []string{"6381", "not-a-ref", "11111111-1111-1111-1111-11111111111z"} {
		_, err := svc.MemberConnection(context.Background(), token, ref)
		require.ErrorIs(t, err, ErrSubmissionInvalid)
		_, err = svc.UpdateMemberConnection(context.Background(), token, ref, 0, "11111111-1111-1111-1111-111111111111", SubmissionConnectionInput{Mode: "direct"})
		require.ErrorIs(t, err, ErrSubmissionInvalid)
	}
}

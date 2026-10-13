package service

import (
	"context"
	"strings"
	"time"
)

const MaxSubmissionBatchKeys = 100

type AccountSubmissionBatchRepository interface {
	SubmitInviteBatch(context.Context, string, []string, SubmissionOptions) error
}

func NormalizeSubmissionBatchKeys(keys []string) ([]string, error) {
	if len(keys) < 1 || len(keys) > MaxSubmissionBatchKeys {
		return nil, ErrSubmissionInvalid
	}
	normalized := make([]string, len(keys))
	seen := make(map[string]bool, len(keys))
	for n, key := range keys {
		key = strings.TrimSpace(key)
		if !validSubmissionKey(key) || seen[key] {
			return nil, ErrSubmissionInvalid
		}
		normalized[n], seen[key] = key, true
	}
	return normalized, nil
}

func (s *AccountSubmissionService) SubmitBatch(ctx context.Context, token string, keys []string, options SubmissionOptions) error {
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return err
	}
	if keys, err = NormalizeSubmissionBatchKeys(keys); err != nil {
		return err
	}
	invite, err := s.repo.InspectInvite(ctx, hash)
	if err != nil {
		return err
	}
	if invite.State(time.Now()) == "submitted" {
		return nil
	}
	if invite.State(time.Now()) != "pending" {
		return ErrSubmissionInactive
	}
	if options.PreparedAccount != nil {
		return ErrSubmissionInvalid
	}
	if err = ValidateSubmissionOptions(ctx, &options); err != nil {
		return err
	}
	if err = s.validateGroups(ctx, invite.Config); err != nil {
		return err
	}
	repo, ok := s.repo.(AccountSubmissionBatchRepository)
	if !ok {
		return ErrSubmissionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return repo.SubmitInviteBatch(ctx, hash, keys, options)
}

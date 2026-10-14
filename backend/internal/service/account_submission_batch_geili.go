package service

import (
	"context"
	"strings"
	"time"
)

const MaxSubmissionBatchKeys = 100

// Bind each credential to its connection in the same object; never zip separately
// filtered client arrays. The legacy api_keys + shared options API remains compatible.
type SubmissionBatchItem struct {
	APIKey     string                    `json:"api_key"`
	Connection SubmissionConnectionInput `json:"connection"`
}

func (s *AccountSubmissionService) SubmitBatchItems(ctx context.Context, token string, items []SubmissionBatchItem, options SubmissionOptions) error {
	if len(items) < 1 || len(items) > MaxSubmissionBatchKeys || options.Connection != nil || len(options.PerKeyConnections) != 0 {
		return ErrSubmissionInvalid
	}
	keys := make([]string, len(items))
	options.PerKeyConnections = make([]SubmissionConnectionInput, len(items))
	for n, item := range items {
		keys[n] = item.APIKey
		options.PerKeyConnections[n] = item.Connection
		if item.Connection.Proxy != nil {
			proxy := *item.Connection.Proxy
			options.PerKeyConnections[n].Proxy = &proxy
		}
	}
	return s.SubmitBatch(ctx, token, keys, options)
}

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
	if options.PreparedAccount != nil || (len(options.PerKeyConnections) > 0 && (len(options.PerKeyConnections) != len(keys) || options.Connection != nil)) {
		return ErrSubmissionInvalid
	}
	// Bound validation (including public proxy DNS), not just the database write.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = ValidateSubmissionOptions(ctx, &options); err != nil {
		return err
	}
	for n := range options.PerKeyConnections {
		if err = ValidateSubmissionConnection(ctx, &options.PerKeyConnections[n]); err != nil {
			return err
		}
	}
	if err = s.validateGroups(ctx, invite.Config); err != nil {
		return err
	}
	repo, ok := s.repo.(AccountSubmissionBatchRepository)
	if !ok {
		return ErrSubmissionUnavailable
	}
	return repo.SubmitInviteBatch(ctx, hash, keys, options)
}

package service

import (
	"context"
	"time"
)

type AccountSubmissionMemberRepository interface {
	ReadSubmissionMemberConnection(context.Context, string, string) (*SubmissionConnection, error)
	UpdateSubmissionMemberConnection(context.Context, string, string, int64, string, SubmissionConnectionInput) (*SubmissionConnection, error)
}

func (s *AccountSubmissionService) MemberConnection(ctx context.Context, token, ref string) (*SubmissionConnection, error) {
	if ref == "" {
		return s.Connection(ctx, token)
	}
	if !submissionRequestID.MatchString(ref) {
		return nil, ErrSubmissionInvalid
	}
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(AccountSubmissionMemberRepository)
	if !ok {
		return nil, ErrSubmissionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return repo.ReadSubmissionMemberConnection(ctx, hash, ref)
}

func (s *AccountSubmissionService) UpdateMemberConnection(ctx context.Context, token, ref string, version int64, requestID string, input SubmissionConnectionInput) (*SubmissionConnection, error) {
	if ref == "" {
		return s.UpdateConnection(ctx, token, version, requestID, input)
	}
	if !submissionRequestID.MatchString(ref) || version < 0 || !submissionRequestID.MatchString(requestID) {
		return nil, ErrSubmissionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.MemberConnection(ctx, token, ref); err != nil {
		return nil, err
	}
	if err := ValidateSubmissionConnection(ctx, &input); err != nil {
		return nil, err
	}
	hash, _ := AccountSubmissionTokenHash(token)
	repo, ok := s.repo.(AccountSubmissionMemberRepository)
	if !ok {
		return nil, ErrSubmissionUnavailable
	}
	return repo.UpdateSubmissionMemberConnection(ctx, hash, ref, version, requestID, input)
}

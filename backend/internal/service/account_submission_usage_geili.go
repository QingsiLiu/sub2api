package service

import (
	"context"
	"reflect"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrSubmissionCredentialsImmutable = infraerrors.BadRequest("SUBMISSION_CREDENTIALS_IMMUTABLE", "Externally submitted credentials cannot be replaced; create a new invitation")

// Only this allowlisted projection can cross the public capability boundary.
type SubmissionUsageBucket struct {
	Date                string  `json:"date,omitempty"`
	Model               string  `json:"model,omitempty"`
	Requests            int64   `json:"requests"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	ReferenceCost       float64 `json:"reference_cost"`
}
type SubmissionAccountUsage struct {
	AccountRef string                `json:"account_ref"`
	Name       string                `json:"name"`
	Platform   string                `json:"platform"`
	Enabled    bool                  `json:"enabled"`
	Summary    SubmissionUsageBucket `json:"summary"`
}
type SubmissionUsage struct {
	Accounts  []SubmissionAccountUsage `json:"accounts"`
	Name      string                   `json:"name"`
	Platform  string                   `json:"platform"`
	Enabled   bool                     `json:"enabled"`
	StartDate string                   `json:"start_date"`
	EndDate   string                   `json:"end_date"`
	Timezone  string                   `json:"timezone"`
	Currency  string                   `json:"currency"`
	UpdatedAt time.Time                `json:"updated_at"`
	Summary   SubmissionUsageBucket    `json:"summary"`
	Daily     []SubmissionUsageBucket  `json:"daily"`
	Models    []SubmissionUsageBucket  `json:"models"`
}
type AccountSubmissionUsageRepository interface {
	SubmissionUsage(context.Context, string, time.Time, time.Time) (*SubmissionUsage, error)
	RevokeSubmissionView(context.Context, int64) error
	ResetSubmissionView(context.Context, int64, string) error
}

// Inclusive Beijing dates, at most 90 days. No unbounded lifetime scans.
func SubmissionUsageDates(start, end string, now time.Time) (time.Time, time.Time, error) {
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	today := time.Date(now.In(zone).Year(), now.In(zone).Month(), now.In(zone).Day(), 0, 0, 0, 0, zone)
	if start == "" && end == "" {
		return today.AddDate(0, 0, -6), today.AddDate(0, 0, 1), nil
	}
	from, e1 := time.ParseInLocation("2006-01-02", start, zone)
	through, e2 := time.ParseInLocation("2006-01-02", end, zone)
	if e1 != nil || e2 != nil || from.After(through) || through.After(today) || through.Sub(from) >= 90*24*time.Hour {
		return time.Time{}, time.Time{}, ErrSubmissionInvalid
	}
	return from, through.AddDate(0, 0, 1), nil
}
func (s *AccountSubmissionService) Usage(ctx context.Context, token, start, end string) (*SubmissionUsage, error) {
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return nil, err
	}
	from, to, err := SubmissionUsageDates(start, end, time.Now())
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(AccountSubmissionUsageRepository)
	if !ok {
		return nil, ErrSubmissionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return repo.SubmissionUsage(ctx, hash, from, to)
}
func (s *AccountSubmissionService) RevokeView(ctx context.Context, id int64) error {
	repo, ok := s.repo.(AccountSubmissionUsageRepository)
	if !ok {
		return ErrSubmissionUnavailable
	}
	return repo.RevokeSubmissionView(ctx, id)
}
func (s *AccountSubmissionService) ResetView(ctx context.Context, id int64) (string, error) {
	repo, ok := s.repo.(AccountSubmissionUsageRepository)
	if !ok {
		return "", ErrSubmissionUnavailable
	}
	token, err := newSubmissionToken()
	if err != nil {
		return "", err
	}
	hash, _ := AccountSubmissionTokenHash(token)
	if err = repo.ResetSubmissionView(ctx, id, hash); err != nil {
		return "", err
	}
	return token, nil
}

// geili hook: metadata edits remain allowed; a new Key requires a new account.
func protectSubmissionCredentialUpdate(ctx context.Context, repo AccountRepository, account *Account, input *UpdateAccountInput) error {
	changed := input.Type != "" && input.Type != account.Type
	for _, key := range SensitiveCredentialKeys {
		if value, ok := input.Credentials[key]; ok && !reflect.DeepEqual(value, account.Credentials[key]) {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if protection, ok := repo.(interface {
		ProtectedAccountIDs(context.Context, []int64) (map[int64]bool, error)
	}); ok {
		ids, err := protection.ProtectedAccountIDs(ctx, []int64{account.ID})
		if err != nil {
			return err
		}
		if ids[account.ID] {
			return ErrSubmissionCredentialsImmutable
		}
	}
	return nil
}

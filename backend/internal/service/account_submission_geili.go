package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrSubmissionInvalid         = infraerrors.BadRequest("SUBMISSION_INVALID", "Invalid invitation or API key")
	ErrSubmissionUnavailable     = infraerrors.ServiceUnavailable("SUBMISSION_UNAVAILABLE", "Unable to process invitation; please try again")
	ErrSubmissionInactive        = infraerrors.New(410, "SUBMISSION_INACTIVE", "Invitation has expired or been revoked")
	ErrSubmissionConfig          = infraerrors.BadRequest("SUBMISSION_CONFIG_INVALID", "Check the invitation name, platform, groups and proxy")
	ErrSubmissionProtected       = infraerrors.BadRequest("SUBMISSION_ACCOUNT_PROTECTED", "Externally submitted accounts cannot be duplicated")
	ErrSubmissionBackupProtected = infraerrors.Forbidden("SUBMISSION_BACKUP_PROTECTED", "Database backups may contain externally submitted credentials; console downloads are disabled")
)

// AccountSubmissionConfig is entirely administrator-owned. Public callers only supply a key.
type AccountSubmissionConfig struct {
	Name           string  `json:"name"`
	Platform       string  `json:"platform"`
	GroupIDs       []int64 `json:"group_ids"`
	ProxyID        *int64  `json:"proxy_id"`
	Concurrency    int     `json:"concurrency"`
	Priority       int     `json:"priority"`
	RateMultiplier float64 `json:"rate_multiplier"`
}

type AccountSubmissionInvite struct {
	ID          int64                   `json:"id"`
	CreatedBy   int64                   `json:"created_by"`
	Config      AccountSubmissionConfig `json:"config"`
	CreatedAt   time.Time               `json:"created_at"`
	ExpiresAt   time.Time               `json:"expires_at"`
	RevokedAt   *time.Time              `json:"revoked_at"`
	SubmittedAt *time.Time              `json:"submitted_at"`
	AccountID   *int64                  `json:"account_id"`
	Status      string                  `json:"status"`
}

func (i *AccountSubmissionInvite) State(now time.Time) string {
	if i.SubmittedAt != nil {
		return "submitted"
	}
	if i.RevokedAt != nil {
		return "revoked"
	}
	if !now.Before(i.ExpiresAt) {
		return "expired"
	}
	return "pending"
}

type AccountSubmissionRepository interface {
	CreateInvite(context.Context, string, int64, AccountSubmissionConfig) (*AccountSubmissionInvite, error)
	ListInvites(context.Context, int, int) ([]AccountSubmissionInvite, int64, error)
	RevokeInvite(context.Context, int64) error
	InspectInvite(context.Context, string) (*AccountSubmissionInvite, error)
	SubmitInvite(context.Context, string, string) error
	ProtectedAccountIDs(context.Context, []int64) (map[int64]bool, error)
	HasProtectedAccounts(context.Context) (bool, error)
}

type AccountSubmissionService struct {
	repo  AccountSubmissionRepository
	admin AdminService
}

func NewAccountSubmissionService(repo AccountSubmissionRepository, admin AdminService) *AccountSubmissionService {
	return &AccountSubmissionService{repo: repo, admin: admin}
}

func AccountSubmissionTokenHash(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return "", ErrSubmissionInvalid
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), nil
}

func ValidateAccountSubmissionConfig(c *AccountSubmissionConfig) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || utf8.RuneCountInString(c.Name) > 100 || strings.ContainsAny(c.Name, "\r\n") ||
		(c.Platform != PlatformAnthropic && c.Platform != PlatformOpenAI) || c.Concurrency < 1 || c.Concurrency > 1000 ||
		c.Priority < 0 || c.Priority > 10000 || math.IsNaN(c.RateMultiplier) || math.IsInf(c.RateMultiplier, 0) || c.RateMultiplier < 0 || c.RateMultiplier > 999999 ||
		(c.ProxyID != nil && *c.ProxyID <= 0) || len(c.GroupIDs) > 100 {
		return ErrSubmissionConfig
	}
	seen := make(map[int64]bool, len(c.GroupIDs))
	for _, id := range c.GroupIDs {
		if id <= 0 || seen[id] {
			return ErrSubmissionConfig
		}
		seen[id] = true
	}
	return nil
}

func (s *AccountSubmissionService) Create(ctx context.Context, actor int64, c AccountSubmissionConfig) (*AccountSubmissionInvite, string, error) {
	if actor <= 0 {
		return nil, "", ErrSubmissionConfig
	}
	if err := ValidateAccountSubmissionConfig(&c); err != nil {
		return nil, "", err
	}
	if err := s.validateGroups(ctx, c); err != nil {
		return nil, "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", ErrSubmissionUnavailable
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := AccountSubmissionTokenHash(token)
	invite, err := s.repo.CreateInvite(ctx, hash, actor, c)
	if err != nil {
		return nil, "", err
	}
	return invite, token, nil
}

func (s *AccountSubmissionService) validateGroups(ctx context.Context, c AccountSubmissionConfig) error {
	if err := s.admin.ValidateAccountGroupBindings(ctx, c.GroupIDs); err != nil {
		return ErrSubmissionConfig
	}
	if err := s.admin.CheckMixedChannelRisk(ctx, 0, c.Platform, c.GroupIDs); err != nil {
		return ErrSubmissionConfig
	}
	return nil
}

func (s *AccountSubmissionService) Inspect(ctx context.Context, token string) (*AccountSubmissionInvite, error) {
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return nil, err
	}
	return s.repo.InspectInvite(ctx, hash)
}

func (s *AccountSubmissionService) Submit(ctx context.Context, token, key string) error {
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 2048 || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return ErrSubmissionInvalid
	}
	invite, err := s.repo.InspectInvite(ctx, hash)
	if err != nil {
		return err
	}
	// A lost response can safely be confirmed without creating or replacing credentials.
	if invite.State(time.Now()) == "submitted" {
		return nil
	}
	if invite.State(time.Now()) != "pending" {
		return ErrSubmissionInactive
	}
	if err := s.validateGroups(ctx, invite.Config); err != nil {
		return err
	}
	return s.repo.SubmitInvite(ctx, hash, key)
}

func (s *AccountSubmissionService) List(ctx context.Context, page, size int) ([]AccountSubmissionInvite, int64, error) {
	return s.repo.ListInvites(ctx, page, size)
}
func (s *AccountSubmissionService) Revoke(ctx context.Context, id int64) error {
	return s.repo.RevokeInvite(ctx, id)
}
func (s *AccountSubmissionService) ProtectedAccountIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	return s.repo.ProtectedAccountIDs(ctx, ids)
}
func (s *AccountSubmissionService) HasProtectedAccounts(ctx context.Context) (bool, error) {
	return s.repo.HasProtectedAccounts(ctx)
}

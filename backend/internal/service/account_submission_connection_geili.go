package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrSubmissionConnectionConflict = infraerrors.Conflict("SUBMISSION_CONNECTION_CONFLICT", "Configuration changed; refresh before saving")
var submissionRequestID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type SubmissionProxyDraft struct {
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"`
	ClearPassword bool   `json:"clear_password,omitempty"`
}
type SubmissionConnectionInput struct {
	Mode  string                `json:"mode"`
	Proxy *SubmissionProxyDraft `json:"proxy,omitempty"`
}
type SubmissionOptions struct {
	PreparedAccount *Account                   `json:"-"` // Internal, validated official account form.
	Name            *string                    `json:"name,omitempty"`
	Concurrency     *int                       `json:"concurrency,omitempty"`
	Connection      *SubmissionConnectionInput `json:"connection,omitempty"`
}
type SubmissionConnection struct {
	Mode        string                `json:"mode"`
	Version     int64                 `json:"version"`
	HasPreset   bool                  `json:"has_preset"`
	Proxy       *SubmissionProxyDraft `json:"proxy,omitempty"`
	HasPassword bool                  `json:"has_password"`
}
type AccountSubmissionConnectionRepository interface {
	SubmitInviteWithOptions(context.Context, string, string, SubmissionOptions) error
	ReadSubmissionConnection(context.Context, string) (*SubmissionConnection, error)
	UpdateSubmissionConnection(context.Context, string, int64, string, SubmissionConnectionInput) (*SubmissionConnection, error)
}

func ValidateSubmissionOptions(ctx context.Context, o *SubmissionOptions) error {
	if o.Name != nil {
		name := strings.TrimSpace(*o.Name)
		o.Name = &name
		if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "\r\n") {
			return ErrSubmissionInvalid
		}
	}
	if o.Concurrency != nil && (*o.Concurrency < 1 || *o.Concurrency > 1000) {
		return ErrSubmissionInvalid
	}
	if o.Connection != nil {
		return ValidateSubmissionConnection(ctx, o.Connection)
	}
	return nil
}
func ValidateSubmissionConnection(ctx context.Context, c *SubmissionConnectionInput) error {
	switch c.Mode {
	case "preset", "direct":
		if c.Proxy != nil {
			return ErrSubmissionInvalid
		}
		return nil
	case "custom":
	default:
		return ErrSubmissionInvalid
	}
	p := c.Proxy
	if p == nil {
		return ErrSubmissionInvalid
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	if p.Name == "" || len(p.Name) > 80 || len(p.Host) == 0 || len(p.Host) > 255 || strings.ContainsAny(p.Host, "/\\@?#% \t\r\n") || p.Port < 1 || p.Port > 65535 || len(p.Username) > 100 || len(p.Password) > 100 || (p.ClearPassword && p.Password != "") {
		return ErrSubmissionInvalid
	}
	if p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks5" && p.Protocol != "socks5h" {
		return ErrSubmissionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if proxyutil.ValidatePublicProxyHost(ctx, p.Host) != nil {
		return ErrSubmissionInvalid
	}
	return nil
}
func (s *AccountSubmissionService) SubmitWithOptions(ctx context.Context, token, key string, o SubmissionOptions) error {
	if o.Name == nil && o.Concurrency == nil && o.Connection == nil {
		return s.Submit(ctx, token, key)
	}
	// Preserve the established key validation before any credential-bearing write.
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if !validSubmissionKey(key) {
		return ErrSubmissionInvalid
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
	if err = ValidateSubmissionOptions(ctx, &o); err != nil {
		return err
	}
	if err = s.validateGroups(ctx, invite.Config); err != nil {
		return err
	}
	repo, ok := s.repo.(AccountSubmissionConnectionRepository)
	if !ok {
		return ErrSubmissionUnavailable
	}
	return repo.SubmitInviteWithOptions(ctx, hash, key, o)
}
func validSubmissionKey(key string) bool {
	return key != "" && len(key) <= 2048 && strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func (s *AccountSubmissionService) Connection(ctx context.Context, token string) (*SubmissionConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(AccountSubmissionConnectionRepository)
	if !ok {
		return nil, ErrSubmissionUnavailable
	}
	return repo.ReadSubmissionConnection(ctx, hash)
}
func (s *AccountSubmissionService) UpdateConnection(ctx context.Context, token string, version int64, requestID string, c SubmissionConnectionInput) (*SubmissionConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return nil, err
	}
	if version < 0 || !submissionRequestID.MatchString(requestID) {
		return nil, ErrSubmissionInvalid
	}
	// Authenticate before resolving any submitted proxy host.
	if _, err = s.Connection(ctx, token); err != nil {
		return nil, err
	}
	if err = ValidateSubmissionConnection(ctx, &c); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(AccountSubmissionConnectionRepository)
	if !ok {
		return nil, ErrSubmissionUnavailable
	}
	return repo.UpdateSubmissionConnection(ctx, hash, version, requestID, c)
}

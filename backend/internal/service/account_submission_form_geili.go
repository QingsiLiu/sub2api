package service

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type submissionCreateContextKey struct{}
type SubmissionAccountCreate func(context.Context, *Account, []int64) (*Account, error)

// WithSubmissionAccountCreate is an internal capability boundary. The official
// account service validates the full form, then delegates the atomic write.
func WithSubmissionAccountCreate(ctx context.Context, create SubmissionAccountCreate) context.Context {
	return context.WithValue(ctx, submissionCreateContextKey{}, create)
}
func submissionAccountCreate(ctx context.Context) SubmissionAccountCreate {
	create, _ := ctx.Value(submissionCreateContextKey{}).(SubmissionAccountCreate)
	return create
}

type AccountSubmissionFormRepository interface {
	PrepareSubmissionProxy(context.Context, string, SubmissionConnectionInput) (*int64, error)
}

func (s *AccountSubmissionService) PrepareForm(ctx context.Context, token string, connection SubmissionConnectionInput) (*AccountSubmissionInvite, *int64, error) {
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return nil, nil, err
	}
	invite, err := s.repo.InspectInvite(ctx, hash)
	if err != nil {
		return nil, nil, err
	}
	if invite.State(time.Now()) != "pending" {
		return nil, nil, ErrSubmissionInactive
	}
	if err = ValidateSubmissionConnection(ctx, &connection); err != nil {
		return nil, nil, err
	}
	repo, ok := s.repo.(AccountSubmissionFormRepository)
	if !ok {
		return nil, nil, ErrSubmissionUnavailable
	}
	proxyID, err := repo.PrepareSubmissionProxy(ctx, hash, connection)
	return invite, proxyID, err
}
func (s *AccountSubmissionService) SubmitForm(ctx context.Context, token string, account *Account, groups []int64) (*Account, error) {
	hash, err := AccountSubmissionTokenHash(token)
	if err != nil {
		return nil, err
	}
	invite, err := s.repo.InspectInvite(ctx, hash)
	if err != nil {
		return nil, err
	}
	if account.Platform != invite.Config.Platform {
		return nil, ErrSubmissionInvalid
	}
	if err = s.validateGroups(ctx, invite.Config); err != nil {
		return nil, err
	}
	if account.Name == "" || account.Concurrency < 1 || account.Concurrency > 1000 || account.Priority < 0 {
		return nil, ErrSubmissionInvalid
	}
	if account.Type != AccountTypeOAuth && account.Type != AccountTypeSetupToken && account.Type != AccountTypeAPIKey && account.Type != "bedrock" && account.Type != "service_account" {
		return nil, ErrSubmissionInvalid
	}
	repo, ok := s.repo.(AccountSubmissionConnectionRepository)
	if !ok {
		return nil, ErrSubmissionUnavailable
	}
	if err = validateSubmissionFormCredentialsGeili(ctx, account); err != nil {
		return nil, err
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra["external_submission_form_geili"] = true
	account.Status = "inactive"
	account.Schedulable = false
	err = repo.SubmitInviteWithOptions(ctx, hash, "", SubmissionOptions{PreparedAccount: account})
	if err != nil {
		return nil, err
	}
	invite, err = s.repo.InspectInvite(ctx, hash)
	if err != nil || invite.AccountID == nil {
		return nil, ErrSubmissionUnavailable
	}
	account.ID = *invite.AccountID
	return account, nil
}

func validateSubmissionFormCredentialsGeili(ctx context.Context, a *Account) error {
	if a.Platform == PlatformOpenAI && a.Type != AccountTypeOAuth && a.Type != AccountTypeAPIKey {
		return ErrSubmissionInvalid
	}
	if len(a.Credentials) == 0 {
		return ErrSubmissionInvalid
	}
	has := func(k string) bool { v, ok := a.Credentials[k].(string); return ok && strings.TrimSpace(v) != "" }
	switch a.Type {
	case AccountTypeAPIKey:
		value, _ := a.Credentials["api_key"].(string)
		if !validSubmissionKey(strings.TrimSpace(value)) {
			return ErrSubmissionInvalid
		}
		a.Credentials["api_key"] = strings.TrimSpace(value)
	case AccountTypeOAuth:
		if !has("access_token") && !has("agent_private_key") {
			return ErrSubmissionInvalid
		}
	case AccountTypeSetupToken:
		if !has("access_token") {
			return ErrSubmissionInvalid
		}
	case "bedrock":
		region, _ := a.Credentials["aws_region"].(string)
		if region != "" && !regexp.MustCompile(`^[a-z0-9-]{1,64}$`).MatchString(region) {
			return ErrSubmissionInvalid
		}
		if !has("api_key") && (!has("aws_access_key_id") || !has("aws_secret_access_key")) {
			return ErrSubmissionInvalid
		}
	case "service_account":
		value := a.Credentials["service_account_json"]
		if value == nil {
			value = a.Credentials["service_account"]
		}
		var data map[string]any
		if raw, ok := value.(string); ok {
			if json.Unmarshal([]byte(raw), &data) != nil {
				return ErrSubmissionInvalid
			}
		} else {
			encoded, e := json.Marshal(value)
			if e != nil || json.Unmarshal(encoded, &data) != nil {
				return ErrSubmissionInvalid
			}
		}
		uri, _ := data["token_uri"].(string)
		if uri != "" && uri != "https://oauth2.googleapis.com/token" {
			return ErrSubmissionInvalid
		}
		if a.Credentials["service_account_json"] == nil && a.Credentials["service_account"] == nil {
			return ErrSubmissionInvalid
		}
	}
	for _, key := range []string{"base_url", "custom_base_url"} {
		raw, ok := a.Credentials[key].(string)
		if !ok || raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" || u.Fragment != "" {
			return ErrSubmissionInvalid
		}
		timed, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = proxyutil.ValidatePublicProxyHost(timed, u.Hostname())
		cancel()
		if err != nil {
			return ErrSubmissionInvalid
		}
	}
	return nil
}

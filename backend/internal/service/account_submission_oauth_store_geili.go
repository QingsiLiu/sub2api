package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// SubmissionOAuthSessionStoreGeili keeps PKCE state server-side and independent
// of the serving instance. Handlers depend on this capability, never Redis.
type SubmissionOAuthSessionStoreGeili interface {
	Save(context.Context, string, string, []byte, time.Duration) error
	Take(context.Context, string, string) ([]byte, error)
}
type submissionOAuthRedisStoreGeili struct{ client *redis.Client }

func NewSubmissionOAuthSessionStoreGeili(client *redis.Client) SubmissionOAuthSessionStoreGeili {
	if client == nil {
		return nil
	}
	return &submissionOAuthRedisStoreGeili{client: client}
}
func submissionOAuthSessionKeyGeili(id string) string {
	v := sha256.Sum256([]byte(id))
	return "account-submission-oauth:" + hex.EncodeToString(v[:])
}
func (s *submissionOAuthRedisStoreGeili) Save(ctx context.Context, id, owner string, state []byte, ttl time.Duration) error {
	if id == "" || owner == "" || len(state) == 0 || ttl <= 0 || ttl > 30*time.Minute {
		return ErrSubmissionInvalid
	}
	value, err := json.Marshal(struct {
		Owner string `json:"owner"`
		State []byte `json:"state"`
	}{owner, state})
	if err != nil {
		return ErrSubmissionUnavailable
	}
	if s.client.Set(ctx, submissionOAuthSessionKeyGeili(id), value, ttl).Err() != nil {
		return ErrSubmissionUnavailable
	}
	return nil
}
func (s *submissionOAuthRedisStoreGeili) Take(ctx context.Context, id, owner string) ([]byte, error) {
	if id == "" || owner == "" {
		return nil, ErrSubmissionInvalid
	}
	raw, err := s.client.Eval(ctx, `local v=redis.call('GET',KEYS[1]); if not v then return false end; local d=cjson.decode(v); if d.owner~=ARGV[1] then return false end; redis.call('DEL',KEYS[1]); return v`, []string{submissionOAuthSessionKeyGeili(id)}, owner).Text()
	var value struct {
		Owner string `json:"owner"`
		State []byte `json:"state"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &value) != nil || value.Owner != owner || len(value.State) == 0 {
		return nil, ErrSubmissionInvalid
	}
	return value.State, nil
}

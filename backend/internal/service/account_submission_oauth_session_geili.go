package service

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"time"
)

// Only the invitation bridge moves an already server-generated PKCE session to
// shared Redis. The browser never receives the verifier or serialized state.
func (s *OAuthService) ExportSubmissionSessionGeili(id string) ([]byte, error) {
	session, ok := s.sessionStore.Get(id)
	if !ok {
		return nil, ErrSubmissionInvalid
	}
	return json.Marshal(session)
}
func (s *OAuthService) RestoreSubmissionSessionGeili(id string, raw []byte) error {
	var session oauth.OAuthSession
	if json.Unmarshal(raw, &session) != nil || session.CodeVerifier == "" || session.State == "" || time.Since(session.CreatedAt) < -30*time.Second || time.Since(session.CreatedAt) > oauth.SessionTTL {
		return ErrSubmissionInvalid
	}
	s.sessionStore.Set(id, &session)
	return nil
}
func (s *OpenAIOAuthService) ExportSubmissionSessionGeili(id string) ([]byte, error) {
	session, ok := s.sessionStore.Get(id)
	if !ok {
		return nil, ErrSubmissionInvalid
	}
	return json.Marshal(session)
}
func (s *OpenAIOAuthService) RestoreSubmissionSessionGeili(id string, raw []byte) error {
	var session openai.OAuthSession
	if json.Unmarshal(raw, &session) != nil || session.CodeVerifier == "" || session.State == "" || time.Since(session.CreatedAt) < -30*time.Second || time.Since(session.CreatedAt) > openai.SessionTTL {
		return ErrSubmissionInvalid
	}
	s.sessionStore.Set(id, &session)
	return nil
}

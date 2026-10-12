package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSubmissionOAuthSessionGeiliContinuesAcrossInstances(t *testing.T) {
	claudeA := NewOAuthService(nil, nil)
	claudeB := NewOAuthService(nil, nil)
	t.Cleanup(claudeA.Stop)
	t.Cleanup(claudeB.Stop)
	claudeA.sessionStore.Set("owned-session", &oauth.OAuthSession{State: "owned-state", CodeVerifier: "synthetic-pkce-verifier", Scope: "user:inference", CreatedAt: time.Now()})
	raw, err := claudeA.ExportSubmissionSessionGeili("owned-session")
	require.NoError(t, err)
	require.NoError(t, claudeB.RestoreSubmissionSessionGeili("owned-session", raw))
	session, ok := claudeB.sessionStore.Get("owned-session")
	require.True(t, ok)
	require.Equal(t, "synthetic-pkce-verifier", session.CodeVerifier)
	require.Equal(t, "user:inference", session.Scope)
	openA := NewOpenAIOAuthService(nil, nil)
	openB := NewOpenAIOAuthService(nil, nil)
	t.Cleanup(openA.Stop)
	t.Cleanup(openB.Stop)
	openA.sessionStore.Set("owned-session", &openai.OAuthSession{State: "owned-state", CodeVerifier: "synthetic-pkce-verifier", ClientID: "synthetic-client", RedirectURI: "http://localhost/callback", CreatedAt: time.Now()})
	raw, err = openA.ExportSubmissionSessionGeili("owned-session")
	require.NoError(t, err)
	require.NoError(t, openB.RestoreSubmissionSessionGeili("owned-session", raw))
	second, ok := openB.sessionStore.Get("owned-session")
	require.True(t, ok)
	require.Equal(t, "synthetic-client", second.ClientID)
	require.ErrorIs(t, openB.RestoreSubmissionSessionGeili("foreign-session", []byte(`{}`)), ErrSubmissionInvalid)
}

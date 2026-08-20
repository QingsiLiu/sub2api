package trainingdata

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeHeadersRedactsCredentials(t *testing.T) {
	headers := http.Header{
		"Authorization":   []string{"Bearer secret-token-value"},
		"Cookie":          []string{"session=secret"},
		"X-Forwarded-For": []string{"203.0.113.10"},
		"X-Email":         []string{"person@example.com"},
		"User-Agent":      []string{"client/1.0"},
	}
	clean := SanitizeHeaders(headers)
	require.Equal(t, "<redacted>", clean.Get("Authorization"))
	require.Equal(t, "<redacted>", clean.Get("Cookie"))
	require.Equal(t, "<redacted>", clean.Get("X-Forwarded-For"))
	require.Equal(t, "<redacted>", clean.Get("X-Email"))
	require.Equal(t, "client/1.0", clean.Get("User-Agent"))
}

func TestRedactSecretsRemovesStandaloneTokensAndPrivateKeys(t *testing.T) {
	value := "github_pat_1234567890abcdefghijklmnop\n-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----"
	cleaned := redactSecretsInString(value)
	require.NotContains(t, cleaned, "github_pat_")
	require.NotContains(t, cleaned, "BEGIN PRIVATE KEY")
	require.Contains(t, cleaned, "<redacted-credential>")
	require.Contains(t, cleaned, "<redacted-private-key>")
}

func TestRedactJSONBodyPreservesPromptAndRemovesSecrets(t *testing.T) {
	body, ok := RedactJSONBody([]byte(`{"model":"gpt","api_key":"sk-secretvalue123","messages":[{"role":"user","content":"hello Bearer abcdefghijklmnop"}]}`))
	require.True(t, ok)
	require.JSONEq(t, `{"model":"gpt","api_key":"<redacted>","messages":[{"role":"user","content":"hello Bearer <redacted>"}]}`, string(body))
}

func TestRedactJSONBodyRejectsOpaqueBytes(t *testing.T) {
	body, ok := RedactJSONBody([]byte("not-json"))
	require.False(t, ok)
	require.Nil(t, body)
}

func TestSanitizeURLRedactsSensitiveQuery(t *testing.T) {
	got := SanitizeURL("https://example.com/v1?api_key=secret&mode=test")
	require.Contains(t, got, "api_key=%3Credacted%3E")
	require.Contains(t, got, "mode=test")
}

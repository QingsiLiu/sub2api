package trainingdata

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	bearerSecretPattern         = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+\-/]+=*`)
	inlineSecretPattern         = regexp.MustCompile(`(?i)\b(sk|rk|pk|api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password|private[_-]?key)[-_:=\s]+[A-Za-z0-9._~+\-/]{8,}`)
	standaloneCredentialPattern = regexp.MustCompile(`(?i)\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|(?:AKIA|ASIA)[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})\b`)
	pemPrivateKeyPattern        = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
)

var sensitiveHeaderNames = map[string]struct{}{
	"authorization": {}, "proxy-authorization": {}, "cookie": {}, "set-cookie": {},
	"x-api-key": {}, "api-key": {}, "x-goog-api-key": {}, "x-auth-token": {},
	"x-admin-key": {}, "x-management-key": {}, "x-openai-api-key": {},
	"x-userid": {}, "x-user-id": {}, "x-email": {}, "x-forwarded-for": {},
	"x-real-ip": {}, "cf-connecting-ip": {}, "forwarded": {},
}

var sensitiveJSONKeys = map[string]struct{}{
	"authorization": {}, "cookie": {}, "password": {}, "passwd": {}, "secret": {},
	"client_secret": {}, "api_key": {}, "apikey": {}, "access_token": {},
	"refresh_token": {}, "token": {}, "private_key": {}, "management_key": {},
}

func SanitizeHeaders(headers http.Header) http.Header {
	clean := make(http.Header, len(headers))
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if _, sensitive := sensitiveHeaderNames[strings.ToLower(strings.TrimSpace(key))]; sensitive {
			clean[canonical] = []string{"<redacted>"}
			continue
		}
		for _, value := range values {
			clean.Add(canonical, redactSecretsInString(value))
		}
	}
	return clean
}

func SanitizeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return ""
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(strings.TrimSpace(key))
		if _, sensitive := sensitiveJSONKeys[lower]; sensitive || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "key") {
			query.Set(key, "<redacted>")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// RedactJSONBody returns a normalized JSON representation with credential-like
// fields and inline bearer/API-key patterns removed. Invalid or non-JSON bodies
// are rejected rather than copied into the raw vault without deterministic
// redaction.
func RedactJSONBody(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, true
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	value = redactJSONValue(value, "")
	redacted, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return redacted, true
}

func redactJSONValue(value any, key string) any {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if _, sensitive := sensitiveJSONKeys[lowerKey]; sensitive || strings.Contains(lowerKey, "access_token") || strings.Contains(lowerKey, "refresh_token") {
		return "<redacted>"
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			typed[childKey] = redactJSONValue(child, childKey)
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = redactJSONValue(typed[index], key)
		}
		return typed
	case string:
		return redactSecretsInString(typed)
	default:
		return value
	}
}

func redactSecretsInString(value string) string {
	value = pemPrivateKeyPattern.ReplaceAllString(value, "<redacted-private-key>")
	value = bearerSecretPattern.ReplaceAllString(value, "Bearer <redacted>")
	value = standaloneCredentialPattern.ReplaceAllString(value, "<redacted-credential>")
	return inlineSecretPattern.ReplaceAllString(value, "$1=<redacted>")
}

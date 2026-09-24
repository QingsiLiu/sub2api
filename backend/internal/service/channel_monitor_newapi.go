package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// fetchNewAPIBalanceSnapshot reads the NewAPI account wallet. The monitor
// endpoint normally ends in /v1; the account endpoint is rooted at the same
// site and is queried at /api/user/self. New-Api-User (when required by an
// installation) can be supplied through the monitor's extra headers.
func fetchNewAPIBalanceSnapshot(ctx context.Context, endpoint, apiKey string, extraHeaders map[string]string) *domain.MonitorQuotaSnapshot {
	now := time.Now()
	base, err := newAPISiteBase(endpoint)
	if err != nil {
		return quotaErrorSnapshot("newapi", "invalid NewAPI endpoint", now)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/user/self", nil)
	if err != nil {
		return quotaErrorSnapshot("newapi", "invalid NewAPI balance request", now)
	}
	// Only the non-secret user ID is relevant to the account API.
	for name, value := range extraHeaders {
		if strings.EqualFold(name, "New-Api-User") {
			req.Header.Set("New-Api-User", value)
		}
	}
	// The encrypted monitor key is authoritative for this mode; an advanced
	// header must not silently replace it with a different credential.
	key := strings.TrimSpace(apiKey)
	if strings.HasPrefix(strings.ToLower(key), "bearer ") {
		req.Header.Set("Authorization", key)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// geili hook: management credentials must not follow redirects.
	client := *monitorHTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return quotaErrorSnapshot("newapi", "NewAPI balance request failed", now)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snapshot := quotaErrorSnapshot("newapi", fmt.Sprintf("NewAPI balance HTTP %d", resp.StatusCode), now)
		snapshot.CredentialInvalid = resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		return snapshot
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, monitorResponseMaxBytes+1))
	if err != nil || len(body) > monitorResponseMaxBytes {
		return quotaErrorSnapshot("newapi", "NewAPI balance response unreadable", now)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Quota json.RawMessage `json:"quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !payload.Success {
		return quotaErrorSnapshot("newapi", "NewAPI account balance response invalid", now)
	}
	quota, ok := newAPIQuotaNumber(payload.Data.Quota)
	if !ok {
		return quotaErrorSnapshot("newapi", "NewAPI account response missing quota", now)
	}
	return &domain.MonitorQuotaSnapshot{
		Source: "newapi", Success: true, Balance: &quota, Currency: "quota",
		BalanceLow: quota <= 0, FetchedAt: now,
	}
}

func newAPIQuotaNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	default:
		return 0, false
	}
}

func newAPISiteBase(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid endpoint")
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	return u.Scheme + "://" + u.Host + path, nil
}

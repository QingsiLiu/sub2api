package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const AUAPIImageDefaultBaseURL = "https://api.auapi.ai"

func (a *Account) IsAUAPIImageAccount() bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(a.GetCredential("image_provider")), "auapi") {
		return true
	}
	base := strings.TrimSpace(a.GetCredential("base_url"))
	u, err := url.Parse(base)
	return err == nil && (strings.EqualFold(u.Hostname(), "api.auapi.ai") || strings.HasSuffix(strings.ToLower(u.Hostname()), ".auapi.ai"))
}

type auapiImageRequest struct {
	Model string `json:"model"`
	Input struct {
		Prompt string `json:"prompt"`
	} `json:"input"`
	Parameters struct {
		N            int    `json:"n"`
		Size         string `json:"size,omitempty"`
		Resolution   string `json:"resolution,omitempty"`
		Quality      string `json:"quality,omitempty"`
		OutputFormat string `json:"output_format,omitempty"`
	} `json:"parameters"`
}

// Only explicitly supported parameters are accepted: silently dropping a mask,
// reference, transparency or other generation option changes the user's request.
func buildAUAPIImagePayload(body []byte) (auapiImageRequest, string, error) {
	var r auapiImageRequest
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return r, "", errors.New("invalid JSON image request")
	}
	allowed := map[string]bool{"model": true, "prompt": true, "n": true, "size": true, "resolution": true, "quality": true, "output_format": true, "stream": true, "response_format": true, "user": true}
	for key := range fields {
		if !allowed[key] {
			return r, "", fmt.Errorf("AUAPI text-to-image does not support parameter %s", key)
		}
	}
	var in struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		N              *int   `json:"n"`
		Size           string `json:"size"`
		Resolution     string `json:"resolution"`
		Quality        string `json:"quality"`
		Format         string `json:"output_format"`
		Stream         bool   `json:"stream"`
		ResponseFormat string `json:"response_format"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return r, "", errors.New("invalid image parameter type")
	}
	if strings.TrimSpace(in.Model) == "" || strings.TrimSpace(in.Prompt) == "" {
		return r, "", errors.New("model and prompt are required")
	}
	if in.Stream {
		return r, "", errors.New("streaming is not supported for asynchronous tasks")
	}
	if in.ResponseFormat != "" && in.ResponseFormat != "url" && in.ResponseFormat != "b64_json" {
		return r, "", errors.New("invalid response_format")
	}
	r.Model = in.Model
	r.Input.Prompt = in.Prompt
	r.Parameters.N = 1
	if in.N != nil {
		r.Parameters.N = *in.N
	}
	if r.Parameters.N < 1 || r.Parameters.N > 4 {
		return r, "", errors.New("n must be between 1 and 4")
	}
	if in.Size != "" && in.Size != "auto" && in.Resolution != "" {
		return r, "", errors.New("specify size or resolution, not both")
	}
	tier := "1K"
	if in.Resolution != "" {
		switch strings.ToLower(in.Resolution) {
		case "1k", "2k", "4k":
			r.Parameters.Resolution = strings.ToLower(in.Resolution)
			tier = strings.ToUpper(in.Resolution)
		default:
			return r, "", errors.New("resolution must be 1k, 2k or 4k")
		}
	} else {
		switch in.Size {
		case "", "auto":
			r.Parameters.Resolution = "1k"
		case "1024x1024", "1024x1536", "1536x1024":
			r.Parameters.Size = in.Size
			tier = NormalizeImageBillingTierOrDefault(in.Size)
			r.Parameters.Resolution = strings.ToLower(tier)
		default:
			return r, "", errors.New("AUAPI size must be 1024x1024, 1024x1536 or 1536x1024; use resolution for 2k/4k")
		}
	}
	switch in.Quality {
	case "", "auto", "low", "medium", "high":
		r.Parameters.Quality = in.Quality
	default:
		return r, "", errors.New("unsupported AUAPI quality")
	}
	switch in.Format {
	case "", "png", "jpeg", "webp":
		r.Parameters.OutputFormat = in.Format
	default:
		return r, "", errors.New("unsupported output_format")
	}
	return r, tier, nil
}

type auapiTaskStatus struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	AmountUSD string `json:"amount_usd"`
	Outputs   []struct {
		Index    int    `json:"index"`
		MimeType string `json:"mimeType"`
	} `json:"outputs"`
}

type auapiHTTPError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *auapiHTTPError) Error() string { return fmt.Sprintf("AUAPI returned HTTP %d", e.Status) }
func (e *auapiHTTPError) Retryable() bool {
	return e.Status == 429 || e.Status == 408 || e.Status >= 500
}

type auapiImageClient struct {
	account *Account
	gateway *OpenAIGatewayService
}

func (c *auapiImageClient) request(ctx context.Context, method, path string, body []byte, idem string, limit int64) ([]byte, string, error) {
	base := strings.TrimSpace(c.account.GetCredential("base_url"))
	if base == "" {
		base = AUAPIImageDefaultBaseURL
	}
	base, err := c.gateway.validateUpstreamBaseURL(base)
	if err != nil {
		return nil, "", errors.New("invalid AUAPI base URL")
	}
	// Only server-generated paths are appended, never an upstream-supplied URL.
	target := buildOpenAIEndpointURL(base, strings.SplitN(path, "?", 2)[0])
	if _, q, ok := strings.Cut(path, "?"); ok {
		u, e := url.Parse(target)
		if e != nil {
			return nil, "", e
		}
		u.RawQuery = q
		target = u.String()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.account.GetCredential("api_key"))
	req.Header.Set("Content-Type", "application/json")
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	proxy := ""
	if c.account.Proxy != nil {
		proxy = c.account.Proxy.URL()
	}
	resp, err := c.gateway.doOpenAIUpstream(req, proxy, c.account)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, "", errors.New("AUAPI transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		delay := time.Duration(0)
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
			delay = time.Duration(min(seconds, 300)) * time.Second
		} else if date, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
			delay = time.Until(date)
			if delay < 0 {
				delay = 0
			}
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
		}
		return nil, "", &auapiHTTPError{Status: resp.StatusCode, RetryAfter: delay}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", errors.New("failed to read AUAPI response")
	}
	if int64(len(raw)) > limit {
		return nil, "", errors.New("AUAPI response exceeds size limit")
	}
	return raw, resp.Header.Get("Content-Type"), nil
}
func (c *auapiImageClient) json(ctx context.Context, method, path string, body []byte, idem string, out any) error {
	raw, _, err := c.request(ctx, method, path, body, idem, 2<<20)
	if err != nil {
		return err
	}
	if out == nil {
		if !json.Valid(raw) {
			return errors.New("invalid AUAPI JSON response")
		}
		return nil
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return errors.New("invalid AUAPI JSON response")
	}
	return nil
}
func (c *auapiImageClient) Estimate(ctx context.Context, body []byte, idem string) error {
	return c.json(ctx, "POST", "/v1/pricing/estimate", body, idem, nil)
}
func (c *auapiImageClient) Submit(ctx context.Context, body []byte, idem string) (string, error) {
	var out auapiTaskStatus
	err := c.json(ctx, "POST", "/v1/images/tasks", body, idem, &out)
	if err != nil {
		return "", err
	}
	if !validAUAPITaskID(out.ID) {
		return "", errors.New("invalid AUAPI task ID")
	}
	return out.ID, nil
}
func validAUAPITaskID(id string) bool {
	if len(id) == 0 || len(id) > 191 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (c *auapiImageClient) Status(ctx context.Context, id string) (auapiTaskStatus, error) {
	var out auapiTaskStatus
	err := c.json(ctx, "GET", "/v1/tasks/"+url.PathEscape(id), nil, "", &out)
	return out, err
}
func (c *auapiImageClient) Content(ctx context.Context, id string, index int, limit int64) ([]byte, string, error) {
	return c.request(ctx, "GET", "/v1/tasks/"+url.PathEscape(id)+"/content?index="+strconv.Itoa(index), nil, "", limit)
}

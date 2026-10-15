package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// This opt-in protocol uses the durable AUAPI worker for all three GPT models.
// The ordinary OpenAI Images and legacy AUAPI accounts retain their protocols.
func (a *Account) IsAUAPIGPTAsyncAccount() bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeAPIKey &&
		strings.EqualFold(strings.TrimSpace(a.GetCredential("image_provider")), "auapi_gpt_async")
}

func isAUAPIGPTAsyncModel(model string) bool {
	return model == "gpt-image-2" || model == "gpt-image-2.5-flare" || model == "gpt-image-2.5-sunburst"
}

type auapiGPTPrepared struct {
	request    auapiImageRequest
	tier, hash string
	uploads    []OpenAIImagesUpload
}

var auapiGPTReferenceField = regexp.MustCompile(`^image(\[([0-9]+)?\])?$`)

// Exact image-studio presets. GPT v2 tariffs use pixels/area, so a 1536x1024
// reference edit remains the site's 1K tier rather than a max-edge 2K guess.
var auapiGPTImageSizes = map[string]string{
	"1024x1024": "1K", "1536x1024": "1K", "1024x1536": "1K", "1024x768": "1K", "768x1024": "1K", "1536x864": "1K", "864x1536": "1K",
	"2048x2048": "2K", "2016x1344": "2K", "1344x2016": "2K", "2048x1536": "2K", "1536x2048": "2K", "2048x1152": "2K", "1152x2048": "2K",
	"2880x2880": "4K", "3456x2304": "4K", "2304x3456": "4K", "3264x2448": "4K", "2448x3264": "4K", "3840x2160": "4K", "2160x3840": "4K",
}

// SubmitImageRequest keeps the original input hash separate from uploaded URLs:
// replaying an idempotency key does not upload again or create another task.
func (s *AUAPIImageTaskService) SubmitImageRequest(ctx context.Context, key *APIKey, sub *UserSubscription, body []byte, parsed *OpenAIImagesRequest, idem string) (*ImageTask, bool, error) {
	if key == nil || key.GroupID == nil {
		return nil, false, infraerrors.BadRequest("invalid_request_error", "invalid image request")
	}
	model := ""
	if parsed != nil {
		model = parsed.Model
	} else {
		var in struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &in)
		model = in.Model
	}
	list, err := s.accounts.ListByGroup(ctx, *key.GroupID)
	if err != nil {
		return nil, false, ErrImageTaskUnavailable.WithCause(err)
	}
	for i := range list {
		if list[i].IsAUAPIGPTAsyncAccount() && list[i].IsModelSupported(model) {
			if parsed == nil {
				return nil, false, infraerrors.BadRequest("invalid_request_error", "invalid GPT asynchronous image request")
			}
			prepared, err := prepareAUAPIGPTImages(body, parsed)
			if err != nil {
				return nil, false, infraerrors.BadRequest("invalid_request_error", err.Error())
			}
			return s.submitMediaRequest(ctx, key, sub, body, idem, "image", prepared)
		}
	}
	if parsed != nil && parsed.Endpoint == openAIImagesEditsEndpoint {
		return nil, false, infraerrors.BadRequest("invalid_request_error", "AUAPI edits require an explicitly configured GPT asynchronous account")
	}
	return s.Submit(ctx, key, sub, body, idem)
}

func prepareAUAPIGPTImages(body []byte, p *OpenAIImagesRequest) (*auapiGPTPrepared, error) {
	if !isAUAPIGPTAsyncModel(p.Model) || p.Stream || p.HasMask || p.MaskUpload != nil || p.MaskImageURL != "" || p.PartialImages != nil || p.Style != "" {
		return nil, errors.New("unsupported GPT asynchronous image model or option")
	}
	allowed := map[string]bool{"model": true, "prompt": true, "n": true, "size": true, "quality": true, "background": true, "output_format": true, "output_compression": true, "moderation": true, "input_fidelity": true, "response_format": true, "user": true, "stream": true, "image": true, "images": true, "image_url": true, "image_urls": true}
	if p.Multipart {
		_, params, err := mime.ParseMediaType(p.ContentType)
		if err != nil {
			return nil, err
		}
		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		seen := map[string]bool{}
		for {
			part, err := r.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			name := part.FormName()
			if part.FileName() == "" {
				if !allowed[name] || seen[name] || name == "image" || name == "images" || name == "image_url" || name == "image_urls" {
					return nil, fmt.Errorf("unsupported or duplicate GPT field %s", name)
				}
				seen[name] = true
			} else if !auapiGPTReferenceField.MatchString(name) {
				return nil, fmt.Errorf("unsupported GPT file field %s", name)
			}
			part.Close()
		}
	} else {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, err
		}
		for name := range fields {
			if !allowed[name] {
				return nil, fmt.Errorf("unsupported GPT field %s", name)
			}
		}
	}
	if p.N < 1 || p.N > 10 || strings.TrimSpace(p.Prompt) == "" {
		return nil, errors.New("prompt and n between 1 and 10 are required")
	}
	if p.ResponseFormat != "" && p.ResponseFormat != "url" && p.ResponseFormat != "b64_json" {
		return nil, errors.New("unsupported response_format")
	}
	var request auapiImageRequest
	request.Kind, request.Action, request.Model, request.Input.Prompt = "image", "generate", p.Model, p.Prompt
	request.Parameters.N, request.Parameters.Size = p.N, p.Size
	if request.Parameters.Size == "" {
		request.Parameters.Size = "1024x1024"
	}
	tier, ok := auapiGPTImageSizes[request.Parameters.Size]
	if !ok {
		return nil, errors.New("choose an explicit supported GPT image size; automatic size has no fixed customer tariff")
	}
	request.Parameters.Resolution = strings.ToLower(tier)
	request.Parameters.Quality = p.Quality
	if request.Parameters.Quality == "" || request.Parameters.Quality == "auto" {
		request.Parameters.Quality = "low"
	}
	switch request.Parameters.Quality {
	case "low", "medium", "high":
	case "xhigh", "max":
		if p.Model == "gpt-image-2" {
			return nil, errors.New("quality requires GPT Image 2.5")
		}
	default:
		return nil, errors.New("unsupported GPT quality")
	}
	if p.Background != "" && p.Background != "auto" && p.Background != "opaque" && p.Background != "transparent" {
		return nil, errors.New("unsupported background")
	}
	if p.OutputFormat != "" && p.OutputFormat != "png" && p.OutputFormat != "jpeg" && p.OutputFormat != "webp" {
		return nil, errors.New("unsupported output_format")
	}
	request.Parameters.Background, request.Parameters.OutputFormat = p.Background, p.OutputFormat
	if p.Background == "transparent" && p.OutputFormat == "jpeg" {
		return nil, errors.New("JPEG cannot have a transparent background")
	}
	if p.Model == "gpt-image-2" && p.Background == "transparent" {
		return nil, errors.New("transparent background requires GPT Image 2.5")
	}
	if p.OutputCompression != nil && (*p.OutputCompression < 0 || *p.OutputCompression > 100 || (p.OutputFormat != "jpeg" && p.OutputFormat != "webp")) {
		return nil, errors.New("output_compression requires JPEG or WebP and 0 through 100")
	}
	request.Parameters.OutputCompression, request.Parameters.Moderation, request.Parameters.InputFidelity = p.OutputCompression, p.Moderation, p.InputFidelity
	if p.InputFidelity != "" && p.Endpoint != openAIImagesEditsEndpoint {
		return nil, errors.New("input_fidelity requires edits")
	}
	if p.Moderation != "" && p.Moderation != "auto" && p.Moderation != "low" {
		return nil, errors.New("unsupported moderation")
	}
	if p.InputFidelity != "" && p.InputFidelity != "low" && p.InputFidelity != "high" {
		return nil, errors.New("unsupported input_fidelity")
	}
	if p.Endpoint == openAIImagesEditsEndpoint {
		request.Action = "edit"
		if len(p.InputImageURLs)+len(p.Uploads) < 1 || len(p.InputImageURLs)+len(p.Uploads) > 16 {
			return nil, errors.New("edits require 1 through 16 reference images")
		}
	} else if len(p.InputImageURLs)+len(p.Uploads) != 0 {
		return nil, errors.New("reference images require the edits endpoint")
	}
	for _, link := range p.InputImageURLs {
		if !validAUAPIReferenceURL(link) {
			return nil, errors.New("references must use HTTPS")
		}
	}
	request.Parameters.ImageURLs = append([]string(nil), p.InputImageURLs...)
	hashes := []string{}
	for _, upload := range p.Uploads {
		ct := detectedImageContentType(upload.Data)
		if len(upload.Data) == 0 || len(upload.Data) > 10<<20 || (ct != "image/png" && ct != "image/jpeg" && ct != "image/webp") {
			return nil, errors.New("reference must be PNG/JPEG/WebP up to 10 MiB")
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(upload.Data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return nil, errors.New("invalid reference dimensions")
		}
		hashes = append(hashes, HashUsageRequestPayload(upload.Data))
	}
	canonical, err := json.Marshal(struct {
		Request    auapiImageRequest
		References []string
	}{request, hashes})
	if err != nil {
		return nil, err
	}
	return &auapiGPTPrepared{request: request, tier: tier, hash: HashUsageRequestPayload(canonical), uploads: p.Uploads}, nil
}

func validAUAPIReferenceURL(link string) bool {
	u, err := url.Parse(link)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func (c *auapiImageClient) uploadGPTReference(ctx context.Context, upload OpenAIImagesUpload) (string, error) {
	// No image procurement occurs here. Only the JSON task, persisted below,
	// may start generation; raw bytes and temporary files never enter the task.
	base, err := c.gateway.validateUpstreamBaseURL(strings.TrimSpace(c.account.GetCredential("base_url")))
	if err != nil {
		return "", errors.New("invalid AUAPI base URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildOpenAIEndpointURL(base, "/v1/media/references"), bytes.NewReader(upload.Data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.account.GetCredential("api_key"))
	req.Header.Set("Content-Type", detectedImageContentType(upload.Data))
	proxy := ""
	if c.account.Proxy != nil {
		proxy = c.account.Proxy.URL()
	}
	resp, err := c.gateway.doOpenAIUpstream(req, proxy, c.account)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return "", errors.New("AUAPI reference upload failed; no task submitted")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New("AUAPI reference upload rejected; no task submitted")
	}
	var out struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
		Size     int    `json:"size"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || json.Unmarshal(raw, &out) != nil || !validAUAPIReferenceURL(out.URL) || out.Size != len(upload.Data) || out.MimeType != detectedImageContentType(upload.Data) {
		return "", errors.New("invalid AUAPI reference upload receipt")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(upload.Data))
	if err != nil || out.Width != cfg.Width || out.Height != cfg.Height {
		return "", errors.New("AUAPI reference dimensions changed")
	}
	return out.URL, nil
}

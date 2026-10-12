package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

type SubmissionFormHandlerGeili struct {
	handlers *AdminHandlers
	redis    *redis.Client
}

func NewSubmissionFormHandlerGeili(h *AdminHandlers, r *redis.Client) *SubmissionFormHandlerGeili {
	return &SubmissionFormHandlerGeili{h, r}
}

type submissionFormRequestGeili struct {
	Token      string                            `json:"token"`
	Action     string                            `json:"action"`
	Payload    map[string]any                    `json:"payload"`
	Connection service.SubmissionConnectionInput `json:"connection"`
}

func submissionOAuthSessionKeyGeili(id string) string {
	v := sha256.Sum256([]byte(id))
	return "account-submission-oauth:" + hex.EncodeToString(v[:])
}
func (h *SubmissionFormHandlerGeili) Handle(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	var req submissionFormRequestGeili
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || dec.Decode(&struct{}{}) != io.EOF || req.Payload == nil || h.redis == nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	invite, err := h.handlers.Account.SubmissionFormService().Inspect(c.Request.Context(), req.Token)
	var proxyID *int64
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if invite.Status == "submitted" && invite.ViewRevokedAt == nil && (req.Action == "create" || req.Action == "codex-pat" || req.Action == "codex-import") {
		if req.Action == "codex-import" {
			response.Success(c, gin.H{"created": 1, "updated": 0, "skipped": 0, "failed": 0})
		} else {
			response.Success(c, gin.H{"id": 0, "status": "submitted"})
		}
		return
	}
	if invite.Status != "pending" {
		response.ErrorFrom(c, service.ErrSubmissionInactive)
		return
	}
	tokenHash, err := service.AccountSubmissionTokenHash(req.Token)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var action gin.HandlerFunc
	create := false
	switch req.Action {
	case "create":
		action = h.handlers.Account.CreateSubmissionFormAccount
		create = true
	case "profiles":
		action = h.handlers.TLSFingerprintProfile.List
	case "settings":
		action = h.handlers.Setting.GetSettings
	case "web-search":
		action = h.handlers.Setting.GetWebSearchEmulationConfig
	case "mixed-risk":
		action = h.handlers.Account.CheckMixedChannel
	case "models-preview":
		action = h.handlers.Account.SyncUpstreamModelsPreview
	case "openai-models":
		action = h.handlers.Setting.GetSettings
	case "/admin/accounts/generate-auth-url":
		if invite.Config.Platform == service.PlatformAnthropic {
			action = h.handlers.OAuth.GenerateAuthURL
		}
	case "/admin/accounts/generate-setup-token-url":
		if invite.Config.Platform == service.PlatformAnthropic {
			action = h.handlers.OAuth.GenerateSetupTokenURL
		}
	case "/admin/accounts/exchange-code":
		if invite.Config.Platform == service.PlatformAnthropic {
			action = h.handlers.OAuth.ExchangeCode
		}
	case "/admin/accounts/exchange-setup-token-code":
		if invite.Config.Platform == service.PlatformAnthropic {
			action = h.handlers.OAuth.ExchangeSetupTokenCode
		}
	case "/admin/accounts/cookie-auth":
		if invite.Config.Platform == service.PlatformAnthropic {
			action = h.handlers.OAuth.CookieAuth
		}
	case "/admin/accounts/setup-token-cookie-auth":
		if invite.Config.Platform == service.PlatformAnthropic {
			action = h.handlers.OAuth.SetupTokenCookieAuth
		}
	case "/admin/openai/generate-auth-url":
		if invite.Config.Platform == service.PlatformOpenAI {
			action = h.handlers.OpenAIOAuth.GenerateAuthURL
		}
	case "/admin/openai/exchange-code":
		if invite.Config.Platform == service.PlatformOpenAI {
			action = h.handlers.OpenAIOAuth.ExchangeCode
		}
	case "/admin/openai/refresh-token":
		if invite.Config.Platform == service.PlatformOpenAI {
			action = h.handlers.OpenAIOAuth.RefreshToken
		}
	case "codex-import":
		if invite.Config.Platform == service.PlatformOpenAI && h.handlers.Account.ValidateSingleSubmissionCodexImport(req.Payload) {
			action = h.handlers.Account.ImportSubmissionCodexSession
			create = true
		}
	case "codex-pat":
		if invite.Config.Platform == service.PlatformOpenAI {
			action = h.handlers.OpenAIOAuth.CreateAccountFromCodexPAT
			create = true
		}
	}
	if action == nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	readonly := req.Action == "profiles" || req.Action == "settings" || req.Action == "web-search" || req.Action == "openai-models" || req.Action == "mixed-risk"
	if !readonly {
		_, proxyID, err = h.handlers.Account.SubmissionFormService().PrepareForm(c.Request.Context(), req.Token, req.Connection)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	if value, ok := req.Payload["platform"].(string); ok && value != invite.Config.Platform {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	req.Payload["platform"] = invite.Config.Platform
	// External payloads never choose administrator-owned identifiers or update targets.
	req.Payload["proxy_id"] = proxyID
	req.Payload["group_ids"] = invite.Config.GroupIDs
	req.Payload["skip_default_group_bind"] = true
	req.Payload["update_existing"] = false
	delete(req.Payload, "account_id")
	delete(req.Payload, "id")
	if create {
		if platform, ok := req.Payload["platform"].(string); ok && platform != invite.Config.Platform {
			response.ErrorFrom(c, service.ErrSubmissionInvalid)
			return
		}
		req.Payload["platform"] = invite.Config.Platform
	}
	if strings.Contains(req.Action, "exchange") {
		id, _ := req.Payload["session_id"].(string)
		flow := strings.Replace(req.Action, "exchange-setup-token-code", "generate-setup-token-url", 1)
		flow = strings.Replace(flow, "exchange-code", "generate-auth-url", 1)
		// Compare the invitation and flow before atomically consuming shared PKCE
		// state, so another invitation cannot burn a valid session or race a replay.
		raw, e := h.redis.Eval(c.Request.Context(), `local v=redis.call('GET',KEYS[1]); if not v then return false end; local d=cjson.decode(v); if d.owner~=ARGV[1] then return false end; redis.call('DEL',KEYS[1]); return v`, []string{submissionOAuthSessionKeyGeili(id)}, tokenHash+":"+invite.Config.Platform+":"+flow).Text()
		var session struct {
			Owner string `json:"owner"`
			State []byte `json:"state"`
		}
		if id == "" || e != nil || json.Unmarshal([]byte(raw), &session) != nil {
			response.ErrorFrom(c, service.ErrSubmissionInvalid)
			return
		}
		if invite.Config.Platform == service.PlatformOpenAI {
			e = h.handlers.OpenAIOAuth.RestoreSubmissionSessionGeili(id, session.State)
		} else {
			e = h.handlers.OAuth.RestoreSubmissionSessionGeili(id, session.State)
		}
		if e != nil {
			response.ErrorFrom(c, service.ErrSubmissionInvalid)
			return
		}
	}
	raw, err := json.Marshal(req.Payload)
	if err != nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	recorder := httptest.NewRecorder()
	child, _ := gin.CreateTestContext(recorder)
	child.Request = c.Request.Clone(service.WithSubmissionAccountCreate(service.WithSubmissionPublicEgressGeili(c.Request.Context()), func(ctx context.Context, a *service.Account, groups []int64) (*service.Account, error) {
		return h.handlers.Account.SubmissionFormService().SubmitForm(ctx, req.Token, a, groups)
	}))
	child.Set("geili_submission_proxy_id", proxyID)
	child.Request.Body = io.NopCloser(bytes.NewReader(raw))
	child.Request.ContentLength = int64(len(raw))
	child.Request.Header = make(http.Header)
	child.Request.Header.Set("Content-Type", "application/json")
	action(child)
	var result struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if recorder.Code < 200 || recorder.Code >= 300 || json.Unmarshal(recorder.Body.Bytes(), &result) != nil || result.Code != 0 {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	var data map[string]any
	if create {
		// Import handlers may report partial per-item failures despite a 200 envelope.
		if req.Action == "codex-import" {
			if json.Unmarshal(result.Data, &data) != nil || data["created"] != float64(1) || data["failed"] != float64(0) {
				response.ErrorFrom(c, service.ErrSubmissionInvalid)
				return
			}
			response.Success(c, gin.H{"created": 1, "updated": 0, "skipped": 0, "failed": 0})
			return
		}
		response.Success(c, gin.H{"id": 0, "status": "submitted"})
		return
	}
	if req.Action == "openai-models" {
		if json.Unmarshal(result.Data, &data) != nil {
			response.ErrorFrom(c, service.ErrSubmissionUnavailable)
			return
		}
		response.Success(c, data["openai_sync_model_ids"])
		return
	}
	if req.Action == "profiles" {
		var profiles []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(result.Data, &profiles) != nil {
			response.ErrorFrom(c, service.ErrSubmissionUnavailable)
			return
		}
		response.Success(c, profiles)
		return
	}
	if json.Unmarshal(result.Data, &data) != nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	if req.Action == "mixed-risk" {
		response.Success(c, gin.H{"has_risk": data["has_risk"], "message": "请确认管理员允许此邀请使用所选认证方式。"})
		return
	}
	if req.Action == "settings" {
		response.Success(c, gin.H{"account_quota_notify_enabled": data["account_quota_notify_enabled"], "openai_sync_model_ids": data["openai_sync_model_ids"]})
		return
	}
	if req.Action == "web-search" {
		// The form needs capability booleans, never provider tokens or URLs.
		providers, _ := data["providers"].([]any)
		names := make([]gin.H, len(providers))
		for i := range names {
			names[i] = gin.H{"enabled": true}
		}
		response.Success(c, gin.H{"enabled": data["enabled"], "providers": names})
		return
	}
	if id, ok := data["session_id"].(string); ok && id != "" && strings.Contains(req.Action, "generate") {
		var state []byte
		if invite.Config.Platform == service.PlatformOpenAI {
			state, err = h.handlers.OpenAIOAuth.ExportSubmissionSessionGeili(id)
		} else {
			state, err = h.handlers.OAuth.ExportSubmissionSessionGeili(id)
		}
		if err != nil {
			response.ErrorFrom(c, service.ErrSubmissionUnavailable)
			return
		}
		session, _ := json.Marshal(struct {
			Owner string `json:"owner"`
			State []byte `json:"state"`
		}{tokenHash + ":" + invite.Config.Platform + ":" + req.Action, state})
		ttl := time.Until(invite.ExpiresAt)
		if ttl > 30*time.Minute {
			ttl = 30 * time.Minute
		}
		if ttl <= 0 || h.redis.Set(c.Request.Context(), submissionOAuthSessionKeyGeili(id), session, ttl).Err() != nil {
			response.ErrorFrom(c, service.ErrSubmissionUnavailable)
			return
		}
	}
	response.Success(c, json.RawMessage(result.Data))
}

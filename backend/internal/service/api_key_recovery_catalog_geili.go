package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/tidwall/gjson"
)

// The HTTP route caller opts in only for its existing text recovery paths.
// Tools and tool history remain ordinary text; retained response IDs and
// protected encrypted continuity keep their existing fail-closed route rules.
func ExplicitKeyStoppedTextRecoveryAllowedGeili(ctx context.Context, body []byte) bool {
	if !AccountRecoveryScopeAllowedGeili(ctx) || !RequestRecoveryAllowed(ctx) || OpenAIEncryptedRouteProtectedGeili(ctx) || strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String()) != "" {
		return false
	}
	if model := gjson.GetBytes(body, "model").String(); model != "" && !accountRecoveryProbeTextModelGeili(model) {
		return false
	}
	for _, modality := range gjson.GetBytes(body, "modalities").Array() {
		if modality.String() == "audio" || modality.String() == "image" || modality.String() == "video" {
			return false
		}
	}
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		switch tool.Get("type").String() {
		case "image_generation", "video_generation":
			return false
		}
	}
	return true
}

func (r *CompositeRouteResolver) explicitRecoverableTextModelGeili(ctx context.Context, group *Group, model, endpoint string) (bool, error) {
	if !AccountRecoveryScopeAllowedGeili(ctx) || group == nil || !group.ModelAllowlist.Allows(model) || !accountRecoveryProbeTextModelGeili(model) || !RequestRecoveryAllowed(ctx) || OpenAIEncryptedRouteProtectedGeili(ctx) {
		return false, nil
	}
	switch endpoint {
	case CompositeRouteEndpointMessages, CompositeRouteEndpointChatCompletions, CompositeRouteEndpointResponses:
	default:
		return false, nil
	}
	runtime, ok := r.accountRepo.(AccountRuntimeRepositoryGeili)
	if !ok {
		return false, nil
	}
	remaining := accountRecoveryForegroundRemainingGeili(ctx, true)
	if remaining <= 0 {
		return false, nil
	}
	if remaining > 3*time.Second {
		remaining = 3 * time.Second
	}
	scanCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	for page := 1; page > 0; {
		candidates, next, err := listAccountRecoveryCandidatePageGeili(scanCtx, r.accountRepo, group.ID, group.Platform, page)
		if err != nil {
			return false, err
		}
		for i := range candidates {
			a := &candidates[i]
			if a.Platform != group.Platform || !accountRecoveryGroupMemberGeili(a, group.ID) || !explicitAccountDeclaresModelGeili(a, model) {
				continue
			}
			fresh, err := r.accountRepo.GetByID(scanCtx, a.ID)
			if err != nil {
				return false, err
			}
			if fresh == nil || fresh.Platform != group.Platform || !accountRecoveryGroupMemberGeili(fresh, group.ID) || !explicitAccountDeclaresModelGeili(fresh, model) {
				continue
			}
			state, err := runtime.GetAccountRuntimeGeili(scanCtx, fresh.ID)
			if err != nil {
				return false, err
			}
			fresh.RuntimeState = state
			verdict, err := runtime.CheckAccountRuntimeAdmissionGeili(scanCtx, fresh.ID, model)
			if err != nil || verdict == nil {
				return false, infraerrors.ServiceUnavailable("MODEL_CATALOG_UNAVAILABLE", "account model catalog is unavailable")
			}
			if verdict.BalanceBlocked || verdict.Reason == "balance" || state != nil && state.BalanceBlocked {
				continue
			}
			if !recoveryAccountEligibleGeili(fresh, model, time.Now(), true) || !accountRecoveryProbeTextModelGeili(fresh.GetMappedModel(model)) {
				continue
			}
			if fresh.IsActive() && fresh.Schedulable && fresh.IsSchedulable() && (state == nil || !state.Models[model].Blocked) {
				continue
			}
			return true, nil
		}
		page = next
	}
	return false, nil
}

func explicitAccountDeclaresModelGeili(a *Account, model string) bool {
	if a == nil {
		return false
	}
	if _, ok := a.GetModelMapping()[model]; ok {
		return true
	}
	// OAuth defaults are the same explicit provider catalog used by the model
	// list. API-key transports never infer support from an arbitrary model name.
	if a.Type != AccountTypeOAuth {
		return false
	}
	switch a.Platform {
	case PlatformOpenAI:
		for _, known := range openai.DefaultModelIDs() {
			if known == model {
				return true
			}
		}
	case PlatformAnthropic:
		for _, known := range claude.DefaultModels {
			if known.ID == model {
				return true
			}
		}
	case PlatformGemini:
		for _, known := range geminicli.DefaultModels {
			if known.ID == model {
				return true
			}
		}
	}
	return false
}

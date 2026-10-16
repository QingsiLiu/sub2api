package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type geminiSupplierSelectionModelKeyGeili struct{}

// Native handlers resolve the channel mapping before selecting an account.
// Mark that exact selection model so a chained mapping is never applied twice.
func WithGeminiSupplierSelectionModelGeili(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, geminiSupplierSelectionModelKeyGeili{}, model)
}

type geminiSupplierRecoveryKeyGeili struct{}

// Native endpoints share exclusions without body prereads or changing their
// existing recovery, media, cache and account-probe budgets.
func WithGeminiSupplierRecoveryGeili(ctx context.Context) context.Context {
	if geminiSupplierRecoveryStateGeili(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, geminiSupplierRecoveryKeyGeili{}, &requestRecoveryState{})
}

func geminiSupplierRecoveryStateGeili(ctx context.Context) *requestRecoveryState {
	if ctx == nil {
		return nil
	}
	if state := requestRecoveryFromContext(ctx); state != nil {
		return state
	}
	state, _ := ctx.Value(geminiSupplierRecoveryKeyGeili{}).(*requestRecoveryState)
	return state
}

// These exclusions belong to one downstream request. They neither stop an
// account persistently nor add health-store reads to the healthy request path.
func RecordGeminiSupplierFailureGeili(ctx context.Context, account *Account, model string, balance bool) {
	state := geminiSupplierRecoveryStateGeili(ctx)
	if state == nil || account == nil || account.Platform != PlatformGemini {
		return
	}
	key := strings.TrimSpace(model)
	if balance {
		key = "" // Supplier balance belongs to the account, across models.
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.geminiSupplierFailures == nil {
		state.geminiSupplierFailures = make(map[int64]map[string]bool)
	}
	if state.geminiSupplierFailures[account.ID] == nil {
		state.geminiSupplierFailures[account.ID] = make(map[string]bool)
	}
	state.geminiSupplierFailures[account.ID][key] = true
}

func GeminiSupplierFailuresSeenGeili(ctx context.Context) bool {
	state := geminiSupplierRecoveryStateGeili(ctx)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.geminiSupplierFailures) != 0
}

func (s *GatewayService) geminiSupplierAccountExcludedGeili(ctx context.Context, account *Account, model string) bool {
	if account == nil || account.Platform != PlatformGemini {
		return false
	}
	state := geminiSupplierRecoveryStateGeili(ctx)
	if state == nil {
		return false
	}
	state.mu.Lock()
	failed := state.geminiSupplierFailures[account.ID]
	if len(failed) == 0 {
		state.mu.Unlock()
		return false
	}
	if failed[""] {
		state.mu.Unlock()
		return true
	}
	// Copy before consulting channel mapping: no other work runs under the lock.
	models := make(map[string]bool, len(failed))
	for name := range failed {
		models[name] = true
	}
	state.mu.Unlock()
	if selected, applied := ctx.Value(geminiSupplierSelectionModelKeyGeili{}).(string); applied {
		model = selected
	} else if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil && s != nil && s.channelService != nil {
		if mapping := s.channelService.ResolveChannelMapping(ctx, group.ID, model); mapping.Mapped {
			model = mapping.MappedModel
		}
	}
	return models[strings.TrimSpace(account.GetMappedModel(model))]
}

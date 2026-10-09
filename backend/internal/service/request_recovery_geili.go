package service

import (
	"context"
	"sync"
	"time"
)

const defaultRequestRecoveryBudget = 600 * time.Second

type requestRecoveryContextKey struct{}

// requestRecoveryState belongs to the client request, not an account/group
// attempt. Its clock starts only after the first recoverable upstream failure.
type requestRecoveryState struct {
	mu              sync.Mutex
	budget          time.Duration
	deadline        time.Time
	now             func() time.Time
	signatureUsed   bool
	selectionWaits  int
	replayForbidden bool
}

func WithRequestRecovery(ctx context.Context, budget time.Duration) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if requestRecoveryFromContext(ctx) != nil {
		return ctx
	}
	if budget <= 0 {
		budget = defaultRequestRecoveryBudget
	}
	return context.WithValue(ctx, requestRecoveryContextKey{}, &requestRecoveryState{budget: budget, now: time.Now})
}

func requestRecoveryFromContext(ctx context.Context) *requestRecoveryState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(requestRecoveryContextKey{}).(*requestRecoveryState)
	return state
}

// BeginRequestRecovery starts the immutable recovery window. Call only for a
// retryable upstream failure; local admission errors must not consume it.
func BeginRequestRecovery(ctx context.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	state := requestRecoveryFromContext(ctx)
	if state == nil {
		return true // Direct service callers without gateway middleware retain their contract.
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.replayForbidden {
		return false
	}
	if state.deadline.IsZero() {
		state.deadline = state.now().Add(state.budget)
		// Retain a shorter client/composite deadline in the shared state too:
		// usage-draining code may detach cancellation from later attempts.
		if ctx != nil {
			if deadline, ok := ctx.Deadline(); ok && deadline.Before(state.deadline) {
				state.deadline = deadline
			}
		}
	}
	return state.now().Before(state.deadline)
}

// An observed built-in operation can have side effects even for a synchronous
// client that has received no bytes. Keep that verdict across group attempts.
func forbidRequestReplayGeili(ctx context.Context) {
	if state := requestRecoveryFromContext(ctx); state != nil {
		state.mu.Lock()
		state.replayForbidden = true
		state.mu.Unlock()
	}
}

// RequestRecoveryContext applies the shared deadline to a later attempt. The
// initial generation has no recovery deadline, and a parent deadline always wins.
func RequestRecoveryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	state := requestRecoveryFromContext(ctx)
	if state == nil {
		return ctx, func() {}
	}
	state.mu.Lock()
	deadline := state.deadline
	state.mu.Unlock()
	if deadline.IsZero() {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, deadline)
}

func RequestRecoveryAllowed(ctx context.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	state := requestRecoveryFromContext(ctx)
	if state == nil {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.deadline.IsZero() || state.now().Before(state.deadline)
}

// RequestRecoveryRemaining includes an earlier client/composite deadline. Before
// recovery begins this reports the configured window, without starting its clock.
func RequestRecoveryRemaining(ctx context.Context) time.Duration {
	if ctx != nil && ctx.Err() != nil {
		return 0
	}
	now := time.Now()
	remaining := defaultRequestRecoveryBudget
	if state := requestRecoveryFromContext(ctx); state != nil {
		state.mu.Lock()
		now = state.now()
		remaining = state.budget
		if !state.deadline.IsZero() {
			remaining = state.deadline.Sub(now)
		}
		state.mu.Unlock()
	}
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok && deadline.Sub(now) < remaining {
			remaining = deadline.Sub(now)
		}
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}

func ClaimThinkingSignatureRecovery(ctx context.Context) bool {
	if !BeginRequestRecovery(ctx) {
		return false
	}
	state := requestRecoveryFromContext(ctx)
	if state == nil {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.signatureUsed {
		return false
	}
	state.signatureUsed = true
	return true
}

// ClaimRequestRecoverySelectionWait bounds empty-selection waits across groups.
func ClaimRequestRecoverySelectionWait(ctx context.Context) bool {
	if !BeginRequestRecovery(ctx) {
		return false
	}
	state := requestRecoveryFromContext(ctx)
	if state == nil {
		return true // Handler state provides the fallback bound for direct callers.
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.selectionWaits >= 3 {
		return false
	}
	state.selectionWaits++
	return true
}

func IsLongThinkingRequestModel(model string) bool {
	return isClaude55SignedThinkingModel(model) || isOpenAIGPT6AstraModel(model)
}

// LongThinkingRoute resolves the same channel -> account model mapping used by
// the selected group. It only informs the default overall request budget; it
// neither changes routing nor grants additional time beyond explicit limits.
func (r *CompositeRouteResolver) LongThinkingRoute(ctx context.Context, decision CompositeRouteDecision) bool {
	model := decision.UpstreamModel
	if model == "" {
		model = decision.PublicModel
	}
	if IsLongThinkingRequestModel(model) {
		return true
	}
	if r == nil || decision.TargetGroupID == nil {
		return false
	}
	if r.pricing != nil && r.pricing.channelService != nil {
		mapped := r.pricing.channelService.ResolveChannelMapping(ctx, *decision.TargetGroupID, model)
		if mapped.Mapped {
			model = mapped.MappedModel
		}
	}
	if IsLongThinkingRequestModel(model) {
		return true
	}
	if r.accountRepo == nil {
		return false
	}
	accounts, err := r.accountRepo.ListModelAvailabilityCandidates(ctx, decision.TargetGroupID, []string{decision.TargetPlatform}, false)
	if err != nil {
		return false // Optional classification must not create a new rejection.
	}
	for i := range accounts {
		account := &accounts[i]
		if account.Platform == decision.TargetPlatform && account.IsSchedulable() && account.IsModelSupported(model) && IsLongThinkingRequestModel(account.GetMappedModel(model)) {
			return true
		}
	}
	return false
}

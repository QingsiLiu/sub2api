package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrAccountRechargeConfirmationRequiredGeili = errors.New("confirm recharge before checking this account")

type accountRecoveryProbeContextGeili struct{}
type accountRecoverySelectionContextGeili struct{}

func isAccountRecoveryProbeGeili(ctx context.Context) bool {
	v, _ := ctx.Value(accountRecoveryProbeContextGeili{}).(bool)
	return v
}

type AccountRecoveryProberGeili func(context.Context, *Account, string) (bool, error)
type AccountBalanceVerifierGeili func(context.Context, *Account) (known, sufficient bool, err error)

type accountRecoveryJobGeili struct {
	ready       chan struct{}
	once        sync.Once
	lastRequest time.Time
	restored    map[int64]struct{}
}
type AccountRecoveryServiceGeili struct {
	accounts                 AccountRepository
	runtime                  AccountRuntimeRepositoryGeili
	probe                    AccountRecoveryProberGeili
	verifyBalance            AccountBalanceVerifierGeili
	balanceLatch             AccountBalanceLatchCacheGeili
	clearSchedulingBlock     func(context.Context, int64) error
	notifySchedulingRecovery func(int64)
	ctx                      context.Context
	cancel                   context.CancelFunc
	mu                       sync.Mutex
	jobs                     map[string]*accountRecoveryJobGeili
	wg                       sync.WaitGroup
}

func NewAccountRecoveryServiceGeili(accounts AccountRepository, probe AccountRecoveryProberGeili, balance AccountBalanceVerifierGeili) *AccountRecoveryServiceGeili {
	r, ok := accounts.(AccountRuntimeRepositoryGeili)
	if !ok || probe == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &AccountRecoveryServiceGeili{accounts: accounts, runtime: r, probe: probe, verifyBalance: balance, ctx: ctx, cancel: cancel, jobs: make(map[string]*accountRecoveryJobGeili)}
}
func (s *AccountRecoveryServiceGeili) Stop() {
	if s != nil {
		s.cancel()
		s.wg.Wait()
	}
}
func (s *RateLimitService) AccountRecoveryGeili() *AccountRecoveryServiceGeili {
	if s == nil {
		return nil
	}
	return s.accountRecoveryGeili
}

func (s *AccountRecoveryServiceGeili) check(ctx context.Context, account *Account, model, source string, confirmed bool, admission ...func(context.Context, *Account) bool) error {
	ctx, wholeCancel := context.WithTimeout(ctx, 30*time.Second)
	stop := context.AfterFunc(s.ctx, wholeCancel)
	defer func() { stop(); wholeCancel() }()
	lease, err := s.runtime.AcquireProbeLeaseGeili(ctx, account.ID, model, 35*time.Second)
	if err != nil {
		return err
	}
	if lease == nil {
		return ErrAccountProbeUnhealthyGeili
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = s.runtime.ReleaseProbeLeaseGeili(release, lease)
	}()
	fresh, err := s.accounts.GetByID(ctx, account.ID)
	if err != nil {
		return err
	}
	state, err := s.runtime.GetAccountRuntimeGeili(ctx, account.ID)
	if err != nil {
		return err
	}
	// A shadow cannot authorize a paid check of its debt-blocked credential owner.
	// Recharge confirmation and recovery must target that original account.
	if fresh.ParentAccountID != nil && *fresh.ParentAccountID != fresh.ID {
		owner, e := s.runtime.GetAccountRuntimeGeili(ctx, *fresh.ParentAccountID)
		if e != nil {
			return e
		}
		if owner != nil && owner.BalanceBlocked {
			return ErrAccountBalanceBlockedGeili
		}
		if s.balanceLatch != nil {
			token, e := s.balanceLatch.GetAccountBalanceLatchGeili(ctx, *fresh.ParentAccountID)
			if e != nil {
				return NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
			}
			if token != "" {
				return ErrAccountBalanceBlockedGeili
			}
		}
	}
	latchToken := ""
	if s.balanceLatch != nil {
		latchToken, err = s.balanceLatch.GetAccountBalanceLatchGeili(ctx, account.ID)
		if err != nil {
			return NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
		}
		if latchToken != "" && (state == nil || !state.BalanceBlocked) {
			if err := s.runtime.MarkAccountBalanceBlockedGeili(ctx, account.ID, "supplier_insufficient_balance"); err != nil {
				return err
			}
			fresh, err = s.accounts.GetByID(ctx, account.ID)
			if err != nil {
				return err
			}
			state, err = s.runtime.GetAccountRuntimeGeili(ctx, account.ID)
			if err != nil {
				return err
			}
		}
	}
	fresh.RuntimeState = state
	if len(admission) > 0 && !admission[0](ctx, fresh) {
		return ErrAccountRuntimeConflictGeili
	}
	verified := false
	if state != nil && state.BalanceBlocked {
		known, sufficient := false, false
		if s.verifyBalance != nil {
			known, sufficient, err = s.verifyBalance(ctx, fresh)
			if err != nil {
				return err
			}
		}
		if known && !sufficient {
			return ErrAccountBalanceBlockedGeili
		}
		if !known && !confirmed {
			return ErrAccountRechargeConfirmationRequiredGeili
		}
		verified = known && sufficient || confirmed
		// Trusted balance readers may persist their snapshot. Reload that write,
		// but never accept a changed credential, proxy or group as the same grant.
		before := *fresh
		before.UpdatedAt = time.Time{}
		reloaded, e := s.accounts.GetByID(ctx, account.ID)
		if e != nil {
			return e
		}
		after := *reloaded
		after.UpdatedAt = time.Time{}
		if MakeAccountProbeSnapshotGeili(&before).Fingerprint != MakeAccountProbeSnapshotGeili(&after).Fingerprint {
			return ErrAccountRuntimeConflictGeili
		}
		newState, e := s.runtime.GetAccountRuntimeGeili(ctx, account.ID)
		if e != nil {
			return e
		}
		if newState == nil || newState.Version != state.Version || !newState.BalanceBlocked {
			return ErrAccountRuntimeConflictGeili
		}
		reloaded.RuntimeState = newState
		fresh = reloaded
		if len(admission) > 0 && !admission[0](ctx, fresh) {
			return ErrAccountRuntimeConflictGeili
		}
	}
	if !recoveryAccountEligibleGeili(fresh, model, time.Now(), false) {
		return ErrAccountProbeUnhealthyGeili
	}
	genericTemporary := AccountGenericTemporaryStopGeili(fresh, time.Now())
	snapshot := MakeAccountProbeSnapshotGeili(fresh)
	started := time.Now()
	observedCtx, observation := WithAccountRecoveryProbeObservationGeili(ctx)
	probeCtx, cancel := context.WithTimeout(context.WithValue(observedCtx, accountRecoveryProbeContextGeili{}, true), 25*time.Second)
	probeModel := model
	if forwarded, ok := openAIForwardModelFromContext(ctx); ok && forwarded.model != "" {
		probeModel = forwarded.model
	}
	complete, probeErr := s.probe(probeCtx, fresh, probeModel)
	if probeCtx.Err() != nil {
		probeErr = probeCtx.Err()
		complete = false
	}
	cancel()
	result := AccountProbeResultGeili{Snapshot: snapshot, Model: model, Source: source, Success: probeErr == nil && complete, Complete: complete, BalanceVerified: verified, CheckedAt: time.Now(), DurationMs: time.Since(started).Milliseconds()}
	result.HTTPStatus, result.TimeToFirstTokenMs = observation.HTTPStatus, observation.TimeToFirstTokenMs
	if probeErr != nil || !complete {
		result.Reason = "probe_failed"
		if errors.Is(probeErr, ErrAccountBalanceBlockedGeili) {
			result.Reason = "supplier_insufficient_balance"
			_ = s.runtime.MarkAccountBalanceBlockedGeili(ctx, account.ID, result.Reason)
		}
		if err := s.runtime.RecordAccountProbeGeili(ctx, result); err != nil {
			return err
		}
		return ErrAccountProbeUnhealthyGeili
	}
	if err := s.runtime.RecordAccountProbeGeili(ctx, result); err != nil {
		return err
	}
	_, err = s.runtime.ActivateAccountAfterProbeGeili(ctx, AccountProbeActivationGeili{Snapshot: snapshot, Model: model, TrustedBalanceVerified: verified})
	if err != nil {
		return err
	}
	if s.balanceLatch != nil {
		cleared, e := s.balanceLatch.ClearAccountBalanceLatchGeili(ctx, account.ID, latchToken)
		if e != nil {
			return e
		}
		if !cleared {
			return ErrAccountRuntimeConflictGeili
		}
	}
	if genericTemporary && s.clearSchedulingBlock != nil {
		if err := s.clearSchedulingBlock(ctx, account.ID); err != nil {
			return err
		}
	} else if s.notifySchedulingRecovery != nil {
		s.notifySchedulingRecovery(account.ID)
	}
	return nil
}

func recoveryAccountEligibleGeili(a *Account, model string, now time.Time, respectWait bool) bool {
	if a == nil || strings.TrimSpace(model) == "" || (a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt)) || a.IsAPIKeyOrBedrock() && a.IsQuotaExceeded() {
		return false
	}
	if a.RateLimitResetAt != nil && now.Before(*a.RateLimitResetAt) {
		return false
	}
	// Future explicit provider windows remain authoritative; generic temporary
	// failures use our bounded testing schedule instead of permanently dying.
	if respectWait && a.RuntimeState != nil {
		if a.RuntimeState.BalanceBlocked {
			return false
		}
		if t := a.RuntimeState.NextCheckAt; t != nil && now.Before(*t) {
			return false
		}
		if m, ok := a.RuntimeState.Models[model]; ok && m.NextCheckAt != nil && now.Before(*m.NextCheckAt) {
			return false
		}
	}
	if a.Type == AccountTypeAPIKey && a.GetCredential("api_key") == "" {
		return false
	}
	return true
}

func (s *AccountRecoveryServiceGeili) RecheckAccountGeili(ctx context.Context, id int64, model string, confirm bool) (*Account, error) {
	if strings.TrimSpace(model) == "" || len(model) > 100 {
		return nil, ErrAccountProbeUnhealthyGeili
	}
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = s.check(ctx, a, model, "admin", confirm); err != nil {
		return nil, err
	}
	return s.accounts.GetByID(ctx, id)
}

// Only callers that already applied route, model, profit and continuity rules
// may supply this predicate. Candidates never leave the original target group.
func (s *AccountRecoveryServiceGeili) RecoverGroupGeili(ctx context.Context, groupID int64, model string, eligible func(context.Context, *Account) bool) bool {
	if s == nil || groupID <= 0 || model == "" || isAccountRecoveryProbeGeili(ctx) || !RequestRecoveryAllowed(ctx) {
		return false
	}
	key := fmt.Sprintf("%d:%s", groupID, model)
	if !BeginRequestRecovery(ctx) {
		return false
	}
	if state := requestRecoveryFromContext(ctx); state != nil {
		state.mu.Lock()
		if state.accountRecoverySelections == nil {
			state.accountRecoverySelections = map[string]bool{}
		}
		if state.accountRecoverySelections[key] || (!state.accountRecoveryDeadline.IsZero() && !time.Now().Before(state.accountRecoveryDeadline)) {
			state.mu.Unlock()
			return false
		}
		state.accountRecoverySelections[key] = true
		if state.accountRecoveryDeadline.IsZero() {
			state.accountRecoveryDeadline = time.Now().Add(30 * time.Second)
		}
		state.mu.Unlock()
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	job := s.jobs[key]
	if job == nil {
		if len(s.jobs) >= 128 {
			s.mu.Unlock()
			return false
		}
		job = &accountRecoveryJobGeili{ready: make(chan struct{}), lastRequest: time.Now(), restored: make(map[int64]struct{})}
		s.jobs[key] = job
		s.wg.Add(1)
		go s.runGroup(context.WithoutCancel(ctx), key, groupID, model, eligible, job)
	} else {
		job.lastRequest = time.Now()
	}
	s.mu.Unlock()
	wait := 30 * time.Second
	if state := requestRecoveryFromContext(ctx); state != nil {
		state.mu.Lock()
		if left := time.Until(state.accountRecoveryDeadline); left < wait {
			wait = left
		}
		state.mu.Unlock()
	}
	if r := RequestRecoveryRemaining(ctx); r < wait {
		wait = r
	}
	if wait <= 0 {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-job.ready:
		if state := requestRecoveryFromContext(ctx); state != nil {
			s.mu.Lock()
			ids := make([]int64, 0, len(job.restored))
			for id := range job.restored {
				ids = append(ids, id)
			}
			s.mu.Unlock()
			state.mu.Lock()
			if state.accountRecoveryRestored == nil {
				state.accountRecoveryRestored = make(map[int64]struct{})
			}
			for _, id := range ids {
				state.accountRecoveryRestored[id] = struct{}{}
			}
			state.mu.Unlock()
		}
		return true
	case <-ctx.Done():
		return false
	case <-s.ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (s *AccountRecoveryServiceGeili) runGroup(parent context.Context, key string, groupID int64, model string, eligible func(context.Context, *Account) bool, job *accountRecoveryJobGeili) {
	defer s.wg.Done()
	defer func() { s.mu.Lock(); delete(s.jobs, key); s.mu.Unlock() }()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	for {
		s.mu.Lock()
		expired := time.Since(job.lastRequest) > 15*time.Minute
		s.mu.Unlock()
		if expired || ctx.Err() != nil {
			return
		}
		accounts, err := s.accounts.ListByGroup(ctx, groupID)
		if err == nil {
			sort.SliceStable(accounts, func(i, j int) bool {
				if accounts[i].Priority != accounts[j].Priority {
					return accounts[i].Priority < accounts[j].Priority
				}
				ti, tj := time.Time{}, time.Time{}
				if r := accounts[i].RuntimeState; r != nil && r.LastProbe != nil {
					ti = r.LastProbe.CheckedAt
				}
				if r := accounts[j].RuntimeState; r != nil && r.LastProbe != nil {
					tj = r.LastProbe.CheckedAt
				}
				return ti.Before(tj)
			})
			var batch sync.WaitGroup
			var foundMu sync.Mutex
			found := false
			n := 0
			for i := range accounts {
				a := accounts[i]
				state, e := s.runtime.GetAccountRuntimeGeili(ctx, a.ID)
				if e != nil {
					continue
				}
				a.RuntimeState = state
				if s.balanceLatch != nil {
					token, latchErr := s.balanceLatch.GetAccountBalanceLatchGeili(ctx, a.ID)
					if latchErr != nil || token != "" {
						continue
					}
				}
				blocked := state != nil && (state.StopReason == "temporary_failure" || state.Models[model].Blocked)
				if a.IsActive() && a.Schedulable && !blocked && !(a.TempUnschedulableUntil != nil && time.Now().Before(*a.TempUnschedulableUntil)) && !(a.OverloadUntil != nil && time.Now().Before(*a.OverloadUntil)) {
					continue
				}
				if !recoveryAccountEligibleGeili(&a, model, time.Now(), true) || eligible != nil && !eligible(ctx, &a) {
					continue
				}
				n++
				batch.Add(1)
				go func(a Account) {
					defer batch.Done()
					if s.check(ctx, &a, model, "automatic", false, func(ctx context.Context, fresh *Account) bool {
						member := false
						for _, id := range fresh.GroupIDs {
							if id == groupID {
								member = true
								break
							}
						}
						return member && (eligible == nil || eligible(ctx, fresh))
					}) == nil {
						foundMu.Lock()
						found = true
						foundMu.Unlock()
						s.mu.Lock()
						job.restored[a.ID] = struct{}{}
						s.mu.Unlock()
						job.once.Do(func() { close(job.ready) })
					}
				}(a)
				if n >= 3 {
					break
				}
			}
			batch.Wait()
			if found {
				return
			}
		}
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func PresentAccountRuntimeGeili(a *Account) map[string]any {
	if a == nil {
		return nil
	}
	r := a.RuntimeState
	state := "stopped"
	if a.IsActive() && a.Schedulable && !a.IsSchedulable() {
		state = "temporarily_stopped"
	}
	if a.IsSchedulable() {
		state = "enabled"
	}
	view := map[string]any{"state": state, "balance_blocked": false, "stop_reason": ""}
	if r == nil {
		return view
	}
	view["balance_blocked"], view["stop_reason"], view["next_check_at"] = r.BalanceBlocked, r.StopReason, r.NextCheckAt
	if r.BalanceBlocked {
		view["state"] = "balance_stopped"
	} else if r.StopReason == "temporary_failure" {
		view["state"] = "temporarily_stopped"
	}
	probeView := func(p *AccountProbeResultGeili) any {
		if p == nil {
			return nil
		}
		status := "failed"
		if p.Success && p.Complete {
			status = "success"
		}
		return map[string]any{"status": status, "model": p.Model, "checked_at": p.CheckedAt, "message": p.Reason}
	}
	view["last_probe"] = probeView(r.LastProbe)
	models := make(map[string]any, len(r.Models))
	for name, m := range r.Models {
		models[name] = map[string]any{"blocked": m.Blocked, "next_check_at": m.NextCheckAt, "last_probe": probeView(m.LastProbe)}
	}
	view["models"] = models
	return view
}

// A verified recovery is a new attempt. Preserve all other request exclusions.
func recoveredAccountExclusionsGeili(ctx context.Context, excluded map[int64]struct{}) map[int64]struct{} {
	state := requestRecoveryFromContext(ctx)
	if state == nil || len(excluded) == 0 {
		return excluded
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	result := make(map[int64]struct{}, len(excluded))
	for id := range excluded {
		if _, restored := state.accountRecoveryRestored[id]; !restored {
			result[id] = struct{}{}
		}
	}
	return result
}

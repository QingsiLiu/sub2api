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
	ready          chan struct{}
	once           sync.Once
	lastRequest    time.Time
	restored       map[int64]struct{}
	requestContext context.Context
	eligible       func(context.Context, *Account) bool
	wake           chan struct{}
	candidatePage  int
}
type AccountRecoveryServiceGeili struct {
	accounts                 AccountRepository
	runtime                  AccountRuntimeRepositoryGeili
	probe                    AccountRecoveryProberGeili
	clearSchedulingBlock     func(context.Context, int64) error
	notifySchedulingRecovery func(int64)
	ctx                      context.Context
	cancel                   context.CancelFunc
	mu                       sync.Mutex
	jobs                     map[string]*accountRecoveryJobGeili
	wg                       sync.WaitGroup
}

// The legacy balance callback is retained only for call-site compatibility.
// Debt is an independent administrative label and never controls recovery.
func NewAccountRecoveryServiceGeili(accounts AccountRepository, probe AccountRecoveryProberGeili, _ AccountBalanceVerifierGeili) *AccountRecoveryServiceGeili {
	r, ok := accounts.(AccountRuntimeRepositoryGeili)
	if !ok || probe == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &AccountRecoveryServiceGeili{accounts: accounts, runtime: r, probe: probe, ctx: ctx, cancel: cancel, jobs: make(map[string]*accountRecoveryJobGeili)}
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
	// A child still needs usable shared credentials; debt is only a label.
	if fresh.ParentAccountID != nil && *fresh.ParentAccountID != fresh.ID {
		owner, e := s.accounts.GetByID(ctx, *fresh.ParentAccountID)
		if e != nil {
			return e
		}
		if owner == nil {
			return ErrAccountProbeUnhealthyGeili
		}
		if !owner.IsCredentialUsableForShadow() {
			return ErrAccountProbeUnhealthyGeili
		}
	}
	fresh.RuntimeState = state
	if len(admission) > 0 && !admission[0](ctx, fresh) {
		return ErrAccountRuntimeConflictGeili
	}
	if source != "admin" && AccountManuallyStoppedGeili(fresh) {
		return ErrAccountProbeUnhealthyGeili
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
	result := AccountProbeResultGeili{Snapshot: snapshot, Model: model, Source: source, Success: probeErr == nil && complete, Complete: complete, CheckedAt: time.Now(), DurationMs: time.Since(started).Milliseconds()}
	result.HTTPStatus, result.TimeToFirstTokenMs = observation.HTTPStatus, observation.TimeToFirstTokenMs
	if probeErr != nil || !complete {
		result.Reason = "probe_failed"
		if err := s.runtime.RecordAccountProbeGeili(ctx, result); err != nil {
			return err
		}
		return ErrAccountProbeUnhealthyGeili
	}
	if err := s.runtime.RecordAccountProbeGeili(ctx, result); err != nil {
		return err
	}
	_, err = s.runtime.ActivateAccountAfterProbeGeili(ctx, AccountProbeActivationGeili{Snapshot: snapshot, Model: model})
	if err != nil {
		return err
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
		if p := a.RuntimeState.Models[model].LastProbe; p != nil && (!p.Success || !p.Complete) && p.NextProbeAt != nil && now.Before(*p.NextProbeAt) {
			return false
		}
	}
	if respectWait && AccountManuallyStoppedGeili(a) {
		return false
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
	if s == nil || !AccountRecoveryScopeAllowedGeili(ctx) || groupID <= 0 || model == "" || isAccountRecoveryProbeGeili(ctx) || !RequestRecoveryAllowed(ctx) {
		return false
	}
	key := fmt.Sprintf("%d:%s", groupID, model)
	if accountRecoveryForegroundRemainingGeili(ctx, true) <= 0 {
		return false
	}
	if !BeginRequestRecovery(ctx) {
		return false
	}
	if state := requestRecoveryFromContext(ctx); state != nil {
		state.mu.Lock()
		if state.accountRecoverySelections == nil {
			state.accountRecoverySelections = map[string]bool{}
		}
		if state.accountRecoverySelections[key] || (!state.accountRecoveryDeadline.IsZero() && !state.now().Before(state.accountRecoveryDeadline)) {
			state.mu.Unlock()
			return false
		}
		state.accountRecoverySelections[key] = true
		if state.accountRecoveryDeadline.IsZero() {
			state.accountRecoveryDeadline = state.now().Add(30 * time.Second)
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
		job = &accountRecoveryJobGeili{ready: make(chan struct{}), lastRequest: time.Now(), restored: make(map[int64]struct{}), requestContext: context.WithoutCancel(ctx), eligible: eligible, wake: make(chan struct{}, 1)}
		s.jobs[key] = job
		s.wg.Add(1)
		go s.runGroup(key, groupID, model, job)
	} else {
		job.lastRequest = time.Now()
		// New callers may have different profit/capability restrictions. Keep
		// one serialized group job, but wake it with the current admission.
		job.requestContext, job.eligible = context.WithoutCancel(ctx), eligible
		select {
		case job.wake <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
	wait := accountRecoveryForegroundRemainingGeili(ctx, false)
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

func (s *AccountRecoveryServiceGeili) runGroup(key string, groupID int64, model string, job *accountRecoveryJobGeili) {
	defer s.wg.Done()
	defer func() { s.mu.Lock(); delete(s.jobs, key); s.mu.Unlock() }()
	ctx, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	for {
		s.mu.Lock()
		expired := time.Since(job.lastRequest) > 15*time.Minute
		requestContext, eligible := job.requestContext, job.eligible
		page := job.candidatePage
		s.mu.Unlock()
		if expired || ctx.Err() != nil {
			return
		}
		attemptCtx, attemptCancel := context.WithCancel(requestContext)
		stopAttempt := context.AfterFunc(ctx, attemptCancel)
		accounts, nextPage, err := listAccountRecoveryCandidatePageGeili(attemptCtx, s.accounts, groupID, "", page)
		if err == nil {
			s.mu.Lock()
			job.candidatePage = nextPage
			if nextPage == 0 {
				job.candidatePage = 1
			}
			s.mu.Unlock()
		}
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
				state, e := s.runtime.GetAccountRuntimeGeili(attemptCtx, a.ID)
				if e != nil {
					continue
				}
				a.RuntimeState = state
				if a.IsSchedulable() {
					continue
				}
				if !recoveryAccountEligibleGeili(&a, model, time.Now(), true) || eligible != nil && !eligible(attemptCtx, &a) {
					continue
				}
				n++
				batch.Add(1)
				go func(a Account) {
					defer batch.Done()
					if s.check(attemptCtx, &a, model, "automatic", false, func(ctx context.Context, fresh *Account) bool {
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
			// Empty/healthy pages do not consume the three-probe batch. Continue
			// discovery promptly so later manual stops can enter the same wait.
			if n == 0 && nextPage > 0 {
				stopAttempt()
				attemptCancel()
				continue
			}
			if found {
				stopAttempt()
				attemptCancel()
				return
			}
		}
		stopAttempt()
		attemptCancel()
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-job.wake:
			timer.Stop()
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
	if !AccountManuallyStoppedGeili(a) && (a.Status == StatusError || a.IsActive() && a.Schedulable && !a.IsSchedulable()) {
		state = "temporarily_stopped"
	}
	if a.IsSchedulable() {
		state = "enabled"
	}
	view := map[string]any{"state": state, "balance_blocked": false, "stop_reason": ""}
	if state == "temporarily_stopped" {
		view["stop_reason"] = "temporary_failure"
	} else if state == "stopped" && AccountManuallyStoppedGeili(a) {
		view["stop_reason"] = "manual"
	}
	if r == nil {
		return view
	}
	view["balance_blocked"] = r.BalanceBlocked
	probeNextCheck := func(p *AccountProbeResultGeili) *time.Time {
		if !AccountManuallyStoppedGeili(a) && p != nil && (!p.Success || !p.Complete) && p.NextProbeAt != nil && time.Now().Before(*p.NextProbeAt) {
			return p.NextProbeAt
		}
		return nil
	}
	if state != "enabled" {
		view["next_check_at"] = probeNextCheck(r.LastProbe)
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
		if m.LastProbe == nil {
			continue
		}
		// Keep the response field for older clients, without presenting retired
		// consecutive-failure flags as an ongoing scheduling prohibition.
		models[name] = map[string]any{"blocked": false, "next_check_at": probeNextCheck(m.LastProbe), "last_probe": probeView(m.LastProbe)}
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

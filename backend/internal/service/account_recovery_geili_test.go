//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Embedding keeps this contract test limited to methods recovery actually owns;
// unexpected use of another repository operation fails instead of silently passing.
type recoveryRuntimeRepoGeili struct {
	AccountRepository
	mu             sync.Mutex
	accounts       map[int64]Account
	busy           map[string]bool
	records        []AccountProbeResultGeili
	activations    []AccountProbeActivationGeili
	groups         []int64
	releases       int
	releaseBounded bool
	markError      error
	admissionError error
	stale          bool
}

func newRecoveryRuntimeRepoGeili(accounts ...Account) *recoveryRuntimeRepoGeili {
	r := &recoveryRuntimeRepoGeili{accounts: make(map[int64]Account), busy: make(map[string]bool)}
	for _, a := range accounts {
		if a.RuntimeState == nil {
			a.RuntimeState = &AccountRuntimeStateGeili{AccountID: a.ID, Models: map[string]AccountModelHealthGeili{}}
		}
		r.accounts[a.ID] = a
	}
	return r
}
func stoppedRecoveryAccountGeili(id, group int64) Account {
	return Account{ID: id, Name: fmt.Sprintf("account-%d", id), Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusError, Schedulable: false, GroupIDs: []int64{group}, Priority: int(id), Credentials: map[string]any{"api_key": "fixture-key"}}
}
func copyRecoveryAccountGeili(a Account) *Account {
	encoded, _ := json.Marshal(a)
	var out Account
	_ = json.Unmarshal(encoded, &out)
	return &out
}
func (r *recoveryRuntimeRepoGeili) GetByID(ctx context.Context, id int64) (*Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accounts[id]
	if !ok {
		return nil, errors.New("missing account")
	}
	return copyRecoveryAccountGeili(a), nil
}
func (r *recoveryRuntimeRepoGeili) ListByGroup(ctx context.Context, id int64) ([]Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groups = append(r.groups, id)
	var out []Account
	for _, a := range r.accounts {
		if a.Status != StatusActive {
			continue
		}
		for _, g := range a.GroupIDs {
			if g == id {
				out = append(out, *copyRecoveryAccountGeili(a))
				break
			}
		}
	}
	return out, nil
}
func (r *recoveryRuntimeRepoGeili) GetAccountRuntimeGeili(ctx context.Context, id int64) (*AccountRuntimeStateGeili, error) {
	a, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.RuntimeState, nil
}
func (r *recoveryRuntimeRepoGeili) LoadAccountRuntimeBatchGeili(ctx context.Context, ids []int64) (map[int64]*AccountRuntimeStateGeili, error) {
	out := map[int64]*AccountRuntimeStateGeili{}
	for _, id := range ids {
		a, e := r.GetAccountRuntimeGeili(ctx, id)
		if e != nil {
			return nil, e
		}
		out[id] = a
	}
	return out, nil
}
func (r *recoveryRuntimeRepoGeili) MarkAccountBalanceBlockedGeili(ctx context.Context, id int64, reason string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.markError != nil {
		return r.markError
	}
	a := r.accounts[id]
	a.RuntimeState.BalanceBlocked = true
	a.RuntimeState.BalanceReason = reason
	a.RuntimeState.Version++
	r.accounts[id] = a
	return nil
}
func (r *recoveryRuntimeRepoGeili) RecordModelFailureGeili(ctx context.Context, id int64, model, reason string, next time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.accounts[id]
	m := a.RuntimeState.Models[model]
	m.FailureCount++
	m.LastError = reason
	m.NextCheckAt = &next
	a.RuntimeState.Models[model] = m
	r.accounts[id] = a
	return nil
}
func (r *recoveryRuntimeRepoGeili) ResetAccountModelSuccessGeili(ctx context.Context, id int64, model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.accounts[id]
	m := a.RuntimeState.Models[model]
	m.FailureCount = 0
	a.RuntimeState.Models[model] = m
	r.accounts[id] = a
	return nil
}
func (r *recoveryRuntimeRepoGeili) RecordAccountProbeGeili(ctx context.Context, result AccountProbeResultGeili) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, result)
	a := r.accounts[result.Snapshot.AccountID]
	a.RuntimeState.LastProbe = &result
	m := a.RuntimeState.Models[result.Model]
	m.LastProbe = &result
	a.RuntimeState.Models[result.Model] = m
	r.accounts[a.ID] = a
	return nil
}
func (r *recoveryRuntimeRepoGeili) ActivateAccountAfterProbeGeili(ctx context.Context, grant AccountProbeActivationGeili) (*AccountRuntimeStateGeili, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.accounts[grant.Snapshot.AccountID]
	if r.stale || MakeAccountProbeSnapshotGeili(&a) != grant.Snapshot {
		return nil, ErrAccountRuntimeConflictGeili
	}
	last := a.RuntimeState.LastProbe
	if last == nil || last.Model != grant.Model || !last.Success || !last.Complete {
		return nil, ErrAccountProbeUnhealthyGeili
	}
	r.activations = append(r.activations, grant)
	manual := AccountManuallyStoppedGeili(&a)
	a.Status, a.Schedulable = StatusActive, !manual
	a.RuntimeState.StopReason = ""
	if manual {
		a.RuntimeState.StopReason = "manual"
	}
	a.RuntimeState.Version++
	m := a.RuntimeState.Models[grant.Model]
	m.Blocked = false
	m.FailureCount = 0
	a.RuntimeState.Models[grant.Model] = m
	r.accounts[a.ID] = a
	return copyRecoveryAccountGeili(a).RuntimeState, nil
}
func (r *recoveryRuntimeRepoGeili) AcquireProbeLeaseGeili(ctx context.Context, id int64, model string, ttl time.Duration) (*AccountProbeLeaseGeili, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fmt.Sprintf("%d:%s", id, model)
	if r.busy[key] {
		return nil, nil
	}
	r.busy[key] = true
	return &AccountProbeLeaseGeili{AccountID: id, Model: model, Token: key, ExpiresAt: time.Now().Add(ttl)}, nil
}
func (r *recoveryRuntimeRepoGeili) ReleaseProbeLeaseGeili(ctx context.Context, lease *AccountProbeLeaseGeili) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, r.releaseBounded = ctx.Deadline()
	delete(r.busy, lease.Token)
	r.releases++
	return nil
}
func (r *recoveryRuntimeRepoGeili) CheckAccountRuntimeAdmissionGeili(ctx context.Context, id int64, model string) (*AccountRuntimeAdmissionGeili, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.admissionError != nil {
		return nil, r.admissionError
	}
	a := r.accounts[id]
	state := a.RuntimeState
	if state.Models[model].Blocked {
		return &AccountRuntimeAdmissionGeili{Allowed: false, Reason: "model"}, nil
	}
	return &AccountRuntimeAdmissionGeili{Allowed: true}, nil
}

func TestAccountRecoveryGeiliErrorStoppedSameGroupOnly(t *testing.T) {
	r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7), stoppedRecoveryAccountGeili(2, 8))
	var probed atomic.Int64
	s := NewAccountRecoveryServiceGeili(r, func(ctx context.Context, a *Account, model string) (bool, error) {
		require.True(t, isAccountRecoveryProbeGeili(ctx))
		require.Equal(t, "claude-opus-5", model)
		probed.Store(a.ID)
		return true, nil
	}, nil)
	defer s.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.True(t, s.RecoverGroupGeili(ctx, 7, "claude-opus-5", nil))
	a, _ := r.GetByID(context.Background(), 1)
	other, _ := r.GetByID(context.Background(), 2)
	require.Equal(t, int64(1), probed.Load())
	require.Equal(t, StatusActive, a.Status)
	require.True(t, a.Schedulable)
	require.Equal(t, StatusError, other.Status)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Equal(t, []int64{7}, r.groups)
	require.Len(t, r.activations, 1)
	require.Equal(t, "automatic", r.records[0].Source)
}

func TestAccountRecoveryGeiliManualStoppedNotAutomatic(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	a.Status = "inactive"
	a.RuntimeState = &AccountRuntimeStateGeili{AccountID: 1, BalanceBlocked: true, Models: map[string]AccountModelHealthGeili{}}
	r := newRecoveryRuntimeRepoGeili(a)
	var calls atomic.Int64
	s := NewAccountRecoveryServiceGeili(r, func(context.Context, *Account, string) (bool, error) { calls.Add(1); return true, nil }, nil)
	defer s.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.False(t, s.RecoverGroupGeili(ctx, 7, "m", nil))
	require.Zero(t, calls.Load())
}

func TestAccountRecoveryGeiliCandidatesBoundedAndSharedJob(t *testing.T) {
	accounts := make([]Account, 5)
	for i := range accounts {
		accounts[i] = stoppedRecoveryAccountGeili(int64(i+1), 7)
	}
	r := newRecoveryRuntimeRepoGeili(accounts...)
	gate := make(chan struct{})
	var calls atomic.Int64
	var started sync.WaitGroup
	started.Add(3)
	s := NewAccountRecoveryServiceGeili(r, func(ctx context.Context, a *Account, model string) (bool, error) {
		calls.Add(1)
		started.Done()
		select {
		case <-gate:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}, nil)
	defer s.Stop()
	results := make(chan bool, 8)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 8; i++ {
		go func() { results <- s.RecoverGroupGeili(ctx, 7, "m", nil) }()
	}
	started.Wait()
	require.Equal(t, int64(3), calls.Load())
	time.Sleep(20 * time.Millisecond)
	s.mu.Lock()
	require.Len(t, s.jobs, 1)
	s.mu.Unlock()
	close(gate)
	for i := 0; i < 8; i++ {
		require.True(t, <-results)
	}
	require.Equal(t, int64(3), calls.Load())
}

func TestAccountRecoveryGeiliSharedRepositoryBusyLeaseNotActivated(t *testing.T) {
	r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	r.busy["1:m"] = true
	var calls atomic.Int64
	s := NewAccountRecoveryServiceGeili(r, func(context.Context, *Account, string) (bool, error) { calls.Add(1); return true, nil }, nil)
	defer s.Stop()
	_, err := s.RecheckAccountGeili(context.Background(), 1, "m", false)
	require.ErrorIs(t, err, ErrAccountProbeUnhealthyGeili)
	require.Zero(t, calls.Load())
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Empty(t, r.activations)
	require.Empty(t, r.records)
}

func TestAccountRecoveryGeiliFailedProbeNeverEnables(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
			s := NewAccountRecoveryServiceGeili(r, func(context.Context, *Account, string) (bool, error) {
				if complete {
					return true, errors.New("upstream unavailable")
				}
				return false, nil
			}, nil)
			defer s.Stop()
			_, err := s.RecheckAccountGeili(context.Background(), 1, "m", false)
			require.ErrorIs(t, err, ErrAccountProbeUnhealthyGeili)
			a, _ := r.GetByID(context.Background(), 1)
			require.Equal(t, StatusError, a.Status)
			require.False(t, a.Schedulable)
			r.mu.Lock()
			defer r.mu.Unlock()
			require.Len(t, r.records, 1)
			require.False(t, r.records[0].Success)
			require.Empty(t, r.activations)
		})
	}
}

func TestAccountRecoveryGeiliDebtLabelPreservedByCurrentGrant(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	a.RuntimeState = &AccountRuntimeStateGeili{AccountID: 1, Version: 4, BalanceBlocked: true, StopReason: "balance", Models: map[string]AccountModelHealthGeili{}}
	r := newRecoveryRuntimeRepoGeili(a)
	var calls atomic.Int64
	s := NewAccountRecoveryServiceGeili(r, func(context.Context, *Account, string) (bool, error) { calls.Add(1); return true, nil }, nil)
	defer s.Stop()
	updated, err := s.RecheckAccountGeili(context.Background(), 1, "m", false)
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load())
	require.Equal(t, StatusActive, updated.Status)
	require.True(t, updated.Schedulable)
	require.True(t, updated.RuntimeState.BalanceBlocked)
	require.Equal(t, int64(5), updated.RuntimeState.Version)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.activations, 1)
	require.False(t, r.activations[0].TrustedBalanceVerified)
	require.Equal(t, int64(4), r.activations[0].Snapshot.RuntimeVersion)
	require.False(t, r.records[0].BalanceVerified)
}

func TestAccountRecoveryGeiliLegacyVerifierIsNeverCalled(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual_blocked=%t", blocked), func(t *testing.T) {
			a := stoppedRecoveryAccountGeili(1, 7)
			a.RuntimeState = &AccountRuntimeStateGeili{AccountID: 1, BalanceBlocked: blocked, Models: map[string]AccountModelHealthGeili{}}
			r := newRecoveryRuntimeRepoGeili(a)
			var calls atomic.Int64
			s := NewAccountRecoveryServiceGeili(r, func(context.Context, *Account, string) (bool, error) { calls.Add(1); return true, nil }, func(context.Context, *Account) (bool, bool, error) {
				t.Fatal("manual recovery must not request supplier balance")
				return true, false, errors.New("legacy supplier unavailable")
			})
			defer s.Stop()
			updated, err := s.RecheckAccountGeili(context.Background(), 1, "m", true)
			require.NoError(t, err)
			require.Equal(t, int64(1), calls.Load())
			require.Equal(t, blocked, updated.RuntimeState.BalanceBlocked)
		})
	}
}

func TestAccountRecoveryGeiliStaleCASCannotActivate(t *testing.T) {
	r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	s := NewAccountRecoveryServiceGeili(r, func(context.Context, *Account, string) (bool, error) {
		r.mu.Lock()
		a := r.accounts[1]
		a.Credentials = map[string]any{"api_key": "new-fixture"}
		r.accounts[1] = a
		r.mu.Unlock()
		return true, nil
	}, nil)
	defer s.Stop()
	_, err := s.RecheckAccountGeili(context.Background(), 1, "m", false)
	require.ErrorIs(t, err, ErrAccountRuntimeConflictGeili)
	a, _ := r.GetByID(context.Background(), 1)
	require.Equal(t, StatusError, a.Status)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Empty(t, r.activations)
}

func TestAccountRecoveryGeiliCanceledWaiterLeavesBoundedBackgroundJob(t *testing.T) {
	r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	started := make(chan context.Context, 1)
	gate := make(chan struct{})
	s := NewAccountRecoveryServiceGeili(r, func(ctx context.Context, a *Account, m string) (bool, error) {
		started <- ctx
		select {
		case <-gate:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}, nil)
	defer s.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan bool, 1)
	go func() { result <- s.RecoverGroupGeili(ctx, 7, "m", nil) }()
	probeCtx := <-started
	deadline, ok := probeCtx.Deadline()
	require.True(t, ok)
	require.LessOrEqual(t, time.Until(deadline), 25*time.Second)
	require.False(t, <-result)
	require.NoError(t, probeCtx.Err(), "caller cancellation does not abort the bounded shared job")
	close(gate)
	require.Eventually(t, func() bool { a, _ := r.GetByID(context.Background(), 1); return a.Schedulable }, time.Second, time.Millisecond)
}

func TestAccountRecoveryGeiliStopCancelsProbeAndRemovesJob(t *testing.T) {
	r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	started := make(chan struct{})
	finished := make(chan struct{})
	s := NewAccountRecoveryServiceGeili(r, func(ctx context.Context, a *Account, m string) (bool, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return false, ctx.Err()
	}, nil)
	result := make(chan bool, 1)
	go func() { result <- s.RecoverGroupGeili(context.Background(), 7, "m", nil) }()
	<-started
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop leaked the recovery job")
	}
	<-finished
	require.False(t, <-result)
	s.mu.Lock()
	require.Empty(t, s.jobs)
	s.mu.Unlock()
	r.mu.Lock()
	require.Equal(t, 1, r.releases)
	require.True(t, r.releaseBounded, "lease cleanup must remain bounded after Stop")
	require.Empty(t, r.activations)
	r.mu.Unlock()
}

func TestAccountRecoveryGeiliProbeBackoffIsNotPermanentModelDisable(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	future := time.Now().Add(time.Hour)
	a.RuntimeState = &AccountRuntimeStateGeili{AccountID: 1, Models: map[string]AccountModelHealthGeili{"a": {
		Blocked: true, NextCheckAt: &future,
		LastProbe: &AccountProbeResultGeili{Model: "a", NextProbeAt: &future},
	}}}
	require.False(t, recoveryAccountEligibleGeili(&a, "a", time.Now(), true))
	require.True(t, recoveryAccountEligibleGeili(&a, "b", time.Now(), true))
	a.Status, a.Schedulable = StatusActive, true
	require.True(t, a.IsSchedulable(), "old model flags do not prohibit normal scheduling")
	a.RuntimeState.Models["a"] = AccountModelHealthGeili{Blocked: true, NextCheckAt: &future}
	require.True(t, recoveryAccountEligibleGeili(&a, "a", time.Now(), true), "only failed probes impose recovery backoff")
}

func TestAccountRecoveryGeiliTwoServicesShareRepositoryLease(t *testing.T) {
	r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	started, gate := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	prober := func(ctx context.Context, a *Account, model string) (bool, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-gate:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	first, second := NewAccountRecoveryServiceGeili(r, prober, nil), NewAccountRecoveryServiceGeili(r, prober, nil)
	defer first.Stop()
	defer second.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := first.RecheckAccountGeili(ctx, 1, "m", false); done <- err }()
	<-started
	_, err := second.RecheckAccountGeili(ctx, 1, "m", false)
	require.ErrorIs(t, err, ErrAccountProbeUnhealthyGeili)
	require.Equal(t, int64(1), calls.Load())
	close(gate)
	require.NoError(t, <-done)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.activations, 1)
	require.Empty(t, r.busy)
}

func TestAccountRecoveryGeiliAdminCancellationCannotEnableAccount(t *testing.T) {
	r := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	s := NewAccountRecoveryServiceGeili(r, func(ctx context.Context, a *Account, model string) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}, nil)
	defer s.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.RecheckAccountGeili(ctx, 1, "m", false)
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second)
	stored, err := r.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, stored.Schedulable)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Empty(t, r.activations)
	require.Empty(t, r.busy)
}

// Match the production paginated list: unlike ListByGroup it accepts an empty
// status filter, including inactive/error accounts while respecting membership.
func (r *recoveryRuntimeRepoGeili) ListWithFilters(ctx context.Context, p pagination.PaginationParams, platform, accountType, status, search string, gid int64, privacy string) ([]Account, *pagination.PaginationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.groups = append(r.groups, gid)
	var all []Account
	for _, a := range r.accounts {
		if !accountRecoveryGroupMemberGeili(&a, gid) || platform != "" && a.Platform != platform || status != "" && a.Status != status {
			continue
		}
		all = append(all, *copyRecoveryAccountGeili(a))
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Priority != all[j].Priority {
			return all[i].Priority < all[j].Priority
		}
		return all[i].ID < all[j].ID
	})
	result := &pagination.PaginationResult{Total: int64(len(all)), Page: p.Page, PageSize: p.Limit(), Pages: (len(all) + p.Limit() - 1) / p.Limit()}
	start := p.Offset()
	if start >= len(all) {
		return nil, result, nil
	}
	end := start + p.Limit()
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], result, nil
}

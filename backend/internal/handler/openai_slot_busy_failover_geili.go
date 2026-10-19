package handler

// A saturated account is not an upstream failure: its slot wait timing out
// (often a sticky session pinned to it, or a pool-mode same-account retry)
// says nothing about the rest of the group. Opted-in routing loops exclude it
// for this request only and select again; when no other account can be
// chosen, the original concurrency error is written exactly as before.
//
// geili hook: excluding a busy account also removes it from this request's
// candidate set. When it was the only account for the model, the next
// selection reports "no available accounts" even though the account is healthy
// and its slots turn over quickly. One bounded requeue re-admits the saturated
// accounts so the request keeps waiting for a slot (with SSE keepalive) instead
// of failing. It never reaches the account-recovery probe, which only restores
// unschedulable accounts and cannot help a busy one.
import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	openAISlotBusyFailoverKeyGeili = "geili_openai_slot_busy_failover"
	// Extra accounts tried after a slot wait timeout; each may wait its own WaitPlan.
	openAISlotBusyMaxSwitchesGeili = 3
	// Saturated exclusions are re-admitted this many times before the error is final.
	openAISlotBusyMaxRequeuesGeili = 1
	// The re-admitted round only hunts slots that free right after the first
	// timeout, so total latency stays within the pre-existing single-round worst case.
	openAISlotBusyRequeueWaitGeili = 10 * time.Second
)

// openAISlotAcquireBusyGeili: the selected account stayed saturated. Nothing
// was written; the caller excludes the account and selects again.
const openAISlotAcquireBusyGeili openAISlotAcquireResult = 100

type openAISlotBusyStateGeili struct {
	switches int
	requeues int
	busy     map[int64]struct{}
	write    func()
}

func enableOpenAISlotBusyFailoverGeili(c *gin.Context) {
	if c != nil {
		c.Set(openAISlotBusyFailoverKeyGeili, &openAISlotBusyStateGeili{})
	}
}

func openAISlotBusyStateFromGeili(c *gin.Context) *openAISlotBusyStateGeili {
	if c == nil {
		return nil
	}
	v, ok := c.Get(openAISlotBusyFailoverKeyGeili)
	if !ok {
		return nil
	}
	state, _ := v.(*openAISlotBusyStateGeili)
	return state
}

// markOpenAISlotBusyAccountGeili records that an account left this request's
// candidate set only because its slots were busy, never because it failed.
func markOpenAISlotBusyAccountGeili(c *gin.Context, accountID int64) {
	state := openAISlotBusyStateFromGeili(c)
	if state == nil {
		return
	}
	if state.busy == nil {
		state.busy = make(map[int64]struct{}, 4)
	}
	state.busy[accountID] = struct{}{}
}

// deferOpenAISlotBusyGeili keeps the concurrency error unwritten while another
// account may still take the request. It returns false when the caller did not
// opt in or the switch budget is spent, so the error is written immediately.
func deferOpenAISlotBusyGeili(c *gin.Context, write func()) bool {
	state := openAISlotBusyStateFromGeili(c)
	if state == nil || write == nil || state.switches >= openAISlotBusyMaxSwitchesGeili || requestRecoveryStopped(c) {
		return false
	}
	state.switches++
	state.write = write
	return true
}

// requeueBusyAccountsGeili re-admits saturated accounts once selection runs out
// of candidates. It returns false when any exclusion was a real failure, when
// nothing was saturated, or when the requeue budget is spent, so callers fall
// through to the original error.
func requeueBusyAccountsGeili(c *gin.Context, excluded map[int64]struct{}) bool {
	state := openAISlotBusyStateFromGeili(c)
	if state == nil || len(state.busy) == 0 || len(excluded) == 0 ||
		state.requeues >= openAISlotBusyMaxRequeuesGeili || requestRecoveryStopped(c) {
		return false
	}
	for id := range excluded {
		if _, busy := state.busy[id]; !busy {
			return false
		}
	}
	for id := range excluded {
		delete(excluded, id)
	}
	state.requeues++
	state.write = nil
	return true
}

// slotWaitTimeoutGeili bounds the current round. The first round keeps the
// scheduler's own wait plan; a re-admitted round only waits for the short window
// in which an almost-free slot frees, instead of repeating the whole budget.
func slotWaitTimeoutGeili(c *gin.Context, planned time.Duration) time.Duration {
	state := openAISlotBusyStateFromGeili(c)
	if state != nil && state.requeues > 0 && planned > openAISlotBusyRequeueWaitGeili {
		return openAISlotBusyRequeueWaitGeili
	}
	return planned
}

// selectContextGeili marks the selection context while every excluded account was
// excluded only because its slots were busy. The account-recovery probe restores
// accounts that are not schedulable, so it cannot free a busy one and would add
// its whole foreground budget to a request that is about to fail anyway.
func selectContextGeili(c *gin.Context, excluded map[int64]struct{}) context.Context {
	ctx := c.Request.Context()
	state := openAISlotBusyStateFromGeili(c)
	if state == nil || len(state.busy) == 0 || len(excluded) == 0 {
		return service.ClearBusyRequeuePendingGeili(ctx)
	}
	for id := range excluded {
		if _, busy := state.busy[id]; !busy {
			return service.ClearBusyRequeuePendingGeili(ctx)
		}
	}
	return service.WithBusyRequeuePendingGeili(ctx)
}

// writeOpenAISlotBusyExhaustedGeili writes the deferred concurrency error when
// selection ran out of accounts and no upstream failure is a better answer.
func writeOpenAISlotBusyExhaustedGeili(c *gin.Context) bool {
	state := openAISlotBusyStateFromGeili(c)
	if state == nil || state.write == nil {
		return false
	}
	write := state.write
	state.write = nil
	if c != nil && c.Writer != nil && !c.Writer.Written() {
		// A saturated pool is transient: tell clients when to come back instead
		// of letting them treat the failure as an outage.
		c.Header("Retry-After", "3")
	}
	write()
	return true
}

// clearOpenAISlotBusyGeili drops a deferred error once another account
// admitted the request; later outcomes own the response.
func clearOpenAISlotBusyGeili(c *gin.Context) {
	if state := openAISlotBusyStateFromGeili(c); state != nil {
		state.write = nil
	}
}

package handler

// A saturated account is not an upstream failure: its slot wait timing out
// (often a sticky session pinned to it, or a pool-mode same-account retry)
// says nothing about the rest of the group. Opted-in routing loops exclude it
// for this request only and select again; when no other account can be
// chosen, the original concurrency error is written exactly as before.
import (
	"github.com/gin-gonic/gin"
)

const (
	openAISlotBusyFailoverKeyGeili = "geili_openai_slot_busy_failover"
	// Extra accounts tried after a slot wait timeout; each may wait its own WaitPlan.
	openAISlotBusyMaxSwitchesGeili = 3
)

// openAISlotAcquireBusyGeili: the selected account stayed saturated. Nothing
// was written; the caller excludes the account and selects again.
const openAISlotAcquireBusyGeili openAISlotAcquireResult = 100

type openAISlotBusyStateGeili struct {
	switches int
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

// writeOpenAISlotBusyExhaustedGeili writes the deferred concurrency error when
// selection ran out of accounts and no upstream failure is a better answer.
func writeOpenAISlotBusyExhaustedGeili(c *gin.Context) bool {
	state := openAISlotBusyStateFromGeili(c)
	if state == nil || state.write == nil {
		return false
	}
	write := state.write
	state.write = nil
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

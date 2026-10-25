package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/lib/pq"
)

const encryptedRouteFailureCooldownGeili = 500 * time.Millisecond

var errEncryptedRouteBusyGeili = errors.New("encrypted provenance temporarily unavailable")

// A separate SQL pool bounds live sessions; this guard also bounds callers and
// connection churn when lib/pq retires sockets after short query cancellations.
// Rejected calls never prolong the cooldown. Only one operation probes recovery.
type encryptedRouteBulkheadGeili struct {
	mu           sync.Mutex
	inFlight     int
	generation   uint64
	blockedUntil time.Time
	probeActive  bool
}

type encryptedRoutePermitGeili struct {
	guard      *encryptedRouteBulkheadGeili
	generation uint64
	probe      bool
}

func (g *encryptedRouteBulkheadGeili) acquire(now time.Time) (encryptedRoutePermitGeili, error) {
	return g.acquireWithRetry(now, false)
}

func (g *encryptedRouteBulkheadGeili) acquireWithRetry(now time.Time, retry bool) (encryptedRoutePermitGeili, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight >= 2 {
		return encryptedRoutePermitGeili{}, errEncryptedRouteBusyGeili
	}
	// The one existing tracked-session retry retains its original cumulative
	// deadline and concurrency cap. It cannot close the global circuit.
	probe := !retry && !g.blockedUntil.IsZero()
	if probe && (now.Before(g.blockedUntil) || g.probeActive) {
		return encryptedRoutePermitGeili{}, errEncryptedRouteBusyGeili
	}
	if probe {
		g.probeActive = true
	}
	g.inFlight++
	return encryptedRoutePermitGeili{guard: g, generation: g.generation, probe: probe}, nil
}

func (p encryptedRoutePermitGeili) finish(err error) {
	if p.guard == nil {
		return
	}
	g := p.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inFlight--
	if p.probe {
		g.probeActive = false
	}
	if encryptedRouteAvailabilityFailureGeili(err) {
		g.generation++
		g.blockedUntil = time.Now().Add(encryptedRouteFailureCooldownGeili)
	} else if p.probe && p.generation == g.generation {
		// A pre-failure operation completing successfully cannot clear a newer
		// cooldown; only the current recovery probe can reopen admission.
		g.blockedUntil = time.Time{}
	}
}

func encryptedRouteAvailabilityFailureGeili(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	var postgres *pq.Error
	if errors.As(err, &postgres) {
		code := string(postgres.Code)
		return postgres.Code.Class() == "08" || code == "53300" || code == "57P01" ||
			code == "57P02" || code == "57P03" || code == "57014" || code == "55P03"
	}
	return false
}

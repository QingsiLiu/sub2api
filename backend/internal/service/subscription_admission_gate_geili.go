package service

import (
	"context"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Admissions previously leased a database connection before waiting for their
// subscription's row lock. A burst for one subscription could therefore occupy
// the entire shared pool before the handler's user concurrency check ran.
// This process-local gate moves that wait before BeginTx. PostgreSQL row locks
// remain authoritative across processes, and settlement/cancellation are not
// put behind the admission queue.
type subscriptionAdmissionGateGeili struct {
	mu    sync.Mutex
	limit int
	slots chan struct{}
	keys  map[int64]*subscriptionAdmissionKeyGeili
}

type subscriptionAdmissionKeyGeili struct {
	token chan struct{}
	refs  int
}

func (s *SubscriptionService) initAdmissionGateGeili(cfg *config.Config) {
	limit := 8
	if cfg != nil && cfg.Database.MaxOpenConns > 0 {
		limit = cfg.Database.MaxOpenConns / 3
		if limit < 1 {
			limit = 1
		}
		if limit > 8 {
			limit = 8
		}
	}
	s.admissionGateGeili.limit = limit
}

func (g *subscriptionAdmissionGateGeili) acquire(ctx context.Context, subscriptionID int64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.slots == nil {
		limit := g.limit
		if limit < 1 {
			limit = 8
		}
		g.slots = make(chan struct{}, limit)
		g.keys = make(map[int64]*subscriptionAdmissionKeyGeili)
	}
	key := g.keys[subscriptionID]
	if key == nil {
		key = &subscriptionAdmissionKeyGeili{token: make(chan struct{}, 1)}
		g.keys[subscriptionID] = key
	}
	key.refs++
	g.mu.Unlock()

	forget := func() {
		g.mu.Lock()
		key.refs--
		if key.refs == 0 {
			delete(g.keys, subscriptionID)
		}
		g.mu.Unlock()
	}
	// Acquire the per-subscription token first: queued requests for a hot
	// subscription must not consume the global budget and starve other users.
	if err := acquireAdmissionTokenGeili(ctx, key.token); err != nil {
		forget()
		return nil, err
	}
	if err := acquireAdmissionTokenGeili(ctx, g.slots); err != nil {
		<-key.token
		forget()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.slots
			<-key.token
			forget()
		})
	}, nil
}

func acquireAdmissionTokenGeili(ctx context.Context, token chan struct{}) error {
	select {
	case token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-token
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

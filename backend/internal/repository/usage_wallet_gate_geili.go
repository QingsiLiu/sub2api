package repository

import (
	"context"
	"sync"
)

// Coordinate the contended wallet-write section within this process. SQL row
// locks remain authoritative across instances and other balance writers. This
// queue is independent of subscription admission and never caches a balance.
// Entries exist only while an owner or waiter references them.
type usageWalletGateGeili struct {
	mu   sync.Mutex
	keys map[int64]*usageWalletKeyGeili
}

type usageWalletKeyGeili struct {
	token chan struct{}
	refs  int
}

func (g *usageWalletGateGeili) acquire(ctx context.Context, userID int64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.keys == nil {
		g.keys = make(map[int64]*usageWalletKeyGeili)
	}
	key := g.keys[userID]
	if key == nil {
		key = &usageWalletKeyGeili{token: make(chan struct{}, 1)}
		g.keys[userID] = key
	}
	key.refs++
	g.mu.Unlock()
	forget := func() {
		g.mu.Lock()
		key.refs--
		if key.refs == 0 {
			delete(g.keys, userID)
		}
		g.mu.Unlock()
	}
	select {
	case key.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-key.token
			forget()
			return nil, err
		}
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-key.token
			forget()
		})
	}, nil
}

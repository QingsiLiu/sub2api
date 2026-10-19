// Package releaselifecycle coordinates an opt-in, process-local release drain.
// It never cancels a customer request or resets shared scheduling state.
package releaselifecycle

import (
	"context"
	"os"
	"sync"
	"time"
)

type Status struct {
	Enabled          bool           `json:"enabled"`
	Ready            bool           `json:"ready"`
	Draining         bool           `json:"draining"`
	BackgroundActive bool           `json:"background_active"`
	Active           map[string]int `json:"active"`
	Total            int            `json:"total"`
	BackgroundTotal  int            `json:"background_total"`
	Drained          bool           `json:"drained"`
}

type Controller struct {
	mu         sync.Mutex
	enabled    bool
	ready      bool
	draining   bool
	background bool
	active     map[string]int
}

func New(enabled, background bool) *Controller {
	return &Controller{enabled: enabled, background: background, active: make(map[string]int)}
}

// Configuration is available before Wire starts any background workers.
var Default = New(os.Getenv("GEILI_RELEASE_CONTROL") == "true", os.Getenv("GEILI_RELEASE_CONTROL") != "true")

func (c *Controller) Enabled() bool { return c.Status().Enabled }

func (c *Controller) MarkReady() {
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
}

func (c *Controller) Drain() {
	c.mu.Lock()
	c.draining, c.background = true, false
	c.mu.Unlock()
}

// Resume is used only after a release rollback has selected this instance.
func (c *Controller) Resume() {
	c.mu.Lock()
	c.draining, c.background = false, true
	c.mu.Unlock()
}

func (c *Controller) PauseBackground() {
	c.mu.Lock()
	c.background = false
	c.mu.Unlock()
}

func (c *Controller) beginLocked(kind string) func() {
	c.active[kind]++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.active[kind]--
			c.mu.Unlock()
		})
	}
}

// Begin tracks work already admitted, including detached usage persistence.
// Draining does not reject old nginx workers that are still finishing requests.
func (c *Controller) Begin(kind string) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return func() {}
	}
	return c.beginLocked(kind)
}

// BeginBackground atomically checks admission and accounts for the entire job.
// Existing jobs retain their original context, leases, and business deadlines.
func (c *Controller) BeginBackground(kind string) (func(), bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return func() {}, true
	}
	if c.draining || !c.background {
		return nil, false
	}
	return c.beginLocked("background:" + kind), true
}

func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Status{Enabled: c.enabled, Ready: c.ready && !c.draining, Draining: c.draining, BackgroundActive: c.background, Active: make(map[string]int)}
	for kind, count := range c.active {
		if count > 0 {
			s.Active[kind] = count
			s.Total += count
			if len(kind) >= 11 && kind[:11] == "background:" {
				s.BackgroundTotal += count
			}
		}
	}
	s.Drained = c.draining && s.Total == 0
	return s
}

func (c *Controller) Wait(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c.Status().Total == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

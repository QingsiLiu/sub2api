package releaselifecycle

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestGeiliDrainPreservesAdmittedWork(t *testing.T) {
	c := New(true, true)
	c.MarkReady()
	request := c.Begin("http")
	ws := c.Begin("websocket")
	job, ok := c.BeginBackground("image")
	if !ok {
		t.Fatal("active process must admit work")
	}
	c.Drain()
	if _, ok := c.BeginBackground("image"); ok {
		t.Fatal("draining process claimed another task")
	}
	usage := c.Begin("settlement") // an admitted request can finish after drain starts
	request()
	ws()
	job()
	if c.Status().Drained {
		t.Fatal("detached settlement is still running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if c.Wait(ctx) == nil {
		t.Fatal("drain must wait, not cancel admitted work")
	}
	usage()
	usage()
	if !c.Status().Drained {
		t.Fatal("completed work must drain exactly once")
	}
	c.Resume()
	if !c.Status().Ready {
		t.Fatal("rollback resume must restore readiness")
	}
}

func TestGeiliStandbyAndConcurrentDrain(t *testing.T) {
	c := New(true, false)
	if _, ok := c.BeginBackground("cron"); ok {
		t.Fatal("standby started cron")
	}
	c.Resume()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if done, ok := c.BeginBackground("cron"); ok {
				done()
			}
		}()
	}
	c.Drain()
	wg.Wait()
	if !c.Status().Drained {
		t.Fatalf("leaked work: %+v", c.Status())
	}
}

func TestGeiliDisabledRetainsSingleInstanceBehavior(t *testing.T) {
	c := New(false, false)
	c.Drain()
	done, ok := c.BeginBackground("cron")
	if !ok {
		t.Fatal("legacy mode must not suppress background jobs")
	}
	done()
}

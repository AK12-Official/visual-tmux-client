package desktop

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuitCancellationStaleResponseAndForcedShutdown(t *testing.T) {
	requests := make(chan uint64, 8)
	var cleanups, exits atomic.Int32
	wantErr := errors.New("cleanup failed")
	l := NewLifecycle(func() error { cleanups.Add(1); return wantErr }, func(err error) {
		if !errors.Is(err, wantErr) {
			t.Error(err)
		}
		exits.Add(1)
	}, func(id uint64) { requests <- id })
	l.RequestQuit()
	first := <-requests
	l.RequestQuit()
	if len(requests) != 0 {
		t.Fatal("duplicate confirmation")
	}
	if l.Respond(first+1, true) {
		t.Fatal("accepted unknown response")
	}
	if !l.Respond(first, false) || l.State() != Running {
		t.Fatal("cancel did not preserve running state")
	}
	l.RequestQuit()
	second := <-requests
	if l.Respond(first, true) {
		t.Fatal("accepted stale approval")
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(l.Stop)
	}
	group.Wait()
	if l.Respond(second, true) {
		t.Fatal("accepted approval after forced shutdown")
	}
	if cleanups.Load() != 1 || exits.Load() != 1 || l.State() != Stopped {
		t.Fatal("cleanup/exit was not exactly once")
	}
}

func TestApprovedQuitCleansUpWithoutNativeLoopReturning(t *testing.T) {
	requests := make(chan uint64, 1)
	exited := make(chan struct{})
	l := NewLifecycle(func() error { return nil }, func(error) { close(exited) }, func(id uint64) { requests <- id })
	l.RequestQuit()
	if !l.Respond(<-requests, true) {
		t.Fatal("approval rejected")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("cleanup waited for native Run")
	}
	if l.State() != Stopped {
		t.Fatal(l.State())
	}
	l.Stop()
}

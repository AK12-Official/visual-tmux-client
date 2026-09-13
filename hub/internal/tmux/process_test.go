package tmux

import (
	"context"
	"sync"
	"testing"
)

func TestPTYProcessConcurrencyAndLifecycle(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	sessName := "concurrency-sess"
	if _, err := c.Create(ctx, sessName); err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	proc, err := c.NewProcess(ctx, sessName, 80, 24)
	if err != nil {
		t.Fatalf("NewProcess failed: %v", err)
	}

	// 1. Concurrent Resize and Close calls must be safe and not panic
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				_ = proc.Resize(100+idx, 30+idx) //nolint:errcheck // intentional concurrent stress
			} else {
				_ = proc.Close() //nolint:errcheck // intentional concurrent stress
			}
		}(i)
	}
	wg.Wait()

	// 2. Detach terminates the attach process without terminating the session
	if err := proc.Detach(); err != nil {
		t.Fatalf("Detach failed: %v", err)
	}

	// Wait must be concurrent-safe and return consistent results
	var waitWg sync.WaitGroup
	results := make([]*int, 10)
	errorsList := make([]error, 10)
	for i := 0; i < 10; i++ {
		waitWg.Add(1)
		go func(idx int) {
			defer waitWg.Done()
			code, err := proc.Wait()
			results[idx] = code
			errorsList[idx] = err
		}(i)
	}
	waitWg.Wait()

	for i := 1; i < 10; i++ {
		if results[i] != results[0] {
			t.Errorf("expected identical exit codes across Wait calls, got %v vs %v", results[i], results[0])
		}
		if (errorsList[i] == nil) != (errorsList[0] == nil) {
			t.Errorf("expected identical error presence across Wait calls, got %v vs %v", errorsList[i], errorsList[0])
		}
	}

	// 3. The tmux session itself must survive Detach
	if !c.HasSession(ctx, sessName) {
		t.Errorf("tmux session %q should still exist after client Detach", sessName)
	}
}

func TestProcessSecretIsolation(t *testing.T) {
	c := newTestClient(t)
	cmd := c.AttachCommand("demo-session")
	for _, envVar := range cmd.Env {
		if len(envVar) >= 24 && envVar[:24] == "VISUAL_TMUX_CLIENT_TOKEN" {
			t.Errorf("found secret token in child command environment: %s", envVar)
		}
	}
}

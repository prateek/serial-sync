package patreon

import (
	"context"
	"testing"
	"time"
)

func TestRetryDelayRespectsRetryAfterHeader(t *testing.T) {
	t.Parallel()

	if got, want := retryDelay("60", 1), 60*time.Second; got != want {
		t.Fatalf("retryDelay() = %s, want %s", got, want)
	}
}

func TestRequestBudgetBlocksDuringCooldown(t *testing.T) {
	t.Parallel()

	budget := newRequestBudget()
	update := budget.markRateLimit(40 * time.Millisecond)
	if !update.CooldownExtended {
		t.Fatal("expected cooldown to be extended after rate limit")
	}

	startedAt := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := budget.acquire(context.Background())
		if err == nil {
			budget.release()
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("acquire returned too early with err=%v", err)
	case <-time.After(15 * time.Millisecond):
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("acquire() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("acquire did not unblock after cooldown")
	}

	if elapsed := time.Since(startedAt); elapsed < 35*time.Millisecond {
		t.Fatalf("acquire elapsed = %s, want at least %s", elapsed, 35*time.Millisecond)
	}
}

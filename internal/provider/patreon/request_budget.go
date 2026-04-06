package patreon

import (
	"context"
	"sync"
	"time"
)

const (
	patreonRequestLimitMin              = 1
	patreonRequestLimitInitial          = 1
	patreonRequestLimitMax              = 1
	patreonRequestLimitSuccessThreshold = 32
	patreonRequestLimitPollInterval     = 25 * time.Millisecond
)

type requestBudget struct {
	mu            sync.Mutex
	limit         int
	inFlight      int
	successStreak int
}

type requestBudgetSnapshot struct {
	Limit         int `json:"limit"`
	InFlight      int `json:"in_flight"`
	SuccessStreak int `json:"success_streak"`
}

func newRequestBudget() *requestBudget {
	return &requestBudget{limit: patreonRequestLimitInitial}
}

func (b *requestBudget) acquire(ctx context.Context) (requestBudgetSnapshot, error) {
	ticker := time.NewTicker(patreonRequestLimitPollInterval)
	defer ticker.Stop()
	for {
		b.mu.Lock()
		if b.inFlight < b.limit {
			b.inFlight++
			snapshot := b.snapshotLocked()
			b.mu.Unlock()
			return snapshot, nil
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return requestBudgetSnapshot{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *requestBudget) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inFlight > 0 {
		b.inFlight--
	}
}

func (b *requestBudget) markSuccess() (bool, requestBudgetSnapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.successStreak++
	if b.successStreak >= patreonRequestLimitSuccessThreshold && b.limit < patreonRequestLimitMax {
		b.limit++
		b.successStreak = 0
		return true, b.snapshotLocked()
	}
	return false, b.snapshotLocked()
}

func (b *requestBudget) markRateLimit() (bool, requestBudgetSnapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.successStreak = 0
	next := max(patreonRequestLimitMin, (b.limit+1)/2)
	if next == b.limit {
		return false, b.snapshotLocked()
	}
	b.limit = next
	return true, b.snapshotLocked()
}

func (b *requestBudget) workerCeiling(total int) int {
	if total <= 0 {
		return 0
	}
	return min(total, patreonRequestLimitMax)
}

func (b *requestBudget) snapshotLocked() requestBudgetSnapshot {
	return requestBudgetSnapshot{
		Limit:         b.limit,
		InFlight:      b.inFlight,
		SuccessStreak: b.successStreak,
	}
}

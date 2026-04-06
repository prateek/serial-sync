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
	blockedUntil  time.Time
}

type requestBudgetSnapshot struct {
	Limit         int    `json:"limit"`
	InFlight      int    `json:"in_flight"`
	SuccessStreak int    `json:"success_streak"`
	CooldownUntil string `json:"cooldown_until,omitempty"`
}

type rateLimitUpdate struct {
	LimitReduced     bool
	CooldownExtended bool
	Snapshot         requestBudgetSnapshot
}

func newRequestBudget() *requestBudget {
	return &requestBudget{limit: patreonRequestLimitInitial}
}

func (b *requestBudget) acquire(ctx context.Context) (requestBudgetSnapshot, error) {
	for {
		b.mu.Lock()
		now := time.Now()
		switch {
		case b.blockedUntil.After(now):
			wait := time.Until(b.blockedUntil)
			b.mu.Unlock()
			if err := sleepWithContext(ctx, wait); err != nil {
				return requestBudgetSnapshot{}, err
			}
		case b.inFlight < b.limit:
			b.inFlight++
			snapshot := b.snapshotLocked()
			b.mu.Unlock()
			return snapshot, nil
		default:
			b.mu.Unlock()
			if err := sleepWithContext(ctx, patreonRequestLimitPollInterval); err != nil {
				return requestBudgetSnapshot{}, err
			}
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

func (b *requestBudget) markRateLimit(delay time.Duration) rateLimitUpdate {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.successStreak = 0
	update := rateLimitUpdate{}
	next := max(patreonRequestLimitMin, (b.limit+1)/2)
	if next != b.limit {
		b.limit = next
		update.LimitReduced = true
	}
	if delay > 0 {
		until := time.Now().Add(delay)
		if until.After(b.blockedUntil) {
			b.blockedUntil = until
			update.CooldownExtended = true
		}
	}
	update.Snapshot = b.snapshotLocked()
	return update
}

func (b *requestBudget) workerCeiling(total int) int {
	if total <= 0 {
		return 0
	}
	return min(total, patreonRequestLimitMax)
}

func (b *requestBudget) snapshotLocked() requestBudgetSnapshot {
	snapshot := requestBudgetSnapshot{
		Limit:         b.limit,
		InFlight:      b.inFlight,
		SuccessStreak: b.successStreak,
	}
	if b.blockedUntil.After(time.Now()) {
		snapshot.CooldownUntil = b.blockedUntil.UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

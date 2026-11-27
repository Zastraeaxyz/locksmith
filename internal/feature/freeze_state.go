package feature

// Freeze state comes from the API, never from stored notification state. The
// lock log only dates the freeze so the status can say how long it has held.
//
// ChastiSafe can keep reporting frozenByCard for a short time after a card
// freeze has ended. A freeze lasts at most freezeMaxFactor draw intervals, so a
// card freeze past that point is treated as stale: the API is asked once more
// and the surfaces stop claiming a duration the freeze cannot have.

import (
	"context"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

const (
	// freezeMaxFactor is the longest a card freeze can hold, as a multiple of
	// the lock's draw interval.
	freezeMaxFactor = 4
	// freezeRetryDelay is how long to wait before checking a card freeze that
	// has outlived its maximum.
	freezeRetryDelay = time.Minute
	// freezeRetryWindow is how long past the maximum Locksmith keeps looking
	// for a card freeze to clear before it falls back to the regular cadence.
	freezeRetryWindow = 5 * time.Minute
)

// freezeState is what the lock log says about one frozen lock.
type freezeState struct {
	since       time.Time
	pastMaximum bool
}

// freezePastMaximum reports whether a card freeze has outlived the longest it
// can hold. A keyholder freeze has no maximum, and a lock without a draw
// interval has no scale.
func freezePastMaximum(lock *cs.LoadedLock, since, now time.Time) bool {
	if lock == nil || since.IsZero() || lock.FrozenByKeyholder || !lock.FrozenByCard || lock.Regularity <= 0 {
		return false
	}
	return !now.Before(freezeGuaranteeAt(since, time.Duration(lock.Regularity)*time.Minute))
}

// freezeStates finds the freeze start for each frozen lock and whether a card
// freeze is already past its maximum. ChastiSafe does not return a start time,
// so the lock log is the only source.
func (f *Feature) freezeStates(ctx context.Context, client *cs.Client, locks []cs.LoadedLock, now time.Time) map[cs.ID]freezeState {
	states := make(map[cs.ID]freezeState)
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsFrozen() {
			continue
		}
		var state freezeState
		entries, err := f.freezeLog(ctx, client, lock)
		if err != nil {
			f.log.Warn("loading freeze history", "lock_id", lock.ID.String(), "err", err)
		} else if since, ok := freezeStartFor(lock, entries); ok {
			state.since = since
		} else {
			f.log.Debug("freeze start not found",
				"lock_id", lock.ID.String(),
				"frozen_by_card", lock.FrozenByCard,
				"frozen_by_keyholder", lock.FrozenByKeyholder,
				"entries", len(entries),
			)
		}
		state.pastMaximum = freezePastMaximum(lock, state.since, now)
		states[lock.ID] = state
	}
	return states
}

// freshLocks returns the lock state to act on. When a card freeze in the list
// is already past the longest it can hold, it asks the API once more, because
// ChastiSafe can keep reporting frozenByCard for a short time after the freeze
// ends. The first result is kept when the refresh fails.
func (f *Feature) freshLocks(ctx context.Context, client *cs.Client, locks []cs.LoadedLock, now time.Time) ([]cs.LoadedLock, map[cs.ID]freezeState) {
	states := f.freezeStates(ctx, client, locks, now)
	if !anyPastMaximum(states) {
		return locks, states
	}
	refreshed, err := client.Loaded(ctx)
	if err != nil {
		f.log.Warn("refreshing locks after a freeze timed out", "err", err)
		return locks, states
	}
	return refreshed, f.freezeStates(ctx, client, refreshed, now)
}

// anyPastMaximum reports whether any lock's card freeze is past its maximum.
func anyPastMaximum(states map[cs.ID]freezeState) bool {
	for _, state := range states {
		if state.pastMaximum {
			return true
		}
	}
	return false
}

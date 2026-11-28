package feature

// Deck observations are the before-and-after numbers that make a hidden deck's
// double up exact. The log records the card, not the deck size, so the bot
// stores a small snapshot whenever it actually sees a lock. Hidden locks store
// only the count; visible locks also store the composition.

import (
	"context"
	"encoding/json"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
)

// observeLock records the lock's current deck. It never blocks the caller: a
// failed observation is a missed estimate, not a failed draw.
func (f *Feature) observeLock(ctx context.Context, lock *cs.LoadedLock) {
	if f == nil || f.store == nil || lock == nil || lock.ID == "" || !lock.IsCardLock() {
		return
	}

	total := lock.CardCount
	deckJSON := ""
	if !lock.Hidden && lock.Deck != nil {
		total = lock.Deck.Total()
		if encoded, err := json.Marshal(lock.Deck); err == nil {
			deckJSON = string(encoded)
		}
	}

	obs := store.LockObservation{
		LockID:     lock.ID.String(),
		ObservedAt: f.now().UTC(),
		CardCount:  lock.CardCount,
		DeckTotal:  total,
		DeckJSON:   deckJSON,
		Draws:      string(lock.Draws),
		Hidden:     lock.Hidden,
	}
	if err := f.store.RecordLockObservation(ctx, obs); err != nil {
		f.log.Debug("recording lock observation", "lock_id", lock.ID.String(), "err", err)
	}
}

func (f *Feature) observeLocks(ctx context.Context, locks []cs.LoadedLock) {
	for idx := range locks {
		f.observeLock(ctx, &locks[idx])
	}
}

// progressObservations loads a lock's snapshots since a moment. A missing store
// or a read error leaves the progress report with fewer anchors, not a failure.
func (f *Feature) progressObservations(ctx context.Context, lockID string, since time.Time) []store.LockObservation {
	if f == nil || f.store == nil || lockID == "" {
		return nil
	}
	observations, err := f.store.LockObservations(ctx, lockID, since)
	if err != nil {
		f.log.Warn("loading lock observations", "lock_id", lockID, "err", err)
		return nil
	}
	return observations
}

// progressObservationSince returns the start of the window worth loading. The
// current lock only needs its own lifetime; a previous lock never has live
// observations, so its caller skips this.
func progressObservationSince(lock *cs.LoadedLock) time.Time {
	if lock == nil || lock.LoadTime.IsZero() {
		return time.Time{}
	}
	since := lock.LoadTime.Time
	if !lock.LastResetTime.IsZero() && lock.LastResetTime.Time.Before(since) {
		since = lock.LastResetTime.Time
	}
	return since.Add(-time.Minute)
}

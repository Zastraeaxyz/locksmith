package feature

// The lock selection module answers one question for every surface: which of
// the member's locks does this interaction mean?
//
// The draw board, /lock open, /lock checkin, /lock log, /lock notify, and the
// /lock autocomplete all cross this module. It is pure: it takes the caller's
// ChastiSafe ID and the lock lists the adapter returned, and it returns locks
// or a reference error. No Discord session and no ChastiSafe client is needed,
// so the selection rules have one test surface.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

// lockRef is one resolved reference. Exactly one field is set.
type lockRef struct {
	Running  *cs.LoadedLock
	Previous *cs.RecentLock
}

// ownLocks keeps the locks the member is the lockee of. An empty ChastiSafe ID
// means the account is not known, so nothing is filtered.
func ownLocks(locks []cs.LoadedLock, chastisafeID string) []cs.LoadedLock {
	out := make([]cs.LoadedLock, 0, len(locks))
	for idx := range locks {
		if chastisafeID != "" && locks[idx].LockeeID.String() != chastisafeID {
			continue
		}
		out = append(out, locks[idx])
	}
	return out
}

// ownLockViews wraps the member's locks as lockee views.
func ownLockViews(locks []cs.LoadedLock, chastisafeID string) []cs.LockView {
	own := ownLocks(locks, chastisafeID)
	views := make([]cs.LockView, 0, len(own))
	for idx := range own {
		views = append(views, cs.LockView{Lock: own[idx], Role: cs.RoleLockee})
	}
	return views
}

// newestRunningIndex returns the index of the newest running lock, or -1.
func newestRunningIndex(locks []cs.LoadedLock) int {
	best := -1
	for idx := range locks {
		if best < 0 || locks[idx].LoadTime.Time.After(locks[best].LoadTime.Time) {
			best = idx
		}
	}
	return best
}

// newestRunning returns the newest running lock, or nil.
func newestRunning(locks []cs.LoadedLock) *cs.LoadedLock {
	idx := newestRunningIndex(locks)
	if idx < 0 {
		return nil
	}
	return &locks[idx]
}

// newestRecentIndex returns the index of the most recently unlocked lock, or -1.
func newestRecentIndex(locks []cs.RecentLock) int {
	best := -1
	for idx := range locks {
		if best < 0 || locks[idx].UnlockTime.Time.After(locks[best].UnlockTime.Time) {
			best = idx
		}
	}
	return best
}

// newestRecent returns the most recently unlocked lock, or nil.
func newestRecent(locks []cs.RecentLock) *cs.RecentLock {
	idx := newestRecentIndex(locks)
	if idx < 0 {
		return nil
	}
	return &locks[idx]
}

// newestRunningFirst copies the locks, newest first.
func newestRunningFirst(locks []cs.LoadedLock) []cs.LoadedLock {
	out := append([]cs.LoadedLock(nil), locks...)
	sort.SliceStable(out, func(a, b int) bool {
		return out[a].LoadTime.Time.After(out[b].LoadTime.Time)
	})
	return out
}

// newestRecentFirst copies the locks, most recently unlocked first.
func newestRecentFirst(locks []cs.RecentLock) []cs.RecentLock {
	out := append([]cs.RecentLock(nil), locks...)
	sort.SliceStable(out, func(a, b int) bool {
		return out[a].UnlockTime.Time.After(out[b].UnlockTime.Time)
	})
	return out
}

// findLockByID returns the lock with that ID, or nil.
func findLockByID(locks []cs.LoadedLock, id cs.ID) *cs.LoadedLock {
	for idx := range locks {
		if locks[idx].ID == id {
			return &locks[idx]
		}
	}
	return nil
}

// drawableLock returns the caller's newest card lock that can be drawn now:
// active, unfrozen, not ready, and preferring one with a draw available.
func drawableLock(locks []cs.LoadedLock, chastisafeID string) *cs.LoadedLock {
	var newest, withDraws *cs.LoadedLock
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsCardLock() || lock.IsFrozen() || lock.IsReady() {
			continue
		}
		if chastisafeID != "" && lock.LockeeID.String() != chastisafeID {
			continue
		}
		if newest == nil || lock.LoadTime.Time.After(newest.LoadTime.Time) {
			newest = lock
		}
		if lock.DrawsAvailable() > 0 {
			if withDraws == nil || lock.LoadTime.Time.After(withDraws.LoadTime.Time) {
				withDraws = lock
			}
		}
	}
	if withDraws != nil {
		return withDraws
	}
	return newest
}

// readyCardLock returns the caller's newest card lock that is ready to unlock.
func readyCardLock(locks []cs.LoadedLock, chastisafeID string) *cs.LoadedLock {
	var newest *cs.LoadedLock
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsCardLock() || !lock.IsReady() || lock.IsFrozen() {
			continue
		}
		if chastisafeID != "" && lock.LockeeID.String() != chastisafeID {
			continue
		}
		if newest == nil || lock.LoadTime.Time.After(newest.LoadTime.Time) {
			newest = lock
		}
	}
	return newest
}

// readyOpenLock returns the caller's newest lock of any type that is ready to
// open and not frozen.
func readyOpenLock(locks []cs.LoadedLock, chastisafeID string) *cs.LoadedLock {
	var ready *cs.LoadedLock
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsReady() || lock.IsFrozen() {
			continue
		}
		if chastisafeID != "" && lock.LockeeID.String() != chastisafeID {
			continue
		}
		if ready == nil || lock.LoadTime.Time.After(ready.LoadTime.Time) {
			ready = lock
		}
	}
	return ready
}

// drawRefusalTarget returns the newest card lock even when it cannot be drawn,
// so the caller can explain why.
func drawRefusalTarget(locks []cs.LoadedLock, chastisafeID string) *cs.LoadedLock {
	var newest *cs.LoadedLock
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsCardLock() {
			continue
		}
		if chastisafeID != "" && lock.LockeeID.String() != chastisafeID {
			continue
		}
		if newest == nil || lock.LoadTime.Time.After(newest.LoadTime.Time) {
			newest = lock
		}
	}
	return newest
}

// nextCheckinLock returns the caller's active lock that needs check-ins,
// preferring the one due soonest. An overdue check-in counts as due now.
func nextCheckinLock(locks []cs.LoadedLock, chastisafeID string, now time.Time) *cs.LoadedLock {
	var pick *cs.LoadedLock
	var pickIn time.Duration
	for idx := range locks {
		lock := &locks[idx]
		if lock.IsReady() || lock.CheckInPeriod <= 0 {
			continue
		}
		if chastisafeID != "" && lock.LockeeID.String() != chastisafeID {
			continue
		}
		remaining := checkinIn(lock, now)
		if remaining < 0 {
			remaining = 0
		}
		if pick == nil || remaining < pickIn {
			pick, pickIn = lock, remaining
		}
	}
	return pick
}

// findLock resolves the lock a watched subscription refers to: the named lock,
// else the newest drawable card lock, else the lock with the next check-in.
func findLock(locks []cs.LoadedLock, lockID, chastisafeID string, now time.Time) *cs.LoadedLock {
	for idx := range locks {
		if lockID != "" && locks[idx].ID.String() == lockID {
			return &locks[idx]
		}
	}
	if lock := drawableLock(locks, chastisafeID); lock != nil {
		return lock
	}
	return nextCheckinLock(locks, chastisafeID, now)
}

// countReadyLocks counts the locks that can be opened now.
func countReadyLocks(locks []cs.LoadedLock) int {
	count := 0
	for idx := range locks {
		if locks[idx].IsReady() && !locks[idx].IsFrozen() {
			count++
		}
	}
	return count
}

// countCheckinLocks counts the active locks that still need check-ins.
func countCheckinLocks(locks []cs.LoadedLock) int {
	count := 0
	for idx := range locks {
		if locks[idx].CheckInPeriod > 0 && !locks[idx].IsReady() {
			count++
		}
	}
	return count
}

// resolveRef matches a ref against running locks first, then previous locks. A
// ref matches by exact ID, exact name, or unique name prefix. Several matches
// with the same name resolve to the newest and add a note.
func resolveRef(ref string, running []cs.LoadedLock, recent []cs.RecentLock) (lockRef, string, error) {
	ref = strings.TrimSpace(ref)
	lower := strings.ToLower(ref)

	for idx := range running {
		if running[idx].ID.String() == ref {
			return lockRef{Running: &running[idx]}, "", nil
		}
	}
	for idx := range recent {
		if recent[idx].LockID.String() == ref {
			return lockRef{Previous: &recent[idx]}, "", nil
		}
	}

	var exactRunning []int
	for idx := range running {
		if strings.ToLower(strings.TrimSpace(running[idx].LockName)) == lower {
			exactRunning = append(exactRunning, idx)
		}
	}
	if len(exactRunning) == 1 {
		return lockRef{Running: &running[exactRunning[0]]}, "", nil
	}
	if len(exactRunning) > 1 {
		newest := exactRunning[0]
		for _, idx := range exactRunning[1:] {
			if running[idx].LoadTime.Time.After(running[newest].LoadTime.Time) {
				newest = idx
			}
		}
		return lockRef{Running: &running[newest]},
			fmt.Sprintf("Several active locks have that name. Using the newest, **%s**.", lockName(&running[newest])), nil
	}

	var exactRecent []int
	for idx := range recent {
		if strings.ToLower(strings.TrimSpace(recent[idx].LockName)) == lower {
			exactRecent = append(exactRecent, idx)
		}
	}
	if len(exactRecent) > 0 {
		newest := exactRecent[0]
		for _, idx := range exactRecent[1:] {
			if recent[idx].UnlockTime.Time.After(recent[newest].UnlockTime.Time) {
				newest = idx
			}
		}
		return lockRef{Previous: &recent[newest]}, "", nil
	}

	var (
		runningMatches []int
		recentMatches  []int
		names          []string
	)
	for idx := range running {
		if prefixMatch(running[idx].LockName, lower) {
			runningMatches = append(runningMatches, idx)
			names = append(names, lockName(&running[idx]))
		}
	}
	for idx := range recent {
		if prefixMatch(recent[idx].LockName, lower) {
			recentMatches = append(recentMatches, idx)
			names = append(names, recentName(&recent[idx]))
		}
	}

	switch {
	case len(runningMatches) == 1:
		return lockRef{Running: &running[runningMatches[0]]}, "", nil
	case len(runningMatches) > 1:
		return lockRef{}, "", fmt.Errorf("`%s` matches several active locks: %s", ref, strings.Join(names, ", "))
	case len(recentMatches) == 1:
		return lockRef{Previous: &recent[recentMatches[0]]}, "", nil
	case len(recentMatches) > 1:
		return lockRef{}, "", fmt.Errorf("`%s` matches several previous locks: %s", ref, strings.Join(names, ", "))
	default:
		return lockRef{}, "", fmt.Errorf("No lock matches `%s`.", ref)
	}
}

// prefixMatch reports whether name starts with lower. Both are already
// trimmed and lowercased by the caller.
func prefixMatch(name, lower string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name != "" && strings.HasPrefix(name, lower)
}

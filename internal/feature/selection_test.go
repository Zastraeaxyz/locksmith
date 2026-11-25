package feature

import (
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func selectionTestNow() time.Time {
	return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
}

func TestOwnLocksKeepsOnlyLockee(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "mine", LockeeID: "me"},
		{ID: "theirs", LockeeID: "someone"},
	}
	if got := ownLocks(locks, "me"); len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("ownLocks = %+v", got)
	}
	if got := ownLocks(locks, ""); len(got) != 2 {
		t.Fatalf("empty ID should not filter: %+v", got)
	}
}

func TestNewestRunningFirstOrdersByLoadTime(t *testing.T) {
	now := selectionTestNow()
	locks := []cs.LoadedLock{
		{ID: "old", LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
		{ID: "new", LoadTime: cs.Time{Time: now}},
		{ID: "mid", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
	}

	ordered := newestRunningFirst(locks)
	want := []cs.ID{"new", "mid", "old"}
	for idx, id := range want {
		if ordered[idx].ID != id {
			t.Fatalf("order = %v, want %v", idsOf(ordered), want)
		}
	}
	if locks[0].ID != "old" {
		t.Fatal("newestRunningFirst must not reorder the input")
	}
}

func TestResolveRefMatchesIDNameAndPrefix(t *testing.T) {
	now := selectionTestNow()
	running := []cs.LoadedLock{
		{ID: "a", LockName: "Steel belt", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "b", LockName: "Test card", LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
	}

	for _, ref := range []string{"a", "steel belt", "Steel"} {
		resolved, _, err := resolveRef(ref, running, nil)
		if err != nil {
			t.Fatalf("resolveRef(%q): %v", ref, err)
		}
		if resolved.Running == nil || resolved.Running.ID != "a" {
			t.Fatalf("resolveRef(%q) = %+v", ref, resolved)
		}
	}

	resolved, _, err := resolveRef("Test", running, nil)
	if err != nil {
		t.Fatalf("prefix resolve: %v", err)
	}
	if resolved.Running == nil || resolved.Running.ID != "b" {
		t.Fatalf("prefix = %+v", resolved)
	}
}

func TestResolveRefPicksNewestOfSameName(t *testing.T) {
	now := selectionTestNow()
	running := []cs.LoadedLock{
		{ID: "old", LockName: "Night", LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
		{ID: "new", LockName: "Night", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
	}

	resolved, note, err := resolveRef("night", running, nil)
	if err != nil {
		t.Fatalf("resolveRef: %v", err)
	}
	if resolved.Running == nil || resolved.Running.ID != "new" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if !strings.Contains(note, "newest") {
		t.Fatalf("note = %q", note)
	}
}

func TestResolveRefFallsBackToPreviousLocks(t *testing.T) {
	now := selectionTestNow()
	recent := []cs.RecentLock{
		{LockID: "r1", LockName: "Lock one", UnlockTime: cs.Time{Time: now.Add(-48 * time.Hour)}},
		{LockID: "r2", LockName: "Lock two", UnlockTime: cs.Time{Time: now.Add(-24 * time.Hour)}},
	}

	resolved, _, err := resolveRef("r1", nil, recent)
	if err != nil {
		t.Fatalf("resolveRef: %v", err)
	}
	if resolved.Previous == nil || resolved.Previous.LockID != "r1" {
		t.Fatalf("resolved = %+v", resolved)
	}

	resolved, _, err = resolveRef("lock t", nil, recent)
	if err != nil {
		t.Fatalf("prefix resolve: %v", err)
	}
	if resolved.Previous == nil || resolved.Previous.LockID != "r2" {
		t.Fatalf("prefix = %+v", resolved)
	}
}

func TestResolveRefExplainsAmbiguityAndMisses(t *testing.T) {
	now := selectionTestNow()
	running := []cs.LoadedLock{
		{ID: "a", LockName: "Test one", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "b", LockName: "Test two", LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
	}

	if _, _, err := resolveRef("test", running, nil); err == nil ||
		!strings.Contains(err.Error(), "several active locks") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := resolveRef("missing", running, nil); err == nil ||
		!strings.Contains(err.Error(), "No lock matches") {
		t.Fatalf("err = %v", err)
	}
}

func idsOf(locks []cs.LoadedLock) []cs.ID {
	out := make([]cs.ID, 0, len(locks))
	for _, lock := range locks {
		out = append(out, lock.ID)
	}
	return out
}

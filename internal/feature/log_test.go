package feature

import (
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func at(d time.Duration) cs.Time {
	return cs.Time{Time: time.Now().Add(d)}
}

func TestResolveLogTargetNewest(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "old", LockName: "Older lock", LoadTime: at(-2 * time.Hour)},
		{ID: "new", LockName: "Newer lock", LoadTime: at(-time.Hour)},
	}

	target, note, err := resolveLogTarget("", locks, nil)
	if err != nil {
		t.Fatalf("resolveLogTarget: %v", err)
	}
	if target.running == nil || target.running.ID != "new" {
		t.Fatalf("target = %+v, want newest lock", target)
	}
	if note == "" {
		t.Fatal("expected a note about choosing the newest lock")
	}
}

func TestResolveLogTargetByName(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "a", LockName: "Steel belt", LoadTime: at(-time.Hour)},
		{ID: "b", LockName: "Test card", LoadTime: at(-2 * time.Hour)},
	}

	target, _, err := resolveLogTarget("steel belt", locks, nil)
	if err != nil {
		t.Fatalf("resolveLogTarget: %v", err)
	}
	if target.running == nil || target.running.ID != "a" {
		t.Fatalf("target = %+v", target)
	}

	target, _, err = resolveLogTarget("Test", locks, nil)
	if err != nil {
		t.Fatalf("prefix resolve: %v", err)
	}
	if target.running == nil || target.running.ID != "b" {
		t.Fatalf("prefix target = %+v", target)
	}
}

func TestResolveLogTargetPrevious(t *testing.T) {
	recent := []cs.RecentLock{
		{LockID: "r1", LockName: "Lock one", UnlockTime: at(-48 * time.Hour)},
		{LockID: "r2", LockName: "Lock two", UnlockTime: at(-24 * time.Hour)},
	}

	target, note, err := resolveLogTarget("", nil, recent)
	if err != nil {
		t.Fatalf("resolveLogTarget: %v", err)
	}
	if target.previous == nil || target.previous.LockID != "r2" {
		t.Fatalf("target = %+v, want newest previous lock", target)
	}
	if note == "" {
		t.Fatal("expected a note about using the most recent lock")
	}

	target, _, err = resolveLogTarget("r1", nil, recent)
	if err != nil {
		t.Fatalf("resolve by id: %v", err)
	}
	if target.previous == nil || target.previous.LockID != "r1" {
		t.Fatalf("id target = %+v", target)
	}
}

func TestResolveLogTargetAmbiguous(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "a", LockName: "Test one", LoadTime: at(-time.Hour)},
		{ID: "b", LockName: "Test two", LoadTime: at(-2 * time.Hour)},
	}
	if _, _, err := resolveLogTarget("test", locks, nil); err == nil {
		t.Fatal("expected an ambiguity error")
	}
}

func TestNotificationText(t *testing.T) {
	lock := &cs.LoadedLock{
		ID:             "lock",
		LockName:       "Test card",
		Type:           "card",
		Picks:          2,
		MaxPicks:       3,
		Wait:           120,
		GreensRequired: 10000,
		Deck:           cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}

	text := notificationText(lock)
	for _, want := range []string{
		"Test card",
		"2 draws available",
		"of 3",
		"3 cards left",
		"2 greens needed",
		"Next draw in 2m",
		"/lock draw",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("notificationText missing %q:\n%s", want, text)
		}
	}
}

func TestDrawsAvailableZero(t *testing.T) {
	lock := &cs.LoadedLock{Type: "card", Picks: 0, Wait: 300}
	if got := lock.DrawsAvailable(); got != 0 {
		t.Fatalf("DrawsAvailable = %d, want 0", got)
	}
}

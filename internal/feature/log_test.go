package feature

import (
	"context"
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

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	text := notificationText(lock, now)
	for _, want := range []string{
		"Test card",
		"2 draws available",
		"of 3",
		"3 cards left",
		"2 greens needed",
		"Next draw <t:",
		":R>",
		"/lock draw",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("notificationText missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"2m", "in 2 minutes"} {
		if strings.Contains(text, bad) {
			t.Errorf("notificationText still contains %q:\n%s", bad, text)
		}
	}
}

func TestDrawsAvailableZero(t *testing.T) {
	lock := &cs.LoadedLock{Type: "card", Picks: 0, Wait: 300}
	if got := lock.DrawsAvailable(); got != 0 {
		t.Fatalf("DrawsAvailable = %d, want 0", got)
	}
}

func TestLogOutputUsesCardEmoji(t *testing.T) {
	stamp := cs.Time{Time: time.Date(2026, 11, 24, 14, 32, 5, 0, time.UTC)}
	entries := []cs.LogEntry{
		{Timestamp: stamp, Actor: 2, EventType: 6, Card: "g"},
		{Timestamp: stamp, Actor: 1, EventType: 6, Card: "d"},
		{Timestamp: stamp, Actor: 1, EventType: 6, Card: "i", Amount: 4},
	}

	out := logOutput("", entries, false, false, "")
	for _, want := range []string{
		"<:green:1574875078255185957>",
		"<:double:1574875037104742581>",
		"<:dice:1574875035749982268>",
		"<t:",
		"Lockee picked",
		"Keyholder picked",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
}

func TestLogHeaderUsesCompactStatus(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	f := &Feature{now: func() time.Time { return now }}

	header := f.logHeader(context.Background(), nil, &logTarget{running: &lock}, "viewer")
	for _, want := range []string{
		":locked: Locked",
		":key: Keyholder: keyholder#0",
		":flower_playing_cards:",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("header missing %q:\n%s", want, header)
		}
	}
	for _, bad := range []string{"Type:", "Role:", "Purpose:", "Lockee:"} {
		if strings.Contains(header, bad) {
			t.Errorf("header should omit %q:\n%s", bad, header)
		}
	}
}

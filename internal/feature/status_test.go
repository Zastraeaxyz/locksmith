package feature

import (
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func statusTestNow() time.Time {
	return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
}

func statusTestLock(id, name string, now time.Time) cs.LoadedLock {
	return cs.LoadedLock{
		ID:          cs.ID(id),
		LockName:    name,
		Type:        "card",
		Lockee:      "viewer#0",
		LockeeID:    "viewer",
		Keyholder:   "keyholder#0",
		KeyholderID: "keyholder",
		LockPurpose: cs.Purpose("CHASTITY"),
		CardCount:   5,
		Picks:       0,
		MaxPicks:    1,
		Wait:        300,
		LoadTime:    cs.Time{Time: now.Add(-time.Hour)},
	}
}

func TestStatusSingleLockCompact(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	text := renderStatus([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now)

	for _, bad := range []string{"Type:", "Role:", "Purpose:", "Lockee:"} {
		if strings.Contains(text, bad) {
			t.Errorf("single-lock status should omit %q:\n%s", bad, text)
		}
	}
	for _, want := range []string{
		":locked: Locked",
		":key: Keyholder: keyholder#0",
		":flower_playing_cards: 5 cards · 0/1 draws",
		":hourglass: Next draw <t:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
}

func TestStatusVaryingPurpose(t *testing.T) {
	now := statusTestNow()
	first := statusTestLock("a", "Lock A", now)
	second := statusTestLock("b", "Lock B", now)
	second.LockPurpose = cs.Purpose("BONDAGE")

	text := renderStatus([]cs.LockView{
		{Lock: first, Role: cs.RoleLockee},
		{Lock: second, Role: cs.RoleLockee},
	}, "viewer", nil, now)

	for _, want := range []string{"Purpose: CHASTITY", "Purpose: BONDAGE"} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"Type:", "Role:"} {
		if strings.Contains(text, bad) {
			t.Errorf("status should omit %q:\n%s", bad, text)
		}
	}
}

func TestStatusVaryingType(t *testing.T) {
	now := statusTestNow()
	card := statusTestLock("a", "Card lock", now)
	timer := statusTestLock("b", "Timer lock", now)
	timer.Type = "timer"
	timer.TimerRemaining = 3600

	text := renderStatus([]cs.LockView{
		{Lock: card, Role: cs.RoleLockee},
		{Lock: timer, Role: cs.RoleLockee},
	}, "viewer", nil, now)

	for _, want := range []string{"Type: card", "Type: timer", ":hourglass: Unlocks <t:"} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Role:") {
		t.Errorf("status should omit Role:\n%s", text)
	}
}

func TestStatusSelfLocked(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	lock.Keyholder = "<self>"
	lock.KeyholderID = "0"

	text := renderStatus([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now)
	if !strings.Contains(text, ":key: Self-locked") {
		t.Errorf("status missing self-locked line:\n%s", text)
	}
	if strings.Contains(text, "Keyholder:") {
		t.Errorf("status should not name a keyholder:\n%s", text)
	}
}

func TestStatusFrozenSince(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	lock.FrozenByKeyholder = true
	since := now.Add(-2 * time.Hour)

	text := renderStatus(
		[]cs.LockView{{Lock: lock, Role: cs.RoleLockee}},
		"viewer",
		map[cs.ID]time.Time{lock.ID: since},
		now,
	)

	if !strings.Contains(text, ":ice_cube: Frozen since <t:") {
		t.Errorf("status missing frozen timestamp:\n%s", text)
	}
	if strings.Contains(text, "check-in") || strings.Contains(text, "Check-in") {
		t.Errorf("state line should not repeat check-in:\n%s", text)
	}
}

func TestStatusReady(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	lock.ReadyToUnlock = true

	text := renderStatus([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now)
	if !strings.Contains(text, ":unlocked: Ready to unlock") {
		t.Errorf("status missing ready state:\n%s", text)
	}
}

func TestFreezeStart(t *testing.T) {
	now := statusTestNow()

	newestUnfreeze := []cs.LogEntry{
		{EventType: 3, Timestamp: cs.Time{Time: now}},
		{EventType: 2, Timestamp: cs.Time{Time: now.Add(-time.Hour)}},
	}
	if _, ok := freezeStart(newestUnfreeze); ok {
		t.Fatal("unfreeze after freeze should not report a freeze start")
	}

	newestFreeze := []cs.LogEntry{
		{EventType: 2, Timestamp: cs.Time{Time: now.Add(-time.Hour)}},
		{EventType: 3, Timestamp: cs.Time{Time: now.Add(-2 * time.Hour)}},
	}
	since, ok := freezeStart(newestFreeze)
	if !ok {
		t.Fatal("expected a freeze start")
	}
	if !since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("since = %s", since)
	}
}

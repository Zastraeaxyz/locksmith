package feature

import (
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func openTestNow() time.Time {
	return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
}

func TestPickOpenLockNewestReady(t *testing.T) {
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "older", LockName: "Older", Type: "card", ReadyToUnlock: true, LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
		{ID: "newer", LockName: "Newer", Type: "card", ReadyToUnlock: true, LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "running", LockName: "Running", Type: "card", Picks: 1, LoadTime: cs.Time{Time: now}},
	}

	lock, note, err := pickOpenLock("", locks, now)
	if err != nil {
		t.Fatalf("pickOpenLock: %v", err)
	}
	if lock.ID != "newer" {
		t.Fatalf("lock = %q, want newer", lock.ID)
	}
	if !strings.Contains(note, "newest ready lock") {
		t.Fatalf("note = %q, want a newest-ready note", note)
	}
}

func TestPickOpenLockByReference(t *testing.T) {
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "one", LockName: "Quiet Week", Type: "card", ReadyToUnlock: true, LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "two", LockName: "Quiet Month", Type: "card", ReadyToUnlock: true, LoadTime: cs.Time{Time: now}},
	}

	lock, note, err := pickOpenLock("Quiet Week", locks, now)
	if err != nil {
		t.Fatalf("pickOpenLock: %v", err)
	}
	if lock.ID != "one" {
		t.Fatalf("lock = %q, want one", lock.ID)
	}
	if note != "" {
		t.Fatalf("note = %q, want none", note)
	}

	if _, _, err := pickOpenLock("missing", locks, now); err == nil {
		t.Fatal("missing reference should fail")
	}
}

func TestPickOpenLockExplainsWhyNotReady(t *testing.T) {
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "card", LockName: "Card lock", Type: "card", Picks: 2, Deck: cs.Deck{cs.CardGreen: 2}},
	}

	_, _, err := pickOpenLock("", locks, now)
	if err == nil {
		t.Fatal("a running lock should not open")
	}
	for _, want := range []string{"Card lock", "not ready", "2 cards left"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestPickOpenLockRejectsFrozenLock(t *testing.T) {
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "frozen", LockName: "Frozen", Type: "card", ReadyToUnlock: true, FrozenByKeyholder: true},
	}

	_, _, err := pickOpenLock("", locks, now)
	if err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("error = %v, want a frozen refusal", err)
	}
}

func TestPickOpenLockWithoutLocks(t *testing.T) {
	if _, _, err := pickOpenLock("", nil, openTestNow()); err == nil {
		t.Fatal("no locks should fail")
	}
}

func TestOpenNotReadyTimer(t *testing.T) {
	now := openTestNow()
	lock := &cs.LoadedLock{ID: "timer", LockName: "Timer", Type: "timer", TimerRemaining: 3600}

	err := openNotReadyError(lock, now)
	if err == nil {
		t.Fatal("a running timer should not open")
	}
	for _, want := range []string{"Timer", "not ready", "unlocks"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	lock.ReadyToUnlock = true
	if err := openNotReadyError(lock, now); err != nil {
		t.Fatalf("ready lock: %v", err)
	}
}

func TestOpenResultText(t *testing.T) {
	lock := &cs.LoadedLock{ID: "lock", LockName: "Quiet Week"}
	text := openResultText(lock, "5241")
	for _, want := range []string{"Quiet Week", "`5241`", "/history"} {
		if !strings.Contains(text, want) {
			t.Errorf("open text missing %q:\n%s", want, text)
		}
	}

	empty := openResultText(lock, "")
	if strings.Contains(empty, "Device code:") {
		t.Errorf("empty code should not invent a code:\n%s", empty)
	}
}

func TestOpenCandidatesOnlyReady(t *testing.T) {
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "ready", LockName: "Ready", Type: "card", ReadyToUnlock: true, LockeeID: "me", LoadTime: cs.Time{Time: now}},
		{ID: "running", LockName: "Running", Type: "card", Picks: 1, LockeeID: "me", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "frozen", LockName: "Frozen", Type: "card", ReadyToUnlock: true, FrozenByCard: true, LockeeID: "me"},
		{ID: "other", LockName: "Other", Type: "card", ReadyToUnlock: true, LockeeID: "them"},
	}

	choices := openCandidates(locks, "me")
	if len(choices) != 1 || choices[0].id != "ready" {
		t.Fatalf("choices = %+v, want only the ready own lock", choices)
	}
}

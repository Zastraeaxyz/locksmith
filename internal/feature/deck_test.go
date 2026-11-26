package feature

import (
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestDeckTargetPrefersDrawable(t *testing.T) {
	now := selectionTestNow()
	locks := []cs.LoadedLock{
		{ID: "frozen", LockName: "Frozen", Type: "card", Picks: 1, FrozenByKeyholder: true, LoadTime: cs.Time{Time: now}},
		{ID: "drawable", LockName: "Drawable", Type: "card", Picks: 1, LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "timer", LockName: "Timer", Type: "timer", LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
	}

	got, _, err := deckTarget(locks, "", "")
	if err != nil {
		t.Fatalf("deckTarget: %v", err)
	}
	if got == nil || got.ID != "drawable" {
		t.Fatalf("deckTarget = %+v, want the drawable lock", got)
	}
}

func TestDeckTargetFallsBackToNewestCardLock(t *testing.T) {
	now := selectionTestNow()
	locks := []cs.LoadedLock{
		{ID: "old", LockName: "Old", Type: "card", Picks: 1, FrozenByKeyholder: true, LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
		{ID: "new", LockName: "New", Type: "card", Picks: 1, FrozenByKeyholder: true, LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "timer", LockName: "Timer", Type: "timer", LoadTime: cs.Time{Time: now}},
	}

	got, _, err := deckTarget(locks, "", "")
	if err != nil {
		t.Fatalf("deckTarget: %v", err)
	}
	if got == nil || got.ID != "new" {
		t.Fatalf("deckTarget = %+v, want the newest card lock", got)
	}
}

func TestDeckTargetRef(t *testing.T) {
	now := selectionTestNow()
	locks := []cs.LoadedLock{
		{ID: "card", LockName: "Card lock", Type: "card", Picks: 1, LoadTime: cs.Time{Time: now}},
		{ID: "timer", LockName: "Timer lock", Type: "timer", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
	}

	got, _, err := deckTarget(locks, "", "Card")
	if err != nil {
		t.Fatalf("deckTarget by name: %v", err)
	}
	if got == nil || got.ID != "card" {
		t.Fatalf("deckTarget by name = %+v", got)
	}

	if _, _, err := deckTarget(locks, "", "Timer"); err == nil {
		t.Fatal("a timer lock should be refused")
	}
}

func TestDeckTargetRefKeepsToOwnLocks(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "mine", LockName: "Mine", LockeeID: "me", Type: "card"},
		{ID: "theirs", LockName: "Theirs", LockeeID: "other", Type: "card"},
	}

	if _, _, err := deckTarget(locks, "me", "Theirs"); err == nil {
		t.Fatal("another member's lock should not resolve")
	}
	got, _, err := deckTarget(locks, "me", "Mine")
	if err != nil {
		t.Fatalf("deckTarget own lock: %v", err)
	}
	if got == nil || got.ID != "mine" {
		t.Fatalf("deckTarget own lock = %+v", got)
	}
}

func TestDeckIconsShowsFacesInOrder(t *testing.T) {
	lock := &cs.LoadedLock{Deck: cs.Deck{cs.CardRed: 1, cs.CardGreen: 2}}

	icons, hidden := deckIcons(lock)
	if hidden {
		t.Fatal("a visible deck should not report hidden faces")
	}
	want := []string{
		cardEmojiMarkup(cs.CardGreen),
		cardEmojiMarkup(cs.CardGreen),
		cardEmojiMarkup(cs.CardRed),
	}
	if len(icons) != len(want) {
		t.Fatalf("deckIcons = %d icons, want %d", len(icons), len(want))
	}
	for idx := range want {
		if icons[idx] != want[idx] {
			t.Errorf("icon %d = %q, want %q", idx, icons[idx], want[idx])
		}
	}
}

func TestDeckIconsUsesCardBackForUnknownCard(t *testing.T) {
	lock := &cs.LoadedLock{Deck: cs.Deck{cs.Card("MYSTERY"): 2, cs.CardGreen: 1}}

	icons, _ := deckIcons(lock)
	want := []string{cardEmojiMarkup(cs.CardGreen), deckCardBack(), deckCardBack()}
	if len(icons) != len(want) {
		t.Fatalf("deckIcons = %d icons, want %d", len(icons), len(want))
	}
	for idx := range want {
		if icons[idx] != want[idx] {
			t.Errorf("icon %d = %q, want %q", idx, icons[idx], want[idx])
		}
	}
}

func TestDeckIconsHiddenLockNeverRevealsFaces(t *testing.T) {
	lock := &cs.LoadedLock{
		Hidden: true,
		Deck:   cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}

	icons, hidden := deckIcons(lock)
	if !hidden {
		t.Fatal("a hidden lock should report hidden faces")
	}
	if len(icons) != 3 {
		t.Fatalf("deckIcons = %d icons, want 3", len(icons))
	}
	for idx, icon := range icons {
		if icon != deckCardBack() {
			t.Errorf("icon %d = %q, want the card back", idx, icon)
		}
	}
}

func TestDeckIconsUsesCardCountWhenDeckIsMissing(t *testing.T) {
	lock := &cs.LoadedLock{CardCount: 3}

	icons, hidden := deckIcons(lock)
	if !hidden {
		t.Fatal("an unknown deck should report hidden faces")
	}
	if len(icons) != 3 {
		t.Fatalf("deckIcons = %d icons, want 3", len(icons))
	}
	for idx, icon := range icons {
		if icon != deckCardBack() {
			t.Errorf("icon %d = %q, want the card back", idx, icon)
		}
	}
}

func TestDeckTextEmpty(t *testing.T) {
	lock := &cs.LoadedLock{LockName: "Empty", Deck: cs.Deck{}}
	got := deckText(lock, "")
	if !strings.Contains(got, "The deck is empty.") {
		t.Fatalf("deckText = %q", got)
	}

	lock.ReadyToUnlock = true
	got = deckText(lock, "")
	if !strings.Contains(got, "The lock is ready to unlock.") {
		t.Fatalf("ready deckText = %q", got)
	}
}

func TestDeckTextHiddenEmpty(t *testing.T) {
	lock := &cs.LoadedLock{LockName: "Secret", Hidden: true}
	got := deckText(lock, "")
	if !strings.Contains(got, "The deck is hidden.") {
		t.Fatalf("deckText = %q", got)
	}
}

func TestDeckTextWrapsRows(t *testing.T) {
	lock := &cs.LoadedLock{ID: "l1", LockName: "Deck", Deck: cs.Deck{cs.CardGreen: 12}}

	got := deckText(lock, "")
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("deckText has %d lines, want 5:\n%s", len(lines), got)
	}
	if count := strings.Count(lines[1], cardEmojiMarkup(cs.CardGreen)); count != deckRowCards {
		t.Errorf("first row has %d cards, want %d", count, deckRowCards)
	}
	if count := strings.Count(lines[2], cardEmojiMarkup(cs.CardGreen)); count != 2 {
		t.Errorf("second row has %d cards, want 2", count)
	}
	if !strings.Contains(lines[4], "12 cards left") {
		t.Errorf("summary = %q", lines[4])
	}
}

func TestDeckTextHiddenNote(t *testing.T) {
	lock := &cs.LoadedLock{LockName: "Secret", Hidden: true, CardCount: 2}

	got := deckText(lock, "")
	for _, want := range []string{"Secret", "2 cards left", "faces hidden"} {
		if !strings.Contains(got, want) {
			t.Errorf("deckText missing %q:\n%s", want, got)
		}
	}
}

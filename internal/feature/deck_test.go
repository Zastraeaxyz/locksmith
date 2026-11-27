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

func TestRevealedDrawsStopsAtReset(t *testing.T) {
	entries := []cs.LogEntry{
		{EventType: 6, Card: "r"},
		{EventType: 6, Card: "g"},
		{EventType: 39},
		{EventType: 6, Card: "y"},
	}

	got := revealedDraws(entries)
	want := []cs.Card{cs.CardGreen, cs.CardRed}
	if len(got) != len(want) {
		t.Fatalf("revealedDraws = %v, want %v", got, want)
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Errorf("card %d = %q, want %q", idx, got[idx], want[idx])
		}
	}
}

func TestRevealedDrawsSkipsOtherEvents(t *testing.T) {
	entries := []cs.LogEntry{
		{EventType: 6, Card: "r"},
		{EventType: 8},
		{EventType: 6, Card: "g"},
	}

	got := revealedDraws(entries)
	want := []cs.Card{cs.CardGreen, cs.CardRed}
	if len(got) != len(want) {
		t.Fatalf("revealedDraws = %v, want %v", got, want)
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Errorf("card %d = %q, want %q", idx, got[idx], want[idx])
		}
	}
}

func TestRevealedDrawsAfterReset(t *testing.T) {
	entries := []cs.LogEntry{
		{EventType: 9},
		{EventType: 6, Card: "g"},
	}

	if got := revealedDraws(entries); len(got) != 0 {
		t.Fatalf("revealedDraws after a reset = %v, want none", got)
	}
}

func TestDeckViewShowsRevealedAndVisibleDeck(t *testing.T) {
	lock := &cs.LoadedLock{
		Draws: cs.Draws("gr"),
		Deck:  cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}

	view := buildDeckView(lock, lock.Draws.Cards())
	if view.revealed != 2 || view.remaining != 3 || view.masked {
		t.Fatalf("view = %+v", view)
	}

	want := []string{
		cardEmojiMarkup(cs.CardGreen),
		cardEmojiMarkup(cs.CardRed),
		cardEmojiMarkup(cs.CardGreen),
		cardEmojiMarkup(cs.CardGreen),
		cardEmojiMarkup(cs.CardRed),
	}
	if len(view.icons) != len(want) {
		t.Fatalf("icons = %d, want %d", len(view.icons), len(want))
	}
	for idx := range want {
		if view.icons[idx] != want[idx] {
			t.Errorf("icon %d = %q, want %q", idx, view.icons[idx], want[idx])
		}
	}
}

func TestDeckViewHiddenDeckShowsCardBacks(t *testing.T) {
	lock := &cs.LoadedLock{
		Hidden: true,
		Draws:  cs.Draws("g"),
		Deck:   cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}

	view := buildDeckView(lock, lock.Draws.Cards())
	if view.revealed != 1 || view.remaining != 3 || !view.masked {
		t.Fatalf("view = %+v", view)
	}
	if len(view.icons) != 4 {
		t.Fatalf("icons = %d, want 4", len(view.icons))
	}
	if view.icons[0] != cardEmojiMarkup(cs.CardGreen) {
		t.Errorf("revealed icon = %q, want the drawn green", view.icons[0])
	}
	for idx := 1; idx < len(view.icons); idx++ {
		if view.icons[idx] != deckCardBack() {
			t.Errorf("icon %d = %q, want the card back", idx, view.icons[idx])
		}
	}
}

func TestDeckViewUnknownCardUsesCardBack(t *testing.T) {
	lock := &cs.LoadedLock{Deck: cs.Deck{cs.Card("MYSTERY"): 2, cs.CardGreen: 1}}

	view := buildDeckView(lock, nil)
	want := []string{cardEmojiMarkup(cs.CardGreen), deckCardBack(), deckCardBack()}
	if len(view.icons) != len(want) {
		t.Fatalf("icons = %d, want %d", len(view.icons), len(want))
	}
	for idx := range want {
		if view.icons[idx] != want[idx] {
			t.Errorf("icon %d = %q, want %q", idx, view.icons[idx], want[idx])
		}
	}
}

func TestDeckViewUsesCardCountWhenDeckIsMissing(t *testing.T) {
	lock := &cs.LoadedLock{CardCount: 3, Draws: cs.Draws("g")}

	view := buildDeckView(lock, lock.Draws.Cards())
	if view.revealed != 1 || view.remaining != 3 || !view.masked {
		t.Fatalf("view = %+v", view)
	}
	if len(view.icons) != 4 {
		t.Fatalf("icons = %d, want 4", len(view.icons))
	}
	if view.icons[0] != cardEmojiMarkup(cs.CardGreen) {
		t.Errorf("revealed icon = %q, want the drawn green", view.icons[0])
	}
}

func TestDeckTextEmpty(t *testing.T) {
	lock := &cs.LoadedLock{LockName: "Empty", Deck: cs.Deck{}}
	got := deckText(lock, "", nil)
	if !strings.Contains(got, "The deck is empty.") {
		t.Fatalf("deckText = %q", got)
	}

	lock.ReadyToUnlock = true
	got = deckText(lock, "", nil)
	if !strings.Contains(got, "The lock is ready to unlock.") {
		t.Fatalf("ready deckText = %q", got)
	}
}

func TestDeckTextHiddenEmpty(t *testing.T) {
	lock := &cs.LoadedLock{LockName: "Secret", Hidden: true}
	got := deckText(lock, "", nil)
	if !strings.Contains(got, "The deck is hidden.") {
		t.Fatalf("deckText = %q", got)
	}
}

func TestDeckTextWrapsRows(t *testing.T) {
	lock := &cs.LoadedLock{ID: "l1", LockName: "Deck", Draws: cs.Draws(strings.Repeat("g", 12))}

	got := deckText(lock, "", lock.Draws.Cards())
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
	if !strings.Contains(lines[4], "12 cards · 12 revealed · 0 left") {
		t.Errorf("summary = %q", lines[4])
	}
}

func TestDeckTextHiddenComposition(t *testing.T) {
	lock := &cs.LoadedLock{
		LockName: "Secret",
		Hidden:   true,
		Draws:    cs.Draws("g"),
		Deck:     cs.Deck{cs.CardRed: 2},
	}

	got := deckText(lock, "", lock.Draws.Cards())
	for _, want := range []string{
		"Secret",
		cardEmojiMarkup(cs.CardGreen),
		"3 cards · 1 revealed · 2 left",
		deckCardBack() + " = in deck",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("deckText missing %q:\n%s", want, got)
		}
	}
}

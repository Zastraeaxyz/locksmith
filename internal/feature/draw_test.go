package feature

import (
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestDrawSessionPermissions(t *testing.T) {
	session := &drawSession{ownerID: "owner", keyholderID: "keyholder"}
	if !session.canUse("owner") {
		t.Fatal("owner should be allowed")
	}
	if !session.canUse("keyholder") {
		t.Fatal("keyholder should be allowed")
	}
	if session.canUse("stranger") {
		t.Fatal("stranger should not be allowed")
	}
	if session.canUse("") {
		t.Fatal("empty user should not be allowed")
	}

	self := &drawSession{ownerID: "owner"}
	if self.canUse("keyholder") {
		t.Fatal("self lock should not allow an external keyholder")
	}
}

func TestRenderDrawLog(t *testing.T) {
	if got := renderDrawLog(nil); !strings.Contains(got, "No draws yet") {
		t.Fatalf("empty log = %q", got)
	}

	log := renderDrawLog([]drawEntry{
		{card: cs.CardGreen},
		{card: cs.CardRed, byKeyholder: true},
		{card: cs.CardDice, dice: 4},
	})
	for _, want := range []string{
		"1. Green card",
		"2. Red card · keyholder",
		"4",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}

func TestDrawEmbedRunning(t *testing.T) {
	lock := &cs.LoadedLock{
		Type:           "card",
		LockName:       "Test card",
		Picks:          2,
		MaxPicks:       3,
		Wait:           120,
		GreensRequired: 10000,
		Deck:           cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}
	session := &drawSession{
		ownerName:     "Zastraea",
		keyholderID:   "kh",
		keyholderName: "Raven",
		lockName:      "Test card",
	}
	session.setLock(lock)

	embed := session.embed(lock, "A note.", false)
	if embed.Title != "Test card" {
		t.Fatalf("title = %q", embed.Title)
	}
	if embed.Author != nil {
		t.Fatalf("author should be omitted: %+v", embed.Author)
	}
	if embed.Footer != nil {
		t.Fatalf("footer should be omitted: %+v", embed.Footer)
	}
	if len(embed.Fields) != 0 {
		t.Fatalf("fields should be empty: %d", len(embed.Fields))
	}
	for _, want := range []string{"0 used · 2 left", "A note."} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description missing %q:\n%s", want, embed.Description)
		}
	}
	if strings.Contains(embed.Description, "Next draw") {
		t.Errorf("running board should not show a next draw:\n%s", embed.Description)
	}
}

func TestDrawEmbedFinishedSummary(t *testing.T) {
	lock := &cs.LoadedLock{
		Type:     "card",
		LockName: "Test card",
		Picks:    0,
		Wait:     3271,
	}
	session := &drawSession{lockName: "Test card"}
	session.setLock(lock)
	session.add(drawEntry{card: cs.CardYellowP3})
	session.add(drawEntry{card: cs.CardRed})
	session.add(drawEntry{card: cs.CardDoubleUp})

	embed := session.embed(lock, "", true)
	for _, want := range []string{
		"1. Yellow +3",
		"2. Red card",
		"3. Double up card",
		"3 used · 0 left",
		"Next draw <t:",
		"Totals: 1 red, 0 green, 1 yellow, 1 double up",
	} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description missing %q:\n%s", want, embed.Description)
		}
	}
	if strings.Contains(embed.Description, "- yellow +3, red") {
		t.Errorf("finished board should not repeat the draws as a list:\n%s", embed.Description)
	}
}

func TestTotalsLineOmitsMissingCards(t *testing.T) {
	got := totalsLine([]drawEntry{{card: cs.CardGreen}, {card: cs.CardGreen}})
	if got != "Totals: 0 red, 2 green" {
		t.Fatalf("totalsLine = %q", got)
	}
}

func TestCardLogLabel(t *testing.T) {
	tests := map[cs.Card]string{
		cs.CardYellowP1: "Yellow +1",
		cs.CardYellowP2: "Yellow +2",
		cs.CardYellowP3: "Yellow +3",
		cs.CardYellowM1: "Yellow -1",
		cs.CardYellowM2: "Yellow -2",
		cs.CardYellowRN: "Yellow +?",
		cs.CardYellowUK: "Yellow +?",
		cs.CardGreen:    "Green card",
		cs.CardRed:      "Red card",
	}
	for card, want := range tests {
		if got := cardLogLabel(card); got != want {
			t.Errorf("cardLogLabel(%q) = %q, want %q", card, got, want)
		}
	}
}

func TestCardColor(t *testing.T) {
	if cardColor(cs.CardGreen) == cardColor(cs.CardRed) {
		t.Fatal("green and red should differ")
	}
	if cardColor("") == 0 {
		t.Fatal("default color should not be zero")
	}
}

func TestDrawBlockedMessage(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	if got := drawBlockedMessage(nil, nil, now); !strings.Contains(got, "no active card lock") {
		t.Fatalf("nil message = %q", got)
	}

	frozen := &cs.LoadedLock{ID: "f", LockName: "Frozen Lock", Type: "card", FrozenByKeyholder: true}
	got := drawBlockedMessage(frozen, map[cs.ID]time.Time{"f": now.Add(-time.Hour)}, now)
	for _, want := range []string{"Frozen Lock", "frozen since <t:", ":R>", "unfrozen"} {
		if !strings.Contains(got, want) {
			t.Errorf("frozen message missing %q:\n%s", want, got)
		}
	}

	got = drawBlockedMessage(frozen, nil, now)
	if strings.Contains(got, "since") {
		t.Errorf("frozen message without history should not claim a since time:\n%s", got)
	}

	ready := &cs.LoadedLock{ID: "r", LockName: "Ready Lock", Type: "card", ReadyToUnlock: true}
	if got := drawBlockedMessage(ready, nil, now); !strings.Contains(got, "ready to unlock") {
		t.Errorf("ready message = %q", got)
	}

	waiting := &cs.LoadedLock{ID: "w", LockName: "Waiting Lock", Type: "card", Picks: 0, Wait: 300}
	if got := drawBlockedMessage(waiting, nil, now); !strings.Contains(got, "Next draw <t:") {
		t.Errorf("waiting message = %q", got)
	}

	waiting.Wait = 0
	if got := drawBlockedMessage(waiting, nil, now); !strings.Contains(got, "No draws available right now") {
		t.Errorf("waiting without timer message = %q", got)
	}

	drawable := &cs.LoadedLock{ID: "d", LockName: "Drawable", Type: "card", Picks: 1}
	if got := drawBlockedMessage(drawable, nil, now); got != "" {
		t.Errorf("drawable message = %q, want empty", got)
	}
}

func TestDrawTargetNewest(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "old", LockName: "Old", Type: "card", LockeeID: "viewer", LoadTime: at(-2 * time.Hour)},
		{ID: "new", LockName: "New", Type: "card", LockeeID: "viewer", LoadTime: at(-time.Hour)},
		{ID: "timer", LockName: "Timer", Type: "timer", LockeeID: "viewer", LoadTime: at(-30 * time.Minute)},
		{ID: "other", LockName: "Other", Type: "card", LockeeID: "someone", LoadTime: at(-10 * time.Minute)},
	}

	got := drawTarget(locks, "viewer")
	if got == nil || got.ID != "new" {
		t.Fatalf("drawTarget = %+v, want newest card lock", got)
	}
}

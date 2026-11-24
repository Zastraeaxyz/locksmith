package feature

import (
	"strings"
	"testing"

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

	embed := session.embed(lock, "", true)
	for _, want := range []string{
		"1. Yellow +3",
		"2. Red card",
		"2 used · 0 left",
		"Next draw <t:",
		"- yellow +3, red",
		"Totals: 1 red, 0 green",
	} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description missing %q:\n%s", want, embed.Description)
		}
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

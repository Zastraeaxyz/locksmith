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

func TestDrawEmbed(t *testing.T) {
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

	embed := session.embed(lock, "A note.")
	if embed.Title != "Test card" {
		t.Fatalf("title = %q", embed.Title)
	}
	if embed.Author == nil || !strings.Contains(embed.Author.Name, "Zastraea") {
		t.Fatalf("author = %+v", embed.Author)
	}
	if embed.Footer == nil || !strings.Contains(embed.Footer.Text, "Raven") {
		t.Fatalf("footer = %+v", embed.Footer)
	}
	if !strings.Contains(embed.Description, "A note.") {
		t.Fatalf("description = %q", embed.Description)
	}
	if len(embed.Fields) != 4 {
		t.Fatalf("fields = %d", len(embed.Fields))
	}

	values := map[string]string{}
	for _, field := range embed.Fields {
		values[field.Name] = field.Value
	}
	if values["Draws available"] != "2 of 3" {
		t.Errorf("draws = %q", values["Draws available"])
	}
	if values["Cards left"] != "3" {
		t.Errorf("cards = %q", values["Cards left"])
	}
	if values["Greens needed"] != "2" {
		t.Errorf("greens = %q", values["Greens needed"])
	}
	if values["Next draw"] != "in 2m" {
		t.Errorf("next = %q", values["Next draw"])
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

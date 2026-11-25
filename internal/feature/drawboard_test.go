package feature

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestBoardKeepsOpen(t *testing.T) {
	cases := []struct {
		name string
		lock *cs.LoadedLock
		want bool
	}{
		{"no lock", nil, false},
		{"ready", &cs.LoadedLock{Type: "card", ReadyToUnlock: true}, true},
		{"ready and frozen keeps the ready actions", &cs.LoadedLock{
			Type: "card", ReadyToUnlock: true, FrozenByCard: true,
		}, true},
		{"draws available", &cs.LoadedLock{Type: "card", Picks: 1, MaxPicks: 2, GreensRequired: 1}, true},
		{"no draws available", &cs.LoadedLock{Type: "card", Picks: 0, MaxPicks: 1, GreensRequired: 1}, false},
		{"frozen", &cs.LoadedLock{
			Type: "card", Picks: 1, MaxPicks: 2, GreensRequired: 1, FrozenByCard: true,
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := boardKeepsOpen(tc.lock); got != tc.want {
				t.Fatalf("boardKeepsOpen = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBoardViewRendersButtonsOnlyWhenAsked(t *testing.T) {
	session := &drawSession{id: "s1", ownerName: "Zastraea", lockName: "Test card"}
	lock := &cs.LoadedLock{
		Type: "card", LockName: "Test card", Picks: 0, MaxPicks: 2,
		GreensRequired: 1, Deck: cs.Deck{cs.CardGreen: 1, cs.CardRed: 1},
	}

	embeds, components := boardView(session, lock, true, "A note.")
	if len(embeds) != 1 || embeds[0].Title != "Test card" {
		t.Fatalf("embeds = %+v", embeds)
	}
	if !strings.Contains(embeds[0].Description, "A note.") {
		t.Fatalf("description = %q", embeds[0].Description)
	}
	if len(components) != 1 {
		t.Fatalf("components = %+v", components)
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok || len(row.Components) == 0 {
		t.Fatalf("component row = %+v", components[0])
	}

	_, none := boardView(session, lock, false, "")
	if len(none) != 0 {
		t.Fatalf("components without buttons = %+v", none)
	}
}

func TestOpenBoardNoteMatchesReplyVisibility(t *testing.T) {
	guild := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "guild-1"}}
	dm := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}}

	if note := openBoardNote(guild); !strings.Contains(note, "privately") {
		t.Fatalf("guild note = %q", note)
	}
	if note := openBoardNote(dm); !strings.Contains(note, "below") {
		t.Fatalf("DM note = %q", note)
	}
}

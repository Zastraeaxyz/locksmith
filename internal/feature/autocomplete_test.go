package feature

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestFocusedStringValue(t *testing.T) {
	options := []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "public", Type: discordgo.ApplicationCommandOptionBoolean},
		{Name: "ref", Type: discordgo.ApplicationCommandOptionString, Value: "quiet", Focused: true},
	}
	if got, ok := focusedStringValue(options); !ok || got != "quiet" {
		t.Fatalf("focused value = %q, %v", got, ok)
	}

	nested := []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "share", Type: discordgo.ApplicationCommandOptionSubCommand, Options: options},
	}
	if got, ok := focusedStringValue(nested); !ok || got != "quiet" {
		t.Fatalf("nested value = %q, %v", got, ok)
	}

	if got, ok := focusedStringValue(nil); ok || got != "" {
		t.Fatalf("nil value = %q, %v", got, ok)
	}

	// A focused non-string option must not panic on StringValue.
	boolFocused := []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "public", Type: discordgo.ApplicationCommandOptionBoolean, Focused: true},
	}
	if got, ok := focusedStringValue(boolFocused); ok || got != "" {
		t.Fatalf("bool value = %q, %v", got, ok)
	}
}

func TestBuildRefChoices(t *testing.T) {
	candidates := []refChoice{
		{id: "1", name: "Quiet Week"},
		{id: "2", name: "Quiet Month"},
		{id: "3", name: "Test Run"},
	}

	cases := []struct {
		name string
		ref  string
		want []string
	}{
		{name: "empty", ref: "", want: []string{"1", "2", "3"}},
		{name: "name substring", ref: "quiet", want: []string{"1", "2"}},
		{name: "case insensitive", ref: "TEST", want: []string{"3"}},
		{name: "id prefix", ref: "2", want: []string{"2"}},
		{name: "no match", ref: "nope", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := choiceValues(buildRefChoices(tc.ref, candidates))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("values = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildRefChoicesDedupesAndDisambiguates(t *testing.T) {
	candidates := []refChoice{
		{id: "1", name: "Same"},
		{id: "1", name: "Same duplicate"},
		{id: "2", name: "Same"},
	}
	choices := buildRefChoices("", candidates)
	if len(choices) != 2 {
		t.Fatalf("choices = %d, want 2", len(choices))
	}
	if choices[0].Name != "Same (1)" || choices[1].Name != "Same (2)" {
		t.Fatalf("names = %q, %q", choices[0].Name, choices[1].Name)
	}
}

func TestBuildRefChoicesLimitAndTruncate(t *testing.T) {
	candidates := make([]refChoice, 0, 30)
	for i := 0; i < 30; i++ {
		candidates = append(candidates, refChoice{
			id:   cs.ID(strconv.Itoa(i)),
			name: strings.Repeat("n", 150),
		})
	}
	choices := buildRefChoices("", candidates)
	if len(choices) != autocompleteChoiceLimit {
		t.Fatalf("choices = %d, want %d", len(choices), autocompleteChoiceLimit)
	}
	if len([]rune(choices[0].Name)) != 100 {
		t.Fatalf("name length = %d, want 100", len([]rune(choices[0].Name)))
	}
}

func TestShareCandidates(t *testing.T) {
	templates := []cs.Template{
		{ID: "mine", LockName: "Mine", KeyholderID: "me"},
		{ID: "other", LockName: "Other", KeyholderID: "them"},
		{ID: "off", LockName: "Off", KeyholderID: "me", Status: "DISABLED"},
		{ID: "self", LockName: "Self"},
	}
	got := refChoiceIDs(shareCandidates(templates, "me"))
	if want := "mine,self"; got != want {
		t.Fatalf("ids = %q, want %q", got, want)
	}
}

func TestCheckinCandidates(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	locks := []cs.LoadedLock{
		{ID: "no-checkin", LockName: "No", LockeeID: "me"},
		{ID: "ready", LockName: "Ready", LockeeID: "me", CheckInPeriod: 60, ReadyToUnlock: true},
		{ID: "other", LockName: "Other", LockeeID: "them", CheckInPeriod: 60},
		{ID: "older", LockName: "Older", LockeeID: "me", CheckInPeriod: 60, LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "newer", LockName: "Newer", LockeeID: "me", CheckInPeriod: 60, LoadTime: cs.Time{Time: now}},
	}
	got := refChoiceIDs(checkinCandidates(locks, "me"))
	if want := "newer,older"; got != want {
		t.Fatalf("ids = %q, want %q", got, want)
	}
}

func TestLogCandidates(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	running := []cs.LoadedLock{
		{ID: "active-old", LockName: "Active", LockeeID: "me", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "active-new", LockName: "Active", LockeeID: "me", LoadTime: cs.Time{Time: now}},
		{ID: "other", LockName: "Other", LockeeID: "them"},
	}
	recent := []cs.RecentLock{
		{LockID: "prev-old", LockName: "Previous", UnlockTime: cs.Time{Time: now.Add(-time.Hour)}},
		{LockID: "prev-new", LockName: "Previous", UnlockTime: cs.Time{Time: now}},
	}
	got := refChoiceIDs(logCandidates(running, recent, "me"))
	if want := "active-new,active-old,prev-new,prev-old"; got != want {
		t.Fatalf("ids = %q, want %q", got, want)
	}
}

func TestRefOptionsAutocomplete(t *testing.T) {
	subcommands := map[string]*discordgo.ApplicationCommandOption{}
	for _, option := range lockCommand().Options {
		subcommands[option.Name] = option
	}

	for name, want := range map[string]bool{"checkin": true, "share": true, "log": true, "deck": true} {
		sub := subcommands[name]
		if sub == nil {
			t.Fatalf("missing /lock %s", name)
		}
		var ref *discordgo.ApplicationCommandOption
		for _, option := range sub.Options {
			if option.Name == "ref" {
				ref = option
			}
		}
		if ref == nil {
			t.Fatalf("/lock %s has no ref option", name)
		}
		if ref.Autocomplete != want {
			t.Errorf("/lock %s ref.Autocomplete = %v, want %v", name, ref.Autocomplete, want)
		}
	}
}

func TestDeckCandidates(t *testing.T) {
	now := selectionTestNow()
	locks := []cs.LoadedLock{
		{ID: "old", LockName: "Old", LockeeID: "me", Type: "card", LoadTime: cs.Time{Time: now.Add(-2 * time.Hour)}},
		{ID: "new", LockName: "New", LockeeID: "me", Type: "card", LoadTime: cs.Time{Time: now}},
		{ID: "timer", LockName: "Timer", LockeeID: "me", Type: "timer", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "theirs", LockName: "Theirs", LockeeID: "other", Type: "card", LoadTime: cs.Time{Time: now}},
	}

	got := refChoiceIDs(deckCandidates(locks, "me"))
	if want := "new,old"; got != want {
		t.Fatalf("ids = %q, want %q", got, want)
	}
}

func choiceValues(choices []*discordgo.ApplicationCommandOptionChoice) []string {
	out := make([]string, 0, len(choices))
	for _, choice := range choices {
		out = append(out, choice.Value.(string))
	}
	return out
}

func refChoiceIDs(candidates []refChoice) string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.id.String())
	}
	return strings.Join(ids, ",")
}

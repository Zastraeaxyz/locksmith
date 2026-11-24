package feature

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestResolveShareTemplate(t *testing.T) {
	templates := []cs.Template{
		{ID: "tpl-1", LockName: "Quiet Week"},
		{ID: "tpl-2", LockName: "Quiet Month"},
		{ID: "tpl-3", LockName: "Test Run"},
	}

	cases := []struct {
		name string
		ref  string
		want cs.ID
		bad  string
	}{
		{name: "exact id", ref: "tpl-2", want: "tpl-2"},
		{name: "exact name", ref: "quiet month", want: "tpl-2"},
		{name: "prefix", ref: "quiet", bad: "matches several"},
		{name: "unique prefix", ref: "test", want: "tpl-3"},
		{name: "missing", ref: "nope", bad: "No lock template"},
		{name: "empty", ref: "  ", bad: "Choose a lock template"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveShareTemplate(tc.ref, templates)
			if tc.bad != "" {
				if err == nil || !strings.Contains(err.Error(), tc.bad) {
					t.Fatalf("error = %v, want %q", err, tc.bad)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveShareTemplate(%q): %v", tc.ref, err)
			}
			if got == nil || got.ID != tc.want {
				t.Fatalf("got %+v, want %q", got, tc.want)
			}
		})
	}
}

func TestShareableTemplate(t *testing.T) {
	if err := shareableTemplate(nil, "me"); err == nil {
		t.Fatal("nil template should fail")
	}
	disabled := &cs.Template{ID: "tpl-1", LockName: "Off", Status: "DISABLED"}
	if err := shareableTemplate(disabled, "me"); err == nil {
		t.Fatal("disabled template should fail")
	}
	other := &cs.Template{ID: "tpl-1", LockName: "Other", KeyholderID: "someone-else"}
	if err := shareableTemplate(other, "me"); err == nil {
		t.Fatal("another keyholder's template should fail")
	}
	ok := &cs.Template{ID: "tpl-1", LockName: "Mine", KeyholderID: "me"}
	if err := shareableTemplate(ok, "me"); err != nil {
		t.Fatalf("own template: %v", err)
	}
}

func TestShareEmbed(t *testing.T) {
	template := &cs.Template{
		ID:              "tpl-1",
		LockName:        "Test Lock",
		Keyholder:       "Keyholder",
		KeyholderID:     "kh",
		LockType:        "CARDLOCK",
		Cards:           []cs.TemplateCard{{Name: cs.CardGreen, Min: 2, Max: 4}},
		RequiredGreens:  2,
		Regularity:      60,
		CheckInPeriod:   1440,
		AllowedPurposes: cs.PurposeMaskChastity | cs.PurposeMaskTest,
		Passcode:        "yes",
		Description:     "A lock for testing.",
	}
	embed := shareEmbed(template, "https://chastisafe.com/api-v1", "sharer")
	if embed.Title != "Test Lock" {
		t.Fatalf("title = %q", embed.Title)
	}
	if embed.URL != "https://chastisafe.com/#l/tpl-1" {
		t.Fatalf("url = %q", embed.URL)
	}
	for _, want := range []string{
		"Keyholder: **Keyholder**",
		"Cards: green 2-4",
		"Greens needed: 2",
		"Draws: every 1 hour",
		"Check-ins: every 1 day",
		"Passcode: required",
		"Load with Locksmith",
		"Open in ChastiSafe",
		"A lock for testing.",
	} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description missing %q:\n%s", want, embed.Description)
		}
	}
	if embed.Footer == nil || !strings.Contains(embed.Footer.Text, "tpl-1") {
		t.Fatalf("footer = %+v", embed.Footer)
	}
	if len([]rune(embed.Description)) > 4096 {
		t.Fatalf("description too long: %d", len([]rune(embed.Description)))
	}
}

func TestShareButtons(t *testing.T) {
	components := shareButtons("tpl-1", "https://chastisafe.com/api-v1")
	if len(components) != 1 {
		t.Fatalf("components = %d, want 1 row", len(components))
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component type = %T", components[0])
	}
	if len(row.Components) != 2 {
		t.Fatalf("buttons = %d, want 2", len(row.Components))
	}

	load, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("first button type = %T", row.Components[0])
	}
	if load.CustomID != "share:load:tpl-1" || load.Style != discordgo.PrimaryButton {
		t.Fatalf("load button = %+v", load)
	}

	open, ok := row.Components[1].(discordgo.Button)
	if !ok {
		t.Fatalf("second button type = %T", row.Components[1])
	}
	if open.Style != discordgo.LinkButton || open.URL != "https://chastisafe.com/#l/tpl-1" {
		t.Fatalf("open button = %+v", open)
	}
}

func TestTemplateCardsText(t *testing.T) {
	cards := []cs.TemplateCard{
		{Name: cs.CardGreen, Min: 2, Max: 4},
		{Name: cs.CardRed, Min: 3, Max: 3},
	}
	if got := templateCardsText(cards); got != "green 2-4, red 3" {
		t.Fatalf("cards = %q", got)
	}
	if got := templateCardsText(nil); got != "" {
		t.Fatalf("empty cards = %q", got)
	}
	if got := greensText(1001); got != "all greens" {
		t.Fatalf("all greens = %q", got)
	}
	if got := greensText(2); got != "2" {
		t.Fatalf("greens = %q", got)
	}
}

func TestAppAvailableFor(t *testing.T) {
	presser := "presser"
	interaction := func(owners map[discordgo.ApplicationIntegrationType]string) *discordgo.InteractionCreate {
		return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			User:                         &discordgo.User{ID: presser},
			AuthorizingIntegrationOwners: owners,
		}}
	}

	if !appAvailableFor(nil) {
		t.Error("nil interaction should be treated as available")
	}
	if !appAvailableFor(interaction(nil)) {
		t.Error("missing owners should be treated as available")
	}
	if !appAvailableFor(interaction(map[discordgo.ApplicationIntegrationType]string{
		discordgo.ApplicationIntegrationGuildInstall: "guild",
	})) {
		t.Error("guild install should be available")
	}
	if !appAvailableFor(interaction(map[discordgo.ApplicationIntegrationType]string{
		discordgo.ApplicationIntegrationUserInstall: presser,
	})) {
		t.Error("presser's own user install should be available")
	}
	if appAvailableFor(interaction(map[discordgo.ApplicationIntegrationType]string{
		discordgo.ApplicationIntegrationUserInstall: "someone-else",
	})) {
		t.Error("another user's install should not be available")
	}
}

package feature

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestAboutCommand(t *testing.T) {
	cmd := aboutCommand()
	if cmd.Name != "about" {
		t.Fatalf("name = %q", cmd.Name)
	}
	if strings.TrimSpace(cmd.Description) == "" {
		t.Fatal("description is empty")
	}
	if len(cmd.Options) != 0 {
		t.Fatalf("options = %d, want none", len(cmd.Options))
	}
}

func TestAboutEmbed(t *testing.T) {
	embed := aboutEmbed()
	if embed.Title != "Locksmith" {
		t.Fatalf("title = %q", embed.Title)
	}
	if embed.URL != repoURL {
		t.Fatalf("url = %q, want %q", embed.URL, repoURL)
	}
	if embed.Color != embedColor {
		t.Fatalf("color = %#x", embed.Color)
	}

	for _, want := range []string{
		"Your ChastiSafe locks, inside Discord.",
		"/login",
		"/lock load",
		"/lock draw",
		"encrypted",
	} {
		if !strings.Contains(embed.Description+aboutFieldText(embed), want) {
			t.Errorf("about text missing %q", want)
		}
	}
	if embed.Footer == nil || !strings.Contains(embed.Footer.Text, "Browse docs") {
		t.Fatalf("footer = %+v", embed.Footer)
	}
	if len([]rune(embed.Description)) > 4096 {
		t.Fatalf("description too long: %d", len([]rune(embed.Description)))
	}

	total := len([]rune(embed.Description))
	for _, field := range embed.Fields {
		if len([]rune(field.Name)) > 256 {
			t.Errorf("field %q: name too long", field.Name)
		}
		if len([]rune(field.Value)) > 1024 {
			t.Errorf("field %q: value too long: %d", field.Name, len([]rune(field.Value)))
		}
		total += len([]rune(field.Name)) + len([]rune(field.Value))
	}
	if total > 6000 {
		t.Fatalf("embed text too long: %d", total)
	}
}

// aboutFieldText flattens the field bodies so the copy assertions can check the
// whole embed as one string.
func aboutFieldText(embed *discordgo.MessageEmbed) string {
	var b strings.Builder
	for _, field := range embed.Fields {
		b.WriteString(field.Name)
		b.WriteString(" ")
		b.WriteString(field.Value)
		b.WriteString("\n")
	}
	return b.String()
}

func TestAboutButtons(t *testing.T) {
	components := aboutButtons("123")
	if len(components) != 1 {
		t.Fatalf("components = %d, want 1 row", len(components))
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component type = %T", components[0])
	}
	if len(row.Components) != 4 {
		t.Fatalf("buttons = %d, want 4", len(row.Components))
	}

	labels := make(map[string]discordgo.Button)
	for _, component := range row.Components {
		button, ok := component.(discordgo.Button)
		if !ok {
			t.Fatalf("button type = %T", component)
		}
		labels[button.Label] = button
	}

	if got := labels["Install to your account"].URL; got != userInstallURL("123") {
		t.Errorf("install url = %q", got)
	}
	if got := labels["ChastiSafe"].URL; got != chastisafeURL {
		t.Errorf("chastisafe url = %q", got)
	}
	if got := labels["Repository"].URL; got != repoURL {
		t.Errorf("repo url = %q", got)
	}

	docsButton, ok := labels["Browse docs"]
	if !ok {
		t.Fatal("missing Browse docs button")
	}
	if docsButton.Style != discordgo.SecondaryButton {
		t.Errorf("Browse docs style = %v, want secondary", docsButton.Style)
	}
	if docsButton.CustomID != aboutCustomID {
		t.Errorf("Browse docs custom ID = %q, want %q", docsButton.CustomID, aboutCustomID)
	}
	// The docs module must not claim this button, or the press opens an
	// out-of-date page instead of the index.
	if _, ok := docsCustomIDKey(docsButton.CustomID); ok {
		t.Errorf("docs handler claims %q", docsButton.CustomID)
	}
}

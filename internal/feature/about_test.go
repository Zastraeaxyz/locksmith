package feature

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestLocksmithCommand(t *testing.T) {
	cmd := locksmithCommand()
	if cmd.Name != "locksmith" {
		t.Fatalf("name = %q", cmd.Name)
	}
	if strings.TrimSpace(cmd.Description) == "" {
		t.Fatal("description is empty")
	}
}

func TestLocksmithEmbed(t *testing.T) {
	embed := locksmithEmbed("123")
	if embed.Title != "Locksmith" {
		t.Fatalf("title = %q", embed.Title)
	}
	if embed.URL != repoURL {
		t.Fatalf("url = %q, want %q", embed.URL, repoURL)
	}
	if embed.Color != aboutColor {
		t.Fatalf("color = %#x", embed.Color)
	}

	for _, want := range []string{
		"/lock status",
		"/lock draw",
		"/lock notify",
		"/lock log",
		"chastisafe.com",
		"group DMs",
	} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description missing %q:\n%s", want, embed.Description)
		}
	}
	if embed.Footer == nil || !strings.Contains(embed.Footer.Text, "Work in progress") {
		t.Fatalf("footer = %+v", embed.Footer)
	}
	if len([]rune(embed.Description)) > 4096 {
		t.Fatalf("description too long: %d", len([]rune(embed.Description)))
	}
}

func TestUserInstallURL(t *testing.T) {
	got := userInstallURL("123")
	want := "https://discord.com/oauth2/authorize?client_id=123&integration_type=1&scope=applications.commands"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if !strings.Contains(userInstallURL(""), defaultClientID) {
		t.Fatalf("empty client ID should fall back to %q", defaultClientID)
	}
}

func TestLocksmithButtons(t *testing.T) {
	components := locksmithButtons("123")
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

	labels := make(map[string]string)
	for _, component := range row.Components {
		button, ok := component.(discordgo.Button)
		if !ok {
			t.Fatalf("button type = %T", component)
		}
		if button.Style != discordgo.LinkButton {
			t.Errorf("%q is not a link button", button.Label)
		}
		if !strings.HasPrefix(button.URL, "https://") {
			t.Errorf("%q has url %q", button.Label, button.URL)
		}
		labels[button.Label] = button.URL
	}

	if got := labels["Install to your account"]; got != userInstallURL("123") {
		t.Errorf("install url = %q", got)
	}
	if got := labels["Repository"]; got != repoURL {
		t.Errorf("repo url = %q", got)
	}
}

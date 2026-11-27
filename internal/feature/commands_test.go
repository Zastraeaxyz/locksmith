package feature

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestLoginFailureReason(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errNotOwner, "not owner"},
		{cs.ErrUnauthorized, "rejected"},
		{errNotConfigured, "unconfigured"},
		{errors.New("network down"), "unreachable"},
		{fmt.Errorf("wrapped: %w", cs.ErrUnauthorized), "rejected"},
	}
	for _, tc := range cases {
		if got := loginFailureReason(tc.err); got != tc.want {
			t.Errorf("loginFailureReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestLoginWelcomeText(t *testing.T) {
	text := loginWelcomeText(&cs.Identity{User: "zastraea", ID: "123"})
	for _, want := range []string{
		"Logged in as **zastraea**",
		"`/lock status`",
		"set your device to the code I reply with",
		"`/lock draw`",
		"`/lock checkin [ref]`",
		"`/lock rate`",
		"`/lock share ref:<template>`",
		"`/lock notify [enabled]`",
		"`/lock log [full] [ref]`",
		"`/lock deck [ref]`",
		"`/history`",
		"`/docs`",
		"`/help`",
		"/lock load link:https://chastisafe.com/#l/1234",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("welcome text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "/locksmith") {
		t.Errorf("welcome text still mentions /locksmith:\n%s", text)
	}
	if len([]rune(text)) > 2000 {
		t.Fatalf("welcome text too long: %d", len([]rune(text)))
	}
	if got := loginWelcomeText(nil); !strings.Contains(got, "Logged in as your ChastiSafe account") {
		t.Fatalf("nil identity = %q", got)
	}
}

func TestLockDrawHasAutoOption(t *testing.T) {
	var draw *discordgo.ApplicationCommandOption
	for _, option := range lockCommand().Options {
		if option.Name == "draw" {
			draw = option
			break
		}
	}
	if draw == nil {
		t.Fatal("lock command has no draw subcommand")
	}
	if draw.Description == "" {
		t.Fatal("draw subcommand has no description")
	}

	for _, option := range draw.Options {
		if option.Name != "auto" {
			continue
		}
		if option.Type != discordgo.ApplicationCommandOptionBoolean {
			t.Fatalf("auto option type = %v, want boolean", option.Type)
		}
		if option.Required {
			t.Fatal("auto option should be optional")
		}
		return
	}
	t.Fatal("draw subcommand has no auto option")
}

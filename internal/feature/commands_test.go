package feature

import (
	"errors"
	"fmt"
	"strings"
	"testing"

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
		"`/history`",
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

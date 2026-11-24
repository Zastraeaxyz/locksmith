package feature

import (
	"context"
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestParseTemplateRef(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want cs.ID
		bad  bool
	}{
		{name: "share url", in: "https://chastisafe.com/#l/1234", want: "1234"},
		{name: "template url", in: "https://chastisafe.com/#lock/1234", want: "1234"},
		{name: "angle brackets", in: "<https://chastisafe.com/#l/1234>", want: "1234"},
		{name: "fragment only", in: "#l/1234", want: "1234"},
		{name: "bare id", in: "1234", want: "1234"},
		{name: "query suffix", in: "https://chastisafe.com/#l/1234?from=discord", want: "1234"},
		{name: "trailing slash", in: "https://chastisafe.com/#l/1234/", want: "1234"},
		{name: "letters and dashes", in: "https://example.com/#l/abc-1_2", want: "abc-1_2"},
		{name: "running lock link", in: "https://chastisafe.com/#loaded/1234", bad: true},
		{name: "empty fragment", in: "https://chastisafe.com/#l/", bad: true},
		{name: "plain text", in: "not a link", bad: true},
		{name: "empty", in: "  ", bad: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTemplateRef(tc.in)
			if tc.bad {
				if err == nil {
					t.Fatalf("parseTemplateRef(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTemplateRef(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("parseTemplateRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestChastisafeShareURL(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{base: "", want: "https://chastisafe.com/#l/1234"},
		{base: "https://chastisafe.com/api-v1", want: "https://chastisafe.com/#l/1234"},
		{base: "http://localhost:8080/api-v1", want: "http://localhost:8080/#l/1234"},
		{base: "not a url", want: "https://chastisafe.com/#l/1234"},
	}
	for _, tc := range cases {
		if got := chastisafeShareURL(tc.base, "1234"); got != tc.want {
			t.Errorf("chastisafeShareURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestNewLockCode(t *testing.T) {
	code := newLockCode()
	if len(code) != 4 {
		t.Fatalf("code = %q, want 4 digits", code)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("code = %q, want digits only", code)
		}
	}
}

func TestDefaultPurpose(t *testing.T) {
	if got := defaultPurpose(nil); got != cs.PurposeChastity {
		t.Errorf("nil = %q, want CHASTITY", got)
	}
	if got := defaultPurpose(&cs.Template{}); got != cs.PurposeChastity {
		t.Errorf("no mask = %q, want CHASTITY", got)
	}
	allowed := &cs.Template{AllowedPurposes: cs.PurposeMaskTest | cs.PurposeMaskChastity}
	if got := defaultPurpose(allowed); got != cs.PurposeChastity {
		t.Errorf("chastity allowed = %q, want CHASTITY", got)
	}
	noChastity := &cs.Template{AllowedPurposes: cs.PurposeMaskTest | cs.PurposeMaskBondage}
	if got := defaultPurpose(noChastity); got != cs.PurposeTest {
		t.Errorf("no chastity = %q, want TEST", got)
	}
}

func TestLoadFailureReason(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{err: &cs.APIError{Reason: "nodoublelocking"}, want: "double locking"},
		{err: &cs.APIError{Reason: "requestedpurposenotallowed"}, want: "purpose not allowed"},
		{err: &cs.APIError{Reason: "locknotavailable"}, want: "lock unavailable"},
		{err: &cs.APIError{Reason: "passcodemismatch"}, want: "passcode mismatch"},
		{err: cs.ErrLockNotFound, want: "not found"},
		{err: cs.ErrUnauthorized, want: "rejected"},
		{err: cs.ErrInvalidResponse, want: "unreachable"},
	}
	for _, tc := range cases {
		if got := loadFailureReason(tc.err); got != tc.want {
			t.Errorf("loadFailureReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestLoadResultText(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	lock := &cs.LoadedLock{
		ID:                "lock-1",
		LockName:          "Test lock",
		Type:              "card",
		LockeeID:          "me",
		Keyholder:         "Keyholder",
		KeyholderID:       "kh",
		CheckInPeriod:     1440,
		TimeToNextCheckIn: 3600,
		Picks:             2,
		MaxPicks:          5,
	}
	text := loadResultText(lock, "me", now)
	for _, want := range []string{"Loaded **Test lock**", "/lock draw", "/lock checkin"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}

func TestLoadFailurePasscode(t *testing.T) {
	f := &Feature{}
	got := f.loadFailure(context.Background(), "me", &cs.APIError{Reason: "passcodemismatch"}, nil)
	if !strings.Contains(got, "passcode") || !strings.Contains(got, "/lock load") {
		t.Fatalf("passcode error = %q", got)
	}
	if got := f.loadFailure(context.Background(), "me", &cs.APIError{Reason: "nodoublelocking"}, nil); !strings.Contains(got, "second keyholder") {
		t.Fatalf("double locking error = %q", got)
	}
}

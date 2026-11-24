package feature

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
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

func TestGenerateLockCode(t *testing.T) {
	code, err := generateLockCode(4, "NUMERIC")
	if err != nil {
		t.Fatalf("generateLockCode: %v", err)
	}
	if len(code) != 4 {
		t.Fatalf("code = %q, want 4 digits", code)
	}
	seen := make(map[rune]bool)
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("code = %q, want digits only", code)
		}
		if seen[r] {
			t.Fatalf("code = %q, want no duplicate digits", code)
		}
		seen[r] = true
	}
}

func TestGenerateLockCodeUsesAccountSettings(t *testing.T) {
	code, err := generateLockCode(6, "ALPHANUMERIC")
	if err != nil {
		t.Fatalf("generateLockCode: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code = %q, want 6 characters", code)
	}
	seen := make(map[rune]bool)
	for _, r := range code {
		if !strings.ContainsRune(codeAlphabet("ALPHANUMERIC"), r) {
			t.Fatalf("code = %q has a character outside the setting", code)
		}
		if seen[r] {
			t.Fatalf("code = %q, want no duplicate characters", code)
		}
		seen[r] = true
	}
}

func TestGenerateLockCodeDefaults(t *testing.T) {
	code, err := generateLockCode(0, "")
	if err != nil {
		t.Fatalf("generateLockCode: %v", err)
	}
	if len(code) != defaultLockCodeDigits {
		t.Fatalf("code = %q, want %d digits", code, defaultLockCodeDigits)
	}
}

func TestGenerateLockCodeLongerThanAlphabet(t *testing.T) {
	code, err := generateLockCode(14, "NUMERIC")
	if err != nil {
		t.Fatalf("generateLockCode: %v", err)
	}
	if len(code) != 14 {
		t.Fatalf("code = %q, want 14 digits", code)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("code = %q, want digits only", code)
		}
	}
}

func TestCodeAlphabet(t *testing.T) {
	cases := []struct {
		set  string
		want string
	}{
		{set: "", want: "0123456789"},
		{set: "NUMERIC", want: "0123456789"},
		{set: "ALPHABETIC", want: "ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
		{set: "LETTERS", want: "ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
		{set: "ALPHANUMERIC", want: "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
		{set: "unknown", want: "0123456789"},
	}
	for _, tc := range cases {
		if got := codeAlphabet(tc.set); got != tc.want {
			t.Errorf("codeAlphabet(%q) = %q, want %q", tc.set, got, tc.want)
		}
	}
}

func TestLoadModalHasOnlyPasscode(t *testing.T) {
	data := loadModalData("1234")
	if !strings.HasPrefix(data.CustomID, loadNamespace+":form:") {
		t.Fatalf("custom ID = %q", data.CustomID)
	}
	var ids []string
	for _, component := range data.Components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			t.Fatalf("component type = %T", component)
		}
		for _, inner := range row.Components {
			input, ok := inner.(*discordgo.TextInput)
			if !ok {
				t.Fatalf("input type = %T", inner)
			}
			ids = append(ids, input.CustomID)
		}
	}
	if len(ids) != 1 || ids[0] != "passcode" {
		t.Fatalf("inputs = %v, want only passcode", ids)
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
	text := loadResultText(lock, "me", "1234", true, now)
	for _, want := range []string{"Loaded **Test lock**", "Set your device to code `1234`", "/lock draw", "/lock checkin", "Reminders are on"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}

	off := loadResultText(lock, "me", "", false, now)
	if strings.Contains(off, "Set your device") {
		t.Errorf("text without a code still shows one:\n%s", off)
	}
	if !strings.Contains(off, "Reminders are off") {
		t.Errorf("text missing the reminders-off note:\n%s", off)
	}
}

func TestWatchLock(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &Feature{store: st, now: func() time.Time { return now }}

	lock := &cs.LoadedLock{ID: "lock-1", LockName: "Test lock", Type: "card", Draws: "g", Wait: 60}
	if err := f.watchLock(ctx, "user-1", lock); err != nil {
		t.Fatalf("watchLock: %v", err)
	}
	got, err := st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification: %v", err)
	}
	if got == nil || got.LockID != "lock-1" {
		t.Fatalf("notification = %+v", got)
	}
	if got.LastDraws != "g" || got.LastPicks != 0 {
		t.Fatalf("draw state = %q/%d, want g/0", got.LastDraws, got.LastPicks)
	}
	if !got.NextCheckAt.Equal(now.Add(62 * time.Second)) {
		t.Fatalf("next check = %s, want %s", got.NextCheckAt, now.Add(62*time.Second))
	}

	// A lock with draws ready does not wait for the next pick.
	ready := &cs.LoadedLock{ID: "lock-2", LockName: "Other", Type: "card", Draws: "rg", Picks: 1}
	if err := f.watchLock(ctx, "user-1", ready); err != nil {
		t.Fatalf("watchLock with draws: %v", err)
	}
	got, err = st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification: %v", err)
	}
	if got == nil || got.LockID != "lock-2" || !got.NextCheckAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("notification = %+v", got)
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

package feature

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func at(d time.Duration) cs.Time {
	return cs.Time{Time: time.Now().Add(d)}
}

func TestResolveLogTargetNewest(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "old", LockName: "Older lock", LoadTime: at(-2 * time.Hour)},
		{ID: "new", LockName: "Newer lock", LoadTime: at(-time.Hour)},
	}

	target, note, err := resolveLogTarget("", locks, nil)
	if err != nil {
		t.Fatalf("resolveLogTarget: %v", err)
	}
	if target.running == nil || target.running.ID != "new" {
		t.Fatalf("target = %+v, want newest lock", target)
	}
	if note == "" {
		t.Fatal("expected a note about choosing the newest lock")
	}
}

func TestResolveLogTargetByName(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "a", LockName: "Steel belt", LoadTime: at(-time.Hour)},
		{ID: "b", LockName: "Test card", LoadTime: at(-2 * time.Hour)},
	}

	target, _, err := resolveLogTarget("steel belt", locks, nil)
	if err != nil {
		t.Fatalf("resolveLogTarget: %v", err)
	}
	if target.running == nil || target.running.ID != "a" {
		t.Fatalf("target = %+v", target)
	}

	target, _, err = resolveLogTarget("Test", locks, nil)
	if err != nil {
		t.Fatalf("prefix resolve: %v", err)
	}
	if target.running == nil || target.running.ID != "b" {
		t.Fatalf("prefix target = %+v", target)
	}
}

func TestResolveLogTargetPrevious(t *testing.T) {
	recent := []cs.RecentLock{
		{LockID: "r1", LockName: "Lock one", UnlockTime: at(-48 * time.Hour)},
		{LockID: "r2", LockName: "Lock two", UnlockTime: at(-24 * time.Hour)},
	}

	target, note, err := resolveLogTarget("", nil, recent)
	if err != nil {
		t.Fatalf("resolveLogTarget: %v", err)
	}
	if target.previous == nil || target.previous.LockID != "r2" {
		t.Fatalf("target = %+v, want newest previous lock", target)
	}
	if note == "" {
		t.Fatal("expected a note about using the most recent lock")
	}

	target, _, err = resolveLogTarget("r1", nil, recent)
	if err != nil {
		t.Fatalf("resolve by id: %v", err)
	}
	if target.previous == nil || target.previous.LockID != "r1" {
		t.Fatalf("id target = %+v", target)
	}
}

func TestResolveLogTargetAmbiguous(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "a", LockName: "Test one", LoadTime: at(-time.Hour)},
		{ID: "b", LockName: "Test two", LoadTime: at(-2 * time.Hour)},
	}
	if _, _, err := resolveLogTarget("test", locks, nil); err == nil {
		t.Fatal("expected an ambiguity error")
	}
}

func TestNotificationText(t *testing.T) {
	lock := &cs.LoadedLock{
		ID:             "lock",
		LockName:       "Test card",
		Type:           "card",
		Picks:          2,
		MaxPicks:       3,
		Wait:           120,
		GreensRequired: 10000,
		Deck:           cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	text := notificationText(lock, now)
	for _, want := range []string{
		"Test card",
		"2 draws available",
		"of 3",
		"3 cards left",
		"2 greens needed",
		"Next draw <t:",
		":R>",
		"Open draw board",
		"/lock draw",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("notificationText missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"2m", "in 2 minutes"} {
		if strings.Contains(text, bad) {
			t.Errorf("notificationText still contains %q:\n%s", bad, text)
		}
	}
}

func TestDrawNotificationButtons(t *testing.T) {
	lock := &cs.LoadedLock{ID: "lock-id", Type: "card"}
	components := drawNotificationButtons(lock)
	if len(components) != 1 {
		t.Fatalf("components = %d, want 1 row", len(components))
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok || len(row.Components) != 1 {
		t.Fatalf("row = %+v, want one button", components[0])
	}
	button, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("component = %T, want a button", row.Components[0])
	}
	if button.CustomID != "drawboard:open:lock-id" {
		t.Errorf("custom ID = %q", button.CustomID)
	}
	if button.Label != "Open draw board" {
		t.Errorf("label = %q", button.Label)
	}
	if button.Style != discordgo.PrimaryButton {
		t.Errorf("style = %v, want primary", button.Style)
	}
	if got := drawNotificationButtons(nil); got != nil {
		t.Errorf("nil lock buttons = %+v, want nil", got)
	}
}

func TestDrawsAvailableZero(t *testing.T) {
	lock := &cs.LoadedLock{Type: "card", Picks: 0, Wait: 300}
	if got := lock.DrawsAvailable(); got != 0 {
		t.Fatalf("DrawsAvailable = %d, want 0", got)
	}
}

func TestLogOutputCondensesDraws(t *testing.T) {
	stamp := cs.Time{Time: time.Date(2026, 11, 24, 14, 32, 30, 0, time.UTC)}
	view := logView{entries: []cs.LogEntry{
		{Timestamp: stamp, Actor: 2, EventType: 6, Card: "g"},
		{Timestamp: cs.Time{Time: stamp.Time.Add(-10 * time.Second)}, Actor: 1, EventType: 6, Card: "d"},
		{Timestamp: cs.Time{Time: stamp.Time.Add(-20 * time.Second)}, Actor: 1, EventType: 6, Card: "i", Amount: 4},
	}}

	out := logOutput("", view, "")
	for _, want := range []string{
		cardEmojiMarkup(cs.CardGreen),
		":key:" + cardEmojiMarkup(cs.CardDoubleUp),
		":key:" + cardEmojiMarkup(cs.CardDice) + "4",
		"<t:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "picked") {
		t.Errorf("draws should be icons only:\n%s", out)
	}
	if got := strings.Count(out, "\n• "); got != 1 {
		t.Errorf("draw line count = %d, want 1:\n%s", got, out)
	}
	if got := strings.Count(out, "<t:"); got != 1 {
		t.Errorf("timestamp count = %d, want 1:\n%s", got, out)
	}
}

func TestLogOutputSplitsDrawsAfterAMinute(t *testing.T) {
	stamp := cs.Time{Time: time.Date(2026, 11, 24, 14, 32, 30, 0, time.UTC)}
	view := logView{entries: []cs.LogEntry{
		{Timestamp: stamp, Actor: 2, EventType: 6, Card: "g"},
		{Timestamp: cs.Time{Time: stamp.Time.Add(-30 * time.Second)}, Actor: 2, EventType: 6, Card: "r"},
		{Timestamp: cs.Time{Time: stamp.Time.Add(-2 * time.Minute)}, Actor: 2, EventType: 6, Card: "y"},
	}}

	out := logOutput("", view, "")
	if got := strings.Count(out, "\n• "); got != 2 {
		t.Fatalf("draw line count = %d, want 2:\n%s", got, out)
	}
	if got := strings.Count(out, "<t:"); got != 2 {
		t.Errorf("timestamp count = %d, want 2:\n%s", got, out)
	}
}

func TestLogOutputShowsEarlierDraws(t *testing.T) {
	stamp := cs.Time{Time: time.Date(2026, 11, 24, 14, 32, 30, 0, time.UTC)}
	view := logView{earlier: []cs.LogEntry{
		{Timestamp: stamp, Actor: 2, EventType: 6, Card: "g"},
		{Timestamp: cs.Time{Time: stamp.Time.Add(-20 * time.Second)}, Actor: 2, EventType: 6, Card: "r"},
	}}

	out := logOutput("", view, "")
	for _, want := range []string{
		"No draws in the last 5 minutes.",
		"**Earlier draws**",
		cardEmojiMarkup(cs.CardGreen),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "**Draws in the last 5 minutes**") {
		t.Errorf("earlier draws should not be under the recent heading:\n%s", out)
	}
}

func TestLogOutputKeepsEventText(t *testing.T) {
	stamp := cs.Time{Time: time.Date(2026, 11, 24, 14, 32, 30, 0, time.UTC)}
	view := logView{full: true, entries: []cs.LogEntry{
		{Timestamp: stamp, Actor: 2, EventType: 6, Card: "g"},
		{Timestamp: cs.Time{Time: stamp.Time.Add(-5 * time.Second)}, Actor: 2, EventType: 39},
	}}

	out := logOutput("", view, "")
	if !strings.Contains(out, "Lockee loaded the lock") {
		t.Errorf("full log should keep event text:\n%s", out)
	}
	if strings.Contains(out, "picked") {
		t.Errorf("draws should be icons only:\n%s", out)
	}
}

func TestLogOutputHidesHygieneCode(t *testing.T) {
	stamp := cs.Time{Time: time.Date(2026, 11, 24, 14, 32, 5, 0, time.UTC)}
	view := logView{full: true, entries: []cs.LogEntry{
		{Timestamp: stamp, Actor: 2, EventType: 21, Info: "4821"},
	}}

	out := logOutput("", view, "")
	if strings.Contains(out, "4821") {
		t.Fatalf("log output leaked a lock code:\n%s", out)
	}
	if !strings.Contains(out, "code hidden") {
		t.Fatalf("log output should say the code is hidden:\n%s", out)
	}
}

func TestLogHeaderUsesCompactStatus(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	header := logHeader(&logTarget{running: &lock}, nil, "viewer", now)
	for _, want := range []string{
		":locked: Locked",
		":key: Keyholder: keyholder#0",
		":flower_playing_cards:",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("header missing %q:\n%s", want, header)
		}
	}
	for _, bad := range []string{"Type:", "Role:", "Purpose:", "Lockee:"} {
		if strings.Contains(header, bad) {
			t.Errorf("header should omit %q:\n%s", bad, header)
		}
	}
}

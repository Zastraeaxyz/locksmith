package feature

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

// statusText flattens status embeds so tests can inspect their content.
func statusText(embeds []*discordgo.MessageEmbed) string {
	var b strings.Builder
	for _, embed := range embeds {
		b.WriteString(embed.Title)
		b.WriteString("\n")
		b.WriteString(embed.Description)
		b.WriteString("\n")
	}
	return b.String()
}

func statusTestNow() time.Time {
	return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
}

func statusTestLock(id, name string, now time.Time) cs.LoadedLock {
	return cs.LoadedLock{
		ID:          cs.ID(id),
		LockName:    name,
		Type:        "card",
		Lockee:      "viewer#0",
		LockeeID:    "viewer",
		Keyholder:   "keyholder#0",
		KeyholderID: "keyholder",
		LockPurpose: cs.Purpose("CHASTITY"),
		CardCount:   5,
		Picks:       0,
		MaxPicks:    1,
		Wait:        300,
		LoadTime:    cs.Time{Time: now.Add(-time.Hour)},
	}
}

func TestStatusSingleLockCompact(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	text := statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))

	for _, bad := range []string{"Type:", "Role:", "Purpose:", "Lockee:"} {
		if strings.Contains(text, bad) {
			t.Errorf("single-lock status should omit %q:\n%s", bad, text)
		}
	}
	for _, want := range []string{
		":locked: Locked",
		":key: Keyholder: keyholder#0",
		":flower_playing_cards: 5 cards · 0/1 draws",
		":hourglass: Next draw <t:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
}

func TestStatusVaryingPurpose(t *testing.T) {
	now := statusTestNow()
	first := statusTestLock("a", "Lock A", now)
	second := statusTestLock("b", "Lock B", now)
	second.LockPurpose = cs.Purpose("BONDAGE")

	text := statusText(renderStatusEmbeds([]cs.LockView{
		{Lock: first, Role: cs.RoleLockee},
		{Lock: second, Role: cs.RoleLockee},
	}, "viewer", nil, now))

	for _, want := range []string{"Purpose: CHASTITY", "Purpose: BONDAGE"} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"Type:", "Role:"} {
		if strings.Contains(text, bad) {
			t.Errorf("status should omit %q:\n%s", bad, text)
		}
	}
}

func TestStatusVaryingType(t *testing.T) {
	now := statusTestNow()
	card := statusTestLock("a", "Card lock", now)
	timer := statusTestLock("b", "Timer lock", now)
	timer.Type = "timer"
	timer.TimerRemaining = 3600

	text := statusText(renderStatusEmbeds([]cs.LockView{
		{Lock: card, Role: cs.RoleLockee},
		{Lock: timer, Role: cs.RoleLockee},
	}, "viewer", nil, now))

	for _, want := range []string{"Type: card", "Type: timer", ":hourglass: Unlocks <t:"} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Role:") {
		t.Errorf("status should omit Role:\n%s", text)
	}
}

func TestStatusSelfLocked(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	lock.Keyholder = "<self>"
	lock.KeyholderID = "0"

	text := statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))
	if !strings.Contains(text, ":key: Self-locked") {
		t.Errorf("status missing self-locked line:\n%s", text)
	}
	if strings.Contains(text, "Keyholder:") {
		t.Errorf("status should not name a keyholder:\n%s", text)
	}
}

func TestStatusFrozenSince(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	lock.FrozenByKeyholder = true
	since := now.Add(-2 * time.Hour)

	text := statusText(renderStatusEmbeds(
		[]cs.LockView{{Lock: lock, Role: cs.RoleLockee}},
		"viewer",
		map[cs.ID]time.Time{lock.ID: since},
		now,
	))

	if !strings.Contains(text, ":ice_cube: Frozen for 2hrs") {
		t.Errorf("status missing frozen duration:\n%s", text)
	}
	if strings.Contains(text, ":ice_cube: Frozen for <t:") {
		t.Errorf("frozen duration should not be a relative timestamp:\n%s", text)
	}
	if strings.Contains(text, "check-in") || strings.Contains(text, "Check-in") {
		t.Errorf("state line should not repeat check-in:\n%s", text)
	}
}

func TestStatusFreezeMinimum(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	lock.Regularity = 60
	lock.FrozenByCard = true

	text := statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))
	if !strings.Contains(text, ":hourglass: Minimum freeze 2hrs (2x the 1hr draw interval)") {
		t.Errorf("status missing freeze minimum:\n%s", text)
	}

	lock.Regularity = 120
	text = statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))
	if !strings.Contains(text, ":hourglass: Minimum freeze 4hrs (2x the 2hrs draw interval)") {
		t.Errorf("status missing scaled freeze minimum:\n%s", text)
	}

	lock.FrozenByCard = false
	text = statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))
	if strings.Contains(text, "Minimum freeze") {
		t.Errorf("unfrozen status should not show a freeze minimum:\n%s", text)
	}

	lock.FrozenByCard = true
	lock.Regularity = 0
	text = statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))
	if strings.Contains(text, "Minimum freeze") {
		t.Errorf("status without a draw interval should not guess a minimum:\n%s", text)
	}
}

func TestStatusReady(t *testing.T) {
	now := statusTestNow()
	lock := statusTestLock("a", "Lock A", now)
	lock.ReadyToUnlock = true

	text := statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))
	if !strings.Contains(text, ":unlocked: Ready to unlock") {
		t.Errorf("status missing ready state:\n%s", text)
	}
}

func TestStatusLoadedElapsed(t *testing.T) {
	now := statusTestNow()

	cases := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{name: "days hours minutes", ago: 39*time.Hour + 56*time.Minute, want: "locked for 1d 15hrs 56min"},
		{name: "hours minutes", ago: 5*time.Hour + 12*time.Minute, want: "locked for 5hrs 12min"},
		{name: "whole hours", ago: 3 * time.Hour, want: "locked for 3hrs"},
		{name: "minutes", ago: 42 * time.Minute, want: "locked for 42min"},
		{name: "less than a minute", ago: 30 * time.Second, want: "locked for less than a minute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lock := statusTestLock("a", "Lock A", now)
			lock.LoadTime = cs.Time{Time: now.Add(-tc.ago)}
			text := statusText(renderStatusEmbeds([]cs.LockView{{Lock: lock, Role: cs.RoleLockee}}, "viewer", nil, now))
			if !strings.Contains(text, tc.want) {
				t.Errorf("status missing %q:\n%s", tc.want, text)
			}
			if strings.Contains(text, "loaded <t:") {
				t.Errorf("loaded time should not be a relative timestamp:\n%s", text)
			}
		})
	}
}

func TestStatusColorsAndEmbeds(t *testing.T) {
	now := statusTestNow()

	frozen := statusTestLock("a", "Frozen", now)
	frozen.FrozenByKeyholder = true
	ready := statusTestLock("b", "Ready", now)
	ready.ReadyToUnlock = true
	waiting := statusTestLock("c", "Waiting", now)
	drawable := statusTestLock("d", "Drawable", now)
	drawable.Picks = 1
	timer := statusTestLock("e", "Timer", now)
	timer.Type = "timer"

	embeds := renderStatusEmbeds([]cs.LockView{
		{Lock: frozen, Role: cs.RoleLockee},
		{Lock: ready, Role: cs.RoleLockee},
		{Lock: waiting, Role: cs.RoleLockee},
		{Lock: drawable, Role: cs.RoleLockee},
		{Lock: timer, Role: cs.RoleLockee},
	}, "viewer", nil, now)

	if len(embeds) != 5 {
		t.Fatalf("embeds = %d, want one per lock", len(embeds))
	}

	want := map[string]int{
		"Frozen":   cardColor(cs.CardFreeze),
		"Ready":    cardColor(cs.CardGreen),
		"Waiting":  cardColor(cs.CardRed),
		"Drawable": embedColor,
		"Timer":    embedColor,
	}
	for _, embed := range embeds {
		if embed.Title == "" {
			t.Errorf("embed has no title: %+v", embed)
		}
		if embed.Color != want[embed.Title] {
			t.Errorf("%s color = %#x, want %#x", embed.Title, embed.Color, want[embed.Title])
		}
	}
}

func TestFreezeStart(t *testing.T) {
	now := statusTestNow()

	keyholder := &cs.LoadedLock{FrozenByKeyholder: true}
	newestUnfreeze := []cs.LogEntry{
		{EventType: 3, Timestamp: cs.Time{Time: now}},
		{EventType: 2, Timestamp: cs.Time{Time: now.Add(-time.Hour)}},
	}
	if _, ok := freezeStartFor(keyholder, newestUnfreeze); ok {
		t.Fatal("unfreeze after freeze should not report a freeze start")
	}

	newestFreeze := []cs.LogEntry{
		{EventType: 2, Timestamp: cs.Time{Time: now.Add(-time.Hour)}},
		{EventType: 3, Timestamp: cs.Time{Time: now.Add(-2 * time.Hour)}},
	}
	since, ok := freezeStartFor(keyholder, newestFreeze)
	if !ok {
		t.Fatal("expected a keyholder freeze start")
	}
	if !since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("since = %s", since)
	}

	cardFreeze := &cs.LoadedLock{FrozenByCard: true}
	cardEntries := []cs.LogEntry{
		{EventType: 6, Card: "r", Timestamp: cs.Time{Time: now}},
		{EventType: 6, Card: "f", Timestamp: cs.Time{Time: now.Add(-time.Hour)}},
	}
	since, ok = freezeStartFor(cardFreeze, cardEntries)
	if !ok {
		t.Fatal("expected a card freeze start")
	}
	if !since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("card since = %s", since)
	}

	both := &cs.LoadedLock{FrozenByKeyholder: true, FrozenByCard: true}
	bothEntries := []cs.LogEntry{
		{EventType: 2, Timestamp: cs.Time{Time: now.Add(-30 * time.Minute)}},
		{EventType: 6, Card: "s", Timestamp: cs.Time{Time: now.Add(-10 * time.Minute)}},
	}
	since, ok = freezeStartFor(both, bothEntries)
	if !ok {
		t.Fatal("expected a mixed freeze start")
	}
	if !since.Equal(now.Add(-10 * time.Minute)) {
		t.Fatalf("mixed since = %s", since)
	}
}

func TestIsFreezeCard(t *testing.T) {
	for _, letter := range []string{"f", "a", "s"} {
		if !isFreezeCard(letter) {
			t.Errorf("isFreezeCard(%q) = false, want true", letter)
		}
	}
	for _, letter := range []string{"", "r", "g", "t", "i"} {
		if isFreezeCard(letter) {
			t.Errorf("isFreezeCard(%q) = true, want false", letter)
		}
	}
}

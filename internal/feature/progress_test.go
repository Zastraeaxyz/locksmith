package feature

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func progressTime(day, hour int) cs.Time {
	return cs.Time{Time: time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC)}
}

func progressLock(deck cs.Deck, hidden bool, now time.Time) *cs.LoadedLock {
	return &cs.LoadedLock{
		ID:             "lock",
		LockName:       "Progress",
		Type:           "card",
		Hidden:         hidden,
		CardCount:      5,
		GreensRequired: 1,
		Deck:           deck,
		LoadTime:       progressTime(25, 10),
		LastResetTime:  progressTime(25, 10),
	}
}

func TestProgressExactVisibleReconstruction(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(cs.Deck{cs.CardRed: 4}, false, now)

	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(25, 10), EventType: 6, Card: "g", Actor: 2},
		{Timestamp: progressTime(25, 11), EventType: 6, Card: "r", Actor: 2},
		{Timestamp: progressTime(26, 10), EventType: 6, Card: "2", Actor: 2},
		{Timestamp: progressTime(26, 11), EventType: 6, Card: "d", Actor: 2},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "f", Actor: 2},
	}
	infos := make([]progressEventInfo, len(events))
	states := progressStates(lock, events, infos, now)

	for key, want := range map[string]struct {
		total   int
		greens  int
		redYell int
	}{
		"2026-11-25": {total: 3, greens: 0, redYell: 1},
		"2026-11-26": {total: 5, greens: 0, redYell: 4},
		"2026-11-27": {total: 4, greens: 0, redYell: 4},
	} {
		state, ok := states[key]
		if !ok {
			t.Fatalf("missing state for %s", key)
		}
		if !state.exact {
			t.Errorf("%s state is not exact", key)
		}
		if state.total != want.total || state.greens != want.greens || state.redYellow != want.redYell {
			t.Errorf("%s state = total %d greens %d red/yellow %d, want %+v",
				key, state.total, state.greens, state.redYellow, want)
		}
	}

	if !infos[4].hasDouble || !infos[4].exact || infos[4].double != 2 {
		t.Fatalf("double info = %+v, want an exact addition of 2", infos[4])
	}
	if got := states["2026-11-27"].greensNeeded; got != 0 {
		t.Errorf("greens needed = %d, want 0", got)
	}
}

func TestProgressHiddenUsesNoDeckFaces(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(cs.Deck{cs.CardGreen: 5}, true, now)
	lock.CardCount = 10
	lock.GreensRequired = 3

	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "g", Actor: 2},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "r", Actor: 2},
		{Timestamp: progressTime(27, 11), EventType: 6, Card: "d", Actor: 2},
		{Timestamp: progressTime(27, 11), EventType: 6, Card: "s", Actor: 2},
	}
	infos := make([]progressEventInfo, len(events))
	states := progressStates(lock, events, infos, now)
	state := states["2026-11-27"]

	if state.exact {
		t.Fatal("hidden deck produced an exact state")
	}
	if state.cards != nil {
		t.Fatal("hidden deck faces leaked into the state")
	}
	if state.total != 9 {
		t.Errorf("total = %d, want 9", state.total)
	}
	if !infos[3].hasDouble || infos[3].double != 2 {
		t.Errorf("double info = %+v, want an estimated addition of 2", infos[3])
	}
	if !state.rough {
		t.Error("a double up should make the hidden estimate rough")
	}
}

func TestProgressResetUsesOriginalDeck(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(cs.Deck{cs.CardGreen: 1, cs.CardRed: 1}, false, now)
	lock.CardCount = 3

	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(25, 11), EventType: 6, Card: "g", Actor: 2},
		{Timestamp: progressTime(26, 10), EventType: 9},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "g", Actor: 2},
	}
	infos := make([]progressEventInfo, len(events))
	states := progressStates(lock, events, infos, now)

	day1 := states["2026-11-25"]
	if !day1.exact || day1.total != 2 {
		t.Fatalf("day before reset = %+v, want 2 exact cards", day1)
	}
	day3 := states["2026-11-27"]
	if !day3.exact || day3.total != 2 {
		t.Fatalf("day after reset = %+v, want 2 exact cards", day3)
	}
}

func TestProgressTallyAndChanges(t *testing.T) {
	events := []cs.LogEntry{
		{EventType: 6, Card: "s", Actor: 2},
		{EventType: 6, Card: "a", Actor: 2},
		{EventType: 6, Card: "2", Actor: 2},
		{EventType: 6, Card: "d", Actor: 1},
		{EventType: 15, Card: "r", Amount: 3, Actor: 2},
		{EventType: 16, Card: "g", Amount: 1, Actor: 2},
	}
	infos := []progressEventInfo{
		{}, {}, {}, {hasDouble: true, double: 4, doubleLow: 4, doubleHigh: 4, exact: true}, {}, {},
	}
	tally := tallyProgressDay(events, infos)
	if tally.draws != 4 || tally.sticky != 1 || tally.arctic != 1 {
		t.Errorf("draw tally = %+v", tally)
	}
	if tally.yellowCount != 1 || tally.yellowRedAdd != 2 {
		t.Errorf("yellow tally = %d cards, +%d reds", tally.yellowCount, tally.yellowRedAdd)
	}
	if tally.doubleCount != 1 || tally.doubleAdded != 4 || tally.doubleRough {
		t.Errorf("double tally = %+v", tally)
	}
	if tally.added != 9 || tally.removed != 3 {
		t.Errorf("added = %d, removed = %d, want 9 and 3", tally.added, tally.removed)
	}

	lines := strings.Join(tally.changeLines(), "\n")
	for _, want := range []string{
		"Removed: 2 drawn cards.",
		"double up added 4 cards",
		"Yellow cards added 2 reds",
		"Added:",
		"Removed:",
		"Net: +6 cards.",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("change lines missing %q:\n%s", want, lines)
		}
	}
}

func TestProgressGreenBackAndPutBack(t *testing.T) {
	events := []cs.LogEntry{
		{EventType: 15, Card: "g", Amount: 1, Actor: 2},
	}
	tally := tallyProgressDay(events, []progressEventInfo{{}})
	if tally.putGreenBack != 1 {
		t.Fatalf("put green back = %d, want 1", tally.putGreenBack)
	}
	if len(tally.explicitAdds) != 0 {
		t.Fatalf("put green back also appeared as an explicit add: %+v", tally.explicitAdds)
	}
}

func TestProgressDaysGroupByUTCDay(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	events := []cs.LogEntry{
		{Timestamp: cs.Time{Time: time.Date(2026, 11, 26, 23, 59, 0, 0, time.UTC)}, EventType: 6, Card: "g"},
		{Timestamp: cs.Time{Time: time.Date(2026, 11, 27, 0, 1, 0, 0, time.UTC)}, EventType: 6, Card: "r"},
	}
	infos := make([]progressEventInfo, len(events))
	days := progressDays(events, infos, map[string]progressDeckState{}, now, 0)
	if len(days) != 2 {
		t.Fatalf("days = %d, want 2", len(days))
	}
	if days[0].key != "2026-11-27" || !days[0].isToday {
		t.Errorf("first day = %+v", days[0])
	}
	if days[1].key != "2026-11-26" {
		t.Errorf("second day = %+v", days[1])
	}
	limited := progressDays(events, infos, map[string]progressDeckState{}, now, 1)
	if len(limited) != 1 || limited[0].key != "2026-11-27" {
		t.Errorf("limited days = %+v", limited)
	}
}

func TestProgressTargetPrefersCardLock(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "timer", LockName: "Timer", Type: "timer"},
		{ID: "card", LockName: "Card", Type: "card", LockeeID: testUserID},
	}
	target, _, err := progressTargetFor(locks, nil, testUserID, "")
	if err != nil {
		t.Fatalf("progressTargetFor: %v", err)
	}
	if target.running == nil || target.running.ID != "card" {
		t.Fatalf("target = %+v, want the card lock", target)
	}

	if _, _, err := progressTargetFor(locks, nil, testUserID, "timer"); err == nil {
		t.Fatal("a timer lock should be refused")
	}
}

func TestLockProgressCommandOptions(t *testing.T) {
	var progress *discordgo.ApplicationCommandOption
	for _, option := range lockCommand().Options {
		if option.Name == "progress" {
			progress = option
			break
		}
	}
	if progress == nil {
		t.Fatal("lock command has no progress subcommand")
	}
	if progress.Description == "" {
		t.Fatal("progress subcommand has no description")
	}

	options := make(map[string]*discordgo.ApplicationCommandOption)
	for _, option := range progress.Options {
		options[option.Name] = option
	}
	for _, name := range []string{"ref", "days", "public"} {
		if options[name] == nil {
			t.Errorf("progress is missing the %s option", name)
		}
	}
	if options["days"].Type != discordgo.ApplicationCommandOptionInteger {
		t.Errorf("days type = %v, want integer", options["days"].Type)
	}
	if options["ref"].Autocomplete != true {
		t.Error("ref should autocomplete")
	}
}

func progressNewestFirst(events []cs.LogEntry) []cs.LogEntry {
	out := make([]cs.LogEntry, len(events))
	for idx := range events {
		out[idx] = events[len(events)-1-idx]
	}
	return out
}

func setProgressTestEmojis(t *testing.T) {
	t.Helper()
	ids := map[string]string{applicationEmojiBlank: "test-blank"}
	for _, name := range cardEmojiNames {
		ids[name] = "test-" + name
	}
	setApplicationEmojiIDs(ids)
	t.Cleanup(func() { setApplicationEmojiIDs(nil) })
}

func TestBuildProgressReportVisible(t *testing.T) {
	setProgressTestEmojis(t)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(cs.Deck{cs.CardRed: 4}, false, now)
	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(25, 10), EventType: 6, Card: "g", Actor: 2},
		{Timestamp: progressTime(25, 11), EventType: 6, Card: "r", Actor: 2},
		{Timestamp: progressTime(26, 10), EventType: 6, Card: "2", Actor: 2},
		{Timestamp: progressTime(26, 11), EventType: 6, Card: "d", Actor: 2},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "f", Actor: 2},
	}
	report := buildProgressReport(&progressTarget{running: lock}, progressNewestFirst(events), false, 0, now, "")

	for _, want := range []string{
		"**Progress**",
		"What's left now",
		"By day (UTC)",
		"double up added 2 cards",
		"Exact from the visible deck.",
		"Removed:",
		"Net:",
		"This lock",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
}

func TestBuildProgressReportHidden(t *testing.T) {
	setProgressTestEmojis(t)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(cs.Deck{cs.CardGreen: 5}, true, now)
	lock.CardCount = 10
	lock.GreensRequired = 3
	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "g", Actor: 2},
		{Timestamp: progressTime(27, 11), EventType: 6, Card: "d", Actor: 2},
	}
	report := buildProgressReport(&progressTarget{running: lock}, progressNewestFirst(events), false, 0, now, "")

	for _, want := range []string{
		"What's left now",
		"Rough estimate.",
		"Hidden deck estimates use only the cards seen in the log.",
		"range",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("hidden report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "Exact from the visible deck.") {
		t.Errorf("hidden report claimed exact deck data:\n%s", report)
	}
}

func TestProgressResetWithoutFollowingDraws(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(cs.Deck{cs.CardGreen: 2, cs.CardRed: 1}, false, now)
	lock.CardCount = 3

	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(25, 11), EventType: 6, Card: "g", Actor: 2},
		{Timestamp: progressTime(26, 10), EventType: 9},
	}
	infos := make([]progressEventInfo, len(events))
	states := progressStates(lock, events, infos, now)

	if day1 := states["2026-11-25"]; !day1.exact || day1.total != 2 {
		t.Fatalf("day before reset = %+v, want 2 exact cards", day1)
	}
	if today := states["2026-11-27"]; !today.exact || today.total != 3 {
		t.Fatalf("today = %+v, want the restored 3-card deck", today)
	}
}

func TestBuildProgressReportPreviousDoubleIsUnknown(t *testing.T) {
	setProgressTestEmojis(t)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	recent := &cs.RecentLock{LockID: "old", LockName: "Old lock"}
	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(25, 11), EventType: 6, Card: "d", Actor: 2},
	}
	report := buildProgressReport(&progressTarget{previous: recent}, progressNewestFirst(events), false, 0, now, "")
	if !strings.Contains(report, "the number added is unknown") {
		t.Errorf("previous double was not marked unknown:\n%s", report)
	}
	if strings.Contains(report, "Net:") {
		t.Errorf("previous double should not claim a net:\n%s", report)
	}
}

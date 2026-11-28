package feature

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
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

func progressObservation(at cs.Time, cardCount int, draws string) store.LockObservation {
	return store.LockObservation{
		LockID:     "lock",
		ObservedAt: at.Time,
		CardCount:  cardCount,
		DeckTotal:  cardCount,
		Draws:      draws,
		Hidden:     true,
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
	ends, starts := progressStates(lock, events, infos, nil, now)

	for key, want := range map[string]struct {
		total   int
		greens  int
		redYell int
	}{
		"2026-11-25": {total: 3, greens: 0, redYell: 1},
		"2026-11-26": {total: 5, greens: 0, redYell: 4},
		"2026-11-27": {total: 4, greens: 0, redYell: 4},
	} {
		state, ok := ends[key]
		if !ok {
			t.Fatalf("missing state for %s", key)
		}
		if !state.totalExact || !state.redYellowExact || !state.greensExact {
			t.Errorf("%s state is not exact: %+v", key, state)
		}
		if state.total != want.total || state.greens != want.greens || state.redYellow != want.redYell {
			t.Errorf("%s state = total %d greens %d red/yellow %d, want %+v",
				key, state.total, state.greens, state.redYellow, want)
		}
	}
	if len(starts) == 0 {
		t.Fatal("no day starts were recorded")
	}

	if !infos[4].hasDouble || !infos[4].exact || infos[4].double != 2 {
		t.Fatalf("double info = %+v, want an exact addition of 2", infos[4])
	}
	if got := ends["2026-11-27"].greensNeeded; got != 0 {
		t.Errorf("greens needed = %d, want 0", got)
	}
}

func TestProgressHiddenDoubleUsesObservationCounts(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(nil, true, now)
	lock.CardCount = 227
	lock.GreensRequired = 3

	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(26, 10), EventType: 6, Card: "d", Actor: 2},
	}
	observations := []store.LockObservation{
		progressObservation(progressTime(26, 9), 163, "gr"),
		progressObservation(progressTime(26, 11), 227, "grd"),
		progressObservation(progressTime(27, 10), 227, "grd"),
	}
	infos := make([]progressEventInfo, len(events))
	ends, _ := progressStates(lock, events, infos, observations, now)

	if !infos[1].hasDouble || !infos[1].exact {
		t.Fatalf("double info = %+v, want an exact anchor", infos[1])
	}
	if infos[1].before != 163 || infos[1].after != 227 || infos[1].double != 65 || infos[1].other != 97 {
		t.Fatalf("double info = %+v, want 163 -> 227, added 65, other 97", infos[1])
	}
	state := ends["2026-11-27"]
	if !state.totalExact || state.total != 227 {
		t.Fatalf("total = %+v, want 227", state)
	}
	if !state.redYellowExact || state.redYellow != 130 {
		t.Fatalf("red/yellow = %+v, want 130 after the double", state)
	}
}

func TestProgressDoubleCandidateFallback(t *testing.T) {
	ry, other, ok := progressDoubleCandidate(10, 20)
	if !ok || ry != 10 || other != 0 {
		t.Fatalf("candidate = %d/%d/%t, want 10/0/true", ry, other, ok)
	}
	if _, _, ok := progressDoubleCandidate(0, 1); ok {
		t.Fatal("an impossible pair should not produce a candidate")
	}
}

func TestProgressHiddenWithoutObservationsIsUnknown(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(nil, true, now)
	lock.CardCount = 40
	lock.GreensRequired = 3
	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "d", Actor: 2},
	}
	infos := make([]progressEventInfo, len(events))
	ends, _ := progressStates(lock, events, infos, nil, now)
	if !infos[1].hasDouble || infos[1].exact {
		t.Fatalf("double info = %+v, want unknown", infos[1])
	}
	state := ends["2026-11-27"]
	if !state.totalExact || state.total != 40 {
		t.Fatalf("total = %+v, want the live 40", state)
	}
	if !state.redYellowUnknown {
		t.Fatalf("red/yellow = %+v, want unknown without anchors", state)
	}
}

func TestProgressTallyNetsYellows(t *testing.T) {
	events := []cs.LogEntry{
		{EventType: 6, Card: "r", Actor: 2},
		{EventType: 6, Card: "r", Actor: 2},
		{EventType: 6, Card: "r", Actor: 2},
		{EventType: 6, Card: "r", Actor: 2},
		{EventType: 6, Card: "2", Actor: 2},
		{EventType: 6, Card: "y", Actor: 2},
		{EventType: 6, Card: "g", Actor: 2},
		{EventType: 15, Card: "g", Amount: 1, Actor: 2},
	}
	infos := make([]progressEventInfo, len(events))
	tally := tallyProgressDay(events, infos)
	if tally.redsDrawn != 4 {
		t.Errorf("reds drawn = %d, want 4", tally.redsDrawn)
	}
	if tally.yellowRedNet() != 1 {
		t.Errorf("yellow net = %d, want +1", tally.yellowRedNet())
	}
	if tally.redsNet() != 3 {
		t.Errorf("reds net = %d, want 3", tally.redsNet())
	}
	if tally.greensNet() != 0 {
		t.Errorf("greens net = %d, want 0 after put back", tally.greensNet())
	}
	if tally.drawnRemoved != 7 {
		t.Errorf("drawn removed = %d, want 7", tally.drawnRemoved)
	}
	if !strings.Contains(tally.detailText(), "Yellow draws") {
		t.Errorf("detail text missing yellow detail:\n%s", tally.detailText())
	}
}

func TestProgressTallyDoubleDetails(t *testing.T) {
	events := []cs.LogEntry{
		{EventType: 6, Card: "d", Actor: 1},
	}
	infos := []progressEventInfo{{
		hasDouble: true,
		exact:     true,
		double:    65,
		before:    163,
		after:     227,
		other:     97,
	}}
	tally := tallyProgressDay(events, infos)
	if !tally.doubleExact || tally.doubleAdded != 65 || tally.doubleBefore != 163 || tally.doubleOther != 97 {
		t.Fatalf("double tally = %+v", tally)
	}
	text := tally.detailText()
	for _, want := range []string{"163 -> 227", "added 65 red/yellow", "97 other"} {
		if !strings.Contains(text, want) {
			t.Errorf("detail text missing %q:\n%s", want, text)
		}
	}
}

func TestProgressGreenBackAndPutBack(t *testing.T) {
	events := []cs.LogEntry{
		{EventType: 15, Card: "g", Amount: 1, Actor: 2},
	}
	tally := tallyProgressDay(events, []progressEventInfo{{}})
	if tally.putGreenBack != 1 || tally.greensBack != 1 {
		t.Fatalf("put green back = %d/%d, want 1/1", tally.putGreenBack, tally.greensBack)
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
	days := progressDays(events, infos, map[string]progressDeckState{}, map[string]progressDeckState{}, now, 0)
	if len(days) != 2 {
		t.Fatalf("days = %d, want 2", len(days))
	}
	if days[0].key != "2026-11-27" || !days[0].isToday {
		t.Errorf("first day = %+v", days[0])
	}
	if days[1].key != "2026-11-26" {
		t.Errorf("second day = %+v", days[1])
	}
	limited := progressDays(events, infos, map[string]progressDeckState{}, map[string]progressDeckState{}, now, 1)
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

func TestBuildProgressReport(t *testing.T) {
	setProgressTestEmojis(t)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	lock := progressLock(nil, true, now)
	lock.CardCount = 226
	lock.GreensRequired = 3

	events := []cs.LogEntry{
		{Timestamp: progressTime(25, 10), EventType: 39},
		{Timestamp: progressTime(26, 10), EventType: 6, Card: "d", Actor: 2},
		{Timestamp: progressTime(27, 10), EventType: 6, Card: "r", Actor: 2},
	}
	observations := []store.LockObservation{
		progressObservation(progressTime(26, 9), 163, "gr"),
		progressObservation(progressTime(26, 11), 227, "grd"),
		progressObservation(progressTime(27, 11), 226, "grdr"),
	}
	report := buildProgressReport(&progressTarget{running: lock}, progressNewestFirst(events), observations, false, 0, now, "")
	for _, want := range []string{
		"**Progress**",
		"What's left",
		"By day (UTC)",
		"red/yellow",
		"Greens +0",
		"Reds",
		"Double up: 163 -> 227",
		"added 65 red/yellow",
		"97 other",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
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
	report := buildProgressReport(&progressTarget{previous: recent}, progressNewestFirst(events), nil, false, 0, now, "")
	if !strings.Contains(report, "counts around this draw were not stored") {
		t.Errorf("previous double was not marked unknown:\n%s", report)
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
	ends, _ := progressStates(lock, events, infos, nil, now)
	if day1 := ends["2026-11-25"]; !day1.totalExact || day1.total != 2 {
		t.Fatalf("day before reset = %+v, want 2 exact cards", day1)
	}
	if today := ends["2026-11-27"]; !today.totalExact || today.total != 3 {
		t.Fatalf("today = %+v, want the restored 3-card deck", today)
	}
}

func TestObserveLockStoresHiddenCountOnly(t *testing.T) {
	ctx := context.Background()
	st, _ := openAccountTestStore(t)
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	f := &Feature{store: st, now: func() time.Time { return now }}

	lock := &cs.LoadedLock{
		ID:        "lock",
		Type:      "card",
		Hidden:    true,
		CardCount: 42,
		Deck:      cs.Deck{cs.CardGreen: 10},
	}
	f.observeLock(ctx, lock)

	observations, err := st.LockObservations(ctx, "lock", time.Time{})
	if err != nil {
		t.Fatalf("LockObservations: %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(observations))
	}
	got := observations[0]
	if got.CardCount != 42 || got.DeckTotal != 42 {
		t.Fatalf("counts = %d/%d, want 42/42", got.CardCount, got.DeckTotal)
	}
	if got.DeckJSON != "" {
		t.Fatalf("hidden deck faces were stored: %q", got.DeckJSON)
	}
	if !got.Hidden {
		t.Fatal("hidden flag was not stored")
	}
}

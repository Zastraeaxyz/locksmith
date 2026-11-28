package feature

// The progress module turns a lock log into a day-by-day deck report. Draws
// and explicit card edits are facts from the log. The remaining deck is exact
// when the live deck is visible, because the log can be walked back from that
// composition. A hidden deck is estimated from its configured size, the cards
// seen so far, and the known effects of those cards. Hidden card faces are
// never read, so the estimates cannot leak the deck the lockee chose to hide.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
	"locksmith/internal/store"
)

const (
	progressLogPageSize = 100
	progressLogLimit    = 2000
	progressDayLimit    = 90
	progressDayLayout   = "2006-01-02"
)

// progressTarget is the lock a progress report describes. A running lock has a
// live deck to compare; a previous lock only has its log.
type progressTarget struct {
	running  *cs.LoadedLock
	previous *cs.RecentLock
}

func (t *progressTarget) id() cs.ID {
	switch {
	case t == nil:
		return ""
	case t.running != nil:
		return t.running.ID
	case t.previous != nil:
		return t.previous.LockID
	default:
		return ""
	}
}

// progressDeckState is the deck at one point in time, either exact from a
// visible deck or estimated for a hidden one.
type progressDeckState struct {
	cards cs.Deck

	total      int
	totalExact bool

	redYellow        int
	redYellowExact   bool
	redYellowUnknown bool

	greens         int
	greensExact    bool
	greensProgress int
	greensGoal     int

	greensNeeded int
	allGreens    bool

	notes []string
}

// progressEventInfo carries the parts of one event that the daily summary
// cannot recover from the log alone, such as the number of cards a double up
// added.
type progressEventInfo struct {
	hasDouble bool
	double    int
	before    int
	after     int
	other     int
	exact     bool
	unknown   bool

	clamped       bool
	clampedRemove bool
}

// progressDay is one UTC day of the report.
type progressDay struct {
	key      string
	events   []cs.LogEntry
	infos    []progressEventInfo
	state    progressDeckState
	start    progressDeckState
	hasState bool
	hasStart bool
	isToday  bool
}

// handleLockProgress shows a day-by-day deck report for one card lock.
func (f *Feature) handleLockProgress(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	ref, _ := stringOption(opts, "ref")
	days := intOptionOr(opts, "days", 0)
	public := boolOption(opts, "public")
	if days < 0 {
		days = 0
	}
	if days > progressDayLimit {
		days = progressDayLimit
	}

	if err := bot.Defer(s, i, !public); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}
	now := f.now()
	locks, _ = f.freshLocks(ctx, client, locks, now)

	recent, err := client.Recent(ctx)
	if err != nil {
		f.log.Warn("loading previous locks", "user_id", userID, "err", err)
		recent = nil
	}

	target, note, err := progressTargetFor(locks, recent, cred.ChastiSafeID, ref)
	if err != nil {
		return bot.EditChunks(s, i, err.Error(), !public)
	}

	if target.running != nil {
		// Record this read before loading observations, so today's state has an
		// anchor even when the lock has not been polled recently.
		f.observeLock(ctx, target.running)
	}
	var observations []store.LockObservation
	if target.running != nil {
		observations = f.progressObservations(ctx, target.id().String(), progressObservationSince(target.running))
	}

	entries, truncated, err := f.fetchProgressLog(ctx, client, target.id())
	if err != nil {
		f.log.Warn("loading progress log", "user_id", userID, "lock_id", target.id().String(), "err", err)
		header := progressHeader(target, now)
		return bot.EditChunks(s, i, header+"\n\nI could not load the log for that lock.", !public)
	}

	report := buildProgressReport(target, entries, observations, truncated, days, now, note)
	return bot.EditChunks(s, i, report, !public)
}

// progressTargetFor resolves the lock a progress report describes, preferring
// the caller's newest card lock.
func progressTargetFor(locks []cs.LoadedLock, recent []cs.RecentLock, chastisafeID, ref string) (*progressTarget, string, error) {
	own := ownLocks(locks, chastisafeID)
	ref = strings.TrimSpace(ref)

	if ref == "" {
		if lock := drawableLock(own, chastisafeID); lock != nil {
			return &progressTarget{running: lock}, progressAutoNote(own, lock), nil
		}
		if lock := drawRefusalTarget(own, chastisafeID); lock != nil {
			return &progressTarget{running: lock}, progressAutoNote(own, lock), nil
		}
		if target := newestRecent(recent); target != nil {
			return &progressTarget{previous: target},
				fmt.Sprintf("You have no active card lock. Showing your most recent completed lock, **%s**.", recentName(target)), nil
		}
		return nil, "", fmt.Errorf("You have no ChastiSafe card locks.")
	}

	resolved, note, err := resolveRef(ref, own, recent)
	if err != nil {
		return nil, "", err
	}
	if resolved.Running != nil {
		if !resolved.Running.IsCardLock() {
			return nil, "", fmt.Errorf("**%s** is not a card lock, so it has no deck progress.", lockName(resolved.Running))
		}
		return &progressTarget{running: resolved.Running}, note, nil
	}
	if resolved.Previous != nil {
		return &progressTarget{previous: resolved.Previous}, note, nil
	}
	return nil, "", fmt.Errorf("I could not find a lock called `%s`.", ref)
}

// progressAutoNote explains an automatic choice when more than one card lock
// could have been meant.
func progressAutoNote(locks []cs.LoadedLock, chosen *cs.LoadedLock) string {
	if chosen == nil {
		return ""
	}
	count := 0
	for idx := range locks {
		if locks[idx].IsCardLock() {
			count++
		}
	}
	if count <= 1 {
		return ""
	}
	return fmt.Sprintf("Using **%s**. Pass `ref` to choose another card lock.", lockName(chosen))
}

// fetchProgressLog reads the whole log, newest first, up to a safe cap. Progress
// needs the start of the lock, not just today, because every day in the lock is
// part of the report.
func (f *Feature) fetchProgressLog(ctx context.Context, client *cs.Client, lockID cs.ID) ([]cs.LogEntry, bool, error) {
	if lockID == "" {
		return nil, false, fmt.Errorf("no lock selected")
	}

	entries := make([]cs.LogEntry, 0, progressLogPageSize*4)
	for page := 0; len(entries) < progressLogLimit; page++ {
		batch, err := client.Log(ctx, lockID, page*progressLogPageSize, progressLogPageSize)
		if err != nil {
			return nil, false, err
		}
		if len(batch) == 0 {
			return entries, false, nil
		}
		entries = append(entries, batch...)
		if len(batch) < progressLogPageSize {
			return entries, false, nil
		}
	}
	return entries, true, nil
}

// buildProgressReport turns the log into the text reply.
func buildProgressReport(target *progressTarget, entries []cs.LogEntry, observations []store.LockObservation, truncated bool, days int, now time.Time, note string) string {
	header := progressHeader(target, now)
	chronological := progressChronological(entries)
	infos := make([]progressEventInfo, len(chronological))
	ends := make(map[string]progressDeckState)
	starts := make(map[string]progressDeckState)
	if target.running != nil {
		ends, starts = progressStates(target.running, chronological, infos, observations, now)
	}
	dayList := progressDays(chronological, infos, ends, starts, now, days)

	var sections []string
	sections = append(sections, header)
	if note != "" {
		sections = append(sections, note)
	}
	if len(entries) == 0 && target.running == nil {
		sections = append(sections, "No log entries for this lock.")
	}
	if target.running != nil {
		nowState, ok := ends[progressDayKey(now)]
		sections = append(sections, progressNowText(target.running, nowState, ok, chronological, now))
		sections = append(sections, progressTotalsText(target.running, chronological))
	}
	if len(dayList) > 0 {
		sections = append(sections, progressDaysText(dayList, target.running))
	}
	if truncated {
		sections = append(sections, fmt.Sprintf(
			"The log was truncated at the newest %d entries, so older days are missing.", progressLogLimit))
	}
	if target.running != nil && target.running.Hidden {
		sections = append(sections, "Hidden deck counts come from stored observations. Hidden card faces are never read.")
	}
	return strings.Join(nonEmptyStrings(sections), "\n\n")
}

// progressChronological reverses a newest-first log into draw order.
func progressChronological(entries []cs.LogEntry) []cs.LogEntry {
	out := make([]cs.LogEntry, len(entries))
	for idx := range entries {
		out[idx] = entries[len(entries)-1-idx]
	}
	return out
}

// progressHeader names the lock and its current state.
func progressHeader(target *progressTarget, now time.Time) string {
	if target == nil {
		return "No lock selected."
	}
	if target.previous != nil {
		return previousText(target.previous)
	}
	lock := target.running
	if lock == nil {
		return "No lock selected."
	}

	state := ":locked: Locked"
	switch {
	case lock.IsFrozen():
		state = ":ice_cube: Frozen"
	case lock.IsReady():
		state = ":unlocked: Ready to unlock"
	}
	visibility := "visible"
	if lock.Hidden {
		visibility = "hidden"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (ID `%s`)\n", lockName(lock), lock.ID)
	fmt.Fprintf(&b, "%s · %s", state, visibility)
	if !lock.LoadTime.IsZero() {
		fmt.Fprintf(&b, " · locked for %s", elapsedLabel(now.Sub(lock.LoadTime.Time)))
	}
	if !lock.LoadTime.IsZero() {
		fmt.Fprintf(&b, "\nLoaded: %s", fullTimestamp(lock.LoadTime.Time))
	}
	return b.String()
}

// progressNowText summarizes the live deck.
func progressNowText(lock *cs.LoadedLock, state progressDeckState, ok bool, events []cs.LogEntry, now time.Time) string {
	if lock == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("**What's left**\n")
	if !ok {
		b.WriteString("I cannot build a deck state from the log yet.")
		return b.String()
	}
	b.WriteString(progressStateText(lock, state))
	if forecast := progressForecastText(lock, state, events, now); forecast != "" {
		b.WriteString("\n")
		b.WriteString(forecast)
	}
	return b.String()
}

// progressForecastText estimates the greens left and the remaining draw time.
func progressForecastText(lock *cs.LoadedLock, state progressDeckState, events []cs.LogEntry, now time.Time) string {
	if lock == nil || lock.IsReady() {
		return ""
	}
	point, low, high, exact, ok := progressGreensLeft(lock, state, events, now)
	if !ok {
		return ""
	}
	var lines []string
	switch {
	case exact:
		lines = append(lines, fmt.Sprintf("Greens left: %d", point))
	case low == high:
		lines = append(lines, fmt.Sprintf("Greens left: ~%d", point))
	default:
		lines = append(lines, fmt.Sprintf("Greens left: ~%d (range %d to %d)", point, low, high))
	}
	if !progressCanFinish(lock, state) {
		lines = append(lines, "Draw time: not reachable with the current deck.")
	} else if min, avg, max, ok := progressDurationPrediction(lock, state, events, now); ok {
		lines = append(lines, fmt.Sprintf("Draw time: %s min · %s avg · %s max",
			elapsedLabel(min), elapsedLabel(avg), elapsedLabel(max)))
	}
	if caveat := progressCaveatText(state); caveat != "" {
		lines = append(lines, caveat)
	}
	return strings.Join(lines, "\n")
}

// progressCanFinish reports a known impossible green requirement.
func progressCanFinish(lock *cs.LoadedLock, state progressDeckState) bool {
	if lock == nil || !state.totalExact || state.allGreens {
		return true
	}
	needed := state.greensNeeded
	if needed <= 0 {
		return true
	}
	if needed > state.total {
		return false
	}
	if state.greensExact {
		return needed <= state.greens
	}
	if !state.redYellowExact {
		return true
	}
	other := state.total - state.redYellow
	return needed <= other
}

// progressCaveatText names the cards that can invalidate the estimate.
func progressCaveatText(state progressDeckState) string {
	double, reset := false, false
	if state.cards != nil {
		double = state.cards[cs.CardDoubleUp] > 0
		reset = state.cards[cs.CardReset] > 0
	} else {
		double = true
		reset = true
	}
	var parts []string
	if double {
		parts = append(parts, "A double up can extend this.")
	}
	if reset {
		parts = append(parts, "A reset rebuilds the deck, so this estimate no longer applies after one.")
	}
	return strings.Join(parts, " ")
}

// progressTotalsText shows counters that span the whole lock.
func progressTotalsText(lock *cs.LoadedLock, events []cs.LogEntry) string {
	if lock == nil {
		return ""
	}
	draws, doubles, sticky, arctic := 0, 0, 0, 0
	for _, entry := range events {
		if entry.EventType != 6 {
			continue
		}
		card, ok := progressDrawCard(entry)
		if !ok {
			continue
		}
		switch card {
		case cs.CardSticky:
			sticky++
		case cs.CardArctic:
			arctic++
		case cs.CardDoubleUp:
			doubles++
		default:
		}
		draws++
	}

	parts := []string{fmt.Sprintf("%d draw%s", draws, plural(draws))}
	if sticky > 0 {
		parts = append(parts, fmt.Sprintf("%s %d sticky", cardEmojiMarkup(cs.CardSticky), sticky))
	}
	if arctic > 0 {
		parts = append(parts, fmt.Sprintf("%s %d arctic", cardEmojiMarkup(cs.CardArctic), arctic))
	}
	if doubles > 0 {
		parts = append(parts, fmt.Sprintf("%s %d double up", cardEmojiMarkup(cs.CardDoubleUp), doubles))
	}
	text := "**This lock** · " + strings.Join(parts, " · ")
	seen := tallyProgressDay(events, nil)
	if line := seen.drawLine(); line != "" {
		text += "\nDrawn: " + line
	}
	return text
}

// progressStateText renders one deck state on one line.
func progressStateText(lock *cs.LoadedLock, state progressDeckState) string {
	if !state.totalExact {
		return "Deck size unknown."
	}

	parts := []string{fmt.Sprintf("%d cards", state.total)}
	if state.redYellowUnknown {
		parts = append(parts, "red/yellow unknown")
	} else {
		prefix := ""
		if !state.redYellowExact {
			prefix = "~"
		}
		other := state.total - state.redYellow
		parts = append(parts, fmt.Sprintf("%s%d red/yellow", prefix, state.redYellow))
		if other > 0 {
			parts = append(parts, fmt.Sprintf("%s%d other", prefix, other))
		}
		if state.total > 0 {
			parts = append(parts, fmt.Sprintf("%d%% red/yellow", (state.redYellow*100+state.total/2)/state.total))
		}
	}
	if tally := progressGreenTally(state); tally != "" {
		parts = append(parts, tally)
	}
	return strings.Join(parts, " · ")
}

// progressGreenTally renders the green progress as drawn over the goal. For an
// all-greens hidden deck the goal is unknown, so it reads 32/?.
func progressGreenTally(state progressDeckState) string {
	progress := state.greensProgress
	if state.allGreens && state.greensGoal == 0 {
		return fmt.Sprintf("%s %d/?", cardEmojiMarkup(cs.CardGreen), progress)
	}
	goal := state.greensGoal
	if goal <= 0 {
		goal = state.greensNeeded + progress
	}
	if goal <= 0 {
		return ""
	}
	progress = min(progress, goal)
	return fmt.Sprintf("%s %d/%d", cardEmojiMarkup(cs.CardGreen), progress, goal)
}

// progressDays groups events by UTC day, newest first, and attaches the state
// at the start and end of each day.
func progressDays(events []cs.LogEntry, infos []progressEventInfo, ends, starts map[string]progressDeckState, now time.Time, limit int) []progressDay {
	type bucket struct {
		keys   []string
		events map[string][]int
	}
	b := bucket{events: make(map[string][]int)}
	for idx, entry := range events {
		key := progressDayKey(entry.Timestamp.Time)
		if _, ok := b.events[key]; !ok {
			b.keys = append(b.keys, key)
		}
		b.events[key] = append(b.events[key], idx)
	}
	sort.SliceStable(b.keys, func(a, c int) bool { return b.keys[a] > b.keys[c] })

	today := progressDayKey(now)
	if limit > 0 {
		start := now.UTC().AddDate(0, 0, -(limit - 1)).Format(progressDayLayout)
		kept := b.keys[:0]
		for _, key := range b.keys {
			if key >= start {
				kept = append(kept, key)
			}
		}
		b.keys = kept
	}

	out := make([]progressDay, 0, len(b.keys))
	for _, key := range b.keys {
		indices := b.events[key]
		day := progressDay{
			key:     key,
			infos:   make([]progressEventInfo, 0, len(indices)),
			isToday: key == today,
		}
		for _, idx := range indices {
			day.events = append(day.events, events[idx])
			day.infos = append(day.infos, infos[idx])
		}
		if state, ok := ends[key]; ok {
			day.state = state
			day.hasState = true
		}
		if state, ok := starts[key]; ok {
			day.start = state
			day.hasStart = true
		}
		out = append(out, day)
	}
	return out
}

// progressDaysText renders every selected day.
func progressDaysText(days []progressDay, lock *cs.LoadedLock) string {
	if len(days) == 0 {
		return "No draws or deck changes in this window."
	}
	var sections []string
	for _, day := range days {
		sections = append(sections, progressDayText(day, lock))
	}
	return strings.Join(sections, "\n\n")
}

// progressDayText renders one day's net progress.
func progressDayText(day progressDay, lock *cs.LoadedLock) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(progressDayLabel(day.key, day.isToday))
	b.WriteString("**")
	tally := tallyProgressDay(day.events, day.infos)
	if tally.draws > 0 {
		fmt.Fprintf(&b, " · %d draw%s", tally.draws, plural(tally.draws))
	} else {
		b.WriteString(" · no draws")
	}
	if tally.reset {
		b.WriteString(" · reset")
	} else if tally.load {
		b.WriteString(" · loaded")
	}

	if !tally.reset && !tally.load && (day.hasStart || day.hasState) {
		parts := []string{fmt.Sprintf("Greens %s", signedDelta(-tally.greensNet()))}
		if tally.yellowUnknown > 0 {
			parts = append(parts, "Reds ? net")
		} else {
			// Reds is progress: positive means reds were cleared.
			parts = append(parts, fmt.Sprintf("Reds %s net", signedDelta(-tally.redsNet())))
		}
		if deck, ok := progressDeckChange(day); ok {
			parts = append(parts, fmt.Sprintf("Deck %s", signedDelta(deck)))
		} else {
			parts = append(parts, "Deck ?")
		}
		b.WriteString("\n")
		b.WriteString(strings.Join(parts, " · "))
	}
	if tally.reset {
		b.WriteString("\nDeck reset; the deck restarted from its original cards.")
	} else if tally.load {
		b.WriteString("\nLock loaded; the deck started here.")
	}

	if day.hasState && !day.isToday {
		b.WriteString("\nLeft: ")
		b.WriteString(progressStateText(lock, day.state))
	}
	if text := tally.detailText(); text != "" {
		b.WriteString("\n")
		b.WriteString(text)
	}
	return b.String()
}

// progressDeckChange reports the total deck change for one day.
func progressDeckChange(day progressDay) (int, bool) {
	if !day.hasStart || !day.hasState || !day.start.totalExact || !day.state.totalExact {
		return 0, false
	}
	return day.state.total - day.start.total, true
}

func progressDayLabel(key string, today bool) string {
	parsed, err := time.Parse(progressDayLayout, key)
	if err != nil {
		return key
	}
	if today {
		return "Today · " + parsed.Format("Mon 2 Jan")
	}
	return parsed.Format("Mon 2 Jan")
}

// progressDayTally is the factual summary of one day.
type progressDayTally struct {
	draws          int
	keyholderDraws int

	counts map[cs.Card]int

	redsDrawn    int
	greensDrawn  int
	greensBack   int
	drawnRemoved int

	yellowCount     int
	yellowRedAdd    int
	yellowRedRemove int
	yellowUnknown   int

	sticky int
	arctic int

	doubleCount   int
	doubleAdded   int
	doubleBefore  int
	doubleAfter   int
	doubleOther   int
	doubleExact   bool
	doubleUnknown bool

	explicitAdds    map[cs.Card]int
	explicitRemoves map[cs.Card]int
	putGreenBack    int

	reset       bool
	load        bool
	unknownEdit bool
	pink        bool
	notes       []string
}

func (t progressDayTally) yellowRedNet() int {
	return t.yellowRedAdd - t.yellowRedRemove
}

func (t progressDayTally) redsNet() int {
	return t.redsDrawn - t.yellowRedNet()
}

func (t progressDayTally) greensNet() int {
	return t.greensDrawn - t.greensBack
}

func tallyProgressDay(events []cs.LogEntry, infos []progressEventInfo) progressDayTally {
	tally := progressDayTally{
		counts:          make(map[cs.Card]int),
		explicitAdds:    make(map[cs.Card]int),
		explicitRemoves: make(map[cs.Card]int),
	}
	for idx, entry := range events {
		info := progressEventInfo{}
		if idx < len(infos) {
			info = infos[idx]
		}
		switch entry.EventType {
		case 6:
			card, ok := progressDrawCard(entry)
			if !ok {
				tally.draws++
				tally.notes = append(tally.notes, "One draw named an unknown card.")
				continue
			}
			tally.draws++
			if entry.Actor == 1 {
				tally.keyholderDraws++
			}
			switch card {
			case cs.CardSticky:
				tally.sticky++
				tally.counts[card]++
				continue
			case cs.CardArctic:
				tally.arctic++
				tally.counts[card]++
				continue
			case cs.CardDoubleUp:
				tally.drawnRemoved++
				tally.doubleCount++
				if info.exact {
					tally.doubleExact = true
					tally.doubleAdded += info.double
					tally.doubleBefore += info.before
					tally.doubleAfter += info.after
					tally.doubleOther += info.other
				} else {
					tally.doubleUnknown = true
				}
				tally.counts[card]++
				continue
			case cs.CardPink:
				tally.pink = true
				tally.notes = append(tally.notes, "A pink card changed the deck.")
			case cs.CardReset:
				tally.reset = true
			case cs.CardGreen:
				tally.greensDrawn++
			case cs.CardRed:
				tally.redsDrawn++
			}
			tally.drawnRemoved++
			if isYellowCard(card) {
				tally.yellowCount++
				delta, known := yellowRedDelta(card)
				if known {
					if delta > 0 {
						tally.yellowRedAdd += delta
					} else if delta < 0 {
						tally.yellowRedRemove += -delta
					}
				} else {
					tally.yellowUnknown++
					tally.notes = append(tally.notes, "A random or hidden yellow did not reveal its red change.")
				}
				continue
			}
			tally.counts[card]++
		case 15, 17:
			card, ok := progressEditCard(entry)
			amount := progressEditAmount(entry)
			if !ok {
				tally.notes = append(tally.notes, "A card addition did not name its card.")
				continue
			}
			if card == cs.CardGreen && entry.Actor == 2 {
				tally.putGreenBack += amount
				tally.greensBack += amount
				continue
			}
			tally.explicitAdds[card] += amount
		case 16, 18:
			card, ok := progressEditCard(entry)
			if !ok {
				tally.notes = append(tally.notes, "A card removal did not name its card.")
				continue
			}
			tally.explicitRemoves[card] += progressEditAmount(entry)
		case 28, 29, 30:
			tally.unknownEdit = true
			tally.notes = append(tally.notes, "A deck edit had no readable card details.")
		case 7, 9, 10, 13:
			tally.reset = true
		case 39:
			tally.load = true
		}
		if info.clamped {
			tally.notes = append(tally.notes, "A yellow removed fewer reds than it asked for because the deck ran out.")
		}
		if info.clampedRemove {
			tally.notes = append(tally.notes, "A card removal took fewer cards than it asked for because the deck ran out.")
		}
	}
	return tally
}

// detailText renders per-card counts and the double up math.
func (t progressDayTally) detailText() string {
	var lines []string
	if t.sticky > 0 || t.arctic > 0 {
		var parts []string
		if t.sticky > 0 {
			parts = append(parts, fmt.Sprintf("%s %d sticky", cardEmojiMarkup(cs.CardSticky), t.sticky))
		}
		if t.arctic > 0 {
			parts = append(parts, fmt.Sprintf("%s %d arctic", cardEmojiMarkup(cs.CardArctic), t.arctic))
		}
		lines = append(lines, "Returned: "+strings.Join(parts, " · ")+".")
	}
	if t.yellowCount > 0 {
		lines = append(lines, fmt.Sprintf("%s %d · +%d/-%d · net %s",
			cardEmojiMarkup(cs.CardYellow), t.yellowCount, t.yellowRedAdd, t.yellowRedRemove, signedDelta(t.yellowRedNet())))
	}
	if t.doubleCount > 0 {
		if t.doubleExact && t.doubleCount == 1 {
			lines = append(lines, fmt.Sprintf("%s %d -> %d · %s red/yellow · %d other",
				cardEmojiMarkup(cs.CardDoubleUp), t.doubleBefore, t.doubleAfter, signedDelta(t.doubleAdded), t.doubleOther))
		} else if t.doubleExact {
			lines = append(lines, fmt.Sprintf("%s %d double ups · %s red/yellow",
				cardEmojiMarkup(cs.CardDoubleUp), t.doubleCount, signedDelta(t.doubleAdded)))
		} else {
			lines = append(lines, fmt.Sprintf("%s counts around this draw were not stored",
				cardEmojiMarkup(cs.CardDoubleUp)))
		}
	}
	if t.putGreenBack > 0 {
		lines = append(lines, fmt.Sprintf("Put green back: %s +%d.", cardEmojiMarkup(cs.CardGreen), t.putGreenBack))
	}
	if len(t.explicitAdds) > 0 {
		lines = append(lines, "Added: "+progressCardCounts(t.explicitAdds, "+")+".")
	}
	if len(t.explicitRemoves) > 0 {
		lines = append(lines, "Removed: "+progressCardCounts(t.explicitRemoves, "-")+".")
	}
	lines = append(lines, t.notes...)
	return strings.Join(lines, "\n")
}

// drawLine renders the day's draw counts.
func (t progressDayTally) drawLine() string {
	var parts []string
	order := []cs.Card{
		cs.CardGreen, cs.CardRed, cs.CardYellow, cs.CardFreeze, cs.CardArctic,
		cs.CardStun, cs.CardSticky, cs.CardDoubleUp, cs.CardReset, cs.CardDice,
		cs.CardPink, cs.CardGoAgain,
	}
	for _, card := range order {
		if card == cs.CardSticky || card == cs.CardArctic {
			continue
		}
		if card == cs.CardYellow {
			if t.yellowCount > 0 {
				parts = append(parts, fmt.Sprintf("%s -%d", cardEmojiMarkup(card), t.yellowCount))
			}
			continue
		}
		if count := t.counts[card]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s -%d", cardEmojiMarkup(card), count))
		}
	}
	if t.keyholderDraws > 0 {
		parts = append(parts, fmt.Sprintf(":key: keyholder drew %d", t.keyholderDraws))
	}
	if len(parts) == 0 {
		return "No draws."
	}
	return strings.Join(parts, " · ")
}

func progressCardCounts(counts map[cs.Card]int, sign string) string {
	var parts []string
	seen := make(map[cs.Card]bool, len(counts))
	for _, card := range deckCardOrder {
		seen[card] = true
		if count := counts[card]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %s%d", cardEmojiMarkup(card), sign, count))
		}
	}
	extra := make([]cs.Card, 0, len(counts))
	for card, count := range counts {
		if !seen[card] && count > 0 {
			extra = append(extra, card)
		}
	}
	sort.Slice(extra, func(a, b int) bool { return string(extra[a]) < string(extra[b]) })
	for _, card := range extra {
		parts = append(parts, fmt.Sprintf("%s %s%d", cardEmojiMarkup(card), sign, counts[card]))
	}
	return strings.Join(parts, " · ")
}

func signedRemoval(count int) string {
	if count == 0 {
		return "0"
	}
	return fmt.Sprintf("-%d", count)
}

func signedDelta(value int) string {
	if value == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", value)
}

func intOptionOr(options []*discordgo.ApplicationCommandInteractionDataOption, name string, fallback int) int {
	for _, option := range options {
		if option.Name == name {
			return int(option.IntValue())
		}
	}
	return fallback
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func intPtrFloat(value int) *float64 {
	out := float64(value)
	return &out
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

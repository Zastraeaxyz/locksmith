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
	exact bool
	rough bool

	cards cs.Deck

	total     int
	totalLow  int
	totalHigh int

	greens     int
	greensLow  int
	greensHigh int

	redYellow     int
	redYellowLow  int
	redYellowHigh int

	greensNeeded int
	allGreens    bool
	unknownSize  bool

	notes []string
}

// progressEventInfo carries the parts of one event that the daily summary
// cannot recover from the log alone, such as the number of cards a double up
// added.
type progressEventInfo struct {
	hasDouble  bool
	double     int
	doubleLow  int
	doubleHigh int
	exact      bool

	clamped       bool
	clampedRemove bool
}

// progressDay is one UTC day of the report.
type progressDay struct {
	key      string
	events   []cs.LogEntry
	infos    []progressEventInfo
	state    progressDeckState
	hasState bool
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

	entries, truncated, err := f.fetchProgressLog(ctx, client, target.id())
	if err != nil {
		f.log.Warn("loading progress log", "user_id", userID, "lock_id", target.id().String(), "err", err)
		header := progressHeader(target, now)
		return bot.EditChunks(s, i, header+"\n\nI could not load the log for that lock.", !public)
	}

	report := buildProgressReport(target, entries, truncated, days, now, note)
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
func buildProgressReport(target *progressTarget, entries []cs.LogEntry, truncated bool, days int, now time.Time, note string) string {
	header := progressHeader(target, now)
	if len(entries) == 0 {
		sections := []string{header, "No log entries for this lock."}
		if target.running != nil {
			state, ok := progressLiveState(target.running)
			sections = append(sections, progressNowText(target.running, state, ok))
		}
		return strings.Join(sections, "\n\n")
	}

	chronological := progressChronological(entries)
	infos := make([]progressEventInfo, len(chronological))
	states := make(map[string]progressDeckState)
	if target.running != nil {
		states = progressStates(target.running, chronological, infos, now)
	}
	dayList := progressDays(chronological, infos, states, now, days)

	var sections []string
	sections = append(sections, header)
	if note != "" {
		sections = append(sections, note)
	}
	if target.running != nil {
		nowState, ok := states[progressDayKey(now)]
		sections = append(sections, progressNowText(target.running, nowState, ok))
		sections = append(sections, progressTotalsText(target.running, chronological))
	}
	sections = append(sections, progressDaysText(dayList, target.running))
	if truncated {
		sections = append(sections, fmt.Sprintf(
			"The log was truncated at the newest %d entries, so older days are missing.", progressLogLimit))
	}
	if target.running != nil && target.running.Hidden {
		sections = append(sections, "Hidden deck estimates use only the cards seen in the log. The deck itself is never read.")
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

// progressNowText summarizes the live deck, or the best estimate for a hidden
// one. ok is false when no deck state could be built.
func progressNowText(lock *cs.LoadedLock, state progressDeckState, ok bool) string {
	if lock == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("**What's left now**\n")
	if !ok {
		b.WriteString("I cannot estimate the deck from the log yet.")
		return b.String()
	}
	b.WriteString(progressStateText(lock, state))
	return b.String()
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
	return "**This lock** · " + strings.Join(parts, " · ")
}

// progressStateText renders one deck state.
func progressStateText(lock *cs.LoadedLock, state progressDeckState) string {
	var b strings.Builder
	if state.unknownSize {
		b.WriteString("Deck size unknown.")
		return b.String()
	}
	if state.exact {
		fmt.Fprintf(&b, "%d card%s", state.total, plural(state.total))
		if state.total > 0 {
			fmt.Fprintf(&b, " · %s %d", cardEmojiMarkup(cs.CardGreen), state.greens)
			fmt.Fprintf(&b, " · %s %d", cardEmojiMarkup(cs.CardRed), state.cards.Reds())
			fmt.Fprintf(&b, " · %s %d", cardEmojiMarkup(cs.CardYellow), state.cards.Yellows())
			other := state.total - state.greens - state.cards.Reds() - state.cards.Yellows()
			if other > 0 {
				fmt.Fprintf(&b, " · other %d", other)
			}
		}
	} else {
		fmt.Fprintf(&b, "About %d card%s", state.total, plural(state.total))
		if state.totalLow != state.totalHigh {
			fmt.Fprintf(&b, " (range %d to %d)", state.totalLow, state.totalHigh)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s Greens: ", cardEmojiMarkup(cs.CardGreen))
		if state.allGreens {
			fmt.Fprintf(&b, "about %d left (range %d to %d)", state.greens, state.greensLow, state.greensHigh)
		} else if state.greensNeeded > 0 {
			fmt.Fprintf(&b, "%d needed, at least %d left", state.greensNeeded, state.greensNeeded)
		} else if state.greens > 0 {
			fmt.Fprintf(&b, "about %d left (range %d to %d)", state.greens, state.greensLow, state.greensHigh)
		} else {
			b.WriteString("requirement met")
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s Reds and yellows: about %d", cardEmojiMarkup(cs.CardYellow), state.redYellow)
		if state.redYellowLow != state.redYellowHigh {
			fmt.Fprintf(&b, " (range %d to %d)", state.redYellowLow, state.redYellowHigh)
		}
	}

	if state.allGreens {
		b.WriteString("\nAll greens required.")
	} else if state.exact && state.greensNeeded > 0 {
		fmt.Fprintf(&b, "\nGreens needed: %d.", state.greensNeeded)
	}
	if state.exact {
		b.WriteString("\nExact from the visible deck.")
	} else if state.rough {
		b.WriteString("\nRough estimate.")
	} else {
		b.WriteString("\nEstimated from the deck size and the cards seen.")
	}
	if len(state.notes) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(state.notes, " "))
	}
	return b.String()
}

// progressDays groups events by UTC day, newest first, and attaches the state
// at the end of each day.
func progressDays(events []cs.LogEntry, infos []progressEventInfo, states map[string]progressDeckState, now time.Time, limit int) []progressDay {
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
		if state, ok := states[key]; ok {
			day.state = state
			day.hasState = true
		}
		out = append(out, day)
	}
	return out
}

// progressDaysText renders every selected day followed by the report caveats.
func progressDaysText(days []progressDay, lock *cs.LoadedLock) string {
	if len(days) == 0 {
		return "No draws or deck changes in this window."
	}
	var sections []string
	sections = append(sections, "**By day (UTC)**")
	for _, day := range days {
		sections = append(sections, progressDayText(day, lock))
	}
	return strings.Join(sections, "\n\n")
}

// progressDayText renders one day's draws, deck changes, and closing state.
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
	if tally.draws > 0 {
		b.WriteString("\n")
		b.WriteString(tally.drawLine())
	}
	if tally.sticky > 0 || tally.arctic > 0 {
		b.WriteString("\nReturned: ")
		var parts []string
		if tally.sticky > 0 {
			parts = append(parts, fmt.Sprintf("%s %d sticky", cardEmojiMarkup(cs.CardSticky), tally.sticky))
		}
		if tally.arctic > 0 {
			parts = append(parts, fmt.Sprintf("%s %d arctic", cardEmojiMarkup(cs.CardArctic), tally.arctic))
		}
		b.WriteString(strings.Join(parts, " · "))
	}

	changeLines := tally.changeLines()
	if len(changeLines) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(changeLines, "\n"))
	}

	if day.hasState && !day.isToday {
		b.WriteString("\nLeft at end of day: ")
		b.WriteString(progressStateText(lock, day.state))
	}
	return b.String()
}

func progressDayLabel(key string, today bool) string {
	parsed, err := time.Parse(progressDayLayout, key)
	if err != nil {
		return key
	}
	if today {
		return "Today · " + parsed.Format("Mon 2 Jan")
	}
	return parsed.Format("Mon 2 Jan 2006")
}

// progressDayTally is the factual summary of one day.
type progressDayTally struct {
	draws          int
	keyholderDraws int

	counts          map[cs.Card]int
	yellowCount     int
	yellowRedAdd    int
	yellowRedRemove int
	yellowUnknown   int
	sticky          int
	arctic          int

	removed         int
	drawnRemoved    int
	explicitRemoved int
	added           int

	doubleCount   int
	doubleAdded   int
	doubleLow     int
	doubleHigh    int
	doubleRough   bool
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

// tallyProgressDay counts one day's cards and deck changes.
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
				tally.removed++
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
				tally.doubleCount++
				tally.drawnRemoved++
				if info.hasDouble {
					tally.doubleAdded += info.double
					tally.doubleLow += info.doubleLow
					tally.doubleHigh += info.doubleHigh
					tally.added += info.double
					if !info.exact {
						tally.doubleRough = true
					}
				} else {
					tally.doubleRough = true
					tally.doubleUnknown = true
				}
				tally.removed++
				tally.counts[card]++
				continue
			case cs.CardPink:
				tally.pink = true
				tally.notes = append(tally.notes, "A pink card changed the deck.")
			case cs.CardReset:
				tally.reset = true
			}

			tally.drawnRemoved++
			if isYellowCard(card) {
				tally.yellowCount++
				delta, known := yellowRedDelta(card)
				if known {
					if delta > 0 {
						tally.yellowRedAdd += delta
						tally.added += delta
					} else if delta < 0 {
						tally.yellowRedRemove += -delta
						tally.removed += -delta
					}
				} else {
					tally.yellowUnknown++
					tally.notes = append(tally.notes, "A random or hidden yellow did not reveal its red change.")
				}
				tally.removed++
				continue
			}

			tally.removed++
			tally.counts[card]++
		case 15, 17:
			card, ok := progressEditCard(entry)
			amount := progressEditAmount(entry)
			if !ok {
				tally.added += amount
				tally.notes = append(tally.notes, "A card addition did not name its card.")
				continue
			}
			tally.added += amount
			if card == cs.CardGreen && entry.Actor == 2 {
				tally.putGreenBack += amount
				continue
			}
			tally.explicitAdds[card] += amount
		case 16, 18:
			card, ok := progressEditCard(entry)
			amount := progressEditAmount(entry)
			if !ok {
				tally.removed += amount
				tally.notes = append(tally.notes, "A card removal did not name its card.")
				continue
			}
			tally.removed += amount
			tally.explicitRemoved += amount
			tally.explicitRemoves[card] += amount
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

// drawLine renders the day's draw counts as one compact line.
func (t progressDayTally) drawLine() string {
	var parts []string
	order := []cs.Card{
		cs.CardGreen, cs.CardRed, cs.CardYellow, cs.CardFreeze, cs.CardArctic,
		cs.CardStun, cs.CardSticky, cs.CardDoubleUp, cs.CardReset, cs.CardDice,
		cs.CardPink, cs.CardGoAgain,
	}
	for _, card := range order {
		switch card {
		case cs.CardYellow:
			if t.yellowCount == 0 {
				continue
			}
			label := fmt.Sprintf("%s %d", cardEmojiMarkup(cs.CardYellow), t.yellowCount)
			if t.yellowRedAdd > 0 {
				label += fmt.Sprintf(" (+%d red)", t.yellowRedAdd)
			}
			if t.yellowRedRemove > 0 {
				label += fmt.Sprintf(" (-%d red)", t.yellowRedRemove)
			}
			if t.yellowUnknown > 0 {
				label += " (+? red)"
			}
			parts = append(parts, label)
			continue
		default:
			if count := t.counts[card]; count > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", cardEmojiMarkup(card), count))
			}
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

// changeLines renders the day's additions, removals, and net change.
func (t progressDayTally) changeLines() []string {
	var lines []string

	if t.reset || t.load {
		if t.load {
			lines = append(lines, "Lock loaded; the deck started here.")
		}
		if t.reset {
			lines = append(lines, "Deck reset; the deck restarted from its original cards.")
		}
	}
	if t.drawnRemoved > 0 {
		lines = append(lines, fmt.Sprintf("Removed: %d drawn card%s.", t.drawnRemoved, plural(t.drawnRemoved)))
	}
	if t.doubleCount > 0 {
		switch {
		case t.doubleUnknown:
			lines = append(lines, fmt.Sprintf("%s %d double up changed the deck; the number added is unknown.",
				cardEmojiMarkup(cs.CardDoubleUp), t.doubleCount))
		case !t.doubleRough:
			lines = append(lines, fmt.Sprintf("%s %d double up added %d card%s.",
				cardEmojiMarkup(cs.CardDoubleUp), t.doubleCount, t.doubleAdded, plural(t.doubleAdded)))
		default:
			lines = append(lines, fmt.Sprintf("%s %d double up added about %d card%s (range %d to %d).",
				cardEmojiMarkup(cs.CardDoubleUp), t.doubleCount, t.doubleAdded, plural(t.doubleAdded), t.doubleLow, t.doubleHigh))
		}
	}
	if t.yellowRedAdd > 0 {
		lines = append(lines, fmt.Sprintf("%s Yellow cards added %d red%s.", cardEmojiMarkup(cs.CardYellow), t.yellowRedAdd, plural(t.yellowRedAdd)))
	}
	if t.yellowRedRemove > 0 {
		lines = append(lines, fmt.Sprintf("%s Yellow cards removed %d red%s.", cardEmojiMarkup(cs.CardYellow), t.yellowRedRemove, plural(t.yellowRedRemove)))
	}
	if t.putGreenBack > 0 {
		lines = append(lines, fmt.Sprintf("Put green back: %s %d.", cardEmojiMarkup(cs.CardGreen), t.putGreenBack))
	}
	if len(t.explicitAdds) > 0 {
		lines = append(lines, "Added: "+progressCardCounts(t.explicitAdds)+".")
	}
	if len(t.explicitRemoves) > 0 {
		lines = append(lines, "Removed: "+progressCardCounts(t.explicitRemoves)+".")
	}

	if t.yellowUnknown > 0 && !t.reset && !t.load && !t.unknownEdit && !t.pink && !t.doubleUnknown {
		lines = append(lines, "Net: unknown because a yellow's effect was hidden.")
	}
	if !t.reset && !t.load && !t.unknownEdit && !t.pink && !t.doubleUnknown && t.yellowUnknown == 0 {
		net := t.added - t.removed
		if net != 0 || t.doubleRough {
			label := fmt.Sprintf("Net: %+d card%s.", net, plural(absInt(net)))
			if t.doubleRough {
				low := t.added - t.doubleAdded + t.doubleLow - t.removed
				high := t.added - t.doubleAdded + t.doubleHigh - t.removed
				word := "cards"
				if absInt(net) == 1 {
					word = "card"
				}
				label = fmt.Sprintf("Net: about %+d %s (range %+d to %+d).", net, word, low, high)
			}
			lines = append(lines, label)
		}
	}
	if t.unknownEdit {
		lines = append(lines, "Deck edited; later estimates are rough.")
	}
	if len(t.notes) > 0 {
		lines = append(lines, strings.Join(t.notes, " "))
	}
	return lines
}

func progressCardCounts(counts map[cs.Card]int) string {
	var parts []string
	seen := make(map[cs.Card]bool, len(counts))
	for _, card := range deckCardOrder {
		seen[card] = true
		if count := counts[card]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", cardEmojiMarkup(card), count))
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
		parts = append(parts, fmt.Sprintf("%s %d", cardEmojiMarkup(card), counts[card]))
	}
	return strings.Join(parts, " · ")
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

package feature

// The deck model underneath progress reports. It walks a lock log forwards to
// estimate a hidden deck, and backwards from a visible deck to recover exact
// daily states. The command and its rendering live in progress.go.

import (
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

// progressStates builds the end-of-day state for every day with events. Exact
// reconstruction is used when the live deck is visible. A hidden deck, or a
// log that cannot be walked back because of an unknown card, falls back to the
// estimate path.
func progressStates(lock *cs.LoadedLock, events []cs.LogEntry, infos []progressEventInfo, now time.Time) map[string]progressDeckState {
	states := make(map[string]progressDeckState)
	segments := progressSegments(events)

	if lock != nil && !lock.Hidden && lock.Deck != nil {
		if progressExactStates(lock, events, segments, infos, states) {
			key := progressDayKey(now)
			needed := progressCurrentGreensNeeded(lock, events)
			states[key] = exactProgressState(lock.Deck, lock, needed)
			return states
		}
		// The exact walk failed. Rebuild every day as an estimate, then make
		// the current day exact from the live deck.
		states = make(map[string]progressDeckState)
	}

	progressEstimateStates(lock, events, segments, infos, states)
	key := progressDayKey(now)
	if _, ok := states[key]; !ok {
		if latest, ok := latestProgressState(states, key); ok {
			states[key] = latest
		} else if lock != nil {
			needed := progressCurrentGreensNeeded(lock, events)
			est := newProgressEstimate(lock)
			states[key] = est.state(lock, needed)
		}
	}
	if lock != nil && !lock.Hidden && lock.Deck != nil {
		needed := progressCurrentGreensNeeded(lock, events)
		states[key] = exactProgressState(lock.Deck, lock, needed)
	}
	return states
}

// progressLiveState builds the current state without a log. A visible deck is
// exact; a hidden deck uses the configured size and the greens requirement.
func progressLiveState(lock *cs.LoadedLock) (progressDeckState, bool) {
	if lock == nil {
		return progressDeckState{}, false
	}
	needed := progressGreensNeeded(lock, 0, 0)
	if !lock.Hidden && lock.Deck != nil {
		return exactProgressState(lock.Deck, lock, needed), true
	}
	est := newProgressEstimate(lock)
	return est.state(lock, needed), true
}

// latestProgressState returns the newest day state at or before the given key.
func latestProgressState(states map[string]progressDeckState, today string) (progressDeckState, bool) {
	best := ""
	for key := range states {
		if key > today {
			continue
		}
		if best == "" || key > best {
			best = key
		}
	}
	if best == "" {
		return progressDeckState{}, false
	}
	return states[best], true
}

// progressExactStates walks the visible current deck backwards through each
// segment, then forwards from the recovered original deck to record every day.
func progressExactStates(lock *cs.LoadedLock, events []cs.LogEntry, segments []progressSegment, infos []progressEventInfo, states map[string]progressDeckState) bool {
	if len(segments) == 0 {
		return true
	}
	// Walk the events after the newest reset back to that reset. A reset
	// restores the original deck, so that recovered composition is also the
	// start of every older segment. An empty current segment means the live
	// deck is already the restored original.
	lastBoundary := -1
	for idx, entry := range events {
		if isDeckReset(entry.EventType) {
			lastBoundary = idx
		}
	}
	currentStart := lastBoundary + 1
	original := lock.Deck.Clone()
	if currentStart < len(events) {
		var ok bool
		original, ok = reverseProgressSegment(lock.Deck.Clone(), events[currentStart:])
		if !ok {
			return false
		}
	}

	for _, seg := range segments {
		segmentStates := make(map[string]progressDeckState)
		end, ok := forwardProgressExact(lock, original.Clone(), events[seg.from:seg.to], infos, segmentStates, seg.from)
		if !ok {
			return false
		}
		if seg.from == currentStart && seg.to == len(events) && !progressSameDeck(end, lock.Deck) {
			return false
		}
		for key, state := range segmentStates {
			states[key] = state
		}
	}
	return true
}

// progressEstimateStates simulates each segment from the configured deck size.
func progressEstimateStates(lock *cs.LoadedLock, events []cs.LogEntry, segments []progressSegment, infos []progressEventInfo, states map[string]progressDeckState) {
	for _, seg := range segments {
		est := newProgressEstimate(lock)
		greensDrawn, greensPutBack := 0, 0
		for idx := seg.from; idx < seg.to; idx++ {
			entry := events[idx]
			info := est.apply(lock, entry)
			infos[idx] = info
			if entry.EventType == 6 {
				if card, ok := progressDrawCard(entry); ok && card == cs.CardGreen {
					greensDrawn++
				}
			}
			if (entry.EventType == 15 || entry.EventType == 17) && entry.Actor == 2 {
				if card, ok := progressEditCard(entry); ok && card == cs.CardGreen {
					greensPutBack += progressEditAmount(entry)
				}
			}
			if progressDayEnd(events, idx, seg.to) {
				needed := progressGreensNeeded(lock, greensDrawn, greensPutBack)
				states[progressDayKey(entry.Timestamp.Time)] = est.state(lock, needed)
			}
		}
	}
}

// progressSegment is one deck lifetime between load and reset events.
type progressSegment struct {
	from int
	to   int
}

func progressSegments(events []cs.LogEntry) []progressSegment {
	firstBoundary := -1
	for idx, entry := range events {
		if isDeckReset(entry.EventType) {
			firstBoundary = idx
			break
		}
	}
	if firstBoundary < 0 {
		return nil
	}

	var out []progressSegment
	start := firstBoundary + 1
	for idx := start; idx < len(events); idx++ {
		if !isDeckReset(events[idx].EventType) {
			continue
		}
		if idx > start {
			out = append(out, progressSegment{from: start, to: idx})
		}
		start = idx + 1
	}
	if start < len(events) {
		out = append(out, progressSegment{from: start, to: len(events)})
	}
	return out
}

func progressDayEnd(events []cs.LogEntry, idx, end int) bool {
	if idx+1 >= end {
		return true
	}
	return progressDayKey(events[idx].Timestamp.Time) != progressDayKey(events[idx+1].Timestamp.Time)
}

// forwardProgressExact applies the segment from a recovered original deck and
// records each day's closing state.
func forwardProgressExact(lock *cs.LoadedLock, start cs.Deck, events []cs.LogEntry, infos []progressEventInfo, states map[string]progressDeckState, base int) (cs.Deck, bool) {
	deck := start.Clone()
	greensDrawn, greensPutBack := 0, 0
	for idx, entry := range events {
		info, ok := applyProgressExact(deck, entry)
		if !ok {
			return nil, false
		}
		infos[base+idx] = info
		if entry.EventType == 6 {
			if card, ok := progressDrawCard(entry); ok && card == cs.CardGreen {
				greensDrawn++
			}
		}
		if (entry.EventType == 15 || entry.EventType == 17) && entry.Actor == 2 {
			if card, ok := progressEditCard(entry); ok && card == cs.CardGreen {
				greensPutBack += progressEditAmount(entry)
			}
		}
		if progressDayEnd(events, idx, len(events)) {
			needed := progressGreensNeeded(lock, greensDrawn, greensPutBack)
			states[progressDayKey(entry.Timestamp.Time)] = exactProgressState(deck, lock, needed)
		}
	}
	return deck, true
}

// progressSameDeck reports whether two compositions are equal.
func progressSameDeck(left, right cs.Deck) bool {
	if left.Total() != right.Total() {
		return false
	}
	for card, count := range left {
		if count != 0 && right[card] != count {
			return false
		}
	}
	for card, count := range right {
		if count != 0 && left[card] != count {
			return false
		}
	}
	return true
}

// reverseProgressSegment undoes a segment from its end to its start.
func reverseProgressSegment(end cs.Deck, events []cs.LogEntry) (cs.Deck, bool) {
	deck := end.Clone()
	for idx := len(events) - 1; idx >= 0; idx-- {
		if !reverseProgressEvent(deck, events[idx]) {
			return nil, false
		}
	}
	return deck, true
}

// applyProgressExact applies one event to an exact deck.
func applyProgressExact(deck cs.Deck, entry cs.LogEntry) (progressEventInfo, bool) {
	var info progressEventInfo
	switch entry.EventType {
	case 6:
		card, ok := progressDrawCard(entry)
		if !ok {
			return info, false
		}
		if card == cs.CardSticky || card == cs.CardArctic {
			return info, true
		}
		if deck[card] <= 0 {
			return info, false
		}
		switch card {
		case cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3:
			deck[card]--
			n := yellowRedAmount(card)
			deck[cs.CardRed] += n
		case cs.CardYellowM1, cs.CardYellowM2:
			deck[card]--
			n := yellowRedAmount(card)
			removed := min(n, deck[cs.CardRed])
			deck[cs.CardRed] -= removed
			info.clamped = removed < n
		case cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN:
			return info, false
		case cs.CardDoubleUp:
			deck[cs.CardDoubleUp]--
			added := deck.Reds() + deck.Yellows()
			deck[cs.CardRed] *= 2
			for _, yellow := range yellowCards {
				deck[yellow] *= 2
			}
			info.hasDouble = true
			info.double = added
			info.doubleLow = added
			info.doubleHigh = added
			info.exact = true
		case cs.CardPink:
			deck[cs.CardPink]--
			return info, false
		default:
			deck[card]--
		}
		return info, true
	case 15, 17:
		card, ok := progressEditCard(entry)
		if !ok {
			return info, false
		}
		amount := progressEditAmount(entry)
		deck[card] += amount
		return info, true
	case 16, 18:
		card, ok := progressEditCard(entry)
		if !ok {
			return info, false
		}
		amount := progressEditAmount(entry)
		removed := min(amount, deck[card])
		deck[card] -= removed
		info.clampedRemove = removed < amount
		return info, true
	case 28, 29, 30:
		return info, false
	case 7, 9, 10, 13, 39:
		return info, false
	default:
		return info, true
	}
}

// reverseProgressEvent undoes one event on an exact deck.
func reverseProgressEvent(deck cs.Deck, entry cs.LogEntry) bool {
	switch entry.EventType {
	case 6:
		card, ok := progressDrawCard(entry)
		if !ok {
			return false
		}
		if card == cs.CardSticky || card == cs.CardArctic {
			return true
		}
		switch card {
		case cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3:
			n := yellowRedAmount(card)
			if deck[cs.CardRed] < n {
				return false
			}
			deck[cs.CardRed] -= n
			deck[card]++
		case cs.CardYellowM1, cs.CardYellowM2:
			n := yellowRedAmount(card)
			deck[cs.CardRed] += n
			deck[card]++
		case cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN:
			return false
		case cs.CardDoubleUp:
			if deck.Reds()%2 != 0 || deck.Yellows()%2 != 0 {
				return false
			}
			deck[cs.CardRed] /= 2
			for _, yellow := range yellowCards {
				if deck[yellow]%2 != 0 {
					return false
				}
				deck[yellow] /= 2
			}
			deck[cs.CardDoubleUp]++
		case cs.CardPink:
			return false
		default:
			deck[card]++
		}
		return true
	case 15, 17:
		card, ok := progressEditCard(entry)
		if !ok {
			return false
		}
		amount := progressEditAmount(entry)
		if deck[card] < amount {
			return false
		}
		deck[card] -= amount
		return true
	case 16, 18:
		card, ok := progressEditCard(entry)
		if !ok {
			return false
		}
		deck[card] += progressEditAmount(entry)
		return true
	case 28, 29, 30:
		return false
	case 7, 9, 10, 13, 39:
		return false
	default:
		return true
	}
}

// progressEstimate is the hidden-deck model. Point values follow the known
// changes; bounds widen when a double up or an unknown card makes the exact
// number unknowable.
type progressEstimate struct {
	total     int
	totalLow  int
	totalHigh int

	greens     int
	greensLow  int
	greensHigh int

	redYellow     int
	redYellowLow  int
	redYellowHigh int

	rough   bool
	unknown bool
	notes   []string
}

func newProgressEstimate(lock *cs.LoadedLock) progressEstimate {
	// CardCount is the size of the deck when the lock loaded or reset, not the
	// number of cards left. The log supplies everything removed or added since.
	size := 0
	required := 0
	if lock != nil {
		size = lock.CardCount
		required = lock.GreensRequired
	}
	if size <= 0 {
		return progressEstimate{unknown: true, notes: []string{"No deck size was reported."}}
	}

	greensLow, greensHigh := 0, size
	greens := size / 3
	if required > 1000 {
		greensLow = 1
		if greens < 1 {
			greens = 1
		}
	} else if required > 0 {
		greensLow = min(required, size)
		greens = max(greensLow, size/3)
	}

	redYellowLow := 0
	redYellowHigh := max(0, size-greensLow)
	redYellow := max(0, (size-greens)/2)

	est := progressEstimate{
		total:         size,
		totalLow:      size,
		totalHigh:     size,
		greens:        greens,
		greensLow:     greensLow,
		greensHigh:    greensHigh,
		redYellow:     redYellow,
		redYellowLow:  redYellowLow,
		redYellowHigh: redYellowHigh,
	}
	est.clamp()
	return est
}

func (e progressEstimate) state(lock *cs.LoadedLock, greensNeeded int) progressDeckState {
	allGreens := lock != nil && lock.GreensRequired > 1000
	return progressDeckState{
		exact:         false,
		rough:         e.rough,
		total:         e.total,
		totalLow:      e.totalLow,
		totalHigh:     e.totalHigh,
		greens:        e.greens,
		greensLow:     e.greensLow,
		greensHigh:    e.greensHigh,
		redYellow:     e.redYellow,
		redYellowLow:  e.redYellowLow,
		redYellowHigh: e.redYellowHigh,
		greensNeeded:  greensNeeded,
		allGreens:     allGreens,
		unknownSize:   e.unknown,
		notes:         append([]string(nil), e.notes...),
	}
}

// apply updates the hidden-deck estimate for one event.
func (e *progressEstimate) apply(lock *cs.LoadedLock, entry cs.LogEntry) progressEventInfo {
	var info progressEventInfo
	if e.unknown {
		return info
	}
	switch entry.EventType {
	case 6:
		card, ok := progressDrawCard(entry)
		if !ok {
			e.total--
			e.rough = true
			e.notes = append(e.notes, "One draw named an unknown card.")
			e.clamp()
			return info
		}
		if card == cs.CardSticky || card == cs.CardArctic {
			return info
		}
		switch card {
		case cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3:
			n := yellowRedAmount(card)
			e.total += n - 1
			e.totalLow += n - 1
			e.totalHigh += n - 1
			e.redYellow += n
			e.redYellowLow += n
			e.redYellowHigh += n
		case cs.CardYellowM1, cs.CardYellowM2:
			n := yellowRedAmount(card)
			e.total -= n + 1
			e.totalLow -= n + 1
			e.totalHigh -= n + 1
			e.redYellow -= n
			e.redYellowLow -= n
			e.redYellowHigh -= n
		case cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN:
			e.total--
			e.totalLow -= 3
			e.totalHigh += 2
			e.redYellowLow -= 2
			e.redYellowHigh += 3
			e.rough = true
			e.notes = append(e.notes, "A random or hidden yellow did not reveal its red change.")
		case cs.CardDoubleUp:
			added := e.redYellow
			addedLow := e.redYellowLow
			addedHigh := e.redYellowHigh
			e.total += added - 1
			e.totalLow += addedLow - 1
			e.totalHigh += addedHigh - 1
			e.redYellow *= 2
			e.redYellowLow *= 2
			e.redYellowHigh *= 2
			e.rough = true
			e.notes = append(e.notes, "Double up added an estimated number of reds and yellows.")
			info.hasDouble = true
			info.double = added
			info.doubleLow = addedLow
			info.doubleHigh = addedHigh
			info.exact = false
		case cs.CardPink:
			e.total--
			e.rough = true
			e.notes = append(e.notes, "A pink card changed the deck.")
		default:
			e.total--
			e.totalLow--
			e.totalHigh--
			switch card {
			case cs.CardGreen:
				e.greens--
				e.greensLow--
				e.greensHigh--
			case cs.CardRed:
				e.redYellow--
				e.redYellowLow--
				e.redYellowHigh--
			}
		}
	case 15, 17:
		amount := progressEditAmount(entry)
		card, ok := progressEditCard(entry)
		e.total += amount
		e.totalLow += amount
		e.totalHigh += amount
		if ok {
			switch {
			case card == cs.CardGreen:
				e.greens += amount
				e.greensLow += amount
				e.greensHigh += amount
			case card == cs.CardRed || isYellowCard(card):
				e.redYellow += amount
				e.redYellowLow += amount
				e.redYellowHigh += amount
			}
		} else {
			e.rough = true
			e.notes = append(e.notes, "A card addition did not name its card.")
		}
	case 16, 18:
		amount := progressEditAmount(entry)
		card, ok := progressEditCard(entry)
		e.total -= amount
		e.totalLow -= amount
		e.totalHigh -= amount
		if ok {
			switch {
			case card == cs.CardGreen:
				e.greens -= amount
				e.greensLow -= amount
				e.greensHigh -= amount
			case card == cs.CardRed || isYellowCard(card):
				e.redYellow -= amount
				e.redYellowLow -= amount
				e.redYellowHigh -= amount
			}
		} else {
			e.rough = true
			e.notes = append(e.notes, "A card removal did not name its card.")
		}
	case 28, 29, 30:
		e.rough = true
		e.notes = append(e.notes, "A deck edit had no readable card details.")
	}
	e.clamp()
	return info
}

func (e *progressEstimate) clamp() {
	if e.total < 0 {
		e.total = 0
	}
	if e.totalLow < 0 {
		e.totalLow = 0
	}
	if e.totalHigh < e.totalLow {
		e.totalHigh = e.totalLow
	}
	if e.total < e.totalLow {
		e.total = e.totalLow
	}
	if e.total > e.totalHigh {
		e.total = e.totalHigh
	}

	e.greens = clampInt(e.greens, 0, e.total)
	e.greensLow = clampInt(e.greensLow, 0, e.totalHigh)
	e.greensHigh = clampInt(e.greensHigh, e.greensLow, e.totalHigh)

	e.redYellow = clampInt(e.redYellow, 0, max(0, e.total-e.greens))
	e.redYellowLow = clampInt(e.redYellowLow, 0, max(0, e.totalHigh-e.greensLow))
	e.redYellowHigh = clampInt(e.redYellowHigh, e.redYellowLow, max(0, e.totalHigh-e.greensLow))
	if e.redYellow < e.redYellowLow {
		e.redYellow = e.redYellowLow
	}
	if e.redYellow > e.redYellowHigh {
		e.redYellow = e.redYellowHigh
	}
}

// exactProgressState derives a state from an exact deck composition.
func exactProgressState(deck cs.Deck, lock *cs.LoadedLock, greensNeeded int) progressDeckState {
	total := deck.Total()
	greens := deck.Greens()
	reds := deck.Reds()
	yellows := deck.Yellows()
	ry := reds + yellows
	allGreens := lock != nil && lock.GreensRequired > 1000
	return progressDeckState{
		exact:         true,
		cards:         deck.Clone(),
		total:         total,
		totalLow:      total,
		totalHigh:     total,
		greens:        greens,
		greensLow:     greens,
		greensHigh:    greens,
		redYellow:     ry,
		redYellowLow:  ry,
		redYellowHigh: ry,
		greensNeeded:  greensNeeded,
		allGreens:     allGreens,
	}
}

// progressGreensNeeded reports how many more greens are required, or -1 when
// the lock needs every green.
func progressGreensNeeded(lock *cs.LoadedLock, drawn, putBack int) int {
	if lock == nil || lock.GreensRequired > 1000 {
		return -1
	}
	needed := lock.GreensRequired - drawn + putBack
	if needed < 0 {
		return 0
	}
	return needed
}

// progressCurrentGreensNeeded counts greens since the most recent reset or load.
func progressCurrentGreensNeeded(lock *cs.LoadedLock, events []cs.LogEntry) int {
	if lock == nil || lock.GreensRequired > 1000 {
		return -1
	}
	start := 0
	for idx, entry := range events {
		if isDeckReset(entry.EventType) {
			start = idx + 1
		}
	}
	drawn, putBack := 0, 0
	for _, entry := range events[start:] {
		if entry.EventType == 6 {
			if card, ok := progressDrawCard(entry); ok && card == cs.CardGreen {
				drawn++
			}
		}
		if (entry.EventType == 15 || entry.EventType == 17) && entry.Actor == 2 {
			if card, ok := progressEditCard(entry); ok && card == cs.CardGreen {
				putBack += progressEditAmount(entry)
			}
		}
	}
	return progressGreensNeeded(lock, drawn, putBack)
}

// progressDrawCard returns the card named by a draw event.
func progressDrawCard(entry cs.LogEntry) (cs.Card, bool) {
	return cardForLetter(entry.Card)
}

// progressEditCard returns the card named by an add or remove event.
func progressEditCard(entry cs.LogEntry) (cs.Card, bool) {
	return cardForLetter(entry.Card)
}

func progressEditAmount(entry cs.LogEntry) int {
	if entry.Amount > 0 {
		return entry.Amount
	}
	return 1
}

// yellowCards are every yellow variant, for doubling and halving.
var yellowCards = []cs.Card{
	cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3,
	cs.CardYellowM1, cs.CardYellowM2,
	cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN,
}

func isYellowCard(card cs.Card) bool {
	for _, yellow := range yellowCards {
		if card == yellow {
			return true
		}
	}
	return false
}

// yellowRedDelta is the net red change of a known yellow, ignoring the yellow
// card itself. Random or hidden yellows report false.
func yellowRedDelta(card cs.Card) (int, bool) {
	switch card {
	case cs.CardYellowP1:
		return 1, true
	case cs.CardYellowP2:
		return 2, true
	case cs.CardYellowP3:
		return 3, true
	case cs.CardYellowM1:
		return -1, true
	case cs.CardYellowM2:
		return -2, true
	}
	return 0, false
}

func yellowRedAmount(card cs.Card) int {
	delta, ok := yellowRedDelta(card)
	if !ok {
		return 0
	}
	if delta < 0 {
		return -delta
	}
	return delta
}

func progressDayKey(t time.Time) string {
	return t.UTC().Format(progressDayLayout)
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

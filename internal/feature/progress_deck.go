package feature

// The deck model underneath progress reports. A visible deck is walked back
// from the live composition, so every day is exact. A hidden deck is anchored
// by stored observations of its current card count. Those observations supply
// the before and after totals that make a double up exact.

import (
	"sort"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
)

// progressStates builds the deck state at the start and end of every day with
// events. Exact reconstruction is used when the live deck is visible.
func progressStates(lock *cs.LoadedLock, events []cs.LogEntry, infos []progressEventInfo, observations []store.LockObservation, now time.Time) (map[string]progressDeckState, map[string]progressDeckState) {
	ends := make(map[string]progressDeckState)
	starts := make(map[string]progressDeckState)
	segments := progressSegments(events)

	if lock != nil && !lock.Hidden && lock.Deck != nil {
		if progressExactStates(lock, events, segments, infos, ends, starts) {
			key := progressDayKey(now)
			needed := progressGreensNeededAt(lock, events, now.Add(time.Second))
			ends[key] = exactProgressState(lock.Deck, lock, needed)
			if _, ok := starts[key]; !ok {
				starts[key] = ends[key]
			}
			return ends, starts
		}
		// The exact walk failed. Fall through to the observation model, but
		// make the current state exact from the live deck.
		for idx := range infos {
			infos[idx] = progressEventInfo{}
		}
		ends = make(map[string]progressDeckState)
		starts = make(map[string]progressDeckState)
	}

	model := newProgressDeckModel(lock, events, observations, now)
	model.infos = infos
	model.buildDoubleAnchors()
	model.buildDays(ends, starts)

	if lock != nil && !lock.Hidden && lock.Deck != nil {
		key := progressDayKey(now)
		needed := progressGreensNeededAt(lock, events, now.Add(time.Second))
		ends[key] = exactProgressState(lock.Deck, lock, needed)
		if _, ok := starts[key]; !ok {
			starts[key] = ends[key]
		}
	}
	return ends, starts
}

// progressDeckModel is the observation-anchored view of a hidden deck.
type progressDeckModel struct {
	lock         *cs.LoadedLock
	events       []cs.LogEntry
	observations []store.LockObservation
	now          time.Time
	infos        []progressEventInfo
	anchors      map[int]progressEventInfo
}

func newProgressDeckModel(lock *cs.LoadedLock, events []cs.LogEntry, observations []store.LockObservation, now time.Time) *progressDeckModel {
	sort.SliceStable(observations, func(a, b int) bool {
		return observations[a].ObservedAt.Before(observations[b].ObservedAt)
	})
	return &progressDeckModel{
		lock:         lock,
		events:       events,
		observations: observations,
		now:          now,
		anchors:      make(map[int]progressEventInfo),
	}
}

// buildDays records the start and end state for every day with events and for
// the current day.
func (m *progressDeckModel) buildDays(ends, starts map[string]progressDeckState) {
	keys := make(map[string]bool, len(m.events)+1)
	today := progressDayKey(m.now)
	keys[today] = true
	for _, entry := range m.events {
		keys[progressDayKey(entry.Timestamp.Time)] = true
	}
	for key := range keys {
		start, err := time.Parse(progressDayLayout, key)
		if err != nil {
			continue
		}
		end := start.AddDate(0, 0, 1)
		if key == today {
			end = m.now
		}
		starts[key] = m.stateAt(start)
		ends[key] = m.stateAt(end)
	}
}

// stateAt builds the deck state at one moment.
func (m *progressDeckModel) stateAt(at time.Time) progressDeckState {
	total, totalExact := m.totalAt(at)
	redYellow, exact, known := m.redYellowAt(at)
	needed := progressGreensNeededAt(m.lock, m.events, at)
	allGreens := m.lock != nil && m.lock.GreensRequired > 1000

	state := progressDeckState{
		total:            total,
		totalExact:       totalExact,
		redYellow:        redYellow,
		redYellowExact:   exact,
		redYellowUnknown: !known,
		greensNeeded:     needed,
		allGreens:        allGreens,
	}
	if !totalExact {
		state.notes = append(state.notes, "No deck count was stored for this moment.")
	}
	if !known {
		state.notes = append(state.notes, "The red and yellow count is unknown here.")
	} else if !exact && !totalExact {
		state.notes = append(state.notes, "The red and yellow count is estimated.")
	}
	return state
}

// totalAt reports the deck total at a moment, using the nearest observation
// and the known effects of events between them.
func (m *progressDeckModel) totalAt(at time.Time) (int, bool) {
	if m.lock != nil && !at.Before(m.now.Add(-time.Second)) {
		return progressLiveTotal(m.lock), true
	}

	before, after := m.observationsAround(at)
	if before != nil {
		delta, ok := progressKnownDeckDeltaRange(m.events, m.infos, before.ObservedAt, at)
		if !ok {
			return 0, false
		}
		return progressObservationTotal(*before) + delta, true
	}
	if after != nil {
		delta, ok := progressKnownDeckDeltaRange(m.events, m.infos, at, after.ObservedAt)
		if !ok {
			return 0, false
		}
		return progressObservationTotal(*after) - delta, true
	}
	return 0, false
}

// redYellowAt reports the red and yellow pool at a moment. An exact double
// anchor makes it exact; otherwise it is estimated from the draw mix.
func (m *progressDeckModel) redYellowAt(at time.Time) (int, bool, bool) {
	anchorIdx := -1
	var anchor progressEventInfo
	for idx, info := range m.anchors {
		if !info.exact {
			continue
		}
		if m.events[idx].Timestamp.Time.After(at) {
			continue
		}
		if anchorIdx < 0 || m.events[idx].Timestamp.Time.After(m.events[anchorIdx].Timestamp.Time) {
			anchorIdx = idx
			anchor = info
		}
	}

	if anchorIdx >= 0 {
		ry := anchor.double * 2
		for idx := anchorIdx + 1; idx < len(m.events); idx++ {
			entry := m.events[idx]
			if entry.Timestamp.Time.After(at) {
				break
			}
			delta, ok := progressKnownRYDelta(entry, m.infoAt(idx))
			if !ok {
				return 0, false, false
			}
			ry += delta
		}
		if ry < 0 {
			ry = 0
		}
		return ry, true, true
	}

	// No anchor before this moment. Walk back from the first exact anchor
	// after it, so a day before a double still has an exact starting pool.
	nextIdx := -1
	var next progressEventInfo
	for idx, info := range m.anchors {
		if !info.exact {
			continue
		}
		if m.events[idx].Timestamp.Time.Before(at) {
			continue
		}
		if nextIdx < 0 || m.events[idx].Timestamp.Time.Before(m.events[nextIdx].Timestamp.Time) {
			nextIdx = idx
			next = info
		}
	}
	if nextIdx >= 0 {
		ry := next.double
		for idx := nextIdx - 1; idx >= 0; idx-- {
			entry := m.events[idx]
			if entry.Timestamp.Time.Before(at) {
				break
			}
			delta, ok := progressKnownRYDelta(entry, m.infoAt(idx))
			if !ok {
				return 0, false, false
			}
			ry -= delta
		}
		if ry < 0 {
			ry = 0
		}
		return ry, true, true
	}

	// A double with no stored counts makes the pool unknown from that point on.
	for idx, entry := range m.events {
		if entry.Timestamp.Time.After(at) {
			break
		}
		if entry.EventType == 6 {
			if card, ok := progressDrawCard(entry); ok && card == cs.CardDoubleUp {
				if info, ok := m.anchors[idx]; !ok || !info.exact {
					return 0, false, false
				}
			}
		}
	}

	total, totalExact := m.totalAt(at)
	if !totalExact {
		return 0, false, false
	}
	ry, estimated := progressRedYellowEstimate(m.lock, m.events, at, total)
	return ry, false, estimated
}

func (m *progressDeckModel) infoAt(idx int) progressEventInfo {
	if idx >= 0 && idx < len(m.infos) {
		return m.infos[idx]
	}
	return progressEventInfo{}
}

func (m *progressDeckModel) observationsAround(at time.Time) (*store.LockObservation, *store.LockObservation) {
	var before, after *store.LockObservation
	for idx := range m.observations {
		obs := &m.observations[idx]
		if !obs.ObservedAt.After(at) {
			before = obs
			continue
		}
		if after == nil {
			after = obs
			break
		}
	}
	return before, after
}

// buildDoubleAnchors computes the exact red and yellow addition for every
// double up that has observations before and after it.
func (m *progressDeckModel) buildDoubleAnchors() {
	for idx, entry := range m.events {
		if entry.EventType != 6 {
			continue
		}
		card, ok := progressDrawCard(entry)
		if !ok || card != cs.CardDoubleUp {
			continue
		}
		info := progressEventInfo{hasDouble: true}
		before, after := m.doubleObservations(entry.Timestamp.Time)
		if before == nil || after == nil {
			info.unknown = true
			m.anchors[idx] = info
			m.setInfo(idx, info)
			continue
		}

		beforeIdx := progressEventCountAtOrBefore(m.events, before.ObservedAt)
		afterIdx := progressEventCountAtOrBefore(m.events, after.ObservedAt)
		toDouble, okBefore := progressKnownDeckDeltaRangeByIndex(m.events, m.infos, beforeIdx, idx)
		afterDouble, okAfter := progressKnownDeckDeltaRangeByIndex(m.events, m.infos, idx+1, afterIdx)
		if !okBefore || !okAfter {
			info.unknown = true
			m.anchors[idx] = info
			m.setInfo(idx, info)
			continue
		}

		B := progressObservationTotal(*before) + toDouble
		A := progressObservationTotal(*after) - afterDouble
		ry, other, ok := progressDoubleCandidate(B, A)
		if !ok {
			info.unknown = true
			m.anchors[idx] = info
			m.setInfo(idx, info)
			continue
		}
		info.exact = true
		info.double = ry
		info.before = B
		info.after = A
		info.other = other
		m.anchors[idx] = info
		m.setInfo(idx, info)
	}
}

func (m *progressDeckModel) setInfo(idx int, info progressEventInfo) {
	if idx >= 0 && idx < len(m.infos) {
		m.infos[idx] = info
	}
}

func (m *progressDeckModel) doubleObservations(at time.Time) (*store.LockObservation, *store.LockObservation) {
	var before, after *store.LockObservation
	for idx := range m.observations {
		obs := &m.observations[idx]
		// Log timestamps have second precision. A snapshot inside the same
		// second is ambiguous, so the before side needs a one second margin.
		if !obs.ObservedAt.Add(time.Second).Before(at) {
			if obs.ObservedAt.After(at) && after == nil {
				after = obs
			}
			continue
		}
		before = obs
		if after != nil {
			break
		}
	}
	return before, after
}

func progressObservationTotal(obs store.LockObservation) int {
	if obs.DeckTotal > 0 || !obs.Hidden {
		return obs.DeckTotal
	}
	return obs.CardCount
}

func progressLiveTotal(lock *cs.LoadedLock) int {
	if lock == nil {
		return 0
	}
	if !lock.Hidden && lock.Deck != nil {
		return lock.Deck.Total()
	}
	return lock.CardCount
}

// progressDoubleCandidate turns before and after totals into the red/yellow
// addition and the non-red/yellow count. The drawn double card is removed
// before reds and yellows double.
func progressDoubleCandidate(before, after int) (int, int, bool) {
	ry := after - (before - 1)
	other := (before - 1) - ry
	if ry >= 0 && other >= 0 {
		return ry, other, true
	}
	// A client that counts the drawn double in the pile produces the other
	// convention. Use it only when the first form cannot work.
	ry = after - before
	other = before - ry
	if ry >= 0 && other >= 0 {
		return ry, other, true
	}
	return 0, 0, false
}

// progressKnownDeckDeltaRange sums known total-card changes between two times.
func progressKnownDeckDeltaRange(events []cs.LogEntry, infos []progressEventInfo, from, to time.Time) (int, bool) {
	fromIdx := progressEventCountAtOrBefore(events, from)
	toIdx := progressEventCountAtOrBefore(events, to)
	return progressKnownDeckDeltaRangeByIndex(events, infos, fromIdx, toIdx)
}

func progressKnownDeckDeltaRangeByIndex(events []cs.LogEntry, infos []progressEventInfo, from, to int) (int, bool) {
	if from < 0 {
		from = 0
	}
	if to > len(events) {
		to = len(events)
	}
	total := 0
	for idx := from; idx < to; idx++ {
		info := progressEventInfo{}
		if idx >= 0 && idx < len(infos) {
			info = infos[idx]
		}
		delta, ok := progressKnownDeckDelta(events[idx], info)
		if !ok {
			return 0, false
		}
		total += delta
	}
	return total, true
}

func progressEventCountAtOrBefore(events []cs.LogEntry, at time.Time) int {
	count := 0
	for _, entry := range events {
		if entry.Timestamp.Time.After(at) {
			break
		}
		count++
	}
	return count
}

// progressKnownDeckDelta is the total-card effect of one event. Unknown
// effects return false.
func progressKnownDeckDelta(entry cs.LogEntry, info progressEventInfo) (int, bool) {
	switch entry.EventType {
	case 6:
		card, ok := progressDrawCard(entry)
		if !ok {
			return 0, false
		}
		if card == cs.CardSticky || card == cs.CardArctic {
			return 0, true
		}
		if delta, known := yellowRedDelta(card); known {
			return delta - 1, true
		}
		switch card {
		case cs.CardDoubleUp:
			if info.exact {
				return info.double - 1, true
			}
			return 0, false
		case cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN, cs.CardPink, cs.CardReset:
			return 0, false
		default:
			return -1, true
		}
	case 15, 17:
		return progressEditAmount(entry), true
	case 16, 18:
		return -progressEditAmount(entry), true
	case 28, 29, 30:
		return 0, false
	case 7, 9, 10, 13, 39:
		return 0, false
	default:
		return 0, true
	}
}

// progressKnownRYDelta is the red/yellow pool effect of one event.
func progressKnownRYDelta(entry cs.LogEntry, info progressEventInfo) (int, bool) {
	switch entry.EventType {
	case 6:
		card, ok := progressDrawCard(entry)
		if !ok {
			return 0, false
		}
		if card == cs.CardSticky || card == cs.CardArctic {
			return 0, true
		}
		if delta, known := yellowRedDelta(card); known {
			return delta - 1, true
		}
		switch card {
		case cs.CardRed:
			return -1, true
		case cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN:
			return 0, false
		case cs.CardDoubleUp:
			if info.exact {
				return info.double, true
			}
			return 0, false
		case cs.CardPink, cs.CardReset:
			return 0, false
		default:
			return 0, true
		}
	case 15, 17:
		card, ok := progressEditCard(entry)
		if !ok {
			return 0, false
		}
		if card == cs.CardRed || isYellowCard(card) {
			return progressEditAmount(entry), true
		}
		return 0, true
	case 16, 18:
		card, ok := progressEditCard(entry)
		if !ok {
			return 0, false
		}
		if card == cs.CardRed || isYellowCard(card) {
			return -progressEditAmount(entry), true
		}
		return 0, true
	case 28, 29, 30:
		return 0, false
	case 7, 9, 10, 13, 39:
		return 0, false
	default:
		return 0, true
	}
}

// progressRedYellowEstimate estimates the hidden red/yellow pool from the draw
// mix up to a moment.
func progressRedYellowEstimate(lock *cs.LoadedLock, events []cs.LogEntry, at time.Time, total int) (int, bool) {
	draws, ryDraws := 0, 0
	for _, entry := range events {
		if entry.Timestamp.Time.After(at) {
			break
		}
		if entry.EventType != 6 {
			continue
		}
		card, ok := progressDrawCard(entry)
		if !ok {
			continue
		}
		if card == cs.CardSticky || card == cs.CardArctic {
			continue
		}
		draws++
		if card == cs.CardRed || isYellowCard(card) {
			ryDraws++
		}
	}
	if draws == 0 {
		return max(0, total/3), true
	}
	estimate := (ryDraws*total + draws/2) / draws
	if estimate < 0 {
		estimate = 0
	}
	if estimate > total {
		estimate = total
	}
	return estimate, true
}

// progressExactStates walks the visible current deck backwards, then forwards
// from the recovered original deck to record every day.
func progressExactStates(lock *cs.LoadedLock, events []cs.LogEntry, segments []progressSegment, infos []progressEventInfo, ends, starts map[string]progressDeckState) bool {
	if len(segments) == 0 {
		return true
	}
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
		segmentEnds := make(map[string]progressDeckState)
		segmentStarts := make(map[string]progressDeckState)
		end, ok := forwardProgressExact(lock, original.Clone(), events[seg.from:seg.to], infos, segmentEnds, segmentStarts, seg.from)
		if !ok {
			return false
		}
		if seg.from == currentStart && seg.to == len(events) && !progressSameDeck(end, lock.Deck) {
			return false
		}
		for key, state := range segmentEnds {
			ends[key] = state
		}
		for key, state := range segmentStarts {
			starts[key] = state
		}
	}
	return true
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

// forwardProgressExact applies a segment from a recovered original deck and
// records each day's opening and closing state.
func forwardProgressExact(lock *cs.LoadedLock, start cs.Deck, events []cs.LogEntry, infos []progressEventInfo, ends, starts map[string]progressDeckState, base int) (cs.Deck, bool) {
	deck := start.Clone()
	greensDrawn, greensPutBack := 0, 0
	currentDay := ""
	for idx, entry := range events {
		day := progressDayKey(entry.Timestamp.Time)
		if day != currentDay {
			if _, ok := starts[day]; !ok {
				needed := progressGreensNeeded(lock, greensDrawn, greensPutBack)
				starts[day] = exactProgressState(deck, lock, needed)
			}
			currentDay = day
		}
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
			ends[day] = exactProgressState(deck, lock, needed)
		}
	}
	return deck, true
}

func progressDayEnd(events []cs.LogEntry, idx, end int) bool {
	if idx+1 >= end {
		return true
	}
	return progressDayKey(events[idx].Timestamp.Time) != progressDayKey(events[idx+1].Timestamp.Time)
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
			deck[cs.CardRed] += yellowRedAmount(card)
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
		deck[card] += progressEditAmount(entry)
		return info, true
	case 16, 18:
		card, ok := progressEditCard(entry)
		if !ok {
			return info, false
		}
		removed := min(progressEditAmount(entry), deck[card])
		deck[card] -= removed
		info.clampedRemove = removed < progressEditAmount(entry)
		return info, true
	case 28, 29, 30, 7, 9, 10, 13, 39:
		return info, false
	default:
		return info, true
	}
}

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
			deck[cs.CardRed] += yellowRedAmount(card)
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
	case 28, 29, 30, 7, 9, 10, 13, 39:
		return false
	default:
		return true
	}
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

// exactProgressState derives a state from an exact deck composition.
func exactProgressState(deck cs.Deck, lock *cs.LoadedLock, greensNeeded int) progressDeckState {
	total := deck.Total()
	greens := deck.Greens()
	ry := deck.Reds() + deck.Yellows()
	allGreens := lock != nil && lock.GreensRequired > 1000
	return progressDeckState{
		cards:          deck.Clone(),
		total:          total,
		totalExact:     true,
		redYellow:      ry,
		redYellowExact: true,
		greens:         greens,
		greensExact:    true,
		greensNeeded:   greensNeeded,
		allGreens:      allGreens,
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

// progressGreensNeededAt counts greens since the most recent reset or load.
func progressGreensNeededAt(lock *cs.LoadedLock, events []cs.LogEntry, at time.Time) int {
	if lock == nil || lock.GreensRequired > 1000 {
		return -1
	}
	start := 0
	for idx, entry := range events {
		if entry.Timestamp.Time.After(at) {
			break
		}
		if isDeckReset(entry.EventType) {
			start = idx + 1
		}
	}
	drawn, putBack := 0, 0
	for _, entry := range events[start:] {
		if entry.Timestamp.Time.After(at) {
			break
		}
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

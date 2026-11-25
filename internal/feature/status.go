package feature

import (
	"context"
	"fmt"
	"strings"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

// statusOptions holds the values shared across every lock in one status reply.
type statusOptions struct {
	viewerID    string
	showType    bool
	showRole    bool
	showPurpose bool
	freezeSince map[cs.ID]time.Time
	now         time.Time
}

// renderStatus renders the compact /lock status reply.
func renderStatus(views []cs.LockView, viewerID string, freezeSince map[cs.ID]time.Time, now time.Time) string {
	if len(views) == 0 {
		return "You have no active ChastiSafe locks."
	}

	opts := statusOptions{
		viewerID:    viewerID,
		freezeSince: freezeSince,
		now:         now,
	}
	opts.showType, opts.showRole, opts.showPurpose = varyingFields(views)

	var b strings.Builder
	fmt.Fprintf(&b, "**Your active lock%s** (%d)\n", plural(len(views)), len(views))
	for idx := range views {
		b.WriteString("\n")
		b.WriteString(renderLockStatus(&views[idx], opts))
	}
	return strings.TrimRight(b.String(), "\n")
}

// varyingFields reports which per-lock metadata is worth showing. A field is
// shown only when the locks disagree on it.
func varyingFields(views []cs.LockView) (showType, showRole, showPurpose bool) {
	if len(views) < 2 {
		return false, false, false
	}

	firstType := lockTypeLabel(&views[0].Lock)
	firstRole := views[0].Role
	firstPurpose := views[0].Lock.LockPurpose
	for idx := 1; idx < len(views); idx++ {
		if lockTypeLabel(&views[idx].Lock) != firstType {
			showType = true
		}
		if views[idx].Role != firstRole {
			showRole = true
		}
		if views[idx].Lock.LockPurpose != firstPurpose {
			showPurpose = true
		}
	}
	return showType, showRole, showPurpose
}

func renderLockStatus(view *cs.LockView, opts statusOptions) string {
	if view == nil {
		return "Lock not found."
	}
	lock := &view.Lock

	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (ID `%s`)\n", lockName(lock), lock.ID)

	var meta []string
	if opts.showType {
		meta = append(meta, "Type: "+lockTypeLabel(lock))
	}
	if opts.showRole {
		meta = append(meta, "Role: "+roleLabel(view.Role))
	}
	if opts.showPurpose {
		meta = append(meta, "Purpose: "+purposeLabel(lock.LockPurpose))
	}
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, " · "))
		b.WriteString("\n")
	}

	b.WriteString(stateLine(lock, opts))
	b.WriteString("\n")

	if line := peopleLine(lock, opts.viewerID); line != "" {
		b.WriteString(line)
		b.WriteString("\n")
	}

	if lock.IsTimerLock() && !lock.IsFrozen() {
		if remaining := lock.Remaining(); remaining > 0 {
			b.WriteString(":hourglass: Unlocks ")
			b.WriteString(relativeTimestamp(opts.now.Add(remaining)))
			b.WriteString("\n")
		}
	}

	if lock.IsCardLock() {
		b.WriteString(statusCardsLine(lock))
		b.WriteString("\n")
		if wait := lock.NextPickIn(); wait > 0 {
			b.WriteString(":hourglass: Next draw ")
			b.WriteString(relativeTimestamp(opts.now.Add(wait)))
			b.WriteString("\n")
		}
	}

	if lock.CheckInPeriod > 0 {
		if next := lock.NextCheckInIn(); next > 0 {
			fmt.Fprintf(&b, ":alarm_clock: Check-in: every %s · next %s\n",
				cs.FormatMinutes(lock.CheckInPeriod), relativeTimestamp(opts.now.Add(next)))
		} else {
			b.WriteString(":alarm_clock: Check-in: due now\n")
		}
	}

	if hygiene := lock.NextHygieneIn(); hygiene > 0 {
		b.WriteString(":shower: Hygiene ")
		b.WriteString(relativeTimestamp(opts.now.Add(hygiene)))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func stateLine(lock *cs.LoadedLock, opts statusOptions) string {
	parts := make([]string, 0, 3)
	switch {
	case lock.IsFrozen():
		if since, ok := opts.freezeSince[lock.ID]; ok && !since.IsZero() {
			parts = append(parts, ":ice_cube: Frozen for "+elapsedLabel(opts.now.Sub(since)))
		} else {
			parts = append(parts, ":ice_cube: Frozen")
		}
	case lock.IsReady():
		parts = append(parts, ":unlocked: Ready to unlock")
	default:
		parts = append(parts, ":locked: Locked")
	}
	if lock.Hidden {
		parts = append(parts, "hidden")
	}
	if !lock.LoadTime.IsZero() {
		parts = append(parts, "locked for "+elapsedLabel(opts.now.Sub(lock.LoadTime.Time)))
	}
	return strings.Join(parts, " · ")
}

// elapsedLabel renders a past duration as a compact exact count, for example
// 1d 15hr 56m, instead of a rounded relative timestamp.
func elapsedLabel(d time.Duration) string {
	minutes := int64(d / time.Minute)
	if minutes < 1 {
		return "just now"
	}

	days := minutes / (24 * 60)
	hours := (minutes % (24 * 60)) / 60
	mins := minutes % 60

	switch {
	case days > 0 && mins > 0:
		return fmt.Sprintf("%dd %dhr %dm", days, hours, mins)
	case days > 0:
		return fmt.Sprintf("%dd %dhr", days, hours)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dhr %dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dhr", hours)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func statusCardsLine(lock *cs.LoadedLock) string {
	parts := make([]string, 0, 3)
	if cards := cardsLeft(lock); cards > 0 {
		parts = append(parts, fmt.Sprintf("%d card%s", cards, plural(cards)))
	}
	if greens := lock.GreensRemaining(); greens > 0 {
		parts = append(parts, fmt.Sprintf("%d green%s needed", greens, plural(greens)))
	}
	if lock.MaxPicks > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d draws", lock.DrawsAvailable(), lock.MaxPicks))
	} else {
		parts = append(parts, fmt.Sprintf("%d draws", lock.DrawsAvailable()))
	}
	return ":flower_playing_cards: " + strings.Join(parts, " · ")
}

// peopleLine shows only the other side of the lock, never the caller.
func peopleLine(lock *cs.LoadedLock, viewerID string) string {
	if lock == nil {
		return ""
	}
	if lock.IsSelfLock() {
		return ":key: Self-locked"
	}

	viewerID = strings.TrimSpace(viewerID)
	isLockee := viewerID == "" || lock.LockeeID.String() == viewerID
	isKeyholder := viewerID != "" && lock.KeyholderID.String() == viewerID

	switch {
	case isLockee && isKeyholder:
		return ":key: Self-locked"
	case isLockee:
		if name := otherName(lock.Keyholder, lock.KeyholderID); name != "" {
			return ":key: Keyholder: " + name
		}
	case isKeyholder:
		if name := otherName(lock.Lockee, lock.LockeeID); name != "" {
			return ":bust_in_silhouette: Lockee: " + name
		}
	}
	return ""
}

func otherName(display string, id cs.ID) string {
	if name := strings.TrimSpace(display); name != "" && !strings.EqualFold(name, "<self>") {
		return name
	}
	if id != "" && id.String() != "0" {
		return id.String()
	}
	return ""
}

func lockTypeLabel(lock *cs.LoadedLock) string {
	if lock == nil {
		return "lock"
	}
	return cs.LockTypeLabel(lock.Type, lock.IsCardLock(), lock.IsTimerLock())
}

func roleLabel(role cs.LockRole) string {
	switch role {
	case cs.RoleLockee:
		return "lockee"
	case cs.RoleKeyholder:
		return "keyholder"
	case cs.RoleBoth:
		return "lockee and keyholder"
	default:
		return "observer"
	}
}

func purposeLabel(purpose cs.Purpose) string {
	if value := strings.TrimSpace(string(purpose)); value != "" {
		return value
	}
	return "unknown"
}

// freezeTimes finds the current freeze start for each frozen lock. ChastiSafe
// does not return a freeze timestamp on the lock, so the lock log is the
// source. A keyholder freeze is event type 2; a freeze card is a pick event
// with a freeze card.
func (f *Feature) freezeTimes(ctx context.Context, client *cs.Client, locks []cs.LoadedLock) map[cs.ID]time.Time {
	times := make(map[cs.ID]time.Time)
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsFrozen() {
			continue
		}
		entries, err := f.freezeLog(ctx, client, lock)
		if err != nil {
			f.log.Warn("loading freeze history", "lock_id", lock.ID.String(), "err", err)
			continue
		}
		if since, ok := freezeStartFor(lock, entries); ok {
			times[lock.ID] = since
		} else {
			f.log.Debug("freeze start not found",
				"lock_id", lock.ID.String(),
				"frozen_by_card", lock.FrozenByCard,
				"frozen_by_keyholder", lock.FrozenByKeyholder,
				"entries", len(entries),
			)
		}
	}
	return times
}

// freezeLog reads enough of the lock log to locate the current freeze. It
// stops early once a start is found.
func (f *Feature) freezeLog(ctx context.Context, client *cs.Client, lock *cs.LoadedLock) ([]cs.LogEntry, error) {
	const (
		pageSize = 100
		maxPages = 3
	)

	var out []cs.LogEntry
	for page := 0; page < maxPages; page++ {
		entries, err := client.Log(ctx, lock.ID, page*pageSize, pageSize)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
		if _, ok := freezeStartFor(lock, out); ok {
			return out, nil
		}
		if len(entries) < pageSize {
			break
		}
	}
	return out, nil
}

// freezeStartFor returns the newest freeze start for the lock, considering
// both keyholder freezes and freeze-card picks.
func freezeStartFor(lock *cs.LoadedLock, entries []cs.LogEntry) (time.Time, bool) {
	if lock == nil {
		return time.Time{}, false
	}

	var (
		keyholderStart time.Time
		cardStart      time.Time
		keyholderDone  bool
		cardSet        bool
		keyholderSet   bool
	)

	for _, entry := range entries {
		if lock.FrozenByKeyholder && !keyholderDone {
			switch entry.EventType {
			case 2:
				keyholderStart = entry.Timestamp.Time
				keyholderSet = true
				keyholderDone = true
			case 3:
				keyholderDone = true
			}
		}
		if lock.FrozenByCard && !cardSet {
			if entry.EventType == 6 && isFreezeCard(entry.Card) {
				cardStart = entry.Timestamp.Time
				cardSet = true
			}
		}
		if (!lock.FrozenByKeyholder || keyholderDone) && (!lock.FrozenByCard || cardSet) {
			break
		}
	}

	switch {
	case keyholderSet && cardSet:
		if cardStart.After(keyholderStart) {
			return cardStart, true
		}
		return keyholderStart, true
	case keyholderSet:
		return keyholderStart, true
	case cardSet:
		return cardStart, true
	default:
		return time.Time{}, false
	}
}

// isFreezeCard reports whether a log card letter applies a freeze.
func isFreezeCard(letter string) bool {
	letter = strings.ToLower(strings.TrimSpace(letter))
	if letter == "" {
		return false
	}
	card, ok := cs.CardForLetter(letter[0])
	if !ok {
		return false
	}
	switch card {
	case cs.CardFreeze, cs.CardArctic, cs.CardSticky:
		return true
	default:
		return false
	}
}

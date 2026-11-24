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
			parts = append(parts, ":ice_cube: Frozen since "+relativeTimestamp(since))
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
		parts = append(parts, "loaded "+relativeTimestamp(lock.LoadTime.Time))
	}
	return strings.Join(parts, " · ")
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
// does not return a freeze timestamp on the lock, so the newest freeze event in
// the lock log is the source.
func (f *Feature) freezeTimes(ctx context.Context, client *cs.Client, locks []cs.LoadedLock) map[cs.ID]time.Time {
	times := make(map[cs.ID]time.Time)
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsFrozen() {
			continue
		}
		entries, err := client.Log(ctx, lock.ID, 0, 100)
		if err != nil {
			f.log.Warn("loading freeze history", "lock_id", lock.ID.String(), "err", err)
			continue
		}
		if since, ok := freezeStart(entries); ok {
			times[lock.ID] = since
		}
	}
	return times
}

// freezeStart returns the newest freeze event unless an unfreeze came later.
func freezeStart(entries []cs.LogEntry) (time.Time, bool) {
	for _, entry := range entries {
		switch entry.EventType {
		case 2:
			return entry.Timestamp.Time, true
		case 3:
			return time.Time{}, false
		}
	}
	return time.Time{}, false
}

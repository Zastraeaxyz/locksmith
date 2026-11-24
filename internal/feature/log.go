package feature

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const (
	recentDrawWindow = 5 * time.Minute
	logPageSize      = 100
	recentLogLimit   = 500
	fullLogLimit     = 1000
)

type logTarget struct {
	running  *cs.LoadedLock
	previous *cs.RecentLock
}

func (t *logTarget) id() cs.ID {
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

// handleLockLog shows draws from the last five minutes, or the whole log when
// full is set. ref selects a running or previous lock; public makes the reply
// visible in the channel.
func (f *Feature) handleLockLog(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.UserID(i)
	full := boolOption(opts, "full")
	public := boolOption(opts, "public")
	ref, _ := stringOption(opts, "ref")

	if err := bot.Defer(s, i, !public); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.Edit(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return bot.Edit(s, i, f.apiFailure(ctx, userID, err))
	}
	own := filterOwn(locks, cred.ChastiSafeID)

	recent, err := client.Recent(ctx)
	if err != nil {
		f.log.Warn("loading previous locks", "user_id", userID, "err", err)
		recent = nil
	}

	target, note, err := resolveLogTarget(ref, own, recent)
	if err != nil {
		return bot.Edit(s, i, err.Error())
	}

	header := f.logHeader(ctx, client, target, cred.ChastiSafeID)
	entries, truncated, err := f.fetchLog(ctx, client, target.id(), full)
	if err != nil {
		f.log.Warn("loading lock log", "user_id", userID, "lock_id", target.id().String(), "err", err)
		text := logOutput(header, nil, false, full, note) +
			"\n\nI could not load the log for that lock."
		return bot.EditChunks(s, i, text, !public)
	}
	return bot.EditChunks(s, i, logOutput(header, entries, truncated, full, note), !public)
}

// logHeader renders the compact status block for a running lock, or the
// completed-lock stats for a previous lock.
func (f *Feature) logHeader(ctx context.Context, client *cs.Client, target *logTarget, viewerID string) string {
	switch {
	case target == nil:
		return ""
	case target.running != nil:
		freeze := map[cs.ID]time.Time{}
		if target.running.IsFrozen() {
			freeze = f.freezeTimes(ctx, client, []cs.LoadedLock{*target.running})
		}
		view := cs.LockView{Lock: *target.running, Role: cs.RoleLockee}
		return renderLockStatus(&view, statusOptions{
			viewerID:    viewerID,
			freezeSince: freeze,
			now:         f.now(),
		})
	case target.previous != nil:
		return previousText(target.previous)
	default:
		return ""
	}
}

func (f *Feature) fetchLog(ctx context.Context, client *cs.Client, lockID cs.ID, full bool) ([]cs.LogEntry, bool, error) {
	if lockID == "" {
		return nil, false, errors.New("no lock selected")
	}

	limit := recentLogLimit
	if full {
		limit = fullLogLimit
	}
	cutoff := f.now().Add(-recentDrawWindow)

	var out []cs.LogEntry
	from := 0
	for len(out) < limit {
		entries, err := client.Log(ctx, lockID, from, logPageSize)
		if err != nil {
			return nil, false, err
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if !full && entry.Timestamp.Time.Before(cutoff) {
				return out, false, nil
			}
			if full || entry.EventType == 6 {
				out = append(out, entry)
			}
		}
		if len(entries) < logPageSize {
			break
		}
		from += logPageSize
	}
	return out, len(out) >= limit, nil
}

func logOutput(header string, entries []cs.LogEntry, truncated, full bool, note string) string {
	var b strings.Builder

	if header != "" {
		b.WriteString(header)
	}
	if note != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(note)
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}

	if len(entries) == 0 {
		if full {
			b.WriteString("No log entries.")
		} else {
			b.WriteString("No draws in the last 5 minutes. Use `full:true` for the whole log.")
		}
	} else {
		if full {
			b.WriteString("**Full log**")
		} else {
			b.WriteString("**Draws in the last 5 minutes**")
		}
		for _, entry := range entries {
			fmt.Fprintf(&b, "\n• %s %s", shortTimestamp(entry.Timestamp.Time), cs.LogEntryText(entry))
		}
	}

	if truncated {
		b.WriteString("\n\nTruncated at the newest entries.")
	}
	return b.String()
}

func previousText(lock *cs.RecentLock) string {
	if lock == nil {
		return "Lock not found."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (ID `%s`)\n", recentName(lock), lock.LockID)
	fmt.Fprintf(&b, "Previous lock · %s\n", cs.UnlockReasonString(lock.UnlockReason))
	fmt.Fprintf(&b, "Lockee: %s · Keyholder: %s\n", displayOr(lock.Lockee, "unknown"), displayOr(lock.Keyholder, "unknown"))
	if !lock.LoadTime.IsZero() {
		fmt.Fprintf(&b, "Loaded: %s\n", fullTimestamp(lock.LoadTime.Time))
	}
	if !lock.UnlockTime.IsZero() {
		fmt.Fprintf(&b, "Unlocked: %s\n", fullTimestamp(lock.UnlockTime.Time))
	}
	fmt.Fprintf(&b, "Duration: %s\n", cs.FormatDuration(time.Duration(lock.LockDuration)*time.Second))
	if lock.Rating != 0 {
		fmt.Fprintf(&b, "Rating: %d\n", lock.Rating)
	}
	if purpose := strings.TrimSpace(string(lock.LockPurpose)); purpose != "" {
		fmt.Fprintf(&b, "Purpose: %s\n", purpose)
	}
	return strings.TrimRight(b.String(), "\n")
}

func resolveLogTarget(ref string, running []cs.LoadedLock, recent []cs.RecentLock) (*logTarget, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		switch {
		case len(running) > 0:
			target := newestRunning(running)
			note := ""
			if len(running) > 1 {
				note = fmt.Sprintf("Using the newest active lock, **%s**. Pass `ref` to choose another.", lockName(target))
			}
			return &logTarget{running: target}, note, nil
		case len(recent) > 0:
			target := newestRecent(recent)
			return &logTarget{previous: target},
				fmt.Sprintf("You have no active locks. Showing your most recent lock, **%s**.", recentName(target)), nil
		default:
			return nil, "", errors.New("You have no ChastiSafe locks.")
		}
	}

	lower := strings.ToLower(ref)

	for idx := range running {
		if running[idx].ID.String() == ref {
			return &logTarget{running: &running[idx]}, "", nil
		}
	}
	for idx := range recent {
		if recent[idx].LockID.String() == ref {
			return &logTarget{previous: &recent[idx]}, "", nil
		}
	}

	var exactRunning []*cs.LoadedLock
	for idx := range running {
		if strings.ToLower(strings.TrimSpace(running[idx].LockName)) == lower {
			exactRunning = append(exactRunning, &running[idx])
		}
	}
	if len(exactRunning) == 1 {
		return &logTarget{running: exactRunning[0]}, "", nil
	}
	if len(exactRunning) > 1 {
		target := newestRunningPtrs(exactRunning)
		return &logTarget{running: target},
			fmt.Sprintf("Several active locks have that name. Using the newest, **%s**.", lockName(target)), nil
	}

	var exactRecent []*cs.RecentLock
	for idx := range recent {
		if strings.ToLower(strings.TrimSpace(recent[idx].LockName)) == lower {
			exactRecent = append(exactRecent, &recent[idx])
		}
	}
	if len(exactRecent) > 0 {
		return &logTarget{previous: newestRecentPtrs(exactRecent)}, "", nil
	}

	var (
		runningMatches []*cs.LoadedLock
		recentMatches  []*cs.RecentLock
		names          []string
	)
	for idx := range running {
		if prefixMatch(running[idx].LockName, lower) {
			runningMatches = append(runningMatches, &running[idx])
			names = append(names, lockName(&running[idx]))
		}
	}
	for idx := range recent {
		if prefixMatch(recent[idx].LockName, lower) {
			recentMatches = append(recentMatches, &recent[idx])
			names = append(names, recentName(&recent[idx]))
		}
	}

	switch {
	case len(runningMatches) == 1:
		return &logTarget{running: runningMatches[0]}, "", nil
	case len(runningMatches) > 1:
		return nil, "", fmt.Errorf("`%s` matches several active locks: %s", ref, strings.Join(names, ", "))
	case len(recentMatches) == 1:
		return &logTarget{previous: recentMatches[0]}, "", nil
	case len(recentMatches) > 1:
		return nil, "", fmt.Errorf("`%s` matches several previous locks: %s", ref, strings.Join(names, ", "))
	default:
		return nil, "", fmt.Errorf("No lock matches `%s`.", ref)
	}
}

func prefixMatch(name, lower string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name != "" && strings.HasPrefix(name, lower)
}

func newestRunning(locks []cs.LoadedLock) *cs.LoadedLock {
	if len(locks) == 0 {
		return nil
	}
	best := &locks[0]
	for idx := 1; idx < len(locks); idx++ {
		if locks[idx].LoadTime.Time.After(best.LoadTime.Time) {
			best = &locks[idx]
		}
	}
	return best
}

func newestRunningPtrs(locks []*cs.LoadedLock) *cs.LoadedLock {
	if len(locks) == 0 {
		return nil
	}
	best := locks[0]
	for _, lock := range locks[1:] {
		if lock.LoadTime.Time.After(best.LoadTime.Time) {
			best = lock
		}
	}
	return best
}

func newestRecent(locks []cs.RecentLock) *cs.RecentLock {
	if len(locks) == 0 {
		return nil
	}
	best := &locks[0]
	for idx := 1; idx < len(locks); idx++ {
		if locks[idx].UnlockTime.Time.After(best.UnlockTime.Time) {
			best = &locks[idx]
		}
	}
	return best
}

func newestRecentPtrs(locks []*cs.RecentLock) *cs.RecentLock {
	if len(locks) == 0 {
		return nil
	}
	best := locks[0]
	for _, lock := range locks[1:] {
		if lock.UnlockTime.Time.After(best.UnlockTime.Time) {
			best = lock
		}
	}
	return best
}

func recentName(lock *cs.RecentLock) string {
	if lock == nil {
		return "lock"
	}
	if name := strings.TrimSpace(lock.LockName); name != "" {
		return name
	}
	return "lock"
}

func displayOr(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func filterOwn(locks []cs.LoadedLock, chastisafeID string) []cs.LoadedLock {
	out := make([]cs.LoadedLock, 0, len(locks))
	for idx := range locks {
		if chastisafeID != "" && locks[idx].LockeeID.String() != chastisafeID {
			continue
		}
		out = append(out, locks[idx])
	}
	return out
}

func ownLockViews(locks []cs.LoadedLock, chastisafeID string) []cs.LockView {
	own := filterOwn(locks, chastisafeID)
	views := make([]cs.LockView, 0, len(own))
	for idx := range own {
		views = append(views, cs.LockView{Lock: own[idx], Role: cs.RoleLockee})
	}
	return views
}

func boolOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) bool {
	for _, option := range options {
		if option.Name == name {
			return option.BoolValue()
		}
	}
	return false
}

func stringOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) (string, bool) {
	for _, option := range options {
		if option.Name == name {
			return option.StringValue(), true
		}
	}
	return "", false
}

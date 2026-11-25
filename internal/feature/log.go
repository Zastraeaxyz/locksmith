package feature

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const (
	recentDrawWindow  = 5 * time.Minute
	recentDrawMinimum = 10
	drawGroupWindow   = time.Minute
	logPageSize       = 100
	recentLogLimit    = 500
	fullLogLimit      = 1000
)

// logView is what one /lock log reply shows: the recent window, or every event
// in full mode, plus older draws that top the recent view up.
type logView struct {
	entries   []cs.LogEntry
	earlier   []cs.LogEntry
	truncated bool
	full      bool
}

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
	own := ownLocks(locks, cred.ChastiSafeID)

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
	view, err := f.fetchLog(ctx, client, target.id(), full)
	if err != nil {
		f.log.Warn("loading lock log", "user_id", userID, "lock_id", target.id().String(), "err", err)
		text := logOutput(header, logView{full: full}, note) +
			"\n\nI could not load the log for that lock."
		return bot.EditChunks(s, i, text, !public)
	}
	return bot.EditChunks(s, i, logOutput(header, view, note), !public)
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

// fetchLog reads the lock log. Full mode keeps every event, newest first, up
// to the full limit. The recent view keeps every draw from the last five
// minutes plus older draws until it has at least recentDrawMinimum, so a quiet
// lock still shows recent results.
func (f *Feature) fetchLog(ctx context.Context, client *cs.Client, lockID cs.ID, full bool) (logView, error) {
	view := logView{full: full}
	if lockID == "" {
		return view, errors.New("no lock selected")
	}

	limit := recentLogLimit
	if full {
		limit = fullLogLimit
	}
	cutoff := f.now().Add(-recentDrawWindow)

	for page := 0; page < fullLogLimit/logPageSize; page++ {
		entries, err := client.Log(ctx, lockID, page*logPageSize, logPageSize)
		if err != nil {
			return logView{full: full}, err
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if full {
				if len(view.entries) >= limit {
					view.truncated = true
					return view, nil
				}
				view.entries = append(view.entries, entry)
				continue
			}
			if entry.EventType != 6 {
				continue
			}
			if entry.Timestamp.Time.Before(cutoff) {
				if len(view.entries)+len(view.earlier) >= recentDrawMinimum {
					return view, nil
				}
				view.earlier = append(view.earlier, entry)
				continue
			}
			view.entries = append(view.entries, entry)
			if len(view.entries) >= limit {
				view.truncated = true
				return view, nil
			}
		}
		if len(entries) < logPageSize {
			break
		}
	}
	return view, nil
}

func logOutput(header string, view logView, note string) string {
	var sections []string
	if header != "" {
		sections = append(sections, header)
	}
	if note != "" {
		sections = append(sections, note)
	}

	switch {
	case view.full && len(view.entries) == 0:
		sections = append(sections, "No log entries.")
	case view.full:
		sections = append(sections, "**Full log**"+logEntriesText(view.entries))
	case len(view.entries) > 0:
		sections = append(sections, "**Draws in the last 5 minutes**"+logEntriesText(view.entries))
	case len(view.earlier) == 0:
		sections = append(sections, "No draws in the last 5 minutes. Use `full:true` for the whole log.")
	default:
		sections = append(sections, "No draws in the last 5 minutes.")
	}
	if len(view.earlier) > 0 {
		sections = append(sections, "**Earlier draws**"+logEntriesText(view.earlier))
	}

	if view.truncated {
		sections = append(sections, "Truncated at the newest entries.")
	}
	return strings.Join(sections, "\n\n")
}

// logEntriesText renders one line per event, condensing draws that happened
// within drawGroupWindow of each other into a single icon-only line.
func logEntriesText(entries []cs.LogEntry) string {
	var b strings.Builder
	for idx := 0; idx < len(entries); {
		if entries[idx].EventType != 6 {
			b.WriteString("\n• ")
			b.WriteString(shortTimestamp(entries[idx].Timestamp.Time))
			b.WriteString(" ")
			writeCardLetterEmoji(&b, entries[idx].Card)
			b.WriteString(logEntryText(entries[idx]))
			idx++
			continue
		}

		end := idx + 1
		for end < len(entries) && entries[end].EventType == 6 &&
			!entries[end].Timestamp.Time.Before(entries[idx].Timestamp.Time.Add(-drawGroupWindow)) {
			end++
		}
		writeDrawGroup(&b, entries[idx:end])
		idx = end
	}
	return b.String()
}

// writeDrawGroup writes one condensed line: the time the run of draws started,
// then each card in draw order. Entries are newest first, so the group is
// reversed into chronological order and the earliest draw gives the time.
func writeDrawGroup(b *strings.Builder, group []cs.LogEntry) {
	b.WriteString("\n• ")
	b.WriteString(shortTimestamp(group[len(group)-1].Timestamp.Time))
	for idx := len(group) - 1; idx >= 0; idx-- {
		writeDrawIcon(b, group[idx])
	}
}

// writeDrawIcon writes a leading space and the icon for one draw. A keyholder
// draw keeps a key marker and a dice draw keeps the number it threw.
func writeDrawIcon(b *strings.Builder, entry cs.LogEntry) {
	b.WriteString(" ")
	if entry.Actor == 1 {
		b.WriteString(":key:")
	}

	card, ok := cardForLetter(entry.Card)
	if !ok {
		b.WriteString(":question:")
		return
	}
	b.WriteString(cardEmojiMarkup(card))
	if card == cs.CardDice && entry.Amount > 0 {
		b.WriteString(strconv.Itoa(entry.Amount))
	}
}

// logEntryText renders one log event without revealing a lock code. A hygiene
// unlock (event 21) carries the code in Info, and a public lock log is
// permanent, so Locksmith never prints it.
func logEntryText(entry cs.LogEntry) string {
	if entry.EventType == 21 && entry.Info != "" {
		entry.Info = ""
		return cs.LogEntryText(entry) + " (code hidden)"
	}
	return cs.LogEntryText(entry)
}

// writeCardLetterEmoji writes the emoji for a ChastiSafe log card letter, if
// the entry names a known card.
func writeCardLetterEmoji(b *strings.Builder, letter string) {
	if card, ok := cardForLetter(letter); ok {
		writeCardEmoji(b, card)
	}
}

// cardForLetter maps a log card letter to its card, ignoring surrounding space.
func cardForLetter(letter string) (cs.Card, bool) {
	letter = strings.TrimSpace(letter)
	if letter == "" {
		return "", false
	}
	return cs.CardForLetter(letter[0])
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

	resolved, note, err := resolveRef(ref, running, recent)
	if err != nil {
		return nil, "", err
	}
	return &logTarget{running: resolved.Running, previous: resolved.Previous}, note, nil
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

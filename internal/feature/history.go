package feature

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
	historyWindowYears = 1
	historyEntryLimit  = 50
)

func historyCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "history",
		Description: "Show your completed ChastiSafe locks",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "include_test",
				Description: "Include TEST locks, which are hidden by default",
				Required:    false,
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "include_old",
				Description: "Include locks older than a year, which are hidden by default",
				Required:    false,
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "public",
				Description: "Post the reply publicly instead of privately",
				Required:    false,
			},
		},
	}
}

// historyOptions controls which completed locks are shown.
type historyOptions struct {
	includeTest bool
	includeOld  bool
	now         time.Time
}

// historyView is the filtered, sorted history plus what the filters hid.
type historyView struct {
	entries    []cs.RecentLock
	hiddenTest int
	hiddenOld  int
	truncated  int
}

func (f *Feature) handleHistoryCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	opts := i.ApplicationCommandData().Options
	public := boolOption(opts, "public")
	if err := bot.Defer(s, i, !public); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}
	recent, err := client.Recent(ctx)
	if err != nil {
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}

	view := newHistoryView(recent, historyOptions{
		includeTest: boolOption(opts, "include_test"),
		includeOld:  boolOption(opts, "include_old"),
		now:         f.now(),
	})
	return bot.EditChunks(s, i, renderHistory(view), !public)
}

// newHistoryView keeps recent non-test locks by default, sorts them newest
// first, and records how many the filters and the entry cap held back.
func newHistoryView(locks []cs.RecentLock, opts historyOptions) historyView {
	if opts.now.IsZero() {
		opts.now = time.Now()
	}
	cutoff := opts.now.AddDate(-historyWindowYears, 0, 0)

	var view historyView
	kept := make([]cs.RecentLock, 0, len(locks))
	for _, lock := range locks {
		if !opts.includeTest && lock.LockPurpose == cs.PurposeTest {
			view.hiddenTest++
			continue
		}
		if !opts.includeOld {
			if at, ok := historyMoment(lock); ok && at.Before(cutoff) {
				view.hiddenOld++
				continue
			}
		}
		kept = append(kept, lock)
	}

	sort.SliceStable(kept, func(a, b int) bool {
		return historySortKey(kept[a]).After(historySortKey(kept[b]))
	})

	if len(kept) > historyEntryLimit {
		view.truncated = len(kept) - historyEntryLimit
		kept = kept[:historyEntryLimit]
	}
	view.entries = kept
	return view
}

// historyMoment reports when a completed lock ended, falling back to when it
// started. Locks with no timestamps report false.
func historyMoment(lock cs.RecentLock) (time.Time, bool) {
	if !lock.UnlockTime.IsZero() {
		return lock.UnlockTime.Time, true
	}
	if !lock.LoadTime.IsZero() {
		return lock.LoadTime.Time, true
	}
	return time.Time{}, false
}

func historySortKey(lock cs.RecentLock) time.Time {
	at, ok := historyMoment(lock)
	if !ok {
		return time.Time{}
	}
	return at
}

func renderHistory(view historyView) string {
	var b strings.Builder
	if len(view.entries) == 0 {
		if view.hiddenTest == 0 && view.hiddenOld == 0 {
			return "You have no completed ChastiSafe locks."
		}
		b.WriteString("No completed locks to show.")
	} else {
		fmt.Fprintf(&b, "**Your lock history** (%d)\n", len(view.entries))
		for _, lock := range view.entries {
			b.WriteString("\n")
			b.WriteString(historyLine(lock))
		}
	}

	if view.truncated > 0 {
		fmt.Fprintf(&b, "\n\nShowing the newest %d locks, %d older %s left out.",
			historyEntryLimit, view.truncated, entryWord(view.truncated))
	}
	if hints := historyHints(view); hints != "" {
		b.WriteString("\n\n")
		b.WriteString(hints)
	}
	return b.String()
}

func historyHints(view historyView) string {
	var hints []string
	if view.hiddenTest > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d TEST lock%s hidden. Use `include_test:true` to include them.",
			view.hiddenTest, plural(view.hiddenTest),
		))
	}
	if view.hiddenOld > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d lock%s older than a year hidden. Use `include_old:true` to include them.",
			view.hiddenOld, plural(view.hiddenOld),
		))
	}
	return strings.Join(hints, "\n")
}

// historyLine renders one completed lock as a compact single line.
func historyLine(lock cs.RecentLock) string {
	parts := []string{"**" + recentName(&lock) + "**"}
	if duration := historyDuration(time.Duration(lock.LockDuration) * time.Second); duration != "" {
		parts = append(parts, duration)
	}
	parts = append(parts, cs.UnlockReasonString(lock.UnlockReason))
	if at, ok := historyMoment(lock); ok {
		parts = append(parts, fullTimestamp(at))
	}
	if keyholder := historyKeyholder(lock); keyholder != "" {
		parts = append(parts, "keyholder "+keyholder)
	}
	if purpose := historyPurposeTag(lock); purpose != "" {
		parts = append(parts, purpose)
	}
	if lock.Rating != 0 {
		parts = append(parts, fmt.Sprintf("rated %d", lock.Rating))
	}
	return "• " + strings.Join(parts, " · ")
}

func historyKeyholder(lock cs.RecentLock) string {
	name := strings.TrimSpace(lock.Keyholder)
	if name == "" || strings.EqualFold(name, "<self>") {
		return ""
	}
	return name
}

// historyPurposeTag shows purposes that differ from the default, so TEST rows
// stand out when they are included.
func historyPurposeTag(lock cs.RecentLock) string {
	purpose := strings.TrimSpace(string(lock.LockPurpose))
	if purpose == "" || strings.EqualFold(purpose, string(cs.PurposeChastity)) {
		return ""
	}
	return purpose
}

// historyDuration renders a lock duration compactly, for example 2d 4h or
// 5h 12m, because cs.FormatDuration is too long for a list line.
func historyDuration(d time.Duration) string {
	minutes := int64(d / time.Minute)
	if d > 0 && minutes < 1 {
		return "<1m"
	}
	if minutes <= 0 {
		return ""
	}

	days := minutes / (24 * 60)
	hours := (minutes % (24 * 60)) / 60
	mins := minutes % 60

	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func entryWord(count int) string {
	if count == 1 {
		return "entry"
	}
	return "entries"
}

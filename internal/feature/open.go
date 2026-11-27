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

// handleLockOpen opens a ready lock and reveals the code the lockee set on the
// device. ChastiSafe withholds the code during the lock, so this is the moment
// it becomes available again.
func (f *Feature) handleLockOpen(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	ref, _ := stringOption(opts, "ref")
	// A guild reply is always private, because a code is only one command away
	// from a public channel. A DM or group DM is already limited to the member
	// and their keyholder, so the reply is a normal message there and survives
	// in the conversation history.
	public := i.GuildID == ""
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

	lock, note, err := pickOpenLock(ref, ownLocks(locks, cred.ChastiSafeID), now)
	if err != nil {
		return bot.EditChunks(s, i, err.Error(), !public)
	}

	result, err := client.Open(ctx, lock.ID)
	if err != nil {
		if errors.Is(err, cs.ErrLockNotReady) {
			text := "That lock is not ready to open yet."
			if notReady := openNotReadyError(lock, now); notReady != nil {
				text = notReady.Error()
			}
			return bot.EditChunks(s, i, text, !public)
		}
		f.log.Warn("opening a lock failed", "user_id", userID, "lock_id", lock.ID.String(), "err", err)
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}

	f.forgetLockAutocomplete(userID)
	f.log.Info("lock opened", "user_id", userID, "lock_id", lock.ID.String())

	text := openResultText(lock, result.Code)
	if note != "" {
		text = note + "\n\n" + text
	}
	if err := bot.EditChunks(s, i, text, !public); err != nil {
		return err
	}
	return f.sendRatePrompt(s, i, lock)
}

// pickOpenLock selects a ready lock by reference, or the newest ready lock when
// no reference is given. Errors explain why no lock can be opened.
func pickOpenLock(ref string, locks []cs.LoadedLock, now time.Time) (*cs.LoadedLock, string, error) {
	if strings.TrimSpace(ref) != "" {
		resolved, note, err := resolveRef(ref, locks, nil)
		if err != nil {
			return nil, "", err
		}
		if resolved.Running == nil {
			return nil, "", fmt.Errorf("No active lock matches `%s`.", ref)
		}
		if err := openNotReadyError(resolved.Running, now); err != nil {
			return nil, "", err
		}
		return resolved.Running, note, nil
	}

	if len(locks) == 0 {
		return nil, "", errors.New("You have no active ChastiSafe locks.")
	}

	ready := readyOpenLock(locks, "")
	if ready == nil {
		return nil, "", openNotReadyError(newestRunning(locks), now)
	}

	note := ""
	if countReadyLocks(locks) > 1 {
		note = fmt.Sprintf("Using **%s**, the newest ready lock. Pass `ref` to choose another.", lockName(ready))
	}
	return ready, note, nil
}

// openNotReadyError explains why a lock cannot be opened. It returns nil when
// the lock is ready.
func openNotReadyError(lock *cs.LoadedLock, now time.Time) error {
	if lock == nil {
		return errors.New("No active lock matches that reference.")
	}
	if lock.IsFrozen() {
		return fmt.Errorf("**%s** is frozen. It cannot be opened until it is unfrozen.", lockName(lock))
	}
	if lock.IsReady() {
		return nil
	}
	if lock.IsCardLock() {
		if left := cardsLeft(lock); left > 0 {
			return fmt.Errorf("**%s** is not ready to open yet. %d card%s left in the deck.",
				lockName(lock), left, plural(left))
		}
		if wait := lock.NextPickIn(); wait > 0 {
			return fmt.Errorf("**%s** is not ready to open yet. Next draw %s.",
				lockName(lock), relativeTimestamp(now.Add(wait)))
		}
	}
	if lock.IsTimerLock() {
		if remaining := lock.Remaining(); remaining > 0 {
			return fmt.Errorf("**%s** is not ready to open yet. It unlocks %s.",
				lockName(lock), relativeTimestamp(now.Add(remaining)))
		}
	}
	return fmt.Errorf("**%s** is not ready to open yet.", lockName(lock))
}

// openResultText is the reply after a successful open. The code is shown here,
// and a private /history can show it again if the reply is lost.
func openResultText(lock *cs.LoadedLock, code string) string {
	name := lockName(lock)
	if strings.TrimSpace(code) == "" {
		return fmt.Sprintf(":unlocked: Opened **%s**, but ChastiSafe did not return the device code.\n\nRun `/history` privately to look for it.", name)
	}
	return fmt.Sprintf(":unlocked: Opened **%s**.\n\nDevice code: `%s`\n\nRun `/history` privately if you need it again.", name, code)
}

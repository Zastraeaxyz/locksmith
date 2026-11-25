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

// handleLockCheckin records a check-in on demand, so a member can still check
// in when a reminder was missed or notifications are off.
func (f *Feature) handleLockCheckin(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	ref, _ := stringOption(opts, "ref")
	public := boolOption(opts, "public")
	if err := bot.Defer(s, i, !public); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		f.track("checkin failed")
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		f.track("checkin failed")
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}

	lock, note, err := pickCheckinLock(ref, ownLocks(locks, cred.ChastiSafeID), f.now())
	if err != nil {
		f.track("checkin failed")
		return bot.EditChunks(s, i, err.Error(), !public)
	}

	if refusal := checkinRefusal(lock, f.now()); refusal != "" {
		f.track("checkin failed")
		return bot.EditChunks(s, i, refusal, !public)
	}

	if err := client.CheckIn(ctx, lock.ID); err != nil {
		f.track("checkin failed")
		f.log.Warn("check-in failed", "user_id", userID, "lock_id", lock.ID.String(), "err", err)
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}
	if f.store != nil {
		if err := f.store.ClearCheckin(ctx, userID, lock.ID.String()); err != nil {
			f.log.Warn("clearing check-in state", "user_id", userID, "err", err)
		}
	}
	f.track("checkin success")
	f.log.Info("check-in recorded", "user_id", userID, "lock_id", lock.ID.String())

	now := f.now()
	message := fmt.Sprintf("Checked in for **%s** %s.", lockName(lock), fullTimestamp(now))
	if refreshed, err := client.Loaded(ctx); err == nil {
		if updated := findLockByID(refreshed, lock.ID); updated != nil {
			next := checkinDeadline(updated, now)
			if !next.IsZero() && next.After(now) {
				message += fmt.Sprintf(" Next check-in %s.", relativeTimestamp(next))
			}
		}
	}
	if note != "" {
		message = note + "\n\n" + message
	}
	return bot.EditChunks(s, i, message, !public)
}

// pickCheckinLock selects the lock to check in. With a reference it resolves
// by ID or name. Without one it prefers the lock whose check-in is nearest,
// including an overdue check-in.
func pickCheckinLock(ref string, locks []cs.LoadedLock, now time.Time) (*cs.LoadedLock, string, error) {
	if strings.TrimSpace(ref) != "" {
		resolved, note, err := resolveRef(ref, locks, nil)
		if err != nil {
			return nil, "", err
		}
		if resolved.Running == nil {
			return nil, "", fmt.Errorf("No active lock matches `%s`.", ref)
		}
		lock := resolved.Running
		if lock.CheckInPeriod <= 0 {
			return nil, "", fmt.Errorf("**%s** does not use check-ins.", lockName(lock))
		}
		if lock.IsReady() {
			return nil, "", fmt.Errorf("**%s** is ready to unlock. There is nothing to check in.", lockName(lock))
		}
		return lock, note, nil
	}

	lock := nextCheckinLock(locks, "", now)
	if lock == nil {
		return nil, "", errors.New("None of your active locks need a check-in.")
	}
	note := ""
	if countCheckinLocks(locks) > 1 {
		note = fmt.Sprintf("Using **%s**, the lock with the next check-in. Pass `ref` to choose another.", lockName(lock))
	}
	return lock, note, nil
}

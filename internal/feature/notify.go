package feature

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
)

// NotifyLoop checks due draw subscriptions until ctx is done.
func (f *Feature) NotifyLoop(ctx context.Context, s *discordgo.Session) {
	if f.store == nil {
		return
	}
	ticker := time.NewTicker(f.notifyInterval)
	defer ticker.Stop()

	// Let the gateway connect before the first DM attempt.
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}

	f.runDueNotifications(ctx, s)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.runDueNotifications(ctx, s)
		}
	}
}

func (f *Feature) runDueNotifications(ctx context.Context, s *discordgo.Session) {
	due, err := f.store.DueNotifications(ctx, f.now())
	if err != nil {
		f.log.Warn("loading due notifications", "err", err)
		return
	}
	for _, n := range due {
		if ctx.Err() != nil {
			return
		}
		f.checkNotification(ctx, s, n)
	}
}

func (f *Feature) checkNotification(ctx context.Context, s *discordgo.Session, n store.Notification) {
	userID := n.DiscordUserID

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		f.log.Info("dropping notification subscription", "user_id", userID, "err", err)
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}

	locks, err := client.Loaded(ctx)
	if err != nil {
		if errors.Is(err, cs.ErrUnauthorized) {
			f.log.Info("chastisafe rejected a saved code during notify", "user_id", userID)
			_ = f.store.Unsubscribe(ctx, userID)
			_ = f.store.DeleteCredential(ctx, userID)
			return
		}
		f.log.Warn("notification fetch failed", "user_id", userID, "err", err)
		n.NextCheckAt = f.now().Add(5 * time.Minute)
		_ = f.store.Subscribe(ctx, &n)
		return
	}

	lock := findLock(locks, n.LockID, cred.ChastiSafeID)
	if lock == nil {
		_ = sendDM(s, userID, "Your ChastiSafe card lock is no longer active, so draw notifications are off.")
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}
	if lock.IsReady() {
		_ = sendDM(s, userID, fmt.Sprintf("**%s** is ready to unlock, so draw notifications are off.", lockName(lock)))
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}
	if lock.IsFrozen() {
		n.NextCheckAt = f.now().Add(15 * time.Minute)
		_ = f.store.Subscribe(ctx, &n)
		return
	}

	wait := lock.NextPickIn()
	now := f.now()

	if wait > 0 {
		n.NotifiedReady = false
		n.LastDraws = string(lock.Draws)
		n.NextCheckAt = now.Add(wait + 2*time.Second)
	} else {
		if !n.NotifiedReady {
			msg := fmt.Sprintf("**%s**: a new draw is ready.%s\nRun `/lock draw` in the server.",
				lockName(lock), cardsLine(lock))
			if err := sendDM(s, userID, msg); err != nil {
				f.log.Warn("sending draw notification", "user_id", userID, "err", err)
				n.NextCheckAt = now.Add(5 * time.Minute)
			} else {
				n.LastNotifiedAt = now
				n.NotifiedReady = true
				n.NextCheckAt = now.Add(5 * time.Minute)
				f.log.Info("draw notification sent", "user_id", userID, "lock_id", lock.ID.String())
			}
		} else {
			// Waiting for the member to draw. Poll slowly and do not ping again.
			n.NextCheckAt = now.Add(5 * time.Minute)
		}
		n.LastDraws = string(lock.Draws)
	}

	if err := f.store.Subscribe(ctx, &n); err != nil {
		f.log.Warn("updating notification", "user_id", userID, "err", err)
	}
}

func findLock(locks []cs.LoadedLock, lockID, chastisafeID string) *cs.LoadedLock {
	for idx := range locks {
		if lockID != "" && locks[idx].ID.String() == lockID {
			return &locks[idx]
		}
	}
	return activeCardLock(locks, chastisafeID)
}

func lockName(lock *cs.LoadedLock) string {
	if lock == nil {
		return "your lock"
	}
	if name := strings.TrimSpace(lock.LockName); name != "" {
		return name
	}
	return "your lock"
}

func cardsLine(lock *cs.LoadedLock) string {
	if lock == nil {
		return ""
	}
	var parts []string
	if cards := lock.CardsRemaining(); cards > 0 {
		parts = append(parts, fmt.Sprintf("%d card%s left", cards, plural(cards)))
	}
	if greens := lock.GreensRemaining(); greens > 0 {
		parts = append(parts, fmt.Sprintf("%d green%s needed", greens, plural(greens)))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, ", ") + "."
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func sendDM(s *discordgo.Session, userID, content string) error {
	channel, err := s.UserChannelCreate(userID)
	if err != nil {
		return fmt.Errorf("opening DM channel: %w", err)
	}
	if channel == nil || channel.ID == "" {
		return errors.New("no DM channel returned")
	}
	_, err = s.ChannelMessageSend(channel.ID, content)
	return err
}

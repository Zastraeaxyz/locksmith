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

	now := f.now()
	lock := findLock(locks, n.LockID, cred.ChastiSafeID, now)
	if lock == nil {
		_ = sendDM(s, userID, "Your ChastiSafe lock is no longer active, so notifications are off.")
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}
	if lock.IsReady() {
		_ = sendDM(s, userID, fmt.Sprintf("**%s** is ready to unlock, so notifications are off.", lockName(lock)))
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}
	sendFailed := f.notifyCheckin(s, userID, lock, cred.ChastiSafeID, now, &n)

	if lock.IsFrozen() {
		next := now.Add(15 * time.Minute)
		if checkin := f.nextCheckinCheck(lock, now); !checkin.IsZero() && checkin.Before(next) {
			next = checkin
		}
		n.NextCheckAt = next
		if err := f.store.Subscribe(ctx, &n); err != nil {
			f.log.Warn("updating notification", "user_id", userID, "err", err)
		}
		return
	}

	draws := lock.DrawsAvailable()

	drawFailed := false
	if draws > n.LastPicks {
		if err := sendDM(s, userID, notificationText(lock, now)); err != nil {
			f.log.Warn("sending draw notification", "user_id", userID, "err", err)
			drawFailed = true
			sendFailed = true
		} else {
			n.LastNotifiedAt = now
			f.log.Info("draw notification sent",
				"user_id", userID,
				"lock_id", lock.ID.String(),
				"draws", draws,
			)
		}
	}

	n.LockID = lock.ID.String()
	if !drawFailed {
		n.LastDraws = string(lock.Draws)
		n.LastPicks = draws
	}
	n.NextCheckAt = f.nextNotificationCheck(lock, now, draws, sendFailed)

	if err := f.store.Subscribe(ctx, &n); err != nil {
		f.log.Warn("updating notification", "user_id", userID, "err", err)
	}
}

// notifyCheckin sends any due check-in reminder and records the cycle stage.
// It reports whether a send failed, so the scheduler retries soon.
func (f *Feature) notifyCheckin(s *discordgo.Session, userID string, lock *cs.LoadedLock, chastisafeID string, now time.Time, n *store.Notification) bool {
	if n == nil {
		return false
	}
	if lock.CheckInPeriod <= 0 || lock.LockeeID.String() != chastisafeID {
		n.CheckinDueAt = time.Time{}
		n.CheckinStage = ""
		return false
	}

	reminder := f.checkinReminderFor(lock, now, *n)
	if reminder.Send == "" {
		n.CheckinDueAt = reminder.Due
		n.CheckinStage = reminder.Stage
		return false
	}

	if err := sendDMWithComponents(s, userID,
		checkinText(lock, reminder.Send, reminder.Due),
		checkinButtons(lock),
	); err != nil {
		f.log.Warn("sending check-in notification", "user_id", userID, "err", err)
		return true
	}

	n.CheckinDueAt = reminder.Due
	n.CheckinStage = reminder.Stage
	f.log.Info("check-in notification sent",
		"user_id", userID,
		"lock_id", lock.ID.String(),
		"stage", reminder.Stage,
	)
	return false
}

// nextNotificationCheck picks the next poll for draws and check-ins.
func (f *Feature) nextNotificationCheck(lock *cs.LoadedLock, now time.Time, draws int, retry bool) time.Time {
	var next time.Time
	if lock.IsCardLock() {
		next = now.Add(5 * time.Minute)
		if wait := lock.NextPickIn(); wait > 0 {
			next = now.Add(wait + 2*time.Second)
			if draws > 0 && lock.MaxPicks > 0 && draws >= lock.MaxPicks {
				// Capped: wait for the member to draw before more can accrue.
				next = now.Add(5 * time.Minute)
			}
		}
	}
	if retry {
		next = now.Add(5 * time.Minute)
	}
	if checkin := f.nextCheckinCheck(lock, now); !checkin.IsZero() && (next.IsZero() || checkin.Before(next)) {
		next = checkin
	}
	if next.IsZero() {
		next = now.Add(15 * time.Minute)
	}
	return next
}

func notificationText(lock *cs.LoadedLock, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**: %s available", lockName(lock), drawsPhrase(lock.DrawsAvailable()))
	if lock.MaxPicks > 0 {
		fmt.Fprintf(&b, " of %d", lock.MaxPicks)
	}
	b.WriteString(".")

	var parts []string
	if cards := cardsLeft(lock); cards > 0 {
		parts = append(parts, fmt.Sprintf("%d card%s left", cards, plural(cards)))
	}
	if greens := lock.GreensRemaining(); greens > 0 {
		parts = append(parts, fmt.Sprintf("%d green%s needed", greens, plural(greens)))
	}
	if len(parts) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString(".")
	}

	if wait := lock.NextPickIn(); wait > 0 {
		fmt.Fprintf(&b, "\nNext draw %s.", relativeTimestamp(now.Add(wait)))
	}
	if next := lock.NextCheckInIn(); next > 0 {
		fmt.Fprintf(&b, "\nCheck-in %s.", relativeTimestamp(now.Add(next)))
	}
	if hygiene := lock.NextHygieneIn(); hygiene > 0 {
		fmt.Fprintf(&b, "\nHygiene unlock %s.", relativeTimestamp(now.Add(hygiene)))
	}
	b.WriteString("\nRun `/lock draw` in the server.")
	return b.String()
}

func findLock(locks []cs.LoadedLock, lockID, chastisafeID string, now time.Time) *cs.LoadedLock {
	for idx := range locks {
		if lockID != "" && locks[idx].ID.String() == lockID {
			return &locks[idx]
		}
	}
	if lock := activeCardLock(locks, chastisafeID); lock != nil {
		return lock
	}
	return activeCheckinLock(locks, chastisafeID, now)
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

func cardsLeft(lock *cs.LoadedLock) int {
	cards, _ := cardsRemaining(lock)
	return cards
}

// cardsRemaining reports the cards left and whether the lock reports a deck.
// A reported but empty deck is zero cards, while a missing deck means the
// count is unknown.
func cardsRemaining(lock *cs.LoadedLock) (int, bool) {
	if lock == nil {
		return 0, false
	}
	if lock.Deck != nil {
		return lock.Deck.Total(), true
	}
	if lock.CardCount > 0 {
		return lock.CardCount, true
	}
	return 0, false
}

func drawsPhrase(count int) string {
	if count == 1 {
		return "1 draw"
	}
	return fmt.Sprintf("%d draws", count)
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func sendDM(s *discordgo.Session, userID, content string) error {
	return sendDMWithComponents(s, userID, content, nil)
}

func sendDMWithComponents(s *discordgo.Session, userID, content string, components []discordgo.MessageComponent) error {
	channel, err := s.UserChannelCreate(userID)
	if err != nil {
		return fmt.Errorf("opening DM channel: %w", err)
	}
	if channel == nil || channel.ID == "" {
		return errors.New("no DM channel returned")
	}
	_, err = s.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{
		Content:    content,
		Components: components,
	})
	return err
}

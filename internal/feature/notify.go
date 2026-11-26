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

// Notification kinds decide which messages replace each other. A newer
// notification deletes the previous one of the same kind.
const (
	notificationKindDraw    = "draw"
	notificationKindCheckin = "checkin"
	notificationKindNotice  = "notice"

	// notificationMessageTTL is how long a notification DM stays before the
	// bot deletes it. Notifications are transient by design.
	notificationMessageTTL = 24 * time.Hour
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
	f.expireNotificationMessages(ctx, s)

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
		f.clearNotificationMessages(ctx, s, userID)
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}

	locks, err := client.Loaded(ctx)
	if err != nil {
		if errors.Is(err, cs.ErrUnauthorized) {
			f.log.Info("chastisafe rejected a saved code during notify", "user_id", userID)
			f.clearNotificationMessages(ctx, s, userID)
			_ = f.store.Unsubscribe(ctx, userID)
			f.apiFailure(ctx, userID, err)
			return
		}
		f.log.Warn("notification fetch failed", "user_id", userID, "err", err)
		f.reschedule(ctx, userID, f.now().Add(5*time.Minute))
		return
	}

	now := f.now()
	lock := findLock(locks, n.LockID, cred.ChastiSafeID, now)
	if lock == nil {
		f.clearNotificationMessages(ctx, s, userID)
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}
	if lock.IsReady() {
		f.clearNotificationMessages(ctx, s, userID)
		_ = f.sendNotification(ctx, s, userID, notificationKindNotice,
			fmt.Sprintf("**%s** is ready to unlock, so notifications are off.", lockName(lock)), nil)
		_ = f.store.Unsubscribe(ctx, userID)
		return
	}
	checkin, sendFailed := f.notifyCheckin(ctx, s, userID, lock, cred.ChastiSafeID, now, n)

	if lock.IsFrozen() {
		next := f.nextFreezeCheck(ctx, client, lock, now)
		if next.IsZero() {
			f.clearNotificationMessages(ctx, s, userID)
			_ = f.sendNotification(ctx, s, userID, notificationKindNotice, fmt.Sprintf(
				"**%s** is still frozen after the expected freeze window, so draw notifications are off. Use `/lock notify` to turn them back on.",
				lockName(lock),
			), nil)
			_ = f.store.Unsubscribe(ctx, userID)
			return
		}
		if checkinAt := f.nextCheckinCheck(lock, now); !checkinAt.IsZero() && checkinAt.Before(next) {
			next = checkinAt
		}
		f.saveCheckin(ctx, userID, lock.ID, checkin, n)
		f.reschedule(ctx, userID, next)
		return
	}

	draws := lock.DrawsAvailable()

	drawFailed := false
	record := store.Draws{
		DiscordUserID:  userID,
		LockID:         lock.ID.String(),
		LastDraws:      n.LastDraws,
		LastPicks:      n.LastPicks,
		LastNotifiedAt: n.LastNotifiedAt,
	}
	if draws > n.LastPicks {
		if err := f.sendNotification(ctx, s, userID, notificationKindDraw,
			notificationText(lock, now), drawNotificationButtons(lock)); err != nil {
			f.log.Warn("sending draw notification", "user_id", userID, "err", err)
			drawFailed = true
			sendFailed = true
		} else {
			record.LastNotifiedAt = now
			f.log.Info("draw notification sent",
				"user_id", userID,
				"lock_id", lock.ID.String(),
				"draws", draws,
			)
		}
	}

	if !drawFailed {
		record.LastDraws = string(lock.Draws)
		record.LastPicks = draws
	}
	record.NextCheckAt = f.nextNotificationCheck(lock, now, draws, sendFailed)

	f.saveCheckin(ctx, userID, lock.ID, checkin, n)
	if err := f.store.RecordDraws(ctx, record); err != nil {
		f.log.Warn("updating notification", "user_id", userID, "err", err)
	}
}

// saveCheckin stores a reminder stage when the poll changed it, so a poll that
// found nothing new does not rewrite the row.
func (f *Feature) saveCheckin(ctx context.Context, userID string, lockID cs.ID, next checkinReminder, prev store.Notification) {
	if next.Due.Unix() == prev.CheckinDueAt.Unix() && next.Stage == prev.CheckinStage {
		return
	}
	if err := f.store.RecordReminder(ctx, store.Reminder{
		DiscordUserID: userID,
		LockID:        lockID.String(),
		DueAt:         next.Due,
		Stage:         next.Stage,
		NextCheckAt:   prev.NextCheckAt,
	}); err != nil {
		f.log.Warn("updating check-in state", "user_id", userID, "err", err)
	}
}

// reschedule moves the next poll without touching what the poll has recorded.
func (f *Feature) reschedule(ctx context.Context, userID string, next time.Time) {
	if err := f.store.Reschedule(ctx, userID, next); err != nil {
		f.log.Warn("updating notification", "user_id", userID, "err", err)
	}
}

// notifyCheckin sends any due check-in reminder. It returns the reminder state
// to store and whether a send failed. The stage is only advanced when the
// reminder was delivered, so a failed send is retried in the same cycle.
func (f *Feature) notifyCheckin(ctx context.Context, s *discordgo.Session, userID string, lock *cs.LoadedLock, chastisafeID string, now time.Time, prev store.Notification) (checkinReminder, bool) {
	if lock.CheckInPeriod <= 0 || lock.LockeeID.String() != chastisafeID {
		return checkinReminder{}, false
	}

	reminder := f.checkinReminderFor(lock, now, prev)
	if reminder.Send == "" {
		return reminder, false
	}

	if err := f.sendNotification(ctx, s, userID, notificationKindCheckin,
		checkinText(lock, reminder.Send, reminder.Due),
		checkinNotificationComponents(lock, now),
	); err != nil {
		f.log.Warn("sending check-in notification", "user_id", userID, "err", err)
		return checkinReminder{Due: prev.CheckinDueAt, Stage: prev.CheckinStage}, true
	}

	f.log.Info("check-in notification sent",
		"user_id", userID,
		"lock_id", lock.ID.String(),
		"stage", reminder.Stage,
	)
	return reminder, false
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

// nextFreezeCheck picks when to poll a frozen lock again. A card freeze lasts
// between two and four draw intervals, so it samples half-interval steps
// across that window. It returns the zero time once the window has passed and
// polling should stop. A keyholder freeze has no known end, so it keeps the
// usual cadence.
func (f *Feature) nextFreezeCheck(ctx context.Context, client *cs.Client, lock *cs.LoadedLock, now time.Time) time.Time {
	if lock == nil || lock.FrozenByKeyholder {
		return now.Add(15 * time.Minute)
	}
	interval := time.Duration(lock.Regularity) * time.Minute
	if interval <= 0 {
		return now.Add(15 * time.Minute)
	}
	entries, err := f.freezeLog(ctx, client, lock)
	if err != nil {
		f.log.Warn("loading freeze history", "lock_id", lock.ID.String(), "err", err)
		return now.Add(15 * time.Minute)
	}
	since, ok := freezeStartFor(lock, entries)
	if !ok || since.IsZero() {
		return now.Add(15 * time.Minute)
	}
	next, ok := nextFreezeCheckAt(since, interval, now)
	if !ok {
		return time.Time{}
	}
	return next
}

// nextFreezeCheckAt returns the next card-freeze poll after now. Checkpoints
// run from 2x to 4x the draw interval in half-interval steps, so a one hour
// draw time polls at 2h, 2h30, 3h, 3h30, and 4h. The final poll at 4x is the
// last chance to see the freeze end.
func nextFreezeCheckAt(since time.Time, interval time.Duration, now time.Time) (time.Time, bool) {
	if interval <= 0 {
		return time.Time{}, false
	}
	for step := 4; step <= 8; step++ {
		at := since.Add(time.Duration(step) * interval / 2)
		if at.After(now) {
			return at.Add(2 * time.Second), true
		}
	}
	return time.Time{}, false
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
	b.WriteString("\nPress **Open draw board** to draw here, or run `/lock draw`.")
	return b.String()
}

// drawNotificationButtons offers the draw board directly in the notification
// DM, so the member does not have to run a command.
func drawNotificationButtons(lock *cs.LoadedLock) []discordgo.MessageComponent {
	if lock == nil {
		return nil
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: drawBoardNamespace + ":open:" + lock.ID.String(),
				Label:    "Open draw board",
				Style:    discordgo.PrimaryButton,
			},
		}},
	}
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

// sentNotification is the location of a notification DM that was just sent.
type sentNotification struct {
	ChannelID string
	MessageID string
}

// sendNotification sends one DM and records it so it can be deleted later. A
// newer message of the same kind replaces the previous one.
func (f *Feature) sendNotification(ctx context.Context, s *discordgo.Session, userID, kind, content string, components []discordgo.MessageComponent) error {
	sent, err := sendDMWithComponents(s, userID, content, components)
	if err != nil {
		return err
	}

	now := f.now()
	if err := f.store.RecordNotificationMessage(ctx, store.NotificationMessage{
		MessageID:     sent.MessageID,
		DiscordUserID: userID,
		ChannelID:     sent.ChannelID,
		Kind:          kind,
		CreatedAt:     now,
		DeleteAt:      now.Add(notificationMessageTTL),
	}); err != nil {
		f.log.Warn("recording a notification message", "user_id", userID, "err", err)
		return nil
	}

	f.supersedeNotificationMessages(ctx, s, userID, kind, sent.MessageID)
	return nil
}

// supersedeNotificationMessages deletes the member's older messages of one
// kind, so only the newest notification of that kind remains.
func (f *Feature) supersedeNotificationMessages(ctx context.Context, s *discordgo.Session, userID, kind, keepMessageID string) {
	if f.store == nil {
		return
	}
	messages, err := f.store.NotificationMessages(ctx, userID)
	if err != nil {
		f.log.Warn("loading notification messages", "user_id", userID, "err", err)
		return
	}
	for _, message := range supersededNotificationMessages(messages, kind, keepMessageID) {
		f.deleteNotificationMessage(ctx, s, message)
	}
}

// supersededNotificationMessages picks the stored messages that a new message
// of the same kind replaces.
func supersededNotificationMessages(messages []store.NotificationMessage, kind, keepMessageID string) []store.NotificationMessage {
	var out []store.NotificationMessage
	for _, message := range messages {
		if message.Kind == kind && message.MessageID != keepMessageID {
			out = append(out, message)
		}
	}
	return out
}

// clearNotificationMessages deletes every notification DM a member still has,
// so ending a subscription leaves no stale buttons behind.
func (f *Feature) clearNotificationMessages(ctx context.Context, s *discordgo.Session, userID string) {
	if f.store == nil {
		return
	}
	messages, err := f.store.NotificationMessages(ctx, userID)
	if err != nil {
		f.log.Warn("loading notification messages", "user_id", userID, "err", err)
		return
	}
	for _, message := range messages {
		f.deleteNotificationMessage(ctx, s, message)
	}
}

// expireNotificationMessages deletes notification DMs past their lifetime.
func (f *Feature) expireNotificationMessages(ctx context.Context, s *discordgo.Session) {
	if f.store == nil {
		return
	}
	messages, err := f.store.DueNotificationMessages(ctx, f.now())
	if err != nil {
		f.log.Warn("loading expired notification messages", "err", err)
		return
	}
	for _, message := range messages {
		if ctx.Err() != nil {
			return
		}
		f.deleteNotificationMessage(ctx, s, message)
	}
}

// deleteNotificationMessage deletes one DM and forgets it. Deletion is best
// effort: a message Discord refuses to delete must not be retried forever.
func (f *Feature) deleteNotificationMessage(ctx context.Context, s *discordgo.Session, message store.NotificationMessage) {
	if s != nil {
		if err := s.ChannelMessageDelete(message.ChannelID, message.MessageID); err != nil {
			f.log.Warn("deleting a notification message", "user_id", message.DiscordUserID, "err", err)
		}
	}
	if err := f.store.ForgetNotificationMessage(ctx, message.MessageID); err != nil {
		f.log.Warn("forgetting a notification message", "user_id", message.DiscordUserID, "err", err)
	}
}

func sendDMWithComponents(s *discordgo.Session, userID, content string, components []discordgo.MessageComponent) (sentNotification, error) {
	channel, err := s.UserChannelCreate(userID)
	if err != nil {
		return sentNotification{}, fmt.Errorf("opening DM channel: %w", err)
	}
	if channel == nil || channel.ID == "" {
		return sentNotification{}, errors.New("no DM channel returned")
	}
	msg, err := s.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{
		Content:    content,
		Components: components,
	})
	if err != nil {
		return sentNotification{}, err
	}
	if msg == nil || msg.ID == "" {
		return sentNotification{}, errors.New("no DM message returned")
	}
	return sentNotification{ChannelID: channel.ID, MessageID: msg.ID}, nil
}

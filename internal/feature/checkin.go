package feature

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
	"locksmith/internal/store"
)

const (
	checkinNamespace = "checkin"
	// checkinFinalLead is the mandatory reminder before a check-in is due.
	// It cannot be disabled.
	checkinFinalLead = 5 * time.Minute
	// checkinDeadlineDrift is the jitter tolerated before a moved deadline is
	// treated as a new check-in cycle.
	checkinDeadlineDrift = 2 * time.Minute
)

// checkinReminder is the reminder decision for the current check-in cycle.
type checkinReminder struct {
	// Send is the stage to notify now, or empty when nothing is due.
	Send string
	// Stage is the highest stage reached in this cycle.
	Stage string
	// Due is the absolute check-in moment for this cycle.
	Due time.Time
}

// checkinIn is the remaining time before the next required check-in. It uses
// the feature clock rather than time.Until so every check sees the same now.
func checkinIn(lock *cs.LoadedLock, now time.Time) time.Duration {
	if lock == nil || lock.CheckInPeriod <= 0 {
		return 0
	}
	if lock.TimeToNextCheckIn != 0 {
		return time.Duration(lock.TimeToNextCheckIn) * time.Second
	}
	if !lock.NextCheckInTime.IsZero() {
		return lock.NextCheckInTime.Time.Sub(now)
	}
	return 0
}

// checkinDeadline is the absolute moment of the next check-in, or the zero
// time when the server reports no timestamp.
func checkinDeadline(lock *cs.LoadedLock, now time.Time) time.Time {
	if lock == nil || lock.CheckInPeriod <= 0 {
		return time.Time{}
	}
	if !lock.NextCheckInTime.IsZero() {
		return lock.NextCheckInTime.Time
	}
	if lock.TimeToNextCheckIn != 0 {
		return now.Add(time.Duration(lock.TimeToNextCheckIn) * time.Second)
	}
	return time.Time{}
}

// checkinWindowOpen reports whether ChastiSafe should accept a check-in now.
// The window opens at the due moment and stays open while it is overdue.
// ChastiSafe's check-in tolerance is the late window after the due time, not an
// early one, so it never opens the window before the due moment.
func checkinWindowOpen(lock *cs.LoadedLock, now time.Time) bool {
	if lock == nil || lock.CheckInPeriod <= 0 {
		return false
	}
	return checkinIn(lock, now) <= 0
}

// checkinRefusal explains that a check-in is not available yet. It returns an
// empty string when the window is open.
func checkinRefusal(lock *cs.LoadedLock, now time.Time) string {
	if lock == nil {
		return "That lock is no longer active."
	}
	if checkinWindowOpen(lock, now) {
		return ""
	}

	remaining := checkinIn(lock, now)
	due := checkinDeadline(lock, now)
	if due.IsZero() || !due.After(now) {
		due = now.Add(remaining)
	}
	return fmt.Sprintf("Check-in for **%s** is not due yet. It is due %s.",
		lockName(lock), relativeTimestamp(due))
}

// checkinReminderFor decides whether to remind and which cycle stage is due.
// The lead stage is configurable; the final stage and the overdue stage always
// fire.
func (f *Feature) checkinReminderFor(lock *cs.LoadedLock, now time.Time, prev store.Notification) checkinReminder {
	if lock == nil || lock.CheckInPeriod <= 0 {
		return checkinReminder{}
	}

	remaining := checkinIn(lock, now)
	due := checkinDeadline(lock, now)
	if due.IsZero() {
		due = prev.CheckinDueAt
		if due.IsZero() {
			due = now
		}
	}

	// A deadline that moved beyond polling jitter is a new cycle.
	sameCycle := !prev.CheckinDueAt.IsZero() &&
		absDuration(due.Sub(prev.CheckinDueAt)) <= checkinDeadlineDrift
	if remaining <= 0 && sameCycle {
		// Keep the original deadline once it has passed; the server may stop
		// reporting a timestamp while the check-in is overdue.
		due = prev.CheckinDueAt
	}

	stage := ""
	switch {
	case remaining <= 0:
		stage = "overdue"
	case remaining <= checkinFinalLead:
		stage = "final"
	case f.checkinLead > 0 && remaining <= f.checkinLead:
		stage = "lead"
	}

	prevStage := ""
	if sameCycle {
		prevStage = prev.CheckinStage
	}
	send := ""
	if checkinStageRank(stage) > checkinStageRank(prevStage) {
		send = stage
	}
	return checkinReminder{Send: send, Stage: stage, Due: due}
}

// checkinStageRank orders reminder stages so a passed stage is never repeated.
func checkinStageRank(stage string) int {
	switch stage {
	case "lead":
		return 1
	case "final":
		return 2
	case "overdue":
		return 3
	default:
		return 0
	}
}

// nextCheckinCheck is when to poll for the next check-in transition.
func (f *Feature) nextCheckinCheck(lock *cs.LoadedLock, now time.Time) time.Time {
	if lock == nil || lock.CheckInPeriod <= 0 {
		return time.Time{}
	}
	remaining := checkinIn(lock, now)
	if remaining <= 0 {
		return now.Add(5 * time.Minute)
	}

	lead := f.checkinLead
	if lead < checkinFinalLead {
		lead = checkinFinalLead
	}

	var wait time.Duration
	switch {
	case remaining > lead:
		wait = remaining - lead
	case remaining > checkinFinalLead:
		wait = remaining - checkinFinalLead
	default:
		wait = remaining
	}
	if wait < time.Minute {
		wait = time.Minute
	}
	return now.Add(wait + 2*time.Second)
}

// checkinText renders a check-in reminder. Exact moments use Discord
// timestamps, never hard-coded countdowns.
func checkinText(lock *cs.LoadedLock, stage string, due time.Time) string {
	name := lockName(lock)
	switch stage {
	case "final":
		return fmt.Sprintf("**%s**: check-in due soon, %s.", name, relativeTimestamp(due))
	case "overdue":
		return fmt.Sprintf("**%s**: check-in is overdue. It was due %s.", name, relativeTimestamp(due))
	default:
		return fmt.Sprintf("**%s**: check-in due %s.", name, relativeTimestamp(due))
	}
}

// checkinButtons returns the Check-in button for a lock.
func checkinButtons(lock *cs.LoadedLock) []discordgo.MessageComponent {
	if lock == nil {
		return nil
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Check-in",
				Style:    discordgo.PrimaryButton,
				CustomID: checkinNamespace + ":" + lock.ID.String(),
			},
		}},
	}
}

// checkinNotificationComponents offers the button only while a check-in is
// actually available.
func checkinNotificationComponents(lock *cs.LoadedLock, now time.Time) []discordgo.MessageComponent {
	if !checkinWindowOpen(lock, now) {
		return nil
	}
	return checkinButtons(lock)
}

// handleCheckinComponent records a check-in from a reminder button.
func (f *Feature) handleCheckinComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.MessageComponentData()
	lockID := strings.TrimPrefix(data.CustomID, checkinNamespace+":")
	if lockID == "" || lockID == data.CustomID {
		return bot.ReplyEphemeral(s, i, "This button is out of date.")
	}

	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		f.track("checkin failed")
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, userID, err))
	}

	locks, err := client.Loaded(ctx)
	if err != nil {
		f.track("checkin failed")
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, userID, err))
	}

	lock := findLockByID(locks, cs.ID(lockID))
	if lock == nil {
		f.track("checkin failed")
		return bot.ReplyEphemeralFollowup(s, i, "That lock is no longer active.")
	}
	if cred.ChastiSafeID != "" && lock.LockeeID.String() != cred.ChastiSafeID {
		f.track("checkin failed")
		return bot.ReplyEphemeralFollowup(s, i, "That check-in is not yours to complete.")
	}
	if refusal := checkinRefusal(lock, f.now()); refusal != "" {
		f.track("checkin failed")
		return bot.ReplyEphemeralFollowup(s, i, refusal)
	}

	if err := client.CheckIn(ctx, lock.ID); err != nil {
		f.track("checkin failed")
		f.log.Warn("check-in failed", "user_id", userID, "lock_id", lock.ID.String(), "err", err)
		return bot.ReplyEphemeralFollowup(s, i, f.apiFailure(ctx, userID, err))
	}

	if f.store != nil {
		if err := f.store.ClearCheckin(ctx, userID, lock.ID.String()); err != nil {
			f.log.Warn("clearing check-in state", "user_id", userID, "err", err)
		}
	}
	f.track("checkin success")
	f.log.Info("check-in recorded", "user_id", userID, "lock_id", lock.ID.String())

	return bot.EditMessage(s, i,
		fmt.Sprintf("Checked in for **%s** %s.", lockName(lock), relativeTimestamp(f.now())),
		nil, nil)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

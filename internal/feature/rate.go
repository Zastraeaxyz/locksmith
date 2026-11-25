package feature

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const rateNamespace = "rate"

// handleLockRate shows a rating prompt for the most recently completed lock.
func (f *Feature) handleLockRate(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.EditMessage(s, i, f.apiFailure(ctx, userID, err), nil, nil)
	}
	recent, err := client.Recent(ctx)
	if err != nil {
		f.log.Warn("loading recent locks for rating", "user_id", userID, "err", err)
		return bot.EditMessage(s, i, f.apiFailure(ctx, userID, err), nil, nil)
	}

	lock, problem := rateTarget(recent)
	if lock == nil {
		return bot.EditMessage(s, i, problem, nil, nil)
	}
	return bot.EditMessage(s, i, ratePrompt(rateName(lock)), nil, rateButtons(lock.LockID))
}

// rateTarget picks the most recently completed lock and explains when it
// cannot be rated. An empty problem with a lock means it is ready to rate.
func rateTarget(recent []cs.RecentLock) (*cs.RecentLock, string) {
	var newest *cs.RecentLock
	var newestAt time.Time
	for idx := range recent {
		lock := &recent[idx]
		if lock.Fake {
			continue
		}
		at := historySortKey(*lock)
		if newest == nil || at.After(newestAt) {
			newest, newestAt = lock, at
		}
	}
	if newest == nil {
		return nil, "You have no completed locks to rate."
	}

	name := rateName(newest)
	switch {
	case strings.EqualFold(strings.TrimSpace(string(newest.LockPurpose)), string(cs.PurposeTest)):
		return nil, fmt.Sprintf("**%s** is a test lock, so it cannot be rated.", name)
	case strings.EqualFold(strings.TrimSpace(newest.Keyholder), "<self>"):
		return nil, fmt.Sprintf("**%s** is a self lock, so it cannot be rated.", name)
	case newest.UnlockReason == cs.UnlockReasonAbandoned:
		return nil, fmt.Sprintf("**%s** was abandoned, so it cannot be rated.", name)
	case newest.Rating > 0:
		return nil, fmt.Sprintf("**%s** is already rated %d/5.", name, newest.Rating)
	}
	return newest, ""
}

func rateName(lock *cs.RecentLock) string {
	if name := strings.TrimSpace(lock.LockName); name != "" {
		return name
	}
	return "your lock"
}

func ratePrompt(name string) string {
	return fmt.Sprintf("How was **%s**? Rate the lock from 1 to 5.", name)
}

// rateButtons renders the one-to-five rating controls for a lock.
func rateButtons(lockID cs.ID) []discordgo.MessageComponent {
	buttons := make([]discordgo.MessageComponent, 0, 5)
	for score := 1; score <= 5; score++ {
		buttons = append(buttons, discordgo.Button{
			CustomID: fmt.Sprintf("%s:%s:%d", rateNamespace, lockID, score),
			Label:    strconv.Itoa(score),
			Style:    discordgo.SecondaryButton,
			Emoji:    &discordgo.ComponentEmoji{Name: "⭐"},
		})
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: buttons}}
}

// canRateLock reports whether a lock that just opened is worth a rating
// prompt. Fake locks never appear here, and test and self locks cannot be
// rated.
func canRateLock(lock *cs.LoadedLock) bool {
	if lock == nil || lock.IsSelfLock() {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(string(lock.LockPurpose)), string(cs.PurposeTest))
}

// sendRatePrompt posts the private rating controls next to an opened lock's
// code. The prompt is always ephemeral, even in a DM, because the buttons are
// only for the member.
func (f *Feature) sendRatePrompt(s *discordgo.Session, i *discordgo.InteractionCreate, lock *cs.LoadedLock) error {
	if !canRateLock(lock) {
		return nil
	}
	return bot.ReplyFollowupEmbed(s, i, ratePrompt(lockName(lock)), nil, rateButtons(lock.ID), true)
}

// handleRateComponent records a rating chosen from a rating prompt.
func (f *Feature) handleRateComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 3 {
		return bot.ReplyEphemeral(s, i, "This rating is out of date.")
	}
	lockID := cs.ID(parts[1])
	score, err := strconv.Atoi(parts[2])
	if err != nil || score < 1 || score > 5 {
		return bot.ReplyEphemeral(s, i, "This rating is out of date.")
	}

	userID := bot.UserID(i)
	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.EditMessage(s, i, f.apiFailure(ctx, userID, err), nil, nil)
	}
	if err := client.Rate(ctx, lockID, score); err != nil {
		f.log.Warn("rating a lock failed", "user_id", userID, "lock_id", lockID.String(), "err", err)
		return bot.EditMessage(s, i, f.apiFailure(ctx, userID, err), nil, nil)
	}
	f.track("lock rated")
	return bot.EditMessage(s, i, fmt.Sprintf(":star: Rated %d/5. Thanks!", score), nil, nil)
}

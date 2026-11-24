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
	"locksmith/internal/store"
)

const modalNamespace = "login"

func loginCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "login",
		Description: "Store your ChastiSafe developer code",
	}
}

func logoutCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "logout",
		Description: "Forget your stored ChastiSafe developer code",
	}
}

func lockCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lock",
		Description: "Work with your ChastiSafe lock",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "status",
				Description: "Show your active ChastiSafe locks",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "draw",
				Description: "Draw the next card from your card lock",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "notify",
				Description: "Get pinged when a new draw is ready",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "enabled",
						Description: "Turn draw notifications on or off (default on)",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "log",
				Description: "Show the draw log for a lock",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "full",
						Description: "Show the whole log instead of draws from the last 5 minutes",
						Required:    false,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "ref",
						Description: "Lock ID or name, including previous locks",
						Required:    false,
					},
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "public",
						Description: "Post the reply publicly instead of privately",
						Required:    false,
					},
				},
			},
		},
	}
}

// handleLoginCommand shows the developer-code modal.
func (f *Feature) handleLoginCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if f.store == nil || f.box == nil {
		return bot.ReplyEphemeral(s, i, "Locksmith is not configured to store codes.")
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			Title:    "ChastiSafe login",
			CustomID: modalNamespace + ":code",
			Components: []discordgo.MessageComponent{
				&discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						&discordgo.TextInput{
							CustomID:    "code",
							Label:       "Developer code",
							Style:       discordgo.TextInputShort,
							Placeholder: "Paste the code from your ChastiSafe profile",
							MinLength:   1,
							MaxLength:   512,
							Required:    true,
						},
					},
				},
			},
		},
	})
}

// handleLoginModal validates the submitted code and stores it encrypted.
func (f *Feature) handleLoginModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if !f.loginAttemptAllowed(userID) {
		return bot.ReplyEphemeral(s, i, "Too many login attempts. Wait a few minutes and try again.")
	}

	code := modalValue(i.ModalSubmitData().Components, "code")
	if strings.TrimSpace(code) == "" {
		return bot.ReplyEphemeral(s, i, "The developer code was empty. Run `/login` again.")
	}

	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	identity, err := f.authenticate(ctx, userID, code)
	if err != nil {
		f.log.Info("login failed", "user_id", userID, "err", err)
		switch {
		case errors.Is(err, errNotOwner):
			return sendEphemeral(s, i,
				"That code belongs to a different ChastiSafe account.\n"+
					"Link your Discord account in ChastiSafe, then run `/login` again.")
		case errors.Is(err, cs.ErrUnauthorized):
			return sendEphemeral(s, i, "ChastiSafe rejected that developer code. Check it and try again.")
		case errors.Is(err, errNotConfigured):
			return sendEphemeral(s, i, "Locksmith is not configured to store codes.")
		default:
			f.log.Warn("login request failed", "user_id", userID, "err", err)
			return sendEphemeral(s, i, "I could not reach ChastiSafe right now. Try again in a moment.")
		}
	}

	f.loginSucceeded(userID)
	f.log.Info("login stored",
		"user_id", userID,
		"chastisafe_user", identity.User,
		"chastisafe_id", identity.ID.String(),
	)
	return sendEphemeral(s, i, fmt.Sprintf(
		"Logged in as **%s** (ID `%s`).\n"+
			"Use `/lock status`, `/lock draw`, and `/lock notify`.",
		identity.User, identity.ID,
	))
}

// handleLogoutCommand removes the caller's stored code.
func (f *Feature) handleLogoutCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}
	if err := f.forget(ctx, userID); err != nil {
		if errors.Is(err, errNotConfigured) {
			return sendEphemeral(s, i, "Locksmith is not configured to store codes.")
		}
		return err
	}
	f.log.Info("logout", "user_id", userID)
	return sendEphemeral(s, i, "Your saved ChastiSafe code has been removed.")
}

// handleLockCommand routes /lock subcommands.
func (f *Feature) handleLockCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return bot.ReplyEphemeral(s, i, "Choose a `/lock` subcommand.")
	}
	sub := data.Options[0]

	switch sub.Name {
	case "status":
		return f.handleLockStatus(ctx, s, i)
	case "draw":
		return f.handleLockDraw(ctx, s, i)
	case "notify":
		enabled := true
		if len(sub.Options) > 0 {
			enabled = sub.Options[0].BoolValue()
		}
		return f.handleLockNotify(ctx, s, i, enabled)
	case "log":
		return f.handleLockLog(ctx, s, i, sub.Options)
	default:
		return bot.ReplyEphemeral(s, i, "Unknown `/lock` subcommand.")
	}
}

func (f *Feature) handleLockStatus(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}

	views := ownLockViews(locks, cred.ChastiSafeID)
	if len(views) == 0 {
		return sendEphemeral(s, i, "You have no active ChastiSafe locks.")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**Your active lock%s** (%d)\n", plural(len(views)), len(views))
	for idx := range views {
		b.WriteString("\n")
		b.WriteString(cs.LockDetailText(&views[idx]))
		b.WriteString("\n")
	}
	return sendEphemeral(s, i, b.String())
}

func (f *Feature) handleLockDraw(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}

	lock := activeCardLock(locks, cred.ChastiSafeID)
	if lock == nil {
		return sendEphemeral(s, i, "You have no active card lock to draw from.")
	}
	if lock.DrawsAvailable() == 0 {
		if wait := lock.NextPickIn(); wait > 0 {
			return sendEphemeral(s, i, fmt.Sprintf(
				"No draws available yet for **%s**. Next draw in %s.",
				lockName(lock), cs.FormatShortDuration(wait),
			))
		}
		return sendEphemeral(s, i, fmt.Sprintf("No draws available right now for **%s**.", lockName(lock)))
	}

	result, err := client.Pick(ctx, lock.ID)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	return sendEphemeral(s, i, drawText(lock, result))
}

func (f *Feature) handleLockNotify(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, enabled bool) error {
	userID := bot.UserID(i)
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	if !enabled {
		if err := f.store.Unsubscribe(ctx, userID); err != nil {
			f.log.Warn("unsubscribing", "user_id", userID, "err", err)
			return sendEphemeral(s, i, "I could not update your notifications. Try again in a moment.")
		}
		return sendEphemeral(s, i, "Draw notifications are off.")
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}

	lock := activeCardLock(locks, cred.ChastiSafeID)
	if lock == nil {
		_ = f.store.Unsubscribe(ctx, userID)
		return sendEphemeral(s, i, "You have no active card lock to watch, so notifications are off.")
	}

	next := f.now().Add(10 * time.Second)
	if lock.DrawsAvailable() == 0 {
		if wait := lock.NextPickIn(); wait > 0 {
			next = f.now().Add(wait + 2*time.Second)
		} else {
			next = f.now().Add(time.Minute)
		}
	}
	if err := f.store.Subscribe(ctx, &store.Notification{
		DiscordUserID: userID,
		LockID:        lock.ID.String(),
		LastDraws:     string(lock.Draws),
		LastPicks:     0, // notify on the first check when draws are already ready
		NextCheckAt:   next,
	}); err != nil {
		f.log.Warn("subscribing", "user_id", userID, "err", err)
		return sendEphemeral(s, i, "I could not save your subscription. Try again in a moment.")
	}

	name := lockName(lock)
	f.log.Info("notify subscribed", "user_id", userID, "lock_id", lock.ID.String())
	if draws := lock.DrawsAvailable(); draws > 0 {
		return sendEphemeral(s, i, fmt.Sprintf(
			"Draw notifications are on for **%s**. You have %s available now.",
			name, drawsPhrase(draws),
		))
	}
	if wait := lock.NextPickIn(); wait > 0 {
		return sendEphemeral(s, i, fmt.Sprintf(
			"Draw notifications are on for **%s**. Next draw opens in %s.",
			name, cs.FormatShortDuration(wait),
		))
	}
	return sendEphemeral(s, i, fmt.Sprintf(
		"Draw notifications are on for **%s**. I will ping you when I see a fresh draw.",
		name,
	))
}

// activeCardLock returns the caller's newest active, unfrozen card lock,
// preferring one that has a draw ready.
func activeCardLock(locks []cs.LoadedLock, chastisafeID string) *cs.LoadedLock {
	var newest, withDraws *cs.LoadedLock
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsCardLock() || lock.IsFrozen() || lock.IsReady() {
			continue
		}
		if chastisafeID != "" && lock.LockeeID.String() != chastisafeID {
			continue
		}
		if newest == nil || lock.LoadTime.Time.After(newest.LoadTime.Time) {
			newest = lock
		}
		if lock.DrawsAvailable() > 0 {
			if withDraws == nil || lock.LoadTime.Time.After(withDraws.LoadTime.Time) {
				withDraws = lock
			}
		}
	}
	if withDraws != nil {
		return withDraws
	}
	return newest
}

func drawText(lock *cs.LoadedLock, result *cs.PickResult) string {
	name := "your lock"
	if lock != nil {
		if trimmed := strings.TrimSpace(lock.LockName); trimmed != "" {
			name = trimmed
		}
	}

	var b strings.Builder
	switch {
	case result != nil && result.Card != "":
		fmt.Fprintf(&b, "Drew **%s** from **%s**.", result.Card.Label(), name)
	case result != nil && result.Dice > 0:
		fmt.Fprintf(&b, "Rolled **%d** on **%s**.", result.Dice, name)
	default:
		fmt.Fprintf(&b, "Drew from **%s**.", name)
	}

	updated := lock
	if result != nil && result.Lock != nil {
		updated = result.Lock
	}
	if updated != nil {
		b.WriteString("\n")
		b.WriteString(cs.LockStateText(updated))
		fmt.Fprintf(&b, "\n%s available.", drawsPhrase(updated.DrawsAvailable()))
		if wait := updated.NextPickIn(); wait > 0 {
			fmt.Fprintf(&b, " Next draw in %s.", cs.FormatShortDuration(wait))
		}
	}
	if result != nil && result.Done {
		b.WriteString("\nThat was the last draw. The lock is ready to unlock.")
	}
	return b.String()
}

func modalValue(components []discordgo.MessageComponent, customID string) string {
	for _, component := range components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, inner := range row.Components {
			input, ok := inner.(*discordgo.TextInput)
			if ok && input.CustomID == customID {
				return input.Value
			}
		}
	}
	return ""
}

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
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "public",
						Description: "Post the reply publicly instead of privately",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "load",
				Description: "Load a lock from a ChastiSafe share link",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "link",
						Description: "ChastiSafe share link or template ID",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "draw",
				Description: "Draw the next card from your card lock",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "open",
				Description: "Open a ready lock and reveal its device code",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "ref",
						Description:  "Pick a lock that is ready to open",
						Required:     false,
						Autocomplete: true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "checkin",
				Description: "Record a required check-in",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "ref",
						Description:  "Pick a lock that needs a check-in",
						Required:     false,
						Autocomplete: true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "public",
						Description: "Post the reply publicly instead of privately",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "share",
				Description: "Share a lock template so someone can load it",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "ref",
						Description:  "Pick one of your lock templates",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "notify",
				Description: "Get pinged for locks you loaded in the ChastiSafe app",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "enabled",
						Description: "Turn notifications on or off (default on)",
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
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "ref",
						Description:  "Pick an active or previous lock",
						Required:     false,
						Autocomplete: true,
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
	if !f.configured() {
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
		f.track("login failed: rate limited")
		return bot.ReplyEphemeral(s, i, "Too many login attempts. Wait a few minutes and try again.")
	}

	code := modalValue(i.ModalSubmitData().Components, "code")
	if strings.TrimSpace(code) == "" {
		f.track("login failed: empty code")
		return bot.ReplyEphemeral(s, i, "The developer code was empty. Run `/login` again.")
	}

	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	identity, err := f.authenticate(ctx, userID, code)
	if err != nil {
		reason := loginFailureReason(err)
		f.track("login failed: " + reason)
		f.log.Info("login failed", "user_id", userID, "err", err)
		switch reason {
		case "not owner":
			return sendEphemeral(s, i,
				"That code belongs to a different ChastiSafe account.\n"+
					"Link your Discord account in ChastiSafe, then run `/login` again.")
		case "rejected":
			return sendEphemeral(s, i, "ChastiSafe rejected that developer code. Check it and try again.")
		case "unconfigured":
			return sendEphemeral(s, i, "Locksmith is not configured to store codes.")
		default:
			f.log.Warn("login request failed", "user_id", userID, "err", err)
			return sendEphemeral(s, i, "I could not reach ChastiSafe right now. Try again in a moment.")
		}
	}

	f.loginSucceeded(userID)
	f.forgetAutocomplete(userID)
	f.track("login success")
	f.log.Info("login stored",
		"user_id", userID,
		"chastisafe_user", identity.User,
		"chastisafe_id", identity.ID.String(),
	)
	return sendEphemeral(s, i, loginWelcomeText(identity))
}

// loginWelcomeText is the private onboarding reply after a successful login.
func loginWelcomeText(identity *cs.Identity) string {
	name := "your ChastiSafe account"
	id := ""
	if identity != nil {
		if trimmed := strings.TrimSpace(identity.User); trimmed != "" {
			name = "**" + trimmed + "**"
		}
		id = identity.ID.String()
	}

	var b strings.Builder
	if id != "" {
		fmt.Fprintf(&b, "Logged in as %s (ID `%s`).\n\n", name, id)
	} else {
		fmt.Fprintf(&b, "Logged in as %s.\n\n", name)
	}
	b.WriteString(guideText())
	return b.String()
}

// loginFailureReason buckets a failed login for anonymous analytics. The
// labels are coarse on purpose: no codes, IDs, or usernames.
func loginFailureReason(err error) string {
	switch {
	case errors.Is(err, errNotOwner):
		return "not owner"
	case errors.Is(err, cs.ErrUnauthorized):
		return "rejected"
	case errors.Is(err, errNotConfigured):
		return "unconfigured"
	default:
		return "unreachable"
	}
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
	f.forgetAutocomplete(userID)
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
		return f.handleLockStatus(ctx, s, i, sub.Options)
	case "load":
		link, _ := stringOption(sub.Options, "link")
		return f.handleLockLoad(ctx, s, i, link)
	case "draw":
		return f.handleLockDraw(ctx, s, i)
	case "open":
		return f.handleLockOpen(ctx, s, i, sub.Options)
	case "checkin":
		return f.handleLockCheckin(ctx, s, i, sub.Options)
	case "share":
		return f.handleLockShare(ctx, s, i, sub.Options)
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

func (f *Feature) handleLockStatus(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.UserID(i)
	public := boolOption(opts, "public")
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

	views := ownLockViews(locks, cred.ChastiSafeID)
	if len(views) == 0 {
		return bot.EditChunks(s, i, "You have no active ChastiSafe locks.", !public)
	}

	own := ownLocks(locks, cred.ChastiSafeID)
	freeze := f.freezeTimes(ctx, client, own)
	embeds := renderStatusEmbeds(views, cred.ChastiSafeID, freeze, f.now())
	return bot.EditEmbeds(s, i, embeds, !public)
}

func (f *Feature) handleLockDraw(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return f.startDrawBoard(ctx, s, i)
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
		return sendEphemeral(s, i, "Notifications are off.")
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}

	now := f.now()
	lock := drawableLock(locks, cred.ChastiSafeID)
	checkinOnly := false
	if lock == nil {
		lock = nextCheckinLock(locks, cred.ChastiSafeID, now)
		checkinOnly = lock != nil
	}
	if lock == nil {
		_ = f.store.Unsubscribe(ctx, userID)
		return sendEphemeral(s, i, "You have no active card lock or check-in to watch, so notifications are off.")
	}

	if err := f.watchLock(ctx, userID, lock); err != nil {
		f.log.Warn("subscribing", "user_id", userID, "err", err)
		return sendEphemeral(s, i, "I could not save your subscription. Try again in a moment.")
	}

	name := lockName(lock)
	f.log.Info("notify subscribed", "user_id", userID, "lock_id", lock.ID.String(), "checkin_only", checkinOnly)
	if !lock.IsCardLock() {
		if remaining := checkinIn(lock, now); remaining > 0 {
			return sendEphemeral(s, i, fmt.Sprintf(
				"Check-in notifications are on for **%s**. Next check-in %s.",
				name, relativeTimestamp(now.Add(remaining)),
			))
		}
		return sendEphemeral(s, i, fmt.Sprintf(
			"Check-in notifications are on for **%s**. I will ping you when it is due.", name,
		))
	}
	if draws := lock.DrawsAvailable(); draws > 0 {
		return sendEphemeral(s, i, fmt.Sprintf(
			"Notifications are on for **%s**. You have %s available now.",
			name, drawsPhrase(draws),
		))
	}
	if wait := lock.NextPickIn(); wait > 0 {
		return sendEphemeral(s, i, fmt.Sprintf(
			"Notifications are on for **%s**. Next draw opens %s.",
			name, relativeTimestamp(now.Add(wait)),
		))
	}
	return sendEphemeral(s, i, fmt.Sprintf(
		"Notifications are on for **%s**. I will ping you when I see a fresh draw or a check-in is due.",
		name,
	))
}

// watchLock subscribes the member to draw and check-in reminders for one lock.
// A member watches one lock at a time, so a new subscription replaces the old
// one. The first check reports draws that are already available.
func (f *Feature) watchLock(ctx context.Context, userID string, lock *cs.LoadedLock) error {
	if f.store == nil || lock == nil {
		return errNotConfigured
	}
	now := f.now()
	next := now.Add(10 * time.Second)
	if lock.IsCardLock() && lock.DrawsAvailable() == 0 {
		if wait := lock.NextPickIn(); wait > 0 {
			next = now.Add(wait + 2*time.Second)
		} else {
			next = now.Add(time.Minute)
		}
	}
	return f.store.Watch(ctx, store.Watch{
		DiscordUserID: userID,
		LockID:        lock.ID.String(),
		LastDraws:     string(lock.Draws),
		NextCheckAt:   next,
	})
}

// activeCheckinLock and activeCardLock used to live here. Lock selection now
// has one home in selection.go.
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

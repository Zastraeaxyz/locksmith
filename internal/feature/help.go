package feature

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"locksmith/internal/bot"
)

const (
	repoURL = "https://github.com/Zastraeaxyz/locksmith"
	// defaultClientID is the production application, used when the session has
	// not reported its own user yet.
	defaultClientID = "1574588415783997481"
	embedColor      = 0x5865F2
)

func helpCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "help",
		Description: "How to use Locksmith and how to install it",
	}
}

// handleHelpCommand posts the guide with the install buttons.
func (f *Feature) handleHelpCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	clientID := sessionClientID(s)
	return bot.ReplyEmbed(s, i,
		[]*discordgo.MessageEmbed{helpEmbed(clientID)},
		helpButtons(clientID),
		false,
	)
}

// sessionClientID reports the application ID when the session has learned it.
// Widgets that build links fall back to defaultClientID when it is empty.
func sessionClientID(s *discordgo.Session) string {
	if s != nil && s.State != nil && s.State.User != nil {
		return s.State.User.ID
	}
	return ""
}

// userInstallURL is Discord's User Install link, the recommended way to add
// Locksmith to an account.
func userInstallURL(clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = defaultClientID
	}
	return fmt.Sprintf(
		"https://discord.com/oauth2/authorize?client_id=%s&integration_type=1&scope=applications.commands",
		clientID,
	)
}

func helpEmbed(clientID string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "Locksmith",
		URL:         repoURL,
		Color:       embedColor,
		Description: helpDescription(),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Work in progress: not every ChastiSafe feature is covered yet.",
		},
	}
}

func helpDescription() string {
	var b strings.Builder
	b.WriteString("Your ChastiSafe locks, inside Discord.\n\n")
	b.WriteString(guideText())
	b.WriteString("\n\n")
	b.WriteString("Create and edit lock templates on chastisafe.com, then load, draw, check in, ")
	b.WriteString("and watch your locks from Discord. Install Locksmith to your Discord account ")
	b.WriteString("to use the same commands in DMs, group DMs, and servers that do not have the bot. ")
	b.WriteString("The repository has the full docs and further details.")
	return b.String()
}

// guideText is the command list and walkthrough shared by /help and the private
// reply after a successful login.
func guideText() string {
	var b strings.Builder
	b.WriteString("**Commands**\n")
	b.WriteString("• `/lock status` shows your active locks, draws left, timers, and next check-in.\n")
	b.WriteString("• `/lock load link:<share link>` starts a shared lock. I generate the device code, so you cannot pick one you would remember. Set your device to the code I reply with, then forget it.\n")
	b.WriteString("• `/lock draw` opens the board. Press **Draw** to pick a card; when the lock is ready, it offers **Open**, **Put green back**, **Reset**, and **Let keyholder decide**.\n")
	b.WriteString("• `/lock open [ref]` opens a ready lock and shows its device code. The reply is private in servers and normal in DMs.\n")
	b.WriteString("• `/lock rate` rates your latest completed lock.\n")
	b.WriteString("• `/lock checkin [ref]` records a check-in when one is due.\n")
	b.WriteString("• `/lock log [full] [ref]` and `/lock deck [ref]` show draws and the cards left.\n")
	b.WriteString("• `/lock share ref:<template>` lets keyholders post a lock with load buttons.\n")
	b.WriteString("• `/lock notify [enabled]` turns DM reminders on or off for locks loaded in the app. `/lock load` locks are watched automatically.\n")
	b.WriteString("• `/history` lists your completed locks. A private reply includes their codes.\n")
	b.WriteString("• `/profile`, `/rules`, and `/kinks` view or edit your markdown documents.\n")
	b.WriteString("• `/docs` explains every command, card, and button in detail.\n")
	b.WriteString("• `/help` posts this guide and install links; `/changelog` shows changes.\n")
	b.WriteString("• `/logout` removes your saved code.\n\n")

	b.WriteString("**Example: load, draw, check in**\n")
	b.WriteString("1. `/lock load link:https://chastisafe.com/#l/1234`, then set your device to the code I reply with and forget it.\n")
	b.WriteString("2. `/lock status` shows the lock and how many draws are ready.\n")
	b.WriteString("3. `/lock draw` opens the board. Press **Draw** until the lock is ready to open.\n")
	b.WriteString("4. Press **Open** or run `/lock open` to see the code again. Private `/history` keeps it too.\n")
	b.WriteString("5. When a check-in is due, press **Check-in** on the reminder DM, or run `/lock checkin`.\n\n")

	b.WriteString("**Keyholders**\n")
	b.WriteString("Create the template on chastisafe.com, then run `/lock share ref:My Lock`. ")
	b.WriteString("The post has **Load with Locksmith** for bot users and **Open in ChastiSafe** for everyone else.")
	return b.String()
}

func helpButtons(clientID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label: "Install to your account",
				Style: discordgo.LinkButton,
				URL:   userInstallURL(clientID),
			},
			discordgo.Button{
				Label: "Repository",
				Style: discordgo.LinkButton,
				URL:   repoURL,
			},
		}},
	}
}

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
	aboutColor      = 0x5865F2
)

func locksmithCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "locksmith",
		Description: "About Locksmith and how to install it",
	}
}

// handleLocksmithCommand posts the short install-and-features embed.
func (f *Feature) handleLocksmithCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	clientID := ""
	if s != nil && s.State != nil && s.State.User != nil {
		clientID = s.State.User.ID
	}
	return bot.ReplyEmbed(s, i,
		[]*discordgo.MessageEmbed{locksmithEmbed(clientID)},
		locksmithButtons(clientID),
		false,
	)
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

func locksmithEmbed(clientID string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "Locksmith",
		URL:         repoURL,
		Color:       aboutColor,
		Description: locksmithDescription(),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Work in progress: not every ChastiSafe feature is covered yet.",
		},
	}
}

func locksmithDescription() string {
	var b strings.Builder
	b.WriteString("Your ChastiSafe locks, inside Discord.\n\n")
	b.WriteString("• `/lock status` shows your active locks, timers, and check-ins\n")
	b.WriteString("• `/lock draw` opens a public draw board with Draw and Stop\n")
	b.WriteString("• `/lock notify` DMs you when a draw is ready\n")
	b.WriteString("• `/lock log` shows recent draws or the full history\n")
	b.WriteString("• `/history` lists your completed locks\n")
	b.WriteString("• `/profile view`, `/rules view`, and `/kinks view` share your markdown documents\n\n")
	b.WriteString("You no longer need to open chastisafe.com. Install Locksmith to your Discord account, ")
	b.WriteString("then run the commands in DMs, group DMs, and servers that do not have the bot. ")
	b.WriteString("The repository has the full docs and further details.")
	return b.String()
}

func locksmithButtons(clientID string) []discordgo.MessageComponent {
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

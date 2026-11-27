package feature

// The about module renders /about, the short introduction for someone who has
// not installed Locksmith or has not used it yet. The full pages live in
// docs.go; this page points at them instead of repeating them.

import (
	"context"

	"github.com/bwmarrin/discordgo"

	"locksmith/internal/bot"
)

const aboutNamespace = "about"

func aboutCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "about",
		Description: "What Locksmith is and how to start",
	}
}

// handleAboutCommand posts the introduction publicly, so a curious channel can
// read it without running the command themselves.
func (f *Feature) handleAboutCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return bot.ReplyEmbed(s, i,
		[]*discordgo.MessageEmbed{aboutEmbed()},
		aboutButtons(sessionClientID(s)),
		false,
	)
}

// handleAboutComponent opens the docs index privately when someone presses
// Browse docs, so the presser can read further without filling the channel.
func (f *Feature) handleAboutComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if i.MessageComponentData().CustomID != aboutCustomID {
		return bot.ReplyEphemeral(s, i, "This button is out of date. Run `/about` again.")
	}
	return bot.ReplyEmbed(s, i,
		[]*discordgo.MessageEmbed{docsIndexEmbed()},
		docsComponents(docsIndexKey, sessionClientID(s)),
		true,
	)
}

// aboutCustomID is the Browse docs button.
const aboutCustomID = aboutNamespace + ":" + docsIndexKey

func aboutEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "Locksmith",
		URL:         repoURL,
		Color:       embedColor,
		Description: aboutDescription(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "Wearing the lock",
				Value: "Run `/login` once to connect your account. `/lock load` starts a lock your " +
					"keyholder shared, `/lock draw` runs the board, and `/lock status` shows where " +
					"you stand. Locksmith generates the device code, so you cannot pick one you " +
					"would remember.",
			},
			{
				Name: "Holding the keys",
				Value: "Share a template with `/lock share`, watch draws as they happen, and decide " +
					"when a lock may open. The keyholder side is still growing.",
			},
			{
				Name: "Your code stays yours",
				Value: "Each member logs in with their own ChastiSafe developer code. It is stored " +
					"encrypted, never shared with another member, and `/logout` removes it.",
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Run /login to start, or press Browse docs for the full guide.",
		},
	}
}

func aboutDescription() string {
	return "Your ChastiSafe locks, inside Discord.\n\n" +
		"Load a lock your keyholder shared, draw from a public board, check in when " +
		"reminded, and open it when it is ready, without switching to a browser."
}

// aboutButtons keeps the links beside the introduction, plus a private path
// into the documentation.
func aboutButtons(clientID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label: "Install to your account",
				Style: discordgo.LinkButton,
				URL:   userInstallURL(clientID),
			},
			discordgo.Button{
				Label:    "Browse docs",
				Style:    discordgo.SecondaryButton,
				CustomID: aboutCustomID,
			},
			discordgo.Button{
				Label: "ChastiSafe",
				Style: discordgo.LinkButton,
				URL:   chastisafeURL,
			},
			discordgo.Button{
				Label: "Repository",
				Style: discordgo.LinkButton,
				URL:   repoURL,
			},
		}},
	}
}

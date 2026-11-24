package feature

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"

	"locksmith/internal/bot"
)

// changelogDescriptionLimit leaves room under Discord's 4096 character embed
// description limit for the truncation hint.
const changelogDescriptionLimit = 4000

func changelogCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "changelog",
		Description: "Show recent Locksmith changes",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "public",
				Description: "Post the reply publicly instead of privately",
				Required:    false,
			},
		},
	}
}

// handleChangelogCommand posts the curated changelog. The content is embedded
// in the binary, so the reply needs no API calls.
func (f *Feature) handleChangelogCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	public := boolOption(i.ApplicationCommandData().Options, "public")
	description := renderChangelog(f.changelog, changelogDescriptionLimit)
	if strings.TrimSpace(description) == "" {
		return bot.Reply(s, i, "The changelog is empty.", !public)
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Locksmith changelog",
		URL:         repoURL + "/blob/main/CHANGELOG.md",
		Color:       embedColor,
		Description: description,
	}
	return bot.ReplyEmbed(s, i, []*discordgo.MessageEmbed{embed}, nil, !public)
}

// renderChangelog turns the curated Markdown into a compact Discord block. The
// document title and intro are dropped, date headings become bold lines,
// category headings become italic lines, and list markers become bullets.
func renderChangelog(markdown string, limit int) string {
	if limit <= 0 {
		limit = changelogDescriptionLimit
	}

	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	started := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			if started && len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
		case strings.HasPrefix(trimmed, "# "):
			continue
		case strings.HasPrefix(trimmed, "## "):
			started = true
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, "**"+strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))+"**")
		case strings.HasPrefix(trimmed, "### "):
			if !started {
				continue
			}
			out = append(out, "*"+strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))+"*")
		case !started:
			continue
		case strings.HasPrefix(trimmed, "- "):
			out = append(out, "• "+strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		default:
			out = append(out, trimmed)
		}
	}

	text := strings.TrimSpace(strings.Join(out, "\n"))
	if text == "" || len([]rune(text)) <= limit {
		return text
	}
	return truncateChangelog(text, limit)
}

// truncateChangelog cuts on a line boundary and points at the repository copy.
func truncateChangelog(text string, limit int) string {
	const hint = "\n\nOlder changes are in the repository changelog."

	runes := []rune(text)
	room := limit - len([]rune(hint))
	if room < 1 {
		return strings.TrimSpace(hint)
	}

	cut := string(runes[:room])
	if index := strings.LastIndexByte(cut, '\n'); index > 0 {
		cut = cut[:index]
	}
	cut = strings.TrimSpace(cut)
	if cut == "" {
		return strings.TrimSpace(hint)
	}
	return cut + hint
}

package feature

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"locksmith/internal/bot"
	"locksmith/internal/store"
)

const (
	documentTextInput = "text"
	documentMaxLength = 4000
)

func profileCommand() *discordgo.ApplicationCommand {
	return documentCommand(
		"profile",
		"View or edit your shared profile",
		"Share a profile",
		"Set your profile markdown",
	)
}

func rulesCommand() *discordgo.ApplicationCommand {
	return documentCommand(
		"rules",
		"View or edit your rules of play",
		"Share rules of play",
		"Set your rules markdown",
	)
}

func kinksCommand() *discordgo.ApplicationCommand {
	return documentCommand(
		"kinks",
		"View or edit your kinks",
		"Share a kinks list",
		"Set your kinks markdown",
	)
}

func documentCommand(name, description, viewDescription, editDescription string) *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        name,
		Description: description,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "view",
				Description: viewDescription,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "user",
						Description: "User ID or username, for example zastraea or zastraea#0",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "edit",
				Description: editDescription,
			},
		},
	}
}

func (f *Feature) handleProfileCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return f.handleDocumentCommand(ctx, s, i, store.DocumentProfile)
}

func (f *Feature) handleRulesCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return f.handleDocumentCommand(ctx, s, i, store.DocumentRules)
}

func (f *Feature) handleKinksCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return f.handleDocumentCommand(ctx, s, i, store.DocumentKinks)
}

func (f *Feature) handleDocumentCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, kind string) error {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return bot.ReplyEphemeral(s, i, "Choose `view` or `edit`.")
	}

	sub := data.Options[0]
	switch sub.Name {
	case "view":
		return f.handleDocumentView(ctx, s, i, kind, sub.Options)
	case "edit":
		return f.handleDocumentEdit(ctx, s, i, kind)
	default:
		return bot.ReplyEphemeral(s, i, "Unknown subcommand.")
	}
}

func (f *Feature) handleDocumentView(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, kind string, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	callerID := bot.UserID(i)
	if callerID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	targetID := callerID
	if arg, ok := stringOption(opts, "user"); ok && strings.TrimSpace(arg) != "" {
		id, found, err := f.resolveDocumentUser(ctx, arg)
		if err != nil {
			f.log.Warn("resolving document user", "err", err, "input", arg)
			return bot.ReplyEphemeral(s, i, "I could not look that up right now.")
		}
		if !found {
			return bot.ReplyEphemeral(s, i, fmt.Sprintf("No %s found for `%s`.", kindLabel(kind), arg))
		}
		targetID = id
	}

	doc, err := f.store.Document(ctx, targetID, kind)
	if err != nil {
		f.log.Warn("loading document", "err", err, "user_id", targetID, "kind", kind)
		return bot.ReplyEphemeral(s, i, "I could not load that document right now.")
	}
	if doc == nil || strings.TrimSpace(doc.Content) == "" {
		if targetID == callerID {
			return bot.ReplyEphemeral(s, i, emptySelfText(kind))
		}
		return bot.ReplyEphemeral(s, i, emptyOtherText(kind, targetID))
	}

	if targetID == callerID {
		_ = f.store.UpdateHandle(ctx, callerID, normalizeHandle(bot.Username(i)))
	}

	name := strings.TrimSpace(doc.Handle)
	if targetID == callerID {
		if current := strings.TrimSpace(bot.Username(i)); current != "" {
			name = current
		}
	}
	if name == "" {
		name = targetID
	}

	embed := documentEmbed(kind, name, doc.Content)
	return bot.ReplyEmbedAllowed(s, i, []*discordgo.MessageEmbed{embed}, nil, false, noMentions())
}

func documentEmbed(kind, name, content string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("%s's %s", name, kindLabel(kind)),
		Description: clip(content, documentMaxLength),
		Color:       documentColor(kind),
	}
}

func (f *Feature) handleDocumentEdit(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, kind string) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	current := ""
	doc, err := f.store.Document(ctx, userID, kind)
	if err != nil {
		f.log.Warn("loading document for edit", "err", err, "user_id", userID, "kind", kind)
		return bot.ReplyEphemeral(s, i, "I could not load that document right now.")
	}
	if doc != nil {
		current = doc.Content
	}

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: documentModal(kind, current),
	})
}

func (f *Feature) handleProfileModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return f.handleDocumentModal(ctx, s, i, store.DocumentProfile)
}

func (f *Feature) handleRulesModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return f.handleDocumentModal(ctx, s, i, store.DocumentRules)
}

func (f *Feature) handleKinksModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return f.handleDocumentModal(ctx, s, i, store.DocumentKinks)
}

func (f *Feature) handleDocumentModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, kind string) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	value := strings.TrimSpace(modalValue(i.ModalSubmitData().Components, documentTextInput))
	if value == "" {
		if err := f.store.DeleteDocument(ctx, userID, kind); err != nil {
			f.log.Warn("clearing document", "err", err, "user_id", userID, "kind", kind)
			return bot.ReplyEphemeral(s, i, "I could not clear that document right now.")
		}
		return bot.ReplyEphemeral(s, i, clearedText(kind))
	}

	if err := f.store.SaveDocument(ctx, &store.UserDocument{
		DiscordUserID: userID,
		Kind:          kind,
		Handle:        normalizeHandle(bot.Username(i)),
		Content:       value,
	}); err != nil {
		f.log.Warn("saving document", "err", err, "user_id", userID, "kind", kind)
		return bot.ReplyEphemeral(s, i, "I could not save that document right now.")
	}
	return bot.ReplyEphemeral(s, i, savedText(kind))
}

func documentModal(kind, current string) *discordgo.InteractionResponseData {
	current = clip(current, documentMaxLength)
	return &discordgo.InteractionResponseData{
		Title:    modalTitle(kind),
		CustomID: kind + ":edit",
		Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID:    documentTextInput,
						Label:       textInputLabel(kind),
						Style:       discordgo.TextInputParagraph,
						Value:       current,
						Placeholder: "Markdown you want to share",
						Required:    false,
						MaxLength:   documentMaxLength,
					},
				},
			},
		},
	}
}

func (f *Feature) resolveDocumentUser(ctx context.Context, raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	if isSnowflake(raw) {
		return raw, true, nil
	}

	name := normalizeHandle(raw)
	if name == "" {
		return "", false, nil
	}
	if id, ok, err := f.store.UserByChastiSafeName(ctx, name); err != nil {
		return "", false, err
	} else if ok {
		return id, true, nil
	}
	return f.store.UserByHandle(ctx, name)
}

func normalizeHandle(raw string) string {
	raw = strings.TrimSpace(raw)
	if index := strings.IndexByte(raw, '#'); index >= 0 {
		raw = raw[:index]
	}
	return strings.ToLower(raw)
}

func isSnowflake(value string) bool {
	if len(value) < 15 || len(value) > 21 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func noMentions() *discordgo.MessageAllowedMentions {
	return &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}
}

func documentColor(kind string) int {
	switch kind {
	case store.DocumentRules:
		return 0x9B59B6
	case store.DocumentKinks:
		return 0xEB459E
	default:
		return 0x5865F2
	}
}

func kindLabel(kind string) string {
	switch kind {
	case store.DocumentRules:
		return "rules of play"
	case store.DocumentKinks:
		return "kinks"
	default:
		return "profile"
	}
}

func modalTitle(kind string) string {
	switch kind {
	case store.DocumentRules:
		return "Edit rules of play"
	case store.DocumentKinks:
		return "Edit kinks"
	default:
		return "Edit profile"
	}
}

func textInputLabel(kind string) string {
	switch kind {
	case store.DocumentRules:
		return "Rules of play (markdown)"
	case store.DocumentKinks:
		return "Kinks (markdown)"
	default:
		return "Profile (markdown)"
	}
}

func emptySelfText(kind string) string {
	switch kind {
	case store.DocumentRules:
		return "You have not set rules of play yet. Use `/rules edit`."
	case store.DocumentKinks:
		return "You have not set your kinks yet. Use `/kinks edit`."
	default:
		return "You have not set a profile yet. Use `/profile edit`."
	}
}

func emptyOtherText(kind, userID string) string {
	return fmt.Sprintf("No %s set for `%s`.", kindLabel(kind), userID)
}

func savedText(kind string) string {
	switch kind {
	case store.DocumentRules:
		return "Your rules of play have been saved."
	case store.DocumentKinks:
		return "Your kinks have been saved."
	default:
		return "Your profile has been saved."
	}
}

func clearedText(kind string) string {
	switch kind {
	case store.DocumentRules:
		return "Your rules of play have been cleared."
	case store.DocumentKinks:
		return "Your kinks have been cleared."
	default:
		return "Your profile has been cleared."
	}
}

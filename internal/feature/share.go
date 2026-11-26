package feature

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const shareNamespace = "share"

// handleLockShare posts a lock template with a load button and a website link.
func (f *Feature) handleLockShare(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	ref, _ := stringOption(opts, "ref")
	if err := bot.Defer(s, i, false); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		f.track("share failed: not logged in")
		return bot.Edit(s, i, f.apiFailure(ctx, userID, err))
	}
	templates, err := client.Templates(ctx)
	if err != nil {
		f.track("share failed: templates")
		return bot.Edit(s, i, f.apiFailure(ctx, userID, err))
	}
	if len(templates) == 0 {
		f.track("share failed: no templates")
		return bot.Edit(s, i, "You have no lock templates to share. Create one on chastisafe.com, then run `/lock share` again.")
	}

	template, err := resolveShareTemplate(ref, templates)
	if err != nil {
		f.track("share failed: reference")
		return bot.Edit(s, i, err.Error())
	}
	if err := shareableTemplate(template, cred.ChastiSafeID); err != nil {
		f.track("share failed: not shareable")
		return bot.Edit(s, i, err.Error())
	}

	f.track("share success")
	f.log.Info("lock shared", "user_id", userID, "template_id", template.ID.String())
	return bot.EditMessageAllowed(s, i, "",
		[]*discordgo.MessageEmbed{shareEmbed(template, f.baseURL, interactionName(i))},
		shareButtons(template, f.baseURL),
		noMentions(),
	)
}

// resolveShareTemplate finds one template by ID, exact name, or unique prefix.
func resolveShareTemplate(ref string, templates []cs.Template) (*cs.Template, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("Choose a lock template with `ref`.")
	}

	for idx := range templates {
		if templates[idx].ID.String() == ref {
			return &templates[idx], nil
		}
	}

	lower := strings.ToLower(ref)
	var exact, prefix []*cs.Template
	for idx := range templates {
		template := &templates[idx]
		name := strings.ToLower(strings.TrimSpace(template.LockName))
		id := strings.ToLower(template.ID.String())
		switch {
		case name == lower || id == lower:
			exact = append(exact, template)
		case strings.HasPrefix(name, lower) || strings.HasPrefix(id, lower):
			prefix = append(prefix, template)
		}
	}

	matches := exact
	if len(matches) == 0 {
		matches = prefix
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("No lock template matches `%s`. Your templates: %s", ref, templateNameList(templates))
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, template := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", templateName(template), template.ID))
		}
		if len(names) > 10 {
			names = append(names[:10], fmt.Sprintf("and %d more", len(names)-10))
		}
		return nil, fmt.Errorf("`%s` matches several templates: %s", ref, strings.Join(names, ", "))
	}
}

func templateNameList(templates []cs.Template) string {
	names := make([]string, 0, len(templates))
	for idx := range templates {
		names = append(names, templateName(&templates[idx]))
	}
	if len(names) > 10 {
		names = append(names[:10], fmt.Sprintf("and %d more", len(names)-10))
	}
	return strings.Join(names, ", ")
}

// shareableTemplate refuses templates the caller cannot offer to a lockee.
func shareableTemplate(template *cs.Template, chastisafeID string) error {
	if template == nil {
		return errors.New("Lock template not found.")
	}
	if templateDisabled(template) {
		return fmt.Errorf("**%s** is disabled. Enable it on chastisafe.com before sharing it.", templateName(template))
	}
	owner := template.KeyholderID.String()
	if owner != "" && owner != "0" && chastisafeID != "" && owner != chastisafeID {
		return fmt.Errorf("**%s** belongs to another keyholder, so you cannot share it.", templateName(template))
	}
	return nil
}

// templateDisabled reports whether a keyholder turned the template off.
func templateDisabled(template *cs.Template) bool {
	return template != nil && strings.EqualFold(strings.TrimSpace(template.Status), "disabled")
}

func templateName(template *cs.Template) string {
	if template == nil {
		return "lock"
	}
	if name := strings.TrimSpace(template.LockName); name != "" {
		return name
	}
	return "Untitled lock"
}

// templateDetails lists what a lockee should know before loading, without
// exposing private settings or the passcode value itself.
func templateDetails(template *cs.Template, sharer string) string {
	var b strings.Builder
	if keyholder := strings.TrimSpace(template.Keyholder); keyholder != "" && !strings.EqualFold(keyholder, "<self>") {
		fmt.Fprintf(&b, "Keyholder: **%s**\n", keyholder)
	} else if sharer != "" {
		fmt.Fprintf(&b, "Shared by **%s**\n", sharer)
	}
	fmt.Fprintf(&b, "Type: %s\n", cs.LockTypeLabel(template.LockType, template.IsCardLock(), template.IsTimerLock()))
	if template.IsCardLock() {
		if cards := templateCardsText(template.Cards); cards != "" {
			fmt.Fprintf(&b, "Cards: %s\n", cards)
		}
		if template.RequiredGreens > 0 {
			fmt.Fprintf(&b, "Greens needed: %s\n", greensText(template.RequiredGreens))
		}
		if template.Regularity > 0 {
			fmt.Fprintf(&b, "Draws: every %s\n", cs.FormatMinutes(template.Regularity))
		}
	}
	if template.IsTimerLock() && (template.TimerMin > 0 || template.TimerMax > 0) {
		fmt.Fprintf(&b, "Timer: %s to %s\n",
			cs.FormatDuration(time.Duration(template.TimerMin)*time.Second),
			cs.FormatDuration(time.Duration(template.TimerMax)*time.Second),
		)
	}
	if template.CheckInPeriod > 0 {
		fmt.Fprintf(&b, "Check-ins: every %s\n", cs.FormatMinutes(template.CheckInPeriod))
	}
	if purposes := strings.TrimSpace(cs.FormatPurposes(template.AllowedPurposes)); purposes != "" {
		fmt.Fprintf(&b, "Purposes: %s\n", purposes)
	}
	if strings.TrimSpace(template.Passcode) != "" {
		b.WriteString("Passcode: required, ask your keyholder for it\n")
	}
	if template.ContactKeyholder {
		b.WriteString("Contact your keyholder before loading\n")
	}
	if description := strings.TrimSpace(template.Description); description != "" {
		b.WriteString("\n")
		b.WriteString(clip(description, 700))
		b.WriteString("\n")
	}
	return b.String()
}

// shareEmbed summarises the template without exposing private settings or its
// passcode.
func shareEmbed(template *cs.Template, baseURL, sharer string) *discordgo.MessageEmbed {
	description := templateDetails(template, sharer)
	description += "\n-# **Load with Locksmith** starts this lock here with draws, reminders, and history. "
	description += "**Open in ChastiSafe** loads it on the website without the bot."

	embed := &discordgo.MessageEmbed{
		Title:       templateName(template),
		URL:         chastisafeShareURL(baseURL, template.ID),
		Color:       embedColor,
		Description: description,
	}
	if sharer != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{
			Text: "Shared by " + sharer + " · Template " + template.ID.String(),
		}
	} else {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: "Template " + template.ID.String()}
	}
	return embed
}

func templateCardsText(cards []cs.TemplateCard) string {
	if len(cards) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cards))
	for _, card := range cards {
		if card.Min == card.Max {
			parts = append(parts, fmt.Sprintf("%s %d", card.Name.Short(), card.Min))
		} else {
			parts = append(parts, fmt.Sprintf("%s %d-%d", card.Name.Short(), card.Min, card.Max))
		}
	}
	return strings.Join(parts, ", ")
}

func greensText(count int) string {
	if count > 1000 {
		return "all greens"
	}
	return strconv.Itoa(count)
}

// shareButtons offers loading, an optional simulation for card templates, and
// the website link.
func shareButtons(template *cs.Template, baseURL string) []discordgo.MessageComponent {
	if template == nil {
		return nil
	}

	buttons := []discordgo.MessageComponent{
		discordgo.Button{
			CustomID: shareNamespace + ":load:" + template.ID.String(),
			Label:    "Load with Locksmith",
			Style:    discordgo.PrimaryButton,
		},
	}
	if template.IsCardLock() {
		buttons = append(buttons, discordgo.Button{
			CustomID: simulationNamespace + ":" + template.ID.String(),
			Label:    "Simulation",
			Style:    discordgo.SecondaryButton,
		})
	}
	buttons = append(buttons, chastisafeLinkButton(baseURL, template.ID))

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: buttons},
	}
}

// chastisafeLinkButton opens the template on the website without the bot.
func chastisafeLinkButton(baseURL string, templateID cs.ID) discordgo.Button {
	return discordgo.Button{
		Label: "Open in ChastiSafe",
		Style: discordgo.LinkButton,
		URL:   chastisafeShareURL(baseURL, templateID),
	}
}

// handleShareComponent opens the load form for the member who pressed the
// share button. A member without the app gets an install prompt instead.
func (f *Feature) handleShareComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 3 || parts[1] != "load" || strings.TrimSpace(parts[2]) == "" {
		return bot.ReplyEphemeral(s, i, "This button is out of date.")
	}
	templateID := cs.ID(parts[2])

	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	if _, _, err := f.clientFor(ctx, userID); err != nil {
		if errors.Is(err, errNotLoggedIn) && !appAvailableFor(i) {
			f.track("load failed: not installed")
			return f.replyInstallPrompt(s, i, templateID)
		}
		f.track("load failed: not logged in")
		return bot.ReplyEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	return openLoadModal(s, i, templateID)
}

// appAvailableFor reports whether the member who pressed a component can use
// Locksmith. Discord sends the installation contexts that authorized the
// interaction, so a user who did not install the app is visible when the only
// user install belongs to the member who posted the message.
func appAvailableFor(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Interaction == nil {
		return true
	}
	if _, ok := i.AuthorizingIntegrationOwners[discordgo.ApplicationIntegrationGuildInstall]; ok {
		return true
	}
	owner, ok := i.AuthorizingIntegrationOwners[discordgo.ApplicationIntegrationUserInstall]
	if !ok {
		return true
	}
	return owner == bot.UserID(i)
}

// replyInstallPrompt tells a member without the app how to install it and
// offers the website link as the no-install path.
func (f *Feature) replyInstallPrompt(s *discordgo.Session, i *discordgo.InteractionCreate, templateID cs.ID) error {
	clientID := ""
	if s != nil && s.State != nil && s.State.User != nil {
		clientID = s.State.User.ID
	}

	embed := &discordgo.MessageEmbed{
		Title: "Install Locksmith first",
		Color: embedColor,
		Description: "You need Locksmith installed to load locks from Discord.\n\n" +
			"1. Press **Install Locksmith** and add it to your account.\n" +
			"2. Run `/login` with your ChastiSafe developer code.\n" +
			"3. Press **Load with Locksmith** on the original post again.\n\n" +
			"If you do not want to install Locksmith, press **Open in ChastiSafe** to load it on the website.",
	}
	return bot.ReplyEmbed(s, i,
		[]*discordgo.MessageEmbed{embed},
		[]discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label: "Install Locksmith",
					Style: discordgo.LinkButton,
					URL:   userInstallURL(clientID),
				},
				discordgo.Button{
					Label: "Open in ChastiSafe",
					Style: discordgo.LinkButton,
					URL:   chastisafeShareURL(f.baseURL, templateID),
				},
			}},
		},
		true,
	)
}

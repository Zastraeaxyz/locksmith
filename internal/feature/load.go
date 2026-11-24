package feature

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const loadNamespace = "load"

// handleLockLoad parses a share link and opens the private load form. The lock
// code and keyholder passcode are entered in the modal so they never appear in
// the channel.
func (f *Feature) handleLockLoad(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, link string) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	templateID, err := parseTemplateRef(link)
	if err != nil {
		f.track("load failed: bad link")
		return bot.ReplyEphemeral(s, i, err.Error())
	}
	if _, _, err := f.clientFor(ctx, userID); err != nil {
		f.track("load failed: not logged in")
		return bot.ReplyEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	return openLoadModal(s, i, templateID)
}

// openLoadModal asks for the lock code and an optional keyholder passcode.
func openLoadModal(s *discordgo.Session, i *discordgo.InteractionCreate, templateID cs.ID) error {
	if s == nil || i == nil {
		return errors.New("load: missing interaction")
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			Title:    "Load a lock",
			CustomID: loadNamespace + ":code:" + templateID.String(),
			Components: []discordgo.MessageComponent{
				&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID:    "code",
						Label:       "Lock code",
						Style:       discordgo.TextInputShort,
						Placeholder: "Set your device to this code, or enter your own",
						Value:       newLockCode(),
						MinLength:   1,
						MaxLength:   64,
						Required:    true,
					},
				}},
				&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{
						CustomID:    "passcode",
						Label:       "Keyholder passcode (only if you have one)",
						Style:       discordgo.TextInputShort,
						Placeholder: "Leave blank when the lock has no passcode",
						MinLength:   0,
						MaxLength:   64,
						Required:    false,
					},
				}},
			},
		},
	})
}

// handleLoadModal loads the shared template for the member who submitted the
// form.
func (f *Feature) handleLoadModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	parts := strings.Split(i.ModalSubmitData().CustomID, ":")
	if len(parts) != 3 || parts[1] != "code" || strings.TrimSpace(parts[2]) == "" {
		return bot.ReplyEphemeral(s, i, "This load form is out of date. Run `/lock load` again.")
	}
	templateID := cs.ID(parts[2])

	code := strings.TrimSpace(modalValue(i.ModalSubmitData().Components, "code"))
	if code == "" {
		f.track("load failed: empty code")
		return bot.ReplyEphemeral(s, i, "Enter the code you set on your device.")
	}
	var passcode *string
	if value := strings.TrimSpace(modalValue(i.ModalSubmitData().Components, "passcode")); value != "" {
		passcode = &value
	}

	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		f.track("load failed: not logged in")
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}

	template, err := client.Template(ctx, templateID)
	if err != nil {
		f.track("load failed: " + loadFailureReason(err))
		f.log.Info("loading a lock template failed", "user_id", userID, "template_id", templateID.String(), "err", err)
		return sendEphemeral(s, i, f.loadFailure(ctx, userID, err, nil))
	}
	if strings.EqualFold(strings.TrimSpace(template.Status), "disabled") {
		f.track("load failed: disabled")
		return sendEphemeral(s, i, "That lock is disabled, so it cannot be loaded. Ask your keyholder to enable it.")
	}

	locks, err := client.Load(ctx, cs.LoadRequest{
		TemplateID: templateID,
		Code:       code,
		Purpose:    defaultPurpose(template),
		Passcode:   passcode,
	})
	if err != nil {
		f.track("load failed: " + loadFailureReason(err))
		f.log.Info("loading a lock failed", "user_id", userID, "template_id", templateID.String(), "err", err)
		return sendEphemeral(s, i, f.loadFailure(ctx, userID, err, template))
	}
	if len(locks) == 0 {
		f.track("load failed: empty response")
		return sendEphemeral(s, i, "ChastiSafe did not return a lock. Try again, or use the ChastiSafe link.")
	}

	f.track("load success")
	f.forgetLockAutocomplete(userID)
	f.log.Info("lock loaded",
		"user_id", userID,
		"template_id", templateID.String(),
		"lock_id", locks[0].ID.String(),
	)
	return sendEphemeral(s, i, loadResultText(&locks[0], cred.ChastiSafeID, f.now()))
}

// parseTemplateRef accepts a ChastiSafe share link, a template link, or a bare
// template ID. Any URL is allowed because only the fragment is read.
func parseTemplateRef(raw string) (cs.ID, error) {
	text := strings.TrimSpace(raw)
	text = strings.Trim(text, "<>")
	if text == "" {
		return "", errors.New("Paste a ChastiSafe share link, for example `https://chastisafe.com/#l/1234`.")
	}

	if index := strings.IndexByte(text, '#'); index >= 0 {
		fragment := text[index+1:]
		for _, prefix := range []string{"l/", "lock/"} {
			if strings.HasPrefix(fragment, prefix) {
				return cleanTemplateID(strings.TrimPrefix(fragment, prefix))
			}
		}
		if strings.HasPrefix(fragment, "loaded/") {
			return "", errors.New("That is a running lock link, not a share link. Ask your keyholder for the `#l/` link.")
		}
	}

	if !strings.ContainsAny(text, "/:?# \t\r\n") {
		return cleanTemplateID(text)
	}
	return "", errors.New("That does not look like a ChastiSafe share link. It should contain `#l/` and a template ID.")
}

func cleanTemplateID(raw string) (cs.ID, error) {
	id := strings.TrimSpace(raw)
	if cut := strings.IndexAny(id, "/?# \t\r\n"); cut >= 0 {
		id = id[:cut]
	}
	if id == "" {
		return "", errors.New("That share link has no template ID. Ask your keyholder for a fresh link.")
	}
	for _, r := range id {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return "", errors.New("That share link has an invalid template ID. Ask your keyholder for a fresh link.")
		}
	}
	return cs.ID(id), nil
}

// chastisafeShareURL builds the website share link from the configured API
// origin. A self-hosted ChastiSafe keeps its own host.
func chastisafeShareURL(baseURL string, id cs.ID) string {
	origin := "https://chastisafe.com"
	if base := strings.TrimSpace(baseURL); base != "" {
		if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			origin = parsed.Scheme + "://" + parsed.Host
		}
	}
	return origin + "/#l/" + id.String()
}

// newLockCode suggests a four digit code for the member to set on their device.
// They can replace it in the form.
func newLockCode() string {
	const digits = "0123456789"
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "1234"
	}
	for idx := range buf {
		buf[idx] = digits[int(buf[idx])%len(digits)]
	}
	return string(buf)
}

// defaultPurpose picks the purpose the website would preselect: chastity when
// the lock allows it, otherwise the first allowed purpose.
func defaultPurpose(template *cs.Template) cs.Purpose {
	if template == nil || template.AllowedPurposes == 0 {
		return cs.PurposeChastity
	}
	if template.AllowedPurposes.Has(cs.PurposeChastity) {
		return cs.PurposeChastity
	}
	for _, purpose := range []cs.Purpose{
		cs.PurposeTest, cs.PurposeBondage, cs.PurposeTask, cs.PurposeLoyalty, cs.PurposeEmergency,
	} {
		if template.AllowedPurposes.Has(purpose) {
			return purpose
		}
	}
	return cs.PurposeChastity
}

func loadResultText(lock *cs.LoadedLock, chastisafeID string, now time.Time) string {
	if lock == nil {
		return "ChastiSafe did not return a lock."
	}

	var b strings.Builder
	fmt.Fprintf(&b, ":white_check_mark: Loaded **%s**.\n\n", lockName(lock))
	view := cs.LockView{Lock: *lock, Role: cs.RoleLockee}
	b.WriteString(renderLockStatus(&view, statusOptions{
		viewerID:    chastisafeID,
		showType:    true,
		showPurpose: true,
		now:         now,
	}))
	b.WriteString("\n\n")

	switch {
	case lock.IsCardLock():
		b.WriteString("Next: run `/lock draw` to open the draw board, or `/lock notify` for reminders.")
	case lock.IsTimerLock():
		b.WriteString("Next: run `/lock status` to watch the timer, or `/lock notify` for reminders.")
	default:
		b.WriteString("Next: run `/lock status` to watch it, or `/lock notify` for reminders.")
	}
	if lock.CheckInPeriod > 0 {
		b.WriteString(" Check in with `/lock checkin` when one is due.")
	}
	return b.String()
}

// loadFailure turns a ChastiSafe load error into a private reply.
func (f *Feature) loadFailure(ctx context.Context, userID string, err error, template *cs.Template) string {
	switch {
	case cs.IsReason(err, "nodoublelocking"):
		return "That lock does not allow a second keyholder."
	case cs.IsReason(err, "requestedpurposenotallowed"):
		if template != nil {
			return fmt.Sprintf("That lock does not allow that purpose. Allowed purposes: %s.",
				cs.FormatPurposes(template.AllowedPurposes))
		}
		return "That lock does not allow that purpose."
	case cs.IsReason(err, "locknotavailable"):
		return "That lock is not available to load right now. It may be disabled, or your keyholder may have blocked it."
	case cs.IsReason(err, "passcodemismatch"):
		return "That keyholder passcode did not match. Check it with your keyholder, then run `/lock load` again and enter it in the keyholder passcode field."
	case errors.Is(err, cs.ErrLockNotFound):
		return "That lock template no longer exists. Ask your keyholder for a fresh share link."
	default:
		return f.apiFailure(ctx, userID, err)
	}
}

// loadFailureReason buckets a failed load for anonymous analytics. The labels
// are coarse on purpose: no codes, IDs, or usernames.
func loadFailureReason(err error) string {
	switch {
	case cs.IsReason(err, "nodoublelocking"):
		return "double locking"
	case cs.IsReason(err, "requestedpurposenotallowed"):
		return "purpose not allowed"
	case cs.IsReason(err, "locknotavailable"):
		return "lock unavailable"
	case cs.IsReason(err, "passcodemismatch"):
		return "passcode mismatch"
	case errors.Is(err, cs.ErrLockNotFound):
		return "not found"
	case errors.Is(err, cs.ErrUnauthorized):
		return "rejected"
	default:
		return "unreachable"
	}
}

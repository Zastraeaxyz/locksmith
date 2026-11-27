package feature

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const (
	loadNamespace = "load"

	// defaultLockCodeDigits is used when the account has no code length set.
	defaultLockCodeDigits = 4
	// maxLockCodeDigits bounds a code generated from account settings.
	maxLockCodeDigits = 32
)

// handleLockLoad parses a share link and shows the lock privately before
// anything is loaded. The preview carries a Load lock button, so the member
// can check what the lock expects and dismiss it without starting the form.
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
	if err := bot.DeferEphemeral(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, userID)
	if err != nil {
		f.track("load failed: not logged in")
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}

	template, err := client.Template(ctx, templateID)
	if err != nil {
		f.track("load failed: " + loadFailureReason(err))
		f.log.Info("reading a lock template failed", "user_id", userID, "template_id", templateID.String(), "err", err)
		return sendEphemeral(s, i, f.loadFailure(ctx, userID, err, nil))
	}
	if template == nil {
		f.track("load failed: not found")
		return sendEphemeral(s, i, "That lock template no longer exists. Ask your keyholder for a fresh share link.")
	}
	if templateDisabled(template) {
		f.track("load failed: disabled")
		return sendEphemeral(s, i, "That lock is disabled, so it cannot be loaded. Ask your keyholder to enable it.")
	}

	return bot.EditMessageAllowed(s, i, "",
		[]*discordgo.MessageEmbed{loadPreviewEmbed(template, f.baseURL)},
		loadPreviewButtons(template, f.baseURL),
		noMentions(),
	)
}

// loadPreviewEmbed shows the lock before anything is loaded. It carries the
// same details as a share post, minus the sharer, because the member already
// has the link.
func loadPreviewEmbed(template *cs.Template, baseURL string) *discordgo.MessageEmbed {
	description := templateDetails(template, "")
	description += "\n-# Press **Load lock** to start this lock here with draws, reminders, and history. "
	description += "**Open in ChastiSafe** loads it on the website without the bot."

	return &discordgo.MessageEmbed{
		Title:       templateName(template),
		URL:         chastisafeShareURL(baseURL, template.ID),
		Color:       embedColor,
		Description: description,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Template " + template.ID.String()},
	}
}

// loadPreviewButtons offers the load form, a simulation for card templates,
// and the website link. The Simulation button stays visible even when the
// keyholder has turned simulation off, so pressing it can explain why.
func loadPreviewButtons(template *cs.Template, baseURL string) []discordgo.MessageComponent {
	if template == nil {
		return nil
	}

	buttons := []discordgo.MessageComponent{
		discordgo.Button{
			CustomID: loadNamespace + ":start:" + template.ID.String(),
			Label:    "Load lock",
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

// handleLoadComponent opens the passcode form from the private preview.
func (f *Feature) handleLoadComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 3 || parts[1] != "start" || strings.TrimSpace(parts[2]) == "" {
		return bot.ReplyEphemeral(s, i, "This button is out of date. Run `/lock load` again.")
	}
	return openLoadModal(s, i, cs.ID(parts[2]))
}

// openLoadModal asks for an optional keyholder passcode. The lock code is
// generated from the member's ChastiSafe code settings, so it never appears as
// an editable field they could replace with one they would remember.
func openLoadModal(s *discordgo.Session, i *discordgo.InteractionCreate, templateID cs.ID) error {
	if s == nil || i == nil {
		return errors.New("load: missing interaction")
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: loadModalData(templateID),
	})
}

// loadModalData is the private load form. The keyholder passcode is the only
// input; the lock code is not the member's to choose.
func loadModalData(templateID cs.ID) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		Title:    "Load a lock",
		CustomID: loadNamespace + ":form:" + templateID.String(),
		Components: []discordgo.MessageComponent{
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
	}
}

// handleLoadModal loads the shared template for the member who submitted the
// form.
func (f *Feature) handleLoadModal(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}

	parts := strings.Split(i.ModalSubmitData().CustomID, ":")
	if len(parts) != 3 || parts[1] != "form" || strings.TrimSpace(parts[2]) == "" {
		return bot.ReplyEphemeral(s, i, "This load form is out of date. Run `/lock load` again.")
	}
	templateID := cs.ID(parts[2])

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
	if templateDisabled(template) {
		f.track("load failed: disabled")
		return sendEphemeral(s, i, "That lock is disabled, so it cannot be loaded. Ask your keyholder to enable it.")
	}

	profile, err := client.Profile(ctx)
	if err != nil {
		f.track("load failed: " + loadFailureReason(err))
		f.log.Info("reading code settings failed", "user_id", userID, "err", err)
		return sendEphemeral(s, i, f.apiFailure(ctx, userID, err))
	}
	code, err := generateLockCode(profile.Digits, profile.CharacterSet)
	if err != nil {
		f.track("load failed: code generation")
		f.log.Warn("generating a lock code", "user_id", userID, "err", err)
		return sendEphemeral(s, i, "I could not generate a lock code. Run `/lock load` again.")
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

	watched := true
	if err := f.watchLock(ctx, s, userID, &locks[0]); err != nil {
		watched = false
		f.log.Warn("subscribing after load", "user_id", userID, "lock_id", locks[0].ID.String(), "err", err)
	}

	f.track("load success")
	f.forgetLockAutocomplete(userID)
	f.log.Info("lock loaded",
		"user_id", userID,
		"template_id", templateID.String(),
		"lock_id", locks[0].ID.String(),
	)
	return sendLoadResult(s, i, loadResultText(&locks[0], cred.ChastiSafeID, code, watched, f.now()), templateID, &locks[0])
}

// sendLoadResult edits the deferred private reply and offers a Simulation
// button for a card lock, so the lockee can see what the lock expects.
func sendLoadResult(s *discordgo.Session, i *discordgo.InteractionCreate, text string, templateID cs.ID, lock *cs.LoadedLock) error {
	var components []discordgo.MessageComponent
	if lock != nil && lock.IsCardLock() {
		components = simulationButtons(templateID)
	}
	return bot.EditMessageChunks(s, i, text, components, true)
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

// generateLockCode builds the code the member sets on their device, using the
// length and character set from their ChastiSafe account settings. Each
// character appears at most once, so the code has no pattern the member could
// pick and then remember.
func generateLockCode(digits int, characterSet string) (string, error) {
	alphabet := codeAlphabet(characterSet)
	if digits <= 0 {
		digits = defaultLockCodeDigits
	}
	if digits > maxLockCodeDigits {
		digits = maxLockCodeDigits
	}

	out := make([]byte, 0, digits)
	remaining := []byte(alphabet)
	for len(out) < digits && len(remaining) > 0 {
		pick, err := randIndex(len(remaining))
		if err != nil {
			return "", err
		}
		out = append(out, remaining[pick])
		remaining = append(remaining[:pick], remaining[pick+1:]...)
	}
	// A code longer than the character set cannot avoid repeats. Top up from
	// the full set rather than shortening the code the device expects.
	for len(out) < digits {
		pick, err := randIndex(len(alphabet))
		if err != nil {
			return "", err
		}
		out = append(out, alphabet[pick])
	}
	return string(out), nil
}

// codeAlphabet maps the account's ChastiSafe character set to the characters a
// device code may use. Unknown values fall back to digits, which every device
// accepts.
func codeAlphabet(characterSet string) string {
	const (
		digits  = "0123456789"
		letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	)
	set := strings.ToUpper(strings.TrimSpace(characterSet))
	if strings.Contains(set, "ALPHA") || strings.Contains(set, "LETTER") {
		if strings.Contains(set, "NUM") {
			return digits + letters
		}
		return letters
	}
	return digits
}

func randIndex(n int) (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("reading random bytes: %w", err)
	}
	return int(value.Int64()), nil
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

// loadResultText is the private reply after a successful load. It is the only
// place Locksmith shows the generated code, so the member can set the device
// and then forget it.
func loadResultText(lock *cs.LoadedLock, chastisafeID, code string, watched bool, now time.Time) string {
	if lock == nil {
		return "ChastiSafe did not return a lock."
	}

	var b strings.Builder
	fmt.Fprintf(&b, ":white_check_mark: Loaded **%s**.\n\n", lockName(lock))
	if code != "" {
		fmt.Fprintf(&b, "**Set your device to code `%s`, then forget it.** ChastiSafe keeps it for when the lock opens.\n\n", code)
	}
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
		b.WriteString("Next: run `/lock draw` to open the draw board.")
	case lock.IsTimerLock():
		b.WriteString("Next: run `/lock status` to watch the timer.")
	default:
		b.WriteString("Next: run `/lock status` to watch it.")
	}
	if lock.CheckInPeriod > 0 {
		b.WriteString(" Check in with `/lock checkin` when one is due.")
	}
	b.WriteString("\n")
	if watched {
		b.WriteString("Reminders are on: I will DM you when a draw is ready or a check-in is due.")
	} else {
		b.WriteString("Reminders could not be turned on now; I will restore them the next time I start.")
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

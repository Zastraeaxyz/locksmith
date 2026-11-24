package feature

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const (
	drawNamespace        = "draw"
	drawSessionTTL       = 2 * time.Hour
	drawLogLimit         = 50
	drawDescriptionLimit = 3900
)

type drawEntry struct {
	card        cs.Card
	dice        int
	byKeyholder bool
}

// drawSession is one public draw board. It is keyed by an opaque ID embedded
// in the button custom IDs, so it survives edits and does not depend on the
// message ID.
type drawSession struct {
	mu sync.Mutex
	id string

	ownerID       string
	ownerName     string
	keyholderID   string
	keyholderName string

	lockID   cs.ID
	lockName string

	channelID string
	startedAt time.Time

	entries  []drawEntry
	lastCard cs.Card
	lastLock *cs.LoadedLock
	busy     bool
}

func (s *drawSession) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	return true
}

func (s *drawSession) end() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

func (s *drawSession) add(entry drawEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
	if entry.card != "" {
		s.lastCard = entry.card
	}
}

func (s *drawSession) setLock(lock *cs.LoadedLock) {
	s.mu.Lock()
	s.lastLock = lock
	s.mu.Unlock()
}

func (s *drawSession) snapshot() ([]drawEntry, cs.Card, *cs.LoadedLock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]drawEntry(nil), s.entries...), s.lastCard, s.lastLock
}

func (s *drawSession) canUse(userID string) bool {
	if userID == "" {
		return false
	}
	if userID == s.ownerID {
		return true
	}
	return s.keyholderID != "" && userID == s.keyholderID
}

// startDrawBoard posts the public draw board for the caller's card lock.
func (f *Feature) startDrawBoard(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	// The draw board is the interaction response, so acknowledge publicly. A
	// blocked draw deletes this placeholder and answers privately instead.
	if err := bot.Defer(s, i, false); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return f.replyDrawPrivate(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return f.replyDrawPrivate(s, i, f.apiFailure(ctx, userID, err))
	}

	lock := activeCardLock(locks, cred.ChastiSafeID)
	if lock == nil {
		target := drawTarget(locks, cred.ChastiSafeID)
		freeze := map[cs.ID]time.Time{}
		if target != nil && target.IsFrozen() {
			freeze = f.freezeTimes(ctx, client, []cs.LoadedLock{*target})
		}
		return f.replyDrawPrivate(s, i, drawBlockedMessage(target, freeze, f.now()))
	}
	// A lock with nothing to draw gets a plain private note, not an empty board.
	if lock.DrawsAvailable() == 0 {
		return f.replyDrawPrivate(s, i, drawBlockedMessage(lock, nil, f.now()))
	}

	session := &drawSession{
		id:            newSessionID(),
		ownerID:       userID,
		ownerName:     interactionName(i),
		keyholderID:   externalKeyholderID(lock),
		keyholderName: strings.TrimSpace(lock.Keyholder),
		lockID:        lock.ID,
		lockName:      lockName(lock),
		channelID:     i.ChannelID,
		startedAt:     f.now(),
	}
	session.setLock(lock)

	f.putSession(session)
	return bot.EditMessage(s, i, "",
		[]*discordgo.MessageEmbed{session.embed(lock, "")}, drawButtons(session.id))
}

// replyDrawPrivate removes the public placeholder and answers privately, since
// Discord cannot change the visibility of a response that was already posted.
func (f *Feature) replyDrawPrivate(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	if err := bot.DeleteResponse(s, i); err != nil {
		f.log.Warn("deleting draw placeholder", "err", err)
	}
	return bot.ReplyEphemeralFollowup(s, i, content)
}

// drawTarget returns the newest card lock even when it cannot be drawn, so the
// caller can be told why.
func drawTarget(locks []cs.LoadedLock, chastisafeID string) *cs.LoadedLock {
	var newest *cs.LoadedLock
	for idx := range locks {
		lock := &locks[idx]
		if !lock.IsCardLock() {
			continue
		}
		if chastisafeID != "" && lock.LockeeID.String() != chastisafeID {
			continue
		}
		if newest == nil || lock.LoadTime.Time.After(newest.LoadTime.Time) {
			newest = lock
		}
	}
	return newest
}

// drawBlockedMessage explains why a card lock cannot be drawn. An empty
// message means the lock is drawable.
func drawBlockedMessage(lock *cs.LoadedLock, freezeSince map[cs.ID]time.Time, now time.Time) string {
	if lock == nil {
		return "You have no active card lock to draw from."
	}
	name := lockName(lock)
	switch {
	case lock.IsFrozen():
		if since, ok := freezeSince[lock.ID]; ok && !since.IsZero() {
			return fmt.Sprintf("**%s** is frozen since %s. Drawing is unavailable until it is unfrozen.",
				name, relativeTimestamp(since))
		}
		return fmt.Sprintf("**%s** is frozen. Drawing is unavailable until it is unfrozen.", name)
	case lock.IsReady():
		return fmt.Sprintf("**%s** is ready to unlock. There is nothing left to draw.", name)
	case lock.DrawsAvailable() == 0:
		if wait := lock.NextPickIn(); wait > 0 {
			return fmt.Sprintf("No draws available yet for **%s**. Next draw %s.",
				name, relativeTimestamp(now.Add(wait)))
		}
		return fmt.Sprintf("No draws available right now for **%s**.", name)
	default:
		return ""
	}
}

func (f *Feature) handleDrawComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 3 {
		return bot.ReplyEphemeral(s, i, "This button is out of date.")
	}
	action, sessionID := parts[1], parts[2]

	session, ok := f.session(sessionID)
	if !ok {
		_ = bot.ReplyEphemeral(s, i, "This draw session expired. Run `/lock draw` again.")
		if i.Message != nil {
			_ = bot.ClearComponents(s, i.ChannelID, i.Message.ID)
		}
		return nil
	}

	userID := bot.UserID(i)
	if !session.canUse(userID) {
		if action == "stop" {
			return bot.ReplyEphemeral(s, i, "You can't stop someone else's draw session.")
		}
		return bot.ReplyEphemeral(s, i, "You can't draw someone else's lock.")
	}

	switch action {
	case "next":
		return f.drawNext(ctx, s, i, session)
	case "stop":
		return f.drawStop(ctx, s, i, session)
	default:
		return bot.ReplyEphemeral(s, i, "Unknown draw action.")
	}
}

func (f *Feature) drawNext(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is already in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}

	client, _, err := f.clientFor(ctx, session.ownerID)
	if err != nil {
		f.deleteSession(session.id)
		return f.renderBoard(s, i, session, nil, false, f.apiFailure(ctx, session.ownerID, err))
	}

	locks, err := client.Loaded(ctx)
	if err != nil {
		if errors.Is(err, cs.ErrUnauthorized) {
			_ = f.store.DeleteCredential(ctx, session.ownerID)
			f.deleteSession(session.id)
			return f.renderBoard(s, i, session, nil, false, "ChastiSafe rejected the saved code. Run `/login` again.")
		}
		return f.renderBoard(s, i, session, nil, true,
			"I could not reach ChastiSafe right now. Try again in a moment.")
	}

	lock := findLockByID(locks, session.lockID)
	if lock == nil {
		f.deleteSession(session.id)
		return f.renderBoard(s, i, session, nil, false, "That lock is no longer active.")
	}
	session.setLock(lock)

	switch {
	case lock.IsReady():
		f.deleteSession(session.id)
		return f.renderBoard(s, i, session, lock, false, "The lock is ready to unlock. Drawing stopped.")
	case lock.IsFrozen():
		f.deleteSession(session.id)
		return f.renderBoard(s, i, session, lock, false, "The lock is frozen. Drawing stopped.")
	case lock.DrawsAvailable() == 0:
		f.deleteSession(session.id)
		return f.renderBoard(s, i, session, lock, false, "")
	}

	result, err := client.Pick(ctx, lock.ID)
	if err != nil {
		// Leave the board open when draws may still be available so the member
		// can retry.
		return f.renderBoard(s, i, session, lock, true, f.apiFailure(ctx, session.ownerID, err))
	}

	updated := lock
	if result.Lock != nil {
		updated = result.Lock
	}
	session.setLock(updated)
	session.add(drawEntry{
		card:        result.Card,
		dice:        result.Dice,
		byKeyholder: bot.UserID(i) != session.ownerID,
	})

	keep := updated.DrawsAvailable() > 0 && !updated.IsReady() && !updated.IsFrozen()
	var note string
	if result.Done {
		note = "That was the last draw. The lock is ready to unlock."
		keep = false
	}

	if !keep {
		f.deleteSession(session.id)
	}
	return f.renderBoard(s, i, session, updated, keep, note)
}

func (f *Feature) drawStop(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession) error {
	if !session.begin() {
		return bot.ReplyEphemeral(s, i, "A draw is in progress. Try again in a moment.")
	}
	defer session.end()

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}
	f.deleteSession(session.id)
	return f.renderBoard(s, i, session, nil, false, "Drawing stopped.")
}

func (f *Feature) renderBoard(s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession, lock *cs.LoadedLock, buttons bool, note string) error {
	embed := session.embed(lock, note)
	components := []discordgo.MessageComponent{}
	if buttons {
		components = drawButtons(session.id)
	}
	return bot.EditMessage(s, i, "", []*discordgo.MessageEmbed{embed}, components)
}

func (s *drawSession) embed(lock *cs.LoadedLock, note string) *discordgo.MessageEmbed {
	entries, lastCard, stored := s.snapshot()
	if lock == nil {
		lock = stored
	}

	var b strings.Builder
	b.WriteString(renderDrawLog(entries))
	b.WriteString("\n\n")
	b.WriteString(drawStatsLine(lock, len(entries)))
	if next := nextDrawLine(lock, time.Now()); next != "" {
		b.WriteString("\n")
		b.WriteString(next)
	}
	if note != "" {
		b.WriteString("\n\n")
		b.WriteString(note)
	}
	if len(entries) > 0 {
		b.WriteString("\n\n")
		remaining, ok := cardsRemaining(lock)
		b.WriteString(totalsLine(entries, remaining, ok))
	}

	return &discordgo.MessageEmbed{
		Title:       s.lockName,
		Description: truncate(b.String(), drawDescriptionLimit),
		Color:       cardColor(lastCard),
	}
}

func drawButtons(sessionID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: drawNamespace + ":next:" + sessionID,
				Label:    "Draw",
				Style:    discordgo.PrimaryButton,
			},
			discordgo.Button{
				CustomID: drawNamespace + ":stop:" + sessionID,
				Label:    "Stop",
				Style:    discordgo.SecondaryButton,
			},
		}},
	}
}

func renderDrawLog(entries []drawEntry) string {
	var b strings.Builder
	b.WriteString("**Draws this session**")
	if len(entries) == 0 {
		b.WriteString("\nNo draws yet.")
		return b.String()
	}

	start := 0
	if len(entries) > drawLogLimit {
		start = len(entries) - drawLogLimit
		fmt.Fprintf(&b, "\n… %d earlier draw(s) not shown", start)
	}
	for idx := start; idx < len(entries); idx++ {
		entry := entries[idx]
		fmt.Fprintf(&b, "\n%d. ", idx+1)
		switch {
		case entry.card != "":
			writeCardEmoji(&b, entry.card)
			b.WriteString(cardLogLabel(entry.card))
			if entry.card == cs.CardDice && entry.dice > 0 {
				fmt.Fprintf(&b, " (%d)", entry.dice)
			}
		case entry.dice > 0:
			writeCardEmoji(&b, cs.CardDice)
			fmt.Fprintf(&b, "Dice %d", entry.dice)
		default:
			b.WriteString("Draw")
		}
		if entry.byKeyholder {
			b.WriteString(" · keyholder")
		}
	}
	return b.String()
}

func drawStatsLine(lock *cs.LoadedLock, used int) string {
	left := 0
	if lock != nil {
		left = lock.DrawsAvailable()
	}
	return fmt.Sprintf("%d used · %d left", used, left)
}

func nextDrawLine(lock *cs.LoadedLock, now time.Time) string {
	if lock == nil {
		return ""
	}
	switch {
	case lock.IsReady():
		return "Ready to unlock."
	case lock.IsFrozen():
		return "Frozen."
	case lock.DrawsAvailable() > 0:
		return ""
	}
	wait := lock.NextPickIn()
	if wait <= 0 {
		return ""
	}
	return fmt.Sprintf("Next draw %s", relativeTimestamp(now.Add(wait)))
}

func totalsLine(entries []drawEntry, remaining int, hasRemaining bool) string {
	reds, greens, yellows, doubles := 0, 0, 0, 0
	for _, entry := range entries {
		switch entry.card {
		case cs.CardRed:
			reds++
		case cs.CardGreen:
			greens++
		case cs.CardDoubleUp:
			doubles++
		case cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3,
			cs.CardYellowM1, cs.CardYellowM2, cs.CardYellow,
			cs.CardYellowUK, cs.CardYellowRN:
			yellows++
		}
	}

	parts := []string{
		fmt.Sprintf("%s %d red", cardEmojiMarkup(cs.CardRed), reds),
		fmt.Sprintf("%s %d green", cardEmojiMarkup(cs.CardGreen), greens),
	}
	if yellows > 0 {
		parts = append(parts, fmt.Sprintf("%s %d yellow", cardEmojiMarkup(cs.CardYellow), yellows))
	}
	if doubles > 0 {
		parts = append(parts, fmt.Sprintf("%s %d double up", cardEmojiMarkup(cs.CardDoubleUp), doubles))
	}
	if hasRemaining {
		parts = append(parts, fmt.Sprintf("%d remaining", remaining))
	}
	return "Totals: " + strings.Join(parts, ", ")
}

// cardEmojiIDs are application emoji, so they render in DMs and in any guild
// the app is used in, without extra permissions.
var cardEmojiIDs = map[cs.Card]struct{ name, id string }{
	cs.CardGreen:    {"green", "1574875078255185957"},
	cs.CardRed:      {"red", "1574875081254113453"},
	cs.CardYellowP1: {"yellow_plus1", "1574875089978134669"},
	cs.CardYellowP2: {"yellow_plus2", "1574875091160932394"},
	cs.CardYellowP3: {"yellow_plus3", "1574875092079345705"},
	cs.CardYellowM1: {"yellow_minus1", "1574875086408782055"},
	cs.CardYellowM2: {"yellow_minus2", "1574875088208273539"},
	cs.CardYellow:   {"yellow_unknown", "1574875097389465731"},
	cs.CardYellowUK: {"yellow_unknown", "1574875097389465731"},
	cs.CardYellowRN: {"yellow_random", "1574875093425856683"},
	cs.CardFreeze:   {"freeze", "1574875038392385647"},
	cs.CardArctic:   {"arctic", "1574875033468281044"},
	cs.CardStun:     {"stun", "1574875085137772624"},
	cs.CardSticky:   {"sticky", "1574875083883806883"},
	cs.CardReset:    {"reset", "1574875082701017129"},
	cs.CardPink:     {"pink", "1574875079970398278"},
	cs.CardGoAgain:  {"go_again", "1574875040669896885"},
	cs.CardDoubleUp: {"double", "1574875037104742581"},
	cs.CardDice:     {"dice", "1574875035749982268"},
}

// cardEmojiMarkup returns the inline custom emoji for a card, or "" when the
// card has none.
func cardEmojiMarkup(card cs.Card) string {
	emoji, ok := cardEmojiIDs[card]
	if !ok {
		return ""
	}
	return "<:" + emoji.name + ":" + emoji.id + ">"
}

// writeCardEmoji writes a card's emoji with a trailing space, if it has one.
func writeCardEmoji(b *strings.Builder, card cs.Card) {
	if markup := cardEmojiMarkup(card); markup != "" {
		b.WriteString(markup)
		b.WriteString(" ")
	}
}

func cardLogLabel(card cs.Card) string {
	switch card {
	case cs.CardYellowP1:
		return "Yellow +1"
	case cs.CardYellowP2:
		return "Yellow +2"
	case cs.CardYellowP3:
		return "Yellow +3"
	case cs.CardYellowM1:
		return "Yellow -1"
	case cs.CardYellowM2:
		return "Yellow -2"
	case cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN:
		return "Yellow +?"
	default:
		return card.Label()
	}
}

func cardColor(card cs.Card) int {
	switch card {
	case cs.CardGreen:
		return 0x57F287
	case cs.CardRed:
		return 0xED4245
	case cs.CardFreeze, cs.CardArctic:
		return 0x3498DB
	case cs.CardDice:
		return 0x9B59B6
	case cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3,
		cs.CardYellowM1, cs.CardYellowM2, cs.CardYellow,
		cs.CardYellowUK, cs.CardYellowRN:
		return 0xFEE75C
	default:
		return 0x5865F2
	}
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// externalKeyholderID returns the keyholder's Discord user ID. ChastiSafe
// identifies an account by the Discord snowflake linked in ChastiSafe, so this
// is directly comparable to the member who pressed a button.
func externalKeyholderID(lock *cs.LoadedLock) string {
	if lock == nil || lock.IsSelfLock() {
		return ""
	}
	id := strings.TrimSpace(lock.KeyholderID.String())
	if id == "" || id == "0" {
		return ""
	}
	return id
}

func interactionName(i *discordgo.InteractionCreate) string {
	if i != nil && i.Member != nil {
		if nick := strings.TrimSpace(i.Member.Nick); nick != "" {
			return nick
		}
		if i.Member.User != nil {
			if name := strings.TrimSpace(i.Member.User.Username); name != "" {
				return name
			}
		}
	}
	if i != nil && i.User != nil {
		if name := strings.TrimSpace(i.User.Username); name != "" {
			return name
		}
	}
	return "this member"
}

func findLockByID(locks []cs.LoadedLock, id cs.ID) *cs.LoadedLock {
	for idx := range locks {
		if locks[idx].ID == id {
			return &locks[idx]
		}
	}
	return nil
}

func newSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (f *Feature) putSession(session *drawSession) {
	f.sessionsMu.Lock()
	defer f.sessionsMu.Unlock()

	now := f.now()
	for id, existing := range f.sessions {
		if now.Sub(existing.startedAt) > drawSessionTTL {
			delete(f.sessions, id)
		}
	}
	f.sessions[session.id] = session
}

func (f *Feature) session(id string) (*drawSession, bool) {
	f.sessionsMu.Lock()
	defer f.sessionsMu.Unlock()

	session, ok := f.sessions[id]
	if !ok {
		return nil, false
	}
	if f.now().Sub(session.startedAt) > drawSessionTTL {
		delete(f.sessions, id)
		return nil, false
	}
	return session, true
}

func (f *Feature) deleteSession(id string) {
	f.sessionsMu.Lock()
	delete(f.sessions, id)
	f.sessionsMu.Unlock()
}

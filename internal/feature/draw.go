package feature

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
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
	if err := bot.Defer(s, i, false); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.Edit(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return bot.Edit(s, i, f.apiFailure(ctx, userID, err))
	}

	lock := activeCardLock(locks, cred.ChastiSafeID)
	if lock == nil {
		return bot.Edit(s, i, "You have no active card lock to draw from.")
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

	if lock.DrawsAvailable() == 0 {
		return bot.EditMessage(s, i, "",
			[]*discordgo.MessageEmbed{session.embed(lock, drawWaitingNote(lock))}, nil)
	}

	f.putSession(session)
	return bot.EditMessage(s, i, "",
		[]*discordgo.MessageEmbed{session.embed(lock, "")}, drawButtons(session.id))
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
		return f.renderBoard(s, i, session, lock, false, drawWaitingNote(lock))
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
	switch {
	case result.Done:
		note = "That was the last draw. The lock is ready to unlock."
		keep = false
	case updated.DrawsAvailable() == 0:
		if wait := updated.NextPickIn(); wait > 0 {
			note = fmt.Sprintf("No draws left right now. Next draw in %s.", cs.FormatShortDuration(wait))
		} else {
			note = "No draws left right now."
		}
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

	description := renderDrawLog(entries)
	if note != "" {
		description += "\n\n" + note
	}

	embed := &discordgo.MessageEmbed{
		Title:       s.lockName,
		Author:      &discordgo.MessageEmbedAuthor{Name: s.ownerName + "'s draw"},
		Description: truncate(description, drawDescriptionLimit),
		Color:       cardColor(lastCard),
		Footer:      &discordgo.MessageEmbedFooter{Text: s.footer()},
	}
	if lock != nil {
		embed.Fields = []*discordgo.MessageEmbedField{
			{Name: "Draws available", Value: drawsField(lock), Inline: true},
			{Name: "Cards left", Value: strconv.Itoa(cardsLeft(lock)), Inline: true},
			{Name: "Greens needed", Value: strconv.Itoa(lock.GreensRemaining()), Inline: true},
			{Name: "Next draw", Value: nextDrawField(lock), Inline: true},
		}
	}
	return embed
}

func (s *drawSession) footer() string {
	if s.keyholderID != "" {
		if s.keyholderName != "" {
			return fmt.Sprintf("Only %s and their keyholder (%s) can use these buttons.", s.ownerName, s.keyholderName)
		}
		return fmt.Sprintf("Only %s and their keyholder can use these buttons.", s.ownerName)
	}
	return fmt.Sprintf("Only %s can use these buttons.", s.ownerName)
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
			b.WriteString(entry.card.Label())
			if entry.card == cs.CardDice && entry.dice > 0 {
				fmt.Fprintf(&b, " (%d)", entry.dice)
			}
		case entry.dice > 0:
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

func drawWaitingNote(lock *cs.LoadedLock) string {
	if lock == nil {
		return "No draws available right now."
	}
	if wait := lock.NextPickIn(); wait > 0 {
		return fmt.Sprintf("No draws available yet. Next draw in %s.", cs.FormatShortDuration(wait))
	}
	return "No draws available right now."
}

func drawsField(lock *cs.LoadedLock) string {
	if lock == nil {
		return "0"
	}
	value := strconv.Itoa(lock.DrawsAvailable())
	if lock.MaxPicks > 0 {
		value += " of " + strconv.Itoa(lock.MaxPicks)
	}
	return value
}

func nextDrawField(lock *cs.LoadedLock) string {
	switch {
	case lock == nil:
		return "unknown"
	case lock.IsReady():
		return "ready to unlock"
	case lock.IsFrozen():
		return "frozen"
	case lock.NextPickIn() > 0:
		return "in " + cs.FormatShortDuration(lock.NextPickIn())
	default:
		return "now"
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

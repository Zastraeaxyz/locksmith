package feature

// The draw board module owns the state behind a public board, the session
// store that keeps it reachable across edits, and the view it renders.
//
// ADR 0004 decides where a lock code may appear. A guild board is public, so
// the code for an opened lock is a private follow-up; a DM is already limited
// to the member and keyholder. replyBoardCode is the only board helper that
// reveals a code, so it takes that decision in one place.
//
// Actions that refresh the board's lock cross the same helpers, so a vanished
// lock and an unreachable API end the board the same way every time.

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
	messageID string
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

func (s *drawSession) currentLock() *cs.LoadedLock {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastLock
}

func (s *drawSession) setMessageID(id string) {
	s.mu.Lock()
	s.messageID = id
	s.mu.Unlock()
}

func (s *drawSession) messageIDs() (channelID, messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channelID, s.messageID
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

// startDrawBoard posts the public draw board for the caller's card lock. When
// auto is set, it also takes every draw the lock allows in one run.
func (f *Feature) startDrawBoard(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, auto bool) error {
	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	// The draw board is the interaction response, so acknowledge publicly. A
	// blocked draw deletes this placeholder and answers privately instead.
	if err := bot.Defer(s, i, false); err != nil {
		return err
	}
	return f.postDrawBoard(ctx, s, i, userID, "", auto)
}

// handleDrawBoardComponent opens a fresh draw board from a draw notification
// DM. The board is a new message in the same DM, so the notification stays put.
func (f *Feature) handleDrawBoardComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) != 3 || parts[1] != "open" || strings.TrimSpace(parts[2]) == "" {
		return bot.ReplyEphemeral(s, i, "This button is out of date.")
	}

	userID := bot.UserID(i)
	if userID == "" {
		return bot.ReplyEphemeral(s, i, "I cannot tell who you are.")
	}
	if err := bot.Defer(s, i, false); err != nil {
		return err
	}
	return f.postDrawBoard(ctx, s, i, userID, cs.ID(parts[2]), false)
}

// postDrawBoard posts the board in the interaction's channel. The caller must
// have deferred the reply publicly. An empty lockID picks the newest drawable
// lock, while a given ID pins the board to a lock from a notification. When
// auto is set, the board keeps drawing until the lock has nothing left.
func (f *Feature) postDrawBoard(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, userID string, lockID cs.ID, auto bool) error {
	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return f.replyDrawPrivate(s, i, f.apiFailure(ctx, userID, err))
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return f.replyDrawPrivate(s, i, f.apiFailure(ctx, userID, err))
	}

	// The board always starts from live lock state. A card freeze that is past
	// its maximum gets one more API read, so a freeze that has just ended does
	// not open a board that refuses to draw.
	now := f.now()
	locks, freeze := f.freshLocks(ctx, client, locks, now)

	lock, refusal := f.resolveBoardLock(locks, freeze, cred.ChastiSafeID, lockID, now)
	if lock == nil {
		return f.replyDrawPrivate(s, i, refusal)
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
	f.observeLock(ctx, lock)

	f.putSession(session)
	msg, err := bot.EditMessageGet(s, i, "",
		[]*discordgo.MessageEmbed{session.embed(lock, "")}, drawButtons(session, lock))
	if err != nil {
		return err
	}
	if msg != nil {
		session.setMessageID(msg.ID)
	}
	if auto {
		return f.drawAuto(ctx, s, i, session, client, lock)
	}
	return nil
}

// resolveBoardLock picks the lock for a board and explains any refusal. A
// lockID pins one lock, while an empty ID picks the newest drawable lock or a
// ready one, so the ready actions stay reachable. A card lock whose next draw
// has not accrued yet is refused with the next draw time, not opened as an
// empty board.
func (f *Feature) resolveBoardLock(locks []cs.LoadedLock, freeze map[cs.ID]freezeState, chastisafeID string, lockID cs.ID, now time.Time) (*cs.LoadedLock, string) {
	if lockID != "" {
		lock := findLockByID(locks, lockID)
		switch {
		case lock == nil:
			return nil, "That lock is no longer active. Run `/lock draw` to pick another."
		case chastisafeID != "" && lock.LockeeID.String() != chastisafeID:
			return nil, "That lock is not yours."
		case !lock.IsCardLock():
			return nil, fmt.Sprintf("**%s** is not a card lock.", lockName(lock))
		case lock.IsFrozen():
			return nil, drawBlockedMessage(lock, freeze, now)
		case !lock.IsReady() && lock.DrawsAvailable() == 0:
			return nil, drawBlockedMessage(lock, nil, now)
		}
		return lock, ""
	}

	lock := drawableLock(locks, chastisafeID)
	if lock != nil && lock.DrawsAvailable() == 0 {
		// Nothing to draw on the newest lock. A ready lock's actions are still
		// useful, so prefer that over a dead board.
		if ready := readyCardLock(locks, chastisafeID); ready != nil {
			return ready, ""
		}
		return nil, drawBlockedMessage(lock, nil, now)
	}
	if lock == nil {
		lock = readyCardLock(locks, chastisafeID)
	}
	if lock != nil {
		return lock, ""
	}

	target := drawRefusalTarget(locks, chastisafeID)
	return nil, drawBlockedMessage(target, freeze, now)
}

// replyDrawPrivate removes the public placeholder and answers privately, since
// Discord cannot change the visibility of a response that was already posted.
func (f *Feature) replyDrawPrivate(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	if err := bot.DeleteResponse(s, i); err != nil {
		f.log.Warn("deleting draw placeholder", "err", err)
	}
	return bot.ReplyEphemeralFollowup(s, i, content)
}

// drawBlockedMessage explains why a card lock cannot be drawn. An empty
// message means the lock is drawable.
func drawBlockedMessage(lock *cs.LoadedLock, freeze map[cs.ID]freezeState, now time.Time) string {
	if lock == nil {
		return "You have no active card lock to draw from."
	}
	name := lockName(lock)
	switch {
	case lock.IsFrozen():
		state := freeze[lock.ID]
		if state.pastMaximum {
			return fmt.Sprintf("ChastiSafe still reports **%s** frozen after the longest a freeze can hold. It should clear within a minute, so try again shortly.", name)
		}
		message := fmt.Sprintf("**%s** is frozen.", name)
		if !state.since.IsZero() {
			message = fmt.Sprintf("**%s** is frozen for %s.", name, elapsedLabel(now.Sub(state.since)))
		}
		if window := freezeMaximumText(lock); window != "" {
			message += " A freeze lasts " + window + "."
		}
		return message + " Drawing is unavailable until it is unfrozen."
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

func (f *Feature) renderBoard(s *discordgo.Session, i *discordgo.InteractionCreate, session *drawSession, lock *cs.LoadedLock, buttons bool, note string) error {
	embeds, components := boardView(session, lock, buttons, note)
	msg, err := bot.EditMessageGet(s, i, "", embeds, components)
	if err != nil {
		return err
	}
	if msg != nil {
		session.setMessageID(msg.ID)
	}
	return nil
}

// editBoardMessage updates the board outside the interaction that created it,
// which a reset confirmation needs because its modal submit is a new
// interaction with its own token.
func (f *Feature) editBoardMessage(s *discordgo.Session, session *drawSession, lock *cs.LoadedLock, buttons bool, note string) error {
	channelID, messageID := session.messageIDs()
	if channelID == "" || messageID == "" {
		return errors.New("draw: board message is unknown")
	}

	embeds, components := boardView(session, lock, buttons, note)
	_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Embeds:     &embeds,
		Components: &components,
	})
	return err
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
		b.WriteString(totalsLine(entries, lastCard, remaining, ok))
	}

	return &discordgo.MessageEmbed{
		Title:       s.lockName,
		Description: ellipsize(b.String(), drawDescriptionLimit),
		Color:       cardColor(lastCard),
	}
}

// drawButtons renders the board controls. The ready actions appear only when
// the lock has nothing left to draw, so a running lock cannot be opened or
// reset by accident.
func drawButtons(session *drawSession, lock *cs.LoadedLock) []discordgo.MessageComponent {
	if session == nil {
		return nil
	}

	buttons := make([]discordgo.MessageComponent, 0, 4)
	if lock != nil && lock.IsReady() {
		buttons = append(buttons,
			discordgo.Button{
				CustomID: drawNamespace + ":open:" + session.id,
				Label:    "Open",
				Style:    discordgo.SuccessButton,
			},
			discordgo.Button{
				CustomID: drawNamespace + ":green:" + session.id,
				Label:    "Put green back",
				Style:    discordgo.DangerButton,
				Emoji:    cardComponentEmoji(cs.CardGreen),
			},
			discordgo.Button{
				CustomID: drawNamespace + ":reset:" + session.id,
				Label:    "Reset",
				Style:    discordgo.SecondaryButton,
			},
		)
		if session.keyholderID != "" {
			buttons = append(buttons, discordgo.Button{
				CustomID: drawNamespace + ":decide:" + session.id,
				Label:    "Let keyholder decide",
				Style:    discordgo.PrimaryButton,
			})
		}
	} else {
		buttons = append(buttons,
			discordgo.Button{
				CustomID: drawNamespace + ":next:" + session.id,
				Label:    "Draw",
				Style:    discordgo.PrimaryButton,
			},
			discordgo.Button{
				CustomID: drawNamespace + ":stop:" + session.id,
				Label:    "Stop",
				Style:    discordgo.SecondaryButton,
			},
		)
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: buttons},
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

// totalsLine summarises the session. Yellows are shown as their net effect on
// the red count, because a count of yellows says nothing about how the deck
// changed. Hidden and random yellows have no visible effect, so they are left
// out rather than guessed at.
func totalsLine(entries []drawEntry, lastCard cs.Card, remaining int, hasRemaining bool) string {
	reds, greens, doubles, yellowDelta := 0, 0, 0, 0
	for _, entry := range entries {
		switch entry.card {
		case cs.CardRed:
			reds++
		case cs.CardGreen:
			greens++
		case cs.CardDoubleUp:
			doubles++
		case cs.CardYellowP1:
			yellowDelta++
		case cs.CardYellowP2:
			yellowDelta += 2
		case cs.CardYellowP3:
			yellowDelta += 3
		case cs.CardYellowM1:
			yellowDelta--
		case cs.CardYellowM2:
			yellowDelta -= 2
		}
	}

	parts := []string{
		fmt.Sprintf("%s %s red", cardEmojiMarkup(cs.CardRed), signedRemoval(reds)),
		fmt.Sprintf("%s %s green", cardEmojiMarkup(cs.CardGreen), signedRemoval(greens)),
	}
	if yellowDelta != 0 {
		parts = append(parts, fmt.Sprintf("%s %+d red", cardEmojiMarkup(cs.CardYellow), yellowDelta))
	}
	if doubles > 0 {
		parts = append(parts, fmt.Sprintf("%s %d double up", cardEmojiMarkup(cs.CardDoubleUp), doubles))
	}
	if markup := cardEmojiMarkup(lastCard); markup != "" {
		parts = append(parts, fmt.Sprintf("last card was %s", markup))
	}
	if hasRemaining {
		parts = append(parts, fmt.Sprintf("%d remaining", remaining))
	}
	return "Summary: " + strings.Join(parts, ", ")
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

// boardView builds the embed and components for one board state. Every board
// update goes through it, whether it answers an interaction or edits the
// message outside one.
func boardView(session *drawSession, lock *cs.LoadedLock, buttons bool, note string) ([]*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	components := []discordgo.MessageComponent{}
	if buttons {
		components = drawButtons(session, lock)
	}
	return []*discordgo.MessageEmbed{session.embed(lock, note)}, components
}

// boardClient returns a client for the board owner. It reports false when the
// board cannot continue, with the note to render.
func (f *Feature) boardClient(ctx context.Context, session *drawSession) (*cs.Client, string, bool) {
	client, _, err := f.clientFor(ctx, session.ownerID)
	if err != nil {
		f.deleteSession(session.id)
		return nil, f.apiFailure(ctx, session.ownerID, err), false
	}
	return client, "", true
}

// boardLock finds the board's lock in a fresh lock list. A lock that is gone
// ends the board, so every action reports it the same way.
func (f *Feature) boardLock(locks []cs.LoadedLock, session *drawSession) (*cs.LoadedLock, string, bool) {
	lock := findLockByID(locks, session.lockID)
	if lock == nil {
		f.deleteSession(session.id)
		return nil, "That lock is no longer active.", false
	}
	session.setLock(lock)
	return lock, "", true
}

// boardKeepsOpen reports whether the board still has something to offer: a
// ready lock keeps its ready actions, and a lock with draws left keeps drawing.
func boardKeepsOpen(lock *cs.LoadedLock) bool {
	if lock == nil {
		return false
	}
	return lock.IsReady() || (lock.DrawsAvailable() > 0 && !lock.IsFrozen())
}

// openBoardNote explains where an opened lock's code went.
func openBoardNote(i *discordgo.InteractionCreate) string {
	if i.GuildID == "" {
		return "Opened. The code is below."
	}
	return "Opened. The code was sent privately."
}

// replyBoardCode delivers the code for an opened board lock. ADR 0004: a guild
// board is public, so the code is a private follow-up; a DM or group DM is
// already limited to the member and keyholder, so it is a normal message,
// matching /lock open.
func replyBoardCode(s *discordgo.Session, i *discordgo.InteractionCreate, lock *cs.LoadedLock, code string) error {
	return bot.ReplyFollowup(s, i, openResultText(lock, code), i.GuildID != "")
}

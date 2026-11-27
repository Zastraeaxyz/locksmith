package feature

// The deck module renders a card lock's known deck composition. Cards that have
// been drawn are revealed and show their face; cards still in a hidden deck are
// unknown, so they show the blue :blank: card back.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

// deckRowCards is the number of icons on one row. A row stays well inside
// Discord's message limit, so chunking can split between rows.
const deckRowCards = 10

// deckCardOrder is the presentation order for known deck cards. It follows
// ChastiSafe's deck summary so a deck reads the same way as the app.
var deckCardOrder = []cs.Card{
	cs.CardGreen, cs.CardRed,
	cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3,
	cs.CardYellowM1, cs.CardYellowM2, cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN,
	cs.CardFreeze, cs.CardArctic, cs.CardStun, cs.CardSticky,
	cs.CardDoubleUp, cs.CardReset, cs.CardDice, cs.CardPink, cs.CardGoAgain,
}

// deckView is the known composition of one deck: revealed cards in draw order,
// then the cards still in the deck. Masked count shows how many of the
// remaining cards rendered as card backs because their faces are unknown.
type deckView struct {
	icons     []string
	revealed  int
	remaining int
	masked    bool
}

// handleLockDeck shows the known composition of one of the caller's card locks.
func (f *Feature) handleLockDeck(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	userID := bot.UserID(i)
	ref, _ := stringOption(opts, "ref")
	public := boolOption(opts, "public")

	if err := bot.Defer(s, i, !public); err != nil {
		return err
	}

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}
	locks, err := client.Loaded(ctx)
	if err != nil {
		return bot.EditChunks(s, i, f.apiFailure(ctx, userID, err), !public)
	}
	locks, _ = f.freshLocks(ctx, client, locks, f.now())

	target, note, err := deckTarget(locks, cred.ChastiSafeID, ref)
	if err != nil {
		return bot.EditChunks(s, i, err.Error(), !public)
	}
	if target == nil {
		return bot.EditChunks(s, i, "You have no active card lock to show.", !public)
	}

	revealed, err := f.deckRevealed(ctx, client, target)
	if err != nil {
		f.log.Warn("loading deck draws", "user_id", userID, "lock_id", target.ID.String(), "err", err)
		revealed = target.Draws.Cards()
	}
	return bot.EditChunks(s, i, deckText(target, note, revealed), !public)
}

// deckRevealed collects every draw since the deck last reset, oldest first.
// The loaded lock's Draws field only keeps a recent window, so the log is the
// source for the full revealed composition.
func (f *Feature) deckRevealed(ctx context.Context, client *cs.Client, lock *cs.LoadedLock) ([]cs.Card, error) {
	view, err := f.fetchLog(ctx, client, lock.ID, true)
	if err != nil {
		return nil, err
	}
	return revealedDraws(view.entries), nil
}

// revealedDraws returns the draw cards since the newest reset, oldest first.
// Log entries arrive newest first.
func revealedDraws(entries []cs.LogEntry) []cs.Card {
	start := len(entries)
	for idx, entry := range entries {
		if isDeckReset(entry.EventType) {
			start = idx
			break
		}
	}

	out := make([]cs.Card, 0, start)
	for idx := start - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if entry.EventType != 6 {
			continue
		}
		if card, ok := cardForLetter(entry.Card); ok {
			out = append(out, card)
		}
	}
	return out
}

// isDeckReset reports whether a log event starts a fresh deck, so older draws
// are not part of the current composition.
func isDeckReset(eventType int) bool {
	switch eventType {
	case 7, 9, 10, 13, 39:
		return true
	}
	return false
}

// deckTarget resolves which card lock a deck request means. Without a ref it
// prefers a lock that can be drawn now, then the newest card lock so a frozen
// or ready deck still has a view. A ref matches only the caller's active locks.
func deckTarget(locks []cs.LoadedLock, chastisafeID, ref string) (*cs.LoadedLock, string, error) {
	own := ownLocks(locks, chastisafeID)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		if lock := drawableLock(own, chastisafeID); lock != nil {
			return lock, "", nil
		}
		return drawRefusalTarget(own, chastisafeID), "", nil
	}

	resolved, note, err := resolveRef(ref, own, nil)
	if err != nil {
		return nil, "", err
	}
	if resolved.Running == nil {
		return nil, "", fmt.Errorf("`%s` is not a running lock.", ref)
	}
	if !resolved.Running.IsCardLock() {
		return nil, "", fmt.Errorf("**%s** is not a card lock, so it has no deck.", lockName(resolved.Running))
	}
	return resolved.Running, note, nil
}

// deckText renders the lock header, the card grid, and the composition counts.
func deckText(lock *cs.LoadedLock, note string, revealed []cs.Card) string {
	if lock == nil {
		return "No lock selected."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (ID `%s`)\n", lockName(lock), lock.ID)
	if note != "" {
		b.WriteString(note)
		b.WriteString("\n")
	}

	view := buildDeckView(lock, revealed)
	if len(view.icons) == 0 {
		if lock.Hidden {
			b.WriteString("The deck is hidden.")
		} else {
			b.WriteString("The deck is empty.")
		}
		if lock.IsReady() {
			b.WriteString(" The lock is ready to unlock.")
		}
		return b.String()
	}

	for idx, icon := range view.icons {
		switch {
		case idx == 0:
		case idx%deckRowCards == 0:
			b.WriteString("\n")
		default:
			b.WriteString(" ")
		}
		b.WriteString(icon)
	}

	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%d card%s · %d revealed · %d left",
		len(view.icons), plural(len(view.icons)), view.revealed, view.remaining)
	if view.masked && view.remaining > 0 {
		b.WriteString(" · " + deckCardBack() + " = in deck")
	}
	return b.String()
}

// buildDeckView collects the known deck composition. Revealed cards are the
// draws since the last reset, oldest first, so they show their faces. A visible
// deck also shows the faces of its remaining cards. A hidden deck shows its
// remaining cards as card backs, because their faces are unknown until drawn.
func buildDeckView(lock *cs.LoadedLock, revealed []cs.Card) deckView {
	var view deckView
	if lock == nil {
		return view
	}
	back := deckCardBack()

	for _, card := range revealed {
		view.icons = append(view.icons, deckCardIcon(card, back))
		view.revealed++
	}

	if lock.Hidden {
		view.remaining, view.masked = deckRemainingCount(lock)
		view.icons = append(view.icons, repeatIcon(back, view.remaining)...)
		return view
	}

	if lock.Deck != nil {
		remaining, masked := deckMapIcons(lock.Deck, back)
		view.icons = append(view.icons, remaining...)
		view.remaining = len(remaining)
		view.masked = masked
		return view
	}

	view.remaining, view.masked = deckRemainingCount(lock)
	view.icons = append(view.icons, repeatIcon(back, view.remaining)...)
	return view
}

// deckRemainingCount returns the number of cards left and whether their faces
// should be shown as card backs.
func deckRemainingCount(lock *cs.LoadedLock) (int, bool) {
	if count, ok := cardsRemaining(lock); ok {
		if count == 0 {
			if fallback := lock.CardsRemaining(); fallback > 0 {
				count = fallback
			}
		}
		return count, true
	}
	return 0, false
}

// deckMapIcons expands a visible deck into one face icon per card and reports
// whether any card had no face to show.
func deckMapIcons(deck cs.Deck, back string) ([]string, bool) {
	icons := make([]string, 0, deck.Total())
	masked := false
	icon := func(card cs.Card) string {
		if markup := cardEmojiMarkup(card); markup != "" {
			return markup
		}
		masked = true
		return back
	}

	seen := make(map[cs.Card]bool, len(deck))
	for _, card := range deckCardOrder {
		seen[card] = true
		icons = append(icons, repeatIcon(icon(card), deck.Count(card))...)
	}

	extra := make([]cs.Card, 0, len(deck))
	for card, count := range deck {
		if seen[card] || count <= 0 {
			continue
		}
		extra = append(extra, card)
	}
	sort.Slice(extra, func(a, b int) bool { return string(extra[a]) < string(extra[b]) })
	for _, card := range extra {
		icons = append(icons, repeatIcon(icon(card), deck.Count(card))...)
	}
	return icons, masked
}

// deckCardIcon returns a card's face, or the card back when it has no emoji.
func deckCardIcon(card cs.Card, back string) string {
	if markup := cardEmojiMarkup(card); markup != "" {
		return markup
	}
	return back
}

// deckCardBack returns the :blank: card back. Startup fails when it is missing,
// so the plain fallback only appears in a degraded test or development run.
func deckCardBack() string {
	if markup := emojiMarkup(applicationEmojiBlank); markup != "" {
		return markup
	}
	return ":blank:"
}

// repeatIcon returns count copies of an icon.
func repeatIcon(icon string, count int) []string {
	if count <= 0 || icon == "" {
		return nil
	}
	out := make([]string, count)
	for idx := range out {
		out[idx] = icon
	}
	return out
}

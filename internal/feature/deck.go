package feature

// The deck module renders a card lock's remaining deck as a grid of card
// emoji. A revealed card shows its face; a hidden or unknown card shows the
// :blank: card back.

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

// deckCardOrder is the presentation order for a revealed deck. It follows
// ChastiSafe's deck summary so a deck reads the same way as the app.
var deckCardOrder = []cs.Card{
	cs.CardGreen, cs.CardRed,
	cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3,
	cs.CardYellowM1, cs.CardYellowM2, cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN,
	cs.CardFreeze, cs.CardArctic, cs.CardStun, cs.CardSticky,
	cs.CardDoubleUp, cs.CardReset, cs.CardDice, cs.CardPink, cs.CardGoAgain,
}

// handleLockDeck shows the cards left in one of the caller's card locks.
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

	target, note, err := deckTarget(locks, cred.ChastiSafeID, ref)
	if err != nil {
		return bot.EditChunks(s, i, err.Error(), !public)
	}
	if target == nil {
		return bot.EditChunks(s, i, "You have no active card lock to show.", !public)
	}
	return bot.EditChunks(s, i, deckText(target, note), !public)
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

// deckText renders the lock header, the card grid, and the remaining count.
func deckText(lock *cs.LoadedLock, note string) string {
	if lock == nil {
		return "No lock selected."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (ID `%s`)\n", lockName(lock), lock.ID)
	if note != "" {
		b.WriteString(note)
		b.WriteString("\n")
	}

	icons, hidden := deckIcons(lock)
	if len(icons) == 0 {
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

	for idx, icon := range icons {
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
	fmt.Fprintf(&b, "%d card%s left", len(icons), plural(len(icons)))
	if hidden {
		b.WriteString(" · faces hidden")
	}
	return b.String()
}

// deckIcons expands the remaining deck into one icon per card. A hidden lock
// returns card backs for every card so the view never reveals a face. A nil
// deck with a card count also falls back to card backs, because the faces are
// unknown. A card value with no emoji mapping uses the card back too.
func deckIcons(lock *cs.LoadedLock) (icons []string, hidden bool) {
	if lock == nil {
		return nil, false
	}
	back := deckCardBack()
	if lock.Hidden {
		count, _ := cardsRemaining(lock)
		if count == 0 {
			count = lock.CardsRemaining()
		}
		return repeatIcon(back, count), true
	}
	if lock.Deck == nil {
		if count, ok := cardsRemaining(lock); ok {
			return repeatIcon(back, count), true
		}
		return nil, false
	}

	icons = make([]string, 0, lock.Deck.Total())
	seen := make(map[cs.Card]bool, len(lock.Deck))
	for _, card := range deckCardOrder {
		seen[card] = true
		icons = append(icons, repeatIcon(deckCardIcon(card, back), lock.Deck.Count(card))...)
	}

	extra := make([]cs.Card, 0, len(lock.Deck))
	for card, count := range lock.Deck {
		if seen[card] || count <= 0 {
			continue
		}
		extra = append(extra, card)
	}
	sort.Slice(extra, func(a, b int) bool { return string(extra[a]) < string(extra[b]) })
	for _, card := range extra {
		icons = append(icons, repeatIcon(deckCardIcon(card, back), lock.Deck.Count(card))...)
	}
	return icons, false
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

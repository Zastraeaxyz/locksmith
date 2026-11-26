package feature

// The card presentation module renders a ChastiSafe card for Discord: its
// application emoji, its colour, and the label the draw board uses.
//
// Lock-log event text stays with the ChastiSafe adapter (cs.LogEntryText),
// because the API model owns that copy. Emoji and colour are shared, so a
// surface that shows a card looks the same everywhere.

import (
	"strings"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

// cardEmojiNames are application emoji, so they render in DMs and in any guild
// the app is used in, without extra permissions. The IDs are resolved from the
// live application emoji list on READY, so only the names are stored here.
var cardEmojiNames = map[cs.Card]string{
	cs.CardGreen:    "green",
	cs.CardRed:      "red",
	cs.CardYellowP1: "yellow_plus1",
	cs.CardYellowP2: "yellow_plus2",
	cs.CardYellowP3: "yellow_plus3",
	cs.CardYellowM1: "yellow_minus1",
	cs.CardYellowM2: "yellow_minus2",
	cs.CardYellow:   "yellow_unknown",
	cs.CardYellowUK: "yellow_unknown",
	cs.CardYellowRN: "yellow_random",
	cs.CardFreeze:   "freeze",
	cs.CardArctic:   "arctic",
	cs.CardStun:     "stun",
	cs.CardSticky:   "sticky",
	cs.CardReset:    "reset",
	cs.CardPink:     "pink",
	cs.CardGoAgain:  "go_again",
	cs.CardDoubleUp: "double",
	cs.CardDice:     "dice",
}

// cardComponentEmoji returns a button emoji for a card, or nil when the card
// has none.
func cardComponentEmoji(card cs.Card) *discordgo.ComponentEmoji {
	name, ok := cardEmojiNames[card]
	if !ok {
		return nil
	}
	id, ok := applicationEmojiID(name)
	if !ok {
		return nil
	}
	return &discordgo.ComponentEmoji{Name: name, ID: id}
}

// cardEmojiMarkup returns the inline custom emoji for a card, or "" when the
// card has none.
func cardEmojiMarkup(card cs.Card) string {
	name, ok := cardEmojiNames[card]
	if !ok {
		return ""
	}
	return emojiMarkup(name)
}

// writeCardEmoji writes a card's emoji with a trailing space, if it has one.
func writeCardEmoji(b *strings.Builder, card cs.Card) {
	if markup := cardEmojiMarkup(card); markup != "" {
		b.WriteString(markup)
		b.WriteString(" ")
	}
}

// cardLogLabel names a card in the draw log.
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

// cardColor is the embed colour that matches a card.
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

// clip cuts text to a rune limit without adding anything. Discord enforces the
// limit, so a hard cut is right for a value the member may copy back.
func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// ellipsize cuts text to a rune limit and marks that it was shortened.
func ellipsize(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

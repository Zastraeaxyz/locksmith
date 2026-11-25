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

// cardComponentEmoji returns a button emoji for a card, or nil when the card
// has none.
func cardComponentEmoji(card cs.Card) *discordgo.ComponentEmoji {
	emoji, ok := cardEmojiIDs[card]
	if !ok {
		return nil
	}
	return &discordgo.ComponentEmoji{Name: emoji.name, ID: emoji.id}
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

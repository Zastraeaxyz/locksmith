package feature

// The documentation module renders the /docs pages. The pages are static, so a
// page needs no ChastiSafe call and works before a member logs in.
//
// Card copy follows the ChastiSafe web app help: green cards are progress, red
// cards make the lockee wait out the regularity time, and the special cards
// change the deck, freeze draws, or do whatever the keyholder scripted. Keep
// that source in mind when editing a card line; the short labels in cards.go
// are not enough on their own.

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const (
	docsNamespace = "docs"
	chastisafeURL = "https://chastisafe.com"
)

// docsPage is one topic in /docs.
type docsPage struct {
	// Key is the command choice value and part of the button custom ID.
	Key string
	// Title is the embed title, the index button label, and the choice name.
	Title string
	// Blurb is the one-line summary on the index.
	Blurb string
	// Description is the embed body.
	Description string
	// Fields builds grouped content when the page is rendered. The card page
	// needs this because application emoji IDs are resolved at runtime.
	Fields func() []*discordgo.MessageEmbedField
}

// docsPages is the ordered list shown on the index and in the page buttons.
var docsPages = []docsPage{
	{
		Key:   "start",
		Title: "Getting started",
		Blurb: "Install Locksmith, log in, and load your first lock.",
		Description: docText(
			"**1. Add Locksmith to your account**",
			"Use the **Install to your account** button below. A user install lets the same commands work in DMs, group DMs, and servers that do not have Locksmith. A server install adds the bot to that server.",
			"",
			"**2. Log in once**",
			"Run `/login` and paste your ChastiSafe developer code. It is stored encrypted for your account and is never shown to another member. `/logout` removes it.",
			"",
			"**3. Load a lock**",
			"Open a share link from a keyholder, or run `/lock load link:<share link>`. Check the private preview, press **Load lock**, then set your device to the code Locksmith replies with and forget it. Locksmith generates the code, so you cannot choose one you would remember.",
			"If you do not have Locksmith yet, the preview offers an install button instead.",
			"",
			"**4. See where you stand**",
			"`/lock status` lists your active locks, cards left, greens needed, draws available, timers, and the next check-in.",
			"",
			"**5. Open it**",
			"When a card lock has enough greens, or a timer reaches zero, run `/lock open` to see the code. In a server the reply is private. In a DM it is a normal message.",
		),
	},
	{
		Key:   "locks",
		Title: "Locks and templates",
		Blurb: "Share, preview, load, and open a lock.",
		Description: docText(
			"**Templates and locks**",
			"A template is the lock definition you create on chastisafe.com. A lock is one loaded copy of a template on your account. Editing a template does not change a lock that is already running.",
			"",
			"**Keyholders**",
			"Create the template on chastisafe.com, then run `/lock share ref:<template>`. The post shows the lock name, type, cards or timer, greens needed, draw interval, check-ins, allowed purposes, and whether a passcode is required.",
			"",
			"**Lockees**",
			"Press **Load with Locksmith** on a share post, or run `/lock load link:<share link>`. Locksmith shows a private preview with **Load lock**, **Simulation** for card locks, and **Open in ChastiSafe**. Nothing starts until **Load lock** is pressed.",
			"If the template uses a keyholder passcode, the form asks for it. Locksmith then generates the device code from your ChastiSafe code settings and shows it once, privately.",
			"",
			"**Lock codes**",
			"Locksmith never shows a keyholder's passcode. The device code is generated at load time and is hidden again while the lock runs. `/lock open` shows it when the lock is ready. A private `/history` reply also keeps completed codes; a public reply never includes them.",
			"",
			"**Purposes**",
			"A purpose labels a lock: chastity, test, bondage, task, loyalty, or emergency. `TEST` locks are useful for trying a setup and stay out of history by default.",
			"",
			"**Keyholder decisions**",
			"When the lock is ready, the draw board has **Let keyholder decide**. A self lock does not show that button, because there is no one else to ask.",
		),
	},
	{
		Key:   "drawing",
		Title: "Drawing and card locks",
		Blurb: "How card locks work and what every draw board button does.",
		Description: docText(
			"**The goal**",
			"A card lock opens when you have drawn enough green cards. The template sets the number: 1, 2 to 100, or all greens. A deck can hold 1 to 200 greens.",
			"",
			"**Draws and regularity**",
			"The template sets a draw interval. Each draw waits for that interval. A red card sends you back to waiting. With cumulative picks, unused waits stack, so several draws can be available at once. Without cumulative picks, you never have more than one draw waiting.",
			"",
			"**Starting the board**",
			"Run `/lock draw`. The board is public so a keyholder or the channel can watch. It logs cards, totals, and how many draws are left.",
			"Add `auto:true` to draw auto. The board takes every draw the lock allows in a row and updates after each card, including extra draws from greens, stickies, doubles, and go-again cards. It stops when the lock is ready, freezes, or runs out.",
			"",
			"**While the lock runs**",
			"• **Draw** picks the next card.",
			"• **Stop** closes the board. The lock keeps running.",
			"",
			"**Viewing the deck**",
			"Run `/lock deck` to see the deck composition. Drawn cards show their revealed faces, and cards still in a hidden deck show card backs. `ref:` picks another active card lock, and `public:true` posts the grid in the channel.",
			"",
			"**Progress and estimates**",
			"`/lock progress` gives one line per UTC day: green progress, red net progress, the red/yellow pool change, and the total deck change. Greens and reds are progress, so a positive red number means reds were cleared. Pool and deck are changes, so a negative pool or deck number means fewer cards. `days:N` limits the report, `ref:` picks an active card lock, and `verbose:true` adds per-card draws, yellow details, and the double up math.",
			"A visible deck is walked back from the live composition, so every day is exact. A hidden deck uses stored deck counts. Locksmith records the current count when it sees a lock, which supplies the before and after numbers around a double up. Without those numbers, the affected pool is marked unknown rather than given a range. Hidden card faces are never read.",
			"",
			"**When it is ready**",
			"• **Open** shows the code privately in a server.",
			"• **Put green back** returns the last green to the deck, so drawing continues.",
			"• **Reset** asks for typed confirmation and restarts the card deck.",
			"• **Let keyholder decide** sends the decision to the keyholder.",
			"",
			"Only the lockee and keyholder can press board buttons. Anyone else gets a private refusal.",
			"",
			"**Frozen locks**",
			"While frozen, draws do not accumulate. A freeze card holds for 2 to 4 times the draw interval and hides the exact time. `/lock status` and the board show how long the lock has been frozen and the maximum freeze, 4x the interval. A keyholder freeze lasts until the keyholder unfreezes the lock. A card freeze only counts while no draw is available: ChastiSafe can leave its frozen flag set after the freeze ends, so an available pick means the lock can be drawn. Locksmith reads the frozen state from ChastiSafe every time, so a freeze that has just ended clears on the next check. Reminders stay on through a freeze: Locksmith keeps watching and DMs you when draws resume.",
			"",
			"**Simulation**",
			"Card templates have a **Simulation** button on share posts and previews. It shows ChastiSafe's estimate: average duration, average cards, ranges, draw interval, and how many runs were used. If the keyholder turned simulation off, the button explains that instead.",
		),
	},
	{
		Key:   "cards",
		Title: "Cards and effects",
		Blurb: "Every card, its emoji, and what it does.",
		Description: docText(
			"**Every card in a card lock**",
			"A card lock draws from a deck. Greens are your progress; most other cards change the deck or make you wait. The deck does not show which yellow card is next.",
		),
		Fields: cardDocsFields,
	},
	{
		Key:   "checkins",
		Title: "Check-ins and reminders",
		Blurb: "Check-in windows, reminder DMs, and notifications.",
		Description: docText(
			"**Check-ins**",
			"A lock can require a check-in at a regular interval. The template sets the period; ChastiSafe's tolerance is the late window after the due moment. Missing one is visible to your keyholder, who decides what happens.",
			"",
			"**Reminders**",
			"When a lock is watched, Locksmith DMs you:",
			"• a first reminder before the check-in is due,",
			"• a final reminder 5 minutes before,",
			"• and one more when it is due.",
			"The **Check-in** button appears once the check-in is due and stays through the overdue period. It disappears after it succeeds.",
			"",
			"**Checking in by hand**",
			"Run `/lock checkin`. `ref` suggests locks that need a check-in. If the check-in is not due yet, Locksmith tells you when it is instead of calling ChastiSafe too early.",
			"",
			"**What is watched**",
			"`/lock load` subscribes you automatically. A lock loaded in the ChastiSafe app is picked up when Locksmith next starts, or immediately with `/lock notify`, which watches the newest active card lock, even a frozen one, or the next check-in lock. A member watches one lock at a time; a new subscription replaces the old one.",
			"",
			"**Freezes do not stop reminders**",
			"A freeze pauses draws, not notifications. Locksmith keeps checking a frozen lock and announces the first draw after it ends. At 4x the draw interval, the longest a freeze card holds, it checks once more a minute later instead of assuming the freeze overran. There is nothing to run and nothing to turn back on.",
			"",
			"**Notification DMs are temporary**",
			"A newer draw or check-in notification replaces the old DM, and anything older than a day is deleted. `/lock notify enabled:false`, `/logout`, or a new subscription clears the rest.",
		),
	},
	{
		Key:   "history",
		Title: "History and logs",
		Blurb: "Completed locks, draw logs, and ratings.",
		Description: docText(
			"**History**",
			"`/history` lists your completed locks, newest first, with duration, unlock reason, keyholder, rating, and the code when the reply is private.",
			"`TEST` locks and locks older than a year are hidden by default. Add `include_test:true` or `include_old:true` to bring them back. Add `public:true` to post the reply without codes.",
			"",
			"**Lock log**",
			"`/lock log` shows draws from the last five minutes, and always at least the last ten. Older picks appear under **Earlier draws**. Draws within a minute of each other share one line of card icons, so a run reads compactly.",
			"• `full:true` shows every event.",
			"• `ref:` picks a running or previous lock.",
			"• `public:true` posts the reply in the channel.",
			"A hygiene unlock's code is redacted in a public log, because a log reply can be permanent.",
			"",
			"**Ratings**",
			"`/lock rate` rates your most recently completed lock.",
		),
	},
	{
		Key:   "documents",
		Title: "Profiles, rules, and kinks",
		Blurb: "Your markdown documents and who can see them.",
		Description: docText(
			"**Your documents**",
			"Locksmith keeps three plain markdown documents per member, separate from ChastiSafe:",
			"• `/profile view [user]` and `/profile edit` for your profile.",
			"• `/rules view [user]` and `/rules edit` for your rules of play.",
			"• `/kinks view [user]` and `/kinks edit` for your kinks.",
			"",
			"**Viewing**",
			"View posts the document publicly in an embed. `user` accepts a Discord user ID, a ChastiSafe username such as `zastraea` or `zastraea#0`, or the Discord handle saved when the document was last edited.",
			"",
			"**Editing**",
			"Edit opens a private modal prefilled with your current markdown. Submitting an empty modal clears the document. Documents are capped at 4000 characters, Discord's modal limit.",
			"",
			"Mentions in a document are suppressed, so `@everyone` cannot ping through a profile.",
		),
	},
}

// docText joins lines with newlines. The page bodies read better as a list of
// lines than as one escaped string.
func docText(lines ...string) string {
	return strings.Join(lines, "\n")
}

// cardDocsFields groups the card list so the page stays readable and every
// field stays inside Discord's 1024 character field limit.
func cardDocsFields() []*discordgo.MessageEmbedField {
	return []*discordgo.MessageEmbedField{
		{
			Name: "Progress",
			Value: docText(
				docCard(cs.CardGreen, "Counts toward the greens you need. When you reach the required number, the lock is ready and `/lock open` shows the code."),
				"",
				docCard(cs.CardRed, "You must wait out the lock's regularity time before the next draw. With cumulative picks, several waits can stack."),
			),
		},
		{
			Name: "Yellow cards",
			Value: docText(
				docCard(cs.CardYellowP1, "Adds a red to the deck."),
				docCard(cs.CardYellowP2, "Adds 2 reds to the deck."),
				docCard(cs.CardYellowP3, "Adds 3 reds to the deck."),
				docCard(cs.CardYellowM1, "Removes a red from the deck."),
				docCard(cs.CardYellowM2, "Removes 2 reds from the deck."),
				"",
				cardEmojiMarkup(cs.CardYellow)+" **Yellow +?**: A random or hidden yellow. Its exact effect is not visible until it is drawn.",
				"",
				"The deck does not reveal which yellows remain.",
			),
		},
		{
			Name: "Deck changers",
			Value: docText(
				docCard(cs.CardSticky, "Acts like a red, but returns to the deck, so it can be drawn again."),
				docCard(cs.CardReset, "Rebuilds the deck from the original card ranges. Reset cards stay, and the total locked time does not reset."),
				docCard(cs.CardDoubleUp, "Doubles reds and yellows in the deck. Other card types are not doubled."),
				docCard(cs.CardGoAgain, "Does nothing and lets you draw again. It is added when a lock hides its cards, to disguise the deck size."),
			),
		},
		{
			Name: "Freezes and pauses",
			Value: docText(
				docCard(cs.CardFreeze, "Blocks draws for 2 to 4 times the draw interval. The exact time is hidden."),
				docCard(cs.CardArctic, "A freeze that goes back into the deck, so it can freeze you again."),
				docCard(cs.CardStun, "A freeze that also wipes stacked draws. Draws start accumulating again after your first draw once it ends."),
				docCard(cs.CardDice, "Rolls the dice the template chose and records the result. A keyholder lock freezes until the keyholder unfreezes it; a self lock freezes like a normal freeze."),
			),
		},
		{
			Name: "Wildcard",
			Value: docText(
				docCard(cs.CardPink, "The joker. The keyholder writes a PINK script, so it can do almost anything. The effect runs in ChastiSafe, not in Locksmith."),
			),
		},
	}
}

// docCard renders one card line with its application emoji and log label. An
// unresolved emoji is left out rather than starting the line with a space.
func docCard(card cs.Card, effect string) string {
	emoji := cardEmojiMarkup(card)
	if emoji != "" {
		emoji += " "
	}
	return emoji + "**" + cardLogLabel(card) + "**: " + effect
}

// docsCommand opens the documentation. The page option is generated from the
// registry, so a new page appears in both the index and the command.
func docsCommand() *discordgo.ApplicationCommand {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(docsPages))
	for _, page := range docsPages {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  page.Title,
			Value: page.Key,
		})
	}
	return &discordgo.ApplicationCommand{
		Name:        "docs",
		Description: "Read the Locksmith documentation",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "page",
				Description: "Open a specific page",
				Required:    false,
				Choices:     choices,
			},
		},
	}
}

// handleDocsCommand posts the index or a requested page privately, so reading
// the docs does not fill the channel. Page buttons edit the ephemeral reply.
func (f *Feature) handleDocsCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	key := docsIndexKey
	if page, ok := stringOption(i.ApplicationCommandData().Options, "page"); ok {
		key = page
	}
	return bot.ReplyEmbed(s, i,
		[]*discordgo.MessageEmbed{docsEmbed(key)},
		docsComponents(key, sessionClientID(s)),
		true,
	)
}

// docsIndexKey is the pseudo page for the index.
const docsIndexKey = "index"

// handleDocsComponent moves an open docs message to another page in place.
func (f *Feature) handleDocsComponent(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	key, ok := docsCustomIDKey(i.MessageComponentData().CustomID)
	if !ok {
		return bot.ReplyEphemeral(s, i, "This page is out of date. Run `/docs` again.")
	}

	if err := bot.DeferUpdate(s, i); err != nil {
		return err
	}
	return bot.EditMessage(s, i, "",
		[]*discordgo.MessageEmbed{docsEmbed(key)},
		docsComponents(key, sessionClientID(s)),
	)
}

// docsCustomIDKey validates a docs button and returns its page key.
func docsCustomIDKey(customID string) (string, bool) {
	if customID == docsNamespace+":"+docsIndexKey {
		return docsIndexKey, true
	}
	key := strings.TrimPrefix(customID, docsNamespace+":page:")
	if key == customID || key == "" {
		return "", false
	}
	if _, ok := docsPageByKey(key); !ok {
		return "", false
	}
	return key, true
}

// docsPageByKey finds a page by its command value.
func docsPageByKey(key string) (*docsPage, bool) {
	key = strings.TrimSpace(key)
	for idx := range docsPages {
		if docsPages[idx].Key == key {
			return &docsPages[idx], true
		}
	}
	return nil, false
}

// docsEmbed renders the index or one page.
func docsEmbed(key string) *discordgo.MessageEmbed {
	if key == docsIndexKey {
		return docsIndexEmbed()
	}
	page, ok := docsPageByKey(key)
	if !ok {
		return docsIndexEmbed()
	}

	var fields []*discordgo.MessageEmbedField
	if page.Fields != nil {
		fields = page.Fields()
	}

	footer := fmt.Sprintf("Locksmith docs · Page %d of %d", docsPageNumber(page.Key), len(docsPages))
	if page.Key == "cards" {
		footer += " · Card effects follow the ChastiSafe app help"
	}
	return &discordgo.MessageEmbed{
		Title:       page.Title,
		URL:         repoURL,
		Color:       embedColor,
		Description: page.Description,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: footer},
	}
}

// docsIndexEmbed lists every page with a one-line summary.
func docsIndexEmbed() *discordgo.MessageEmbed {
	var b strings.Builder
	b.WriteString("Your ChastiSafe locks, inside Discord.\n\n")
	b.WriteString("These pages explain what Locksmith does, how a lock moves from template to open, and what every card does. Pick a page below.\n\n")
	for idx, page := range docsPages {
		if idx > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "**%s** · %s", page.Title, page.Blurb)
	}
	b.WriteString("\n\n")
	b.WriteString("-# Every topic page has a **Back to docs** button and links at the bottom.")

	return &discordgo.MessageEmbed{
		Title:       "Locksmith docs",
		URL:         repoURL,
		Color:       embedColor,
		Description: b.String(),
		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Locksmith docs · %d pages", len(docsPages))},
	}
}

// docsPageNumber returns the one-based position of a page.
func docsPageNumber(key string) int {
	for idx := range docsPages {
		if docsPages[idx].Key == key {
			return idx + 1
		}
	}
	return 0
}

// docsComponents renders the navigation and the links under a page.
func docsComponents(key string, clientID string) []discordgo.MessageComponent {
	rows := make([]discordgo.MessageComponent, 0, 3)
	if key == docsIndexKey {
		rows = append(rows, docsPageRows()...)
	} else {
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Back to docs",
				Style:    discordgo.SecondaryButton,
				CustomID: docsNamespace + ":" + docsIndexKey,
			},
		}})
	}
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label: "Install to your account",
			Style: discordgo.LinkButton,
			URL:   userInstallURL(clientID),
		},
		discordgo.Button{
			Label: "ChastiSafe",
			Style: discordgo.LinkButton,
			URL:   chastisafeURL,
		},
		discordgo.Button{
			Label: "Repository",
			Style: discordgo.LinkButton,
			URL:   repoURL,
		},
	}})
	return rows
}

// docsPageRows packs the page buttons into rows of five.
func docsPageRows() []discordgo.MessageComponent {
	const perRow = 5
	rows := make([]discordgo.MessageComponent, 0, (len(docsPages)+perRow-1)/perRow)
	for start := 0; start < len(docsPages); start += perRow {
		end := min(start+perRow, len(docsPages))
		buttons := make([]discordgo.MessageComponent, 0, end-start)
		for _, page := range docsPages[start:end] {
			buttons = append(buttons, discordgo.Button{
				Label:    page.Title,
				Style:    discordgo.SecondaryButton,
				CustomID: docsNamespace + ":page:" + page.Key,
			})
		}
		rows = append(rows, discordgo.ActionsRow{Components: buttons})
	}
	return rows
}

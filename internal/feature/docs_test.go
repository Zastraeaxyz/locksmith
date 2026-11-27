package feature

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestDocsCommand(t *testing.T) {
	cmd := docsCommand()
	if cmd.Name != "docs" {
		t.Fatalf("name = %q", cmd.Name)
	}
	if strings.TrimSpace(cmd.Description) == "" {
		t.Fatal("description is empty")
	}
	if len(cmd.Options) != 1 {
		t.Fatalf("options = %d", len(cmd.Options))
	}

	option := cmd.Options[0]
	if option.Name != "page" || option.Type != discordgo.ApplicationCommandOptionString {
		t.Fatalf("option = %+v", option)
	}
	if option.Required {
		t.Fatal("page option must be optional")
	}
	if len(option.Choices) != len(docsPages) {
		t.Fatalf("choices = %d, want %d", len(option.Choices), len(docsPages))
	}
	for idx, page := range docsPages {
		choice := option.Choices[idx]
		if choice.Name != page.Title || choice.Value != page.Key {
			t.Errorf("choice %d = %q/%q, want %q/%q",
				idx, choice.Name, choice.Value, page.Title, page.Key)
		}
	}
}

func TestDocsPages(t *testing.T) {
	keys := make(map[string]bool, len(docsPages))
	for _, page := range docsPages {
		if page.Key == "" || page.Key == docsIndexKey {
			t.Errorf("invalid page key %q", page.Key)
		}
		if keys[page.Key] {
			t.Errorf("duplicate page key %q", page.Key)
		}
		keys[page.Key] = true

		if strings.TrimSpace(page.Title) == "" {
			t.Errorf("%s: title is empty", page.Key)
		}
		if len([]rune(page.Title)) > 80 {
			t.Errorf("%s: title too long for a button: %d", page.Key, len([]rune(page.Title)))
		}
		if strings.TrimSpace(page.Blurb) == "" {
			t.Errorf("%s: blurb is empty", page.Key)
		}
		if strings.TrimSpace(page.Description) == "" {
			t.Errorf("%s: description is empty", page.Key)
		}
		if len([]rune(page.Description)) > 4096 {
			t.Errorf("%s: description too long: %d", page.Key, len([]rune(page.Description)))
		}
		for _, field := range pageFields(page) {
			if len([]rune(field.Name)) > 256 {
				t.Errorf("%s field %q: name too long", page.Key, field.Name)
			}
			if len([]rune(field.Value)) > 1024 {
				t.Errorf("%s field %q: value too long: %d", page.Key, field.Name, len([]rune(field.Value)))
			}
		}
	}

	if len(keys) != len(docsPages) {
		t.Fatalf("unique keys = %d, pages = %d", len(keys), len(docsPages))
	}
}

func TestDocsEmbedsFitDiscordLimits(t *testing.T) {
	embeds := []*discordgo.MessageEmbed{docsIndexEmbed()}
	for _, page := range docsPages {
		embeds = append(embeds, docsEmbed(page.Key))
	}

	for _, embed := range embeds {
		name := embed.Title
		if name == "" {
			name = "index"
		}
		if len([]rune(embed.Title)) > 256 {
			t.Errorf("%s: title too long: %d", name, len([]rune(embed.Title)))
		}
		if len([]rune(embed.Description)) > 4096 {
			t.Errorf("%s: description too long: %d", name, len([]rune(embed.Description)))
		}
		if embed.Footer != nil && len([]rune(embed.Footer.Text)) > 2048 {
			t.Errorf("%s: footer too long: %d", name, len([]rune(embed.Footer.Text)))
		}
		if len(embed.Fields) > 25 {
			t.Errorf("%s: too many fields: %d", name, len(embed.Fields))
		}

		total := len([]rune(embed.Title)) + len([]rune(embed.Description))
		if embed.Footer != nil {
			total += len([]rune(embed.Footer.Text))
		}
		for _, field := range embed.Fields {
			total += len([]rune(field.Name)) + len([]rune(field.Value))
		}
		if total > 6000 {
			t.Errorf("%s: embed text too long: %d", name, total)
		}
	}
}

func TestDocsIndexEmbed(t *testing.T) {
	embed := docsIndexEmbed()
	if embed.Title != "Locksmith docs" {
		t.Fatalf("title = %q", embed.Title)
	}
	if embed.Color != embedColor {
		t.Fatalf("color = %#x", embed.Color)
	}
	if embed.Footer == nil || !strings.Contains(embed.Footer.Text, "pages") {
		t.Fatalf("footer = %+v", embed.Footer)
	}
	for _, page := range docsPages {
		if !strings.Contains(embed.Description, page.Title) {
			t.Errorf("index missing page title %q", page.Title)
		}
		if !strings.Contains(embed.Description, page.Blurb) {
			t.Errorf("index missing page blurb %q", page.Blurb)
		}
	}
}

func TestDocsPageLookup(t *testing.T) {
	for _, page := range docsPages {
		found, ok := docsPageByKey("  " + page.Key + "  ")
		if !ok || found.Key != page.Key {
			t.Errorf("docsPageByKey(%q) = %+v, %v", page.Key, found, ok)
		}
	}
	for _, key := range []string{"", "index", "nope"} {
		if page, ok := docsPageByKey(key); ok || page != nil {
			t.Errorf("docsPageByKey(%q) = %+v, %v", key, page, ok)
		}
	}
}

func TestDocsPageFooter(t *testing.T) {
	for idx, page := range docsPages {
		embed := docsEmbed(page.Key)
		want := "Page " + strconv.Itoa(idx+1) + " of " + strconv.Itoa(len(docsPages))
		if embed.Footer == nil || !strings.Contains(embed.Footer.Text, want) {
			t.Errorf("%s footer = %+v, want %q", page.Key, embed.Footer, want)
		}
	}

	cards := docsEmbed("cards")
	if cards.Footer == nil || !strings.Contains(cards.Footer.Text, "ChastiSafe app help") {
		t.Errorf("cards footer = %+v", cards.Footer)
	}

	unknown := docsEmbed("nope")
	if unknown.Title != "Locksmith docs" {
		t.Errorf("unknown page title = %q", unknown.Title)
	}
}

func TestDocsCustomIDKey(t *testing.T) {
	for _, want := range append([]string{docsIndexKey}, docsKeys()...) {
		customID := docsNamespace + ":" + docsIndexKey
		if want != docsIndexKey {
			customID = docsNamespace + ":page:" + want
		}
		got, ok := docsCustomIDKey(customID)
		if !ok || got != want {
			t.Errorf("docsCustomIDKey(%q) = %q, %v", customID, got, ok)
		}
	}

	for _, customID := range []string{
		"", "docs", "docs:", "docs:page:", "docs:page:nope",
		"other:page:cards", "docs:cards", "docs:page:cards:extra",
	} {
		if got, ok := docsCustomIDKey(customID); ok || got != "" {
			t.Errorf("docsCustomIDKey(%q) = %q, %v", customID, got, ok)
		}
	}
}

func docsKeys() []string {
	keys := make([]string, 0, len(docsPages))
	for _, page := range docsPages {
		keys = append(keys, page.Key)
	}
	return keys
}

func TestDocsComponents(t *testing.T) {
	index := docsComponents(docsIndexKey, "123")
	if len(index) == 0 {
		t.Fatal("index has no components")
	}
	assertDocsRows(t, index)

	var pageIDs []string
	for _, row := range index {
		action, ok := row.(discordgo.ActionsRow)
		if !ok {
			t.Fatalf("row = %#v", row)
		}
		for _, component := range action.Components {
			button, ok := component.(discordgo.Button)
			if !ok {
				t.Fatalf("component = %#v", component)
			}
			if button.Style == discordgo.LinkButton {
				continue
			}
			pageIDs = append(pageIDs, button.CustomID)
		}
	}
	for _, page := range docsPages {
		want := "docs:page:" + page.Key
		if !slices.Contains(pageIDs, want) {
			t.Errorf("index missing button %q", want)
		}
	}

	for _, page := range docsPages {
		components := docsComponents(page.Key, "123")
		assertDocsRows(t, components)

		row, ok := components[0].(discordgo.ActionsRow)
		if !ok || len(row.Components) != 1 {
			t.Fatalf("%s: first row = %#v", page.Key, components[0])
		}
		back, ok := row.Components[0].(discordgo.Button)
		if !ok || back.CustomID != "docs:index" || back.Label != "Back to docs" {
			t.Errorf("%s: back button = %#v", page.Key, row.Components[0])
		}
	}

	links := docsComponents("start", "")
	last, ok := links[len(links)-1].(discordgo.ActionsRow)
	if !ok || len(last.Components) != 3 {
		t.Fatalf("link row = %#v", links[len(links)-1])
	}
	for _, component := range last.Components {
		button, ok := component.(discordgo.Button)
		if !ok || button.Style != discordgo.LinkButton {
			t.Fatalf("link button = %#v", component)
		}
		if !strings.HasPrefix(button.URL, "https://") {
			t.Errorf("link %q is not https: %q", button.Label, button.URL)
		}
	}
}

func assertDocsRows(t *testing.T, components []discordgo.MessageComponent) {
	t.Helper()
	if len(components) > 5 {
		t.Fatalf("rows = %d, want at most 5", len(components))
	}
	for idx, row := range components {
		action, ok := row.(discordgo.ActionsRow)
		if !ok {
			t.Fatalf("row %d = %#v", idx, row)
		}
		if len(action.Components) == 0 || len(action.Components) > 5 {
			t.Fatalf("row %d has %d components", idx, len(action.Components))
		}
		for _, component := range action.Components {
			button, ok := component.(discordgo.Button)
			if !ok {
				t.Fatalf("row %d component = %#v", idx, component)
			}
			if len([]rune(button.Label)) > 80 {
				t.Errorf("button label too long: %q", button.Label)
			}
			if len([]rune(button.CustomID)) > 100 {
				t.Errorf("button custom ID too long: %q", button.CustomID)
			}
			if button.Style == discordgo.LinkButton && !strings.HasPrefix(button.URL, "https://") {
				t.Errorf("link %q is not https: %q", button.Label, button.URL)
			}
		}
	}
}

func TestDocsCardsCoverEveryCard(t *testing.T) {
	cards := []cs.Card{
		cs.CardGreen, cs.CardRed,
		cs.CardYellowP1, cs.CardYellowP2, cs.CardYellowP3,
		cs.CardYellowM1, cs.CardYellowM2,
		cs.CardYellow, cs.CardYellowUK, cs.CardYellowRN,
		cs.CardFreeze, cs.CardArctic, cs.CardStun, cs.CardSticky,
		cs.CardReset, cs.CardDoubleUp, cs.CardDice,
		cs.CardPink, cs.CardGoAgain,
	}

	page, ok := docsPageByKey("cards")
	if !ok {
		t.Fatal("cards page is missing")
	}
	var body strings.Builder
	body.WriteString(page.Description)
	for _, field := range pageFields(*page) {
		body.WriteString(field.Value)
	}
	text := body.String()

	for _, card := range cards {
		if !card.Valid() {
			t.Errorf("unknown card in coverage list: %q", card)
			continue
		}
		label := cardLogLabel(card)
		if !strings.Contains(text, "**"+label+"**") {
			t.Errorf("cards page missing %q (%s)", label, card)
		}
	}
}

func TestDocsCardEmojiAreResolvedAtRenderTime(t *testing.T) {
	embed := docsEmbed("cards")
	if len(embed.Fields) == 0 {
		t.Fatal("cards page has no fields")
	}

	var text strings.Builder
	for _, field := range embed.Fields {
		text.WriteString(field.Value)
		for _, line := range strings.Split(field.Value, "\n") {
			if strings.HasPrefix(line, " **") {
				t.Errorf("card line starts with a space: %q", line)
			}
		}
	}

	for _, card := range []cs.Card{cs.CardGreen, cs.CardFreeze, cs.CardPink} {
		markup := cardEmojiMarkup(card)
		if markup == "" {
			t.Fatalf("test emoji for %s is not seeded", card)
		}
		if !strings.Contains(text.String(), markup) {
			t.Errorf("cards page missing emoji %q", markup)
		}
	}
}

func pageFields(page docsPage) []*discordgo.MessageEmbedField {
	if page.Fields == nil {
		return nil
	}
	return page.Fields()
}

func TestSessionClientID(t *testing.T) {
	if got := sessionClientID(nil); got != "" {
		t.Fatalf("nil session = %q", got)
	}
	if got := userInstallURL(sessionClientID(nil)); !strings.Contains(got, defaultClientID) {
		t.Fatalf("install URL = %q, want fallback %q", got, defaultClientID)
	}
}

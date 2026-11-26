package feature

import (
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

// TestMain seeds the emoji registry so rendering tests do not depend on a live
// Discord application.
func TestMain(m *testing.M) {
	ids := make(map[string]string)
	for _, name := range requiredApplicationEmojiNames() {
		ids[name] = "test-" + name
	}
	setApplicationEmojiIDs(ids)
	os.Exit(m.Run())
}

func TestRequiredApplicationEmojiNames(t *testing.T) {
	required := make(map[string]bool)
	for _, name := range requiredApplicationEmojiNames() {
		required[name] = true
	}
	for card, name := range cardEmojiNames {
		if !required[name] {
			t.Errorf("card %q emoji %q is not required", card, name)
		}
	}
	if !required[applicationEmojiBlank] {
		t.Errorf("%q is not required", applicationEmojiBlank)
	}
	if names := requiredApplicationEmojiNames(); len(names) < 2 || names[0] > names[1] {
		t.Errorf("names are not sorted: %v", names)
	}
}

func TestApplicationEmojiIDsFromList(t *testing.T) {
	var emojis []*discordgo.Emoji
	for _, name := range requiredApplicationEmojiNames() {
		emojis = append(emojis, &discordgo.Emoji{Name: name, ID: "id-" + name})
	}
	emojis = append(emojis, nil, &discordgo.Emoji{Name: "", ID: "ignored"})

	ids, err := applicationEmojiIDsFromList(emojis)
	if err != nil {
		t.Fatalf("applicationEmojiIDsFromList: %v", err)
	}
	for _, name := range requiredApplicationEmojiNames() {
		if got, want := ids[name], "id-"+name; got != want {
			t.Errorf("ids[%q] = %q, want %q", name, got, want)
		}
	}
	if _, ok := ids[""]; ok {
		t.Error("empty emoji name should be skipped")
	}
}

func TestApplicationEmojiIDsFromListMissing(t *testing.T) {
	var emojis []*discordgo.Emoji
	for _, name := range requiredApplicationEmojiNames() {
		if name == applicationEmojiBlank || name == "green" {
			continue
		}
		emojis = append(emojis, &discordgo.Emoji{Name: name, ID: "id-" + name})
	}

	_, err := applicationEmojiIDsFromList(emojis)
	if err == nil {
		t.Fatal("missing emoji should fail")
	}
	for _, want := range []string{":blank:", ":green:", "mise run emojis"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestEmojiMarkup(t *testing.T) {
	if got, want := emojiMarkup("green"), "<:green:test-green>"; got != want {
		t.Errorf("emojiMarkup(green) = %q, want %q", got, want)
	}
	if got := emojiMarkup("not-uploaded"); got != "" {
		t.Errorf("emojiMarkup(not-uploaded) = %q, want empty", got)
	}

	applicationEmojiIDs.RLock()
	saved := applicationEmojiIDs.byName
	applicationEmojiIDs.RUnlock()
	setApplicationEmojiIDs(nil)
	t.Cleanup(func() { setApplicationEmojiIDs(saved) })
	if got := cardEmojiMarkup(cs.CardGreen); got != "" {
		t.Errorf("cardEmojiMarkup without a registry = %q, want empty", got)
	}
}

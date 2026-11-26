package feature

// Application emoji are uploaded to the Discord application, not to a guild, so
// they render in DMs and in every server the bot is installed in. The bot does
// not store emoji IDs: every READY event reloads the application's emoji list
// and resolves the names it needs. A recreated application therefore only needs
// the upload tool, not a new commit.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// applicationEmojiBlank is the card-back icon. Nothing renders it yet, but the
// upload tool registers it so it is available as :blank: when a surface needs
// an unrevealed card.
const applicationEmojiBlank = "blank"

// applicationEmojiIDs holds the current application emoji IDs by name.
var applicationEmojiIDs struct {
	sync.RWMutex
	byName map[string]string
}

// setApplicationEmojiIDs replaces the registry after a successful load.
func setApplicationEmojiIDs(ids map[string]string) {
	applicationEmojiIDs.Lock()
	defer applicationEmojiIDs.Unlock()
	applicationEmojiIDs.byName = ids
}

// applicationEmojiID returns the ID for an emoji name.
func applicationEmojiID(name string) (string, bool) {
	applicationEmojiIDs.RLock()
	defer applicationEmojiIDs.RUnlock()
	id, ok := applicationEmojiIDs.byName[name]
	return id, ok && id != ""
}

// emojiMarkup returns the inline custom emoji for a name, or "" when the
// application does not have it.
func emojiMarkup(name string) string {
	id, ok := applicationEmojiID(name)
	if !ok {
		return ""
	}
	return "<:" + name + ":" + id + ">"
}

// requiredApplicationEmojiNames returns every emoji the bot needs at runtime.
// Startup fails when one is missing, so a forgotten upload is loud instead of
// rendering as plain text.
func requiredApplicationEmojiNames() []string {
	seen := make(map[string]struct{}, len(cardEmojiNames)+1)
	for _, name := range cardEmojiNames {
		seen[name] = struct{}{}
	}
	seen[applicationEmojiBlank] = struct{}{}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// loadApplicationEmojis fetches the application emoji and stores their IDs. It
// runs on every READY event so a reconnect picks up a changed emoji set.
func (f *Feature) loadApplicationEmojis(ctx context.Context, s *discordgo.Session, r *discordgo.Ready) error {
	if r.User == nil || r.User.ID == "" {
		return fmt.Errorf("loading application emoji: ready event has no bot user")
	}
	appID := r.User.ID
	if r.Application != nil && r.Application.ID != "" {
		appID = r.Application.ID
	}

	emojis, err := s.ApplicationEmojis(appID)
	if err != nil {
		return fmt.Errorf("loading application emoji: %w", err)
	}

	ids, err := applicationEmojiIDsFromList(emojis)
	if err != nil {
		return err
	}
	setApplicationEmojiIDs(ids)
	f.log.Info("loaded application emoji", "count", len(ids))
	return nil
}

// applicationEmojiIDsFromList maps emoji names to IDs and fails when one of the
// required names is missing.
func applicationEmojiIDsFromList(emojis []*discordgo.Emoji) (map[string]string, error) {
	ids := make(map[string]string, len(emojis))
	for _, emoji := range emojis {
		if emoji == nil || emoji.Name == "" || emoji.ID == "" {
			continue
		}
		ids[emoji.Name] = emoji.ID
	}

	var missing []string
	for _, name := range requiredApplicationEmojiNames() {
		if ids[name] == "" {
			missing = append(missing, ":"+name+":")
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"application is missing required emoji %s; run `mise run emojis`",
			strings.Join(missing, ", "),
		)
	}
	return ids, nil
}

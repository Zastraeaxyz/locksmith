package feature

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
)

const (
	// autocompleteTimeout leaves headroom before Discord's three second
	// deadline, so a slow API still gets an empty response out in time.
	autocompleteTimeout = 2500 * time.Millisecond
	// autocompleteChoiceLimit is Discord's maximum number of choices.
	autocompleteChoiceLimit = 25
)

// handleLockAutocomplete suggests values for every /lock ref option.
func (f *Feature) handleLockAutocomplete(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return bot.Autocomplete(s, i, nil)
	}
	sub := data.Options[0]
	ref, ok := focusedStringValue(sub.Options)
	if !ok {
		return bot.Autocomplete(s, i, nil)
	}

	userID := bot.UserID(i)
	if userID == "" {
		return bot.Autocomplete(s, i, nil)
	}
	ctx, cancel := context.WithTimeout(ctx, autocompleteTimeout)
	defer cancel()

	client, cred, err := f.clientFor(ctx, userID)
	if err != nil {
		return bot.Autocomplete(s, i, nil)
	}

	switch sub.Name {
	case "share":
		templates, ok := f.templateCache.get(userID)
		if !ok {
			templates, err = client.Templates(ctx)
			if err != nil {
				return bot.Autocomplete(s, i, nil)
			}
			f.templateCache.put(userID, templates)
		}
		return bot.Autocomplete(s, i, buildRefChoices(ref, shareCandidates(templates, cred.ChastiSafeID)))
	case "checkin":
		locks, ok := f.loadedCache.get(userID)
		if !ok {
			locks, err = client.Loaded(ctx)
			if err != nil {
				return bot.Autocomplete(s, i, nil)
			}
			f.loadedCache.put(userID, locks)
		}
		return bot.Autocomplete(s, i, buildRefChoices(ref, checkinCandidates(locks, cred.ChastiSafeID)))
	case "open":
		locks, ok := f.loadedCache.get(userID)
		if !ok {
			locks, err = client.Loaded(ctx)
			if err != nil {
				return bot.Autocomplete(s, i, nil)
			}
			f.loadedCache.put(userID, locks)
		}
		return bot.Autocomplete(s, i, buildRefChoices(ref, openCandidates(locks, cred.ChastiSafeID)))
	case "log":
		locks, hasLocks := f.loadedCache.get(userID)
		if !hasLocks {
			if fetched, err := client.Loaded(ctx); err == nil {
				locks, hasLocks = fetched, true
				f.loadedCache.put(userID, fetched)
			}
		}
		recent, hasRecent := f.recentCache.get(userID)
		if !hasRecent {
			if fetched, err := client.Recent(ctx); err == nil {
				recent, hasRecent = fetched, true
				f.recentCache.put(userID, fetched)
			}
		}
		if !hasLocks && !hasRecent {
			return bot.Autocomplete(s, i, nil)
		}
		return bot.Autocomplete(s, i, buildRefChoices(ref, logCandidates(locks, recent, cred.ChastiSafeID)))
	}
	return bot.Autocomplete(s, i, nil)
}

// focusedStringValue returns the value of the focused string option. Discord
// sends the typed text so the bot can filter its own suggestions.
func focusedStringValue(options []*discordgo.ApplicationCommandInteractionDataOption) (string, bool) {
	for _, option := range options {
		if option.Focused {
			if option.Type != discordgo.ApplicationCommandOptionString {
				return "", false
			}
			return option.StringValue(), true
		}
		if value, ok := focusedStringValue(option.Options); ok {
			return value, true
		}
	}
	return "", false
}

// refChoice is one lock or template that can be suggested for a ref option.
type refChoice struct {
	id   cs.ID
	name string
}

// buildRefChoices filters candidates and turns them into Discord choices. The
// choice value is the ID, so the existing resolvers keep matching after a
// suggestion is selected.
func buildRefChoices(ref string, candidates []refChoice) []*discordgo.ApplicationCommandOptionChoice {
	query := strings.ToLower(strings.TrimSpace(ref))
	matches := make([]refChoice, 0, len(candidates))
	seen := make(map[cs.ID]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate.id == "" || seen[candidate.id] {
			continue
		}
		if query != "" && !matchesRefQuery(candidate.name, candidate.id.String(), query) {
			continue
		}
		seen[candidate.id] = true
		matches = append(matches, candidate)
		if len(matches) == autocompleteChoiceLimit {
			break
		}
	}

	counts := make(map[string]int, len(matches))
	for _, match := range matches {
		counts[displayRefName(match)]++
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(matches))
	for _, match := range matches {
		name := displayRefName(match)
		if counts[name] > 1 {
			name = fmt.Sprintf("%s (%s)", name, match.id)
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  truncateRunes(name, 100),
			Value: match.id.String(),
		})
	}
	return choices
}

func matchesRefQuery(name, id, query string) bool {
	if strings.Contains(strings.ToLower(name), query) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(id), query)
}

func displayRefName(candidate refChoice) string {
	if name := strings.TrimSpace(candidate.name); name != "" {
		return name
	}
	return "Untitled lock"
}

// shareCandidates lists the templates the caller can publish.
func shareCandidates(templates []cs.Template, chastisafeID string) []refChoice {
	out := make([]refChoice, 0, len(templates))
	for idx := range templates {
		template := &templates[idx]
		if shareableTemplate(template, chastisafeID) != nil {
			continue
		}
		out = append(out, refChoice{id: template.ID, name: templateName(template)})
	}
	return out
}

// checkinCandidates lists the caller's locks that can record a check-in.
func checkinCandidates(locks []cs.LoadedLock, chastisafeID string) []refChoice {
	own := newestRunningFirst(ownLocks(locks, chastisafeID))

	out := make([]refChoice, 0, len(own))
	for idx := range own {
		lock := &own[idx]
		if lock.CheckInPeriod <= 0 || lock.IsReady() {
			continue
		}
		out = append(out, refChoice{id: lock.ID, name: lockName(lock)})
	}
	return out
}

// openCandidates lists the caller's locks that are ready to open.
func openCandidates(locks []cs.LoadedLock, chastisafeID string) []refChoice {
	own := newestRunningFirst(ownLocks(locks, chastisafeID))

	out := make([]refChoice, 0, len(own))
	for idx := range own {
		lock := &own[idx]
		if !lock.IsReady() || lock.IsFrozen() {
			continue
		}
		out = append(out, refChoice{id: lock.ID, name: lockName(lock)})
	}
	return out
}

// logCandidates lists the caller's active locks followed by previous locks.
func logCandidates(running []cs.LoadedLock, recent []cs.RecentLock, chastisafeID string) []refChoice {
	own := newestRunningFirst(ownLocks(running, chastisafeID))
	previous := newestRecentFirst(recent)

	out := make([]refChoice, 0, len(own)+len(previous))
	for idx := range own {
		out = append(out, refChoice{id: own[idx].ID, name: lockName(&own[idx])})
	}
	for idx := range previous {
		out = append(out, refChoice{id: previous[idx].LockID, name: recentName(&previous[idx])})
	}
	return out
}

// Package feature implements the Locksmith slash commands.
//
// Every member stores their own ChastiSafe developer code after /login.
// Commands resolve that code for the caller only, through the account module.
package feature

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/bot"
	"locksmith/internal/cryptobox"
	"locksmith/internal/store"
)

// Options configures the feature.
type Options struct {
	Store          *store.Store
	Box            *cryptobox.Box
	BaseURL        string
	UserAgent      string
	Logger         *slog.Logger
	Now            func() time.Time
	NotifyInterval time.Duration
	CheckinLead    time.Duration
	Changelog      string
}

// Feature implements the per-member ChastiSafe commands and notifications.
type Feature struct {
	// accounts is the member account module: credentials, clients, and the
	// rejected-code rule.
	accounts

	store          *store.Store
	bot            *bot.Bot
	baseURL        string
	log            *slog.Logger
	now            func() time.Time
	notifyInterval time.Duration
	checkinLead    time.Duration
	changelog      string

	templateCache *ttlCache[[]cs.Template]
	loadedCache   *ttlCache[[]cs.LoadedLock]
	recentCache   *ttlCache[[]cs.RecentLock]

	sessionsMu sync.Mutex
	sessions   map[string]*drawSession
}

// New builds the feature.
func New(opts Options) *Feature {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "locksmith/1"
	}
	if opts.NotifyInterval <= 0 {
		opts.NotifyInterval = 30 * time.Second
	}
	return &Feature{
		accounts:       newAccounts(opts.Store, opts.Box, opts.BaseURL, opts.UserAgent, opts.Logger, opts.Now),
		store:          opts.Store,
		baseURL:        strings.TrimSpace(opts.BaseURL),
		log:            opts.Logger,
		now:            opts.Now,
		notifyInterval: opts.NotifyInterval,
		checkinLead:    opts.CheckinLead,
		changelog:      opts.Changelog,
		templateCache:  newTTLCache[[]cs.Template](autocompleteCacheTTL, opts.Now),
		loadedCache:    newTTLCache[[]cs.LoadedLock](autocompleteCacheTTL, opts.Now),
		recentCache:    newTTLCache[[]cs.RecentLock](autocompleteCacheTTL, opts.Now),
		sessions:       make(map[string]*drawSession),
	}
}

// Register wires the feature into the bot.
func (f *Feature) Register(b *bot.Bot) {
	if b == nil {
		return
	}
	f.bot = b
	b.AddCommand(loginCommand(), f.handleLoginCommand)
	b.AddCommand(logoutCommand(), f.handleLogoutCommand)
	b.AddCommand(lockCommand(), f.handleLockCommand)
	b.AddCommand(historyCommand(), f.handleHistoryCommand)
	b.AddCommand(changelogCommand(), f.handleChangelogCommand)
	b.AddCommand(helpCommand(), f.handleHelpCommand)
	b.AddCommand(profileCommand(), f.handleProfileCommand)
	b.AddCommand(rulesCommand(), f.handleRulesCommand)
	b.AddCommand(kinksCommand(), f.handleKinksCommand)
	b.AddModal(modalNamespace, f.handleLoginModal)
	b.AddModal(loadNamespace, f.handleLoadModal)
	b.AddModal(resetNamespace, f.handleResetModal)
	b.AddModal(store.DocumentProfile, f.handleProfileModal)
	b.AddModal(store.DocumentRules, f.handleRulesModal)
	b.AddModal(store.DocumentKinks, f.handleKinksModal)
	b.AddComponent(drawNamespace, f.handleDrawComponent)
	b.AddComponent(drawBoardNamespace, f.handleDrawBoardComponent)
	b.AddComponent(checkinNamespace, f.handleCheckinComponent)
	b.AddComponent(rateNamespace, f.handleRateComponent)
	b.AddComponent(shareNamespace, f.handleShareComponent)
	b.AddComponent(loadNamespace, f.handleLoadComponent)
	b.AddComponent(simulationNamespace, f.handleSimulationComponent)
	b.AddAutocomplete("lock", f.handleLockAutocomplete)
}

// forgetAutocomplete drops every cached autocomplete result for a member, so a
// changed credential or a new login never reuses another account's data.
func (f *Feature) forgetAutocomplete(userID string) {
	f.templateCache.forget(userID)
	f.forgetLockAutocomplete(userID)
}

// forgetLockAutocomplete drops cached lock lists for a member, so a newly
// loaded lock appears in suggestions immediately.
func (f *Feature) forgetLockAutocomplete(userID string) {
	f.loadedCache.forget(userID)
	f.recentCache.forget(userID)
}

// apiFailure turns a ChastiSafe error into a private reply and clears cached
// autocomplete data when the account module dropped a rejected code.
func (f *Feature) apiFailure(ctx context.Context, userID string, err error) string {
	copy, dropped := f.failureReply(ctx, userID, err)
	if dropped {
		f.forgetAutocomplete(userID)
	}
	return copy
}

// track records a funnel event. It is a no-op when the feature is not
// registered with a bot, which keeps unit tests simple.
func (f *Feature) track(name string) {
	if f.bot == nil {
		return
	}
	f.bot.Track(name, nil)
}

// sendEphemeral answers a handler that has already been deferred.
func sendEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, text string) error {
	return bot.EditEphemeralChunks(s, i, text)
}

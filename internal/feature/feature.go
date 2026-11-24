// Package feature implements the Locksmith slash commands.
//
// Every member stores their own ChastiSafe developer code after /login.
// Commands resolve that code for the caller only.
package feature

import (
	"context"
	"errors"
	"fmt"
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

var (
	errNotConfigured        = errors.New("locksmith: credential store is not configured")
	errNotLoggedIn          = errors.New("locksmith: member is not logged in")
	errCredentialUnreadable = errors.New("locksmith: stored credential cannot be read")
	errNotOwner             = errors.New("locksmith: code belongs to another Discord account")
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
}

// Feature implements the per-member ChastiSafe commands and notifications.
type Feature struct {
	store          *store.Store
	box            *cryptobox.Box
	bot            *bot.Bot
	baseURL        string
	userAgent      string
	log            *slog.Logger
	now            func() time.Time
	notifyInterval time.Duration
	checkinLead    time.Duration
	limiter        *loginLimiter

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
		store:          opts.Store,
		box:            opts.Box,
		baseURL:        strings.TrimSpace(opts.BaseURL),
		userAgent:      opts.UserAgent,
		log:            opts.Logger,
		now:            opts.Now,
		notifyInterval: opts.NotifyInterval,
		checkinLead:    opts.CheckinLead,
		limiter:        newLoginLimiter(5, 10*time.Minute, opts.Now),
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
	b.AddComponent(checkinNamespace, f.handleCheckinComponent)
	b.AddComponent(shareNamespace, f.handleShareComponent)
	b.AddComponent(simulationNamespace, f.handleSimulationComponent)
	b.AddAutocomplete("lock", f.handleLockAutocomplete)
}

func (f *Feature) clientFor(ctx context.Context, userID string) (*cs.Client, *store.Credential, error) {
	if f.store == nil || f.box == nil {
		return nil, nil, errNotConfigured
	}
	if strings.TrimSpace(userID) == "" {
		return nil, nil, errNotLoggedIn
	}

	cred, err := f.store.Credential(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading a credential: %w", err)
	}
	if cred == nil {
		return nil, nil, errNotLoggedIn
	}

	secret, err := f.box.Open(cred.Secret)
	if err != nil {
		f.log.Warn("cannot decrypt a credential", "user_id", userID, "err", err)
		return nil, nil, errCredentialUnreadable
	}
	code := strings.TrimSpace(string(secret))
	if code == "" {
		return nil, nil, errCredentialUnreadable
	}

	client, err := cs.New(cs.Options{
		Token:     code,
		BaseURL:   f.baseURL,
		UserAgent: f.userAgent,
		Logger:    f.log,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, cred, nil
}

// authenticate validates a submitted code and stores it for the member.
func (f *Feature) authenticate(ctx context.Context, userID, code string) (*cs.Identity, error) {
	if f.store == nil || f.box == nil {
		return nil, errNotConfigured
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, cs.ErrUnauthorized
	}
	if strings.TrimSpace(userID) == "" {
		return nil, errNotOwner
	}

	client, err := cs.New(cs.Options{
		Token:     code,
		BaseURL:   f.baseURL,
		UserAgent: f.userAgent,
		Logger:    f.log,
	})
	if err != nil {
		return nil, err
	}
	identity, err := client.Who(ctx)
	if err != nil {
		return nil, err
	}
	if !identity.OwnedBy(userID) {
		return nil, errNotOwner
	}

	sealed, err := f.box.Seal([]byte(code))
	if err != nil {
		return nil, fmt.Errorf("encrypting a code: %w", err)
	}
	if err := f.store.SaveCredential(ctx, &store.Credential{
		DiscordUserID:  userID,
		ChastiSafeID:   identity.ID.String(),
		ChastiSafeUser: identity.User,
		Secret:         sealed,
	}); err != nil {
		return nil, fmt.Errorf("saving a credential: %w", err)
	}
	return identity, nil
}

// forget deletes the caller's stored code and notification.
func (f *Feature) forget(ctx context.Context, userID string) error {
	if f.store == nil {
		return errNotConfigured
	}
	if err := f.store.DeleteCredential(ctx, userID); err != nil {
		return fmt.Errorf("deleting a credential: %w", err)
	}
	return nil
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

// apiFailure turns a ChastiSafe error into a private reply. An unauthorized
// response also drops the saved code so the member cannot loop on it.
func (f *Feature) apiFailure(ctx context.Context, userID string, err error) string {
	if errors.Is(err, cs.ErrUnauthorized) {
		if f.store != nil {
			if delErr := f.store.DeleteCredential(ctx, userID); delErr != nil {
				f.log.Warn("deleting a rejected credential", "user_id", userID, "err", delErr)
			}
		}
		f.forgetAutocomplete(userID)
		f.log.Info("chastisafe rejected a saved code", "user_id", userID)
		return "ChastiSafe rejected your saved code. Run `/login` again."
	}
	if errors.Is(err, errNotLoggedIn) {
		return "Run `/login` first with your ChastiSafe developer code."
	}
	if errors.Is(err, errCredentialUnreadable) {
		return "I cannot read your saved code. Run `/login` again."
	}
	if errors.Is(err, errNotConfigured) {
		return "Locksmith is not configured to store codes."
	}
	if errors.Is(err, cs.ErrNotKeyholder) {
		return "ChastiSafe says this account cannot do that for this lock."
	}
	if errors.Is(err, cs.ErrLockNotFound) {
		return "That lock no longer exists."
	}
	if errors.Is(err, cs.ErrLockNotReady) {
		return "That lock is not ready for this action."
	}
	if errors.Is(err, cs.ErrNotAllowed) {
		return "ChastiSafe does not allow that action for this lock."
	}
	f.log.Warn("chastisafe request failed", "user_id", userID, "err", err)
	return "I could not reach ChastiSafe right now. Try again in a moment."
}

func (f *Feature) loginAttemptAllowed(userID string) bool {
	return f.limiter.allow(userID)
}

func (f *Feature) loginSucceeded(userID string) {
	f.limiter.reset(userID)
}

// track records a funnel event. It is a no-op when the feature is not
// registered with a bot, which keeps unit tests simple.
func (f *Feature) track(name string) {
	if f.bot == nil {
		return
	}
	f.bot.Track(name, nil)
}

// loginLimiter keeps a short failed-attempt window per member.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
	now      func() time.Time
}

func newLoginLimiter(limit int, window time.Duration, now func() time.Time) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	if limit < 1 {
		limit = 1
	}
	return &loginLimiter{
		attempts: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
		now:      now,
	}
}

func (l *loginLimiter) allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)
	kept := l.attempts[userID][:0]
	for _, attempt := range l.attempts[userID] {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	if len(kept) >= l.limit {
		l.attempts[userID] = kept
		return false
	}
	l.attempts[userID] = append(kept, now)
	return true
}

func (l *loginLimiter) reset(userID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, userID)
}

// sendEphemeral answers a handler that has already been deferred.
func sendEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, text string) error {
	return bot.EditEphemeralChunks(s, i, text)
}

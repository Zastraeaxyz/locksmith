package feature

// The account module is one member's ChastiSafe account: the stored developer
// code, the client built from it, the login rate limit, and the rule for what a
// rejected code means.
//
// Everything that knows a code exists crosses this module. Every rejection path
// goes through failureReply, so a rejected code is dropped in one place, and the
// client factory is a seam that tests can replace.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/cryptobox"
	"locksmith/internal/store"
)

var (
	errNotConfigured        = errors.New("locksmith: credential store is not configured")
	errNotLoggedIn          = errors.New("locksmith: member is not logged in")
	errCredentialUnreadable = errors.New("locksmith: stored credential cannot be read")
	errNotOwner             = errors.New("locksmith: code belongs to another Discord account")
)

// clientFactory builds a ChastiSafe client from one member's options. It is a
// field on accounts so a test can replace the network adapter.
type clientFactory func(cs.Options) (*cs.Client, error)

// accounts holds the per-member credential state. The zero value is safe to
// read: every method reports errNotConfigured instead of panicking.
type accounts struct {
	store     *store.Store
	box       *cryptobox.Box
	baseURL   string
	userAgent string
	log       *slog.Logger
	limiter   *loginLimiter
	newClient clientFactory
}

func newAccounts(st *store.Store, box *cryptobox.Box, baseURL, userAgent string, log *slog.Logger, now func() time.Time) accounts {
	return accounts{
		store:     st,
		box:       box,
		baseURL:   strings.TrimSpace(baseURL),
		userAgent: userAgent,
		log:       log,
		limiter:   newLoginLimiter(5, 10*time.Minute, now),
	}
}

// configured reports whether codes can be stored and read.
func (a *accounts) configured() bool {
	return a.store != nil && a.box != nil
}

func (a *accounts) logger() *slog.Logger {
	if a.log == nil {
		return slog.Default()
	}
	return a.log
}

func (a *accounts) factory() clientFactory {
	if a.newClient != nil {
		return a.newClient
	}
	return cs.New
}

// clientFor builds a ChastiSafe client for a member from their stored code.
func (a *accounts) clientFor(ctx context.Context, userID string) (*cs.Client, *store.Credential, error) {
	if !a.configured() {
		return nil, nil, errNotConfigured
	}
	if strings.TrimSpace(userID) == "" {
		return nil, nil, errNotLoggedIn
	}

	cred, err := a.store.Credential(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading a credential: %w", err)
	}
	if cred == nil {
		return nil, nil, errNotLoggedIn
	}

	secret, err := a.box.Open(cred.Secret)
	if err != nil {
		a.logger().Warn("cannot decrypt a credential", "user_id", userID, "err", err)
		return nil, nil, errCredentialUnreadable
	}
	code := strings.TrimSpace(string(secret))
	if code == "" {
		return nil, nil, errCredentialUnreadable
	}

	client, err := a.factory()(cs.Options{
		Token:     code,
		BaseURL:   a.baseURL,
		UserAgent: a.userAgent,
		Logger:    a.log,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, cred, nil
}

// authenticate validates a submitted code and stores it for the member.
func (a *accounts) authenticate(ctx context.Context, userID, code string) (*cs.Identity, error) {
	if !a.configured() {
		return nil, errNotConfigured
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, cs.ErrUnauthorized
	}
	if strings.TrimSpace(userID) == "" {
		return nil, errNotOwner
	}

	client, err := a.factory()(cs.Options{
		Token:     code,
		BaseURL:   a.baseURL,
		UserAgent: a.userAgent,
		Logger:    a.log,
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

	sealed, err := a.box.Seal([]byte(code))
	if err != nil {
		return nil, fmt.Errorf("encrypting a code: %w", err)
	}
	if err := a.store.SaveCredential(ctx, &store.Credential{
		DiscordUserID:  userID,
		ChastiSafeID:   identity.ID.String(),
		ChastiSafeUser: identity.User,
		Secret:         sealed,
	}); err != nil {
		return nil, fmt.Errorf("saving a credential: %w", err)
	}
	return identity, nil
}

// forget deletes the caller's stored code.
func (a *accounts) forget(ctx context.Context, userID string) error {
	if a.store == nil {
		return errNotConfigured
	}
	if err := a.store.DeleteCredential(ctx, userID); err != nil {
		return fmt.Errorf("deleting a credential: %w", err)
	}
	return nil
}

func (a *accounts) loginAttemptAllowed(userID string) bool {
	if a.limiter == nil {
		return true
	}
	return a.limiter.allow(userID)
}

func (a *accounts) loginSucceeded(userID string) {
	if a.limiter != nil {
		a.limiter.reset(userID)
	}
}

// failureReply turns a ChastiSafe error into a private reply. An unauthorized
// response also drops the saved code so the member cannot loop on it. It
// reports whether the code was dropped, so the caller can clear derived state.
func (a *accounts) failureReply(ctx context.Context, userID string, err error) (string, bool) {
	if errors.Is(err, cs.ErrUnauthorized) {
		if a.store != nil {
			if delErr := a.store.DeleteCredential(ctx, userID); delErr != nil {
				a.logger().Warn("deleting a rejected credential", "user_id", userID, "err", delErr)
			}
		}
		a.logger().Info("chastisafe rejected a saved code", "user_id", userID)
		return "ChastiSafe rejected your saved code. Run `/login` again.", true
	}
	if errors.Is(err, errNotLoggedIn) {
		return "Run `/login` first with your ChastiSafe developer code.", false
	}
	if errors.Is(err, errCredentialUnreadable) {
		return "I cannot read your saved code. Run `/login` again.", false
	}
	if errors.Is(err, errNotConfigured) {
		return "Locksmith is not configured to store codes.", false
	}
	if errors.Is(err, cs.ErrNotKeyholder) {
		return "ChastiSafe says this account cannot do that for this lock.", false
	}
	if errors.Is(err, cs.ErrLockNotFound) {
		return "That lock no longer exists.", false
	}
	if errors.Is(err, cs.ErrLockNotReady) {
		return "That lock is not ready for this action.", false
	}
	if errors.Is(err, cs.ErrNotAllowed) {
		return "ChastiSafe does not allow that action for this lock.", false
	}
	a.logger().Warn("chastisafe request failed", "user_id", userID, "err", err)
	return "I could not reach ChastiSafe right now. Try again in a moment.", false
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

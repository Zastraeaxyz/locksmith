package feature

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func freezeTestClient(t *testing.T, api *fakeAPI) *cs.Client {
	t.Helper()

	client, err := cs.New(cs.Options{
		Token:      "test-token",
		BaseURL:    testChastiSafeBaseURL,
		HTTPClient: &http.Client{Transport: api},
	})
	if err != nil {
		t.Fatalf("cs.New: %v", err)
	}
	return client
}

func TestFreezePastMaximum(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		lock  *cs.LoadedLock
		since time.Time
		want  bool
	}{
		{
			name:  "card freeze past four intervals",
			lock:  &cs.LoadedLock{Type: "card", FrozenByCard: true, Regularity: 60},
			since: now.Add(-5 * time.Hour),
			want:  true,
		},
		{
			name:  "card freeze inside four intervals",
			lock:  &cs.LoadedLock{Type: "card", FrozenByCard: true, Regularity: 60},
			since: now.Add(-3 * time.Hour),
			want:  false,
		},
		{
			name:  "keyholder freeze has no maximum",
			lock:  &cs.LoadedLock{FrozenByKeyholder: true, Regularity: 60},
			since: now.Add(-9 * time.Hour),
			want:  false,
		},
		{
			name:  "mixed freeze has no maximum",
			lock:  &cs.LoadedLock{FrozenByCard: true, FrozenByKeyholder: true, Regularity: 60},
			since: now.Add(-9 * time.Hour),
			want:  false,
		},
		{
			name:  "leftover flag with a draw available",
			lock:  &cs.LoadedLock{Type: "card", FrozenByCard: true, Regularity: 60, Picks: 1},
			since: now.Add(-9 * time.Hour),
			want:  false,
		},
		{
			name:  "missing draw interval has no scale",
			lock:  &cs.LoadedLock{Type: "card", FrozenByCard: true},
			since: now.Add(-9 * time.Hour),
			want:  false,
		},
		{
			name:  "missing start has no maximum",
			lock:  &cs.LoadedLock{Type: "card", FrozenByCard: true, Regularity: 60},
			since: time.Time{},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := freezePastMaximum(tc.lock, tc.since, now); got != tc.want {
				t.Fatalf("freezePastMaximum = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFreshLocksRetriesAStaleCardFreeze(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	api := newFakeAPI()

	frozen := []byte(`[{"id":"lock-1","lockname":"Test","type":"card","frozenByCard":true,"regularity":60,"picks":0,"lockeeId":"me","loadtime":"2026-11-24T07:00:00Z"}]`)
	clear := []byte(`[{"id":"lock-1","lockname":"Test","type":"card","frozenByCard":false,"regularity":60,"picks":0,"lockeeId":"me","loadtime":"2026-11-24T07:00:00Z"}]`)
	api.serveSequence(t, http.MethodGet, testChastiSafeBaseURL+"/loaded", http.StatusOK, frozen, clear)
	api.serveBody(t, http.MethodPost, testChastiSafeBaseURL+"/log", http.StatusOK,
		[]byte(`[{"timestamp":"2026-11-24T07:00:00Z","actor":1,"event_type":6,"card":"f","amount":1,"hidden":0}]`))

	client := freezeTestClient(t, api)
	locks, err := client.Loaded(ctx)
	if err != nil {
		t.Fatalf("Loaded: %v", err)
	}
	if len(locks) != 1 || !locks[0].FrozenByCard {
		t.Fatalf("fixture locks = %+v", locks)
	}

	f := &Feature{log: slog.Default(), now: func() time.Time { return now }}
	got, freeze := f.freshLocks(ctx, client, locks, now)
	if len(got) != 1 || got[0].FrozenByCard {
		t.Fatalf("freshLocks = %+v, want the lock no longer frozen", got)
	}
	if len(freeze) != 0 {
		t.Fatalf("freeze states = %+v, want none after the refresh", freeze)
	}
}

func TestFreshLocksKeepsAFlagThatStaysSet(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	api := newFakeAPI()

	frozen := []byte(`[{"id":"lock-1","lockname":"Test","type":"card","frozenByCard":true,"regularity":60,"picks":0,"lockeeId":"me","loadtime":"2026-11-24T07:00:00Z"}]`)
	api.serveSequence(t, http.MethodGet, testChastiSafeBaseURL+"/loaded", http.StatusOK, frozen, frozen)
	api.serveBody(t, http.MethodPost, testChastiSafeBaseURL+"/log", http.StatusOK,
		[]byte(`[{"timestamp":"2026-11-24T07:00:00Z","actor":1,"event_type":6,"card":"f","amount":1,"hidden":0}]`))

	client := freezeTestClient(t, api)
	locks, err := client.Loaded(ctx)
	if err != nil {
		t.Fatalf("Loaded: %v", err)
	}

	f := &Feature{log: slog.Default(), now: func() time.Time { return now }}
	got, freeze := f.freshLocks(ctx, client, locks, now)
	if len(got) != 1 || !got[0].FrozenByCard {
		t.Fatalf("freshLocks = %+v, want the API flag kept", got)
	}
	state, ok := freeze[got[0].ID]
	if !ok || !state.pastMaximum {
		t.Fatalf("freeze state = %+v, want it marked past the maximum", freeze)
	}
	if state.since.IsZero() {
		t.Fatal("freeze state should keep the start from the log")
	}
}

func TestDrawPickRefreshesFromLoaded(t *testing.T) {
	ctx := context.Background()
	api := newFakeAPI()

	api.serveBody(t, http.MethodGet, testChastiSafeBaseURL+"/pick/lock-1", http.StatusOK,
		[]byte(`{"status":0,"card":"g","lock":{"id":"lock-1","type":"card","picks":1}}`))
	api.serveBody(t, http.MethodGet, testChastiSafeBaseURL+"/loaded", http.StatusOK,
		[]byte(`[{"id":"lock-1","type":"card","picks":0,"lockeeId":"me"}]`))

	client := freezeTestClient(t, api)
	session := &drawSession{}
	lock := &cs.LoadedLock{ID: "lock-1", Type: "card", Picks: 1, LockeeID: "me"}
	f := &Feature{log: slog.Default(), now: time.Now}

	result, updated, err := f.drawPick(ctx, client, session, lock, false)
	if err != nil {
		t.Fatalf("drawPick: %v", err)
	}
	if !strings.EqualFold(string(result.Card), "g") {
		t.Fatalf("card = %q, want green", result.Card)
	}
	if updated.Picks != 0 {
		t.Fatalf("updated picks = %d, want the state from /loaded", updated.Picks)
	}
	if session.currentLock() == nil || session.currentLock().Picks != 0 {
		t.Fatalf("session lock = %+v, want the refreshed lock", session.currentLock())
	}
}

// TestDrawAfterStaleCardFreeze pins the API behaviour that caused a false
// freeze: ChastiSafe keeps frozenByCard set after a card freeze ends, but
// reports a pick. The app draws, and Locksmith must agree.
func TestDrawAfterStaleCardFreeze(t *testing.T) {
	var locks []cs.LoadedLock
	if err := json.Unmarshal(readFixture(t, "chastisafe/loaded_draw_after_freeze.json"), &locks); err != nil {
		t.Fatalf("unmarshal loaded lock: %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("locks = %d, want 1", len(locks))
	}
	lock := &locks[0]

	if !lock.FrozenByCard {
		t.Fatal("fixture should keep the leftover frozenByCard flag")
	}
	if lock.IsFrozen() {
		t.Fatal("a card lock with a pick available is not frozen")
	}
	if got := lock.DrawsAvailable(); got != 1 {
		t.Fatalf("DrawsAvailable = %d, want 1", got)
	}
	if !canDrawAgain(lock) {
		t.Fatal("canDrawAgain = false, want true for an available pick")
	}
	if got := drawableLock(locks, lock.LockeeID.String()); got == nil || got.ID != lock.ID {
		t.Fatalf("drawableLock = %+v, want the lock with the leftover flag", got)
	}

	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	text := statusText(renderStatusEmbeds(
		[]cs.LockView{{Lock: *lock, Role: cs.RoleLockee}},
		lock.LockeeID.String(),
		nil,
		now,
	))
	for _, want := range []string{":locked: Locked", "195 cards", "1/1 draws"} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"Frozen", "Freeze lasts", "still reports"} {
		if strings.Contains(text, bad) {
			t.Errorf("status should not show %q for a drawable lock:\n%s", bad, text)
		}
	}
}

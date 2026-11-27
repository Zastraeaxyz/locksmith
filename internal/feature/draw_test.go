package feature

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestDrawSessionPermissions(t *testing.T) {
	session := &drawSession{ownerID: "owner", keyholderID: "keyholder"}
	if !session.canUse("owner") {
		t.Fatal("owner should be allowed")
	}
	if !session.canUse("keyholder") {
		t.Fatal("keyholder should be allowed")
	}
	if session.canUse("stranger") {
		t.Fatal("stranger should not be allowed")
	}
	if session.canUse("") {
		t.Fatal("empty user should not be allowed")
	}

	self := &drawSession{ownerID: "owner"}
	if self.canUse("keyholder") {
		t.Fatal("self lock should not allow an external keyholder")
	}
}

func TestRenderDrawLog(t *testing.T) {
	if got := renderDrawLog(nil); !strings.Contains(got, "No draws yet") {
		t.Fatalf("empty log = %q", got)
	}

	log := renderDrawLog([]drawEntry{
		{card: cs.CardGreen},
		{card: cs.CardRed, byKeyholder: true},
		{card: cs.CardDice, dice: 4},
	})
	for _, want := range []string{
		"1. " + cardEmojiMarkup(cs.CardGreen) + " Green card",
		"2. " + cardEmojiMarkup(cs.CardRed) + " Red card · keyholder",
		cardEmojiMarkup(cs.CardDice) + " Dice card (4)",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}

func TestDrawEmbedRunning(t *testing.T) {
	lock := &cs.LoadedLock{
		Type:           "card",
		LockName:       "Test card",
		Picks:          2,
		MaxPicks:       3,
		Wait:           120,
		GreensRequired: 10000,
		Deck:           cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}
	session := &drawSession{
		ownerName:     "Zastraea",
		keyholderID:   "kh",
		keyholderName: "Raven",
		lockName:      "Test card",
	}
	session.setLock(lock)

	embed := session.embed(lock, "A note.")
	if embed.Title != "Test card" {
		t.Fatalf("title = %q", embed.Title)
	}
	if embed.Author != nil {
		t.Fatalf("author should be omitted: %+v", embed.Author)
	}
	if embed.Footer != nil {
		t.Fatalf("footer should be omitted: %+v", embed.Footer)
	}
	if len(embed.Fields) != 0 {
		t.Fatalf("fields should be empty: %d", len(embed.Fields))
	}
	for _, want := range []string{"0 used · 2 left", "A note."} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description missing %q:\n%s", want, embed.Description)
		}
	}
	if strings.Contains(embed.Description, "Summary:") {
		t.Errorf("empty board should not show a summary:\n%s", embed.Description)
	}
	if strings.Contains(embed.Description, "Next draw") {
		t.Errorf("running board should not show a next draw:\n%s", embed.Description)
	}
}

func TestDrawEmbedLiveTotalsCountdown(t *testing.T) {
	lock := &cs.LoadedLock{
		Type:  "card",
		Picks: 2,
		Wait:  120,
		Deck:  cs.Deck{cs.CardGreen: 2, cs.CardRed: 1},
	}
	session := &drawSession{lockName: "Test card"}
	session.setLock(lock)
	session.add(drawEntry{card: cs.CardRed})

	embed := session.embed(lock, "")
	want := "Summary: " + cardEmojiMarkup(cs.CardRed) + " 1 red, " + cardEmojiMarkup(cs.CardGreen) + " 0 green, last card was " + cardEmojiMarkup(cs.CardRed) + ", 3 remaining"
	if !strings.Contains(embed.Description, want) {
		t.Errorf("description missing %q:\n%s", want, embed.Description)
	}

	// The next pick reloads the lock with one green gone and adds it to the log.
	lock.Deck = cs.Deck{cs.CardGreen: 1, cs.CardRed: 1}
	session.setLock(lock)
	session.add(drawEntry{card: cs.CardGreen})

	embed = session.embed(lock, "")
	want = "Summary: " + cardEmojiMarkup(cs.CardRed) + " 1 red, " + cardEmojiMarkup(cs.CardGreen) + " 1 green, last card was " + cardEmojiMarkup(cs.CardGreen) + ", 2 remaining"
	if !strings.Contains(embed.Description, want) {
		t.Errorf("description missing %q:\n%s", want, embed.Description)
	}
}

func TestDrawEmbedFinishedSummary(t *testing.T) {
	lock := &cs.LoadedLock{
		Type:     "card",
		LockName: "Test card",
		Picks:    0,
		Wait:     3271,
		Deck:     cs.Deck{},
	}
	session := &drawSession{lockName: "Test card"}
	session.setLock(lock)
	session.add(drawEntry{card: cs.CardYellowP3})
	session.add(drawEntry{card: cs.CardRed})
	session.add(drawEntry{card: cs.CardDoubleUp})

	embed := session.embed(lock, "")
	for _, want := range []string{
		"1. " + cardEmojiMarkup(cs.CardYellowP3) + " Yellow +3",
		"2. " + cardEmojiMarkup(cs.CardRed) + " Red card",
		"3. " + cardEmojiMarkup(cs.CardDoubleUp) + " Double up card",
		"3 used · 0 left",
		"Next draw <t:",
		"Summary: " + cardEmojiMarkup(cs.CardRed) + " 1 red, " + cardEmojiMarkup(cs.CardGreen) + " 0 green, " + cardEmojiMarkup(cs.CardYellow) + " +3 red, " + cardEmojiMarkup(cs.CardDoubleUp) + " 1 double up, last card was " + cardEmojiMarkup(cs.CardDoubleUp) + ", 0 remaining",
	} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("description missing %q:\n%s", want, embed.Description)
		}
	}
	if strings.Contains(embed.Description, "- yellow +3, red") {
		t.Errorf("finished board should not repeat the draws as a list:\n%s", embed.Description)
	}
}

func TestTotalsLineOmitsMissingCards(t *testing.T) {
	got := totalsLine([]drawEntry{{card: cs.CardGreen}, {card: cs.CardGreen}}, "", 0, false)
	want := "Summary: " + cardEmojiMarkup(cs.CardRed) + " 0 red, " + cardEmojiMarkup(cs.CardGreen) + " 2 green"
	if got != want {
		t.Fatalf("totalsLine = %q, want %q", got, want)
	}
}

func TestTotalsLineShowsRemaining(t *testing.T) {
	got := totalsLine([]drawEntry{{card: cs.CardRed}, {card: cs.CardGreen}}, cs.CardGreen, 5, true)
	want := "Summary: " + cardEmojiMarkup(cs.CardRed) + " 1 red, " + cardEmojiMarkup(cs.CardGreen) + " 1 green, last card was " + cardEmojiMarkup(cs.CardGreen) + ", 5 remaining"
	if got != want {
		t.Fatalf("totalsLine = %q, want %q", got, want)
	}
}

func TestTotalsLineShowsYellowNetRedEffect(t *testing.T) {
	entries := []drawEntry{
		{card: cs.CardYellowM2},
		{card: cs.CardGreen},
		{card: cs.CardYellowP2},
		{card: cs.CardGreen},
		{card: cs.CardYellowM2},
		{card: cs.CardRed},
	}
	got := totalsLine(entries, cs.CardRed, 168, true)
	want := "Summary: " + cardEmojiMarkup(cs.CardRed) + " 1 red, " + cardEmojiMarkup(cs.CardGreen) + " 2 green, " + cardEmojiMarkup(cs.CardYellow) + " -2 red, last card was " + cardEmojiMarkup(cs.CardRed) + ", 168 remaining"
	if got != want {
		t.Fatalf("totalsLine = %q, want %q", got, want)
	}
}

func TestTotalsLineOmitsZeroYellowNet(t *testing.T) {
	got := totalsLine([]drawEntry{{card: cs.CardYellowP2}, {card: cs.CardYellowM2}}, "", 0, false)
	if strings.Contains(got, cardEmojiMarkup(cs.CardYellow)+" +0 red") {
		t.Fatalf("totalsLine should omit a zero yellow net: %q", got)
	}
	want := "Summary: " + cardEmojiMarkup(cs.CardRed) + " 0 red, " + cardEmojiMarkup(cs.CardGreen) + " 0 green"
	if got != want {
		t.Fatalf("totalsLine = %q, want %q", got, want)
	}
}

func TestTotalsLineOmitsHiddenYellows(t *testing.T) {
	got := totalsLine([]drawEntry{
		{card: cs.CardYellow},
		{card: cs.CardYellowUK},
		{card: cs.CardYellowRN},
	}, "", 0, false)
	if strings.Contains(got, cardEmojiMarkup(cs.CardYellow)) {
		t.Fatalf("totalsLine should omit hidden yellows: %q", got)
	}
}

func TestCardEmojiMarkup(t *testing.T) {
	for card, name := range cardEmojiNames {
		if !card.Valid() {
			t.Errorf("cardEmojiNames has invalid card %q", card)
		}
		id, ok := applicationEmojiID(name)
		if !ok {
			t.Errorf("card %q emoji %q has no ID", card, name)
			continue
		}
		want := "<:" + name + ":" + id + ">"
		if got := cardEmojiMarkup(card); got != want {
			t.Errorf("cardEmojiMarkup(%q) = %q, want %q", card, got, want)
		}
	}

	if got := cardEmojiMarkup("UNKNOWN"); got != "" {
		t.Fatalf("unknown card emoji = %q, want empty", got)
	}

	var b strings.Builder
	writeCardEmoji(&b, cs.CardGreen)
	if got, want := b.String(), cardEmojiMarkup(cs.CardGreen)+" "; got != want {
		t.Fatalf("writeCardEmoji = %q, want %q", got, want)
	}
}

func TestCardLogLabel(t *testing.T) {
	tests := map[cs.Card]string{
		cs.CardYellowP1: "Yellow +1",
		cs.CardYellowP2: "Yellow +2",
		cs.CardYellowP3: "Yellow +3",
		cs.CardYellowM1: "Yellow -1",
		cs.CardYellowM2: "Yellow -2",
		cs.CardYellowRN: "Yellow +?",
		cs.CardYellowUK: "Yellow +?",
		cs.CardGreen:    "Green card",
		cs.CardRed:      "Red card",
	}
	for card, want := range tests {
		if got := cardLogLabel(card); got != want {
			t.Errorf("cardLogLabel(%q) = %q, want %q", card, got, want)
		}
	}
}

func TestCardColor(t *testing.T) {
	if cardColor(cs.CardGreen) == cardColor(cs.CardRed) {
		t.Fatal("green and red should differ")
	}
	if cardColor("") == 0 {
		t.Fatal("default color should not be zero")
	}
}

func TestDrawBlockedMessage(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	if got := drawBlockedMessage(nil, nil, now); !strings.Contains(got, "no active card lock") {
		t.Fatalf("nil message = %q", got)
	}

	frozen := &cs.LoadedLock{ID: "f", LockName: "Frozen Lock", Type: "card", FrozenByKeyholder: true}
	got := drawBlockedMessage(frozen, map[cs.ID]time.Time{"f": now.Add(-time.Hour)}, now)
	for _, want := range []string{"Frozen Lock", "frozen for 1hr", "unfrozen"} {
		if !strings.Contains(got, want) {
			t.Errorf("frozen message missing %q:\n%s", want, got)
		}
	}

	got = drawBlockedMessage(frozen, nil, now)
	if strings.Contains(got, "frozen for") {
		t.Errorf("frozen message without history should not claim a duration:\n%s", got)
	}

	ready := &cs.LoadedLock{ID: "r", LockName: "Ready Lock", Type: "card", ReadyToUnlock: true}
	if got := drawBlockedMessage(ready, nil, now); !strings.Contains(got, "ready to unlock") {
		t.Errorf("ready message = %q", got)
	}

	waiting := &cs.LoadedLock{ID: "w", LockName: "Waiting Lock", Type: "card", Picks: 0, Wait: 300}
	if got := drawBlockedMessage(waiting, nil, now); !strings.Contains(got, "Next draw <t:") {
		t.Errorf("waiting message = %q", got)
	}

	waiting.Wait = 0
	if got := drawBlockedMessage(waiting, nil, now); !strings.Contains(got, "No draws available right now") {
		t.Errorf("waiting without timer message = %q", got)
	}

	drawable := &cs.LoadedLock{ID: "d", LockName: "Drawable", Type: "card", Picks: 1}
	if got := drawBlockedMessage(drawable, nil, now); got != "" {
		t.Errorf("drawable message = %q, want empty", got)
	}
}

func TestDrawTargetNewest(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "old", LockName: "Old", Type: "card", LockeeID: "viewer", LoadTime: at(-2 * time.Hour)},
		{ID: "new", LockName: "New", Type: "card", LockeeID: "viewer", LoadTime: at(-time.Hour)},
		{ID: "timer", LockName: "Timer", Type: "timer", LockeeID: "viewer", LoadTime: at(-30 * time.Minute)},
		{ID: "other", LockName: "Other", Type: "card", LockeeID: "someone", LoadTime: at(-10 * time.Minute)},
	}

	got := drawRefusalTarget(locks, "viewer")
	if got == nil || got.ID != "new" {
		t.Fatalf("drawRefusalTarget = %+v, want newest card lock", got)
	}
}

func drawButtonLabels(components []discordgo.MessageComponent) []string {
	if len(components) != 1 {
		return nil
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		return nil
	}
	labels := make([]string, 0, len(row.Components))
	for _, component := range row.Components {
		button, ok := component.(discordgo.Button)
		if !ok {
			continue
		}
		labels = append(labels, button.Label)
	}
	return labels
}

func TestDrawButtonsRunning(t *testing.T) {
	session := &drawSession{id: "session", keyholderID: "keyholder"}
	labels := drawButtonLabels(drawButtons(session, &cs.LoadedLock{Type: "card", Picks: 1}))
	if strings.Join(labels, ",") != "Draw,Stop" {
		t.Fatalf("running buttons = %v, want Draw and Stop only", labels)
	}
}

func TestDrawButtonsReady(t *testing.T) {
	session := &drawSession{id: "session", keyholderID: "keyholder"}
	labels := drawButtonLabels(drawButtons(session, &cs.LoadedLock{Type: "card", ReadyToUnlock: true}))
	want := "Open,Put green back,Reset,Let keyholder decide"
	if strings.Join(labels, ",") != want {
		t.Fatalf("ready buttons = %v, want %s", labels, want)
	}
}

func TestDrawButtonsReadySelfLockHidesKeyholder(t *testing.T) {
	session := &drawSession{id: "session"}
	labels := drawButtonLabels(drawButtons(session, &cs.LoadedLock{Type: "card", ReadyToUnlock: true}))
	want := "Open,Put green back,Reset"
	if strings.Join(labels, ",") != want {
		t.Fatalf("self-lock ready buttons = %v, want %s", labels, want)
	}
}

func TestResetConfirmed(t *testing.T) {
	for _, value := range []string{"YES RESET", "  YES RESET  "} {
		if !resetConfirmed(value) {
			t.Errorf("resetConfirmed(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "yes reset", "YES", "YES  RESET", "NO"} {
		if resetConfirmed(value) {
			t.Errorf("resetConfirmed(%q) = true, want false", value)
		}
	}
}

func TestReadyCardLockSkipsFrozen(t *testing.T) {
	locks := []cs.LoadedLock{
		{ID: "ready", LockName: "Ready", Type: "card", ReadyToUnlock: true, LockeeID: "me"},
		{ID: "frozen", LockName: "Frozen", Type: "card", ReadyToUnlock: true, FrozenByKeyholder: true, LockeeID: "me"},
	}
	got := readyCardLock(locks, "me")
	if got == nil || got.ID != "ready" {
		t.Fatalf("readyCardLock = %+v, want the unfrozen ready lock", got)
	}
}

func TestResolveBoardLockPinsNotificationLock(t *testing.T) {
	f := &Feature{now: openTestNow}
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "notified", LockName: "Notified", Type: "card", Picks: 1, LockeeID: "me"},
		{ID: "other", LockName: "Other", Type: "card", Picks: 2, LockeeID: "me", LoadTime: cs.Time{Time: now}},
	}

	lock, refusal := f.resolveBoardLock(context.Background(), nil, locks, "me", "notified")
	if lock == nil || lock.ID != "notified" {
		t.Fatalf("lock = %+v, refusal = %q, want the notified lock", lock, refusal)
	}
	if refusal != "" {
		t.Fatalf("refusal = %q, want none", refusal)
	}
}

func TestResolveBoardLockRefusals(t *testing.T) {
	f := &Feature{now: openTestNow}
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "theirs", LockName: "Theirs", Type: "card", Picks: 1, LockeeID: "them"},
		{ID: "timer", LockName: "Timer", Type: "timer", LockeeID: "me"},
		{ID: "waiting", LockName: "Waiting", Type: "card", Picks: 0, Wait: 300, LockeeID: "me"},
		{ID: "ready", LockName: "Ready", Type: "card", ReadyToUnlock: true, LockeeID: "me", LoadTime: cs.Time{Time: now}},
	}

	cases := map[string]string{
		"missing": "no longer active",
		"theirs":  "not yours",
		"timer":   "not a card lock",
		"waiting": "No draws available",
	}
	for lockID, want := range cases {
		lock, refusal := f.resolveBoardLock(context.Background(), nil, locks, "me", cs.ID(lockID))
		if lock != nil {
			t.Errorf("%s: lock = %+v, want nil", lockID, lock)
		}
		if !strings.Contains(refusal, want) {
			t.Errorf("%s: refusal = %q, want %q", lockID, refusal, want)
		}
	}

	lock, refusal := f.resolveBoardLock(context.Background(), nil, locks, "me", "ready")
	if lock == nil || lock.ID != "ready" || refusal != "" {
		t.Fatalf("ready lock = %+v, refusal = %q", lock, refusal)
	}
}

func TestResolveBoardLockWithoutIDPrefersDrawable(t *testing.T) {
	f := &Feature{now: openTestNow}
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "drawable", LockName: "Drawable", Type: "card", Picks: 1, LockeeID: "me", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
		{ID: "ready", LockName: "Ready", Type: "card", ReadyToUnlock: true, LockeeID: "me", LoadTime: cs.Time{Time: now}},
	}

	lock, refusal := f.resolveBoardLock(context.Background(), nil, locks, "me", "")
	if lock == nil || lock.ID != "drawable" || refusal != "" {
		t.Fatalf("lock = %+v, refusal = %q, want the drawable lock", lock, refusal)
	}
}

func TestResolveBoardLockWithoutIDExplainsNoDraws(t *testing.T) {
	f := &Feature{now: openTestNow}
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "waiting", LockName: "Waiting", Type: "card", Picks: 0, Wait: 300, LockeeID: "me", LoadTime: cs.Time{Time: now}},
	}

	lock, refusal := f.resolveBoardLock(context.Background(), nil, locks, "me", "")
	if lock != nil {
		t.Fatalf("lock = %+v, want nil", lock)
	}
	for _, want := range []string{"No draws available", "Next draw <t:"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal = %q, want %q", refusal, want)
		}
	}
}

func TestResolveBoardLockWithoutIDPrefersReadyOverWaiting(t *testing.T) {
	f := &Feature{now: openTestNow}
	now := openTestNow()
	locks := []cs.LoadedLock{
		{ID: "waiting", LockName: "Waiting", Type: "card", Picks: 0, Wait: 300, LockeeID: "me", LoadTime: cs.Time{Time: now}},
		{ID: "ready", LockName: "Ready", Type: "card", ReadyToUnlock: true, LockeeID: "me", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
	}

	lock, refusal := f.resolveBoardLock(context.Background(), nil, locks, "me", "")
	if lock == nil || lock.ID != "ready" || refusal != "" {
		t.Fatalf("lock = %+v, refusal = %q, want the ready lock", lock, refusal)
	}
}

package feature

import (
	"strconv"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
)

func TestCheckinIn(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	if got := checkinIn(nil, now); got != 0 {
		t.Errorf("nil lock = %v, want 0", got)
	}
	noCheckin := &cs.LoadedLock{}
	if got := checkinIn(noCheckin, now); got != 0 {
		t.Errorf("no check-in = %v, want 0", got)
	}

	relative := &cs.LoadedLock{CheckInPeriod: 1440, TimeToNextCheckIn: 3600}
	if got := checkinIn(relative, now); got != time.Hour {
		t.Errorf("relative = %v, want 1h", got)
	}

	absolute := &cs.LoadedLock{
		CheckInPeriod:   1440,
		NextCheckInTime: cs.Time{Time: now.Add(90 * time.Minute)},
	}
	if got := checkinIn(absolute, now); got != 90*time.Minute {
		t.Errorf("absolute = %v, want 90m", got)
	}

	overdue := &cs.LoadedLock{CheckInPeriod: 1440, TimeToNextCheckIn: -60}
	if got := checkinIn(overdue, now); got != -time.Minute {
		t.Errorf("overdue = %v, want -1m", got)
	}
}

func TestCheckinReminderStages(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &Feature{checkinLead: time.Hour}

	lock := func(remaining time.Duration) *cs.LoadedLock {
		return &cs.LoadedLock{
			CheckInPeriod:     1440,
			TimeToNextCheckIn: int(remaining / time.Second),
		}
	}

	cases := []struct {
		name     string
		lock     *cs.LoadedLock
		prev     store.Notification
		wantSend string
		wantRank int
	}{
		{
			name: "outside lead",
			lock: lock(3 * time.Hour),
		},
		{
			name:     "lead reminder",
			lock:     lock(30 * time.Minute),
			wantSend: "lead",
			wantRank: 1,
		},
		{
			name: "lead already sent",
			lock: lock(30 * time.Minute),
			prev: store.Notification{
				CheckinDueAt: now.Add(30 * time.Minute),
				CheckinStage: "lead",
			},
			wantRank: 1,
		},
		{
			name:     "five minute reminder",
			lock:     lock(4 * time.Minute),
			wantSend: "final",
			wantRank: 2,
		},
		{
			name:     "lead skipped when lead is short",
			lock:     lock(4 * time.Minute),
			prev:     store.Notification{CheckinDueAt: now.Add(4 * time.Minute)},
			wantSend: "final",
			wantRank: 2,
		},
		{
			name:     "overdue reminder",
			lock:     lock(0),
			wantSend: "overdue",
			wantRank: 3,
		},
		{
			name: "overdue already sent",
			lock: lock(0),
			prev: store.Notification{
				CheckinDueAt: now,
				CheckinStage: "overdue",
			},
			wantRank: 3,
		},
		{
			name: "new cycle stays quiet",
			lock: lock(23 * time.Hour),
			prev: store.Notification{
				CheckinDueAt: now.Add(-time.Hour),
				CheckinStage: "overdue",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := f.checkinReminderFor(tc.lock, now, tc.prev)
			if got.Send != tc.wantSend {
				t.Errorf("Send = %q, want %q", got.Send, tc.wantSend)
			}
			if rank := checkinStageRank(got.Stage); rank != tc.wantRank {
				t.Errorf("Stage = %q (rank %d), want rank %d", got.Stage, rank, tc.wantRank)
			}
		})
	}
}

func TestCheckinReminderLeadDisabled(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &Feature{checkinLead: 0}

	lead := &cs.LoadedLock{CheckInPeriod: 1440, TimeToNextCheckIn: 1800}
	if got := f.checkinReminderFor(lead, now, store.Notification{}); got.Send != "" {
		t.Errorf("lead with disabled lead time sent %q", got.Send)
	}

	final := &cs.LoadedLock{CheckInPeriod: 1440, TimeToNextCheckIn: 240}
	if got := f.checkinReminderFor(final, now, store.Notification{}); got.Send != "final" {
		t.Errorf("five minute stage = %q, want final", got.Send)
	}
}

func TestCheckinReminderIgnoresMissingDeadline(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &Feature{checkinLead: time.Hour}

	// No timestamp and no relative time means the check-in is due, but the
	// cycle must be stable across polls.
	lock := &cs.LoadedLock{CheckInPeriod: 1440}
	first := f.checkinReminderFor(lock, now, store.Notification{})
	if first.Send != "overdue" || first.Due.IsZero() {
		t.Fatalf("first = %+v", first)
	}
	second := f.checkinReminderFor(lock, now.Add(time.Minute), store.Notification{
		CheckinDueAt: first.Due,
		CheckinStage: first.Stage,
	})
	if second.Send != "" {
		t.Errorf("second Send = %q, want empty", second.Send)
	}
	if !second.Due.Equal(first.Due) {
		t.Errorf("Due moved from %v to %v", first.Due, second.Due)
	}
}

func TestNextCheckinCheck(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &Feature{checkinLead: time.Hour}

	lock := func(remaining time.Duration) *cs.LoadedLock {
		return &cs.LoadedLock{
			CheckInPeriod:     1440,
			TimeToNextCheckIn: int(remaining / time.Second),
		}
	}

	cases := []struct {
		name string
		lock *cs.LoadedLock
		want time.Duration
	}{
		{"none", &cs.LoadedLock{}, 0},
		{"far out", lock(3 * time.Hour), 2*time.Hour + 2*time.Second},
		{"inside lead", lock(30 * time.Minute), 25*time.Minute + 2*time.Second},
		{"inside final", lock(3 * time.Minute), 3*time.Minute + 2*time.Second},
		{"overdue", lock(0), 5 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := f.nextCheckinCheck(tc.lock, now)
			if tc.want == 0 {
				if !got.IsZero() {
					t.Errorf("got %v, want zero", got)
				}
				return
			}
			if want := now.Add(tc.want); !got.Equal(want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestCheckinTextAndButton(t *testing.T) {
	due := time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)
	lock := &cs.LoadedLock{ID: "lock-1", LockName: "Cage"}

	for _, stage := range []string{"lead", "final", "overdue"} {
		text := checkinText(lock, stage, due)
		if text == "" {
			t.Fatalf("%s text is empty", stage)
		}
		if got := textTimestamp(text); !got.Equal(due) {
			t.Errorf("%s timestamp = %v, want %v", stage, got, due)
		}
	}

	components := checkinButtons(lock)
	if len(components) != 1 {
		t.Fatalf("components = %d, want 1", len(components))
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component = %T", components[0])
	}
	button, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("button = %T", row.Components[0])
	}
	if button.Label != "Check-in" {
		t.Errorf("label = %q", button.Label)
	}
	if button.CustomID != "checkin:lock-1" {
		t.Errorf("custom id = %q", button.CustomID)
	}
	if button.Style != discordgo.PrimaryButton {
		t.Errorf("style = %v", button.Style)
	}
}

// textTimestamp extracts the first Discord timestamp from text.
func textTimestamp(text string) time.Time {
	start := -1
	for i := 0; i+2 < len(text); i++ {
		if text[i] == '<' && text[i+1] == 't' && text[i+2] == ':' {
			start = i + 3
			break
		}
	}
	if start < 0 {
		return time.Time{}
	}
	end := start
	for end < len(text) && text[end] != ':' && text[end] != '>' {
		end++
	}
	seconds, err := strconv.ParseInt(text[start:end], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func TestActiveCheckinLock(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	locks := []cs.LoadedLock{
		{ID: "no-checkin", Type: "timer", LockeeID: "me"},
		{ID: "keyholder", Type: "timer", LockeeID: "other", KeyholderID: "me", CheckInPeriod: 60},
		{ID: "ready", Type: "timer", LockeeID: "me", CheckInPeriod: 60, ReadyToUnlock: true},
		{ID: "later", Type: "timer", LockeeID: "me", CheckInPeriod: 1440, TimeToNextCheckIn: 7200},
		{ID: "sooner", Type: "card", LockeeID: "me", CheckInPeriod: 1440, TimeToNextCheckIn: 600},
		{ID: "overdue", Type: "timer", LockeeID: "me", CheckInPeriod: 1440},
	}

	got := activeCheckinLock(locks, "me", now)
	if got == nil || got.ID != "overdue" {
		t.Fatalf("picked %+v, want overdue", got)
	}

	if got := activeCheckinLock(locks, "someone-else", now); got != nil {
		t.Fatalf("picked %+v for another member", got)
	}

	if got := activeCheckinLock(nil, "me", now); got != nil {
		t.Fatalf("picked %+v from no locks", got)
	}
}

func TestNextNotificationCheckCheckinOnly(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &Feature{checkinLead: time.Hour}

	lock := &cs.LoadedLock{
		ID:                "timer-1",
		Type:              "timer",
		LockeeID:          "me",
		CheckInPeriod:     1440,
		TimeToNextCheckIn: 3 * 3600,
	}

	got := f.nextNotificationCheck(lock, now, 0, false)
	want := now.Add(2*time.Hour + 2*time.Second)
	if !got.Equal(want) {
		t.Fatalf("next = %v, want %v", got, want)
	}

	// A lock with neither draws nor check-ins still wakes up eventually.
	plain := &cs.LoadedLock{ID: "plain", Type: "timer", LockeeID: "me"}
	got = f.nextNotificationCheck(plain, now, 0, false)
	if want := now.Add(15 * time.Minute); !got.Equal(want) {
		t.Fatalf("plain next = %v, want %v", got, want)
	}
}

func TestFindLockFallbacks(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	locks := []cs.LoadedLock{
		{ID: "timer", Type: "timer", LockeeID: "me", CheckInPeriod: 1440, TimeToNextCheckIn: 3600},
	}

	got := findLock(locks, "gone", "me", now)
	if got == nil || got.ID != "timer" {
		t.Fatalf("check-in fallback = %+v, want timer", got)
	}

	withCard := append(locks, cs.LoadedLock{ID: "card", Type: "card", LockeeID: "me"})
	got = findLock(withCard, "gone", "me", now)
	if got == nil || got.ID != "card" {
		t.Fatalf("card fallback = %+v, want card", got)
	}
}

func TestPickCheckinLock(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	locks := []cs.LoadedLock{
		{ID: "no-checkin", Type: "timer", LockName: "No checkin", LockeeID: "me"},
		{ID: "later", Type: "timer", LockName: "Later", LockeeID: "me", CheckInPeriod: 1440, TimeToNextCheckIn: 7200},
		{ID: "sooner", Type: "card", LockName: "Sooner", LockeeID: "me", CheckInPeriod: 1440, TimeToNextCheckIn: 600},
		{ID: "overdue", Type: "timer", LockName: "Overdue", LockeeID: "me", CheckInPeriod: 1440},
		{ID: "ready", Type: "timer", LockName: "Ready", LockeeID: "me", CheckInPeriod: 1440, ReadyToUnlock: true},
	}

	got, note, err := pickCheckinLock("", locks, now)
	if err != nil {
		t.Fatalf("no ref: %v", err)
	}
	if got == nil || got.ID != "overdue" {
		t.Fatalf("no ref picked %+v, want overdue", got)
	}
	if note == "" {
		t.Fatal("multiple check-in locks should add a note")
	}

	got, note, err = pickCheckinLock("later", locks, now)
	if err != nil {
		t.Fatalf("by name: %v", err)
	}
	if got == nil || got.ID != "later" || note != "" {
		t.Fatalf("by name = %+v, note %q", got, note)
	}

	if _, _, err := pickCheckinLock("no-checkin", locks, now); err == nil {
		t.Fatal("lock without check-ins should fail")
	}
	if _, _, err := pickCheckinLock("ready", locks, now); err == nil {
		t.Fatal("ready lock should fail")
	}
	if _, _, err := pickCheckinLock("missing", locks, now); err == nil {
		t.Fatal("unknown lock should fail")
	}
	if _, _, err := pickCheckinLock("", []cs.LoadedLock{{ID: "plain", Type: "timer"}}, now); err == nil {
		t.Fatal("no check-in locks should fail")
	}
}

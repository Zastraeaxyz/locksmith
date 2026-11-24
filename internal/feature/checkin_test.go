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

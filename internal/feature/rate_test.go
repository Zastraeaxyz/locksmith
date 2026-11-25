package feature

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestRateTarget(t *testing.T) {
	cases := []struct {
		name    string
		recent  []cs.RecentLock
		want    string
		problem string
	}{
		{
			name:    "no completed locks",
			recent:  nil,
			problem: "no completed locks",
		},
		{
			name:    "only a fake lock",
			recent:  []cs.RecentLock{{LockName: "Fake", Fake: true}},
			problem: "no completed locks",
		},
		{
			name: "newest lock wins",
			recent: []cs.RecentLock{
				{LockID: "older", LockName: "Older", UnlockTime: cs.Time{Time: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}},
				{LockID: "faker", LockName: "Fake", Fake: true, UnlockTime: cs.Time{Time: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)}},
				{LockID: "newest", LockName: "Newest", UnlockTime: cs.Time{Time: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}},
			},
			want: "newest",
		},
		{
			name:    "test lock",
			recent:  []cs.RecentLock{{LockName: "Test", LockPurpose: cs.PurposeTest}},
			problem: "test lock",
		},
		{
			name:    "self lock",
			recent:  []cs.RecentLock{{LockName: "Self", Keyholder: "<self>"}},
			problem: "self lock",
		},
		{
			name:    "abandoned lock",
			recent:  []cs.RecentLock{{LockName: "Abandoned", UnlockReason: cs.UnlockReasonAbandoned}},
			problem: "abandoned",
		},
		{
			name:    "already rated",
			recent:  []cs.RecentLock{{LockName: "Rated", Rating: 4}},
			problem: "already rated 4/5",
		},
		{
			name:   "ratable",
			recent: []cs.RecentLock{{LockID: "lock-id", LockName: "Ready"}},
			want:   "lock-id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lock, problem := rateTarget(tc.recent)
			if tc.want == "" {
				if lock != nil {
					t.Fatalf("lock = %+v, want nil", lock)
				}
				if !strings.Contains(problem, tc.problem) {
					t.Errorf("problem = %q, want it to contain %q", problem, tc.problem)
				}
				return
			}
			if lock == nil || lock.LockID.String() != tc.want {
				t.Fatalf("lock = %+v, want %s", lock, tc.want)
			}
			if problem != "" {
				t.Errorf("problem = %q, want empty", problem)
			}
		})
	}
}

func TestRateButtons(t *testing.T) {
	rows := rateButtons("lock-id")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row, ok := rows[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("row type = %T", rows[0])
	}
	if len(row.Components) != 5 {
		t.Fatalf("buttons = %d, want 5", len(row.Components))
	}
	for idx, component := range row.Components {
		button, ok := component.(discordgo.Button)
		if !ok {
			t.Fatalf("component %d type = %T", idx, component)
		}
		wantID := "rate:lock-id:" + string(rune('1'+idx))
		if button.CustomID != wantID {
			t.Errorf("custom id = %q, want %q", button.CustomID, wantID)
		}
		if button.Label != string(rune('1'+idx)) {
			t.Errorf("label = %q", button.Label)
		}
		if button.Emoji == nil || button.Emoji.Name != "⭐" {
			t.Errorf("emoji = %+v", button.Emoji)
		}
	}
}

func TestCanRateLock(t *testing.T) {
	cases := []struct {
		name string
		lock *cs.LoadedLock
		want bool
	}{
		{name: "nil", lock: nil, want: false},
		{name: "self lock", lock: &cs.LoadedLock{Keyholder: "<self>"}, want: false},
		{name: "test lock", lock: &cs.LoadedLock{KeyholderID: "kh", LockPurpose: cs.PurposeTest}, want: false},
		{name: "normal lock", lock: &cs.LoadedLock{KeyholderID: "kh", LockPurpose: cs.PurposeChastity}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canRateLock(tc.lock); got != tc.want {
				t.Errorf("canRateLock = %v, want %v", got, tc.want)
			}
		})
	}
}

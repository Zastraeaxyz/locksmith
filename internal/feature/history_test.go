package feature

import (
	"strings"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func historyTestNow() time.Time {
	return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
}

func TestHistoryDefaultsFilterTestAndOld(t *testing.T) {
	now := historyTestNow()
	recent := []cs.RecentLock{
		{
			LockID:      "recent",
			LockName:    "Short lock",
			LockPurpose: cs.PurposeChastity,
			UnlockTime:  cs.Time{Time: now.Add(-24 * time.Hour)},
		},
		{
			LockID:      "test",
			LockName:    "Test lock",
			LockPurpose: cs.PurposeTest,
			UnlockTime:  cs.Time{Time: now.Add(-48 * time.Hour)},
		},
		{
			LockID:      "old",
			LockName:    "Long lock",
			LockPurpose: cs.PurposeChastity,
			UnlockTime:  cs.Time{Time: now.AddDate(-2, 0, 0)},
		},
	}

	view := newHistoryView(recent, historyOptions{now: now})
	if len(view.entries) != 1 || view.entries[0].LockID != "recent" {
		t.Fatalf("entries = %+v, want only the recent lock", view.entries)
	}
	if view.hiddenTest != 1 {
		t.Errorf("hiddenTest = %d, want 1", view.hiddenTest)
	}
	if view.hiddenOld != 1 {
		t.Errorf("hiddenOld = %d, want 1", view.hiddenOld)
	}
}

func TestHistoryIncludeFlags(t *testing.T) {
	now := historyTestNow()
	recent := []cs.RecentLock{
		{LockID: "test", LockPurpose: cs.PurposeTest, UnlockTime: cs.Time{Time: now.Add(-time.Hour)}},
		{LockID: "old", LockPurpose: cs.PurposeBondage, UnlockTime: cs.Time{Time: now.AddDate(-3, 0, 0)}},
	}

	view := newHistoryView(recent, historyOptions{includeTest: true, includeOld: true, now: now})
	if len(view.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(view.entries))
	}
	if view.hiddenTest != 0 || view.hiddenOld != 0 {
		t.Fatalf("hidden counts = %d/%d, want 0/0", view.hiddenTest, view.hiddenOld)
	}
}

func TestHistorySortsNewestFirst(t *testing.T) {
	now := historyTestNow()
	recent := []cs.RecentLock{
		{LockID: "middle", UnlockTime: cs.Time{Time: now.Add(-48 * time.Hour)}},
		{LockID: "oldest", UnlockTime: cs.Time{Time: now.Add(-72 * time.Hour)}},
		{LockID: "newest", UnlockTime: cs.Time{Time: now.Add(-24 * time.Hour)}},
	}

	view := newHistoryView(recent, historyOptions{now: now})
	if len(view.entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(view.entries))
	}
	for idx, want := range []cs.ID{"newest", "middle", "oldest"} {
		if view.entries[idx].LockID != want {
			t.Errorf("entries[%d] = %q, want %q", idx, view.entries[idx].LockID, want)
		}
	}
}

func TestHistoryKeepsLocksWithoutTimestamps(t *testing.T) {
	view := newHistoryView([]cs.RecentLock{{LockID: "bare"}}, historyOptions{now: historyTestNow()})
	if len(view.entries) != 1 || view.entries[0].LockID != "bare" {
		t.Fatalf("entries = %+v, want the untimed lock", view.entries)
	}
}

func TestHistoryFallsBackToLoadTime(t *testing.T) {
	now := historyTestNow()
	recent := []cs.RecentLock{
		{LockID: "old", LoadTime: cs.Time{Time: now.AddDate(-2, 0, 0)}},
		{LockID: "recent", LoadTime: cs.Time{Time: now.Add(-time.Hour)}},
	}

	view := newHistoryView(recent, historyOptions{now: now})
	if len(view.entries) != 1 || view.entries[0].LockID != "recent" {
		t.Fatalf("entries = %+v, want the recent lock", view.entries)
	}
	if view.hiddenOld != 1 {
		t.Errorf("hiddenOld = %d, want 1", view.hiddenOld)
	}
}

func TestHistoryCapsEntries(t *testing.T) {
	now := historyTestNow()
	recent := make([]cs.RecentLock, 0, historyEntryLimit+5)
	for idx := 0; idx < historyEntryLimit+5; idx++ {
		recent = append(recent, cs.RecentLock{
			LockID:     "lock",
			UnlockTime: cs.Time{Time: now.Add(-time.Duration(idx) * time.Hour)},
		})
	}

	view := newHistoryView(recent, historyOptions{now: now})
	if len(view.entries) != historyEntryLimit {
		t.Fatalf("entries = %d, want %d", len(view.entries), historyEntryLimit)
	}
	if view.truncated != 5 {
		t.Fatalf("truncated = %d, want 5", view.truncated)
	}
	if got := view.entries[0].UnlockTime.Time; !got.Equal(now) {
		t.Errorf("first entry = %s, want the newest lock", got)
	}
}

func TestRenderHistoryLine(t *testing.T) {
	now := historyTestNow()
	lock := cs.RecentLock{
		LockID:       "lock",
		LockName:     "Short lock",
		Keyholder:    "Raven",
		UnlockReason: cs.UnlockReasonCompleted,
		UnlockTime:   cs.Time{Time: now},
		LockDuration: int((4*time.Hour + 12*time.Minute).Seconds()),
		LockPurpose:  cs.PurposeChastity,
		Rating:       5,
	}

	text := renderHistory(historyView{entries: []cs.RecentLock{lock}})
	for _, want := range []string{
		"**Your lock history** (1)",
		"• **Short lock** · 4h 12m · completed · <t:",
		"keyholder Raven",
		"rated 5",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("history missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"CHASTITY", "Purpose:"} {
		if strings.Contains(text, bad) {
			t.Errorf("history should omit %q:\n%s", bad, text)
		}
	}
}

func TestRenderHistoryPurposeAndSelfKeyholder(t *testing.T) {
	now := historyTestNow()
	lock := cs.RecentLock{
		LockName:    "Test lock",
		Keyholder:   "<self>",
		LockPurpose: cs.PurposeTest,
		UnlockTime:  cs.Time{Time: now},
	}

	text := renderHistory(historyView{entries: []cs.RecentLock{lock}})
	if !strings.Contains(text, "TEST") {
		t.Errorf("history should tag a TEST lock:\n%s", text)
	}
	if strings.Contains(text, "keyholder") {
		t.Errorf("history should omit a self keyholder:\n%s", text)
	}
}

func TestRenderHistoryEmpty(t *testing.T) {
	if got := renderHistory(historyView{}); got != "You have no completed ChastiSafe locks." {
		t.Fatalf("empty history = %q", got)
	}

	text := renderHistory(historyView{hiddenTest: 2, hiddenOld: 1})
	for _, want := range []string{
		"No completed locks to show.",
		"2 TEST locks hidden",
		"include_test:true",
		"1 lock older than a year hidden",
		"include_old:true",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("hidden history missing %q:\n%s", want, text)
		}
	}
}

func TestHistoryDuration(t *testing.T) {
	tests := map[time.Duration]string{
		0:                            "",
		30 * time.Second:             "<1m",
		8 * time.Minute:              "8m",
		5 * time.Hour:                "5h",
		5*time.Hour + 12*time.Minute: "5h 12m",
		48 * time.Hour:               "2d",
		52 * time.Hour:               "2d 4h",
	}
	for input, want := range tests {
		if got := historyDuration(input); got != want {
			t.Errorf("historyDuration(%s) = %q, want %q", input, got, want)
		}
	}
}

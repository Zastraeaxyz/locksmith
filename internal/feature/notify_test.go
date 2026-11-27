package feature

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
)

func openNotificationStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNextFreezeCheckAt(t *testing.T) {
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	hour := time.Hour

	tests := []struct {
		name     string
		interval time.Duration
		now      time.Time
		want     time.Time
		ok       bool
	}{
		{
			name:     "before the two interval mark",
			interval: hour,
			now:      since.Add(30 * time.Minute),
			want:     since.Add(2 * hour).Add(2 * time.Second),
			ok:       true,
		},
		{
			name:     "at the two interval mark moves to the next step",
			interval: hour,
			now:      since.Add(2 * hour),
			want:     since.Add(150 * time.Minute).Add(2 * time.Second),
			ok:       true,
		},
		{
			name:     "between the first two steps",
			interval: hour,
			now:      since.Add(140 * time.Minute),
			want:     since.Add(150 * time.Minute).Add(2 * time.Second),
			ok:       true,
		},
		{
			name:     "just before the final mark",
			interval: hour,
			now:      since.Add(4*hour - time.Minute),
			want:     since.Add(4 * hour).Add(2 * time.Second),
			ok:       true,
		},
		{
			name:     "at the final mark stops",
			interval: hour,
			now:      since.Add(4 * hour),
			ok:       false,
		},
		{
			name:     "after the final mark stops",
			interval: hour,
			now:      since.Add(6 * hour),
			ok:       false,
		},
		{
			name:     "short draw interval scales",
			interval: 20 * time.Minute,
			now:      since.Add(50 * time.Minute),
			want:     since.Add(60 * time.Minute).Add(2 * time.Second),
			ok:       true,
		},
		{
			name:     "long draw interval scales",
			interval: 3 * hour,
			now:      since.Add(5 * hour),
			want:     since.Add(6 * hour).Add(2 * time.Second),
			ok:       true,
		},
		{
			name:     "zero interval has no checkpoints",
			interval: 0,
			now:      since,
			ok:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := nextFreezeCheckAt(since, tt.interval, tt.now)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				if !got.IsZero() {
					t.Fatalf("time = %v, want zero", got)
				}
				return
			}
			if !got.Equal(tt.want) {
				t.Fatalf("time = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextFreezeCheckFallsBackForKeyholder(t *testing.T) {
	f := New(Options{})
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	window := freezeWindow{
		since:     now.Add(-time.Hour),
		interval:  time.Hour,
		keyholder: true,
		ok:        true,
	}

	got := f.nextFreezeCheck(window, now)
	want := now.Add(15 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("nextFreezeCheck = %v, want %v", got, want)
	}
}

func TestNextFreezeCheckFallsBackWithoutRegularity(t *testing.T) {
	f := New(Options{})
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	window := freezeWindow{since: now.Add(-time.Hour), ok: true}

	got := f.nextFreezeCheck(window, now)
	want := now.Add(15 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("nextFreezeCheck = %v, want %v", got, want)
	}
}

func TestNextFreezeCheckKeepsPollingAfterTheWindow(t *testing.T) {
	f := New(Options{})
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	now := since.Add(5 * time.Hour)
	window := freezeWindow{since: since, interval: time.Hour, ok: true}

	got := f.nextFreezeCheck(window, now)
	want := now.Add(15 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("nextFreezeCheck = %v, want %v", got, want)
	}
}

func TestNextFreezeCheckUsesCheckpointsInsideTheWindow(t *testing.T) {
	f := New(Options{})
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	now := since.Add(30 * time.Minute)
	window := freezeWindow{since: since, interval: time.Hour, ok: true}

	got := f.nextFreezeCheck(window, now)
	want := since.Add(2 * time.Hour).Add(2 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("nextFreezeCheck = %v, want %v", got, want)
	}
}

func TestFreezeGuaranteeAt(t *testing.T) {
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	if got, want := freezeGuaranteeAt(since, time.Hour), since.Add(4*time.Hour); !got.Equal(want) {
		t.Fatalf("freezeGuaranteeAt = %v, want %v", got, want)
	}
	if got := freezeGuaranteeAt(since, 0); !got.IsZero() {
		t.Fatalf("zero interval = %v, want zero", got)
	}
	if got := freezeGuaranteeAt(time.Time{}, time.Hour); !got.IsZero() {
		t.Fatalf("zero start = %v, want zero", got)
	}
}

func TestTrackFreezeArmsTheGuarantee(t *testing.T) {
	ctx := context.Background()
	st := openNotificationStore(t)
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	now := since.Add(time.Hour)
	f := &Feature{store: st, now: func() time.Time { return now }, log: slog.Default()}

	if err := st.Watch(ctx, store.Watch{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		NextCheckAt:   now,
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	n, err := st.Notification(ctx, "user-1")
	if err != nil || n == nil {
		t.Fatalf("Notification: %v, %+v", err, n)
	}
	lock := &cs.LoadedLock{ID: "lock-1", Type: "card", FrozenByCard: true, Regularity: 60}

	got := f.trackFreeze(ctx, "user-1", lock, *n, freezeWindow{
		since: since, interval: time.Hour, ok: true,
	}, now)

	wantPing := since.Add(4 * time.Hour)
	if !got.FreezeStartAt.Equal(since) || !got.FreezePingAt.Equal(wantPing) {
		t.Fatalf("freeze state = %+v, want start %v ping %v", got, since, wantPing)
	}
	stored, err := st.Notification(ctx, "user-1")
	if err != nil || stored == nil {
		t.Fatalf("Notification after tracking: %v, %+v", err, stored)
	}
	if !stored.FreezeStartAt.Equal(since) || !stored.FreezePingAt.Equal(wantPing) {
		t.Fatalf("stored freeze = %+v", stored)
	}
}

func TestTrackFreezeSkipsAStaleGuarantee(t *testing.T) {
	ctx := context.Background()
	st := openNotificationStore(t)
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	now := since.Add(5 * time.Hour)
	f := &Feature{store: st, now: func() time.Time { return now }, log: slog.Default()}

	if err := st.Watch(ctx, store.Watch{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		NextCheckAt:   now,
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	n, err := st.Notification(ctx, "user-1")
	if err != nil || n == nil {
		t.Fatalf("Notification: %v, %+v", err, n)
	}
	lock := &cs.LoadedLock{ID: "lock-1", Type: "card", FrozenByCard: true, Regularity: 60}

	got := f.trackFreeze(ctx, "user-1", lock, *n, freezeWindow{
		since: since, interval: time.Hour, ok: true,
	}, now)

	if !got.FreezeStartAt.Equal(since) {
		t.Fatalf("start = %v, want %v", got.FreezeStartAt, since)
	}
	if !got.FreezePingAt.IsZero() {
		t.Fatalf("ping = %v, want none", got.FreezePingAt)
	}
}

func TestTrackFreezeIgnoresTheKeyholderGuarantee(t *testing.T) {
	ctx := context.Background()
	st := openNotificationStore(t)
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	now := since.Add(time.Hour)
	f := &Feature{store: st, now: func() time.Time { return now }, log: slog.Default()}

	if err := st.Watch(ctx, store.Watch{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		NextCheckAt:   now,
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	n, err := st.Notification(ctx, "user-1")
	if err != nil || n == nil {
		t.Fatalf("Notification: %v, %+v", err, n)
	}
	lock := &cs.LoadedLock{ID: "lock-1", Type: "card", FrozenByKeyholder: true, Regularity: 60}

	got := f.trackFreeze(ctx, "user-1", lock, *n, freezeWindow{
		since: since, interval: time.Hour, keyholder: true, ok: true,
	}, now)

	if !got.FreezeStartAt.Equal(since) || !got.FreezePingAt.IsZero() {
		t.Fatalf("freeze state = %+v", got)
	}
}

func TestSupersededNotificationMessages(t *testing.T) {
	messages := []store.NotificationMessage{
		{MessageID: "draw-old", Kind: notificationKindDraw},
		{MessageID: "draw-new", Kind: notificationKindDraw},
		{MessageID: "checkin", Kind: notificationKindCheckin},
	}
	got := supersededNotificationMessages(messages, notificationKindDraw, "draw-new")
	if len(got) != 1 || got[0].MessageID != "draw-old" {
		t.Fatalf("superseded = %+v", got)
	}
	if got := supersededNotificationMessages(messages, notificationKindNotice, "notice"); got != nil {
		t.Fatalf("notice superseded = %+v, want none", got)
	}
}

func TestExpireNotificationMessages(t *testing.T) {
	ctx := context.Background()
	st := openNotificationStore(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	f := &Feature{store: st, now: func() time.Time { return now }, log: slog.Default()}

	for _, message := range []store.NotificationMessage{
		{MessageID: "stale", DiscordUserID: "user-1", ChannelID: "dm-1", Kind: notificationKindDraw,
			CreatedAt: now.Add(-25 * time.Hour), DeleteAt: now.Add(-time.Hour)},
		{MessageID: "fresh", DiscordUserID: "user-1", ChannelID: "dm-1", Kind: notificationKindCheckin,
			CreatedAt: now, DeleteAt: now.Add(notificationMessageTTL)},
	} {
		if err := st.RecordNotificationMessage(ctx, message); err != nil {
			t.Fatalf("RecordNotificationMessage: %v", err)
		}
	}

	f.expireNotificationMessages(ctx, nil)

	got, err := st.NotificationMessages(ctx, "user-1")
	if err != nil {
		t.Fatalf("NotificationMessages: %v", err)
	}
	if len(got) != 1 || got[0].MessageID != "fresh" {
		t.Fatalf("remaining messages = %+v", got)
	}
}

func TestWatchLockClearsPreviousLockMessages(t *testing.T) {
	ctx := context.Background()
	st := openNotificationStore(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	f := &Feature{store: st, now: func() time.Time { return now }, log: slog.Default()}

	if err := st.Watch(ctx, store.Watch{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		NextCheckAt:   now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if err := st.RecordNotificationMessage(ctx, store.NotificationMessage{
		MessageID:     "m-1",
		DiscordUserID: "user-1",
		ChannelID:     "dm-1",
		Kind:          notificationKindDraw,
		CreatedAt:     now,
		DeleteAt:      now.Add(notificationMessageTTL),
	}); err != nil {
		t.Fatalf("RecordNotificationMessage: %v", err)
	}

	// Re-subscribing to the same lock keeps the current notification.
	if err := f.watchLock(ctx, nil, "user-1", &cs.LoadedLock{ID: "lock-1", Type: "card", Draws: "rg", Picks: 1}); err != nil {
		t.Fatalf("watchLock same lock: %v", err)
	}
	got, err := st.NotificationMessages(ctx, "user-1")
	if err != nil {
		t.Fatalf("NotificationMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("same-lock messages = %d, want 1", len(got))
	}

	// Switching locks drops the old lock's notification DMs.
	if err := f.watchLock(ctx, nil, "user-1", &cs.LoadedLock{ID: "lock-2", Type: "card", Draws: "rg", Picks: 1}); err != nil {
		t.Fatalf("watchLock new lock: %v", err)
	}
	got, err = st.NotificationMessages(ctx, "user-1")
	if err != nil {
		t.Fatalf("NotificationMessages after switch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("after switching locks = %+v, want none", got)
	}
}

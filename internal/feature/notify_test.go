package feature

import (
	"testing"
	"time"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

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
	lock := &cs.LoadedLock{Type: "card", FrozenByKeyholder: true, Regularity: 60}

	got := f.nextFreezeCheck(t.Context(), nil, lock, now)
	want := now.Add(15 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("nextFreezeCheck = %v, want %v", got, want)
	}
}

func TestNextFreezeCheckFallsBackWithoutRegularity(t *testing.T) {
	f := New(Options{})
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	lock := &cs.LoadedLock{Type: "card", FrozenByCard: true}

	got := f.nextFreezeCheck(t.Context(), nil, lock, now)
	want := now.Add(15 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("nextFreezeCheck = %v, want %v", got, want)
	}
}

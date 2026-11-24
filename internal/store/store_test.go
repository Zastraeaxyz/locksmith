package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCredentialRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	cred := &Credential{
		DiscordUserID:  "user-1",
		ChastiSafeID:   "900000000000000001",
		ChastiSafeUser: "zastraea#0",
		Secret:         []byte("sealed"),
	}
	if err := st.SaveCredential(ctx, cred); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	got, err := st.Credential(ctx, "user-1")
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if got == nil {
		t.Fatal("Credential returned nil")
	}
	if got.ChastiSafeID != cred.ChastiSafeID || got.ChastiSafeUser != cred.ChastiSafeUser {
		t.Fatalf("got %+v", got)
	}
	if string(got.Secret) != "sealed" {
		t.Fatalf("secret = %q", got.Secret)
	}

	if err := st.DeleteCredential(ctx, "user-1"); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	gone, err := st.Credential(ctx, "user-1")
	if err != nil {
		t.Fatalf("Credential after delete: %v", err)
	}
	if gone != nil {
		t.Fatalf("credential still present: %+v", gone)
	}
}

func TestNotificationDue(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	n := &Notification{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		LastDraws:     "g",
		NextCheckAt:   now.Add(-time.Minute),
	}
	if err := st.Subscribe(ctx, n); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	due, err := st.DueNotifications(ctx, now)
	if err != nil {
		t.Fatalf("DueNotifications: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %d, want 1", len(due))
	}
	if due[0].LockID != "lock-1" {
		t.Fatalf("lock id = %q", due[0].LockID)
	}

	n.NotifiedReady = true
	n.NextCheckAt = now.Add(time.Hour)
	if err := st.Subscribe(ctx, n); err != nil {
		t.Fatalf("Subscribe update: %v", err)
	}
	due, err = st.DueNotifications(ctx, now)
	if err != nil {
		t.Fatalf("DueNotifications after update: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("due = %d, want 0", len(due))
	}

	got, err := st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification: %v", err)
	}
	if got == nil || !got.NotifiedReady {
		t.Fatalf("NotifiedReady = %+v", got)
	}

	if err := st.Unsubscribe(ctx, "user-1"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	got, err = st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification after unsubscribe: %v", err)
	}
	if got != nil {
		t.Fatalf("notification still present: %+v", got)
	}
}

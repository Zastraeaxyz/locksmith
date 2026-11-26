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

	if err := st.Watch(ctx, Watch{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		LastDraws:     "g",
		NextCheckAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Watch: %v", err)
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

	if err := st.RecordDraws(ctx, Draws{
		DiscordUserID:  "user-1",
		LockID:         "lock-1",
		LastDraws:      "gr",
		LastPicks:      3,
		LastNotifiedAt: now,
		NextCheckAt:    now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RecordDraws: %v", err)
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
	if got == nil || got.LastPicks != 3 || got.LastDraws != "gr" {
		t.Fatalf("draw state = %+v", got)
	}

	if err := st.RecordReminder(ctx, Reminder{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		DueAt:         now.Add(2 * time.Hour),
		Stage:         "lead",
		NextCheckAt:   now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RecordReminder: %v", err)
	}
	got, err = st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification after reminder: %v", err)
	}
	if !got.CheckinDueAt.Equal(now.Add(2*time.Hour)) || got.CheckinStage != "lead" {
		t.Fatalf("check-in state = %+v", got)
	}

	if err := st.RecordDraws(ctx, Draws{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		LastDraws:     "grr",
		LastPicks:     4,
		NextCheckAt:   now.Add(30 * time.Minute),
	}); err != nil {
		t.Fatalf("RecordDraws second: %v", err)
	}
	got, err = st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification after second draw: %v", err)
	}
	if got.CheckinStage != "lead" {
		t.Fatalf("RecordDraws cleared the reminder: %+v", got)
	}

	if err := st.Watch(ctx, Watch{
		DiscordUserID: "user-1",
		LockID:        "lock-1",
		LastDraws:     "g",
		NextCheckAt:   now.Add(10 * time.Second),
	}); err != nil {
		t.Fatalf("Watch again: %v", err)
	}
	got, err = st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification after re-watch: %v", err)
	}
	if got.CheckinStage != "lead" {
		t.Fatalf("re-watching the same lock reset the reminder: %+v", got)
	}

	if err := st.Watch(ctx, Watch{
		DiscordUserID: "user-1",
		LockID:        "lock-2",
		NextCheckAt:   now.Add(10 * time.Second),
	}); err != nil {
		t.Fatalf("Watch new lock: %v", err)
	}
	got, err = st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification after new lock: %v", err)
	}
	if got.LockID != "lock-2" || !got.CheckinDueAt.IsZero() || got.CheckinStage != "" {
		t.Fatalf("new lock should start a fresh subscription: %+v", got)
	}

	if err := st.ClearCheckin(ctx, "user-1", "lock-2"); err != nil {
		t.Fatalf("ClearCheckin: %v", err)
	}
	got, err = st.Notification(ctx, "user-1")
	if err != nil {
		t.Fatalf("Notification after ClearCheckin: %v", err)
	}
	if !got.CheckinDueAt.IsZero() || got.CheckinStage != "" {
		t.Fatalf("check-in state after clear = %+v", got)
	}

	if err := st.Reschedule(ctx, "user-1", now.Add(-time.Minute)); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	due, err = st.DueNotifications(ctx, now)
	if err != nil {
		t.Fatalf("DueNotifications after reschedule: %v", err)
	}
	if len(due) != 1 || due[0].LockID != "lock-2" {
		t.Fatalf("due after reschedule = %+v", due)
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

func TestUserDocumentRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	profile := &UserDocument{
		DiscordUserID: "user-1",
		Kind:          DocumentProfile,
		Handle:        "zastraea",
		Content:       "hello profile",
	}
	if err := st.SaveDocument(ctx, profile); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	got, err := st.Document(ctx, "user-1", DocumentProfile)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if got == nil || got.Content != "hello profile" || got.Handle != "zastraea" {
		t.Fatalf("got %+v", got)
	}

	rules := &UserDocument{
		DiscordUserID: "user-1",
		Kind:          DocumentRules,
		Handle:        "zastraea",
		Content:       "rules here",
	}
	if err := st.SaveDocument(ctx, rules); err != nil {
		t.Fatalf("SaveDocument rules: %v", err)
	}
	kinks := &UserDocument{
		DiscordUserID: "user-1",
		Kind:          DocumentKinks,
		Handle:        "zastraea",
		Content:       "kinks here",
	}
	if err := st.SaveDocument(ctx, kinks); err != nil {
		t.Fatalf("SaveDocument kinks: %v", err)
	}
	kinksGot, err := st.Document(ctx, "user-1", DocumentKinks)
	if err != nil {
		t.Fatalf("Document kinks: %v", err)
	}
	if kinksGot == nil || kinksGot.Content != "kinks here" {
		t.Fatalf("kinks = %+v", kinksGot)
	}
	profileStill, err := st.Document(ctx, "user-1", DocumentProfile)
	if err != nil {
		t.Fatalf("Document profile: %v", err)
	}
	if profileStill == nil || profileStill.Content != "hello profile" {
		t.Fatalf("profile was overwritten: %+v", profileStill)
	}

	if err := st.UpdateHandle(ctx, "user-1", "newhandle"); err != nil {
		t.Fatalf("UpdateHandle: %v", err)
	}
	got, err = st.Document(ctx, "user-1", DocumentProfile)
	if err != nil {
		t.Fatalf("Document after handle update: %v", err)
	}
	if got == nil || got.Handle != "newhandle" {
		t.Fatalf("handle = %+v", got)
	}

	if err := st.DeleteDocument(ctx, "user-1", DocumentProfile); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	gone, err := st.Document(ctx, "user-1", DocumentProfile)
	if err != nil {
		t.Fatalf("Document after delete: %v", err)
	}
	if gone != nil {
		t.Fatalf("profile still present: %+v", gone)
	}
	stillRules, err := st.Document(ctx, "user-1", DocumentRules)
	if err != nil {
		t.Fatalf("Document rules after profile delete: %v", err)
	}
	if stillRules == nil || stillRules.Content != "rules here" {
		t.Fatalf("rules should remain: %+v", stillRules)
	}
	stillKinks, err := st.Document(ctx, "user-1", DocumentKinks)
	if err != nil {
		t.Fatalf("Document kinks after profile delete: %v", err)
	}
	if stillKinks == nil || stillKinks.Content != "kinks here" {
		t.Fatalf("kinks should remain: %+v", stillKinks)
	}
}

func TestSaveDocumentUnknownKind(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	err := st.SaveDocument(ctx, &UserDocument{
		DiscordUserID: "user-1",
		Kind:          "unknown",
		Content:       "x",
	})
	if err == nil {
		t.Fatal("expected an unknown kind error")
	}
}

func TestResolveDocumentUser(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := st.SaveCredential(ctx, &Credential{
		DiscordUserID:  "user-1",
		ChastiSafeID:   "1574588415783997481",
		ChastiSafeUser: "zastraea#0",
		Secret:         []byte("sealed"),
	}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	if err := st.SaveDocument(ctx, &UserDocument{
		DiscordUserID: "user-2",
		Kind:          DocumentProfile,
		Handle:        "other",
		Content:       "x",
	}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}
	if err := st.SaveDocument(ctx, &UserDocument{
		DiscordUserID: "user-3",
		Kind:          DocumentKinks,
		Handle:        "kinksonly",
		Content:       "x",
	}); err != nil {
		t.Fatalf("SaveDocument kinks: %v", err)
	}

	id, ok, err := st.UserByChastiSafeName(ctx, "zastraea")
	if err != nil || !ok || id != "user-1" {
		t.Fatalf("UserByChastiSafeName = %q, %v, %v", id, ok, err)
	}
	id, ok, err = st.UserByChastiSafeName(ctx, "ZASTRAEA")
	if err != nil || !ok || id != "user-1" {
		t.Fatalf("case-insensitive lookup = %q, %v, %v", id, ok, err)
	}
	id, ok, err = st.UserByHandle(ctx, "other")
	if err != nil || !ok || id != "user-2" {
		t.Fatalf("UserByHandle = %q, %v, %v", id, ok, err)
	}
	id, ok, err = st.UserByHandle(ctx, "kinksonly")
	if err != nil || !ok || id != "user-3" {
		t.Fatalf("UserByHandle kinks = %q, %v, %v", id, ok, err)
	}
	if _, ok, err := st.UserByChastiSafeName(ctx, "nobody"); err != nil || ok {
		t.Fatalf("unknown ChastiSafe name = %v, %v", ok, err)
	}
	if _, ok, err := st.UserByHandle(ctx, "nobody"); err != nil || ok {
		t.Fatalf("unknown handle = %v, %v", ok, err)
	}
}

func TestNotificationMessageRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	messages := []NotificationMessage{
		{MessageID: "m-1", DiscordUserID: "user-1", ChannelID: "dm-1", Kind: "draw", CreatedAt: now, DeleteAt: now.Add(24 * time.Hour)},
		{MessageID: "m-2", DiscordUserID: "user-1", ChannelID: "dm-1", Kind: "checkin", CreatedAt: now.Add(time.Minute), DeleteAt: now.Add(48 * time.Hour)},
		{MessageID: "m-3", DiscordUserID: "user-2", ChannelID: "dm-2", Kind: "draw", CreatedAt: now, DeleteAt: now.Add(-time.Minute)},
	}
	for _, message := range messages {
		if err := st.RecordNotificationMessage(ctx, message); err != nil {
			t.Fatalf("RecordNotificationMessage(%s): %v", message.MessageID, err)
		}
	}

	got, err := st.NotificationMessages(ctx, "user-1")
	if err != nil {
		t.Fatalf("NotificationMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("user-1 messages = %d, want 2", len(got))
	}
	if got[0].MessageID != "m-1" || got[1].MessageID != "m-2" {
		t.Fatalf("message order = %s, %s", got[0].MessageID, got[1].MessageID)
	}

	due, err := st.DueNotificationMessages(ctx, now)
	if err != nil {
		t.Fatalf("DueNotificationMessages: %v", err)
	}
	if len(due) != 1 || due[0].MessageID != "m-3" {
		t.Fatalf("due at now = %+v", due)
	}

	due, err = st.DueNotificationMessages(ctx, now.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("DueNotificationMessages later: %v", err)
	}
	if len(due) != 3 {
		t.Fatalf("due later = %d, want 3", len(due))
	}

	if err := st.ForgetNotificationMessage(ctx, "m-1"); err != nil {
		t.Fatalf("ForgetNotificationMessage: %v", err)
	}
	got, err = st.NotificationMessages(ctx, "user-1")
	if err != nil {
		t.Fatalf("NotificationMessages after forget: %v", err)
	}
	if len(got) != 1 || got[0].MessageID != "m-2" {
		t.Fatalf("remaining messages = %+v", got)
	}

	// A rejected or deleted credential leaves the tracked DMs for the
	// notification loop to expire.
	if err := st.DeleteCredential(ctx, "user-1"); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	got, err = st.NotificationMessages(ctx, "user-1")
	if err != nil {
		t.Fatalf("NotificationMessages after DeleteCredential: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("messages after DeleteCredential = %d, want 1", len(got))
	}
}

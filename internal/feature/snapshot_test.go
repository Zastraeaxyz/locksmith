package feature

import (
	"context"
	"net/http"
	"testing"
)

// TestSnapshot drives handlers end to end against fixed Discord and ChastiSafe
// responses and compares the outbound Discord transcript with a golden file.
//
// Run with UPDATE_GOLDEN=1 to rewrite the files after an intentional change.
func TestSnapshot(t *testing.T) {
	t.Run("history private", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "history_private", "history_private", env.feature.handleHistoryCommand)
	})

	t.Run("history public", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "history_public", "history_public", env.feature.handleHistoryCommand)
	})

	t.Run("lock status", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "lock_status", "lock_status", env.feature.handleLockCommand)
	})

	t.Run("lock deck", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "lock_deck", "lock_deck", env.feature.handleLockCommand)
	})

	t.Run("lock share", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "lock_share", "lock_share", env.feature.handleLockCommand)
	})

	t.Run("lock log full", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "lock_log_full", "lock_log_full", env.feature.handleLockCommand)
	})

	t.Run("login modal", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "login_modal", "login_modal", env.feature.handleLoginModal)
	})

	t.Run("rejected saved code", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.api.serve(t, http.MethodGet, testChastiSafeBaseURL+"/loaded",
			http.StatusUnauthorized, "chastisafe/unauthorized.json")

		env.snapshot(t, "lock_status_rejected", "lock_status", env.feature.handleLockCommand)

		credential, err := env.store.Credential(context.Background(), testUserID)
		if err != nil {
			t.Fatalf("loading credential after rejection: %v", err)
		}
		if credential != nil {
			t.Fatalf("rejected code was not deleted: %+v", credential)
		}
	})

	t.Run("help", func(t *testing.T) {
		env := newSnapshotEnv(t)
		env.snapshot(t, "help", "help", env.feature.handleHelpCommand)
	})
}

// Package store is the SQLite persistence layer for Locksmith.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// Store is the SQLite-backed persistence layer.
type Store struct {
	db *sql.DB
}

// Credential is one member's encrypted ChastiSafe developer code. Secret is
// ciphertext; the store never sees or returns a plaintext code.
type Credential struct {
	DiscordUserID  string
	ChastiSafeID   string
	ChastiSafeUser string
	Secret         []byte
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Notification is one member's draw-notification subscription.
type Notification struct {
	DiscordUserID  string
	LockID         string
	LastDraws      string
	NotifiedReady  bool
	LastNotifiedAt time.Time
	NextCheckAt    time.Time
	CreatedAt      time.Time
}

// Open opens the SQLite database, creating its directory if needed, and
// applies any pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating database directory: %w", err)
		}
	}

	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.SetMaxOpenConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	if err := migrate(pingCtx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= schemaVersion {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if version < 1 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS credentials (
				discord_user_id TEXT PRIMARY KEY,
				chastisafe_id   TEXT NOT NULL,
				chastisafe_user TEXT NOT NULL,
				secret          BLOB NOT NULL,
				created_at      INTEGER NOT NULL DEFAULT (unixepoch()),
				updated_at      INTEGER NOT NULL DEFAULT (unixepoch())
			)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS notifications (
				discord_user_id  TEXT PRIMARY KEY,
				lock_id          TEXT NOT NULL DEFAULT '',
				last_draws       TEXT NOT NULL DEFAULT '',
				notified_ready   INTEGER NOT NULL DEFAULT 0,
				last_notified_at INTEGER NOT NULL DEFAULT 0,
				next_check_at    INTEGER NOT NULL DEFAULT 0,
				created_at       INTEGER NOT NULL DEFAULT (unixepoch())
			)`); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveCredential inserts or replaces a member's encrypted code.
func (s *Store) SaveCredential(ctx context.Context, cred *Credential) error {
	if cred == nil {
		return errors.New("store: credential is nil")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO credentials (
			discord_user_id, chastisafe_id, chastisafe_user, secret, updated_at
		) VALUES (?, ?, ?, ?, unixepoch())
		ON CONFLICT(discord_user_id) DO UPDATE SET
			chastisafe_id = excluded.chastisafe_id,
			chastisafe_user = excluded.chastisafe_user,
			secret = excluded.secret,
			updated_at = unixepoch()`,
		cred.DiscordUserID, cred.ChastiSafeID, cred.ChastiSafeUser, cred.Secret,
	)
	return err
}

// Credential returns a member's stored code, or (nil, nil) when they have not
// logged in.
func (s *Store) Credential(ctx context.Context, discordUserID string) (*Credential, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT discord_user_id, chastisafe_id, chastisafe_user, secret, created_at, updated_at
		FROM credentials WHERE discord_user_id = ?`, discordUserID)

	var (
		cred                 Credential
		createdAt, updatedAt int64
	)
	err := row.Scan(&cred.DiscordUserID, &cred.ChastiSafeID, &cred.ChastiSafeUser,
		&cred.Secret, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cred.CreatedAt = time.Unix(createdAt, 0).UTC()
	cred.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &cred, nil
}

// DeleteCredential removes a member's stored code and notification.
func (s *Store) DeleteCredential(ctx context.Context, discordUserID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM credentials WHERE discord_user_id = ?`, discordUserID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE discord_user_id = ?`, discordUserID)
	return err
}

// Subscribe registers or refreshes a member's draw notifications.
func (s *Store) Subscribe(ctx context.Context, n *Notification) error {
	if n == nil {
		return errors.New("store: notification is nil")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications (
			discord_user_id, lock_id, last_draws, notified_ready, last_notified_at, next_check_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(discord_user_id) DO UPDATE SET
			lock_id = excluded.lock_id,
			last_draws = excluded.last_draws,
			notified_ready = excluded.notified_ready,
			last_notified_at = excluded.last_notified_at,
			next_check_at = excluded.next_check_at`,
		n.DiscordUserID, n.LockID, n.LastDraws, boolInt(n.NotifiedReady),
		n.LastNotifiedAt.Unix(), n.NextCheckAt.Unix(),
	)
	return err
}

// Unsubscribe removes a member's draw notifications. Deleting a missing row is
// not an error.
func (s *Store) Unsubscribe(ctx context.Context, discordUserID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE discord_user_id = ?`, discordUserID)
	return err
}

// Notification returns a member's subscription, or (nil, nil).
func (s *Store) Notification(ctx context.Context, discordUserID string) (*Notification, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT discord_user_id, lock_id, last_draws, notified_ready, last_notified_at, next_check_at, created_at
		FROM notifications WHERE discord_user_id = ?`, discordUserID)
	return scanNotification(row)
}

// DueNotifications returns every subscription whose next check is due.
func (s *Store) DueNotifications(ctx context.Context, now time.Time) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT discord_user_id, lock_id, last_draws, notified_ready, last_notified_at, next_check_at, created_at
		FROM notifications
		WHERE next_check_at <= ?
		ORDER BY next_check_at`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		row := rows
		n, err := scanNotification(row)
		if err != nil {
			return nil, err
		}
		if n != nil {
			out = append(out, *n)
		}
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNotification(row scanner) (*Notification, error) {
	var (
		n                                Notification
		notifiedReady                    int
		lastNotified, nextCheck, created int64
	)
	err := row.Scan(&n.DiscordUserID, &n.LockID, &n.LastDraws, &notifiedReady,
		&lastNotified, &nextCheck, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.NotifiedReady = notifiedReady != 0
	n.LastNotifiedAt = time.Unix(lastNotified, 0).UTC()
	n.NextCheckAt = time.Unix(nextCheck, 0).UTC()
	n.CreatedAt = time.Unix(created, 0).UTC()
	return &n, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

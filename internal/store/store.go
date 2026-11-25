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

const schemaVersion = 4

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

// Notification is one member's watch on a lock: the draw schedule state and
// the check-in reminder stage. Reads return it whole; writes go through the
// intent methods below, so a caller cannot overwrite a field it does not own.
type Notification struct {
	DiscordUserID  string
	LockID         string
	LastDraws      string
	LastPicks      int
	LastNotifiedAt time.Time
	NextCheckAt    time.Time
	CreatedAt      time.Time

	// CheckinDueAt is the check-in moment the reminder state refers to.
	CheckinDueAt time.Time
	// CheckinStage is the last reminder sent for that moment: "lead",
	// "final", or "overdue". Empty means no reminder yet.
	CheckinStage string
}

// Watch starts or refreshes a member's draw subscription for one lock. It
// resets the draw schedule state, but it keeps any check-in reminder state for
// the same lock, because re-subscribing is not a new check-in cycle.
type Watch struct {
	DiscordUserID string
	LockID        string
	LastDraws     string
	NextCheckAt   time.Time
}

// Draws records a draw-schedule observation for a watched lock.
type Draws struct {
	DiscordUserID  string
	LockID         string
	LastDraws      string
	LastPicks      int
	LastNotifiedAt time.Time
	NextCheckAt    time.Time
}

// Reminder records the check-in reminder stage for the current cycle.
type Reminder struct {
	DiscordUserID string
	LockID        string
	DueAt         time.Time
	Stage         string
	NextCheckAt   time.Time
}

// Document kinds stored in user_documents.
const (
	DocumentProfile = "profile"
	DocumentRules   = "rules"
	DocumentKinks   = "kinks"
)

func validDocumentKind(kind string) bool {
	switch kind {
	case DocumentProfile, DocumentRules, DocumentKinks:
		return true
	default:
		return false
	}
}

// UserDocument is one member's shared markdown document.
type UserDocument struct {
	DiscordUserID string
	Kind          string
	Handle        string
	Content       string
	UpdatedAt     time.Time
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

	if version < 2 {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE notifications ADD COLUMN last_picks INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	if version < 3 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS user_documents (
				discord_user_id TEXT NOT NULL,
				kind            TEXT NOT NULL,
				handle          TEXT NOT NULL DEFAULT '',
				content         TEXT NOT NULL,
				updated_at      INTEGER NOT NULL DEFAULT (unixepoch()),
				PRIMARY KEY (discord_user_id, kind)
			)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			CREATE INDEX IF NOT EXISTS idx_user_documents_handle
			ON user_documents(kind, handle)`); err != nil {
			return err
		}
	}

	if version < 4 {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE notifications ADD COLUMN checkin_due_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE notifications ADD COLUMN checkin_stage TEXT NOT NULL DEFAULT ''`); err != nil {
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

// Watch starts or refreshes a member's draw subscription. A new lock clears
// the check-in reminder state, because the stored reminder belongs to the old
// lock; the same lock keeps it, so re-subscribing cannot repeat a reminder.
func (s *Store) Watch(ctx context.Context, w Watch) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications (
			discord_user_id, lock_id, last_draws, last_picks, last_notified_at,
			next_check_at, checkin_due_at, checkin_stage
		) VALUES (?, ?, ?, 0, 0, ?, 0, '')
		ON CONFLICT(discord_user_id) DO UPDATE SET
			lock_id = excluded.lock_id,
			last_draws = excluded.last_draws,
			last_picks = excluded.last_picks,
			last_notified_at = excluded.last_notified_at,
			next_check_at = excluded.next_check_at,
			checkin_due_at = CASE
				WHEN notifications.lock_id = excluded.lock_id
				THEN notifications.checkin_due_at ELSE 0 END,
			checkin_stage = CASE
				WHEN notifications.lock_id = excluded.lock_id
				THEN notifications.checkin_stage ELSE '' END`,
		w.DiscordUserID, w.LockID, w.LastDraws, w.NextCheckAt.Unix(),
	)
	return err
}

// RecordDraws stores a draw-schedule observation. It leaves the check-in
// reminder state untouched.
func (s *Store) RecordDraws(ctx context.Context, d Draws) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE notifications
		SET lock_id = ?, last_draws = ?, last_picks = ?, last_notified_at = ?,
			next_check_at = ?
		WHERE discord_user_id = ?`,
		d.LockID, d.LastDraws, d.LastPicks, d.LastNotifiedAt.Unix(),
		d.NextCheckAt.Unix(), d.DiscordUserID,
	)
	return err
}

// RecordReminder stores the check-in reminder stage. It leaves the draw
// schedule state untouched.
func (s *Store) RecordReminder(ctx context.Context, r Reminder) error {
	due := int64(0)
	if !r.DueAt.IsZero() {
		due = r.DueAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE notifications
		SET lock_id = ?, checkin_due_at = ?, checkin_stage = ?, next_check_at = ?
		WHERE discord_user_id = ?`,
		r.LockID, due, r.Stage, r.NextCheckAt.Unix(), r.DiscordUserID,
	)
	return err
}

// Reschedule moves the next poll without touching draw or reminder state.
func (s *Store) Reschedule(ctx context.Context, discordUserID string, nextCheckAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE notifications SET next_check_at = ? WHERE discord_user_id = ?`,
		nextCheckAt.Unix(), discordUserID,
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

// ClearCheckin resets the check-in reminder state for one watched lock, so the
// next cycle can notify again.
func (s *Store) ClearCheckin(ctx context.Context, discordUserID, lockID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE notifications
		SET checkin_due_at = 0, checkin_stage = ''
		WHERE discord_user_id = ? AND lock_id = ?`, discordUserID, lockID)
	return err
}

// Notification returns a member's subscription, or (nil, nil).
func (s *Store) Notification(ctx context.Context, discordUserID string) (*Notification, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT discord_user_id, lock_id, last_draws, last_picks, last_notified_at,
			next_check_at, created_at, checkin_due_at, checkin_stage
		FROM notifications WHERE discord_user_id = ?`, discordUserID)
	return scanNotification(row)
}

// DueNotifications returns every subscription whose next check is due.
func (s *Store) DueNotifications(ctx context.Context, now time.Time) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT discord_user_id, lock_id, last_draws, last_picks, last_notified_at,
			next_check_at, created_at, checkin_due_at, checkin_stage
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
		lastNotified, nextCheck, created int64
		checkinDue                       int64
	)
	err := row.Scan(&n.DiscordUserID, &n.LockID, &n.LastDraws, &n.LastPicks,
		&lastNotified, &nextCheck, &created, &checkinDue, &n.CheckinStage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.LastNotifiedAt = time.Unix(lastNotified, 0).UTC()
	n.NextCheckAt = time.Unix(nextCheck, 0).UTC()
	n.CreatedAt = time.Unix(created, 0).UTC()
	n.CheckinDueAt = time.Time{}
	if checkinDue != 0 {
		n.CheckinDueAt = time.Unix(checkinDue, 0).UTC()
	}
	return &n, nil
}

// SaveDocument inserts or replaces a member's markdown document.
func (s *Store) SaveDocument(ctx context.Context, doc *UserDocument) error {
	if doc == nil {
		return errors.New("store: document is nil")
	}
	if !validDocumentKind(doc.Kind) {
		return fmt.Errorf("store: unknown document kind %q", doc.Kind)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_documents (discord_user_id, kind, handle, content, updated_at)
		VALUES (?, ?, ?, ?, unixepoch())
		ON CONFLICT(discord_user_id, kind) DO UPDATE SET
			handle = excluded.handle,
			content = excluded.content,
			updated_at = unixepoch()`,
		doc.DiscordUserID, doc.Kind, doc.Handle, doc.Content,
	)
	return err
}

// Document returns a member's document, or (nil, nil) when it is not set.
func (s *Store) Document(ctx context.Context, userID, kind string) (*UserDocument, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT discord_user_id, kind, handle, content, updated_at
		FROM user_documents
		WHERE discord_user_id = ? AND kind = ?`, userID, kind)

	var (
		doc     UserDocument
		updated int64
	)
	err := row.Scan(&doc.DiscordUserID, &doc.Kind, &doc.Handle, &doc.Content, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	doc.UpdatedAt = time.Unix(updated, 0).UTC()
	return &doc, nil
}

// DeleteDocument removes a member's document. Deleting a missing row is not an
// error.
func (s *Store) DeleteDocument(ctx context.Context, userID, kind string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM user_documents WHERE discord_user_id = ? AND kind = ?`, userID, kind)
	return err
}

// UpdateHandle refreshes the stored Discord handle for a member's documents.
func (s *Store) UpdateHandle(ctx context.Context, userID, handle string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE user_documents SET handle = ? WHERE discord_user_id = ?`, handle, userID)
	return err
}

// UserByChastiSafeName resolves a ChastiSafe username without its
// discriminator to a Discord user ID.
func (s *Store) UserByChastiSafeName(ctx context.Context, name string) (string, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT discord_user_id FROM credentials
		WHERE lower(chastisafe_user) = lower(?)
		   OR lower(chastisafe_user) LIKE lower(?) || '#%'
		LIMIT 1`, name, name)

	var id string
	err := row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// UserByHandle resolves a stored Discord handle to a Discord user ID.
func (s *Store) UserByHandle(ctx context.Context, handle string) (string, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT discord_user_id FROM user_documents
		WHERE handle = ? AND kind IN (?, ?, ?)
		LIMIT 1`, handle, DocumentProfile, DocumentRules, DocumentKinks)

	var id string
	err := row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

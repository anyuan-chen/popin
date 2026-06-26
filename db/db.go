package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	return db, nil
}

func Migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  DATETIME NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'browser',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sessions_user    ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	if err := migrateSessionsKind(db); err != nil {
		return fmt.Errorf("migrate sessions.kind: %w", err)
	}

	if err := migrateFriendshipEvents(db); err != nil {
		return fmt.Errorf("migrate friendship_events: %w", err)
	}

	return nil
}

// migrateFriendshipEvents creates the append-only friendship_events log. The
// current friendship state between two users is always derived from the
// newest event of the unordered pair (see the friend package). The table is
// append-only: state transitions (request/accept/deny/unfriend) are new
// INSERTs, never UPDATEs, so denial and unfriending never destroy history.
func migrateFriendshipEvents(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS friendship_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    actor_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action     TEXT    NOT NULL CHECK (action IN ('request','accept','deny','unfriend')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fe_actor   ON friendship_events(actor_id);
CREATE INDEX IF NOT EXISTS idx_fe_subject ON friendship_events(subject_id);
`)
	if err != nil {
		return fmt.Errorf("create friendship_events: %w", err)
	}
	return nil
}

// migrateSessionsKind adds the `kind` column to the sessions table for existing
// databases. New databases should ideally have it in CREATE TABLE above, but
// adding it there would not affect already-created tables, so we always run
// this additive migration. SQLite has no ADD COLUMN IF NOT EXISTS, so we probe
// PRAGMA table_info first. The idx_sessions_user_kind index is created here
// (rather than in the CREATE TABLE block above) because it references `kind`
// and must not run until the column exists on legacy databases.
func migrateSessionsKind(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return fmt.Errorf("probe columns: %w", err)
	}

	hasKind := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan column: %w", err)
		}
		if name == "kind" {
			hasKind = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate columns: %w", err)
	}
	// Close the PRAGMA result rows before any subsequent db.Exec: db is
	// configured with SetMaxOpenConns(1), so issuing a write while the
	// read cursor is still open would deadlock waiting for the connection.
	rows.Close()

	if !hasKind {
		if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'browser'`); err != nil {
			return fmt.Errorf("add column: %w", err)
		}
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_user_kind ON sessions(user_id, kind)`); err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	return nil
}

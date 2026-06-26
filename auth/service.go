package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrMissingCredentials = errors.New("username and password are required")
	ErrPasswordTooShort   = errors.New("password must be at least 8 characters")
)

// Session kinds.
const (
	KindBrowser = "browser"
	KindDaemon  = "daemon"
)

func IsValidKind(k string) bool {
	return k == KindBrowser || k == KindDaemon
}

// SessionInfo bundles the authenticated user with the kind of session that
// vouched for them. VerifySession returns this so middleware can enforce
// kind-specific access (e.g. only daemon-kind sessions may hold the daemon
// WebSocket).
type SessionInfo struct {
	*User
	Kind string
}

type AuthService struct {
	db              *sql.DB
	sessionDuration time.Duration
}

func NewService(db *sql.DB, sessionDuration time.Duration) *AuthService {
	return &AuthService{
		db:              db,
		sessionDuration: sessionDuration,
	}
}

// DB returns the underlying database handle. Callers needing the same DB
// connection (e.g. to build a friend.Service that shares the users table)
// should use this rather than opening a second connection.
func (s *AuthService) DB() *sql.DB { return s.db }

func (s *AuthService) Register(username, password string) (*User, error) {
	if username == "" || password == "" {
		return nil, ErrMissingCredentials
	}
	if len(password) < 8 {
		return nil, ErrPasswordTooShort
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	res, err := s.db.Exec(
		`INSERT INTO users (username, password_hash) VALUES (?, ?)`,
		username, string(hash),
	)
	if err != nil {
		if err.Error() == "constraint failed: UNIQUE constraint failed: users.username (2067)" {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	id, _ := res.LastInsertId()
	return &User{
		ID:       id,
		Username: username,
	}, nil
}

// Login authenticates the user and creates a session of the given kind.
// kind must be KindBrowser or KindDaemon; an empty kind defaults to browser.
func (s *AuthService) Login(username, password, kind string) (string, *User, error) {
	if kind == "" {
		kind = KindBrowser
	}
	if !IsValidKind(kind) {
		return "", nil, fmt.Errorf("invalid session kind %q", kind)
	}

	var u User
	var hash string

	err := s.db.QueryRow(
		`SELECT id, username, password_hash FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, ErrInvalidCredentials
		}
		return "", nil, fmt.Errorf("query user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return "", nil, ErrInvalidCredentials
	}

	token, err := s.createSession(u.ID, kind)
	if err != nil {
		return "", nil, err
	}

	return token, &u, nil
}

// IssueDaemonToken mints a daemon-kind session for an already-authenticated
// user ID without re-checking the password. Used by the web login-redirect
// flow: the browser is already logged in (browser-kind cookie), and the user
// wants to authorize a CLI daemon on another machine.
func (s *AuthService) IssueDaemonToken(userID int64) (string, error) {
	return s.createSession(userID, KindDaemon)
}

// createSession inserts a new session row, first deleting any existing
// daemon-kind sessions for the same user when issuing a daemon session —
// this preserves the "single active daemon per user" invariant at the token
// level (the daemon WS also last-conn-wins for safety).
func (s *AuthService) createSession(userID int64, kind string) (string, error) {
	if kind == KindDaemon {
		if _, err := s.db.Exec(
			`DELETE FROM sessions WHERE user_id = ? AND kind = ?`,
			userID, KindDaemon,
		); err != nil {
			return "", fmt.Errorf("clear daemon sessions: %w", err)
		}
	}

	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	expiresAt := time.Now().Add(s.sessionDuration)
	_, err = s.db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at, kind) VALUES (?, ?, ?, ?)`,
		token, userID, expiresAt.UTC(), kind,
	)
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}

	return token, nil
}

func (s *AuthService) VerifySession(ctx context.Context, token string) (*SessionInfo, error) {
	if token == "" {
		return nil, errors.New("missing token")
	}

	var u User
	var kind string
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, s.kind
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token = ? AND s.expires_at > ?`,
		token, time.Now().UTC(),
	).Scan(&u.ID, &u.Username, &kind)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("invalid or expired session")
		}
		return nil, fmt.Errorf("verify session: %w", err)
	}

	return &SessionInfo{User: &u, Kind: kind}, nil
}

func (s *AuthService) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

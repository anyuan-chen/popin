package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/popin/popin/db"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func newTestService(t *testing.T) *AuthService {
	t.Helper()
	return NewService(setupTestDB(t), 24*time.Hour)
}

func TestRegister_Success(t *testing.T) {
	svc := newTestService(t)

	user, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.ID == 0 {
		t.Error("expected non-zero user ID")
	}
	if user.Username != "alice" {
		t.Errorf("username = %q, want %q", user.Username, "alice")
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	_, err = svc.Register("alice", "different123")
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("second Register error = %v, want ErrUsernameTaken", err)
	}
}

func TestRegister_EmptyUsername(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("", "password123")
	if err == nil {
		t.Fatal("expected error for empty username")
	}
}

func TestRegister_EmptyPassword(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "")
	if err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestRegister_ShortPassword(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "short")
	if err == nil {
		t.Fatal("expected error for short password")
	}
}

func TestLogin_Success(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, user, err := svc.Login("alice", "password123", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	if user.Username != "alice" {
		t.Errorf("username = %q, want %q", user.Username, "alice")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, _, err = svc.Login("alice", "wrongpassword", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
}

func TestLogin_NonexistentUser(t *testing.T) {
	svc := newTestService(t)

	_, _, err := svc.Login("ghost", "password123", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
}

func TestLogin_CreatesUniqueTokens(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token1, _, err := svc.Login("alice", "password123", "")
	if err != nil {
		t.Fatalf("first Login: %v", err)
	}

	token2, _, err := svc.Login("alice", "password123", "")
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}

	if token1 == token2 {
		t.Error("expected different tokens for two logins")
	}
}

func TestLogin_DaemonKind(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, _, err := svc.Login("alice", "password123", KindDaemon)
	if err != nil {
		t.Fatalf("Login daemon: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty daemon token")
	}

	info, err := svc.VerifySession(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if info.Kind != KindDaemon {
		t.Errorf("kind = %q, want %q", info.Kind, KindDaemon)
	}
}

func TestLogin_DaemonKindReplacesPriorDaemonTokens(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Register("alice", "password123"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	t1, _, err := svc.Login("alice", "password123", KindDaemon)
	if err != nil {
		t.Fatalf("first daemon login: %v", err)
	}
	t2, _, err := svc.Login("alice", "password123", KindDaemon)
	if err != nil {
		t.Fatalf("second daemon login: %v", err)
	}
	if t1 == t2 {
		t.Fatal("expected new distinct token on re-login")
	}

	// The prior daemon token must be invalidated (single-active-daemon).
	if _, err := svc.VerifySession(context.Background(), t1); err == nil {
		t.Fatal("expected old daemon token to be revoked after re-login")
	}

	info, err := svc.VerifySession(context.Background(), t2)
	if err != nil {
		t.Fatalf("VerifySession new token: %v", err)
	}
	if info.Kind != KindDaemon {
		t.Errorf("new token kind = %q, want daemon", info.Kind)
	}
}

func TestLogin_DaemonKindPreservesBrowserSessions(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Register("alice", "password123"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	browserTok, _, err := svc.Login("alice", "password123", KindBrowser)
	if err != nil {
		t.Fatalf("browser login: %v", err)
	}
	if _, _, err := svc.Login("alice", "password123", KindDaemon); err != nil {
		t.Fatalf("daemon login: %v", err)
	}

	// Browser session must still verify; daemon-token rotation only affects
	// daemon-kind sessions for this user.
	info, err := svc.VerifySession(context.Background(), browserTok)
	if err != nil {
		t.Fatalf("browser VerifySession: %v", err)
	}
	if info.Kind != KindBrowser {
		t.Errorf("browser kind = %q, want browser", info.Kind)
	}
}

func TestLogin_InvalidKind(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Register("alice", "password123"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, _, err := svc.Login("alice", "password123", "robot"); err == nil {
		t.Fatal("expected error for invalid kind")
	}
}

func TestIssueDaemonToken(t *testing.T) {
	svc := newTestService(t)
	u, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, err := svc.IssueDaemonToken(u.ID)
	if err != nil {
		t.Fatalf("IssueDaemonToken: %v", err)
	}
	info, err := svc.VerifySession(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if info.Kind != KindDaemon {
		t.Errorf("kind = %q, want daemon", info.Kind)
	}
	if info.Username != "alice" {
		t.Errorf("username = %q, want alice", info.Username)
	}
}

func TestVerifySession_Valid(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, _, err := svc.Login("alice", "password123", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	info, err := svc.VerifySession(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if info.Username != "alice" {
		t.Errorf("username = %q, want %q", info.Username, "alice")
	}
}

func TestVerifySession_Expired(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = svc.db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		"expired-token", 1, time.Now().Add(-1*time.Hour).UTC(),
	)
	if err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	_, err = svc.VerifySession(context.Background(), "expired-token")
	if err == nil {
		t.Fatal("expected error for expired session")
	}
}

func TestVerifySession_EmptyToken(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.VerifySession(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestVerifySession_NonexistentToken(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.VerifySession(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent token")
	}
}

func TestLogout_RemovesSession(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.Register("alice", "password123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, _, err := svc.Login("alice", "password123", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	_, err = svc.VerifySession(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifySession before logout: %v", err)
	}

	if err := svc.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	_, err = svc.VerifySession(context.Background(), token)
	if err == nil {
		t.Fatal("expected error after logout")
	}
}

func TestLogout_NonexistentToken_NoError(t *testing.T) {
	svc := newTestService(t)

	err := svc.Logout(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("Logout nonexistent token: %v", err)
	}
}

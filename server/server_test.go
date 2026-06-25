package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/popin/popin/auth"
	"github.com/popin/popin/config"
	"github.com/popin/popin/db"
)

func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := &config.Config{
		LiveKitURL:         "ws://localhost:7880",
		LiveKitClientURL:   "ws://localhost:7880",
		LiveKitAPIKey:      "testkey",
		LiveKitSecret:      "testsecret",
		Port:               "0",
		DBPath:             dbPath,
		SessionDuration:    24 * time.Hour,
		CookieSecure:       false,
		CookieDomain:       "",
		CORSAllowedOrigins: "",
	}

	authSvc := auth.NewService(database, cfg.SessionDuration)
	srv := New(cfg, authSvc)

	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	return ts, client
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

func TestHealth(t *testing.T) {
	ts, client := newTestServer(t)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body := decodeBody(t, resp)
	if body["status"] != "ok" {
		t.Errorf("status field = %v, want %q", body["status"], "ok")
	}
}

func TestRegister_Success(t *testing.T) {
	ts, client := newTestServer(t)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	body := decodeBody(t, resp)
	if body["username"] != "alice" {
		t.Errorf("username = %v, want %q", body["username"], "alice")
	}
}

func TestRegister_Duplicate(t *testing.T) {
	ts, client := newTestServer(t)

	body := map[string]string{
		"username": "alice",
		"password": "password123",
	}
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", body)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", body)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}

func TestRegister_ShortPassword(t *testing.T) {
	ts, client := newTestServer(t)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "bob",
		"password": "short",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestRegister_InvalidJSON(t *testing.T) {
	ts, client := newTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/register", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	resp.Body.Close()
}

func TestLogin_Success(t *testing.T) {
	ts, client := newTestServer(t)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var cookieFound bool
	for _, c := range resp.Cookies() {
		if c.Name == "popin_session" && c.Value != "" {
			cookieFound = true
			if !c.HttpOnly {
				t.Error("cookie should be HttpOnly")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Error("cookie should be SameSite=Lax")
			}
		}
	}
	if !cookieFound {
		t.Error("expected popin_session cookie in response")
	}

	body := decodeBody(t, resp)
	if body["username"] != "alice" {
		t.Errorf("username = %v, want %q", body["username"], "alice")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	ts, client := newTestServer(t)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "wrongpassword",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	resp.Body.Close()
}

func TestMe_WithoutCookie(t *testing.T) {
	ts, client := newTestServer(t)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	resp.Body.Close()
}

func TestMe_WithCookie(t *testing.T) {
	ts, client := newTestServer(t)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body := decodeBody(t, resp)
	if body["username"] != "alice" {
		t.Errorf("username = %v, want %q", body["username"], "alice")
	}
}

func TestToken_WithoutCookie(t *testing.T) {
	ts, client := newTestServer(t)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/token", map[string]string{
		"room_name": "test-room",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	resp.Body.Close()
}

func TestToken_MissingRoomName(t *testing.T) {
	ts, client := newTestServer(t)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/token", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	resp.Body.Close()
}

func TestToken_Success(t *testing.T) {
	ts, client := newTestServer(t)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/token", map[string]string{
		"room_name": "test-room",
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body := decodeBody(t, resp)
	token, ok := body["token"].(string)
	if !ok || token == "" {
		t.Errorf("expected non-empty token, got %v", body["token"])
	}
	if body["identity"] != "alice" {
		t.Errorf("identity = %v, want %q", body["identity"], "alice")
	}
	if body["livekit_url"] != "ws://localhost:7880" {
		t.Errorf("livekit_url = %v, want %q", body["livekit_url"], "ws://localhost:7880")
	}
}

func TestRoomsCreate_RequiresAuth(t *testing.T) {
	ts, client := newTestServer(t)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/create", map[string]string{
		"name": "test-room",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	resp.Body.Close()
}

func TestLogout_ClearsSession(t *testing.T) {
	ts, client := newTestServer(t)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/logout", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	resp.Body.Close()

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("me after logout: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	resp.Body.Close()
}

func TestFullFlow(t *testing.T) {
	ts, client := newTestServer(t)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	resp.Body.Close()

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	resp.Body.Close()

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body := decodeBody(t, resp)
	if body["username"] != "alice" {
		t.Fatalf("me: username = %v, want %q", body["username"], "alice")
	}

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/token", map[string]string{
		"room_name": "test-room",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body = decodeBody(t, resp)
	if token, ok := body["token"].(string); !ok || token == "" {
		t.Fatalf("token: expected non-empty token, got %v", body["token"])
	}
	if body["identity"] != "alice" {
		t.Fatalf("token: identity = %v, want %q", body["identity"], "alice")
	}

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/logout", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	resp.Body.Close()

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	resp.Body.Close()
}

func TestMethodNotAllowed(t *testing.T) {
	ts, client := newTestServer(t)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/auth/register"},
		{http.MethodGet, "/api/auth/login"},
		{http.MethodGet, "/api/auth/logout"},
	}
	for _, tc := range cases {
		resp := doJSON(t, client, tc.method, ts.URL+tc.path, nil)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.path, resp.StatusCode, http.StatusMethodNotAllowed)
		}
		resp.Body.Close()
	}
}

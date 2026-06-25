package server

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// registerAndLogin creates a user and logs them in. kind is the session kind
// to mint ("browser" or "daemon"). For browser logins the returned client has
// the cookie set. For daemon logins the cookie is NOT set; instead the daemon
// token is returned in the body and provided as the second return value.
func registerAndLogin(t *testing.T, ts *httptest.Server, username, password, kind string) (*http.Client, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Register best-effort (it's ok if the user already exists; we ignore the
	// conflict response). Use a no-cookie client to avoid registering under an
	// authenticated session, since register doesn't require auth anyway.
	regClient := &http.Client{}
	regResp := doJSON(t, regClient, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": username,
		"password": password,
	})
	regResp.Body.Close()

	if kind == "daemon" {
		redirect := "http://127.0.0.1:0/callback"
		resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
			"username": username,
			"password": password,
			"kind":     kind,
			"redirect": redirect,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("daemon login: status=%d", resp.StatusCode)
		}
		var body struct {
			Token    string `json:"token"`
			Username string `json:"username"`
		}
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if body.Token == "" {
			t.Fatal("daemon login: empty token")
		}
		return client, body.Token
	}

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": username,
		"password": password,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browser login: status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	return client, ""
}

// dialDaemonWS opens a daemon WebSocket to the test server with the given
// daemon-kind token in the query string.
func dialDaemonWS(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(wsURLWithToken(ts, token), nil)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatalf("dial daemon ws: %v", err)
	}
	return conn
}

func wsURLWithToken(ts *httptest.Server, token string) string {
	u, _ := url.Parse(ts.URL)
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	u.Path = "/ws/daemon"
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

// makeFriends establishes an accepted friendship between a (logged in via
// aClient) and b (logged in via bClient). a requests, b accepts. Required now
// that /api/call is gated to accepted friends.
func makeFriends(t *testing.T, ts *httptest.Server, aClient *http.Client, aUsername string, bClient *http.Client, bUsername string) {
	t.Helper()
	resp := doJSON(t, aClient, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": bUsername,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("friend request %s -> %s: status = %d", aUsername, bUsername, resp.StatusCode)
	}
	resp.Body.Close()

	resp = doJSON(t, bClient, http.MethodPost, ts.URL+"/api/friends/accept", map[string]string{
		"username": aUsername,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("friend accept %s <- %s: status = %d", bUsername, aUsername, resp.StatusCode)
	}
	resp.Body.Close()
}

func TestLogin_DaemonKind_ReturnsTokenNoCookie(t *testing.T) {
	ts, _ := newTestServer(t)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
		"kind":     "daemon",
		"redirect": "http://127.0.0.1:0/callback",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	for _, c := range resp.Cookies() {
		if c.Name == "popin_session" {
			t.Error("daemon login unexpectedly set a session cookie")
		}
	}

	var body struct {
		Token    string `json:"token"`
		Kind     string `json:"kind"`
		Username string `json:"username"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.Token == "" {
		t.Error("expected token in body")
	}
	if body.Kind != "daemon" {
		t.Errorf("kind = %q, want daemon", body.Kind)
	}
	if body.Username != "alice" {
		t.Errorf("username = %q, want alice", body.Username)
	}
}

func TestLogin_DaemonKind_RequiresLoopbackRedirect(t *testing.T) {
	ts, client := newTestServer(t)
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})

	cases := []struct {
		name     string
		redirect string
	}{
		{"missing redirect", ""},
		{"external host", "https://example.com/cb"},
		{"non-loopback ip", "http://192.168.0.1:1234/cb"},
		{"non-http scheme", "ftp://127.0.0.1/cb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]string{
				"username": "alice",
				"password": "password123",
				"kind":     "daemon",
			}
			if tc.redirect != "" {
				body["redirect"] = tc.redirect
			}
			resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestLogin_BrowserKindUnaffected(t *testing.T) {
	ts, client := newTestServer(t)
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/register", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var found bool
	for _, c := range resp.Cookies() {
		if c.Name == "popin_session" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected session cookie for browser login")
	}
}

func TestDaemonWS_RejectsBrowserSession(t *testing.T) {
	ts, _ := newTestServer(t)
	client, _ := registerAndLogin(t, ts, "alice", "password123", "browser")

	// Pull the (browser-kind) session cookie out of the jar and present it as
	// the token query param. RequireAuth will accept it, but RequireDaemon
	// must reject it because the session kind is browser, not daemon.
	var cookieVal string
	jarURL, _ := url.Parse(ts.URL)
	for _, c := range client.Jar.Cookies(jarURL) {
		if c.Name == "popin_session" {
			cookieVal = c.Value
		}
	}
	if cookieVal == "" {
		t.Fatal("no session cookie in jar")
	}

	conn, resp, err := websocket.DefaultDialer.Dial(wsURLWithToken(ts, cookieVal), nil)
	if err == nil {
		conn.Close()
		t.Fatal("expected browser-kind daemon ws dial to fail")
	}
	if resp == nil {
		t.Fatal("expected an HTTP response from the failed dial")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (forbidden kind)", resp.StatusCode)
	}
}

func TestDaemonWS_DaemonKindAllowed(t *testing.T) {
	ts, _ := newTestServer(t)
	_, token := registerAndLogin(t, ts, "alice", "password123", "daemon")
	conn := dialDaemonWS(t, ts, token)
	defer conn.Close()

	// An idle daemon WS only sends pings (control frames), which the client
	// pong-handler answers. A ReadMessage on a quiet connection should hit our
	// short deadline rather than return real data — that's the "happy path"
	// for connection establishment with nothing pending.
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected a timeout on idle daemon ws (no messages pending)")
	}
}

func TestCall_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := doJSON(t, &http.Client{}, http.MethodPost, ts.URL+"/api/call", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCall_MissingTarget(t *testing.T) {
	ts, _ := newTestServer(t)
	caller, _ := registerAndLogin(t, ts, "bob", "password123", "browser")
	resp := doJSON(t, caller, http.MethodPost, ts.URL+"/api/call", map[string]string{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCall_TargetOffline_ReturnsUnavailable(t *testing.T) {
	ts, _ := newTestServer(t)
	caller, _ := registerAndLogin(t, ts, "bob", "password123", "browser")
	callee, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	makeFriends(t, ts, caller, "bob", callee, "alice")

	resp := doJSON(t, caller, http.MethodPost, ts.URL+"/api/call", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "unavailable" {
		t.Errorf("status = %v, want unavailable", body["status"])
	}
	if token, ok := body["token"].(string); !ok || token == "" {
		t.Error("expected non-empty caller token even when target is offline")
	}
	if body["room_name"] == nil || body["room_name"] == "" {
		t.Error("expected a room_name even when target is offline")
	}
}

func TestCall_TargetOnline_PushesIncomingCallAndReturnsRinging(t *testing.T) {
	ts, _ := newTestServer(t)

	_, aliceToken := registerAndLogin(t, ts, "alice", "password123", "daemon")
	aliceConn := dialDaemonWS(t, ts, aliceToken)
	defer aliceConn.Close()

	caller, _ := registerAndLogin(t, ts, "bob", "password123", "browser")
	// Alice (browser-style) login so alice can accept bob's friend request.
	aliceBrowser, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	makeFriends(t, ts, caller, "bob", aliceBrowser, "alice")

	resp := doJSON(t, caller, http.MethodPost, ts.URL+"/api/call", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "ringing" {
		t.Fatalf("status = %v, want ringing", body["status"])
	}
	if body["identity"] != "bob" {
		t.Errorf("identity = %v, want bob (the caller)", body["identity"])
	}

	aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := aliceConn.ReadMessage()
	if err != nil {
		t.Fatalf("alice ws read: %v", err)
	}
	var env struct {
		Type           string `json:"type"`
		RoomName       string `json:"room_name"`
		CallerUsername string `json:"caller_username"`
		Token          string `json:"token"`
		Identity       string `json:"identity"`
		WebRoomURL     string `json:"web_room_url"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatalf("unmarshal incoming_call: %v", err)
	}
	if env.Type != "incoming_call" {
		t.Errorf("type = %q, want incoming_call", env.Type)
	}
	if env.CallerUsername != "bob" {
		t.Errorf("caller_username = %q, want bob", env.CallerUsername)
	}
	if env.Identity != "alice" {
		t.Errorf("identity = %q, want alice", env.Identity)
	}
	if env.Token == "" {
		t.Error("expected callee token in incoming_call")
	}
	if env.RoomName == "" {
		t.Error("expected non-empty room_name in incoming_call")
	}
	if env.WebRoomURL == "" {
		t.Error("expected non-empty web_room_url")
	}
	if body["room_name"] != env.RoomName {
		t.Errorf("caller room_name = %v, callee room_name = %q", body["room_name"], env.RoomName)
	}
}

func TestCall_DaemonReplaced_LastConnWins(t *testing.T) {
	ts, _ := newTestServer(t)
	_, aliceToken := registerAndLogin(t, ts, "alice", "password123", "daemon")

	// Re-login: per the single-active-daemon invariant, this rotates the daemon
	// token. The previous token must now be invalid.
	_, secondToken := registerAndLogin(t, ts, "alice", "password123", "daemon")

	// First (revoked) token must now be rejected by /ws/daemon.
	conn1, resp1, err := websocket.DefaultDialer.Dial(wsURLWithToken(ts, aliceToken), nil)
	if err == nil {
		conn1.Close()
		t.Fatal("expected revoked first daemon token to be rejected")
	}
	if resp1 == nil {
		t.Fatal("expected an HTTP response on revoked-token dial")
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked token dial: status = %d, want 401", resp1.StatusCode)
	}

	// Second daemon (new token) connects fine.
	conn2 := dialDaemonWS(t, ts, secondToken)
	defer conn2.Close()

	// Bob calls alice. Only the newest daemon should receive the push.
	caller, _ := registerAndLogin(t, ts, "bob", "password123", "browser")
	aliceBrowser, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	makeFriends(t, ts, caller, "bob", aliceBrowser, "alice")

	resp := doJSON(t, caller, http.MethodPost, ts.URL+"/api/call", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("call status = %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "ringing" {
		t.Fatalf("status = %v, want ringing", body["status"])
	}

	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn2.ReadMessage(); err != nil {
		t.Fatalf("expected alice (new daemon) to receive incoming_call: %v", err)
	}
}

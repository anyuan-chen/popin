package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/popin/popin/auth"
	"github.com/popin/popin/room"
)

// daemon WS server→client message types.
const (
	daemonMsgIncomingCall = "incoming_call"
	daemonMsgReplaced     = "replaced"
)

// callRequest is the body of POST /api/call.
type callRequest struct {
	TargetUsername string `json:"target_username"`
	RoomName       string `json:"room_name"`
}

// callResponse is returned by POST /api/call.
type callResponse struct {
	Status     string `json:"status"`
	RoomName   string `json:"room_name,omitempty"`
	Token      string `json:"token,omitempty"`
	Identity   string `json:"identity,omitempty"`
	LiveKitURL string `json:"livekit_url,omitempty"`
}

// incomingCallMsg is pushed to the target daemon over its WebSocket.
type incomingCallMsg struct {
	Type           string `json:"type"`
	CallID         string `json:"call_id"`
	RoomName       string `json:"room_name"`
	CallerUsername string `json:"caller_username"`
	Token          string `json:"token"`
	Identity       string `json:"identity"`
	LiveKitURL     string `json:"livekit_url"`
	WebRoomURL     string `json:"web_room_url"`
}

// daemonConn wraps an authenticated daemon websocket with basic metadata.
type daemonConn struct {
	conn     *websocket.Conn
	username string
	writeMu  sync.Mutex
}

func (d *daemonConn) writeJSON(v any) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return d.conn.WriteJSON(v)
}

// daemonRegistry tracks live daemon websocket connections keyed by username.
// It is the source of truth for "is <user> online right now". A Server holds
// one daemonRegistry. All access goes through the registry's mutex; the WS
// handler treats last-connection-wins per user.
type daemonRegistry struct {
	mu    sync.Mutex
	conns map[string]*daemonConn
}

func newDaemonRegistry() *daemonRegistry {
	return &daemonRegistry{conns: make(map[string]*daemonConn)}
}

// register installs conn for username. If a connection already exists for that
// user, it is told to close itself and is replaced (last-connection-wins). The
// caller must subsequently run the read loop and call unregister when done.
//
// Returns the previous connection (already notified) so the caller can close
// it, or nil.
func (r *daemonRegistry) register(username string, conn *websocket.Conn) *daemonConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.conns[username]
	if prev != nil {
		// best-effort notify the prior connection it has been replaced
		_ = prev.writeJSON(map[string]string{"type": daemonMsgReplaced})
		_ = prev.conn.Close()
	}
	r.conns[username] = &daemonConn{conn: conn, username: username}
	return prev
}

// unregister removes conn only if it is the currently-registered connection for
// username. This avoids a stale read loop removing a fresh connection (which
// could happen after a replace race).
func (r *daemonRegistry) unregister(username string, conn *daemonConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur := r.conns[username]; cur == conn {
		delete(r.conns, username)
	}
}

// lookup returns the live daemon connection for username, or nil.
func (r *daemonRegistry) lookup(username string) *daemonConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns[username]
}

// sendIncomingCall delivers an incoming_call message to username's daemon.
// Returns true iff a live daemon received it.
func (r *daemonRegistry) sendIncomingCall(username string, msg incomingCallMsg) bool {
	c := r.lookup(username)
	if c == nil {
		return false
	}
	return c.writeJSON(msg) == nil
}

var daemonUpgrader = websocket.Upgrader{
	// Daemon presents its token via query string, so Origin checking is moot;
	// auth is enforced by RequireDaemon before upgrade. Allow any origin.
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	daemonPingInterval = 30 * time.Second
	daemonReadTimeout  = 70 * time.Second // > ping interval to allow pong slack
)

// handleDaemonWS upgrades (after RequireDaemon) and runs the daemon read loop.
// Presence is established the moment the connection is registered; removal
// happens when the read loop exits (client disconnect, ping timeout, replace).
func (s *Server) handleDaemonWS(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := daemonUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		log.Printf("daemon ws upgrade for %q: %v", user.Username, err)
		return
	}
	defer conn.Close()

	s.registry.register(user.Username, conn)
	d := s.registry.lookup(user.Username)
	defer func() {
		if d != nil {
			s.registry.unregister(user.Username, d)
		}
	}()

	log.Printf("daemon connected: %s", user.Username)

	conn.SetReadDeadline(time.Now().Add(daemonReadTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(daemonReadTimeout))
		return nil
	})

	// Ping ticker keeps the connection alive and detects dead clients.
	pingCtx, pingCancel := context.WithCancel(r.Context())
	defer pingCancel()
	go func() {
		t := time.NewTicker(daemonPingInterval)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				if err := conn.WriteControl(
					websocket.PingMessage, nil, time.Now().Add(10*time.Second),
				); err != nil {
					return
				}
			}
		}
	}()

	// Read loop. We mostly ignore inbound messages, but drain them so the
	// connection's close handling fires promptly. The daemon may send
	// {type:"accepted", call_id} acknowledgements; MVP does not act on them.
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("daemon read for %s ended: %v", user.Username, err)
			}
			return
		}
		conn.SetReadDeadline(time.Now().Add(daemonReadTimeout))
	}
}

// generateRoomName produces a short, opaque, lowercase room name when the
// caller doesn't supply one. It only needs to be unique enough that two
// spontaneous calls don't collide; LiveKit treats room names as opaque IDs.
// Entropy is drawn from the nanosecond clock shifts, which is adequate for a
// single-instance, low-rate call server; room collisions are not harmful
// (both callers simply join the same LiveKit room).
func generateRoomName() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	now := time.Now().UnixNano()
	for i := 0; i < 12; i++ {
		b[i] = alphabet[(now>>uint(i*5))%int64(len(alphabet))]
	}
	return "popin-" + string(b)
}

// handleCall is the POST /api/call handler (browser-auth required).
// Initiates a call to target_username by pushing an incoming_call message to
// that user's daemon (if online) and returning a LiveKit token so the caller
// can join the room immediately and wait for the callee.
func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req callRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.TargetUsername == "" {
		writeError(w, "target_username is required", http.StatusBadRequest)
		return
	}

	roomName := strings.TrimSpace(req.RoomName)
	if roomName == "" {
		roomName = generateRoomName()
	}

	caller, _ := auth.UserFromContext(r.Context())

	// Calls are restricted to accepted friends. This preserves the social
	// invariant that the target has consented to being reachable by the
	// caller; without an accepted friendship the call is refused before any
	// LiveKit token is minted or the daemon is pinged. We re-resolve the
	// target's user ID here (the friend.Service does not expose a lookup) so
	// AreFriends can be checked before any expensive token work.
	var targetID int64
	if err := s.authSvc.DB().QueryRowContext(r.Context(),
		`SELECT id FROM users WHERE username = ?`, req.TargetUsername,
	).Scan(&targetID); err != nil {
		writeError(w, "user not found", http.StatusNotFound)
		return
	}
	ok, err := s.friendSvc.AreFriends(r.Context(), caller.ID, targetID)
	if err != nil {
		log.Printf("call: AreFriends(%d,%d): %v", caller.ID, targetID, err)
		writeError(w, "Failed to verify friendship", http.StatusInternalServerError)
		return
	}
	if !ok {
		writeError(w, "not friends", http.StatusForbidden)
		return
	}

	// Build caller's token so the caller can join the room right away.
	callerToken, err := s.roomMgr.GenerateToken(
		s.cfg.LiveKitAPIKey, s.cfg.LiveKitSecret,
		room.TokenParams{
			RoomName:       roomName,
			Identity:       caller.Username,
			CanPublish:     true,
			CanSubscribe:   true,
			CanPublishData: true,
		},
	)
	if err != nil {
		log.Printf("call: caller token for %q: %v", caller.Username, err)
		writeError(w, "Failed to generate caller token", http.StatusInternalServerError)
		return
	}

	// Callee token + outgoing message, sent only if the callee daemon is online.
	calleeToken, err := s.roomMgr.GenerateToken(
		s.cfg.LiveKitAPIKey, s.cfg.LiveKitSecret,
		room.TokenParams{
			RoomName:       roomName,
			Identity:       req.TargetUsername,
			CanPublish:     true,
			CanSubscribe:   true,
			CanPublishData: true,
		},
	)
	if err != nil {
		log.Printf("call: callee token for %q: %v", req.TargetUsername, err)
		writeError(w, "Failed to generate callee token", http.StatusInternalServerError)
		return
	}

	msg := incomingCallMsg{
		Type:           daemonMsgIncomingCall,
		CallID:         generateCallID(),
		RoomName:       roomName,
		CallerUsername: caller.Username,
		Token:          calleeToken,
		Identity:       req.TargetUsername,
		LiveKitURL:     s.cfg.LiveKitClientURL,
		WebRoomURL:     buildWebRoomURL(s.cfg.WebURL, roomName, calleeToken, req.TargetUsername, s.cfg.LiveKitClientURL),
	}

	if !s.registry.sendIncomingCall(req.TargetUsername, msg) {
		// Callee daemon offline: still give the caller a token so they can
		// sit in the (empty) room. The UI may show "no answer" after a timeout.
		// Documented behavior — caller handles the wait client-side.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(callResponse{
			Status:     "unavailable",
			RoomName:   roomName,
			Token:      callerToken,
			Identity:   caller.Username,
			LiveKitURL: s.cfg.LiveKitClientURL,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(callResponse{
		Status:     "ringing",
		RoomName:   roomName,
		Token:      callerToken,
		Identity:   caller.Username,
		LiveKitURL: s.cfg.LiveKitClientURL,
	})
}

// buildWebRoomURL constructs the browser URL the callee's daemon will open.
// The web app's /room page accepts these query params and joins LiveKit
// directly — no web-app login required on the callee's machine.
func buildWebRoomURL(webURL, roomName, token, identity, livekitURL string) string {
	u, err := url.Parse(webURL)
	if err != nil || u.Scheme == "" {
		u, _ = url.Parse("http://localhost:3000")
	}
	u = u.JoinPath("/room")
	q := u.Query()
	q.Set("name", roomName)
	q.Set("token", token)
	q.Set("identity", identity)
	q.Set("livekit_url", livekitURL)
	u.RawQuery = q.Encode()
	return u.String()
}

func generateCallID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = alphabet[(now>>uint(i*4))%int64(len(alphabet))]
	}
	return string(b)
}

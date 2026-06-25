package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/popin/popin/auth"
	"github.com/popin/popin/config"
	"github.com/popin/popin/friend"
	"github.com/popin/popin/room"
)

type Server struct {
	cfg       *config.Config
	roomMgr   *room.Manager
	authSvc   *auth.AuthService
	authMW    *auth.Middleware
	friendSvc *friend.Service
	registry  *daemonRegistry
	mux       *http.ServeMux
}

func New(cfg *config.Config, authSvc *auth.AuthService) *Server {
	authMW := auth.NewMiddleware(authSvc, cfg.CookieSecure, cfg.CookieDomain)

	s := &Server{
		cfg:       cfg,
		roomMgr:   room.NewManager(cfg.LiveKitURL, cfg.LiveKitAPIKey, cfg.LiveKitSecret),
		authSvc:   authSvc,
		authMW:    authMW,
		friendSvc: friend.NewService(authSvc.DB()),
		registry:  newDaemonRegistry(),
		mux:       http.NewServeMux(),
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)

	s.mux.HandleFunc("/api/auth/register", s.handleRegister)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/auth/me", s.authMW.RequireAuth(http.HandlerFunc(s.handleMe)).ServeHTTP)

	s.mux.Handle("/api/rooms/create", s.authMW.RequireAuth(http.HandlerFunc(s.handleCreateRoom)))
	s.mux.Handle("/api/token", s.authMW.RequireAuth(http.HandlerFunc(s.handleGenerateToken)))
	s.mux.Handle("/api/call", s.authMW.RequireAuth(http.HandlerFunc(s.handleCall)))

	s.mux.Handle("/api/friends/request", s.authMW.RequireAuth(http.HandlerFunc(s.handleFriendRequest)))
	s.mux.Handle("/api/friends/accept", s.authMW.RequireAuth(http.HandlerFunc(s.handleFriendAccept)))
	s.mux.Handle("/api/friends/deny", s.authMW.RequireAuth(http.HandlerFunc(s.handleFriendDeny)))
	s.mux.Handle("/api/friends/unfriend", s.authMW.RequireAuth(http.HandlerFunc(s.handleFriendUnfriend)))
	s.mux.Handle("/api/friends", s.authMW.RequireAuth(http.HandlerFunc(s.handleFriendList)))

	s.mux.HandleFunc("/api/rooms", s.handleRooms)

	// /ws/daemon is daemon-kind only; the handler upgrades after auth.
	s.mux.Handle("/ws/daemon", s.authMW.RequireDaemon(http.HandlerFunc(s.handleDaemonWS)))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.corsMiddleware(s.mux).ServeHTTP(w, r)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	origins := strings.Split(s.cfg.CORSAllowedOrigins, ",")
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	user, err := s.authSvc.Register(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrUsernameTaken) {
			writeError(w, err.Error(), http.StatusConflict)
			return
		}
		log.Printf("Error registering user: %v", err)
		writeError(w, "Registration failed", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Kind     string `json:"kind"`
		Redirect string `json:"redirect"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	kind := req.Kind
	if kind == "" {
		kind = auth.KindBrowser
	}
	if !auth.IsValidKind(kind) {
		writeError(w, "invalid kind", http.StatusBadRequest)
		return
	}

	// A daemon-kind login mints a long-lived daemon token for use by the
	// CLI. It must only be allowed when the calling browser intends to send
	// the token back to a localhost callback (the daemon's local server).
	// We therefore require a redirect whose host is loopback. This keeps
	// the daemon-token flow from being usable to mint bearer tokens for
	// arbitrary third parties.
	if kind == auth.KindDaemon {
		if !isLoopbackRedirect(req.Redirect) {
			writeError(w, "daemon login requires a loopback redirect", http.StatusBadRequest)
			return
		}
	}

	token, user, err := s.authSvc.Login(req.Username, req.Password, kind)
	if err != nil {
		writeError(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	// Daemon-kind sessions are NOT cookies: daemons have no cookie jar. We
	// return the token in the JSON body so the web app can pass it back via
	// the daemon's localhost callback URL.
	if kind == auth.KindDaemon {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"token":    token,
			"kind":     kind,
		})
		return
	}

	s.authMW.SetSessionCookie(w, token, int(s.cfg.SessionDuration.Seconds()))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// isLoopbackRedirect returns true iff redirect is an http(s) URL whose host is
// localhost or 127.0.0.1 (any port). Empty/invalid URLs return false.
func isLoopbackRedirect(redirect string) bool {
	if redirect == "" {
		return false
	}
	u, err := url.Parse(redirect)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1"
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if cookie, err := r.Cookie(auth.CookieName); err == nil {
		s.authSvc.Logout(r.Context(), cookie.Value)
	}

	s.authMW.ClearSessionCookie(w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "logged out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rooms, err := s.roomMgr.ListRooms(r.Context())
	if err != nil {
		log.Printf("Error listing rooms: %v", err)
		writeError(w, "Failed to list rooms", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rooms)
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		writeError(w, "Room name is required", http.StatusBadRequest)
		return
	}

	room, err := s.roomMgr.CreateRoom(r.Context(), req.Name)
	if err != nil {
		log.Printf("Error creating room: %v", err)
		writeError(w, "Failed to create room", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(room)
}

func (s *Server) handleGenerateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RoomName string `json:"room_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.RoomName == "" {
		writeError(w, "room_name is required", http.StatusBadRequest)
		return
	}

	user, _ := auth.UserFromContext(r.Context())

	token, err := s.roomMgr.GenerateToken(
		s.cfg.LiveKitAPIKey,
		s.cfg.LiveKitSecret,
		room.TokenParams{
			RoomName:       req.RoomName,
			Identity:       user.Username,
			CanPublish:     true,
			CanSubscribe:   true,
			CanPublishData: true,
		},
	)
	if err != nil {
		log.Printf("Error generating token: %v", err)
		writeError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token":       token,
		"identity":    user.Username,
		"livekit_url": s.cfg.LiveKitClientURL,
	})
}

func (s *Server) Start(ctx context.Context) error {
	log.Printf("Starting server on port %s", s.cfg.Port)
	log.Printf("LiveKit URL: %s", s.cfg.LiveKitURL)
	log.Printf("DB: %s", s.cfg.DBPath)

	server := &http.Server{
		Addr:    ":" + s.cfg.Port,
		Handler: s,
	}

	go func() {
		<-ctx.Done()
		log.Println("Shutting down server...")
		server.Shutdown(context.Background())
	}()

	return server.ListenAndServe()
}

func writeError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

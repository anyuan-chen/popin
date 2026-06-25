// Command popin is the CLI daemon that receives incoming video calls from
// other Popin users and auto-opens a browser tab to join the call.
//
// Subcommands:
//
//	popin login     Authorize the daemon by opening a browser tab to the web
//	                login page. Stores the resulting daemon token on disk.
//	popin run        (default) Connect to the backend and listen for incoming
//	                 calls indefinitely, opening a browser tab for each.
//	popin logout     Delete the stored daemon token (and revoke it server-side
//	                 if the backend is reachable).
//
// Flags (apply to run/login):
//
//	--backend URL   Backend base URL (default $BACKEND_URL or http://localhost:8080)
//	--web URL       Web app base URL  (default $WEB_URL or http://localhost:3000)
//
// The daemon token is stored at ~/.config/popin/daemon-token (0600). Re-running
// `popin login` replaces any existing daemon session for this user server-side
// (single active daemon per user).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

const (
	tokenFileName  = "daemon-token"
	loginTimeout   = 5 * time.Minute
	wsReconnectMin = 1 * time.Second
	wsReconnectMax = 30 * time.Second
	wsPingInterval = 30 * time.Second
)

// defaultBackendURL / defaultWebURL are overridable at build time via -ldflags
// "-X main.defaultBackendURL=... -X main.defaultWebURL=...". The released CLI
// is built against the hosted production URLs so `popin login` works without
// flags or environment; dev builds keep the localhost defaults.
var (
	defaultBackendURL = "http://localhost:8080"
	defaultWebURL     = "http://localhost:3000"
)

func main() {
	// godotenv makes local .env pick up BACKEND_URL/WEB_URL like the server.
	godotenv.Load()

	backend := flag.String("backend", envOr("BACKEND_URL", defaultBackendURL), "backend base URL")
	web := flag.String("web", envOr("WEB_URL", defaultWebURL), "web app base URL")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: popin [flags] <login|run|logout|friend [username]>\n\n")
		fmt.Fprintf(os.Stderr, "Subcommands:\n  login           authorize the daemon via browser\n  run             listen for incoming calls (default)\n  logout          delete the daemon token\n  friend [user]   send a friend request to <user>, or open the friends TUI\n                  (accept/deny incoming, unfriend) when no argument given\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	cmd := "run"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}

	cfg := &daemonConfig{BackendURL: strings.TrimRight(*backend, "/"), WebURL: strings.TrimRight(*web, "/")}

	switch cmd {
	case "login":
		if err := runLogin(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
			os.Exit(1)
		}
	case "logout":
		if err := runLogout(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "logout failed: %v\n", err)
			os.Exit(1)
		}
	case "friend":
		if err := runFriend(cfg, flag.Args()[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "friend failed: %v\n", err)
			os.Exit(1)
		}
	case "run", "":
		if err := runDaemon(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "daemon failed: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", cmd)
		flag.Usage()
		os.Exit(2)
	}
}

type daemonConfig struct {
	BackendURL string
	WebURL     string
}

// wsURL derives a ws/wss URL from the backend's http(s) base URL.
func (c *daemonConfig) wsURL(path string) string {
	u, err := url.Parse(c.BackendURL)
	if err != nil || u.Scheme == "" {
		u, _ = url.Parse("http://localhost:8080")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = path
	return u.String()
}

// ---------------------------------------------------------------------------
// login
// ---------------------------------------------------------------------------

func runLogin(cfg *daemonConfig) error {
	// Bind a free localhost port for the OAuth-style callback.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}
		// Respond to the browser so the user sees success and can close the tab.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><meta charset=utf-8><title>Popin</title>
<body style="font:14px/1.5 system-ui;background:#0a0a0a;color:#eee;display:flex;align-items:center;justify-content:center;height:100vh;margin:0">
<div style="text-align:center"><h2>Authorized</h2><p>You can close this tab and return to your terminal.</p></div></body>`)
		select {
		case tokenCh <- token:
		default:
		}
	})}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	defer srv.Shutdown(context.Background())

	// Open the browser to the web login page, asking it to send the daemon
	// token back to our callback.
	loginURL := cfg.WebURL + "/login?redirect=" + url.QueryEscape(callbackURL)
	fmt.Printf("Opening browser to authorize Popin...\n  %s\n", loginURL)
	if err := openBrowser(loginURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser automatically: %v\n", err)
		fmt.Fprintf(os.Stderr, "Open this URL manually:\n  %s\n", loginURL)
	}

	fmt.Print("Waiting for authorization (up to 5 minutes)...")
	select {
	case token := <-tokenCh:
		fmt.Println(" authorized.")
		if err := saveToken(token); err != nil {
			return fmt.Errorf("save token: %w", err)
		}
		username, err := fetchMe(cfg, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Saved token but could not confirm username: %v\n", err)
		} else {
			fmt.Printf("Logged in as %s.\n", username)
		}
		fmt.Println("Now run `popin run` to listen for incoming calls.")
		return nil
	case err := <-errCh:
		return fmt.Errorf("callback server: %w", err)
	case <-time.After(loginTimeout):
		return errors.New("login timed out")
	}
}

func fetchMe(cfg *daemonConfig, token string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, cfg.BackendURL+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var u struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", err
	}
	return u.Username, nil
}

// ---------------------------------------------------------------------------
// logout
// ---------------------------------------------------------------------------

func runLogout(cfg *daemonConfig) error {
	token, err := loadToken()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("Not logged in.")
			return nil
		}
		return err
	}
	// Best-effort revoke server-side.
	req, _ := http.NewRequest(http.MethodPost, cfg.BackendURL+"/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	if err := deleteToken(); err != nil {
		return err
	}
	fmt.Println("Logged out.")
	return nil
}

// ---------------------------------------------------------------------------
// run (daemon loop)
// ---------------------------------------------------------------------------

func runDaemon(cfg *daemonConfig) error {
	token, err := loadToken()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("not logged in; run `popin login` first")
		}
		return err
	}
	username, err := fetchMe(cfg, token)
	if err != nil {
		return fmt.Errorf("could not verify daemon token (is the backend up?): %w", err)
	}
	fmt.Printf("Popin daemon online as %s. Waiting for incoming calls.\n", username)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	backoff := wsReconnectMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		replaced, err := connectOnce(ctx, cfg, token)
		if ctx.Err() != nil {
			return nil
		}
		if replaced {
			fmt.Println("Replaced by another daemon session. Exiting.")
			return nil
		}
		if err != nil {
			log.Printf("connection lost: %v", err)
		}
		fmt.Printf("Reconnecting in %s...\n", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > wsReconnectMax {
			backoff = wsReconnectMax
		}
	}
}

// connectOnce opens the daemon websocket, reads messages until the connection
// closes, and processes incoming calls. Returns (replaced, err): replaced is
// true if the server told us we've been superseded by another daemon.
func connectOnce(ctx context.Context, cfg *daemonConfig, token string) (bool, error) {
	u := cfg.wsURL("/ws/daemon") + "?token=" + url.QueryEscape(token)

	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.DialContext(ctx, u, nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusForbidden {
			return false, errors.New("server rejected daemon token (run `popin login` again)")
		}
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return false, errors.New("daemon token invalid or expired (run `popin login` again)")
		}
		return false, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go func() {
		t := time.NewTicker(wsPingInterval)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	var replaced bool
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if replaced {
				return true, nil
			}
			return false, err
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &env); err != nil {
			continue
		}
		switch env.Type {
		case "incoming_call":
			handleIncomingCall(msg)
		case "replaced":
			replaced = true
		}
	}
}

func handleIncomingCall(msg []byte) {
	var c struct {
		RoomName       string `json:"room_name"`
		CallerUsername string `json:"caller_username"`
		WebRoomURL     string `json:"web_room_url"`
	}
	if err := json.Unmarshal(msg, &c); err != nil {
		log.Printf("malformed incoming_call: %v", err)
		return
	}
	fmt.Printf("\nIncoming call from %s | room %s\n  Opening: %s\n", c.CallerUsername, c.RoomName, c.WebRoomURL)
	if err := openBrowser(c.WebRoomURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser: %v\n  Open manually: %s\n", err, c.WebRoomURL)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func tokenPath() (string, error) {
	dir := os.Getenv("POPIN_TOKEN_DIR")
	if dir != "" {
		return filepath.Join(dir, tokenFileName), nil
	}
	c, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "popin", tokenFileName), nil
}

func saveToken(token string) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(token), 0o600)
}

func loadToken() (string, error) {
	p, err := tokenPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func deleteToken() error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// openBrowser attempts to open the given URL in the system's default browser.
func openBrowser(rawURL string) error {
	if _, err := exec.LookPath("open"); err == nil {
		return exec.Command("open", rawURL).Start()
	}
	if _, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command("xdg-open", rawURL).Start()
	}
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", "start", "", rawURL).Start()
	}
	return errors.New("no supported browser opener found (looked for 'open', 'xdg-open', Windows 'start')")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

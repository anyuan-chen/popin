// Command popin is the CLI daemon that receives incoming video calls from
// other Popin users and auto-opens a browser tab to join the call.
//
// Subcommands:
//
//	popin login     Authorize the daemon by opening a browser tab to the web
//	                login page. Stores the resulting daemon token on disk.
//	popin signup     Like login, but opens the web page in register mode so a
//	                new account can be created and authorized in one go.
//	popin listen     Connect to the server and listen for incoming calls
//	                 indefinitely, opening a browser tab for each.
//	popin logout     Delete the stored daemon token (and revoke it server-side
//	                 if the server is reachable).
//	popin config     Show or set the server/web URLs (persisted to
//	                 ~/.config/popin/config.json).
//
// URL resolution precedence (highest wins):
//
//  1. --server / --web flags on the current command
//  2. values saved by `popin config` (config.json)
//  3. BACKEND_URL / WEB_URL environment variables (or .env)
//  4. built-in defaults (baked in at build time for release binaries)
//
// With no subcommand, `popin` prints help (like `popin --help`).
//
// Flags (apply to listen/login/signup):
//
//	--server URL   Server base URL (overrides config/env)
//	--web URL      Web app base URL (overrides config/env)
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
	configFileName = "config.json"
	loginTimeout   = 5 * time.Minute
	wsReconnectMin = 1 * time.Second
	wsReconnectMax = 30 * time.Second
	wsPingInterval = 30 * time.Second
)

// defaultServerURL / defaultWebURL are overridable at build time via -ldflags
// "-X main.defaultServerURL=... -X main.defaultWebURL=...". The released CLI
// is built against the hosted production URLs so `popin login` works without
// flags or environment; dev builds keep the localhost defaults.
var (
	defaultServerURL = "http://localhost:8080"
	defaultWebURL    = "http://localhost:3000"
)

func main() {
	// godotenv makes local .env pick up BACKEND_URL/WEB_URL like the server.
	godotenv.Load()

	server := flag.String("server", "", "server base URL (overrides `popin config` / env)")
	web := flag.String("web", "", "web app base URL (overrides `popin config` / env)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: popin [flags] <login|signup|config|listen|logout|friend [username]>\n\n")
		fmt.Fprintf(os.Stderr, "Subcommands:\n  login           authorize the phone attendant via browser\n  signup          register a new account + authorize the phone attendant via browser\n  config          show or set the server/web URLs (persisted to ~/.config/popin/config.json)\n  listen          listen for incoming calls\n  logout          delete the phone attendant token\n  friend [user]   send a friend request to <user>, or open the friends TUI\n                  (accept/deny incoming, unfriend) when no argument given\n\n")
		fmt.Fprintf(os.Stderr, "With no subcommand, prints this help (same as `popin --help`).\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// With no subcommand, show help instead of implicitly starting the
	// listener. Users must opt in with `popin listen`.
	if flag.NArg() == 0 {
		flag.Usage()
		return
	}

	cmd := flag.Arg(0)

	// `popin config` manages the persisted URLs; handle it before resolving
	// URLs for the other commands. It parses its own flags (which appear
	// *after* the subcommand, e.g. `popin config --server X`).
	if cmd == "config" {
		cfgSet := flag.NewFlagSet("config", flag.ExitOnError)
		s := cfgSet.String("server", "", "server base URL")
		w := cfgSet.String("web", "", "web app base URL")
		cfgSet.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: popin config [--server URL] [--web URL]\n\n")
			fmt.Fprintf(os.Stderr, "With no flags, prints the currently resolved URLs.\n")
			fmt.Fprintf(os.Stderr, "With flags, saves them to ~/.config/popin/config.json (either flag is optional).\n\n")
			cfgSet.PrintDefaults()
		}
		cfgSet.Parse(flag.Args()[1:])
		cfgSetChanged := map[string]bool{}
		cfgSet.Visit(func(f *flag.Flag) { cfgSetChanged[f.Name] = true })
		if err := runConfig(*s, *w, cfgSetChanged["server"], cfgSetChanged["web"]); err != nil {
			fmt.Fprintf(os.Stderr, "config failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fileCfg, _ := loadURLConfig() // missing file is fine; fall back to env/defaults
	cfg := &daemonConfig{
		ServerURL: pick(*server, set["server"], fileCfg.ServerURL, "BACKEND_URL", defaultServerURL),
		WebURL:    pick(*web, set["web"], fileCfg.WebURL, "WEB_URL", defaultWebURL),
	}

	switch cmd {
	case "login":
		if err := runLogin(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
			os.Exit(1)
		}
	case "signup":
		if err := runSignup(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "signup failed: %v\n", err)
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
	case "listen":
		if err := runDaemon(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "phone attendant failed: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", cmd)
		flag.Usage()
		os.Exit(2)
	}
}

type daemonConfig struct {
	ServerURL string
	WebURL    string
}

// wsURL derives a ws/wss URL from the server's http(s) base URL.
func (c *daemonConfig) wsURL(path string) string {
	u, err := url.Parse(c.ServerURL)
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
	return runBrowserAuth(cfg, "login")
}

// runSignup is like runLogin but opens the page in register mode so a user
// without an account can create one and authorize the daemon in a single flow.
func runSignup(cfg *daemonConfig) error {
	return runBrowserAuth(cfg, "signup")
}

// runBrowserAuth opens the web <page> (/login or /signup) with a callback,
// waits for the browser to POST back a daemon token, and saves it.
func runBrowserAuth(cfg *daemonConfig, page string) error {
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

	// Open the browser to the web page, asking it to send the daemon
	// token back to our callback.
	loginURL := cfg.WebURL + "/" + page + "?redirect=" + url.QueryEscape(callbackURL)
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
		fmt.Println("Now run `popin listen` to listen for incoming calls.")
		return nil
	case err := <-errCh:
		return fmt.Errorf("callback server: %w", err)
	case <-time.After(loginTimeout):
		return errors.New("login timed out")
	}
}

func fetchMe(cfg *daemonConfig, token string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, cfg.ServerURL+"/api/auth/me", nil)
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
	req, _ := http.NewRequest(http.MethodPost, cfg.ServerURL+"/api/auth/logout", nil)
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
// listen (daemon loop)
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
		return fmt.Errorf("could not verify phone attendant token (is the backend up?): %w", err)
	}
	fmt.Printf("Popin phone attendant online as %s. Waiting for incoming calls.\n", username)

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
			fmt.Println("Replaced by another phone attendant session. Exiting.")
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
			return false, errors.New("server rejected phone attendant token (run `popin login` again)")
		}
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return false, errors.New("phone attendant token invalid or expired (run `popin login` again)")
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

// pick resolves a single URL following the documented precedence: explicit
// flag value > config-file value > environment variable > built-in default.
// flagVal is the --server/--web string (empty when the flag was not passed);
// flagChanged distinguishes a deliberately empty-ish override from "not set",
// though in practice URLs are never empty so the flag value is used as-is.
func pick(flagVal string, flagChanged bool, fileVal, envKey, def string) string {
	if flagChanged && flagVal != "" {
		return strings.TrimRight(flagVal, "/")
	}
	if fileVal != "" {
		return strings.TrimRight(fileVal, "/")
	}
	if v := os.Getenv(envKey); v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

// urlConfig is the persisted ~/.config/popin/config.json. Both fields are
// optional; a field set to "" means "leave it unset (fall back to env/default)".
type urlConfig struct {
	ServerURL string `json:"server_url,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
}

// runConfig implements `popin config`. With URL flags it updates config.json;
// without flags it prints the currently resolved URLs.
func runConfig(serverVal, webVal string, serverChanged, webChanged bool) error {
	if !serverChanged && !webChanged {
		// Print-only mode: show the fully resolved URLs.
		fileCfg, err := loadURLConfig()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		server := pick("", false, fileCfg.ServerURL, "BACKEND_URL", defaultServerURL)
		web := pick("", false, fileCfg.WebURL, "WEB_URL", defaultWebURL)
		fmt.Printf("server: %s\nweb:   %s\n", server, web)
		fmt.Fprintln(os.Stderr, "(URLs resolve in order: --server/--web flags > `popin config` values > BACKEND_URL/WEB_URL env > built-in defaults)")
		return nil
	}

	// Update mode: merge provided flags into the existing file.
	fileCfg, err := loadURLConfig()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if serverChanged {
		fileCfg.ServerURL = strings.TrimRight(serverVal, "/")
	}
	if webChanged {
		fileCfg.WebURL = strings.TrimRight(webVal, "/")
	}
	if err := saveURLConfig(fileCfg); err != nil {
		return err
	}
	fmt.Printf("Saved to %s\nserver: %s\nweb:   %s\n", mustConfigPath(), fileCfg.ServerURL, fileCfg.WebURL)
	return nil
}

func configDir() (string, error) {
	if dir := os.Getenv("POPIN_TOKEN_DIR"); dir != "" {
		return dir, nil
	}
	c, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "popin"), nil
}

func mustConfigPath() string {
	dir, err := configDir()
	if err != nil {
		return configFileName
	}
	return filepath.Join(dir, configFileName)
}

func loadURLConfig() (urlConfig, error) {
	dir, err := configDir()
	if err != nil {
		return urlConfig{}, err
	}
	b, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		return urlConfig{}, err
	}
	var c urlConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return urlConfig{}, fmt.Errorf("parse %s: %w", configFileName, err)
	}
	return c, nil
}

func saveURLConfig(c urlConfig) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(filepath.Join(dir, configFileName), b, 0o600)
}

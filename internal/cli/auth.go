package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const loginTimeout = 5 * time.Minute

// Login authorizes the daemon by opening a browser tab to the web login page
// and stores the resulting daemon token on disk.
func Login(c *Config) error {
	return runBrowserAuth(c, "login", "")
}

// Signup is like Login but opens the page in register mode so a new account
// can be created and authorized in one go.
func Signup(c *Config) error {
	return runBrowserAuth(c, "login", "register")
}

// runBrowserAuth opens the web <page> (/login) with a callback, waits for the
// browser to POST back a daemon token, and saves it. mode, when non-empty,
// is passed as ?mode= so the login page can render in register (or other) mode.
func runBrowserAuth(c *Config, page, mode string) error {
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
	// token back to our callback. Build the URL via net/url so query params
	// (mode, redirect) are properly encoded regardless of c.WebURL shape.
	u, err := url.Parse(c.WebURL)
	if err != nil {
		return fmt.Errorf("parse web url: %w", err)
	}
	u.Path = "/" + page
	q := u.Query()
	if mode != "" {
		q.Set("mode", mode)
	}
	q.Set("redirect", callbackURL)
	u.RawQuery = q.Encode()
	loginURL := u.String()
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
		username, err := fetchMe(c, token)
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

func fetchMe(c *Config, token string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, c.ServerURL+"/api/auth/me", nil)
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

// Logout deletes the stored daemon token (and revokes it server-side if the
// server is reachable).
func Logout(c *Config) error {
	token, err := loadToken()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("Not logged in.")
			return nil
		}
		return err
	}
	// Best-effort revoke server-side.
	req, _ := http.NewRequest(http.MethodPost, c.ServerURL+"/api/auth/logout", nil)
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
// token storage
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

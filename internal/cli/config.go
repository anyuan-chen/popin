package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	tokenFileName  = "daemon-token"
	configFileName = "config.json"
)

// defaultServerURL / defaultWebURL are overridable at build time via -ldflags
// "-X main.defaultServerURL=... -X main.defaultWebURL=...". The released CLI
// is built against the hosted production URLs so `popin login` works without
// flags or environment; dev builds keep the localhost defaults.
var (
	defaultServerURL = "http://localhost:8080"
	defaultWebURL    = "http://localhost:3000"
)

// Config holds the resolved server and web URLs used by the CLI subcommands.
type Config struct {
	ServerURL string
	WebURL    string
}

// WSURL derives a ws/wss URL from the server's http(s) base URL.
func (c *Config) WSURL(path string) string {
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

// Resolve builds a Config following the documented precedence: explicit flag
// value > config-file value > environment variable > built-in default.
// flagVal is the --server/--web string (empty when the flag was not passed);
// flagChanged distinguishes a deliberately empty-ish override from "not set",
// though in practice URLs are never empty so the flag value is used as-is.
func Resolve(serverVal, webVal string, serverChanged, webChanged bool) (*Config, error) {
	fileCfg, _ := loadURLConfig() // missing file is fine; fall back to env/defaults
	return &Config{
		ServerURL: pick(serverVal, serverChanged, fileCfg.ServerURL, "BACKEND_URL", defaultServerURL),
		WebURL:    pick(webVal, webChanged, fileCfg.WebURL, "WEB_URL", defaultWebURL),
	}, nil
}

// pick resolves a single URL following the documented precedence: explicit
// flag value > config-file value > environment variable > built-in default.
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

// urlConfig is the persisted ~/.config/popin/config.json. Both fields are
// optional; a field set to "" means "leave it unset (fall back to env/default)".
type urlConfig struct {
	ServerURL string `json:"server_url,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
}

// RunConfig implements `popin config`. With URL flags it updates config.json;
// without flags it prints the currently resolved URLs.
func RunConfig(serverVal, webVal string, serverChanged, webChanged bool) error {
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

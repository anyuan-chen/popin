package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	LiveKitURL       string
	LiveKitClientURL string
	LiveKitAPIKey    string
	LiveKitSecret    string
	Port             string

	// BackendURL is the base URL of this Go backend (e.g. http://localhost:8080).
	// Used by the daemon to derive its WebSocket URL. The server's own config
	// ignores this — it's only consumed by the daemon.
	BackendURL string

	// WebURL is the base URL of the Next.js web app (e.g. http://localhost:3000).
	// Used to build browser-tab URLs the daemon opens on incoming calls.
	WebURL string

	DBPath             string
	SessionDuration    time.Duration
	CookieSecure       bool
	CookieDomain       string
	CORSAllowedOrigins string
}

func Load() *Config {
	godotenv.Load()

	return &Config{
		LiveKitURL:       getEnv("LIVEKIT_URL", "ws://localhost:7880"),
		LiveKitClientURL: getEnv("LIVEKIT_CLIENT_URL", "ws://localhost:7880"),
		LiveKitAPIKey:    getEnv("LIVEKIT_API_KEY", "devkey"),
		LiveKitSecret:    getEnv("LIVEKIT_API_SECRET", "secret"),
		Port:             getEnv("PORT", "8080"),

		BackendURL: getEnv("BACKEND_URL", "http://localhost:8080"),
		WebURL:     getEnv("WEB_URL", "http://localhost:3000"),

		DBPath:             getEnv("DB_PATH", "popin.db"),
		SessionDuration:    getEnvDuration("SESSION_DURATION", 99*365*24*time.Hour),
		CookieSecure:       getEnvBool("COOKIE_SECURE", false),
		CookieDomain:       os.Getenv("COOKIE_DOMAIN"),
		CORSAllowedOrigins: getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

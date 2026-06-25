package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const (
	userContextKey contextKey = "user"
	kindContextKey contextKey = "kind"
)

const CookieName = "popin_session"

type Middleware struct {
	service *AuthService
	secure  bool
	domain  string
}

func NewMiddleware(service *AuthService, secure bool, domain string) *Middleware {
	return &Middleware{
		service: service,
		secure:  secure,
		domain:  domain,
	}
}

// tokenFromRequest extracts the session token from either the popin_session
// cookie, an `?token=` query parameter, or an `Authorization: Bearer <token>`
// header. The query and bearer forms exist so non-browser clients (the CLI
// daemon) can authenticate without cookies.
func tokenFromRequest(r *http.Request) string {
	if cookie, err := r.Cookie(CookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return q
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// RequireAuth allows any valid session (browser or daemon).
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	allowAny := func(string) bool { return true }
	return m.authHandler(allowAny, next)
}

// RequireDaemon allows only daemon-kind sessions.
func (m *Middleware) RequireDaemon(next http.Handler) http.Handler {
	allowDaemon := func(kind string) bool { return kind == KindDaemon }
	return m.authHandler(allowDaemon, next)
}

// authHandler builds an http.Handler that verifies the session token, enforces
// kind via allowKind, stashes user + kind in the request context, then calls
// next. The token may come from the cookie, ?token= query, or Authorization
// header so that non-browser clients (the CLI daemon) can authenticate.
func (m *Middleware) authHandler(allowKind func(string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromRequest(r)
		if token == "" {
			writeUnauthorized(w)
			return
		}

		info, err := m.service.VerifySession(r.Context(), token)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		if !allowKind(info.Kind) {
			writeForbidden(w)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, info.User)
		ctx = context.WithValue(ctx, kindContextKey, info.Kind)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) SetSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	}
	if m.domain != "" {
		cookie.Domain = m.domain
	}
	http.SetCookie(w, cookie)
}

func (m *Middleware) ClearSessionCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	}
	if m.domain != "" {
		cookie.Domain = m.domain
	}
	http.SetCookie(w, cookie)
}

func UserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey).(*User)
	return user, ok
}

func KindFromContext(ctx context.Context) (string, bool) {
	kind, ok := ctx.Value(kindContextKey).(string)
	return kind, ok
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
}

// Package auth provides constant-time bearer-token verification and an
// HTTP middleware that enforces it. Static config tokens and OAuth 2.1
// access tokens are both accepted.
package auth

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// TokenValidator is implemented by the OAuth server to check access tokens.
type TokenValidator interface {
	ValidAccessToken(token string) bool
	WWWAuthenticate(r *http.Request) string
}

// ValidToken reports whether the Authorization header value (with or without
// the "Bearer " prefix) matches the expected token, compared in constant time.
func ValidToken(headerValue, want string) bool {
	if want == "" {
		return true
	}
	got := extractBearer(headerValue)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func extractBearer(headerValue string) string {
	got := strings.TrimSpace(headerValue)
	if len(got) >= 7 && strings.EqualFold(got[:7], "Bearer ") {
		got = strings.TrimSpace(got[7:])
	}
	return got
}

// RequireBearer wraps next so that requests must carry a valid bearer token.
// Loopback connections (127.0.0.1/::1 — on-device clients and local proxies)
// are exempt unless require is true. If token is empty, require is false, and
// oauth is nil, all requests pass through.
//
// When oauth is non-nil, its access tokens are accepted in addition to the
// static config token.
func RequireBearer(next http.Handler, token string, require bool, oauth TokenValidator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OAuth discovery and token endpoints must remain public.
		if isPublicOAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		needAuth := require || (token != "" && !isLoopback(r.RemoteAddr))
		if !needAuth && oauth == nil {
			next.ServeHTTP(w, r)
			return
		}
		// When OAuth is enabled we always require *some* credential on
		// non-loopback (or when require=true), so clients can complete the
		// discovery → authorize → token flow.
		if oauth != nil && (require || !isLoopback(r.RemoteAddr)) {
			needAuth = true
		}
		if !needAuth {
			next.ServeHTTP(w, r)
			return
		}

		got := extractBearer(r.Header.Get("Authorization"))
		ok := false
		if token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
			ok = true
		}
		if !ok && oauth != nil && oauth.ValidAccessToken(got) {
			ok = true
		}
		if !ok {
			wa := `Bearer realm="termux-mcp"`
			if oauth != nil {
				wa = oauth.WWWAuthenticate(r)
			}
			w.Header().Set("WWW-Authenticate", wa)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isPublicOAuthPath(path string) bool {
	switch path {
	case "/.well-known/oauth-protected-resource",
		"/.well-known/oauth-authorization-server",
		"/oauth/register",
		"/oauth/authorize",
		"/oauth/token",
		"/oauth/consent",
		"/health":
		return true
	default:
		return false
	}
}

// isLoopback reports whether remoteAddr is a loopback connection.
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

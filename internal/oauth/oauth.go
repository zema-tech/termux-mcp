// Package oauth implements a minimal OAuth 2.1 authorization server with
// PKCE (S256), dynamic client registration, and protected-resource metadata
// suitable for MCP Streamable HTTP clients (Grok, Claude, Composio, ...).
//
// Tokens are opaque and stored in memory. The static Bearer token from
// config continues to work alongside OAuth access tokens.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	codeTTL  = 10 * time.Minute
	tokenTTL = 24 * time.Hour

	// maxRegisteredClients caps dynamic client registrations to bound memory.
	maxRegisteredClients = 256
)

// Server is an in-process OAuth 2.1 AS + resource server helper.
type Server struct {
	mu      sync.RWMutex
	clients map[string]*client
	codes   map[string]*authCode
	tokens  map[string]*accessToken

	// PublicBase is the externally reachable base URL (e.g. tunnel URL).
	// If empty, it is inferred from each request's Host header.
	PublicBase string

	// ResourcePath is the MCP endpoint path, default "/mcp".
	ResourcePath string
}

type client struct {
	ID           string
	Secret       string // empty for public clients
	RedirectURIs []string
	Name         string
	Created      time.Time
}

type authCode struct {
	Code                string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	Expires             time.Time
	Used                bool
}

type accessToken struct {
	Token    string
	ClientID string
	Scope    string
	Expires  time.Time
	Created  time.Time
}

// New creates an OAuth server. publicBase may be empty (inferred per-request).
func New(publicBase string) *Server {
	s := &Server{
		clients:      make(map[string]*client),
		codes:        make(map[string]*authCode),
		tokens:       make(map[string]*accessToken),
		PublicBase:   strings.TrimRight(publicBase, "/"),
		ResourcePath: "/mcp",
	}
	go s.janitor()
	return s
}

// janitor periodically removes expired authorization codes and access tokens
// so the in-memory stores do not grow without bound.
func (s *Server) janitor() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		s.mu.Lock()
		for k, c := range s.codes {
			if now.After(c.Expires) {
				delete(s.codes, k)
			}
		}
		for k, tk := range s.tokens {
			if now.After(tk.Expires) {
				delete(s.tokens, k)
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) baseURL(r *http.Request) string {
	if s.PublicBase != "" {
		return s.PublicBase
	}
	scheme := "https"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS == nil {
		scheme = "http"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return scheme + "://" + host
}

func (s *Server) resourceURL(r *http.Request) string {
	return s.baseURL(r) + s.ResourcePath
}

// ValidAccessToken reports whether token is a live OAuth access token.
func (s *Server) ValidAccessToken(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	at, ok := s.tokens[token]
	return ok && time.Now().Before(at.Expires)
}

// --- HTTP handlers ---

// Mount registers all OAuth routes on mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.handleProtectedResource)
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleASMetadata)
	mux.HandleFunc("/oauth/register", s.handleRegister)
	mux.HandleFunc("/oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("/oauth/token", s.handleToken)
	mux.HandleFunc("/oauth/consent", s.handleConsentPage)
}

func (s *Server) handleProtectedResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := s.baseURL(r)
	writeJSON(w, map[string]any{
		"resource":                 s.resourceURL(r),
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"mcp"},
		"resource_name":            "termux-mcp",
	})
}

func (s *Server) handleASMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := s.baseURL(r)
	writeJSON(w, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		"scopes_supported":                      []string{"mcp"},
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	full := len(s.clients) >= maxRegisteredClients
	s.mu.RUnlock()
	if full {
		http.Error(w, `{\"error\":\"server_error\",\"error_description\":\"client registry full\"}`, http.StatusServiceUnavailable)
		return
	}
	var req struct {
		RedirectURIs []string `json:\"redirect_uris\"`
		ClientName   string   `json:\"client_name\"`
		TokenAuth    string   `json:\"token_endpoint_auth_method\"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{\"error\":\"invalid_request\"}`, http.StatusBadRequest)
		return
	}
	if len(req.RedirectURIs) == 0 {
		http.Error(w, `{\"error\":\"invalid_redirect_uri\"}`, http.StatusBadRequest)
		return
	}
	for _, u := range req.RedirectURIs {
		if _, err := url.ParseRequestURI(u); err != nil {
			http.Error(w, `{\"error\":\"invalid_redirect_uri\"}`, http.StatusBadRequest)
			return
		}
	}
	id := randomToken(16)
	secret := \"\"
	if req.TokenAuth == \"client_secret_post\" || req.TokenAuth == \"client_secret_basic\" {
		secret = randomToken(24)
	}
	c := &client{
		ID:           id,
		Secret:       secret,
		RedirectURIs: req.RedirectURIs,
		Name:         req.ClientName,
		Created:      time.Now(),
	}
	s.mu.Lock()
	s.clients[id] = c
	s.mu.Unlock()

	resp := map[string]any{
		\"client_id\":                  id,
		\"client_id_issued_at\":        c.Created.Unix(),
		\"redirect_uris\":              c.RedirectURIs,
		\"grant_types\":                []string{\"authorization_code\"},
		\"response_types\":             []string{\"code\"},
		\"token_endpoint_auth_method\": \"none\",
	}
	if secret != \"\" {
		resp[\"client_secret\"] = secret
		resp[\"token_endpoint_auth_method\"] = req.TokenAuth
	}
	if req.ClientName != \"\" {
		resp[\"client_name\"] = req.ClientName
	}
	w.Header().Set(\"Content-Type\", \"application/json\")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

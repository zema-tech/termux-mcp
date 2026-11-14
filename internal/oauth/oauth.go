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
	codeTTL   = 10 * time.Minute
	tokenTTL  = 24 * time.Hour
	clientTTL = 365 * 24 * time.Hour
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
	Token     string
	ClientID  string
	Scope     string
	Expires   time.Time
	Created   time.Time
}

// New creates an OAuth server. publicBase may be empty (inferred per-request).
func New(publicBase string) *Server {
	return &Server{
		clients:      make(map[string]*client),
		codes:        make(map[string]*authCode),
		tokens:       make(map[string]*accessToken),
		PublicBase:   strings.TrimRight(publicBase, "/"),
		ResourcePath: "/mcp",
	}
}

func (s *Server) baseURL(r *http.Request) string {
	if s.PublicBase != "" {
		return s.PublicBase
	}
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		// Cloudflare tunnel sets X-Forwarded-Proto; plain local stays http.
		if r.Header.Get("X-Forwarded-Proto") == "http" || r.TLS == nil {
			scheme = "http"
		}
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
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
	if !ok || at.Used || time.Now().After(at.Expires) {
		// fix: accessToken has no Used field — check expiry only
		_ = ok
	}
	at, ok = s.tokens[token]
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
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
		TokenAuth    string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	if len(req.RedirectURIs) == 0 {
		http.Error(w, `{"error":"invalid_redirect_uri"}`, http.StatusBadRequest)
		return
	}
	for _, u := range req.RedirectURIs {
		if _, err := url.ParseRequestURI(u); err != nil {
			http.Error(w, `{"error":"invalid_redirect_uri"}`, http.StatusBadRequest)
			return
		}
	}
	id := randomToken(16)
	secret := ""
	if req.TokenAuth == "client_secret_post" || req.TokenAuth == "client_secret_basic" {
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
		"client_id":                  id,
		"client_id_issued_at":        c.Created.Unix(),
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["token_endpoint_auth_method"] = req.TokenAuth
	}
	if req.ClientName != "" {
		resp["client_name"] = req.ClientName
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.authorizeGET(w, r)
	case http.MethodPost:
		s.authorizePOST(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) authorizeGET(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	respType := q.Get("response_type")
	state := q.Get("state")
	challenge := q.Get("code_challenge")
	method := q.Get("code_challenge_method")
	scope := q.Get("scope")
	if scope == "" {
		scope = "mcp"
	}

	if respType != "code" {
		http.Error(w, "unsupported response_type", http.StatusBadRequest)
		return
	}
	if clientID == "" || redirectURI == "" || challenge == "" {
		http.Error(w, "missing required parameters", http.StatusBadRequest)
		return
	}
	if method != "" && method != "S256" {
		http.Error(w, "code_challenge_method must be S256", http.StatusBadRequest)
		return
	}
	if method == "" {
		method = "S256"
	}

	s.mu.RLock()
	c, ok := s.clients[clientID]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if !containsURI(c.RedirectURIs, redirectURI) {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	// Show consent page.
	name := c.Name
	if name == "" {
		name = clientID
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, consentHTML,
		html.EscapeString(name),
		html.EscapeString(clientID),
		html.EscapeString(redirectURI),
		html.EscapeString(state),
		html.EscapeString(challenge),
		html.EscapeString(method),
		html.EscapeString(scope),
	)
}

func (s *Server) authorizePOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("decision") != "approve" {
		// Deny: redirect with error if possible.
		ru := r.Form.Get("redirect_uri")
		if ru != "" {
			u, err := url.Parse(ru)
			if err == nil {
				q := u.Query()
				q.Set("error", "access_denied")
				if st := r.Form.Get("state"); st != "" {
					q.Set("state", st)
				}
				u.RawQuery = q.Encode()
				http.Redirect(w, r, u.String(), http.StatusFound)
				return
			}
		}
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	clientID := r.Form.Get("client_id")
	redirectURI := r.Form.Get("redirect_uri")
	state := r.Form.Get("state")
	challenge := r.Form.Get("code_challenge")
	method := r.Form.Get("code_challenge_method")
	scope := r.Form.Get("scope")
	if scope == "" {
		scope = "mcp"
	}

	s.mu.RLock()
	c, ok := s.clients[clientID]
	s.mu.RUnlock()
	if !ok || !containsURI(c.RedirectURIs, redirectURI) {
		http.Error(w, "invalid client or redirect", http.StatusBadRequest)
		return
	}

	code := randomToken(24)
	ac := &authCode{
		Code:                code,
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
		Scope:               scope,
		Expires:             time.Now().Add(codeTTL),
	}
	s.mu.Lock()
	s.codes[code] = ac
	s.mu.Unlock()

	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, "invalid_request", "cannot parse form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("grant_type") != "authorization_code" {
		writeOAuthError(w, "unsupported_grant_type", "only authorization_code", http.StatusBadRequest)
		return
	}

	code := r.Form.Get("code")
	redirectURI := r.Form.Get("redirect_uri")
	clientID := r.Form.Get("client_id")
	verifier := r.Form.Get("code_verifier")
	if code == "" || redirectURI == "" || clientID == "" || verifier == "" {
		writeOAuthError(w, "invalid_request", "missing parameters", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ac, ok := s.codes[code]
	if !ok || ac.Used || time.Now().After(ac.Expires) {
		writeOAuthError(w, "invalid_grant", "code invalid or expired", http.StatusBadRequest)
		return
	}
	if ac.ClientID != clientID || ac.RedirectURI != redirectURI {
		writeOAuthError(w, "invalid_grant", "client/redirect mismatch", http.StatusBadRequest)
		return
	}
	if !verifyPKCE(verifier, ac.CodeChallenge, ac.CodeChallengeMethod) {
		writeOAuthError(w, "invalid_grant", "pkce verification failed", http.StatusBadRequest)
		return
	}

	// Optional client secret check for confidential clients.
	if c, ok := s.clients[clientID]; ok && c.Secret != "" {
		if r.Form.Get("client_secret") != c.Secret {
			writeOAuthError(w, "invalid_client", "bad client_secret", http.StatusUnauthorized)
			return
		}
	}

	ac.Used = true
	tok := randomToken(32)
	s.tokens[tok] = &accessToken{
		Token:    tok,
		ClientID: clientID,
		Scope:    ac.Scope,
		Expires:  time.Now().Add(tokenTTL),
		Created:  time.Now(),
	}

	writeJSON(w, map[string]any{
		"access_token": tok,
		"token_type":   "Bearer",
		"expires_in":   int(tokenTTL.Seconds()),
		"scope":        ac.Scope,
	})
}

func (s *Server) handleConsentPage(w http.ResponseWriter, r *http.Request) {
	// Convenience landing page explaining the flow.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>termux-mcp OAuth</title>
<style>body{font-family:system-ui;max-width:40rem;margin:2rem auto;padding:0 1rem;background:#0f1115;color:#e8eaed}
a{color:#8ab4f8}</style></head><body>
<h1>termux-mcp OAuth</h1>
<p>This device is ready for OAuth 2.1 + PKCE clients (Grok, Claude, Composio, …).</p>
<p>Clients discover endpoints via <code>/.well-known/oauth-protected-resource</code>.</p>
<p>Static Bearer tokens from <code>auth.token</code> still work.</p>
</body></html>`)
}

// WWWAuthenticate returns the header value clients use to discover the AS.
func (s *Server) WWWAuthenticate(r *http.Request) string {
	meta := s.baseURL(r) + "/.well-known/oauth-protected-resource"
	return fmt.Sprintf(`Bearer realm="termux-mcp", resource_metadata="%s"`, meta)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, code, desc string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

func randomToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func containsURI(list []string, u string) bool {
	for _, x := range list {
		if x == u {
			return true
		}
	}
	return false
}

func verifyPKCE(verifier, challenge, method string) bool {
	if method == "" || method == "S256" {
		h := sha256.Sum256([]byte(verifier))
		calc := base64.RawURLEncoding.EncodeToString(h[:])
		return calc == challenge
	}
	return false
}

const consentHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Authorize termux-mcp</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;max-width:28rem;margin:3rem auto;padding:0 1.25rem;background:#0f1115;color:#e8eaed}
.card{background:#1a1d24;border-radius:12px;padding:1.5rem;border:1px solid #2a2f3a}
h1{font-size:1.25rem;margin:0 0 .5rem}
p{color:#9aa0a6;line-height:1.5}
.client{color:#8ab4f8;font-weight:600}
.actions{display:flex;gap:.75rem;margin-top:1.5rem}
button{flex:1;padding:.75rem 1rem;border:none;border-radius:8px;font-size:1rem;cursor:pointer;font-weight:600}
.approve{background:#1a73e8;color:#fff}
.deny{background:#3c4043;color:#e8eaed}
</style></head><body>
<div class="card">
<h1>Authorize access</h1>
<p><span class="client">%s</span> wants to use tools on this Android device via termux-mcp.</p>
<p>Scope: <code>mcp</code> — device tools, files (if enabled), shell (if enabled).</p>
<form method="POST" action="/oauth/authorize">
<input type="hidden" name="client_id" value="%s">
<input type="hidden" name="redirect_uri" value="%s">
<input type="hidden" name="state" value="%s">
<input type="hidden" name="code_challenge" value="%s">
<input type="hidden" name="code_challenge_method" value="%s">
<input type="hidden" name="scope" value="%s">
<div class="actions">
<button class="deny" type="submit" name="decision" value="deny">Deny</button>
<button class="approve" type="submit" name="decision" value="approve">Approve</button>
</div>
</form>
</div>
</body></html>

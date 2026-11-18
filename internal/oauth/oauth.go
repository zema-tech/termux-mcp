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
		http.Error(w, `{"error":"server_error","error_description":"client registry full"}`, http.StatusServiceUnavailable)
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

	name := c.Name
	if name == "" {
		name = clientID
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, consentHTML,
		html.EscapeString(name),
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

	if c, ok := s.clients[clientID]; ok && c.Secret != "" {
		if subtle.ConstantTimeCompare([]byte(r.Form.Get("client_secret")), []byte(c.Secret)) != 1 {
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, landingHTML)
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
	if _, err := rand.Read(b); err != nil {
		return ""
	}
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

// consentHTML is the authorization consent page shown on GET /oauth/authorize.
// Args: clientName (title), clientName (body), client_id, redirect_uri, state,
// code_challenge, code_challenge_method, scope.
const consentHTML = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="dark">
<title>Authorize · termux-mcp</title>
<style>
:root{
  --bg:#0b0d12;
  --card:#141820;
  --card-border:#242a36;
  --text:#eef0f4;
  --muted:#9aa3b2;
  --accent:#4c8dff;
  --accent-2:#6ee7b7;
  --danger:#3a3f4a;
  --radius:18px;
}
*{box-sizing:border-box}
html,body{height:100%}
body{
  margin:0;font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
  background:
    radial-gradient(1200px 600px at 10% -10%, rgba(76,141,255,.18), transparent 55%),
    radial-gradient(900px 500px at 100% 0%, rgba(110,231,183,.10), transparent 50%),
    var(--bg);
  color:var(--text);
  display:flex;align-items:center;justify-content:center;
  padding:max(1.25rem, env(safe-area-inset-top)) 1.25rem max(1.25rem, env(safe-area-inset-bottom));
  min-height:100%;
}
.shell{width:100%;max-width:26rem}
.brand{
  display:flex;align-items:center;gap:.65rem;justify-content:center;
  margin-bottom:1.25rem;color:var(--muted);font-size:.85rem;letter-spacing:.02em
}
.logo{
  width:2rem;height:2rem;border-radius:10px;
  background:linear-gradient(145deg,#4c8dff,#6ee7b7);
  display:grid;place-items:center;color:#0b0d12;font-weight:800;font-size:1rem;
  box-shadow:0 8px 24px rgba(76,141,255,.25)
}
.card{
  background:linear-gradient(180deg, rgba(255,255,255,.03), transparent 40%), var(--card);
  border:1px solid var(--card-border);
  border-radius:var(--radius);
  padding:1.5rem 1.4rem 1.35rem;
  box-shadow:0 20px 50px rgba(0,0,0,.45), inset 0 1px 0 rgba(255,255,255,.04)
}
.badge{
  display:inline-flex;align-items:center;gap:.35rem;
  font-size:.72rem;font-weight:600;letter-spacing:.04em;text-transform:uppercase;
  color:var(--accent-2);background:rgba(110,231,183,.1);
  border:1px solid rgba(110,231,183,.25);border-radius:999px;
  padding:.28rem .65rem;margin-bottom:1rem
}
h1{font-size:1.35rem;line-height:1.25;margin:0 0 .75rem;font-weight:700;letter-spacing:-.02em}
.lead{margin:0 0 1.15rem;color:var(--muted);line-height:1.55;font-size:.95rem}
.client{
  color:var(--text);font-weight:650;
  background:linear-gradient(90deg,#8ab4f8,#6ee7b7);
  -webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent
}
.scopes{
  list-style:none;margin:0 0 1.35rem;padding:0;
  display:grid;gap:.55rem
}
.scopes li{
  display:flex;align-items:flex-start;gap:.7rem;
  background:rgba(255,255,255,.03);border:1px solid var(--card-border);
  border-radius:12px;padding:.7rem .8rem;font-size:.88rem;line-height:1.4
}
.scopes .ico{
  flex:0 0 auto;width:1.55rem;height:1.55rem;border-radius:8px;
  display:grid;place-items:center;font-size:.85rem;
  background:rgba(76,141,255,.12);color:var(--accent)
}
.scopes strong{display:block;font-weight:600;color:var(--text);margin-bottom:.1rem}
.scopes span{color:var(--muted);font-size:.8rem}
.note{
  margin:0 0 1.25rem;font-size:.78rem;color:var(--muted);line-height:1.45;
  padding:.65rem .75rem;border-radius:10px;background:rgba(255,255,255,.025);
  border:1px dashed var(--card-border)
}
.actions{display:flex;gap:.65rem}
button{
  flex:1;padding:.85rem 1rem;border:none;border-radius:12px;
  font-size:.95rem;font-weight:650;cursor:pointer;
  transition:transform .12s ease, filter .12s ease, background .12s ease;
  -webkit-tap-highlight-color:transparent
}
button:active{transform:scale(.98)}
.approve{
  background:linear-gradient(180deg,#5b97ff,#3b7aef);color:#fff;
  box-shadow:0 8px 20px rgba(59,122,239,.35)
}
.approve:hover{filter:brightness(1.05)}
.deny{background:var(--danger);color:var(--text);border:1px solid #4a5160}
.deny:hover{background:#454b57}
.foot{margin-top:1rem;text-align:center;font-size:.72rem;color:#6b7280}
</style>
</head><body>
<div class="shell">
  <div class="brand">
    <div class="logo" aria-hidden="true">⌁</div>
    <span>termux-mcp</span>
  </div>
  <div class="card">
    <div class="badge">OAuth 2.1 · PKCE</div>
    <h1>Authorize access</h1>
    <p class="lead"><span class="client">%s</span> wants to use tools on this Android device through <strong>termux-mcp</strong>.</p>
    <ul class="scopes">
      <li>
        <span class="ico" aria-hidden="true">📱</span>
        <div><strong>Device tools</strong><span>Battery, sensors, location, clipboard, and system info</span></div>
      </li>
      <li>
        <span class="ico" aria-hidden="true">📁</span>
        <div><strong>Files</strong><span>Sandboxed file access — only if enabled in config</span></div>
      </li>
      <li>
        <span class="ico" aria-hidden="true">⌨</span>
        <div><strong>Shell</strong><span>Command execution — only if allowlisted in config</span></div>
      </li>
    </ul>
    <p class="note">Access follows your termux-mcp configuration. Sensitive and dangerous modules stay off unless you enabled them.</p>
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
  <p class="foot">Request from %s · token expires in 24h</p>
</div>
</body></html>
`

const landingHTML = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark">
<title>termux-mcp · OAuth</title>
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
font-family:ui-sans-serif,system-ui,sans-serif;background:#0b0d12;color:#eef0f4;
padding:1.5rem;
background-image:radial-gradient(800px 400px at 20% 0%,rgba(76,141,255,.15),transparent 55%)}
.card{max-width:26rem;background:#141820;border:1px solid #242a36;border-radius:18px;padding:1.5rem;
box-shadow:0 20px 50px rgba(0,0,0,.4)}
h1{font-size:1.25rem;margin:0 0 .75rem}
p{color:#9aa3b2;line-height:1.55;margin:0 0 .75rem}
code{color:#8ab4f8;font-size:.9em}
a{color:#4c8dff}
.logo{width:2.25rem;height:2.25rem;border-radius:10px;margin-bottom:1rem;
background:linear-gradient(145deg,#4c8dff,#6ee7b7);display:grid;place-items:center;
color:#0b0d12;font-weight:800}
</style></head><body>
<div class="card">
<div class="logo" aria-hidden="true">⌁</div>
<h1>termux-mcp OAuth</h1>
<p>This device is ready for OAuth 2.1 + PKCE clients (Grok, Claude, Composio, …).</p>
<p>Clients discover endpoints via <code>/.well-known/oauth-protected-resource</code>.</p>
<p>Static Bearer tokens from <code>auth.token</code> still work alongside OAuth access tokens.</p>
</div>
</body></html>
`

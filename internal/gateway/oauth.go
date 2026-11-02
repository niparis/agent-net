package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// registerOAuth attaches the public OAuth endpoints to the public listener.
func (g *Gateway) registerOAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", g.oauthProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", g.oauthMetadata)
	mux.HandleFunc("POST /oauth/register", g.oauthRegister)
	mux.HandleFunc("GET /oauth/authorize", g.oauthAuthorize)
	mux.HandleFunc("GET /oauth/authorize/status", g.oauthAuthorizeStatus)
	mux.HandleFunc("POST /oauth/token", g.oauthToken)
}
func (g *Gateway) oauthBase() string { return strings.TrimRight(g.opts.PublicURL, "/") }
func oauthHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
func oauthJSONResponse(w http.ResponseWriter, status int, value any) {
	oauthHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func oauthError(w http.ResponseWriter, status int, code string) {
	oauthJSONResponse(w, status, map[string]string{"error": code})
}
func (g *Gateway) oauthProtectedResource(w http.ResponseWriter, _ *http.Request) {
	base := g.oauthBase()
	oauthJSONResponse(w, http.StatusOK, map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}})
}
func (g *Gateway) oauthMetadata(w http.ResponseWriter, _ *http.Request) {
	base := g.oauthBase()
	oauthJSONResponse(w, http.StatusOK, map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "registration_endpoint": base + "/oauth/register", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}})
}
func validOAuthRedirect(raw string) bool {
	if len(raw) > 2048 || strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Hostname() == "" || u.Host == "" || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
func validOAuthClientName(name string) bool {
	if len(name) > 256 {
		return false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
func validOAuthList(v []string, allowed map[string]bool) bool {
	seen := map[string]bool{}
	for _, s := range v {
		if !allowed[s] || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func (g *Gateway) oauthRegister(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		ClientName    string   `json:"client_name"`
		RedirectURIs  []string `json:"redirect_uris"`
		GrantTypes    []string `json:"grant_types"`
		ResponseTypes []string `json:"response_types"`
		AuthMethod    string   `json:"token_endpoint_auth_method"`
	}
	dec := json.NewDecoder(r.Body)
	if err = dec.Decode(&in); err != nil {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	if len(in.RedirectURIs) < 1 || len(in.RedirectURIs) > 5 || !validOAuthClientName(in.ClientName) {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	for _, uri := range in.RedirectURIs {
		if !validOAuthRedirect(uri) {
			oauthError(w, 400, "invalid_redirect_uri")
			return
		}
	}
	if in.GrantTypes == nil {
		in.GrantTypes = []string{"authorization_code"}
	}
	if in.ResponseTypes == nil {
		in.ResponseTypes = []string{"code"}
	}
	if in.AuthMethod == "" {
		in.AuthMethod = "none"
	}
	if !validOAuthList(in.GrantTypes, map[string]bool{"authorization_code": true, "refresh_token": true}) || !oauthContains(in.GrantTypes, "authorization_code") || !validOAuthList(in.ResponseTypes, map[string]bool{"code": true}) || len(in.ResponseTypes) != 1 || in.AuthMethod != "none" {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	client, err := g.store.registerOAuthClient(oauthClient{ClientName: in.ClientName, RedirectURIs: in.RedirectURIs, GrantTypes: in.GrantTypes, ResponseTypes: in.ResponseTypes, TokenEndpointAuthMethod: in.AuthMethod}, g.store.now())
	if errors.Is(err, errOAuthLimit) {
		oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable")
		return
	}
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	g.logLine("oauth_register", "client_id", client.ClientID, "client_name", client.ClientName, "redirect_uris", strings.Join(client.RedirectURIs, ","), "source", r.RemoteAddr)
	oauthJSONResponse(w, http.StatusCreated, client)
}
func oauthPKCE(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func validOAuthVerifier(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}
func validOAuthChallenge(s string) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}
func oauthSingle(v url.Values, keys ...string) bool {
	for _, k := range keys {
		if len(v[k]) > 1 {
			return false
		}
	}
	return true
}

var oauthPageTemplate = template.Must(template.New("oauth").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connect an app</title>
<style>body{font:16px system-ui,sans-serif;color:#20242b;background:#f4f6f7;margin:0;padding:4rem 1rem}main{max-width:32rem;margin:auto;padding:2rem;background:white;border-radius:12px}strong{display:block;font-size:2.5rem;letter-spacing:.1em;margin:1.5rem 0}#message{color:#46515a}</style></head>
<body><main><h1>Connect {{.ClientName}}</h1>{{if .Approved}}<p>Approved. Continuing sign-in.</p>{{else}}<p>Enter this code in your agent-net TUI: S, Connected apps, Add</p><strong id="user-code">{{.Code}}</strong>{{end}}<p id="message">Waiting for approval...</p></main>
<script>const statusURL = "/oauth/authorize/status?id={{.ID}}";
async function poll(){try{const response=await fetch(statusURL,{cache:"no-store"});if(!response.ok)throw new Error();const result=await response.json();if(result.status==="approved"&&result.redirect){window.location.assign(result.redirect);return}if(result.status==="refused"){document.getElementById("message").textContent="Refused";return}if(result.status==="expired"){document.getElementById("message").textContent="Expired, try again";return}}catch(e){}setTimeout(poll,2000)}poll();</script></body></html>`))

func (g *Gateway) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !oauthSingle(q, "response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "state", "resource", "scope") || q.Get("response_type") != "code" || q.Get("client_id") == "" || q.Get("redirect_uri") == "" || q.Get("code_challenge_method") != "S256" || !validOAuthChallenge(q.Get("code_challenge")) || len(r.URL.RawQuery) > 8192 {
		http.Error(w, "Invalid authorization request", http.StatusBadRequest)
		return
	}
	p, err := g.store.newOAuthPending(q.Get("client_id"), q.Get("redirect_uri"), q.Get("code_challenge"), q.Get("state"), q.Get("resource"), g.store.now())
	if errors.Is(err, errOAuthLimit) {
		http.Error(w, "Too many pending requests. Try again later.", http.StatusTooManyRequests)
		return
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, errOAuthInvalidGrant) {
		http.Error(w, "Invalid authorization request", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "Could not start sign-in", http.StatusInternalServerError)
		return
	}
	g.logLine("oauth_authorize", "client_id", p.ClientID, "redirect_uri", p.RedirectURI, "resource", p.Resource, "scope", q.Get("scope"), "source", r.RemoteAddr)
	oauthHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	_ = oauthPageTemplate.Execute(w, struct {
		ID, ClientName, Code string
		Approved             bool
	}{p.ID, p.ClientName, p.UserCode[:3] + "-" + p.UserCode[3:], p.Status == "approved"})
}
func (g *Gateway) oauthAuthorizeStatus(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !oauthSingle(q, "id") || q.Get("id") == "" {
		oauthError(w, 400, "invalid_request")
		return
	}
	status, redirect, err := g.store.oauthStatus(q.Get("id"), g.store.now())
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	result := map[string]string{"status": status}
	if redirect != "" {
		result["redirect"] = redirect
	}
	oauthJSONResponse(w, 200, result)
}
func (g *Gateway) oauthToken(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		oauthError(w, 400, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err = r.ParseForm(); err != nil || len(r.URL.RawQuery) > 0 || !oauthSingle(r.PostForm, "grant_type", "code", "redirect_uri", "client_id", "code_verifier", "refresh_token") {
		oauthError(w, 400, "invalid_request")
		return
	}
	form := r.PostForm
	clientID := form.Get("client_id")
	grant := form.Get("grant_type")
	if clientID == "" {
		oauthError(w, 400, "invalid_request")
		return
	}
	var access, refresh string
	switch grant {
	case "authorization_code":
		if form.Get("code") == "" || form.Get("redirect_uri") == "" || form.Get("code_verifier") == "" {
			oauthError(w, 400, "invalid_request")
			return
		}
		access, refresh, err = g.store.exchangeOAuthCode(form.Get("code"), clientID, form.Get("redirect_uri"), form.Get("code_verifier"), g.store.now())
	case "refresh_token":
		if form.Get("refresh_token") == "" {
			oauthError(w, 400, "invalid_request")
			return
		}
		access, refresh, err = g.store.refreshOAuth(form.Get("refresh_token"), clientID, g.store.now())
	default:
		oauthError(w, 400, "unsupported_grant_type")
		return
	}
	g.logLine("oauth_token", "client_id", clientID, "grant_type", grant, "source", r.RemoteAddr, "success", err == nil)
	if errors.Is(err, errOAuthInvalidGrant) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	response := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600}
	if refresh != "" {
		response["refresh_token"] = refresh
	}
	oauthJSONResponse(w, 200, response)
}

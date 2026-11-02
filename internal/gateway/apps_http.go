package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/phone"
	"github.com/niparis/agent-net/internal/ratelimit"
)

type appLogKey struct{}
type appLogInfo struct{ id string }

// safeAppLog prevents public input from filling or forging audit records.
func safeAppLog(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		if b.Len()+len(string(r)) > 64 {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

type appSessionPool struct {
	mu      sync.Mutex
	servers []*mcp.Server
	owners  map[string]string
}

var appSessionPools = struct {
	sync.Mutex
	byStore map[*Store]*appSessionPool
}{byStore: make(map[*Store]*appSessionPool)}

func sessionPool(store *Store, server *mcp.Server) *appSessionPool {
	appSessionPools.Lock()
	defer appSessionPools.Unlock()
	pool := appSessionPools.byStore[store]
	if pool == nil {
		pool = &appSessionPool{owners: make(map[string]string)}
		appSessionPools.byStore[store] = pool
	}
	pool.mu.Lock()
	pool.servers = append(pool.servers, server)
	pool.mu.Unlock()
	return pool
}
func (p *appSessionPool) live() map[string]*mcp.ServerSession {
	live := make(map[string]*mcp.ServerSession)
	for _, server := range p.servers {
		for sess := range server.Sessions() {
			live[sess.ID()] = sess
		}
	}
	for id := range p.owners {
		if live[id] == nil {
			delete(p.owners, id)
		}
	}
	return live
}
func closeAppSessions(store *Store, appID string) {
	appSessionPools.Lock()
	pool := appSessionPools.byStore[store]
	appSessionPools.Unlock()
	if pool == nil {
		return
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	live := pool.live()
	for id, owner := range pool.owners {
		if owner == appID {
			if session := live[id]; session != nil {
				_ = session.Close()
			}
			delete(pool.owners, id)
		}
	}
}
func closeAllAppSessions(store *Store) {
	appSessionPools.Lock()
	pool := appSessionPools.byStore[store]
	delete(appSessionPools.byStore, store)
	appSessionPools.Unlock()
	if pool == nil {
		return
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for _, s := range pool.live() {
		_ = s.Close()
	}
}

// validPublicHost accepts the configured host and, if supplied, its port.
func validPublicHost(r *http.Request, base string) bool {
	public, err := url.Parse(base)
	if err != nil || public.Hostname() == "" {
		return false
	}
	host := r.Host
	port := ""
	if h, p, e := net.SplitHostPort(host); e == nil {
		host, port = h, p
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]")
	} else if strings.Contains(host, ":") {
		return false
	}
	if !strings.EqualFold(host, public.Hostname()) {
		return false
	}
	expected := public.Port()
	if expected == "" {
		if public.Scheme == "https" {
			expected = "443"
		} else if public.Scheme == "http" {
			expected = "80"
		}
	}
	if port != "" && port != expected {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !strings.EqualFold(u.Scheme, public.Scheme) || !strings.EqualFold(u.Host, public.Host) {
			return false
		}
	}
	return true
}

// appsAuthenticate checks syntax before the token lookup, and binds each MCP
// session to the app ID using the SDK's session user check.
func appsAuthenticate(base string, lookup func(string, time.Time) (App, error), now func() time.Time, fail func(*http.Request), next http.Handler) http.Handler {
	metadata := protectedResourceMetadataURL(base)
	verified := auth.RequireBearerToken(func(_ context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
		app, err := lookup(token, now())
		if errors.Is(err, sql.ErrNoRows) {
			if fail != nil {
				fail(r)
			}
			return nil, auth.ErrInvalidToken
		}
		if err != nil {
			return nil, err
		}
		if info, ok := r.Context().Value(appLogKey{}).(*appLogInfo); ok {
			info.id = app.ID
		}
		return &auth.TokenInfo{UserID: app.ID, Extra: map[string]any{"app": app}}, nil
	}, &auth.RequireBearerTokenOptions{ResourceMetadataURL: metadata, AllowMissingExpiration: true})(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields := strings.Fields(r.Header.Get("Authorization"))
		valid := len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") && len(fields[1]) == 43
		if valid {
			decoded, err := base64.RawURLEncoding.Strict().DecodeString(fields[1])
			valid = err == nil && len(decoded) == 32
		}
		if !valid {
			if fail != nil {
				fail(r)
			}
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+metadata+`"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		verified.ServeHTTP(w, r)
	})
}

// AppsHandler serves only public app routes, separate from the gateway API.
// Pass the phone handler backed by the gateway host daemon; without one the
// phone shows a disconnected view and cannot perform actions.
func (g *Gateway) AppsHandler(phoneApps ...http.Handler) http.Handler {
	phoneApp := phone.New(nil)
	if len(phoneApps) > 0 && phoneApps[0] != nil {
		phoneApp = phoneApps[0]
	}
	mux := http.NewServeMux()
	server := g.botMCPServer()
	pool := sessionPool(g.store, server)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{SessionTimeout: 10 * time.Minute, DisableLocalhostProtection: true})
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appsAuthenticate(g.publicBase(), func(raw string, now time.Time) (App, error) {
				return g.store.AppForToken(raw, "access", now)
			}, g.store.now, func(r *http.Request) { g.limiter.Fail(ratelimit.ClientAddr(r)) }, next).ServeHTTP(w, r)
		})
	}
	h := authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app := auth.TokenInfoFromContext(r.Context()).Extra["app"].(App)
		if app.Kind != "bot" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Authentication precedes reading the body, including an invalid token lookup.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "bad request", http.StatusBadRequest)
			}
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if _, err := g.store.touchBot(app); err != nil {
			http.Error(w, "cannot establish bot session", http.StatusInternalServerError)
			return
		}
		// Only session creation needs a reservation; hold it until the SDK has
		// returned the new session ID, so concurrent initializations cannot exceed it.
		if r.Method == http.MethodPost && r.Header.Get("Mcp-Session-Id") == "" {
			if ok, _ := g.limiter.AllowApp(app.ID, "call"); !ok {
				http.Error(w, "app call limit reached", http.StatusTooManyRequests)
				return
			}
			pool.mu.Lock()
			defer pool.mu.Unlock()
			pool.live()
			count := 0
			for _, owner := range pool.owners {
				if owner == app.ID {
					count++
				}
			}
			if count >= 4 || len(pool.owners) >= 200 {
				http.Error(w, "MCP session limit reached", http.StatusTooManyRequests)
				return
			}
			// A revocation may have committed while this request waited for the pool.
			if _, err := g.store.AppForToken(strings.Fields(r.Header.Get("Authorization"))[1], "access", g.store.now()); err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			stream.ServeHTTP(w, r)
			if id := w.Header().Get("Mcp-Session-Id"); id != "" {
				pool.owners[id] = app.ID
			}
			return
		}
		stream.ServeHTTP(w, r)
	}))
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := &appLogInfo{id: "unknown"}
		r = r.WithContext(context.WithValue(r.Context(), appLogKey{}, info))
		defer func() {
			source := ratelimit.ClientAddr(r)
			g.logLine("apps.mcp", "app", safeAppLog(info.id), "method", safeAppLog(r.Method), "session", safeAppLog(r.Header.Get("Mcp-Session-Id")), "source", safeAppLog(source), "user_agent", safeAppLog(r.UserAgent()))
		}()
		h.ServeHTTP(w, r)
	}))
	g.registerOAuth(mux)
	mux.HandleFunc("GET /phone/status", g.phoneStatus)
	mux.Handle("/", g.phoneAuth(phoneApp))
	limited := g.appsMiddleware(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validPublicHost(r, g.publicBase()) {
			http.Error(w, "invalid host or origin", http.StatusMisdirectedRequest)
			return
		}
		limited.ServeHTTP(w, r)
	})
}

// appsMiddleware applies one class to every public request, including 404s.
// MCP uses mcp, browser authorize/status use page, and all other paths use oauth.
func (g *Gateway) appsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		class := "oauth"
		if r.URL.Path == "/mcp" {
			class = "mcp"
		} else if r.URL.Path == "/oauth/authorize" || r.URL.Path == "/oauth/authorize/status" || r.URL.Path == "/phone/status" || r.URL.Path == "/" || r.URL.Path == "/events" || strings.HasPrefix(r.URL.Path, "/session/") {
			class = "page"
		}
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/oauth/") {
				r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
			}
			next.ServeHTTP(w, r)
		})
		ratelimit.Middleware(g.limiter, class, h).ServeHTTP(w, r)
	})
}

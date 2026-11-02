package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/phone"
	"github.com/niparis/agent-net/internal/ratelimit"
)

type appLogKey struct{}
type appLogInfo struct{ id string }

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
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	h := appsAuthenticate(g.opts.PublicURL, func(raw string, now time.Time) (App, error) {
		return g.store.AppForToken(raw, "access", now)
	}, g.store.now, func(r *http.Request) { g.limiter.Fail(ratelimit.ClientAddr(r)) }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app := auth.TokenInfoFromContext(r.Context()).Extra["app"].(App)
		if app.Kind != "bot" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if _, err := g.store.touchBot(app); err != nil {
			http.Error(w, "cannot establish bot session", http.StatusInternalServerError)
			return
		}
		stream.ServeHTTP(w, r)
	}))
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := &appLogInfo{id: "unknown"}
		r = r.WithContext(context.WithValue(r.Context(), appLogKey{}, info))
		defer func() {
			source := ratelimit.ClientAddr(r)
			g.logLine("apps.mcp", "app", info.id, "method", r.Method, "session", r.Header.Get("Mcp-Session-Id"), "source", source)
		}()
		h.ServeHTTP(w, r)
	}))
	g.registerOAuth(mux)
	mux.HandleFunc("GET /phone/status", g.phoneStatus)
	mux.Handle("/", g.phoneAuth(phoneApp))
	return g.appsMiddleware(mux)
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
			if r.URL.Path == "/mcp" {
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
			} else if strings.HasPrefix(r.URL.Path, "/oauth/") {
				r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
			}
			next.ServeHTTP(w, r)
		})
		ratelimit.Middleware(g.limiter, class, h).ServeHTTP(w, r)
	})
}

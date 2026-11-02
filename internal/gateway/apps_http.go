package gateway

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type appLogKey struct{}
type appLogInfo struct{ id string }

// appsAuthenticate checks syntax before the token lookup, and binds each MCP
// session to the app ID using the SDK's session user check.
func appsAuthenticate(base string, lookup func(string, time.Time) (App, error), now func() time.Time, next http.Handler) http.Handler {
	metadata := protectedResourceMetadataURL(base)
	verified := auth.RequireBearerToken(func(_ context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
		app, err := lookup(token, now())
		if errors.Is(err, sql.ErrNoRows) {
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
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+metadata+`"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		verified.ServeHTTP(w, r)
	})
}

// AppsHandler serves only the public app routes, separate from the gateway API.
func (g *Gateway) AppsHandler() http.Handler {
	mux := http.NewServeMux()
	server := g.botMCPServer()
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	h := appsAuthenticate(g.opts.PublicURL, func(raw string, now time.Time) (App, error) {
		return g.store.AppForToken(raw, "access", now)
	}, g.store.now, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			source := r.Header.Get("X-Forwarded-For")
			if source == "" {
				source = r.RemoteAddr
			}
			g.logLine("apps.mcp", "app", info.id, "method", r.Method, "session", r.Header.Get("Mcp-Session-Id"), "source", source)
		}()
		h.ServeHTTP(w, r)
	}))
	g.registerOAuth(mux)
	return mux
}

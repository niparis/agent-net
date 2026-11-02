package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/funnel"
	"github.com/niparis/agent-net/internal/protocol"
)

// Public access is serialized separately from node connections and HTTP reads.
func (g *Gateway) publicBase() string {
	g.publicURLMu.RLock()
	defer g.publicURLMu.RUnlock()
	if g.publicURL != "" {
		return g.publicURL
	}
	return g.opts.PublicURL
}
func (g *Gateway) setPublicBase(base string) {
	g.publicURLMu.Lock()
	g.publicURL = base
	g.publicURLMu.Unlock()
}
func (g *Gateway) publicRunner() funnel.Runner {
	if g.opts.FunnelRunner != nil {
		return g.opts.FunnelRunner
	}
	return funnel.ExecRunner{}
}
func (g *Gateway) savePublic() error {
	raw, err := json.Marshal(g.public)
	if err != nil {
		return err
	}
	_, err = g.store.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('public',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, string(raw), g.store.now().UnixNano())
	return err
}
func (g *Gateway) restorePublic() error {
	var raw string
	err := g.store.db.QueryRow(`SELECT value FROM owner_settings WHERE key='public'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &g.public); err != nil {
			return err
		}
	}
	g.setPublicBase(g.public.URL)
	if g.public.On {
		if err := g.startPublicListener(); err != nil {
			g.public.Error = err.Error()
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := g.enablePublic(ctx, "", true); err != nil {
			g.public.Error = err.Error()
		}
	} else if g.opts.AppsListen != "" {
		return g.startPublicListener()
	}
	return nil
}
func (g *Gateway) startPublicListener() error {
	if g.publicServer != nil {
		return nil
	}
	addr := g.opts.AppsListen
	if addr == "" {
		addr = "127.0.0.1:7422"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return fmt.Errorf("apps listener must bind to 127.0.0.1:port")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	var phoneApp []http.Handler
	if g.opts.PhoneApp != nil {
		phoneApp = append(phoneApp, g.opts.PhoneApp)
	}
	srv := newAppsServer(g.AppsHandler(phoneApp...), appsReadTimeout)
	g.publicServer = srv
	g.publicAddr = ln.Addr().String()
	go func() { _ = srv.Serve(ln) }()
	return nil
}
func (g *Gateway) stopPublicListener() {
	if g.publicServer != nil {
		_ = g.publicServer.Close()
		g.publicServer = nil
	}
}
func (g *Gateway) enablePublic(ctx context.Context, pasted string, restore bool) error {
	if g.public.On && g.publicServer != nil && pasted == "" && !restore && g.public.Error == "" {
		return nil
	}
	port := 0
	base := pasted
	if pasted == "" && !(g.public.Port == 0 && g.public.URL != "") {
		info, err := funnel.Status(ctx, g.publicRunner())
		if err != nil {
			return err
		}
		if (restore || g.public.On) && g.public.Port != 0 {
			port = g.public.Port
		} else {
			port, err = funnel.ChoosePort(info)
			if err != nil {
				return err
			}
		}
		base = funnel.URL(info, port)
	} else if pasted == "" {
		base = g.public.URL
	}
	// Publish the address before serving requests, including authentication metadata.
	old := g.publicBase()
	g.setPublicBase(base)
	if err := g.startPublicListener(); err != nil {
		g.setPublicBase(old)
		return err
	}
	if port != 0 {
		target := g.publicAddr
		if err := funnel.On(ctx, g.publicRunner(), port, "http://"+target); err != nil {
			g.stopPublicListener()
			g.setPublicBase(old)
			return err
		}
	}
	g.public = protocol.AppsPublicStatus{On: true, URL: base, Port: port}
	return g.savePublic()
}
func (g *Gateway) disablePublic(ctx context.Context) error {
	if g.public.Port != 0 {
		if err := funnel.Off(ctx, g.publicRunner(), g.public.Port); err != nil {
			return err
		}
	}
	g.stopPublicListener()
	g.public.On = false
	g.public.Error = ""
	return g.savePublic()
}
func (g *Gateway) publicOperation(ctx context.Context, op string, raw []byte) (any, error) {
	g.publicMu.Lock()
	defer g.publicMu.Unlock()
	var err error
	switch op {
	case "apps.public.on":
		err = g.enablePublic(ctx, "", false)
	case "apps.public.off":
		err = g.disablePublic(ctx)
	case "apps.public.url":
		var p struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad public URL")
		}
		u, e := url.Parse(strings.TrimSpace(p.URL))
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, reject("public URL must be an HTTPS origin")
		}
		if g.public.On && g.public.Port != 0 {
			if err = g.disablePublic(ctx); err != nil {
				break
			}
		}
		err = g.enablePublic(ctx, strings.TrimRight(u.String(), "/"), false)
	}
	if err != nil {
		g.public.Error = err.Error()
	} else if op != "apps.public.status" {
		g.public.Error = ""
	}
	return g.public, err
}
func (g *Gateway) revokeApp(ctx context.Context, id string) error {
	g.publicMu.Lock()
	defer g.publicMu.Unlock()
	if err := g.store.RevokeApp(id); err != nil {
		return err
	}
	if !g.public.On {
		return nil
	}
	apps, err := g.store.ListApps()
	if err != nil {
		return err
	}
	for _, app := range apps {
		if app.RevokedAt == nil {
			return nil
		}
	}
	if err := g.disablePublic(ctx); err != nil {
		g.public.Error = err.Error()
		return err
	}
	return nil
}

// appsReadTimeout bounds reading a whole request, body included, so a client
// that sends headers and then stalls cannot hold a connection.
const appsReadTimeout = 30 * time.Second

// appsMaxInFlight caps concurrent requests on the public listener; beyond it
// requests get 503 at once instead of queueing.
const appsMaxInFlight = 64

// newAppsServer is the public listener's HTTP server: header and body read
// deadlines, an idle timeout and a cap on requests in flight.
func newAppsServer(h http.Handler, readTimeout time.Duration) *http.Server {
	slots := make(chan struct{}, appsMaxInFlight)
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			h.ServeHTTP(w, r)
		default:
			http.Error(w, "too many requests", http.StatusServiceUnavailable)
		}
	})
	return &http.Server{Handler: limited, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: readTimeout, IdleTimeout: 60 * time.Second}
}

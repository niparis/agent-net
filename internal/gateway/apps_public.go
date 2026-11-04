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
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/funnel"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tsnode"
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
	g.loadTailscaleChoice()
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
	if g.public.Node != "" && (g.tsChoice == nil || g.tsChoice.Name != g.public.Node) {
		// The owner changed the setup choice: the old node URL is gone and
		// must not pass for a pasted tunnel URL.
		g.public.URL, g.public.Node = "", ""
	}
	g.setPublicBase(g.public.URL)
	if g.public.On {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := g.enablePublic(ctx, "", true); err != nil {
			g.public.Error = g.ownerFunnelError(err).Error()
		}
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
	if g.tsServer != nil {
		// Closing the Funnel listener also removes its Funnel entry.
		_ = g.tsServer.Close()
		g.tsServer = nil
	}
}

// nodeMode reports whether "on" means a Funnel on agent-net's own node. A
// saved pasted tunnel URL (no port, no node) still wins, as it does over the
// machine's Funnel.
func (g *Gateway) nodeMode() bool {
	return g.tsChoice != nil && !(g.public.Port == 0 && g.public.Node == "" && g.public.URL != "")
}
func (g *Gateway) enablePublic(ctx context.Context, pasted string, restore bool) error {
	if g.public.On && g.publicServer != nil && pasted == "" && !restore && g.public.Error == "" {
		return nil
	}
	// Remove a previous managed route before selecting or restoring a port.
	if _, err := funnel.Cleanup(ctx, g.publicRunner(), g.opts.DataDir); err != nil {
		return err
	}
	// The command-line URL is the initial custom tunnel, not an on switch.
	if pasted == "" && g.public.URL == "" && g.opts.PublicURL != "" {
		u, err := url.Parse(strings.TrimSpace(g.opts.PublicURL))
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return reject("public URL must be an HTTPS origin")
		}
		pasted = strings.TrimRight(u.String(), "/")
	}
	if pasted == "" && g.nodeMode() {
		return g.enableNodePublic(ctx)
	}
	port := 0
	base := pasted
	if pasted == "" && !(g.public.Port == 0 && g.public.URL != "") {
		info, err := funnel.Status(ctx, g.publicRunner())
		if err != nil {
			return err
		}
		if restore && g.public.Port != 0 {
			if _, used := info.Ports[g.public.Port]; !used {
				port = g.public.Port
			}
		}
		if port == 0 {
			port, err = funnel.ChoosePort(info)
			if err != nil {
				return err
			}
		}
		base = funnel.URL(info, port)
	} else if pasted == "" {
		base = g.public.URL
	}
	old := g.publicBase()
	g.setPublicBase(base)
	if err := g.startPublicListener(); err != nil {
		g.setPublicBase(old)
		return err
	}
	if port != 0 {
		target := "http://" + g.publicAddr
		if err := funnel.SaveRecord(g.opts.DataDir, funnel.Record{Port: port, Target: target}); err != nil {
			g.stopPublicListener()
			g.setPublicBase(old)
			return err
		}
		if err := funnel.On(ctx, g.publicRunner(), port, target); err != nil {
			g.stopPublicListener()
			g.setPublicBase(old)
			return err
		}
	}
	g.public = protocol.AppsPublicStatus{On: true, URL: base, Port: port}
	return g.savePublic()
}
func (g *Gateway) disablePublic(ctx context.Context) error {
	// Make off durable and close the listener even if Funnel is unavailable.
	g.stopPublicListener()
	g.closeTailscaleNode()
	g.public.On = false
	g.public.Error = ""
	saveErr := g.savePublic()
	_, cleanErr := funnel.Cleanup(ctx, g.publicRunner(), g.opts.DataDir)
	return errors.Join(saveErr, cleanErr)
}

// publicStatus supplements the persisted switch with the live host routes.
type publicStatus struct {
	protocol.AppsPublicStatus
	OtherRoutes []funnel.Route `json:"other_routes"`
}

func (g *Gateway) publicStatus(ctx context.Context) publicStatus {
	if !g.public.On {
		if _, err := funnel.Cleanup(ctx, g.publicRunner(), g.opts.DataDir); err != nil {
			g.public.Error = g.ownerFunnelError(err).Error()
		} else if g.public.Error != "" {
			g.public.Error = ""
			_ = g.savePublic()
		}
	}
	routes := []funnel.Route{}
	if g.tsChoice != nil {
		// Routes on the machine's name do not share agent-net's own name.
		return publicStatus{AppsPublicStatus: g.public, OtherRoutes: routes}
	}
	info, _ := funnel.Status(ctx, g.publicRunner())
	record, err := funnel.ReadRecord(g.opts.DataDir)
	for _, route := range info.Routes {
		if err == nil && route.Port == record.Port && route.Path == "/" && route.Target == record.Target {
			continue
		}
		routes = append(routes, route)
	}
	return publicStatus{AppsPublicStatus: g.public, OtherRoutes: routes}
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
		if g.public.On && (g.public.Port != 0 || g.public.Node != "") {
			if err = g.disablePublic(ctx); err != nil {
				break
			}
		}
		err = g.enablePublic(ctx, strings.TrimRight(u.String(), "/"), false)
	}
	if err != nil {
		err = g.ownerFunnelError(err)
		g.public.Error = err.Error()
	} else if op != "apps.public.status" {
		g.public.Error = ""
	}
	if op == "apps.public.status" {
		return g.publicStatus(ctx), err
	}
	return g.public, err
}

type ownerFacingError struct {
	message string
	cause   error
}

func (e ownerFacingError) Error() string { return e.message }
func (e ownerFacingError) Unwrap() error { return e.cause }

// ownerFunnelError turns errors from the gateway's local Tailscale install
// into instructions that identify which machine the owner needs to use.
func (g *Gateway) ownerFunnelError(err error) error {
	if err == nil {
		return nil
	}
	machine := g.gatewayMachineName()
	var login *tsnode.NeedsLoginError
	switch {
	case errors.As(err, &login):
		return ownerFacingError{message: "Add agent-net's own Tailscale node to your tailnet: open " + login.URL + " and log in, then press p again.", cause: err}
	case errors.Is(err, funnel.ErrNeedsOperator):
		return ownerFacingError{message: fmt.Sprintf("On %s (the gateway machine, where internet access runs), run once in a terminal: sudo tailscale set --operator=$USER. Then press p again.", machine), cause: err}
	case errors.Is(err, funnel.ErrNotInstalled):
		return ownerFacingError{message: fmt.Sprintf("Tailscale is not installed on %s (the gateway machine). Install it there, or use your own tunnel URL (u).", machine), cause: err}
	case errors.Is(err, funnel.ErrNotRunning):
		return ownerFacingError{message: fmt.Sprintf("Tailscale is not running on %s (the gateway machine). Start it there, then press p again.", machine), cause: err}
	case errors.Is(err, funnel.ErrFunnelNotAllowed):
		var denied *funnel.FunnelNotAllowedError
		link := "https://tailscale.com/s/no-funnel"
		if errors.As(err, &denied) {
			link = denied.Link
		}
		return ownerFacingError{message: "Allow Funnel for your tailnet: open " + link + ", then press p again.", cause: err}
	}
	return err
}

// loadTailscaleChoice reads the owner's setup choice. Without one, it removes
// a node that agent-net created earlier, by its ownership marker.
func (g *Gateway) loadTailscaleChoice() {
	choice, err := tsnode.ReadChoice(g.opts.DataDir)
	switch {
	case err == nil:
		g.tsChoice = &choice
		g.logLine("apps.tailscale_node", "name", choice.Name)
	case errors.Is(err, os.ErrNotExist):
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		removed, err := tsnode.Cleanup(ctx, g.opts.DataDir, g.tailscaleOpener())
		if err != nil {
			g.logLine("apps.tailscale_node", "cleanup", "failed", "error", err.Error())
		} else if removed {
			g.logLine("apps.tailscale_node", "cleanup", "removed")
		}
	default:
		g.logLine("apps.tailscale_node", "choice", "unreadable", "error", err.Error())
	}
}

func (g *Gateway) tailscaleOpener() tsnode.Opener {
	if g.opts.TailscaleNode != nil {
		return g.opts.TailscaleNode
	}
	return tsnode.Open
}

// enableNodePublic turns on a Funnel on agent-net's own node. The node stays
// open after a login error, so the login the owner finishes is the one used.
func (g *Gateway) enableNodePublic(ctx context.Context) error {
	if g.tsNode == nil {
		node, err := tsnode.Start(g.opts.DataDir, *g.tsChoice, g.tailscaleOpener())
		if err != nil {
			return err
		}
		g.tsNode = node
	}
	upCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dns, err := g.tsNode.Up(upCtx)
	if err != nil {
		return err
	}
	if g.tsServer != nil {
		_ = g.tsServer.Close()
		g.tsServer = nil
	}
	ln, err := g.tsNode.ListenFunnel(443)
	if err != nil {
		return err
	}
	base := "https://" + strings.TrimSuffix(dns, ".")
	old := g.publicBase()
	g.setPublicBase(base)
	// The loopback listener stays the target for a pasted tunnel URL.
	if err := g.startPublicListener(); err != nil {
		_ = ln.Close()
		g.setPublicBase(old)
		return err
	}
	// Wrapping the loopback server's handler shares its cap on requests in
	// flight between both listeners.
	srv := newAppsServer(g.publicServer.Handler, appsReadTimeout)
	g.tsServer = srv
	go func() { _ = srv.Serve(ln) }()
	g.public = protocol.AppsPublicStatus{On: true, URL: base, Node: g.tsChoice.Name}
	return g.savePublic()
}

func (g *Gateway) closeTailscaleNode() {
	if g.tsNode != nil {
		_ = g.tsNode.Close()
		g.tsNode = nil
	}
}

func (g *Gateway) gatewayMachineName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "the gateway host"
	}
	var name string
	err = g.store.db.QueryRow(`SELECT COALESCE(NULLIF(display_name, ''), NULLIF(reported_hostname, ''), hostname)
		FROM nodes WHERE lower(reported_hostname) = lower(?) OR lower(hostname) = lower(?)
		ORDER BY CASE WHEN lower(reported_hostname) = lower(?) THEN 0 ELSE 1 END LIMIT 1`, host, host, host).Scan(&name)
	if err == nil && strings.TrimSpace(name) != "" {
		return name
	}
	return host
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

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

func openCodeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (d *Daemon) openCodeVersion() string {
	if d != nil {
		installed, _ := d.inventory()
		for _, h := range installed {
			if h.Name == "opencode" && h.Version != "" {
				return h.Version
			}
		}
	}
	return "unknown"
}

func (d *Daemon) discoverOpenCodeRoutes(ctx context.Context, base, password, version, identity string) (opencodeapi.Routes, error) {
	if version == "" {
		version = d.openCodeVersion()
	}
	client := openCodeHTTPClient(10 * time.Second)
	if d == nil {
		return opencodeapi.Discover(ctx, client, base, password, version)
	}
	return d.openCodeRoutes.Discover(ctx, client, base, password, version, identity)
}

func (d *Daemon) openCodePath(ctx context.Context, base, password, version, identity string, op opencodeapi.Operation, id string) (string, error) {
	routes, err := d.discoverOpenCodeRoutes(ctx, base, password, version, identity)
	if err != nil {
		return "", err
	}
	return routes.Path(op, id)
}

func parseOpenCodeInfoVersion(raw []byte) string {
	var info struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return ""
	}
	return info.Version
}

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// compactOpenCodeSession asks the owner's OpenCode service to compact one
// session at its next step boundary.
func (d *Daemon) compactOpenCodeSession(ctx context.Context, base, password, version, identity, session string) error {
	path, err := d.openCodePath(ctx, base, password, version, identity, opencodeapi.Compact, session)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, opencodeapi.Compact.Method(), strings.TrimRight(base, "/")+path, strings.NewReader(`{"delivery":"steer"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if password != "" {
		req.SetBasicAuth("opencode", password)
	}
	resp, err := openCodeHTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("OpenCode compact answered %s", resp.Status)
	}
	return nil
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

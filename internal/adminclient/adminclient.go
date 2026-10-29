// Package adminclient calls the gateway's admin API from the gateway host. It
// finds the gateway through admin.json in the gateway data dir.
package adminclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/gateway"
)

// Client is a connection to the admin API of a running gateway.
type Client struct {
	info gateway.AdminInfo
	hc   *http.Client
}

// Open reads admin.json and ca.pem from dataDir.
func Open(dataDir string) (*Client, error) {
	raw, err := os.ReadFile(gateway.AdminFile(dataDir))
	if err != nil {
		return nil, errors.New("the gateway is not running here (no " + gateway.AdminFile(dataDir) + "); run this on the gateway machine")
	}
	var info gateway.AdminInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(filepath.Join(dataDir, "ca.pem"))
	if err != nil {
		return nil, err
	}
	hc, err := daemon.PinnedClient(ca)
	if err != nil {
		return nil, err
	}
	hc.Timeout = 0 // the caller's context bounds the call
	return &Client{info: info, hc: hc}, nil
}

// Get calls GET path?query and decodes the JSON answer into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	u := c.info.URL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.info.Token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gateway answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

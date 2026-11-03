package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// timeZoneSettings reads the owner's display time zone from the gateway, or
// writes it when change is not nil.
func (d *Daemon) timeZoneSettings(ctx context.Context, change *protocol.TimeZoneSettings) (protocol.TimeZoneSettings, error) {
	c, ok := d.integrationsClient()
	if !ok {
		return protocol.TimeZoneSettings{}, errNotConnected
	}
	method := http.MethodGet
	var body io.Reader
	if change != nil {
		method = http.MethodPut
		raw, err := json.Marshal(change)
		if err != nil {
			return protocol.TimeZoneSettings{}, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/v1/timezone-settings", body)
	if err != nil {
		return protocol.TimeZoneSettings{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return protocol.TimeZoneSettings{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return protocol.TimeZoneSettings{}, fmt.Errorf("gateway answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var out protocol.TimeZoneSettings
	err = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&out)
	return out, err
}

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

func (d *Daemon) ctoSettings(ctx context.Context, in protocol.CTOSettingsRequest) (protocol.CTOSettings, error) {
	c, ok := d.integrationsClient()
	if !ok {
		return protocol.CTOSettings{}, errNotConnected
	}
	method := http.MethodGet
	var body io.Reader
	if in.Model != nil || in.Harness != nil {
		current, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
		if err != nil {
			return protocol.CTOSettings{}, err
		}
		if in.Model != nil {
			current.Model = *in.Model
		}
		if in.Harness != nil {
			current.Harness = *in.Harness
		}
		method = http.MethodPut
		b, _ := json.Marshal(current)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/v1/cto-settings", body)
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return protocol.CTOSettings{}, fmt.Errorf("gateway answered %s", resp.Status)
	}
	var out protocol.CTOSettings
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out)
	if out.Harness == "" {
		out.Harness = "opencode"
	}
	return out, err
}

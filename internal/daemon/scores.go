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

func (d *Daemon) scores(ctx context.Context, change *protocol.ScoreSetRequest) ([]protocol.ModelScore, error) {
	c, ok := d.integrationsClient()
	if !ok {
		return nil, errNotConnected
	}
	method := http.MethodGet
	var body io.Reader
	if change != nil {
		method = http.MethodPut
		raw, err := json.Marshal(change)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/v1/scores", body)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("gateway answered %s", resp.Status)
	}
	var out []protocol.ModelScore
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	return out, err
}

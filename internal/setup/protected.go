package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func proposeProtectedProvider(out io.Writer) {
	fmt.Fprintln(out, "Claude is installed. Workers on the orchestrator's Anthropic subscription spend the limit the orchestrator needs to keep working. Use other subscriptions first; to protect it, run: agent-net workers protect anthropic")
}

func (r *runner) proposeProtectedProviders(ctx context.Context) {
	if claudePath() == "" {
		return
	}
	c, ok := r.integrationsClient()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+"/v1/protected-providers", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var setting protocol.ProtectedProviders
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&setting) != nil || setting.Configured {
		return
	}
	proposeProtectedProvider(r.Out)
}

package setup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
)

// ctoTitleStore reads and saves the network CTO title. Setup uses the gateway
// endpoint; tests replace it.
type ctoTitleStore interface {
	Title(context.Context) (protocol.CTOTitle, error)
	SetTitle(context.Context, string) error
}

// gatewayCTOTitle is the gateway client of the owner setting. It uses the
// node token, like the integrations client.
type gatewayCTOTitle struct{ client integrations.Client }

func (g gatewayCTOTitle) Title(ctx context.Context) (protocol.CTOTitle, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.client.URL, "/")+"/v1/cto-title", nil)
	if err != nil {
		return protocol.CTOTitle{}, err
	}
	req.Header.Set("Authorization", "Bearer "+g.client.Token)
	hc := g.client.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return protocol.CTOTitle{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return protocol.CTOTitle{}, fmt.Errorf("gateway answered %s", resp.Status)
	}
	var out protocol.CTOTitle
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
		return protocol.CTOTitle{}, err
	}
	out.Title = protocol.CTOTitleOrDefault(out.Title)
	return out, nil
}

func (g gatewayCTOTitle) SetTitle(ctx context.Context, title string) error {
	body, err := json.Marshal(protocol.CTOTitle{Title: title})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(g.client.URL, "/")+"/v1/cto-title", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.client.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := g.client.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("gateway answered %s", resp.Status)
	}
	return nil
}

func (r *runner) ctoTitleForSetup() (ctoTitleStore, bool) {
	if r.titleStore != nil {
		return r.titleStore, true
	}
	c, ok := r.integrationsClient()
	if !ok {
		return nil, false
	}
	return gatewayCTOTitle{client: c}, true
}

// askCTOTitle asks for the network CTO name on the first machine only. The
// title is cosmetic and owner-facing, so every other run keeps the default:
// a joining machine, a rerun, an upgrade, and a run without a terminal.
func (r *runner) askCTOTitle(ctx context.Context) {
	if !r.Gateway || r.joining || !r.interactive {
		return
	}
	store, ok := r.ctoTitleForSetup()
	if !ok {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	current, err := store.Title(callCtx)
	if err != nil || current.Set {
		return
	}
	tty, err := openTTY()
	if err != nil {
		return
	}
	defer tty.Close()
	rd := bufio.NewReader(tty)
	title := protocol.DefaultCTOTitle
	for {
		fmt.Fprint(r.Out, "What should your CTO be called? ["+protocol.DefaultCTOTitle+"] ")
		line, _ := rd.ReadString('\n')
		raw := strings.TrimSpace(line)
		if raw == "" {
			title = protocol.DefaultCTOTitle
			break
		}
		normalized, err := protocol.NormalizeCTOTitle(raw)
		if err != nil {
			fmt.Fprintln(r.Out, "Sorry, "+err.Error()+".")
			continue
		}
		title = normalized
		break
	}
	if err := store.SetTitle(callCtx, title); err != nil {
		r.hint("could not save the CTO title for your network: %v", err)
	}
	r.step("cto title", title, nil)
}

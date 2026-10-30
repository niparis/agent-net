// Package integrations holds the setup integrations that the owner answers
// once for the whole network: the answer types, the per-machine local
// overrides, the applied record, and the small client of the gateway's owner
// settings.
package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Answers is the owner's answer for the network.
type Answers = protocol.Integrations

// Item IDs, for callers that do not want to name protocol.
const (
	PeerMessages = protocol.IntegrationPeerMessages
	CodexTools   = protocol.IntegrationCodexTools
	HintHook     = protocol.IntegrationHintHook
	PolicyHook   = protocol.IntegrationPolicyHook
	NeedsHook    = protocol.IntegrationNeedsHook
	WorkersMod   = protocol.IntegrationWorkersMod
	QuotaAXI     = protocol.IntegrationQuotaAXI
)

// Spec is one integration line.
type Spec = protocol.IntegrationSpec

// Specs lists the integrations in question order.
func Specs() []Spec { return protocol.IntegrationSpecs() }

// AllOn turns every integration on.
func AllOn() Answers {
	var a Answers
	for _, s := range Specs() {
		a.Set(s.ID, true)
	}
	return a
}

// AllOff turns every integration off.
func AllOff() Answers { return Answers{} }

// Local is the answers this machine chose for itself, made with the per-item
// setup flags. A local answer wins over the network answer.
type Local struct {
	Items map[string]bool `json:"items,omitempty"`
}

// Get returns the local answer for one item.
func (l Local) Get(id string) (on, ok bool) {
	on, ok = l.Items[id]
	return on, ok
}

// Set records a local answer for one item.
func (l *Local) Set(id string, on bool) {
	if l.Items == nil {
		l.Items = map[string]bool{}
	}
	l.Items[id] = on
}

// LocalPath is where the local overrides of a machine are kept.
func LocalPath(configDir string) string { return filepath.Join(configDir, "integrations-local.json") }

// LoadLocal reads the local overrides. A missing or broken file means none.
func LoadLocal(configDir string) Local {
	var l Local
	b, err := os.ReadFile(LocalPath(configDir))
	if err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

// SaveLocal writes the local overrides.
func SaveLocal(configDir string, l Local) error {
	b, _ := json.MarshalIndent(l, "", "  ")
	return writeFile(LocalPath(configDir), append(b, '\n'))
}

// Effective overlays the local answers on the network answers.
func Effective(net Answers, local Local) Answers {
	out := net
	for id, on := range local.Items {
		out.Set(id, on)
	}
	return out
}

// AppliedPath is where a machine records the answers it last applied, so a
// daemon start does not repeat the work.
func AppliedPath(configDir string) string {
	return filepath.Join(configDir, "integrations-applied.json")
}

// LoadApplied returns the answers this machine last applied.
func LoadApplied(configDir string) (Answers, bool) {
	b, err := os.ReadFile(AppliedPath(configDir))
	if err != nil {
		return Answers{}, false
	}
	var a Answers
	if json.Unmarshal(b, &a) != nil {
		return Answers{}, false
	}
	return a, true
}

// SaveApplied records the answers this machine applied.
func SaveApplied(configDir string, a Answers) error {
	b, _ := json.MarshalIndent(a, "", "  ")
	return writeFile(AppliedPath(configDir), append(b, '\n'))
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Client reads and writes the owner's answers in the gateway. It uses the
// machine's node token, so only this machine's user can reach it.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

func (c Client) do(ctx context.Context, method string, in, out any) error {
	url := strings.TrimRight(c.URL, "/") + "/v1/integrations"
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gateway answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Get reads the network answers. Set is false when the owner has not answered
// yet.
func (c Client) Get(ctx context.Context) (protocol.IntegrationsResult, error) {
	var out protocol.IntegrationsResult
	err := c.do(ctx, "GET", nil, &out)
	return out, err
}

// Set stores the network answers.
func (c Client) Set(ctx context.Context, a Answers) (protocol.IntegrationsResult, error) {
	var out protocol.IntegrationsResult
	err := c.do(ctx, "PUT", a, &out)
	return out, err
}

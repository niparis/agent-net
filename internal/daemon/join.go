package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// NodeConfig is the content of node.json.
type NodeConfig struct {
	GatewayURL string `json:"gateway_url"`
	NodeID     string `json:"node_id"`
	Token      string `json:"token"`
	CASHA256   string `json:"ca_sha256"`
	// Hostname pins the name this machine shows on the network. Edited by hand;
	// without it the name is worked out by Hostname.
	Hostname string `json:"hostname,omitempty"`
	// NoLaunch makes this machine refuse workers (`agent-net setup --no-launch`).
	NoLaunch bool `json:"no_launch,omitempty"`
	// EnterSSH turns on entering a session on another machine through ssh from
	// the TUI (decision 0002, W16). EnterSSHHosts maps a host name to the ssh
	// target to use for it (an alias or user@host). Both are edited by hand.
	EnterSSH      bool              `json:"enter_ssh,omitempty"`
	EnterSSHHosts map[string]string `json:"enter_ssh_hosts,omitempty"`
	// ModelProviders lists the model providers to prefer, first is best, when
	// a loose model name matches models of several providers (W17). For
	// example ["opencode-go", "opencode"]. Edited by hand.
	ModelProviders []string `json:"model_providers,omitempty"`
	// Workers sets the worker limits and timeouts. Absent fields take the
	// defaults of decision 0002.
	Workers *NodeWorkers `json:"workers,omitempty"`
}

// NodeWorkers is the "workers" part of node.json. Durations are Go durations
// such as "60m" or "4h".
type NodeWorkers struct {
	// Max is the hard cap of workers on the machine (default 12).
	Max        int    `json:"max,omitempty"`
	Idle       string `json:"idle,omitempty"`
	MaxAge     string `json:"max_age,omitempty"`
	OwnerGrace string `json:"owner_grace,omitempty"`
	// CPUTarget is the 5-minute CPU busy percent above which no worker starts
	// (default 75). MemReserve is the memory that stays free, such as "2GB"
	// (default: the larger of 1.5GB and 10 percent of RAM). QueueTimeout is how
	// long a start may wait in the queue (default 2h).
	CPUTarget    float64 `json:"cpu_target,omitempty"`
	MemReserve   string  `json:"mem_reserve,omitempty"`
	QueueTimeout string  `json:"queue_timeout,omitempty"`
}

// parseSize reads a size such as "2GB", "1536MB" or "1.5GiB" into bytes.
func parseSize(s string) (uint64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	mult := float64(1)
	for _, u := range []struct {
		suffix string
		mult   float64
	}{{"gib", 1 << 30}, {"gb", 1 << 30}, {"g", 1 << 30}, {"mib", 1 << 20}, {"mb", 1 << 20}, {"m", 1 << 20}} {
		if strings.HasSuffix(t, u.suffix) {
			t, mult = strings.TrimSpace(strings.TrimSuffix(t, u.suffix)), u.mult
			break
		}
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("not a size like 2GB or 1536MB: %q", s)
	}
	return uint64(v * mult), nil
}

// WorkerSettings returns the worker settings of node.json.
func (nc NodeConfig) WorkerSettings() (WorkerSettings, error) {
	var s WorkerSettings
	if nc.Workers == nil {
		return s, nil
	}
	s.Max = nc.Workers.Max
	if nc.Workers.CPUTarget != 0 {
		if nc.Workers.CPUTarget < 0 || nc.Workers.CPUTarget > 100 {
			return s, errors.New("node.json: workers.cpu_target must be a percent between 1 and 100")
		}
		s.CPUTarget = nc.Workers.CPUTarget
	}
	if nc.Workers.MemReserve != "" {
		n, err := parseSize(nc.Workers.MemReserve)
		if err != nil {
			return s, fmt.Errorf("node.json: workers.mem_reserve: %v", err)
		}
		s.MemReserve = n
	}
	for _, f := range []struct {
		name string
		in   string
		out  *time.Duration
	}{{"idle", nc.Workers.Idle, &s.Idle}, {"max_age", nc.Workers.MaxAge, &s.MaxAge}, {"owner_grace", nc.Workers.OwnerGrace, &s.OwnerGrace},
		{"queue_timeout", nc.Workers.QueueTimeout, &s.QueueTimeout}} {
		if f.in == "" {
			continue
		}
		d, err := time.ParseDuration(f.in)
		if err != nil || d <= 0 {
			return s, fmt.Errorf("node.json: workers.%s must be a duration like 60m, not %q", f.name, f.in)
		}
		*f.out = d
	}
	if s.Max < 0 {
		return s, errors.New("node.json: workers.max must not be negative")
	}
	return s, nil
}

// SetNoLaunch writes the no_launch setting into node.json in dir.
func SetNoLaunch(dir string, noLaunch bool) error {
	path := filepath.Join(dir, "node.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var nc NodeConfig
	if err := json.Unmarshal(raw, &nc); err != nil {
		return err
	}
	nc.NoLaunch = noLaunch
	out, _ := json.MarshalIndent(nc, "", "  ")
	return os.WriteFile(path, out, 0o600)
}

// LoadNode reads node.json and ca.pem from dir.
func LoadNode(dir string) (NodeConfig, []byte, error) {
	var nc NodeConfig
	raw, err := os.ReadFile(filepath.Join(dir, "node.json"))
	if err != nil {
		return nc, nil, fmt.Errorf("not joined yet (run `agent-net join`): %w", err)
	}
	if err := json.Unmarshal(raw, &nc); err != nil {
		return nc, nil, err
	}
	ca, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nc, nil, err
	}
	if fp, err := protocol.CertFingerprint(ca); err != nil || fp != nc.CASHA256 {
		return nc, nil, errors.New("ca.pem does not match the fingerprint in node.json")
	}
	return nc, ca, nil
}

// PinnedClient returns an HTTP client that trusts only the given CA.
func PinnedClient(caPEM []byte) (*http.Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("bad CA certificate")
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}, nil
}

// ParseJoinString splits https://host:port#code#ca-sha256.
func ParseJoinString(s string) (url, code, fp string, err error) {
	parts := strings.Split(strings.TrimSpace(s), "#")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", errors.New("join string must look like https://host:7420#CODE#ca-sha256")
	}
	if !strings.HasPrefix(parts[0], "https://") {
		return "", "", "", errors.New("join string must use https://")
	}
	return strings.TrimRight(parts[0], "/"), parts[1], strings.ToLower(parts[2]), nil
}

// Join fetches the gateway CA, checks it against the fingerprint in the join
// string, enrolls this node and writes node.json and ca.pem into dir.
func Join(ctx context.Context, joinString, dir string) (NodeConfig, error) {
	url, code, fp, err := ParseJoinString(joinString)
	if err != nil {
		return NodeConfig{}, err
	}
	// Only the CA certificate is fetched without verification. It is not
	// trusted until its fingerprint matches.
	bootstrap := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/v1/ca", nil)
	resp, err := bootstrap.Do(req)
	if err != nil {
		return NodeConfig{}, fmt.Errorf("fetch CA: %w", err)
	}
	caPEM, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if err != nil {
		return NodeConfig{}, err
	}
	got, err := protocol.CertFingerprint(caPEM)
	if err != nil {
		return NodeConfig{}, fmt.Errorf("gateway sent a bad CA: %w", err)
	}
	if got != fp {
		return NodeConfig{}, fmt.Errorf("CA fingerprint mismatch: join string has %s, gateway presented %s", fp, got)
	}
	client, err := PinnedClient(caPEM)
	if err != nil {
		return NodeConfig{}, err
	}
	// Joining again keeps this machine's own settings, including a host name
	// set by hand.
	var old NodeConfig
	if raw, err := os.ReadFile(filepath.Join(dir, "node.json")); err == nil {
		if json.Unmarshal(raw, &old) != nil {
			old = NodeConfig{}
		}
	}
	host := Hostname(old.Hostname)
	body, _ := json.Marshal(map[string]string{"code": code, "hostname": host, "os": runtime.GOOS})
	req, _ = http.NewRequestWithContext(ctx, "POST", url+"/v1/enroll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		return NodeConfig{}, fmt.Errorf("enroll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return NodeConfig{}, fmt.Errorf("enroll refused: %s", strings.TrimSpace(string(msg)))
	}
	var out struct {
		NodeID string `json:"node_id"`
		Token  string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return NodeConfig{}, err
	}
	nc := NodeConfig{GatewayURL: url, NodeID: out.NodeID, Token: out.Token, CASHA256: fp}
	nc.NoLaunch, nc.Workers, nc.Hostname = old.NoLaunch, old.Workers, old.Hostname
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nc, err
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), caPEM, 0o644); err != nil {
		return nc, err
	}
	raw, _ := json.MarshalIndent(nc, "", "  ")
	return nc, os.WriteFile(filepath.Join(dir, "node.json"), raw, 0o600)
}

package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/upgrade"
)

// AdminInfo is admin.json in the data dir: how the local `agent-net upgrade`
// reaches a running gateway. The file is 0600 and lists the token of the
// current run only.
type AdminInfo struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Dist  string `json:"dist"`
}

// AdminFile is the path of admin.json in a data dir.
func AdminFile(dataDir string) string { return filepath.Join(dataDir, "admin.json") }

// WriteAdminInfo records how to reach this gateway. listen is the address it
// listens on; an unspecified host is replaced by localhost.
func (g *Gateway) WriteAdminInfo(listen string) error {
	host, port := listen, ""
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		host, port = listen[:i], listen[i+1:]
	}
	if host == "" || host == "0.0.0.0" || host == "[::]" || host == "::" {
		host = "localhost"
	}
	info := AdminInfo{URL: "https://" + host + ":" + port, Token: g.adminToken, Dist: g.opts.DistDir}
	raw, _ := json.MarshalIndent(info, "", "  ")
	tmp := AdminFile(g.opts.DataDir) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, AdminFile(g.opts.DataDir))
}

func (g *Gateway) upgradeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /install/version", g.handleVersion)
	mux.HandleFunc("GET /install/checksums.txt", g.handleChecksums)
	mux.HandleFunc("GET /v1/admin/nodes", g.admin(func(w http.ResponseWriter, r *http.Request) {
		info, err := g.NodesInfo()
		writeJSON(w, info, err)
	}))
	mux.HandleFunc("POST /v1/admin/upgrade", g.admin(g.handleAdminUpgrade))
	mux.HandleFunc("POST /v1/admin/restart", g.admin(func(w http.ResponseWriter, r *http.Request) {
		if g.opts.Restart == nil {
			http.Error(w, "this gateway cannot restart itself", http.StatusNotImplemented)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		go func() {
			time.Sleep(300 * time.Millisecond)
			g.opts.Restart()
		}()
	}))
}

func (g *Gateway) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(g.adminToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// NodesInfo lists the nodes with their versions; Online comes from the live
// connections.
func (g *Gateway) NodesInfo() (protocol.NodesInfo, error) {
	nodes, err := g.store.Nodes()
	if err != nil {
		return protocol.NodesInfo{}, err
	}
	g.mu.Lock()
	for i := range nodes {
		_, nodes[i].Online = g.conns[nodes[i].NodeID]
	}
	g.mu.Unlock()
	for i := range nodes {
		tracks, err := g.store.TrackRecords(nodes[i].NodeID, protocol.TrackRecordDays*24*time.Hour)
		if err != nil {
			return protocol.NodesInfo{}, err
		}
		nodes[i].Tracks = tracks
	}
	notes, err := g.store.PlaybookNotes()
	if err != nil {
		return protocol.NodesInfo{}, err
	}
	return protocol.NodesInfo{GatewayVersion: g.opts.Version, Nodes: nodes, Playbook: notes}, nil
}

// handleVersion serves the version of the gateway. With ?os=&arch= it serves
// the version of the dist binary for that platform instead: what a node of
// that platform installs when it upgrades.
func (g *Gateway) handleVersion(w http.ResponseWriter, r *http.Request) {
	goos, goarch := r.URL.Query().Get("os"), r.URL.Query().Get("arch")
	if goos == "" && goarch == "" {
		fmt.Fprintln(w, g.opts.Version)
		return
	}
	if !ValidPlatform(goos, goarch) {
		http.Error(w, "unsupported platform", http.StatusNotFound)
		return
	}
	v, err := g.distVersion(r.Context(), goos, goarch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	fmt.Fprintln(w, v)
}

type verEntry struct {
	size int64
	mod  time.Time
	ver  string
}

var (
	verMu    sync.Mutex
	verCache = map[string]verEntry{}
)

// distVersion is the version of the dist binary for a platform. Only the
// binary for the platform the gateway runs on can be executed, so that one is
// asked (`version`); the dist dir holds the four binaries of one release, so
// its answer stands for the others, as long as their files exist.
func (g *Gateway) distVersion(ctx context.Context, goos, goarch string) (string, error) {
	if st, err := os.Stat(filepath.Join(g.opts.DistDir, upgrade.AssetName(goos, goarch))); err != nil || st.IsDir() {
		return "", fmt.Errorf("no %s binary in the dist dir", upgrade.AssetName(goos, goarch))
	}
	path := filepath.Join(g.opts.DistDir, upgrade.ThisAsset())
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return "", fmt.Errorf("no %s binary in the dist dir to read the version from", upgrade.ThisAsset())
	}
	verMu.Lock()
	e, ok := verCache[path]
	verMu.Unlock()
	if ok && e.size == st.Size() && e.mod.Equal(st.ModTime()) {
		return e.ver, nil
	}
	ver, err := upgrade.BinaryVersion(ctx, path)
	if err != nil {
		return "", err
	}
	verMu.Lock()
	verCache[path] = verEntry{st.Size(), st.ModTime(), ver}
	verMu.Unlock()
	return ver, nil
}

// ---- checksums ----

type sumEntry struct {
	size int64
	mod  time.Time
	sum  string
}

var (
	sumMu    sync.Mutex
	sumCache = map[string]sumEntry{}
)

// handleChecksums serves the SHA-256 of every binary in the dist dir in
// sha256sum format. It is computed from the files, so it always matches what
// /install/bin serves.
func (g *Gateway) handleChecksums(w http.ResponseWriter, r *http.Request) {
	names := upgrade.AllAssets()
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		path := filepath.Join(g.opts.DistDir, n)
		st, err := os.Stat(path)
		if err != nil || st.IsDir() {
			continue
		}
		sumMu.Lock()
		e, ok := sumCache[path]
		sumMu.Unlock()
		if !ok || e.size != st.Size() || !e.mod.Equal(st.ModTime()) {
			sum, err := upgrade.SHA256File(path)
			if err != nil {
				continue
			}
			e = sumEntry{st.Size(), st.ModTime(), sum}
			sumMu.Lock()
			sumCache[path] = e
			sumMu.Unlock()
		}
		fmt.Fprintf(&b, "%s  %s\n", e.sum, n)
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, b.String())
}

// ---- upgrade ----

func (g *Gateway) handleAdminUpgrade(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version        string   `json:"version"`
		TimeoutSeconds int      `json:"timeout_seconds"`
		ExpectOnline   []string `json:"expect_online"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Version == "" {
		http.Error(w, "bad request: version is required", http.StatusBadRequest)
		return
	}
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	res, err := g.UpgradeNodesExpect(r.Context(), req.Version, timeout, req.ExpectOnline)
	writeJSON(w, map[string]any{"results": res}, err)
}

// oldDaemon reports whether a node's hello version predates remote upgrade.
func oldDaemon(v string) bool { return v == "" || v == "step2" }

// OfflineDetail is what an offline node is told. Since nodes catch up by
// themselves, it is not a dead end.
const OfflineDetail = "upgrades itself when it reconnects, unless auto-upgrade is turned off there"

// reconnectWait is the longest UpgradeNodesExpect waits for the nodes that were
// online before a gateway restart to connect again.
const reconnectWait = 30 * time.Second

// UpgradeNodes tells every connected node to upgrade to target and waits up to
// timeout for each to come back on that version. Nodes that are not connected
// are listed as offline.
func (g *Gateway) UpgradeNodes(ctx context.Context, target string, timeout time.Duration) ([]protocol.UpgradeResult, error) {
	return g.UpgradeNodesExpect(ctx, target, timeout, nil)
}

// UpgradeNodesExpect is UpgradeNodes for a caller that knows which nodes were
// online before it restarted the gateway. It first waits (up to 30 s, and not
// longer than timeout) for those nodes to connect again, so a node that is
// only reconnecting, or restarting after it caught up by itself, is not
// reported as offline.
func (g *Gateway) UpgradeNodesExpect(ctx context.Context, target string, timeout time.Duration, expect []string) ([]protocol.UpgradeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	g.waitConnected(ctx, expect, min(reconnectWait, timeout))
	nodes, err := g.store.Nodes()
	if err != nil {
		return nil, err
	}
	results := make([]protocol.UpgradeResult, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		res := protocol.UpgradeResult{NodeID: n.NodeID, Hostname: n.Hostname, From: n.Version, To: target}
		g.mu.Lock()
		nc := g.conns[n.NodeID]
		g.mu.Unlock()
		switch {
		case nc == nil:
			res.Status = protocol.UpgradeOffline
			res.Detail = OfflineDetail
			results[i] = res
		case oldDaemon(n.Version):
			res.Status = protocol.UpgradeFailed
			res.Detail = "this node runs an agent-net too old to upgrade remotely; run agent-net upgrade there"
			results[i] = res
		default:
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = g.upgradeOne(ctx, nc, res, target)
			}()
		}
	}
	wg.Wait()
	return results, nil
}

// waitConnected waits until every node in ids has a live connection, up to d.
func (g *Gateway) waitConnected(ctx context.Context, ids []string, d time.Duration) {
	deadline := time.Now().Add(d)
	for {
		g.mu.Lock()
		all := true
		for _, id := range ids {
			all = all && g.conns[id] != nil
		}
		g.mu.Unlock()
		if all || time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (g *Gateway) upgradeOne(ctx context.Context, nc *nodeConn, res protocol.UpgradeResult, target string) protocol.UpgradeResult {
	fail := func(format string, a ...any) protocol.UpgradeResult {
		res.Status = protocol.UpgradeFailed
		res.Detail = fmt.Sprintf(format, a...)
		return res
	}
	raw, err := nc.call(ctx, "node.upgrade", protocol.NodeUpgrade{Version: target})
	var r protocol.NodeUpgradeResult
	switch {
	case err != nil && ctx.Err() != nil:
		return fail("no answer in time")
	case err != nil && !errors.Is(err, errNodeGone):
		return fail("%v", err)
	case err != nil:
		// The link dropped: the node is restarting, often because it caught up
		// by itself. Wait for it below, like for a restart after an install.
		r.Restarting = true
	case json.Unmarshal(raw, &r) != nil:
		return fail("bad answer from the node")
	}
	if !r.Restarting {
		res.Status = protocol.UpgradeOK
		res.To = r.To
		if r.To != target {
			return fail("node reports %s, wanted %s", r.To, target)
		}
		return res
	}
	// The node restarts now. Wait for it to come back and say hello.
	for {
		g.mu.Lock()
		cur := g.conns[nc.id]
		g.mu.Unlock()
		if cur != nil && cur != nc {
			v, _ := g.store.NodeVersion(nc.id)
			if v == target {
				res.Status = protocol.UpgradeOK
				res.To = v
				return res
			}
			if v != "" && v != res.From {
				return fail("restarted but reports %s", v)
			}
		}
		select {
		case <-ctx.Done():
			return fail("installed, but the node did not come back on %s in time", target)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

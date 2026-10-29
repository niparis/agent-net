package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/upgrade"
)

func adminReq(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(`{"version":"v9"}`))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminNeedsToken(t *testing.T) {
	gw, _ := newInstallGateway(t)
	h := gw.Handler()
	for _, p := range [][2]string{{"GET", "/v1/admin/nodes"}, {"POST", "/v1/admin/upgrade"}, {"POST", "/v1/admin/restart"}} {
		if rec := adminReq(t, h, p[0], p[1], ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: %d", p[0], p[1], rec.Code)
		}
		if rec := adminReq(t, h, p[0], p[1], "wrong"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with a wrong token: %d", p[0], p[1], rec.Code)
		}
	}
	rec := adminReq(t, h, "GET", "/v1/admin/nodes", gw.adminToken)
	if rec.Code != 200 {
		t.Fatalf("nodes with the token: %d %s", rec.Code, rec.Body)
	}
	var info protocol.NodesInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
}

func TestAdminRestartCallsHook(t *testing.T) {
	dist := t.TempDir()
	called := make(chan struct{}, 1)
	gw, err := New(Options{DataDir: t.TempDir(), DistDir: dist, Restart: func() { called <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	if rec := adminReq(t, gw.Handler(), "POST", "/v1/admin/restart", gw.adminToken); rec.Code != http.StatusAccepted {
		t.Fatalf("restart: %d", rec.Code)
	}
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("restart hook not called")
	}
}

func TestAdminInfoFile(t *testing.T) {
	gw, _ := newInstallGateway(t)
	if err := gw.WriteAdminInfo("0.0.0.0:7420"); err != nil {
		t.Fatal(err)
	}
	path := AdminFile(gw.opts.DataDir)
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("admin.json: %v %v", st, err)
	}
	raw, _ := os.ReadFile(path)
	var info AdminInfo
	json.Unmarshal(raw, &info)
	if info.URL != "https://localhost:7420" || info.Token != gw.adminToken || info.Dist != gw.opts.DistDir {
		t.Fatalf("admin.json = %+v", info)
	}
}

func TestVersionAndChecksumsEndpoints(t *testing.T) {
	data, dist := t.TempDir(), t.TempDir()
	gw, err := New(Options{DataDir: data, DistDir: dist, Version: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	body := []byte("binary one")
	if err := os.WriteFile(filepath.Join(dist, "agent-net-linux-amd64"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	h := gw.Handler()
	if rec := adminReq(t, h, "GET", "/install/version", ""); strings.TrimSpace(rec.Body.String()) != "v1.2.3" {
		t.Fatalf("version = %q", rec.Body)
	}
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:]) + "  agent-net-linux-amd64\n"
	if rec := adminReq(t, h, "GET", "/install/checksums.txt", ""); rec.Body.String() != want {
		t.Fatalf("checksums = %q, want %q", rec.Body, want)
	}
	// A changed file gets a new checksum, so it always matches what is served.
	body2 := []byte("binary two, longer")
	os.WriteFile(filepath.Join(dist, "agent-net-linux-amd64"), body2, 0o755)
	sum2 := sha256.Sum256(body2)
	if rec := adminReq(t, h, "GET", "/install/checksums.txt", ""); !strings.HasPrefix(rec.Body.String(), hex.EncodeToString(sum2[:])) {
		t.Fatalf("checksums not refreshed: %q", rec.Body)
	}
}

func TestNodeVersionStored(t *testing.T) {
	s := newTestStoreForNodes(t)
	id, _, err := s.CreateNode("h", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if nodes, _ := s.Nodes(); len(nodes) != 0 {
		t.Fatal("a node that never connected must not be listed")
	}
	if err := s.SetNodeHello(id, "box", "v0.1.0"); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.Nodes()
	if len(nodes) != 1 || nodes[0].Hostname != "box" || nodes[0].Version != "v0.1.0" {
		t.Fatalf("nodes = %+v", nodes)
	}
}

func newTestStoreForNodes(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestVersionOfDistBinary(t *testing.T) {
	dist := t.TempDir()
	gw, err := New(Options{DataDir: t.TempDir(), DistDir: dist, Version: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	h := gw.Handler()
	native := upgrade.ThisAsset()
	query := "/install/version?os=" + runtime.GOOS + "&arch=" + runtime.GOARCH
	version := func(q string) string { return strings.TrimSpace(adminReq(t, h, "GET", q, "").Body.String()) }

	if rec := adminReq(t, h, "GET", query, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("empty dist dir: %d %q", rec.Code, rec.Body)
	}
	// The dist binary says v1.1.0 while the gateway runs v1.0.0: nodes get the dist version.
	path := filepath.Join(dist, native)
	os.WriteFile(path, []byte("#!/bin/sh\necho v1.1.0\n"), 0o755)
	if v := version(query); v != "v1.1.0" {
		t.Fatalf("dist version = %q", v)
	}
	// Replacing the file is seen at once.
	os.WriteFile(path, []byte("#!/bin/sh\necho v1.2.0\n"), 0o755)
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	if v := version(query); v != "v1.2.0" {
		t.Fatalf("dist version after a change = %q", v)
	}
	// Another platform: the same release, once its file exists.
	oos, oarch := "darwin", "arm64"
	if native == upgrade.AssetName(oos, oarch) {
		oos, oarch = "linux", "amd64"
	}
	other := "/install/version?os=" + oos + "&arch=" + oarch
	if rec := adminReq(t, h, "GET", other, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("platform without a binary: %d", rec.Code)
	}
	os.WriteFile(filepath.Join(dist, upgrade.AssetName(oos, oarch)), []byte("x"), 0o755)
	if v := version(other); v != "v1.2.0" {
		t.Fatalf("other platform = %q", v)
	}
	if rec := adminReq(t, h, "GET", "/install/version?os=plan9&arch=amd64", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bad platform: %d", rec.Code)
	}
	// Without a platform it is still the gateway's own version.
	if v := version("/install/version"); v != "v1.0.0" {
		t.Fatalf("plain version = %q", v)
	}
}

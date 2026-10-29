package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBinary is a shell script that prints its version, like `agent-net version`.
func fakeBinary(version string) []byte {
	return []byte("#!/bin/sh\necho " + version + "\n")
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func writeBin(t *testing.T, path, version string) {
	t.Helper()
	if err := os.WriteFile(path, fakeBinary(version), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeGitHub stands in for github.com/<repo>/releases: it holds releases by
// tag and redirects latest/download/<file> to the latest tag.
type fakeGitHub struct {
	*httptest.Server
	latest   string
	releases map[string]map[string][]byte // tag -> file -> content
	hits     int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{releases: map[string]map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/latest/download/{file}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/download/"+f.latest+"/"+r.PathValue("file"), http.StatusFound)
	})
	mux.HandleFunc("/download/{tag}/{file}", func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		b, ok := f.releases[r.PathValue("tag")][r.PathValue("file")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// addRelease publishes a release with a binary for every platform.
func (f *fakeGitHub) addRelease(tag string) {
	files := map[string][]byte{}
	var sums strings.Builder
	for _, n := range AllAssets() {
		b := fakeBinary(tag)
		files[n] = b
		fmt.Fprintf(&sums, "%s  %s\n", sum(b), n)
	}
	files["checksums.txt"] = []byte(sums.String())
	f.releases[tag] = files
}

func TestLatestTagFollowsRedirect(t *testing.T) {
	f := newFakeGitHub(t)
	f.addRelease("v0.2.0")
	f.latest = "v0.2.0"
	tag, err := LatestTag(context.Background(), f.URL)
	if err != nil || tag != "v0.2.0" {
		t.Fatalf("LatestTag = %q, %v", tag, err)
	}
}

func TestLatestTagNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := LatestTag(context.Background(), srv.URL); err == nil {
		t.Fatal("want an error when there is no latest release")
	}
}

func TestReleaseBaseEnvOverride(t *testing.T) {
	t.Setenv("AGENT_NET_RELEASE_BASE_URL", "http://127.0.0.1:1/x/")
	if got := ReleaseBase(); got != "http://127.0.0.1:1/x" {
		t.Fatalf("ReleaseBase = %q", got)
	}
	t.Setenv("AGENT_NET_RELEASE_BASE_URL", "")
	if ReleaseBase() != DefaultReleaseBase {
		t.Fatal("default base not used")
	}
}

func TestDownloadVerifiesEveryFile(t *testing.T) {
	f := newFakeGitHub(t)
	f.addRelease("v0.2.0")
	dir := t.TempDir()
	if err := Download(context.Background(), http.DefaultClient, TagURL(f.URL, "v0.2.0"), AllAssets(), dir); err != nil {
		t.Fatal(err)
	}
	for _, n := range AllAssets() {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	f := newFakeGitHub(t)
	f.addRelease("v0.2.0")
	f.releases["v0.2.0"]["agent-net-darwin-arm64"] = []byte("tampered")
	err := Download(context.Background(), http.DefaultClient, TagURL(f.URL, "v0.2.0"), AllAssets(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch for agent-net-darwin-arm64") {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
}

func TestDownloadMissingFromChecksums(t *testing.T) {
	f := newFakeGitHub(t)
	f.addRelease("v0.2.0")
	f.releases["v0.2.0"]["checksums.txt"] = []byte(sum(fakeBinary("v0.2.0")) + "  agent-net-linux-amd64\n")
	err := Download(context.Background(), http.DefaultClient, TagURL(f.URL, "v0.2.0"), AllAssets(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("want 'not listed', got %v", err)
	}
}

func TestReplaceKeepsPrevAndRollbackSwaps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	exe := filepath.Join(dir, "agent-net")
	writeBin(t, exe, "v1")
	next := filepath.Join(t.TempDir(), "new")
	writeBin(t, next, "v2")

	if err := Replace(exe, next); err != nil {
		t.Fatal(err)
	}
	if v, _ := BinaryVersion(ctx, exe); v != "v2" {
		t.Fatalf("exe is %q, want v2", v)
	}
	if v, _ := BinaryVersion(ctx, PrevPath(exe)); v != "v1" {
		t.Fatalf("prev is %q, want v1", v)
	}
	if st, _ := os.Stat(exe); st.Mode().Perm()&0o111 == 0 {
		t.Fatal("exe is not executable")
	}

	now, err := Rollback(ctx, exe)
	if err != nil || now != "v1" {
		t.Fatalf("Rollback = %q, %v", now, err)
	}
	if v, _ := BinaryVersion(ctx, PrevPath(exe)); v != "v2" {
		t.Fatalf("prev after rollback is %q, want v2", v)
	}
	// Rolling back twice goes forward again.
	if now, err = Rollback(ctx, exe); err != nil || now != "v2" {
		t.Fatalf("second Rollback = %q, %v", now, err)
	}
	// No temp files are left next to the binary.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("want agent-net and agent-net.prev only, got %v", entries)
	}
}

func TestRollbackWithoutPrev(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "agent-net")
	writeBin(t, exe, "v1")
	if _, err := Rollback(context.Background(), exe); err == nil {
		t.Fatal("want an error without a previous binary")
	}
	if v, _ := BinaryVersion(context.Background(), exe); v != "v1" {
		t.Fatal("exe changed")
	}
}

// fakeGateway serves the node-side endpoints.
func fakeGateway(t *testing.T, bin, checksums []byte) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/install/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write(checksums) })
	mux.HandleFunc("/install/bin/{os}/{arch}", func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFromGatewayInstallsAndKeepsPrev(t *testing.T) {
	ctx := context.Background()
	exe := filepath.Join(t.TempDir(), "agent-net")
	writeBin(t, exe, "v1")
	bin := fakeBinary("v2")
	sums := fmt.Sprintf("%s  %s\n", sum(bin), ThisAsset())
	gw := fakeGateway(t, bin, []byte(sums))

	res, err := FromGateway(ctx, http.DefaultClient, gw.URL, exe, "v1", "v2")
	if err != nil || !res.Replaced || res.To != "v2" {
		t.Fatalf("FromGateway = %+v, %v", res, err)
	}
	if v, _ := BinaryVersion(ctx, exe); v != "v2" {
		t.Fatalf("exe is %q", v)
	}
	if v, _ := BinaryVersion(ctx, PrevPath(exe)); v != "v1" {
		t.Fatalf("prev is %q", v)
	}
}

func TestFromGatewayChecksumMismatchChangesNothing(t *testing.T) {
	ctx := context.Background()
	exe := filepath.Join(t.TempDir(), "agent-net")
	writeBin(t, exe, "v1")
	before, _ := os.ReadFile(exe)
	bin := fakeBinary("v2")
	sums := fmt.Sprintf("%s  %s\n", sum([]byte("something else")), ThisAsset())
	gw := fakeGateway(t, bin, []byte(sums))

	_, err := FromGateway(ctx, http.DefaultClient, gw.URL, exe, "v1", "v2")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
	after, _ := os.ReadFile(exe)
	if string(before) != string(after) {
		t.Fatal("exe changed")
	}
	if _, err := os.Stat(PrevPath(exe)); err == nil {
		t.Fatal("a .prev was written for a failed upgrade")
	}
}

func TestFromGatewayWrongVersion(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "agent-net")
	writeBin(t, exe, "v1")
	bin := fakeBinary("v3")
	gw := fakeGateway(t, bin, []byte(fmt.Sprintf("%s  %s\n", sum(bin), ThisAsset())))
	_, err := FromGateway(context.Background(), http.DefaultClient, gw.URL, exe, "v1", "v2")
	if err == nil || !strings.Contains(err.Error(), "v3") {
		t.Fatalf("want a version error, got %v", err)
	}
	if v, _ := BinaryVersion(context.Background(), exe); v != "v1" {
		t.Fatal("exe changed")
	}
}

func TestFromGatewayAlreadyInstalled(t *testing.T) {
	// The binary on disk already is the wanted one (the daemon still runs
	// the old code): nothing is replaced, no .prev is written.
	exe := filepath.Join(t.TempDir(), "agent-net")
	writeBin(t, exe, "v2")
	bin := fakeBinary("v2")
	gw := fakeGateway(t, bin, []byte(fmt.Sprintf("%s  %s\n", sum(bin), ThisAsset())))
	res, err := FromGateway(context.Background(), http.DefaultClient, gw.URL, exe, "v1", "v2")
	if err != nil || res.Replaced || res.To != "v2" {
		t.Fatalf("FromGateway = %+v, %v", res, err)
	}
	if _, err := os.Stat(PrevPath(exe)); err == nil {
		t.Fatal("unexpected .prev")
	}
}

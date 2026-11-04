// Package upgrade finds, downloads, verifies and installs agent-net releases.
// It depends on nothing else in agent-net, so the daemon and the CLI can both
// use it.
package upgrade

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// ReleasePublicKeyBase64 is the Ed25519 release verification key. Replace this
// placeholder with the public key printed by scripts/release-key before release.
const ReleasePublicKeyBase64 = "REPLACE_WITH_RELEASE_PUBLIC_KEY"

// releaseKey is private to this package so tests can use freshly generated keys.
var releaseKey, _ = base64.StdEncoding.DecodeString(ReleasePublicKeyBase64)

// ReleaseMetadata travels with the binaries so nodes can verify the same bytes
// the release signed. The SSH signature is only needed by install.sh.
var ReleaseMetadata = []string{"checksums.txt", "checksums.txt.sig"}

// DefaultReleaseBase is where GitHub serves the releases of agent-net.
const DefaultReleaseBase = "https://github.com/niparis/agent-net/releases"

// ReleaseBase returns the release base URL. AGENT_NET_RELEASE_BASE_URL
// replaces it (tests point it at a fake server).
func ReleaseBase() string {
	if b := os.Getenv("AGENT_NET_RELEASE_BASE_URL"); b != "" {
		return strings.TrimRight(b, "/")
	}
	return DefaultReleaseBase
}

// Platforms are the (os, arch) pairs a release holds.
var Platforms = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}}

// AssetName is the file name of the binary for one platform.
func AssetName(goos, goarch string) string { return "agent-net-" + goos + "-" + goarch }

// ThisAsset is the file name of the binary for this machine.
func ThisAsset() string { return AssetName(runtime.GOOS, runtime.GOARCH) }

// AllAssets lists the binaries of all four platforms.
func AllAssets() []string {
	var out []string
	for _, p := range Platforms {
		out = append(out, AssetName(p[0], p[1]))
	}
	return out
}

var tagRe = regexp.MustCompile(`/download/([^/]+)/`)

// LatestTag asks the release host which release is the latest. It follows no
// redirect: GitHub answers releases/latest/download/<file> with a redirect to
// releases/download/<tag>/<file>, and the tag is read from its Location.
func LatestTag(ctx context.Context, base string) (string, error) {
	c := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, "HEAD", strings.TrimRight(base, "/")+"/latest/download/checksums.txt", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach the release server: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || loc == "" {
		return "", fmt.Errorf("cannot find the latest release (%s answered %s)", base, resp.Status)
	}
	m := tagRe.FindStringSubmatch(loc)
	if m == nil {
		return "", fmt.Errorf("cannot read a release tag from %q", loc)
	}
	return m[1], nil
}

// TagURL is the directory of one release.
func TagURL(base, tag string) string {
	return strings.TrimRight(base, "/") + "/download/" + tag
}

// ParseChecksums reads sha256sum output into name -> hex digest.
func ParseChecksums(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return out, sc.Err()
}

// SHA256File returns the hex SHA-256 of a file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func get(ctx context.Context, c *http.Client, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", url, err)
	}
	return f.Close()
}

// signedChecksums fetches and verifies the exact checksum bytes before parsing.
func signedChecksums(ctx context.Context, c *http.Client, base, dir string) (map[string]string, error) {
	for _, name := range ReleaseMetadata {
		if err := get(ctx, c, base+"/"+name, filepath.Join(dir, name)); err != nil {
			if name == "checksums.txt.sig" {
				return nil, errors.New("release checksums signature is missing or invalid")
			}
			return nil, fmt.Errorf("cannot download checksums.txt: %w", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return nil, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, "checksums.txt.sig"))
	if err != nil || len(releaseKey) != ed25519.PublicKeySize || !ed25519.Verify(releaseKey, data, sig) {
		return nil, errors.New("release checksums signature is missing or invalid")
	}
	return ParseChecksums(strings.NewReader(string(data)))
}

// Download fetches and verifies the signed checksums before downloading each
// named file and checking its SHA-256. On error callers must not use the files.
func Download(ctx context.Context, c *http.Client, dirURL string, names []string, dir string) error {
	dirURL = strings.TrimRight(dirURL, "/")
	want, err := signedChecksums(ctx, c, dirURL, dir)
	if err != nil {
		return err
	}
	for _, n := range names {
		w, ok := want[n]
		if !ok {
			return fmt.Errorf("%s is not listed in checksums.txt", n)
		}
		p := filepath.Join(dir, n)
		if err := get(ctx, c, dirURL+"/"+n, p); err != nil {
			return fmt.Errorf("cannot download %s: %w", n, err)
		}
		got, err := SHA256File(p)
		if err != nil {
			return err
		}
		if got != w {
			return fmt.Errorf("checksum mismatch for %s (wanted %s, got %s)", n, w, got)
		}
	}
	return nil
}

// BinaryVersion runs `<path> version` and returns what it prints.
func BinaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", fmt.Errorf("%s does not run: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// copyAtomic writes src to dst through a temp file in dst's directory, then
// renames it. dst is either the old file or the complete new one.
func copyAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	ok = true
	return nil
}

// PrevPath is where the previous binary is kept.
func PrevPath(exe string) string { return exe + ".prev" }

// Replace keeps the current binary as <exe>.prev and atomically puts newBin
// at exe. If it fails, exe is unchanged.
func Replace(exe, newBin string) error {
	if err := copyAtomic(exe, PrevPath(exe)); err != nil {
		return fmt.Errorf("cannot keep the old binary as %s: %w", PrevPath(exe), err)
	}
	if err := copyAtomic(newBin, exe); err != nil {
		return fmt.Errorf("cannot replace %s: %w", exe, err)
	}
	return nil
}

// Rollback swaps exe and <exe>.prev: the previous binary becomes current and
// the current one becomes the new .prev. It returns the version now in place.
func Rollback(ctx context.Context, exe string) (string, error) {
	prev := PrevPath(exe)
	if _, err := os.Stat(prev); err != nil {
		return "", errors.New("there is no previous binary to roll back to (" + prev + " is missing)")
	}
	if _, err := BinaryVersion(ctx, prev); err != nil {
		return "", fmt.Errorf("the previous binary is not usable: %w", err)
	}
	hold := exe + ".hold"
	if err := copyAtomic(exe, hold); err != nil {
		return "", err
	}
	defer os.Remove(hold)
	if err := os.Rename(prev, exe); err != nil {
		return "", err
	}
	if err := os.Rename(hold, prev); err != nil {
		return "", err
	}
	return BinaryVersion(ctx, exe)
}

// PlaceFile copies src to dst atomically (used for the gateway dist dir).
func PlaceFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyAtomic(src, dst)
}

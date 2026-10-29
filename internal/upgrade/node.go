package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// GatewayVersion asks the gateway which agent-net version it runs.
func GatewayVersion(ctx context.Context, c *http.Client, gatewayURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(gatewayURL, "/")+"/install/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.TrimSpace(string(b)), err
}

// ServedVersion asks the gateway which version it serves to a node of this
// platform: the version of its dist binary, which is what FromGateway
// installs. A gateway from before this feature ignores the platform and
// answers with its own version.
func ServedVersion(ctx context.Context, c *http.Client, gatewayURL string) (string, error) {
	u := strings.TrimRight(gatewayURL, "/") + "/install/version?os=" + runtime.GOOS + "&arch=" + runtime.GOARCH
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.TrimSpace(string(b)), err
}

// Installed is what FromGateway did.
type Installed struct {
	From, To string
	// Replaced is false when the binary on disk already was the wanted one.
	Replaced bool
}

// FromGateway downloads this machine's binary from the gateway over c (which
// must trust only the gateway CA), verifies it against the gateway's
// checksums, and replaces exe, keeping exe.prev. want, when set, is the
// version the new binary must report. current is the version that runs now.
// Nothing changes unless every check passes.
func FromGateway(ctx context.Context, c *http.Client, gatewayURL, exe, current, want string) (Installed, error) {
	res := Installed{From: current}
	dir, err := os.MkdirTemp("", "agent-net-upgrade.")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)
	// The binary is served at /install/bin/{os}/{arch}; fetch it under its
	// release name so it can be checked against the checksum list.
	base := strings.TrimRight(gatewayURL, "/") + "/install"
	asset := ThisAsset()
	if err := downloadGatewayAsset(ctx, c, base, asset, dir); err != nil {
		return res, err
	}
	newBin := dir + "/" + asset
	to, err := BinaryVersion(ctx, newBin)
	if err != nil {
		return res, err
	}
	res.To = to
	if want != "" && to != want {
		return res, fmt.Errorf("the gateway serves %s but %s was asked for", to, want)
	}
	if disk, err := BinaryVersion(ctx, exe); err == nil && disk == to {
		return res, nil
	}
	if err := Replace(exe, newBin); err != nil {
		return res, err
	}
	res.Replaced = true
	return res, nil
}

func downloadGatewayAsset(ctx context.Context, c *http.Client, installURL, asset, dir string) error {
	goos, goarch, ok := splitAsset(asset)
	if !ok {
		return errors.New("bad asset name " + asset)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	sumsPath := dir + "/checksums.txt"
	if err := get(ctx, c, installURL+"/checksums.txt", sumsPath); err != nil {
		return fmt.Errorf("cannot get the checksum from the gateway: %w", err)
	}
	f, err := os.Open(sumsPath)
	if err != nil {
		return err
	}
	want, err := ParseChecksums(f)
	f.Close()
	if err != nil {
		return err
	}
	w, ok := want[asset]
	if !ok {
		return fmt.Errorf("the gateway has no %s binary (run agent-net upgrade on the gateway first)", asset)
	}
	p := dir + "/" + asset
	if err := get(ctx, c, installURL+"/bin/"+goos+"/"+goarch, p); err != nil {
		return fmt.Errorf("cannot download the binary from the gateway: %w", err)
	}
	got, err := SHA256File(p)
	if err != nil {
		return err
	}
	if got != w {
		return fmt.Errorf("checksum mismatch for %s (wanted %s, got %s)", asset, w, got)
	}
	return nil
}

func splitAsset(asset string) (goos, goarch string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(asset, "agent-net-"), "-")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

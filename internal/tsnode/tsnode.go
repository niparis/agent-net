// Package tsnode runs agent-net's own Tailscale node for connected apps, so
// they get a name that no other service on the machine shares (decision 0058).
//
// The owner's choice lives in tailscale-node.json in the gateway data folder.
// The node's state lives in the tailscale folder next to it, with an
// ownership marker that is written before the node first starts. Cleanup
// removes only a folder that carries the marker.
package tsnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	choiceFile = "tailscale-node.json"
	markerFile = "agent-net-owned.json"
)

// ErrNeedsLogin matches a NeedsLoginError with errors.Is.
var ErrNeedsLogin = errors.New("the agent-net Tailscale node needs a login")

// NeedsLoginError carries the login link Tailscale gave for the new node.
type NeedsLoginError struct{ URL string }

func (e *NeedsLoginError) Error() string {
	return "Add agent-net to your tailnet: open " + e.URL + ", then try again."
}
func (e *NeedsLoginError) Unwrap() error { return ErrNeedsLogin }

// Choice is the owner's setup choice. Name is the node's Tailscale host
// name. AuthKeyFile is the path of a file with an auth key; the key itself is
// never stored here.
type Choice struct {
	Name        string `json:"name"`
	AuthKeyFile string `json:"auth_key_file,omitempty"`
}

// Node is the embedded Tailscale node. Tests use a fake.
type Node interface {
	// Up starts the node and waits until it runs. It returns its DNS name
	// (for example agent-net.example.ts.net), or a *NeedsLoginError.
	Up(ctx context.Context) (string, error)
	// ListenFunnel opens a public HTTPS Funnel listener on port. The remote
	// address of each accepted connection is the internet client.
	ListenFunnel(port int) (net.Listener, error)
	// Logout removes the node from the tailnet.
	Logout(ctx context.Context) error
	Close() error
}

// Opener makes a node that keeps its state in stateDir.
type Opener func(stateDir string, c Choice) (Node, error)

var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (c Choice) validate() error {
	if !labelRE.MatchString(c.Name) {
		return fmt.Errorf("Tailscale name %q must be one DNS label: lower-case letters, digits and hyphens", c.Name)
	}
	if c.AuthKeyFile != "" && !filepath.IsAbs(c.AuthKeyFile) {
		return errors.New("the auth key file must be an absolute path")
	}
	return nil
}

// StateDir is the node's state folder. It holds the node's private keys.
func StateDir(dataDir string) string { return filepath.Join(dataDir, "tailscale") }

// SaveChoice records the owner's choice in the gateway data folder.
func SaveChoice(dataDir string, c Choice) error {
	if err := c.validate(); err != nil {
		return err
	}
	return writeJSON(dataDir, choiceFile, c)
}

// ReadChoice returns os.ErrNotExist when the owner has not chosen a name.
func ReadChoice(dataDir string) (Choice, error) {
	var c Choice
	data, err := os.ReadFile(filepath.Join(dataDir, choiceFile))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("decode %s: %w", choiceFile, err)
	}
	if err := c.validate(); err != nil {
		return c, fmt.Errorf("%s: %w", choiceFile, err)
	}
	return c, nil
}

// RemoveChoice goes back to the machine's own node. A missing file is fine.
func RemoveChoice(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, choiceFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type marker struct {
	Name string `json:"name"`
}

// Start writes the ownership marker, then opens the node. The marker comes
// first so a crash never leaves an unmarked state folder.
func Start(dataDir string, c Choice, open Opener) (Node, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	dir := StateDir(dataDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	if err := writeJSON(dir, markerFile, marker{Name: c.Name}); err != nil {
		return nil, err
	}
	return open(dir, c)
}

// Cleanup logs the node out and deletes its state folder, only when the
// folder carries agent-net's marker. When the logout fails the folder is kept
// so a later run can retry. A node that never logged in is just deleted.
func Cleanup(ctx context.Context, dataDir string, open Opener) (bool, error) {
	dir := StateDir(dataDir)
	data, err := os.ReadFile(filepath.Join(dir, markerFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil {
		return false, fmt.Errorf("decode Tailscale node marker: %w", err)
	}
	c := Choice{Name: m.Name}
	if c.validate() != nil {
		c.Name = "agent-net"
	}
	node, err := open(dir, c)
	if err != nil {
		return false, err
	}
	_, upErr := node.Up(ctx)
	var logoutErr error
	switch {
	case errors.Is(upErr, ErrNeedsLogin):
	case upErr != nil:
		logoutErr = fmt.Errorf("start the agent-net Tailscale node to log it out: %w", upErr)
	default:
		logoutErr = node.Logout(ctx)
	}
	closeErr := node.Close()
	if logoutErr != nil {
		return false, logoutErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	return true, nil
}

// readAuthKey reads a key file. Errors name the file, never its content.
func readAuthKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Tailscale auth key file %s: %w", path, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return "", fmt.Errorf("Tailscale auth key file %s must hold one key", path)
	}
	return key, nil
}

func writeJSON(dir, name string, v any) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// upPoll is how often Up checks the node while it waits.
const upPoll = 250 * time.Millisecond

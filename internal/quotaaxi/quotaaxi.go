// Package quotaaxi finds and installs quota-axi, the CLI that reads the
// owner's subscription quotas (decision 0025).
//
// A quota-axi the owner installed is always used first: the path in node.json,
// then PATH, then ~/.local/bin, then on macOS the usual Node folders. When
// none is found and Node 22.19 or newer and npm are available, agent-net
// installs the pinned version in a private folder under its own data
// directory. The daemon then runs that copy. The npm registry checks the
// package integrity; agent-net passes no other install flags.
package quotaaxi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Version is the quota-axi release agent-net installs. Updating it is part
	// of an agent-net release.
	Version = "0.1.69"
	// MinNode is the oldest Node that quota-axi supports.
	MinNode = "22.19"
	// NeedNodeMessage is the one line shown when Node or npm is missing.
	NeedNodeMessage = "quota-axi needs Node 22.19 or newer; install Node and agent-net installs quota-axi for you."

	quotaCommand = "quota-axi"
	// installTimeout bounds one npm install.
	installTimeout = 3 * time.Minute
)

// ErrNeedNode means Node 22.19 or newer, or npm, was not found. agent-net
// never installs Node itself.
var ErrNeedNode = errors.New(NeedNodeMessage)

// platform and homeDir are replaced in tests.
var (
	platform = runtime.GOOS
	homeDir  = os.UserHomeDir
)

// ensureMu makes one install run at a time, so setup and the daemon cannot
// start two npm processes for the same folder.
var ensureMu sync.Mutex

func home() string {
	if h, err := homeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// DataDir is the agent-net folder for data the binary writes.
func DataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "agent-net")
	}
	return filepath.Join(home(), ".local", "share", "agent-net")
}

// Prefix is the private npm prefix of agent-net's quota-axi copy.
func Prefix() string { return filepath.Join(DataDir(), "quota-axi") }

// Binary is the private quota-axi the daemon runs.
func Binary() string { return filepath.Join(Prefix(), "node_modules", ".bin", quotaCommand) }

// RecordPath is where a failed install is remembered, so it is retried at
// most once a day.
func RecordPath() string { return filepath.Join(Prefix(), "install.json") }

func usable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// UserPath finds a quota-axi the owner installed. configured is node.json
// quota_axi, when set. The private copy is never returned.
func UserPath(configured string) (string, bool) {
	if configured = strings.TrimSpace(configured); configured != "" {
		if filepath.IsAbs(configured) {
			return configured, usable(configured)
		}
		if p, err := exec.LookPath(configured); err == nil {
			return p, true
		}
		return "", false
	}
	if p, err := exec.LookPath(quotaCommand); err == nil {
		return p, true
	}
	seen := map[string]bool{}
	for _, dir := range userDirs() {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		p := filepath.Join(dir, quotaCommand)
		if usable(p) {
			return p, true
		}
	}
	return "", false
}

// Path finds the quota-axi to run: a copy the owner installed first, then
// agent-net's private copy.
func Path(configured string) (string, bool) {
	if p, ok := UserPath(configured); ok {
		return p, true
	}
	if usable(Binary()) {
		return Binary(), true
	}
	return "", false
}

// ExecEnv returns the environment for running the quota-axi at path. When a
// node binary sits next to it, that folder goes first on PATH, so a copy in an
// nvm or npm folder uses the Node it belongs to.
func ExecEnv(path string) []string {
	env := os.Environ()
	dir := filepath.Dir(path)
	if usable(filepath.Join(dir, "node")) {
		return prependPath(env, dir)
	}
	return env
}

func prependPath(env []string, dir string) []string {
	sep := string(os.PathListSeparator)
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, "PATH="+dir+sep+strings.TrimPrefix(kv, "PATH="))
			found = true
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir+sep+os.Getenv("PATH"))
	}
	return out
}

// userDirs lists the folders where an owner-installed quota-axi may sit.
func userDirs() []string {
	dirs := []string{filepath.Join(home(), ".local", "bin")}
	if platform != "darwin" {
		return dirs
	}
	dirs = append(dirs, nvmBins()...)
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	if p := npmGlobalBin(); p != "" {
		dirs = append(dirs, p)
	}
	return dirs
}

// nvmBins lists ~/.nvm/versions/node/<version>/bin from newest to oldest.
func nvmBins() []string {
	matches, _ := filepath.Glob(filepath.Join(home(), ".nvm", "versions", "node", "*", "bin"))
	sort.Slice(matches, func(i, j int) bool {
		return versionOf(matches[i]) > versionOf(matches[j])
	})
	return matches
}

// versionOf turns a .../node/v22.19.0/bin path into a number for sorting.
func versionOf(dir string) int {
	name := strings.TrimPrefix(filepath.Base(filepath.Dir(dir)), "v")
	parts := strings.Split(name, ".")
	n := 0
	for i, part := range parts {
		if i >= 3 {
			break
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		n += v << (20 - 8*i)
	}
	return n
}

// npmGlobalBin asks npm where its global packages are. It is used on macOS,
// where a global npm prefix is not always on a daemon's PATH.
func npmGlobalBin() string {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, npm, "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return ""
	}
	return filepath.Join(prefix, "bin")
}

// nodeReady finds a Node 22.19 or newer and an npm next to it (or on PATH).
func nodeReady() (nodeDir, npm string, err error) {
	for _, dir := range nodeDirs() {
		node := filepath.Join(dir, "node")
		if !usable(node) || !nodeVersionOK(node) {
			continue
		}
		candidate := filepath.Join(dir, "npm")
		if !usable(candidate) {
			if p, e := exec.LookPath("npm"); e == nil {
				candidate = p
			} else {
				continue
			}
		}
		return dir, candidate, nil
	}
	return "", "", ErrNeedNode
}

// nodeDirs lists the folders to look for node in, in the order tried.
func nodeDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	if p, err := exec.LookPath("node"); err == nil {
		add(filepath.Dir(p))
	}
	add(filepath.Join(home(), ".local", "bin"))
	if platform == "darwin" {
		for _, dir := range nvmBins() {
			add(dir)
		}
		add("/opt/homebrew/bin")
		add("/usr/local/bin")
		add(npmGlobalBin())
	}
	return dirs
}

// NodeProblem returns ErrNeedNode when Node 22.19 or newer or npm is missing.
func NodeProblem() error {
	if _, _, err := nodeReady(); err != nil {
		return ErrNeedNode
	}
	return nil
}

func nodeVersionOK(node string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "--version").Output()
	if err != nil {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(string(out)), "v"), ".")
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return err1 == nil && err2 == nil && (major > 22 || (major == 22 && minor >= 19))
}

// Ensure installs the pinned quota-axi in the private prefix when the owner
// has none and none is installed yet. It returns ErrNeedNode when Node or npm
// is missing. A failed install is tried again the next day at the earliest.
// logf gets one line per install attempt.
func Ensure(ctx context.Context, logf func(format string, args ...any)) error {
	ensureMu.Lock()
	defer ensureMu.Unlock()

	if _, ok := UserPath(""); ok {
		return nil
	}
	if usable(Binary()) {
		return nil
	}
	if failedToday(time.Now()) {
		return nil
	}
	nodeDir, npm, err := nodeReady()
	if err != nil {
		return ErrNeedNode
	}
	ictx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	cmd := exec.CommandContext(ictx, npm,
		"install", "--prefix", Prefix(), "--no-fund", "--no-audit", "quota-axi@"+Version)
	cmd.Env = prependPath(os.Environ(), nodeDir)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		os.RemoveAll(Prefix())
		recordFailure(time.Now())
		reason := firstLine(out, runErr)
		if logf != nil {
			logf("quota-axi: install failed: %s", reason)
		}
		return fmt.Errorf("install quota-axi: %w", runErr)
	}
	if !usable(Binary()) {
		os.RemoveAll(Prefix())
		recordFailure(time.Now())
		if logf != nil {
			logf("quota-axi: install failed: the package put no binary in %s", Binary())
		}
		return fmt.Errorf("install quota-axi: no binary in %s", Binary())
	}
	os.Remove(RecordPath())
	if logf != nil {
		logf("quota-axi: installed %s", Version)
	}
	return nil
}

// EnsureAsync runs Ensure in the background, so a daemon start never waits
// for npm.
func EnsureAsync(logf func(format string, args ...any)) {
	go func() { _ = Ensure(context.Background(), logf) }()
}

// Remove deletes the private copy and its record. removed is true when a
// folder was there.
func Remove() (removed bool, err error) {
	if !exists(Prefix()) {
		return false, nil
	}
	if err := os.RemoveAll(Prefix()); err != nil {
		return false, err
	}
	return true, nil
}

type installRecord struct {
	FailedDay string `json:"failed_day"`
}

// failedToday tells whether an install failed earlier today, in local time.
func failedToday(now time.Time) bool {
	b, err := os.ReadFile(RecordPath())
	if err != nil {
		return false
	}
	var r installRecord
	if json.Unmarshal(b, &r) != nil {
		return false
	}
	return r.FailedDay == now.Format("2006-01-02")
}

func recordFailure(now time.Time) {
	if err := os.MkdirAll(Prefix(), 0o700); err != nil {
		return
	}
	b, _ := json.Marshal(installRecord{FailedDay: now.Format("2006-01-02")})
	_ = os.WriteFile(RecordPath(), append(b, '\n'), 0o600)
}

// firstLine returns the first non-empty output line, or the error.
func firstLine(out []byte, err error) string {
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}

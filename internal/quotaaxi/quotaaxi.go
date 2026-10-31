// Package quotaaxi finds and installs quota-axi, the CLI that reads the
// owner's subscription quotas (decision 0025).
//
// A compatible quota-axi the owner installed is used first: the path in
// node.json, then PATH, then ~/.local/bin, then on macOS the usual Node
// folders. Older copies are skipped in favour of the pinned private copy.
// When no compatible copy is found and Node 22.19 or newer and npm are
// available, agent-net installs the pin in a private folder under its own
// data directory. The npm registry checks package integrity; agent-net passes
// no other install flags.
package quotaaxi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/loginpath"
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

type cachedVersion struct {
	mtime      int64
	version    string
	err        error
	skipLogged bool
}

var versionCache = struct {
	sync.Mutex
	byPath map[string]cachedVersion
}{byPath: make(map[string]cachedVersion)}

var versionPattern = regexp.MustCompile(`(?:^|[^0-9A-Za-z])v?((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)(?:$|[^0-9A-Za-z.+-])`)

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

// Copy describes one discovered executable, including copies that cannot run.
type Copy struct {
	Path    string
	Version string
	Runs    bool
	Private bool
}

// Discover lists owner and private copies in search order. It does not install
// or change any copy. A failed version command leaves Runs false.
func Discover(configured string) []Copy {
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		p, err := filepath.Abs(p)
		if err != nil || seen[p] || !usable(p) {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	if configured != "" {
		if filepath.IsAbs(configured) {
			add(configured)
		} else if p, err := exec.LookPath(configured); err == nil {
			add(p)
		}
	}
	path := loginpath.Path()
	for _, dir := range filepath.SplitList(path) {
		if dir != "" {
			add(filepath.Join(dir, quotaCommand))
		}
	}
	// Some shells supply additional matches through their which implementation.
	if which, err := exec.LookPath("which"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, which, "-a", quotaCommand)
		cmd.Env = append(os.Environ(), "PATH="+path)
		if out, err := cmd.Output(); err == nil {
			for _, p := range strings.Split(string(out), "\n") {
				add(strings.TrimSpace(p))
			}
		}
		cancel()
	}
	for _, dir := range append(append([]string{filepath.Join(home(), ".local", "bin")}, nvmBins()...), systemNodeDirs...) {
		add(filepath.Join(dir, quotaCommand))
	}
	if dir := npmGlobalBin(); dir != "" {
		add(filepath.Join(dir, quotaCommand))
	}
	add(Binary())
	copies := make([]Copy, 0, len(paths))
	for _, p := range paths {
		version, err := InstalledVersion(p)
		copies = append(copies, Copy{Path: p, Version: version, Runs: err == nil, Private: p == Binary()})
	}
	return copies
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

// Path finds the quota-axi to run: a compatible owner copy first, then
// agent-net's private copy. Older owner copies are logged and skipped.
func Path(configured string, logf ...func(format string, args ...any)) (string, bool) {
	if p, ok := UserPath(configured); ok {
		if version, err := InstalledVersion(p); err == nil && olderThanPinned(version) {
			logSkipped(p, version, logCallback(logf))
		} else {
			return p, true
		}
	}
	if usable(Binary()) {
		return Binary(), true
	}
	return "", false
}

func logCallback(logf []func(format string, args ...any)) func(format string, args ...any) {
	if len(logf) == 0 {
		return nil
	}
	return logf[0]
}

// InstalledVersion runs the fast version command and caches its parsed
// semantic version until the executable's modification time changes.
func InstalledVersion(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	mtime := info.ModTime().UnixNano()
	versionCache.Lock()
	if cached, ok := versionCache.byPath[path]; ok && cached.mtime == mtime {
		versionCache.Unlock()
		return cached.version, cached.err
	}
	versionCache.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env, err := NodeExecEnv(path)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = env
	out, runErr := cmd.Output()
	var version string
	var parseErr error
	if runErr != nil {
		parseErr = fmt.Errorf("run --version: %w", runErr)
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 127 {
			parseErr = ErrNeedNode
		}
	} else if match := versionPattern.FindStringSubmatch(strings.TrimSpace(string(out))); match == nil {
		parseErr = fmt.Errorf("could not parse semantic version from --version output")
	} else {
		version = match[1]
		if _, err := parseSemver(version); err != nil {
			parseErr = err
		}
	}

	versionCache.Lock()
	versionCache.byPath[path] = cachedVersion{mtime: mtime, version: version, err: parseErr}
	versionCache.Unlock()
	return version, parseErr
}

type semanticVersion struct {
	major, minor, patch uint64
	prerelease          []string
}

func parseSemver(raw string) (semanticVersion, error) {
	var v semanticVersion
	main := raw
	if i := strings.IndexByte(main, '+'); i >= 0 {
		main = main[:i]
	}
	if i := strings.IndexByte(main, '-'); i >= 0 {
		v.prerelease = strings.Split(main[i+1:], ".")
		main = main[:i]
	}
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("invalid semantic version %q", raw)
	}
	values := []*uint64{&v.major, &v.minor, &v.patch}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", raw)
		}
		*values[i] = n
	}
	for _, part := range v.prerelease {
		if part == "" {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", raw)
		}
		if _, err := strconv.ParseUint(part, 10, 64); err == nil && len(part) > 1 && part[0] == '0' {
			return semanticVersion{}, fmt.Errorf("invalid semantic version %q", raw)
		}
	}
	return v, nil
}

func olderThanPinned(version string) bool {
	got, err := parseSemver(version)
	if err != nil {
		return false
	}
	pinned, err := parseSemver(Version)
	if err != nil {
		return false
	}
	if got.major != pinned.major {
		return got.major < pinned.major
	}
	if got.minor != pinned.minor {
		return got.minor < pinned.minor
	}
	if got.patch != pinned.patch {
		return got.patch < pinned.patch
	}
	if len(got.prerelease) == 0 {
		return false
	}
	if len(pinned.prerelease) == 0 {
		return true
	}
	return comparePrerelease(got.prerelease, pinned.prerelease) < 0
}

func comparePrerelease(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		an, ae := strconv.ParseUint(a[i], 10, 64)
		bn, be := strconv.ParseUint(b[i], 10, 64)
		switch {
		case ae == nil && be == nil:
			if an < bn {
				return -1
			}
			if an > bn {
				return 1
			}
		case ae == nil:
			return -1
		case be == nil:
			return 1
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

func logSkipped(path, version string, logf func(format string, args ...any)) {
	if logf == nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mtime := info.ModTime().UnixNano()
	versionCache.Lock()
	cached, ok := versionCache.byPath[path]
	if !ok || cached.mtime != mtime || cached.skipLogged {
		versionCache.Unlock()
		return
	}
	cached.skipLogged = true
	versionCache.byPath[path] = cached
	versionCache.Unlock()
	logf("quota-axi: skipped %s (version %s is older than pinned %s)", path, version, Version)
}

// ExecEnv returns the environment for running the quota-axi at path. When a
// node binary sits next to it, that folder goes first on PATH, so a copy in an
// nvm or npm folder uses the Node it belongs to.
func ExecEnv(path string) []string {
	env, _ := NodeExecEnv(path)
	return env
}

// systemNodeDirs are the usual Node folders outside PATH (Homebrew, the
// official installer). Tests clear it so the host's Node does not count.
var systemNodeDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// NodeExecEnv finds a supported Node for quota-axi without requiring npm.
func NodeExecEnv(path string) ([]string, error) {
	env := os.Environ()
	dirs := append([]string{filepath.Dir(path)}, nvmBins()...)
	dirs = append(dirs, systemNodeDirs...)
	if node, err := exec.LookPath("node"); err == nil {
		dirs = append(dirs, filepath.Dir(node))
	}
	for _, dir := range dirs {
		node := filepath.Join(dir, "node")
		if usable(node) && nodeVersionOK(node) {
			return prependPath(env, dir), nil
		}
	}
	return nil, ErrNeedNode
}

// ServiceEnvironment returns the non-secret path settings that the daemon's
// service needs to find the same local quota stores as the setup shell. System
// services do not inherit variables exported by a shell profile. Provider
// credential variables are deliberately excluded.
func ServiceEnvironment(home string) []string {
	values := map[string]string{}
	xdgPathKeys := map[string]bool{
		"XDG_CACHE_HOME":  true,
		"XDG_CONFIG_HOME": true,
		"XDG_DATA_HOME":   true,
		"XDG_RUNTIME_DIR": true,
		"XDG_STATE_HOME":  true,
	}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if xdgPathKeys[key] || key == "OPENCODE_DB" || key == "PI_CODING_AGENT_DIR" || key == "QUOTA_AXI_OPENCODE_GO_PI_AUTH" {
			if value != "" {
				values[key] = value
			}
		}
	}
	if home != "" {
		values["HOME"] = home
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
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
	dirs = append(dirs, systemNodeDirs...)
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
		for _, dir := range systemNodeDirs {
			add(dir)
		}
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
// has no compatible copy and none is installed yet. It returns ErrNeedNode
// when Node or npm is missing. A failed install is tried again the next day at
// the earliest. logf gets one line per install attempt.
func Ensure(ctx context.Context, logf func(format string, args ...any)) error {
	ensureMu.Lock()
	defer ensureMu.Unlock()

	if user, ok := UserPath(""); ok {
		if version, err := InstalledVersion(user); err != nil || !olderThanPinned(version) {
			return nil
		} else {
			logSkipped(user, version, logf)
		}
	}
	return ensurePrivateLocked(ctx, logf)
}

// EnsurePrivate installs the pinned copy even when a compatible owner copy
// exists. It is used when an owner copy rejects options supported by the pin.
func EnsurePrivate(ctx context.Context, logf func(format string, args ...any)) error {
	ensureMu.Lock()
	defer ensureMu.Unlock()
	return ensurePrivateLocked(ctx, logf)
}

func ensurePrivateLocked(ctx context.Context, logf func(format string, args ...any)) error {
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

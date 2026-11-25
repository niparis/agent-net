package agentfiles

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/claudesettings"
	agentnetworkers "github.com/niparis/agent-net/plugins/claude/agent-net-workers"
)

// The Claude Code "agent-net workers" plugin: a band above the prompt with the
// workers the session started, a status line entry, and a line when a worker
// ends. Claude Code loads a plugin from the skills folder of its config
// directory with no marketplace and no settings entry, so agent-net writes the
// plugin files in its own folder and puts one link to them in that folder. The
// daemon restores the link when the owner once said yes.
//
// An older agent-net put the same plugin in a local marketplace and enabled it
// in the user's settings file. That file may belong to another tool (the owner
// keeps his in a dotfiles repository), so this code never writes it when it is
// a link or not writable. The old marketplace entry then stays, and Claude Code
// runs the old copy until the owner moves that entry out. The new link is
// already there for the day the old entry is lost.

// The marketplace, plugin and skills ids Claude Code knows.
const (
	ClaudeWorkersMarketplace = "agent-net"
	ClaudeWorkersPlugin      = "agent-net-workers"
	ClaudeWorkersSkillsID    = "agent-net-workers@skills-dir"
	ClaudeWorkersOldID       = "agent-net-workers@agent-net"
)

const claudeWorkersPluginManifest = ".claude-plugin/plugin.json"

// ClaudeWorkersDir is the local marketplace folder. It sits beside the gateway
// binaries in ~/.local/share/agent-net. The plugin files stay here; the skills
// folder only holds a link to them.
func ClaudeWorkersDir() string {
	return filepath.Join(home(), ".local", "share", "agent-net", "claude-plugins")
}

// ClaudeWorkersPluginDir is the mod inside the marketplace.
func ClaudeWorkersPluginDir() string {
	return filepath.Join(ClaudeWorkersDir(), ClaudeWorkersPlugin)
}

// ClaudeWorkersSkillsLink is the one entry agent-net puts in the skills folder
// of Claude Code's config directory.
func ClaudeWorkersSkillsLink() string {
	dir := claudesettings.Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "skills", ClaudeWorkersPlugin)
}

// claudeMarketplaceJSON lists the one plugin in the local marketplace. An older
// agent-net registered it with Claude Code; a new install does not, but the
// file stays so an old registration still finds its plugin after an upgrade.
const claudeMarketplaceJSON = `{
  "name": "agent-net",
  "owner": {
    "name": "agent-net"
  },
  "description": "Plugins that agent-net setup installs for Claude Code on this machine.",
  "plugins": [
    {
      "name": "agent-net-workers",
      "source": "./agent-net-workers"
    }
  ]
}
`

// ClaudeWorkersFiles lists every file the marketplace and the mod hold, keyed
// by its path below ClaudeWorkersDir.
func ClaudeWorkersFiles() map[string][]byte {
	files, err := claudeWorkersFiles(agentnetworkers.Files())
	if err != nil {
		return nil
	}
	return files
}

func claudeWorkersFiles(pluginFiles map[string][]byte) (map[string][]byte, error) {
	manifest, err := versionedClaudeWorkersManifest(pluginFiles)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		filepath.ToSlash(filepath.Join(".claude-plugin", "marketplace.json")): []byte(claudeMarketplaceJSON),
	}
	for name, data := range pluginFiles {
		if name == claudeWorkersPluginManifest {
			data = manifest
		}
		if name == "hooks/register.tsx" {
			var fields struct {
				Version string `json:"version"`
			}
			json.Unmarshal(manifest, &fields)
			data = []byte(strings.ReplaceAll(string(data), "__AGENT_NET_PANEL_VERSION__", fields.Version))
		}
		files[filepath.ToSlash(filepath.Join(ClaudeWorkersPlugin, filepath.FromSlash(name)))] = data
	}
	return files, nil
}

func versionedClaudeWorkersManifest(pluginFiles map[string][]byte) ([]byte, error) {
	manifest, ok := pluginFiles[claudeWorkersPluginManifest]
	if !ok {
		return nil, fmt.Errorf("%s is missing", claudeWorkersPluginManifest)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &fields); err != nil {
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}
	var baseVersion string
	if err := json.Unmarshal(fields["version"], &baseVersion); err != nil || baseVersion == "" {
		if err == nil {
			err = fmt.Errorf("version is empty")
		}
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}

	h := sha256.New()
	hashClaudeWorkersFiles(h, pluginFiles)
	version, err := json.Marshal(baseVersion + "+" + hex.EncodeToString(h.Sum(nil)[:8]))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}
	fields["version"] = version
	b, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}
	return append(b, '\n'), nil
}

func hashClaudeWorkersFiles(h hash.Hash, pluginFiles map[string][]byte) {
	names := make([]string, 0, len(pluginFiles))
	for name := range pluginFiles {
		if name != claudeWorkersPluginManifest {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		writeHashField(h, []byte(name))
		writeHashField(h, pluginFiles[name])
	}
}

func writeHashField(h hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

// WriteClaudeWorkers writes the marketplace and the mod files, creating the
// folder when it is not there. changed says whether any file was written.
func WriteClaudeWorkers() (changed bool, err error) {
	exe, _ := os.Executable()
	return WriteClaudeWorkersAt(exe)
}

// WriteClaudeWorkersAt records the binary and the standard install fallback.
func WriteClaudeWorkersAt(exe string) (changed bool, err error) {
	files, err := claudeWorkersFiles(agentnetworkers.Files())
	if err != nil {
		return false, err
	}
	paths := []string{}
	if filepath.IsAbs(exe) {
		paths = append(paths, exe)
	}
	paths = append(paths, filepath.Join(home(), ".local", "bin", "agent-net"))
	encoded, _ := json.Marshal(paths)
	quoted, _ := json.Marshal(string(encoded))
	register := filepath.ToSlash(filepath.Join(ClaudeWorkersPlugin, "hooks", "register.tsx"))
	files[register] = []byte(strings.ReplaceAll(string(files[register]), "'__AGENT_NET_BINARY_PATHS__'", string(quoted)))
	for name, data := range files {
		c, _, err := WriteIfChanged(filepath.Join(ClaudeWorkersDir(), filepath.FromSlash(name)), data, 0o644)
		if err != nil {
			return changed, fmt.Errorf("%s: %w", name, err)
		}
		changed = changed || c
	}
	return changed, nil
}

// EnsureClaudeWorkersLink puts the link to the plugin in Claude Code's skills
// folder. A foreign entry with that name is never replaced: foreign is set to
// its path and the caller reports it. changed says whether the link was made.
func EnsureClaudeWorkersLink() (changed bool, foreign string, err error) {
	link := ClaudeWorkersSkillsLink()
	if link == "" {
		return false, "", fmt.Errorf("no Claude Code config directory")
	}
	dir := ClaudeWorkersPluginDir()
	st, lerr := os.Lstat(link)
	switch {
	case lerr == nil && st.Mode()&os.ModeSymlink != 0:
		target, rerr := os.Readlink(link)
		if rerr != nil {
			return false, "", rerr
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(link), target)
		}
		if filepath.Clean(target) == filepath.Clean(dir) {
			if _, serr := os.Stat(filepath.Join(dir, claudeWorkersPluginManifest)); serr == nil {
				return false, "", nil
			}
			// Our link points at a folder that is gone or incomplete. Replace it
			// after the files are written.
		} else {
			return false, link, nil
		}
	case lerr == nil:
		return false, link, nil
	case !os.IsNotExist(lerr):
		return false, "", lerr
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return false, "", err
	}
	if st != nil {
		if err := os.Remove(link); err != nil {
			return false, "", err
		}
	}
	if err := os.Symlink(dir, link); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// RemoveClaudeWorkersLink removes the link only when it is agent-net's. A
// foreign entry is never removed; foreign is set to its path.
func RemoveClaudeWorkersLink() (removed bool, foreign string, err error) {
	link := ClaudeWorkersSkillsLink()
	if link == "" {
		return false, "", nil
	}
	st, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if st.Mode()&os.ModeSymlink == 0 {
		return false, link, nil
	}
	target, err := os.Readlink(link)
	if err != nil {
		return false, "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	if filepath.Clean(target) != filepath.Clean(ClaudeWorkersPluginDir()) {
		return false, link, nil
	}
	if err := os.Remove(link); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// ClaudeWorkersStatus is what Claude Code can load right now. It is read from
// the files and the user settings file, with no claude call, so a daemon start
// does not wait for one.
type ClaudeWorkersStatus struct {
	Link             string // ClaudeWorkersSkillsLink
	LinkOK           bool   // the link is agent-net's and its plugin files exist
	Foreign          string // a foreign entry has the link's name
	Disabled         bool   // the user turned the plugin off in Claude Code
	OldEnabled       bool   // the old marketplace copy is enabled
	OldInstalled     bool   // the old marketplace copy is in Claude's installed list
	OldMarketplace   bool   // the old marketplace is still registered
	OldRegistered    bool   // the old marketplace or plugin entry is present
	SettingsLinked   bool   // settings.json is a link, so agent-net does not write it
	SettingsWritable bool   // agent-net may use the claude CLI on settings.json
}

// OldRuns says the old marketplace copy is the one Claude Code loads.
func (s ClaudeWorkersStatus) OldRuns() bool { return s.OldEnabled && s.OldInstalled }

// Live reports whether one of agent-net's copies is enabled. An enabled old
// entry that is not installed can still shadow the skills copy, so it is not
// counted as live.
func (s ClaudeWorkersStatus) Live() bool {
	return s.OldRuns() || (s.LinkOK && !s.Disabled)
}

// ClaudeWorkersStatusNow reads the live state. It never writes anything.
func ClaudeWorkersStatusNow() ClaudeWorkersStatus {
	s := ClaudeWorkersStatus{Link: ClaudeWorkersSkillsLink()}
	if s.Link == "" {
		return s
	}
	if st, err := os.Lstat(s.Link); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(s.Link); err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(s.Link), target)
				}
				if filepath.Clean(target) == filepath.Clean(ClaudeWorkersPluginDir()) {
					_, err := os.Stat(filepath.Join(ClaudeWorkersPluginDir(), claudeWorkersPluginManifest))
					s.LinkOK = err == nil
				}
			}
		} else {
			s.Foreign = s.Link
		}
	}
	settings := readClaudeWorkersSettings()
	s.Disabled = settings.skillsDisabled
	s.OldEnabled = settings.oldEnabled
	s.OldInstalled = claudeWorkersOldInstalled()
	s.OldMarketplace = settings.oldMarketplace
	s.OldRegistered = settings.oldEnabled || settings.oldMarketplace
	s.SettingsLinked = settings.linked
	s.SettingsWritable = settings.writable
	return s
}

// ClaudeWorkersLiveInstalled reports whether Claude Code can show one of
// agent-net's copies now. It replaces the old record that said installed after
// the plugin had been turned off.
func ClaudeWorkersLiveInstalled() bool { return ClaudeWorkersStatusNow().Live() }

type claudeWorkersSettings struct {
	skillsDisabled bool
	oldEnabled     bool
	oldMarketplace bool
	linked         bool
	writable       bool
}

// readClaudeWorkersSettings reads only the two keys this feature cares about.
// A missing file is writable and has no entries. A file that is not valid JSON
// is reported as neither enabled nor writable, so agent-net leaves it alone.
func readClaudeWorkersSettings() claudeWorkersSettings {
	var s claudeWorkersSettings
	path := claudesettings.File()
	if path == "" {
		return s
	}
	if st, err := os.Lstat(path); err == nil {
		s.linked = st.Mode()&os.ModeSymlink != 0
		s.writable = !s.linked && st.Mode().IsRegular() && st.Mode().Perm()&0o200 != 0
	} else if os.IsNotExist(err) {
		s.writable = true
	} else {
		return s
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var doc struct {
		EnabledPlugins         map[string]json.RawMessage `json:"enabledPlugins"`
		ExtraKnownMarketplaces map[string]json.RawMessage `json:"extraKnownMarketplaces"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		s.writable = false
		return s
	}
	if raw, ok := doc.EnabledPlugins[ClaudeWorkersSkillsID]; ok {
		var on bool
		if json.Unmarshal(raw, &on) == nil && !on {
			s.skillsDisabled = true
		}
	}
	if raw, ok := doc.EnabledPlugins[ClaudeWorkersOldID]; ok {
		var on bool
		if json.Unmarshal(raw, &on) == nil && on {
			s.oldEnabled = true
		}
	}
	_, s.oldMarketplace = doc.ExtraKnownMarketplaces[ClaudeWorkersMarketplace]
	return s
}

// MigrateClaudeWorkers removes the old marketplace plugin after the skills
// link is in place, but only when the settings file is agent-net's to write.
// It returns a line for the log when it did not migrate or when the removal
// failed. An empty line means nothing needed doing or the move succeeded.
func MigrateClaudeWorkers() string {
	if !ClaudeWorkersStatusNow().LinkOK {
		if s := readClaudeWorkersSettings(); s.oldEnabled || s.oldMarketplace {
			return "the old marketplace plugin stays: the skills link is not in place"
		}
		return ""
	}
	return removeOldClaudeWorkers(true)
}

// RemoveOldClaudeWorkers removes the old marketplace registration when the
// settings file is agent-net's to write. It is the off switch, so it does not
// need the skills link.
func RemoveOldClaudeWorkers() string { return removeOldClaudeWorkers(false) }

func removeOldClaudeWorkers(requireSkills bool) string {
	s := readClaudeWorkersSettings()
	if !s.oldEnabled && !s.oldMarketplace {
		return ""
	}
	if s.linked || !s.writable {
		return "the old marketplace plugin stays: " + DisplayPath(claudesettings.File()) + " is not agent-net's to write"
	}
	claude := ClaudePath()
	if claude == "" {
		return "the old marketplace plugin stays: claude not found"
	}
	var failures []string
	if requireSkills {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		list := exec.CommandContext(ctx, claude, "plugin", "list", "--json")
		list.Dir = home()
		out, err := list.Output()
		cancel()
		if err != nil || !strings.Contains(string(out), ClaudeWorkersSkillsID) {
			return "the old marketplace plugin stays: Claude Code does not list " + ClaudeWorkersSkillsID + " yet"
		}
	}
	for _, args := range [][]string{
		{"plugin", "uninstall", ClaudeWorkersOldID, "-s", "user"},
		{"plugin", "marketplace", "remove", ClaudeWorkersMarketplace},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, claude, args...)
		cmd.Dir = home()
		if out, err := cmd.CombinedOutput(); err != nil {
			line := strings.TrimSpace(string(out))
			if line == "" {
				line = err.Error()
			}
			failures = append(failures, "claude "+strings.Join(args, " ")+": "+strings.SplitN(line, "\n", 2)[0])
		}
		cancel()
	}
	if s := readClaudeWorkersSettings(); s.oldEnabled || s.oldMarketplace {
		if len(failures) == 0 {
			failures = append(failures, "the old marketplace entries are still in "+DisplayPath(claudesettings.File()))
		}
		return strings.Join(failures, "; ")
	}
	if requireSkills && !ClaudeWorkersStatusNow().LinkOK {
		return "the old marketplace plugin was removed, but the skills link is not in place"
	}
	return ""
}

// ClaudeWorkersStatePath is the setup record shared with the daemon sync.
func ClaudeWorkersStatePath() string {
	d := os.Getenv("AGENT_NET_CONFIG_DIR")
	if d == "" {
		d = os.Getenv("XDG_CONFIG_HOME")
		if d == "" {
			d = filepath.Join(home(), ".config")
		}
		d = filepath.Join(d, "agent-net")
	}
	return filepath.Join(d, "claude-workers-mod.json")
}

// ClaudePath finds Claude Code using the same locations as setup.
func ClaudePath() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	for _, p := range []string{filepath.Join(home(), ".local", "bin", "claude"), filepath.Join(home(), ".claude", "local", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return p
		}
	}
	return ""
}

type claudeWorkersState struct {
	Asked     bool   `json:"asked"`
	Installed bool   `json:"installed"`
	Dir       string `json:"dir,omitempty"`
}

func loadClaudeWorkersState() claudeWorkersState {
	var st claudeWorkersState
	if b, err := os.ReadFile(ClaudeWorkersStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

// SyncClaudeWorkers refreshes the plugin files and puts the skills link back
// when the owner's stored answer is yes. It reports a missing or disabled
// window once per sync with the one command that fixes it.
func SyncClaudeWorkers() Result {
	r := Result{Name: "claude workers mod", Path: ClaudeWorkersSkillsLink()}
	if !loadClaudeWorkersState().Installed {
		r.Text = "skipped (not installed)"
		return r
	}
	changed, err := WriteClaudeWorkers()
	r.Changed, r.Err = changed, err
	if err != nil {
		return r
	}
	status := ClaudeWorkersStatusNow()
	// A settings file agent-net may not write keeps the old marketplace entry.
	// While that old copy is the window, keep our own link out of the skills
	// folder, so a lost marketplace registration cannot load both copies.
	if !status.SettingsWritable {
		if status.OldRuns() {
			removed, _, err := RemoveClaudeWorkersLink()
			if err != nil {
				r.Err = err
				return r
			}
			r.Changed = r.Changed || removed
			r.Text = "on (old marketplace copy)"
			r.Note = joinNotes(r.Note, "run agent-net setup --claude-workers-mod to move the worker window to the skills folder")
			return r
		}
		if status.OldEnabled && !status.OldInstalled && status.OldMarketplace {
			r.Text = "off (stale marketplace entry)"
			r.Note = "the worker window is off: remove the old agent-net-workers@agent-net entry from Claude Code settings, then run agent-net setup --claude-workers-mod"
			return r
		}
	}
	linkChanged, foreign, err := EnsureClaudeWorkersLink()
	r.Changed = r.Changed || linkChanged
	if err != nil {
		r.Err = err
		return r
	}
	if foreign != "" {
		r.Text = "off (a foreign entry has the name)"
		r.Note = "the worker window is not loaded: " + DisplayPath(foreign) + " is not agent-net's link; move it away, then run agent-net setup --claude-workers-mod"
		return r
	}
	if r.Changed {
		r.Note = "open Claude Code sessions: run /reload-plugins"
	}
	migrateNote := MigrateClaudeWorkers()
	status = ClaudeWorkersStatusNow()
	if status.OldRuns() {
		if _, _, err := RemoveClaudeWorkersLink(); err != nil {
			r.Err = err
			return r
		}
		r.Text = "on (old marketplace copy)"
		r.Note = joinNotes(r.Note, "run agent-net setup --claude-workers-mod to move the worker window to the skills folder")
		r.Note = joinNotes(r.Note, migrateNote)
		return r
	}
	switch {
	case status.Disabled:
		r.Text = "off (disabled in Claude Code)"
		r.Note = joinNotes(r.Note, "the worker window is off: run claude plugin enable "+ClaudeWorkersSkillsID+" to turn it back on")
	case !status.LinkOK:
		r.Text = "off (link missing)"
		r.Note = joinNotes(r.Note, "the worker window is off: run agent-net setup --claude-workers-mod")
	default:
		r.Text = fileResult(r.Changed, true)
	}
	r.Note = joinNotes(r.Note, migrateNote)
	return r
}

// claudeWorkersOldInstalled reads Claude Code's installed plugin list. The old
// record is a fast file read, not a claude call.
func claudeWorkersOldInstalled() bool {
	dir := claudesettings.Dir()
	if dir == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	var doc struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return false
	}
	raw, ok := doc.Plugins[ClaudeWorkersOldID]
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "[]" && trimmed != "null"
}

func joinNotes(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "; ")
}

// ClaudeWorkersPanelVersion identifies the panel installed by this binary.
func ClaudeWorkersPanelVersion() string {
	manifest, err := versionedClaudeWorkersManifest(agentnetworkers.Files())
	if err != nil {
		return ""
	}
	var fields struct {
		Version string `json:"version"`
	}
	json.Unmarshal(manifest, &fields)
	return fields.Version
}

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
// link stays all the time: Claude Code itself enables only one copy while an
// enabled marketplace copy is installed, so the two copies do not show two
// windows. The daemon restores the link when the owner once said yes.
//
// An older agent-net put the same plugin in a local marketplace and enabled it
// in the user's settings file. That file may belong to another tool (the owner
// keeps his in a dotfiles repository), so this code never writes it when it is
// a link or not writable. The old marketplace entry then stays, and Claude Code
// runs the old copy until the owner moves that entry out. The link is already
// there for the day the old entry is lost, and the daemon refreshes the old
// copy as long as it runs. When the enabled entry alone is lost, the installed
// old copy blocks the skills copy; the daemon removes that installed copy,
// which writes no settings file.

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
	Foreign          string // a non-link entry, or a link to somewhere else, has the link's name
	SkillsOn         bool   // enabledPlugins says the skills copy is on
	SkillsOff        bool   // enabledPlugins says the skills copy is off
	OldEnabled       bool   // enabledPlugins says the old marketplace copy is on
	OldOff           bool   // enabledPlugins says the old marketplace copy is off
	OldInstalled     bool   // the old marketplace copy is in Claude's installed list
	OldVersion       string // the version Claude Code installed of the old copy
	OldMarketplace   bool   // the old marketplace is registered in settings or Claude's own list
	Marketplace      bool   // the settings file registers the old marketplace
	KnownMarketplace bool   // Claude Code's own list registers the old marketplace
	OldRegistered    bool   // the old marketplace or plugin entry is present
	Disabled         bool   // the user turned the window off in Claude Code
	SettingsLinked   bool   // settings.json is a link, so agent-net does not write it
	SettingsWritable bool   // agent-net may use the claude CLI on settings.json
}

// OldRuns says the old marketplace copy is the one Claude Code loads.
func (s ClaudeWorkersStatus) OldRuns() bool { return s.OldEnabled && s.OldInstalled }

// BlockedByInstalledCopy says the old copy is in Claude's installed list and
// its marketplace is known, but its enabled entry is gone. In that state
// Claude Code loads neither copy: the installed old copy shadows the skills
// one, and no entry turns it on. Removing the installed old copy brings the
// skills copy back, and it writes no settings file while the enabled entry is
// still missing.
func (s ClaudeWorkersStatus) BlockedByInstalledCopy() bool {
	return s.LinkOK && !s.OldEnabled && !s.OldOff && s.OldInstalled && s.OldMarketplace
}

// DisabledByUser resolves the two enabledPlugins ids. An explicit false turns
// the window off unless the other id is explicitly on. A missing entry is not
// a disable: it is the state in which the owner's dotfiles lost the entries.
func (s ClaudeWorkersStatus) DisabledByUser() bool {
	return (s.OldOff && !s.SkillsOn) || (s.SkillsOff && !s.OldEnabled)
}

// Live reports whether Claude Code can show one of agent-net's copies now. An
// installed old copy whose enabled entry is lost blocks the skills copy until
// the old copy is uninstalled, so that state is not live.
func (s ClaudeWorkersStatus) Live() bool {
	return s.OldRuns() || (s.LinkOK && !s.DisabledByUser() && !s.BlockedByInstalledCopy())
}

// OldNeedsRefresh says the old marketplace copy runs and Claude Code installed
// a different panel version than this binary writes.
func (s ClaudeWorkersStatus) OldNeedsRefresh() bool {
	return s.OldRuns() && s.OldMarketplace && s.OldVersion != ClaudeWorkersPanelVersion()
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
				} else {
					s.Foreign = s.Link
				}
			}
		} else {
			s.Foreign = s.Link
		}
	}
	settings := readClaudeWorkersSettings()
	s.SkillsOn = settings.skillsOn
	s.SkillsOff = settings.skillsOff
	s.OldEnabled = settings.oldOn
	s.OldOff = settings.oldOff
	s.OldInstalled, s.OldVersion = claudeWorkersOldInstall()
	s.Marketplace = settings.oldMarketplace
	s.KnownMarketplace = claudeWorkersKnownMarketplace()
	s.OldMarketplace = s.Marketplace || s.KnownMarketplace
	s.OldRegistered = settings.oldOn || settings.oldOff || s.OldMarketplace
	s.Disabled = s.DisabledByUser()
	s.SettingsLinked = settings.linked
	s.SettingsWritable = settings.writable
	return s
}

// ClaudeWorkersLiveInstalled reports whether Claude Code can show one of
// agent-net's copies now. It replaces the old record that said installed after
// the plugin had been turned off.
func ClaudeWorkersLiveInstalled() bool { return ClaudeWorkersStatusNow().Live() }

// ClaudeWorkersWarning is the short line the Machines screen and `agent-net
// nodes` show when the owner asked for the window and Claude Code cannot load
// it. It reads files only. It is empty when the window is on, when the owner
// turned it off, and when setup was never asked.
func ClaudeWorkersWarning() string {
	if !loadClaudeWorkersState().Installed {
		return ""
	}
	return ClaudeWorkersProblem(ClaudeWorkersStatusNow())
}

// ClaudeWorkersProblem names the one step that fixes this state, or is empty
// when the window loads or the owner turned it off.
func ClaudeWorkersProblem(s ClaudeWorkersStatus) string {
	if s.Live() {
		return ""
	}
	// Claude Code writes an explicit false for an installed old copy whose
	// enabled entry was lost. That false is not a user choice, and on a linked
	// settings file the owner has to run the removal that lets the skills copy
	// load. A false next to a marketplace entry in settings is the user's own
	// disable and is respected below.
	if s.OldOff && !s.OldEnabled && !s.Marketplace && s.KnownMarketplace && s.OldInstalled {
		return "worker window off: run claude plugin uninstall " + ClaudeWorkersOldID + " -s user"
	}
	if s.DisabledByUser() {
		return ""
	}
	switch {
	case s.Foreign != "":
		return "worker window off: " + DisplayPath(s.Foreign) + " is not agent-net's link; move it away, then run agent-net setup --claude-workers-mod"
	case s.BlockedByInstalledCopy():
		return "worker window off: run claude plugin uninstall " + ClaudeWorkersOldID + " -s user"
	default:
		return "worker window off: run agent-net setup --claude-workers-mod"
	}
}

type claudeWorkersSettings struct {
	skillsOn       bool
	skillsOff      bool
	oldOn          bool
	oldOff         bool
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
	readEnabled := func(id string) (on, off bool) {
		raw, ok := doc.EnabledPlugins[id]
		if !ok {
			return false, false
		}
		var v bool
		if json.Unmarshal(raw, &v) != nil {
			return false, false
		}
		return v, !v
	}
	s.skillsOn, s.skillsOff = readEnabled(ClaudeWorkersSkillsID)
	s.oldOn, s.oldOff = readEnabled(ClaudeWorkersOldID)
	_, s.oldMarketplace = doc.ExtraKnownMarketplaces[ClaudeWorkersMarketplace]
	return s
}

// MigrateClaudeWorkers refreshes the old marketplace copy while it runs, then
// moves or repairs it. It runs in setup and in the daemon's background
// migration, never on the start path. It returns a line for the log when it
// did not finish or a command failed; an empty line means nothing needed doing
// or the work succeeded.
func MigrateClaudeWorkers() string {
	s := ClaudeWorkersStatusNow()
	if s.Link == "" {
		return ""
	}
	if !s.LinkOK {
		if s.OldRegistered {
			return "the old marketplace plugin stays: the skills link is not in place"
		}
		return ""
	}
	var notes []string
	if s.OldNeedsRefresh() {
		notes = append(notes, RefreshClaudeWorkers()...)
		s = ClaudeWorkersStatusNow()
	}
	if s.BlockedByInstalledCopy() {
		// No enabled entry is present, so the uninstall has no settings key to
		// remove. This is safe also when the settings file is a link.
		if note := uninstallOldClaudeWorkers(); note != "" {
			notes = append(notes, note)
		}
		s = ClaudeWorkersStatusNow()
	}
	if s.SettingsWritable {
		if note := removeOldClaudeWorkers(true); note != "" {
			notes = append(notes, note)
		}
	}
	return strings.Join(notes, "; ")
}

// MigrateClaudeWorkersOnce is the daemon's background migration. It records a
// failed run against the binary version, so a broken claude command is tried
// once per version and not at every start. A later version tries again.
func MigrateClaudeWorkersOnce(version string) string {
	st := loadClaudeWorkersMigrateState()
	if st.Failed && st.Version == version {
		return ""
	}
	note := MigrateClaudeWorkers()
	if note == "" {
		if st.Failed {
			os.Remove(claudeWorkersMigrateStatePath())
		}
		return ""
	}
	b, _ := json.MarshalIndent(claudeWorkersMigrateState{Version: version, Failed: true}, "", "  ")
	_ = os.WriteFile(claudeWorkersMigrateStatePath(), append(b, '\n'), 0o600)
	return note
}

type claudeWorkersMigrateState struct {
	Version string `json:"version"`
	Failed  bool   `json:"failed"`
}

func claudeWorkersMigrateStatePath() string {
	return filepath.Join(filepath.Dir(ClaudeWorkersStatePath()), "claude-workers-migrate.json")
}

func loadClaudeWorkersMigrateState() claudeWorkersMigrateState {
	var st claudeWorkersMigrateState
	if b, err := os.ReadFile(claudeWorkersMigrateStatePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

// RefreshClaudeWorkers updates Claude Code's copy of the old marketplace
// plugin. The two commands write Claude Code's own plugin state, not
// settings.json, so they are safe on a settings file that is a link. Each
// failure is returned as one line; both commands are attempted.
func RefreshClaudeWorkers() []string {
	claude := ClaudePath()
	if claude == "" {
		return []string{"the old marketplace plugin was not updated: claude not found"}
	}
	var failures []string
	for _, args := range [][]string{{"plugin", "marketplace", "update", ClaudeWorkersMarketplace}, {"plugin", "update", ClaudeWorkersOldID, "-s", "user"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, claude, args...)
		cmd.Dir = home()
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			line := strings.TrimSpace(string(out))
			if line == "" {
				line = err.Error()
			}
			failures = append(failures, "claude "+strings.Join(args[:len(args)-1], " ")+" failed: "+strings.SplitN(line, "\n", 2)[0])
		}
	}
	return failures
}

// uninstallOldClaudeWorkers removes the old copy from Claude Code's installed
// list when its enabled entry is lost. That entry is missing, so the command
// has no settings key to remove and writes no settings file, also when that
// file is a link.
func uninstallOldClaudeWorkers() string {
	claude := ClaudePath()
	if claude == "" {
		return "worker window off: claude not found"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, claude, "plugin", "uninstall", ClaudeWorkersOldID, "-s", "user")
	cmd.Dir = home()
	out, err := cmd.CombinedOutput()
	cancel()
	if err != nil {
		line := strings.TrimSpace(string(out))
		if line == "" {
			line = err.Error()
		}
		return "worker window off: claude plugin uninstall " + ClaudeWorkersOldID + " -s user failed: " + strings.SplitN(line, "\n", 2)[0]
	}
	if installed, _ := claudeWorkersOldInstall(); installed {
		return "worker window off: the old installed copy is still there"
	}
	return ""
}

// RemoveOldClaudeWorkers removes the old marketplace registration when the
// settings file is agent-net's to write. It is the off switch, so it does not
// need the skills link.
func RemoveOldClaudeWorkers() string { return removeOldClaudeWorkers(false) }

func removeOldClaudeWorkers(requireSkills bool) string {
	s := readClaudeWorkersSettings()
	if !s.oldOn && !s.oldMarketplace {
		return ""
	}
	// An explicit false is the user's own disable. Removing the installed copy
	// would let the skills copy load and bring the window back against it.
	if s.oldOff && !s.oldOn {
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
	if s := readClaudeWorkersSettings(); s.oldOn || s.oldMarketplace {
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
// when the owner's stored answer is yes. It writes files only: the Claude
// commands that move or refresh an old marketplace copy run off the start path,
// in MigrateClaudeWorkersOnce. A missing or blocked window is reported once per
// state change, with the one command that fixes it.
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
	linkChanged, _, err := EnsureClaudeWorkersLink()
	r.Changed = r.Changed || linkChanged
	if err != nil {
		r.Err = err
		return r
	}
	if r.Changed {
		r.Note = "open Claude Code sessions: run /reload-plugins"
	}
	status := ClaudeWorkersStatusNow()
	switch {
	case status.Foreign != "":
		r.Text = "off (a foreign entry has the name)"
	case status.OldRuns():
		r.Text = "on (old marketplace copy)"
	case status.Disabled:
		r.Text = "off (disabled in Claude Code)"
	case status.BlockedByInstalledCopy():
		r.Text = "off (the old installed copy blocks the skills plugin)"
	case !status.LinkOK:
		r.Text = "off (link missing)"
	default:
		r.Text = fileResult(r.Changed, true)
	}
	key := claudeWorkersNoticeKey(status)
	if key != loadClaudeWorkersNotice().State {
		r.Note = joinNotes(r.Note, ClaudeWorkersProblem(status))
		saveClaudeWorkersNotice(key)
	}
	return r
}

func claudeWorkersNoticeKey(s ClaudeWorkersStatus) string {
	switch {
	case s.Foreign != "":
		return "foreign"
	case s.Live():
		return "on"
	case s.OldOff && !s.Marketplace && s.KnownMarketplace && s.OldInstalled:
		return "off-lost"
	case s.DisabledByUser():
		return "off-by-user"
	case s.BlockedByInstalledCopy():
		return "blocked"
	case !s.LinkOK:
		return "missing"
	}
	return "off"
}

type claudeWorkersNotice struct {
	State string `json:"state"`
}

func claudeWorkersNoticePath() string {
	return filepath.Join(filepath.Dir(ClaudeWorkersStatePath()), "claude-workers-notice.json")
}

func loadClaudeWorkersNotice() claudeWorkersNotice {
	var st claudeWorkersNotice
	if b, err := os.ReadFile(claudeWorkersNoticePath()); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func saveClaudeWorkersNotice(state string) {
	b, _ := json.MarshalIndent(claudeWorkersNotice{State: state}, "", "  ")
	_ = os.WriteFile(claudeWorkersNoticePath(), append(b, '\n'), 0o600)
}

// RemoveClaudeWorkersRecords removes the daemon's notice and migration
// records. Setup calls it when the owner turns the window off or uninstalls it.
func RemoveClaudeWorkersRecords() {
	os.Remove(claudeWorkersNoticePath())
	os.Remove(claudeWorkersMigrateStatePath())
}

// claudeWorkersOldInstall reads Claude Code's installed plugin list and the
// version it installed of the old copy. The old record is a fast file read,
// not a claude call.
func claudeWorkersOldInstall() (installed bool, version string) {
	dir := claudesettings.Dir()
	if dir == "" {
		return false, ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil {
		return false, ""
	}
	var doc struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return false, ""
	}
	raw, ok := doc.Plugins[ClaudeWorkersOldID]
	if !ok {
		return false, ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" || trimmed == "false" {
		return false, ""
	}
	var entries []struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &entries) == nil {
		for _, e := range entries {
			if e.Version != "" {
				return true, e.Version
			}
		}
	}
	return true, ""
}

// claudeWorkersKnownMarketplace says Claude Code's own list still registers the
// old marketplace. That list is Claude Code's state, not the settings file, so
// it survives a settings loss and decides whether an installed old copy still
// shadows the skills copy.
func claudeWorkersKnownMarketplace() bool {
	dir := claudesettings.Dir()
	if dir == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "known_marketplaces.json"))
	if err != nil {
		return false
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		return false
	}
	_, ok := doc[ClaudeWorkersMarketplace]
	return ok
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

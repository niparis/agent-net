package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// This file closes a project MCP gap between harnesses: the project already
// declares a server for one harness, and an installed harness on the same
// machine does not have it. The plan names every file it would write. Apply
// checks every file again, saves a backup, writes the harness's own format and
// keeps environment references as references. Undo restores what was there.

type coverageTargetState struct {
	target  protocol.MCPCoverageTarget
	content []byte
}

type coveragePlanState struct {
	plan    protocol.MCPCoveragePlan
	targets []coverageTargetState
	at      time.Time
}

var (
	coveragePlansMu sync.Mutex
	coveragePlans   = map[string]*coveragePlanState{}
)

type coverageUndoRecord struct {
	PlanID   string               `json:"plan_id"`
	Project  string               `json:"project"`
	Checkout string               `json:"checkout"`
	Server   string               `json:"server"`
	Targets  []coverageUndoTarget `json:"targets"`
}

type coverageUndoTarget struct {
	Harness  string `json:"harness"`
	Path     string `json:"path"`
	PreHash  string `json:"pre_hash"`
	PostHash string `json:"post_hash"`
	BackupID string `json:"backup_id"`
	Created  bool   `json:"created"`
}

func coveragePlanID() string { return protocol.NewID("cplan") }

// pruneCoveragePlans drops plans that were shown but not applied for an hour.
func pruneCoveragePlans() {
	cut := time.Now().Add(-time.Hour)
	coveragePlansMu.Lock()
	defer coveragePlansMu.Unlock()
	for id, p := range coveragePlans {
		if p.at.Before(cut) {
			delete(coveragePlans, id)
		}
	}
}

func coverageFormat(path string) string {
	if strings.HasSuffix(path, ".jsonc") {
		return "jsonc"
	}
	if strings.HasSuffix(path, ".toml") {
		return "toml"
	}
	return "json"
}

func coverageSummary(harness string, desc map[string]any) string {
	command, _ := desc["command"].(string)
	if list, ok := desc["command"].([]any); ok && len(list) > 0 {
		command, _ = list[0].(string)
	}
	if command != "" {
		args := 0
		if a, ok := desc["args"].([]any); ok {
			args = len(a)
		}
		if a, ok := desc["command"].([]any); ok {
			args += max(len(a)-1, 0)
		}
		line := "command " + command
		if args > 0 {
			line += ", " + plural(args, "argument")
		}
		envField := "env"
		if harness == "opencode" {
			envField = "environment"
		}
		if m, ok := desc[envField].(map[string]any); ok && len(m) > 0 {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			line += ", environment " + strings.Join(keys, ", ")
		}
		return line
	}
	if u, _ := desc["url"].(string); u != "" {
		if i := strings.Index(u, "?"); i >= 0 {
			u = u[:i]
		}
		return "url " + u
	}
	return "declaration"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// mcpCoveragePlan plans one server for the harnesses that do not have it. It
// re-reads the project, so the declaration and every file are current.
func (d *Daemon) mcpCoveragePlan(p protocol.MCPCoverageRequest) protocol.MCPCoveragePlan {
	plan := protocol.MCPCoveragePlan{ID: coveragePlanID(), NodeID: d.cfg.NodeID, Machine: d.cfg.Hostname,
		Project: p.Project, Server: p.Server, SourceHarness: p.SourceHarness, SourcePath: p.SourcePath,
		State: "conflict", Actor: "owner", At: time.Now().UnixNano(), Targets: []protocol.MCPCoverageTarget{}}
	fail := func(why string) protocol.MCPCoveragePlan {
		plan.Conflicts = append(plan.Conflicts, why)
		return plan
	}
	if d.cfg.InventoryHome == "" || d.cfg.StateDir == "" {
		return fail("inventory scanner is disabled")
	}
	if p.Project == "" || p.Server == "" || len(p.Harnesses) == 0 {
		return fail("project, server and at least one harness are needed")
	}
	checkout := filepath.Clean(p.Checkout)
	if checkout == "" || checkout == "." {
		d.toolsMu.Lock()
		for _, item := range d.toolsReport.Items {
			if item.Kind == protocol.InventoryMCP && item.Scope == protocol.InventoryProject && item.Project == p.Project && item.Name == p.Server && item.ProjectRoot != "" {
				checkout = filepath.Clean(item.ProjectRoot)
				break
			}
		}
		d.toolsMu.Unlock()
	}
	if checkout == "" || checkout == "." || !filepath.IsAbs(checkout) {
		return fail("project checkout is unavailable")
	}
	plan.Checkout = checkout

	report := inventory.Scan(inventory.Options{Home: d.cfg.InventoryHome, CodexHome: d.cfg.CodexHome, Machine: d.cfg.Hostname,
		Projects:        []protocol.ProjectEntry{{Name: p.Project, Path: checkout}},
		TrustedProjects: map[string]bool{checkout: true}})
	observed := map[string]protocol.InventoryItem{}
	var source *protocol.InventoryItem
	rank := func(h string) int {
		for i, v := range []string{"claude", "pi", "opencode", "codex"} {
			if v == h {
				return i
			}
		}
		return 4
	}
	for i := range report.Items {
		item := report.Items[i]
		if item.Kind != protocol.InventoryMCP || item.Scope != protocol.InventoryProject || item.Project != p.Project || item.Name != p.Server {
			continue
		}
		if filepath.Clean(item.ProjectRoot) != checkout {
			continue
		}
		observed[item.Harness] = item
		if p.SourceHarness != "" {
			if item.Harness == p.SourceHarness && (p.SourcePath == "" || item.Location == p.SourcePath) {
				source = &item
			}
		} else if source == nil || rank(item.Harness) < rank(source.Harness) {
			source = &item
		}
	}
	if source == nil {
		return fail("the project server is no longer observed")
	}
	plan.SourceHarness, plan.SourcePath = source.Harness, source.Location
	sourceDesc, info, err := inventory.LoadNativeDescriptor(d.nativeOptions(), source.Harness, protocol.InventoryProject, checkout, source.Location, p.Server)
	if err != nil {
		return fail("the source declaration cannot be read again")
	}
	if info.Class != protocol.MCPImportable {
		reason := info.Reason
		if reason == "" {
			reason = "the declaration cannot be mapped to another harness"
		}
		return fail("cannot add " + p.Server + ": " + reason)
	}

	var targets []coverageTargetState
	conflicts := []string{}
	seen := map[string]bool{}
	for _, harness := range p.Harnesses {
		if harness == "" || seen[harness] {
			continue
		}
		seen[harness] = true
		t := protocol.MCPCoverageTarget{Harness: harness, State: "pending"}
		if harness == source.Harness {
			t.State, t.Conflict = "conflict", "the source harness already has it"
			targets = append(targets, coverageTargetState{target: t})
			continue
		}
		path, key, tomlFormat, err := inventory.ProjectMCPFile(harness, checkout)
		if err != nil {
			t.State, t.Conflict = "conflict", err.Error()
			targets = append(targets, coverageTargetState{target: t})
			continue
		}
		t.Path, t.Key, t.Format = path, key, coverageFormat(path)
		if _, exists := observed[harness]; exists {
			t.State, t.Conflict = "conflict", "already declared for "+harness
			targets = append(targets, coverageTargetState{target: t})
			continue
		}
		data, _, readErr := inventory.ReadNativeFile(checkout, path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			t.State, t.Conflict = "conflict", "the file cannot be read"
			targets = append(targets, coverageTargetState{target: t})
			continue
		}
		if readErr == nil {
			t.Action, t.PreHash = "update", sumHex(data)
		} else {
			t.Action = "create"
			data = nil
		}
		mapped, err := inventory.ProjectMCPDescriptor(harness, sourceDesc)
		if err != nil {
			t.State, t.Conflict = "conflict", err.Error()
			targets = append(targets, coverageTargetState{target: t})
			continue
		}
		out, err := inventory.AddProjectMCPEntry(checkout, path, key, tomlFormat, p.Server, mapped, data)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				t.State, t.Conflict = "conflict", "already declared for "+harness
			} else {
				t.State, t.Conflict = "conflict", err.Error()
			}
			targets = append(targets, coverageTargetState{target: t})
			continue
		}
		t.PostHash = sumHex(out)
		t.Preview = []string{
			path,
			"  " + t.Action + " " + p.Server + ": " + coverageSummary(harness, mapped),
		}
		if harness == "pi" || harness == "codex" {
			t.Preview = append(t.Preview, "  this harness reads the file only in a trusted project")
		}
		if t.Action == "update" {
			t.Preview = append(t.Preview, "  the rest of the file stays as it is")
		}
		targets = append(targets, coverageTargetState{target: t, content: out})
	}
	plan.Targets = make([]protocol.MCPCoverageTarget, len(targets))
	for i := range targets {
		plan.Targets[i] = targets[i].target
		if targets[i].target.Conflict != "" {
			conflicts = append(conflicts, targets[i].target.Harness+": "+targets[i].target.Conflict)
		}
	}
	if len(conflicts) > 0 {
		plan.Conflicts = append(plan.Conflicts, conflicts...)
		return plan
	}
	if len(targets) == 0 {
		return fail("nothing to add")
	}
	plan.State = "pending"
	pruneCoveragePlans()
	coveragePlansMu.Lock()
	coveragePlans[plan.ID] = &coveragePlanState{plan: plan, targets: targets, at: time.Now()}
	coveragePlansMu.Unlock()
	return plan
}

// currentCoverageTarget checks that a file still holds the bytes the plan saw.
func currentCoverageTarget(base string, t protocol.MCPCoverageTarget) ([]byte, os.FileMode, string) {
	data, mode, err := inventory.ReadNativeFile(base, t.Path)
	if t.PreHash == "" {
		if err == nil {
			return nil, 0, "changed since plan"
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, 0, "the file cannot be read"
		}
		return nil, 0, ""
	}
	if err != nil {
		return nil, 0, "the file is gone or cannot be read"
	}
	if sumHex(data) != t.PreHash {
		return nil, 0, "changed since plan"
	}
	return data, mode, ""
}

func (d *Daemon) mcpCoverageApply(planID string) protocol.MCPCoverageResult {
	res := protocol.MCPCoverageResult{PlanID: planID, State: "conflict", Targets: []protocol.MCPCoverageTarget{}}
	coveragePlansMu.Lock()
	state := coveragePlans[planID]
	coveragePlansMu.Unlock()
	if state == nil {
		res.Targets = append(res.Targets, protocol.MCPCoverageTarget{State: "conflict", Conflict: "plan not found or expired"})
		return res
	}
	if state.plan.State != "pending" {
		res.Targets = append(res.Targets, protocol.MCPCoverageTarget{State: "conflict", Conflict: "the plan has conflicts"})
		return res
	}
	// Check every target before writing one, so a change on one file cannot
	// leave the other harnesses half done.
	for _, t := range state.targets {
		if _, _, why := currentCoverageTarget(state.plan.Checkout, t.target); why != "" {
			c := t.target
			c.State, c.Conflict = "conflict", why
			res.Targets = append(res.Targets, c)
		}
	}
	if len(res.Targets) > 0 {
		return res
	}
	undo := coverageUndoRecord{PlanID: planID, Project: state.plan.Project, Checkout: state.plan.Checkout, Server: state.plan.Server}
	for _, t := range state.targets {
		c := t.target
		cur, mode, why := currentCoverageTarget(state.plan.Checkout, c)
		if why != "" {
			c.State, c.Conflict = "conflict", why
			res.Targets = append(res.Targets, c)
			continue
		}
		j, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "mcp-coverage", Target: c.Path, BeforeHash: c.PreHash, AfterHash: c.PostHash, Item: state.plan.Server, Step: "prepared"})
		if err != nil {
			c.State, c.Conflict = "failed", "cannot write the operation journal"
			res.Targets = append(res.Targets, c)
			continue
		}
		item := sumHex([]byte(c.Path + "\x00" + state.plan.Server))
		id, stamp, err := d.writeNativeBackup(item, cur, nativeMeta{Harness: c.Harness, Scope: protocol.InventoryProject, Root: state.plan.Checkout, Location: c.Path, Name: state.plan.Server, PreHash: c.PreHash, PostHash: c.PostHash})
		if err != nil {
			_ = j.finish()
			c.State, c.Conflict = "failed", "cannot save a backup; the file was not changed"
			res.Targets = append(res.Targets, c)
			continue
		}
		j.record.Backup = filepath.Join(stamp, "preimage")
		if err := j.advance("backed_up"); err != nil {
			_ = j.finish()
			c.State, c.Conflict = "failed", "operation interrupted; the file was not changed"
			res.Targets = append(res.Targets, c)
			continue
		}
		if why := writeCoverageFile(state.plan.Checkout, c.Path, t.content, c.PreHash, mode); why != "" {
			_ = j.finish()
			c.State, c.Conflict = "conflict", why
			res.Targets = append(res.Targets, c)
			continue
		}
		_ = j.advance("edited")
		_ = j.finish()
		c.State = "applied"
		c.PostHash = sumHex(t.content)
		res.Targets = append(res.Targets, c)
		undo.Targets = append(undo.Targets, coverageUndoTarget{Harness: c.Harness, Path: c.Path, PreHash: c.PreHash, PostHash: c.PostHash, BackupID: id, Created: c.PreHash == ""})
	}
	applied := 0
	failed := 0
	for _, t := range res.Targets {
		if t.State == "applied" {
			applied++
		} else {
			failed++
		}
	}
	if failed > 0 {
		res.State = "conflict"
		// Put back the files this call already wrote.
		_ = d.restoreCoverage(undo)
		return res
	}
	if applied == 0 {
		res.State = "conflict"
		return res
	}
	res.State = "applied"
	if err := d.writeCoverageUndo(undo); err != nil {
		res.State = "failed"
	}
	d.wakeTools()
	return res
}

func (d *Daemon) writeCoverageUndo(rec coverageUndoRecord) error {
	if !safeCoverageID(rec.PlanID) {
		return errors.New("invalid plan")
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := filepath.Join(d.cfg.StateDir, "mcp-coverage-undo-"+rec.PlanID+".json")
	tmp, err := os.CreateTemp(d.cfg.StateDir, ".mcp-coverage-undo-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func safeCoverageID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// writeCoverageFile writes planned bytes and verifies the result. An empty
// preHash means the file must not exist yet.
func writeCoverageFile(base, path string, data []byte, preHash string, mode os.FileMode) string {
	if !safePathForCoverage(base, path) {
		return "the destination is unsafe"
	}
	if preHash == "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "cannot create the folder"
		}
		if !safePathForCoverage(base, path) {
			return "the destination is unsafe"
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if os.IsExist(err) {
				return "changed since plan"
			}
			return "cannot create the file"
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		if e := f.Close(); err == nil {
			err = e
		}
		if err != nil {
			return "cannot write the file"
		}
	} else {
		if mode == 0 {
			mode = 0o600
		}
		if why := replaceNative(path, mode, data, preHash); why != "" {
			if why == "changed since plan" {
				return "changed since plan"
			}
			return why
		}
	}
	got, _, err := inventory.ReadNativeFile(base, path)
	if err != nil || !bytesEqual(got, data) {
		return "changed after the edit; the file was kept as it is"
	}
	return ""
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func safePathForCoverage(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	cur := base
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return errors.Is(err, os.ErrNotExist)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func (d *Daemon) mcpCoverageUndo(planID string) protocol.MCPCoverageResult {
	res := protocol.MCPCoverageResult{PlanID: planID, State: "conflict", Targets: []protocol.MCPCoverageTarget{}}
	if !safeCoverageID(planID) {
		res.Targets = append(res.Targets, protocol.MCPCoverageTarget{State: "conflict", Conflict: "invalid plan"})
		return res
	}
	path := filepath.Join(d.cfg.StateDir, "mcp-coverage-undo-"+planID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		res.Targets = append(res.Targets, protocol.MCPCoverageTarget{State: "conflict", Conflict: "the local undo record is gone"})
		return res
	}
	var rec coverageUndoRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.PlanID != planID {
		res.Targets = append(res.Targets, protocol.MCPCoverageTarget{State: "conflict", Conflict: "the local undo record does not match"})
		return res
	}
	if why := d.restoreCoverage(rec); why != "" {
		res.Targets = append(res.Targets, protocol.MCPCoverageTarget{State: "conflict", Conflict: why})
		return res
	}
	_ = os.Remove(path)
	res.State = "applied"
	d.wakeTools()
	return res
}

func (d *Daemon) restoreCoverage(rec coverageUndoRecord) string {
	for i := len(rec.Targets) - 1; i >= 0; i-- {
		t := rec.Targets[i]
		expected, key, _, err := inventory.ProjectMCPFile(t.Harness, rec.Checkout)
		if err != nil || filepath.Clean(expected) != filepath.Clean(t.Path) || key == "" {
			return "the undo record names an unexpected file"
		}
		if !safePathForCoverage(rec.Checkout, t.Path) {
			return "the undo record names an unsafe file"
		}
		if t.Created {
			data, _, err := inventory.ReadNativeFile(rec.Checkout, t.Path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || sumHex(data) != t.PostHash {
				return "changed since apply"
			}
			if err := os.Remove(t.Path); err != nil {
				return "cannot remove the file"
			}
			continue
		}
		stamp := filepath.Join(d.cfg.StateDir, mcpBackupDir, filepath.FromSlash(t.BackupID))
		pre, err := os.ReadFile(filepath.Join(stamp, "preimage"))
		if err != nil || sumHex(pre) != t.PreHash {
			return "the local backup is gone or does not match"
		}
		cur, mode, err := inventory.ReadNativeFile(rec.Checkout, t.Path)
		if err != nil || sumHex(cur) != t.PostHash {
			return "changed since apply"
		}
		if why := replaceNative(t.Path, mode, pre, t.PostHash); why != "" {
			return why
		}
	}
	return ""
}

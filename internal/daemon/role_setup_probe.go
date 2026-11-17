package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func (d *Daemon) setupRoleModels(ctx context.Context) (protocol.RoleSetupResult, error) {
	var result protocol.RoleSetupResult
	raw, err := d.call(ctx, "settings.snapshot", nil)
	if err != nil {
		return result, err
	}
	snapshot, err := d.cleanSettingsSnapshot(raw)
	if err != nil {
		return result, err
	}
	// Only a network with no CTO, no chief and no merger is new. The gateway
	// decides it, not this machine's own state: another machine may already
	// run the owner's CTO.
	rawNetwork, err := d.call(ctx, protocol.OpRoleNetwork, nil)
	if err != nil {
		return result, err
	}
	var network protocol.RoleNetwork
	if err := json.Unmarshal(rawNetwork, &network); err != nil {
		return result, err
	}
	if !network.New() {
		return existingNetworkRoles(snapshot, network), nil
	}
	missing := setupRoleChanges(snapshot, []protocol.RoleModel{{Harness: "codex"}})
	missingKeys := map[string]bool{}
	for _, change := range missing {
		missingKeys[change.Key] = true
	}
	if missingKeys[protocol.RoleModelsKey(protocol.RoleCTO)] {
		chosen, err := d.setupCTOAlreadyChosen(snapshot)
		if err != nil {
			return result, err
		}
		if chosen {
			delete(missingKeys, protocol.RoleModelsKey(protocol.RoleCTO))
		}
	}
	for _, role := range []string{protocol.RoleCTO, protocol.RoleChief, protocol.RoleMerger} {
		if !missingKeys[protocol.RoleModelsKey(role)] {
			result.Kept = append(result.Kept, role)
		}
	}
	if len(missingKeys) == 0 {
		return result, nil
	}
	installed, generation := d.inventory()
	if generation == 0 {
		installed = d.refreshHarnessInventory(ctx)
	}
	result.Models, result.Failures = chooseSetupRoleModels(ctx, installed, d.readHarnessModels, d.probeSetupRoleModel)
	var changes []protocol.SettingsChange
	for _, change := range setupRoleChanges(snapshot, result.Models) {
		if missingKeys[change.Key] {
			changes = append(changes, change)
		}
	}
	if len(changes) == 0 {
		return result, nil
	}
	if _, err := d.call(ctx, "settings.write", protocol.SettingsWrite{Changes: changes, Reason: "Choose usable role harnesses during setup", By: "owner"}); err != nil {
		return result, err
	}
	latest, err := d.call(ctx, "settings.snapshot", nil)
	if err != nil {
		return result, err
	}
	if err := d.acceptSettingsSnapshot(latest, false); err != nil {
		return result, err
	}
	for _, change := range changes {
		for _, role := range []string{protocol.RoleCTO, protocol.RoleChief, protocol.RoleMerger} {
			if change.Key == protocol.RoleModelsKey(role) {
				result.Added = append(result.Added, role)
			}
		}
		if change.Key == protocol.RoleModelsKey(protocol.RoleCTO) {
			if _, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{Models: &result.Models}); err != nil {
				return result, fmt.Errorf("save the CTO's role models: %w", err)
			}
		}
	}
	return result, nil
}

// existingNetworkRoles reports the role lists of a network that already has
// role sessions. It changes nothing: an unset list keeps its default, which a
// setup run on a new machine must not replace.
func existingNetworkRoles(snapshot settings.Snapshot, network protocol.RoleNetwork) protocol.RoleSetupResult {
	result := protocol.RoleSetupResult{Existing: true, Defaults: map[string]string{}}
	set := networkSetKeys(snapshot)
	sessions := map[string]bool{
		protocol.RoleCTO: network.CTOSession != "", protocol.RoleChief: network.ChiefSession != "", protocol.RoleMerger: network.Mergers > 0,
	}
	for _, role := range []string{protocol.RoleCTO, protocol.RoleChief, protocol.RoleMerger} {
		key := protocol.RoleModelsKey(role)
		if set[key] {
			result.Kept = append(result.Kept, role)
		} else {
			result.Unset = append(result.Unset, role)
			if list, err := protocol.ParseRoleModels(snapshot.String(key, settings.Where{})); err == nil && len(list) > 0 {
				result.Defaults[role] = list[0].Display()
			}
		}
		if !sessions[role] {
			result.Missing = append(result.Missing, role)
		}
	}
	return result
}

func networkSetKeys(snapshot settings.Snapshot) map[string]bool {
	set := map[string]bool{}
	for _, v := range snapshot.Values {
		if v.Level == "network" && v.Scope == "network" && v.Item == "" {
			set[v.Key] = true
		}
	}
	return set
}

func (d *Daemon) startSetupRoles(ctx context.Context, added []string) error {
	startCTO, startChief := false, false
	for _, role := range added {
		startCTO = startCTO || role == protocol.RoleCTO
		startChief = startChief || role == protocol.RoleChief
	}
	if startCTO && !d.ctoRunning() {
		if err := d.registerCTOSession(ctx); err != nil {
			return fmt.Errorf("start the CTO: %w", err)
		}
		if _, err := d.ctoSend(ctx, "Start up and read your standing orders and notes."); err != nil {
			return fmt.Errorf("start the CTO: %w", err)
		}
	}
	if startChief && d.ctoSessionID() != "" {
		if err := d.ensureChief(ctx); err != nil {
			return fmt.Errorf("start the chief of staff: %w", err)
		}
	}
	return nil
}

// An existing local CTO predates the network list and must not be switched by
// setup just because that new setting has no explicit value.
func (d *Daemon) setupCTOAlreadyChosen(snapshot settings.Snapshot) (bool, error) {
	var harness, active, opencode, claude, codex, models string
	err := d.st.db.QueryRow(`SELECT active_harness, active_model, opencode_model, claude_model, codex_model, models FROM cto WHERE id=1`).
		Scan(&harness, &active, &opencode, &claude, &codex, &models)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if active != "" || opencode != "" || claude != "" || codex != "" || (harness != "" && harness != "opencode") ||
		(models != "" && models != snapshot.String(protocol.RoleModelsKey(protocol.RoleCTO), settings.Where{})) || d.ctoRunning() {
		return true, nil
	}
	var conversations int
	err = d.st.db.QueryRow(`SELECT COUNT(*) FROM session_conversations WHERE session_id=` + ctoKeySQL + ` AND native_id<>''`).Scan(&conversations)
	return conversations > 0, err
}

func setupRoleChanges(snapshot settings.Snapshot, models []protocol.RoleModel) []protocol.SettingsChange {
	if len(models) == 0 {
		return nil
	}
	set := networkSetKeys(snapshot)
	value, _ := json.Marshal(protocol.FormatRoleModels(models))
	var changes []protocol.SettingsChange
	for _, role := range []string{protocol.RoleCTO, protocol.RoleChief, protocol.RoleMerger} {
		key := protocol.RoleModelsKey(role)
		if !set[key] {
			changes = append(changes, protocol.SettingsChange{Key: key, Level: "network", Scope: "network", Value: value})
		}
	}
	return changes
}

// chooseSetupRoleModels probes each installed role harness once, in the
// first-use order. Pi cannot yet resume or wake as a role.
func chooseSetupRoleModels(ctx context.Context, installed []protocol.HarnessInfo, models func(context.Context, string) ([]string, error), probe func(context.Context, protocol.RoleModel) protocol.ModelTestResult) ([]protocol.RoleModel, []string) {
	have := make(map[string]bool, len(installed))
	for _, harness := range installed {
		have[harness.Name] = true
	}
	var usable []protocol.RoleModel
	var failures []string
	for _, harness := range []string{"codex", "opencode", "claude"} {
		if !have[harness] {
			continue
		}
		live, err := models(ctx, harness)
		if err != nil {
			failures = append(failures, harness+": "+err.Error())
			continue
		}
		name := defaultRoleModel(harness, live)
		if name == "" {
			failures = append(failures, harness+": no model is available; sign in")
			continue
		}
		names := []string{name}
		if harness == "opencode" {
			seen := map[string]bool{protocol.ProfileProvider(harness, name): true}
			for _, next := range live {
				provider := protocol.ProfileProvider(harness, next)
				if !seen[provider] {
					seen[provider] = true
					names = append(names, next)
				}
			}
		}
		for _, model := range names {
			candidate := protocol.RoleModel{Harness: harness, Provider: protocol.ProfileProvider(harness, model), Model: model}
			result := probe(ctx, candidate)
			if !result.OK {
				failures = append(failures, harness+" "+model+": "+result.Output)
				continue
			}
			usable = append(usable, candidate)
		}
	}
	return usable, failures
}

// probeSetupRoleModel requires the harness's structured final answer. A clean
// exit can still mean that authentication, quota or a turn failed.
func (d *Daemon) probeSetupRoleModel(ctx context.Context, model protocol.RoleModel) protocol.ModelTestResult {
	bin, err := d.harnessBinary(model.Harness)
	if err != nil {
		return protocol.ModelTestResult{Output: err.Error()}
	}
	args, err := modelTestArgs(ctx, bin, model.Harness, model.Model, model.Effort)
	if err != nil {
		return protocol.ModelTestResult{Output: err.Error()}
	}
	var answerFile string
	var flags []string
	switch model.Harness {
	case "codex":
		f, err := os.CreateTemp("", "agent-net-role-answer-*")
		if err != nil {
			return protocol.ModelTestResult{Output: err.Error()}
		}
		answerFile = f.Name()
		f.Close()
		defer os.Remove(answerFile)
		flags = []string{"--output-last-message", answerFile}
	case "claude":
		flags = []string{"--output-format", "json"}
	case "opencode":
		flags = []string{"--standalone", "--format", "json"}
	default:
		return protocol.ModelTestResult{Output: fmt.Sprintf("%s cannot run a role probe", model.Harness)}
	}
	args = append(append(append([]string{}, args[:len(args)-1]...), flags...), args[len(args)-1])
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	diagnostic := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
	if len(diagnostic) > 2000 {
		diagnostic = diagnostic[len(diagnostic)-2000:]
	}
	if cctx.Err() != nil {
		return protocol.ModelTestResult{Output: "the model probe timed out: " + diagnostic}
	}
	if err != nil {
		if diagnostic == "" {
			diagnostic = err.Error()
		}
		return protocol.ModelTestResult{Output: diagnostic}
	}
	var answer string
	switch model.Harness {
	case "codex":
		data, _ := os.ReadFile(answerFile)
		answer = string(data)
	case "claude":
		var result struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if json.Unmarshal(stdout.Bytes(), &result) == nil && result.Type == "result" && !result.IsError {
			answer = result.Result
		}
	case "opencode":
		for _, line := range strings.Split(stdout.String(), "\n") {
			var event struct {
				Type string `json:"type"`
				Part struct {
					Text string `json:"text"`
				} `json:"part"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Type == "text" {
				answer += event.Part.Text
			}
		}
	}
	if strings.TrimSpace(answer) == "" {
		if diagnostic == "" {
			diagnostic = "the model returned no answer"
		}
		return protocol.ModelTestResult{Output: diagnostic}
	}
	return protocol.ModelTestResult{OK: true, Output: strings.TrimSpace(answer)}
}

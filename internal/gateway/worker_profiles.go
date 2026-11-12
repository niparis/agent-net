package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const workerProfilesKey = "worker_profiles"

func (s *Store) WorkerProfiles() ([]protocol.WorkerProfile, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, workerProfilesKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []protocol.WorkerProfile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []protocol.WorkerProfile
	err = json.Unmarshal([]byte(raw), &profiles)
	return profiles, err
}

func cleanMachines(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || seen[strings.ToLower(m)] {
			continue
		}
		seen[strings.ToLower(m)] = true
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// profileTargets picks the machines a new profile is checked on: the ones it
// is configured for, or every online machine with the harness when it names
// none.
func (g *Gateway) profileTargets(nodes []protocol.NodeView, harness string, machines []string) ([]protocol.NodeView, error) {
	byName := map[string]protocol.NodeView{}
	for _, node := range nodes {
		byName[strings.ToLower(node.Hostname)] = node
		byName[strings.ToLower(node.NodeID)] = node
	}
	var targets []protocol.NodeView
	if len(machines) > 0 {
		seen := map[string]bool{}
		for _, name := range machines {
			node, ok := byName[strings.ToLower(name)]
			if !ok {
				return nil, reject("unknown machine %q", name)
			}
			if seen[node.NodeID] {
				continue
			}
			seen[node.NodeID] = true
			has := false
			for _, h := range node.Harnesses {
				if h.Name == harness {
					has = true
				}
			}
			if !has {
				return nil, reject("%s is not installed on %s", harness, node.Hostname)
			}
			if !g.nodeOnline(node.NodeID) {
				return nil, reject("machine %s is offline, cannot check the model live", node.Hostname)
			}
			targets = append(targets, node)
		}
		return targets, nil
	}
	for _, node := range nodes {
		if !g.nodeOnline(node.NodeID) {
			continue
		}
		for _, h := range node.Harnesses {
			if h.Name == harness {
				targets = append(targets, node)
				break
			}
		}
	}
	if len(targets) == 0 {
		return nil, reject("no online machine has %s, so the model cannot be checked live", harness)
	}
	return targets, nil
}

func withWorkerProfileAvailability(profiles []protocol.WorkerProfile, nodes []protocol.NodeView, scores []protocol.ModelScore, history ...map[string]map[string]bool) []protocol.WorkerProfile {
	histories := map[string]map[string]bool{}
	if len(history) > 0 && history[0] != nil {
		histories = history[0]
	}
	out := append([]protocol.WorkerProfile(nil), profiles...)
	for i := range out {
		var checked []string
		var closest []string
		var machineAvailability []protocol.WorkerProfileMachineAvailability
		seenMachines := map[string]bool{}
		seenClosest := map[string]bool{}
		available := false
		known := false
		configured := map[string]bool{}
		for _, name := range out[i].Machines {
			configured[strings.ToLower(name)] = true
		}
		history := histories[out[i].Name]
		for _, node := range nodes {
			for _, harness := range node.Harnesses {
				if harness.Name != out[i].Harness {
					continue
				}
				machine := node.Hostname
				if machine == "" {
					machine = node.NodeID
				}
				if machine != "" && seenMachines[machine] {
					continue
				}
				inHistory := history[node.NodeID] || history[machine]
				if len(configured) > 0 && !configured[strings.ToLower(machine)] && !configured[strings.ToLower(node.NodeID)] && !inHistory {
					continue
				}
				known = true
				if machine != "" {
					checked = append(checked, machine)
					seenMachines[machine] = true
				}
				resolved, ambiguous := protocol.ResolveWorkerProfileModel(out[i].Selection().ModelID(), harness.Models)
				status := protocol.WorkerProfileMachineAvailability{Machine: machine}
				if node.Online && harness.SignedIn != "no" {
					status.Available = true
					available = true
				}
				if resolved != "" && resolved != out[i].Model {
					status.ResolvedModel = resolved
				} else if resolved == "" {
					status.Ambiguous = ambiguous
					if len(ambiguous) > 0 {
						status.Closest = append([]string(nil), ambiguous...)
					} else {
						status.Closest = protocol.ClosestModels(out[i].Model, harness.Models, 3)
					}
					if len(status.Closest) > 3 {
						status.Closest = status.Closest[:3]
					}
					for _, model := range status.Closest {
						if !seenClosest[model] {
							closest = append(closest, model)
							seenClosest[model] = true
						}
					}
				}
				machineAvailability = append(machineAvailability, status)
			}
		}
		sort.Strings(checked)
		sort.Slice(machineAvailability, func(i, j int) bool {
			return machineAvailability[i].Machine < machineAvailability[j].Machine
		})
		sort.Strings(closest)
		if len(closest) > 3 {
			closest = closest[:3]
		}
		out[i].MachinesChecked = checked
		out[i].MachineAvailability = machineAvailability
		out[i].Available = nil
		out[i].Closest = nil
		if known {
			out[i].Available = &available
			if !available {
				out[i].Closest = closest
			}
		}
		if score, ok := protocol.LookupModelScoreWithDefaultEffort(out[i].Selection().LegacyModel(), out[i].Effort, scores); ok {
			value := score.Score
			out[i].Score = &value
			out[i].ScoreSource = score.Source
		}
	}
	return out
}

// profileSuccessNodes returns the machines where a profile started a worker
// before. Availability uses this history, never a scan of model lists.
func (s *Store) profileSuccessNodes(p protocol.WorkerProfile) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := s.db.Query(`SELECT DISTINCT node_id FROM worker_runs WHERE harness = ? AND model IN (?, ?) AND outcome <> ?`,
		p.Harness, p.Model, p.Selection().LegacyModel(), protocol.OutcomeFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var node string
		if err := rows.Scan(&node); err != nil {
			return nil, err
		}
		out[node] = true
	}
	return out, rows.Err()
}

func (s *Store) saveWorkerProfiles(profiles []protocol.WorkerProfile) error {
	raw, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, workerProfilesKey, string(raw), s.now().UnixNano())
	if err != nil {
		return err
	}
	return s.refreshQuality()
}
func (s *Store) AddWorkerProfile(p protocol.WorkerProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.WorkerProfiles()
	if err != nil {
		return err
	}
	for i, other := range profiles {
		if strings.EqualFold(other.Name, p.Name) {
			p.Name = other.Name
			profiles[i] = p
			return s.saveWorkerProfiles(profiles)
		}
	}
	return s.saveWorkerProfiles(append(profiles, p))
}
func (s *Store) RemoveWorkerProfile(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.WorkerProfiles()
	if err != nil {
		return err
	}
	out := make([]protocol.WorkerProfile, 0, len(profiles))
	found := false
	for _, p := range profiles {
		if strings.EqualFold(p.Name, name) {
			found = true
		} else {
			out = append(out, p)
		}
	}
	if !found {
		return reject("worker profile %q not found", name)
	}
	return s.saveWorkerProfiles(out)
}

// workerProfileAction verifies the requesting session and resolves new models
// against the advertised lists before changing the network roster.
func (g *Gateway) workerProfileAction(nodeID, action string, p protocol.WorkerProfileFrame, validate ...bool) (any, error) {
	// The roster is public, like charters: any caller but a worker may read
	// it, the CTO included. Changes also need a session on this machine.
	if action == "worker.profile.list" {
		if agent, ok, err := g.store.agentBase(p.Requester); err != nil {
			return nil, err
		} else if ok && agent.Worker != nil {
			return nil, reject("worker profiles require a non-worker session on this machine")
		}
		profiles, err := g.store.WorkerProfiles()
		if err != nil {
			return nil, err
		}
		nodes, err := g.nodesWithState()
		if err != nil {
			return nil, err
		}
		scores, err := g.store.scores()
		if err != nil {
			return nil, err
		}
		history := map[string]map[string]bool{}
		for _, p := range profiles {
			nodesWithRuns, err := g.store.profileSuccessNodes(p)
			if err != nil {
				return nil, err
			}
			history[p.Name] = nodesWithRuns
		}
		return withWorkerProfileAvailability(profiles, nodes, scores, history), nil
	}
	agent, ok, err := g.store.agentBase(p.Requester)
	if err != nil {
		return nil, err
	}
	if !ok || agent.NodeID != nodeID || agent.Worker != nil {
		return nil, reject("worker profiles require a non-worker session on this machine")
	}
	switch action {
	case "worker.profile.remove":
		if strings.TrimSpace(p.Name) == "" {
			return nil, reject("worker profile name is required")
		}
		if len(validate) > 0 && validate[0] {
			return nil, nil
		}
		if err := g.store.RemoveWorkerProfile(p.Name); err != nil {
			return nil, err
		}
		g.store.Event("worker_profile.remove", nodeID, p.Requester, "", p.Name)
		return map[string]bool{"removed": true}, nil
	case "worker.profile.add":
		harness := strings.ToLower(strings.TrimSpace(p.Harness))
		if !protocol.HarnessKnown(harness) {
			return nil, reject("unknown worker harness %q", harness)
		}
		model := strings.TrimSpace(p.Model)
		if model == "" || len(model) > 256 {
			return nil, reject("a model is required")
		}
		model, level := protocol.SplitModelLevel(model)
		effort := strings.ToLower(strings.TrimSpace(p.Effort))
		if level != "" && effort != "" && !strings.EqualFold(level, effort) {
			return nil, reject("conflicting model efforts")
		}
		if effort == "" {
			effort = strings.ToLower(level)
		}
		if effort != "" {
			_, valid := protocol.SplitModelLevel("model:" + effort)
			if valid == "" {
				return nil, reject("unknown effort %q", effort)
			}
		}
		nodes, err := g.store.Nodes()
		if err != nil {
			return nil, err
		}
		machines := cleanMachines(p.Machines)
		targets, err := g.profileTargets(nodes, harness, machines)
		if err != nil {
			return nil, err
		}
		var models, providers []string
		usage := map[string]int{}
		failed, succeeded := map[string]bool{}, map[string]bool{}
		providerSeen := map[string]bool{}
		seen := map[string]bool{}
		type liveList struct {
			node   protocol.NodeView
			models []string
		}
		var lists []liveList
		for _, node := range targets {
			live, err := g.liveModels(context.Background(), node, harness, "")
			if err != nil {
				return nil, reject("%v", err)
			}
			if len(live.Harnesses) == 0 {
				return nil, reject("machine %s did not answer with a live %s model list", node.Hostname, harness)
			}
			listed := live.Harnesses[0]
			if listed.Error != "" {
				return nil, reject("machine %s could not list %s models live: %s", node.Hostname, harness, listed.Error)
			}
			lists = append(lists, liveList{node: node, models: listed.Models})
			used, err := g.store.ModelUsage(node.NodeID, harness, protocol.ModelUsageDays*24*time.Hour)
			if err != nil {
				return nil, err
			}
			for id, n := range used {
				usage[id] += n
			}
			failures, err := g.store.FailedModels(node.NodeID, harness)
			if err != nil {
				return nil, err
			}
			for id := range failures {
				failed[id] = true
			}
			successes, err := g.store.SucceededModels(node.NodeID, harness)
			if err != nil {
				return nil, err
			}
			for id := range successes {
				succeeded[id] = true
			}
			for _, h := range node.Harnesses {
				if h.Name != harness {
					continue
				}
				for _, provider := range h.ModelProviders {
					if !providerSeen[provider] {
						providerSeen[provider] = true
						providers = append(providers, provider)
					}
				}
			}
			for _, m := range listed.Models {
				if !seen[m] {
					seen[m] = true
					models = append(models, m)
				}
			}
		}
		if len(models) == 0 {
			return nil, reject("no online machine lists models for %s", harness)
		}
		resolution, err := protocol.ResolveModelRanked(model, models, usage, providers, failed, succeeded)
		if err != nil {
			return nil, reject("model: %v", err)
		}
		resolved := resolution.Model
		for _, list := range lists {
			if found, _ := protocol.ResolveWorkerProfileModel(resolved, list.models); found == "" {
				msg := fmt.Sprintf("model %s is not listed by %s live", resolved, list.node.Hostname)
				if closest := protocol.ClosestModels(resolved, list.models, 3); len(closest) > 0 {
					msg += "; closest: " + strings.Join(closest, ", ")
				}
				return nil, reject("%s", msg)
			}
		}
		if p.Test {
			tested, err := g.testModel(context.Background(), targets[0], harness, resolved, effort)
			if err != nil {
				return nil, reject("%v", err)
			}
			if !tested.OK {
				return nil, reject("test start on %s failed: %s", targets[0].Hostname, tested.Output)
			}
		}
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = harness + " " + resolved
			if effort != "" {
				name += ":" + effort
			}
		}
		if len(name) > 300 || strings.ContainsAny(name, "\r\n\x00") {
			return nil, reject("bad profile name")
		}
		profile := protocol.WorkerProfile{Name: name, Harness: harness, Model: resolved, Effort: effort, Machines: machines, AddedBy: p.Requester, AddedAt: g.store.now().Unix()}
		if len(validate) > 0 && validate[0] {
			return nil, nil
		}
		if err := g.store.AddWorkerProfile(profile); err != nil {
			return nil, err
		}
		g.store.Event("worker_profile.add", nodeID, p.Requester, "", name)
		return profile, nil
	}
	return nil, reject("unknown worker profile action")
}

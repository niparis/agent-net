package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
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

func withWorkerProfileAvailability(profiles []protocol.WorkerProfile, nodes []protocol.NodeView) []protocol.WorkerProfile {
	out := append([]protocol.WorkerProfile(nil), profiles...)
	for i := range out {
		var checked []string
		var models []string
		seenMachines := map[string]bool{}
		seenModels := map[string]bool{}
		available := false
		known := false
		for _, node := range nodes {
			if !node.Online {
				continue
			}
			for _, harness := range node.Harnesses {
				if harness.Name != out[i].Harness {
					continue
				}
				known = true
				machine := node.Hostname
				if machine == "" {
					machine = node.NodeID
				}
				if machine != "" && !seenMachines[machine] {
					checked = append(checked, machine)
					seenMachines[machine] = true
				}
				for _, model := range harness.Models {
					if model == out[i].Model {
						available = true
					}
					if !seenModels[model] {
						models = append(models, model)
						seenModels[model] = true
					}
				}
			}
		}
		sort.Strings(checked)
		out[i].MachinesChecked = checked
		out[i].Available = nil
		out[i].Closest = nil
		if known {
			out[i].Available = &available
			if !available {
				out[i].Closest = protocol.ClosestModels(out[i].Model, models, 3)
			}
		}
	}
	return out
}

func (s *Store) saveWorkerProfiles(profiles []protocol.WorkerProfile) error {
	raw, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, workerProfilesKey, string(raw), s.now().UnixNano())
	return err
}
func (s *Store) AddWorkerProfile(p protocol.WorkerProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.WorkerProfiles()
	if err != nil {
		return err
	}
	for _, other := range profiles {
		if strings.EqualFold(other.Name, p.Name) {
			return reject("worker profile %q already exists", p.Name)
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
func (g *Gateway) workerProfileAction(nodeID, action string, p protocol.WorkerProfileFrame) (any, error) {
	agent, ok, err := g.store.Agent(p.Requester)
	if err != nil {
		return nil, err
	}
	if !ok || agent.NodeID != nodeID || agent.Worker != nil {
		return nil, reject("worker profiles require a non-worker session on this machine")
	}
	switch action {
	case "worker.profile.list":
		profiles, err := g.store.WorkerProfiles()
		if err != nil {
			return nil, err
		}
		nodes, err := g.store.Nodes()
		if err != nil {
			return nil, err
		}
		return withWorkerProfileAvailability(profiles, nodes), nil
	case "worker.profile.remove":
		if strings.TrimSpace(p.Name) == "" {
			return nil, reject("worker profile name is required")
		}
		if err := g.store.RemoveWorkerProfile(p.Name); err != nil {
			return nil, err
		}
		g.store.Event("worker_profile.remove", nodeID, p.Requester, "", p.Name)
		return map[string]bool{"removed": true}, nil
	case "worker.profile.add":
		harness := strings.ToLower(strings.TrimSpace(p.Harness))
		if !protocol.WorkerHarnessSupported(harness) {
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
		var models, providers []string
		usage := map[string]int{}
		failed, succeeded := map[string]bool{}, map[string]bool{}
		providerSeen := map[string]bool{}
		seen := map[string]bool{}
		for _, node := range nodes {
			for _, h := range node.Harnesses {
				if h.Name == harness {
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
					for _, provider := range h.ModelProviders {
						if !providerSeen[provider] {
							providerSeen[provider] = true
							providers = append(providers, provider)
						}
					}
					for _, m := range h.Models {
						if !seen[m] {
							seen[m] = true
							models = append(models, m)
						}
					}
				}
			}
		}
		if len(models) == 0 {
			return nil, reject("no machine reports models for %s", harness)
		}
		resolution, err := protocol.ResolveModelRanked(model, models, usage, providers, failed, succeeded)
		if err != nil {
			return nil, reject("model: %v", err)
		}
		resolved := resolution.Model
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
		profile := protocol.WorkerProfile{Name: name, Harness: harness, Model: resolved, Effort: effort, AddedBy: p.Requester, AddedAt: g.store.now().Unix()}
		if err := g.store.AddWorkerProfile(profile); err != nil {
			return nil, err
		}
		g.store.Event("worker_profile.add", nodeID, p.Requester, "", name)
		return profile, nil
	}
	return nil, reject("unknown worker profile action")
}

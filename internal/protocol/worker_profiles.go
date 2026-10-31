package protocol

import (
	"sort"
	"strings"
)

// WorkerProfile is a named model and harness in the network's worker roster.
type WorkerProfile struct {
	Name                string                             `json:"name"`
	Harness             string                             `json:"harness"`
	Model               string                             `json:"model"`
	Effort              string                             `json:"effort,omitempty"`
	AddedBy             string                             `json:"added_by"`
	AddedAt             int64                              `json:"added_at"`
	Available           *bool                              `json:"available,omitempty"`
	Closest             []string                           `json:"closest,omitempty"`
	MachinesChecked     []string                           `json:"machines_checked,omitempty"`
	MachineAvailability []WorkerProfileMachineAvailability `json:"machine_availability,omitempty"`
}

// WorkerProfileMachineAvailability describes how one online machine can run a
// registered worker profile.
type WorkerProfileMachineAvailability struct {
	Machine       string   `json:"machine"`
	Available     bool     `json:"available"`
	ResolvedModel string   `json:"resolved_model,omitempty"`
	Closest       []string `json:"closest,omitempty"`
	Ambiguous     []string `json:"ambiguous,omitempty"`
}

// ResolveWorkerProfileModel resolves a registered profile against one
// machine's model list. It prefers an exact ID. If the profile and a listed
// ID have different providers but the same name after the first slash, that
// listed ID is used when it is the only match. Ambiguous contains all listed
// IDs when more than one provider has that name.
func ResolveWorkerProfileModel(model string, models []string) (resolved string, ambiguous []string) {
	for _, candidate := range models {
		if candidate == model {
			return candidate, nil
		}
	}
	separator := strings.IndexByte(model, '/')
	if separator <= 0 || separator == len(model)-1 {
		return "", nil
	}
	provider, name := model[:separator], model[separator+1:]
	seen := map[string]bool{}
	for _, candidate := range models {
		i := strings.IndexByte(candidate, '/')
		if i <= 0 || i == len(candidate)-1 || candidate[:i] == provider || candidate[i+1:] != name || seen[candidate] {
			continue
		}
		seen[candidate] = true
		ambiguous = append(ambiguous, candidate)
	}
	sort.Strings(ambiguous)
	if len(ambiguous) == 1 {
		return ambiguous[0], nil
	}
	return "", ambiguous
}

type WorkerProfileRequest struct {
	Name    string `json:"name,omitempty"`
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}
type WorkerProfileFrame struct {
	WorkerProfileRequest
	Requester string `json:"requester"`
}

const (
	OpWorkerProfiles      = "worker_profiles"
	OpWorkerProfileAdd    = "worker_profile_add"
	OpWorkerProfileRemove = "worker_profile_remove"
)
